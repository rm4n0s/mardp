//go:build darwin && mardp_freerdp

package main

// The proof of step 3.9 (SPEC §9.3, §15.3): FreeRDP in the test machine
// takes the H.264 path. The test serves color bars itself, through the
// host's pipeline and VideoToolbox, so it needs no screen capture and no
// permission:
//
//	scripts/vm.sh start
//	go test -tags mardp_freerdp -run FreeRDPAVC -v ./cmd/mardp/
//	scripts/vm.sh stop

import (
	"bytes"
	"context"
	"image/png"
	"log/slog"
	"math"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/mardp/internal/host"
	"github.com/rm4n0s/mardp/internal/mac/vtenc"
	"github.com/rm4n0s/mardp/rdp"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// bars are the colors of the bars, left to right.
var bars = [][3]float64{
	{255, 255, 255}, {255, 255, 0}, {0, 255, 255}, {0, 255, 0},
	{255, 0, 255}, {255, 0, 0}, {0, 0, 255}, {0, 0, 0},
}

// ycbcr is BT.709's Y'CbCr of an RGB color, full or video range.
func ycbcr(c [3]float64, full bool) (y, cb, cr uint8) {
	r, g, b := c[0]/255, c[1]/255, c[2]/255
	ly := 0.2126*r + 0.7152*g + 0.0722*b
	pb, pr := (b-ly)/1.8556, (r-ly)/1.5748
	clamp := func(v float64) uint8 { return uint8(math.Max(0, math.Min(255, math.Round(v)))) }
	if full {
		return clamp(255 * ly), clamp(128 + 255*pb), clamp(128 + 255*pr)
	}
	return clamp(16 + 219*ly), clamp(128 + 224*pb), clamp(128 + 224*pr)
}

// barsNV12 is the NV12 picture of the bars at w×h: 8 bars of equal width,
// whose edges fall on even columns.
func barsNV12(w, h int, full bool) (y, uv []byte) {
	y, uv = make([]byte, w*h), make([]byte, w*(h/2))
	bw := w / len(bars) &^ 1
	for col := 0; col < w; col += 2 {
		i := min(col/bw, len(bars)-1)
		ly, cb, cr := ycbcr(bars[i], full)
		for row := range h {
			y[row*w+col], y[row*w+col+1] = ly, ly
		}
		for row := range h / 2 {
			uv[row*w+col], uv[row*w+col+1] = cb, cr
		}
	}
	return y, uv
}

// barSource is a host.VideoSource of the bars through VideoToolbox.
type barSource struct {
	profile vtenc.Profile
	full    bool

	encoded, skipped, sent atomic.Int64
	mu                     sync.Mutex
	err                    error
}

func (b *barSource) CaptureVideo(ctx context.Context, cfg host.VideoConfig, started func(host.VideoEncoder), frame func(*host.EncodedFrame)) error {
	var out host.EncodedFrame
	enc, err := vtenc.New(vtenc.Options{Width: cfg.Width, Height: cfg.Height, FullRange: b.full, Profile: b.profile,
		Bitrate: cfg.Bitrate, FPS: cfg.MaxFPS}, func(f *vtenc.Frame) {
		out.Data, out.Key = f.Data, f.Key
		b.sent.Add(1)
		frame(&out)
	})
	if err != nil {
		return err
	}
	defer enc.Close()
	started(enc)
	y, uv := barsNV12(cfg.Width, cfg.Height, b.full)
	t := time.NewTicker(time.Second / time.Duration(cfg.MaxFPS))
	defer t.Stop()
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		switch st := enc.SubmitNV12(y, uv, time.Since(start)); st {
		case 0:
			b.encoded.Add(1)
		case 1:
			b.skipped.Add(1)
		default:
			b.mu.Lock()
			b.err = errors.New("TestSubmitFailed", "VideoToolbox refused a picture", "status", st)
			b.mu.Unlock()
		}
	}
}

// serveHost serves a host of parts with opts on addr until the test ends,
// with the library's server and logging to logPath; it returns the port.
func serveHost(t *testing.T, addr string, parts host.Parts, opts host.Options, logPath string) int {
	t.Helper()
	lf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(lf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	opts.Logger = log
	hst, err := host.New(parts, opts)
	if err != nil {
		t.Fatal(err)
	}
	cert, _, err := rdptest.Certificate()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := rdp.NewServer(rdp.Config{Certificate: cert, Authenticator: rdp.StaticAuthenticator(testUser, testPassword),
		Handler: hst, DesktopSize: hst.DesktopSize, AudioFormats: hst.AudioFormats(), Clipboard: hst.Clipboard(), Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", net.JoinHostPort(addr, "0"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Serve did not return")
		}
		if err := lf.Close(); err != nil {
			t.Error(err)
		}
	})
	return l.Addr().(*net.TCPAddr).Port
}

// barError is the largest difference, over the bars and the channels,
// between the screenshot at path and the bars, measured at each bar's
// middle; it also returns the colors seen.
func barError(t *testing.T, path string) (float64, [][3]float64) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }() // read only
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	bw := b.Dx() / len(bars) &^ 1
	var worst float64
	var seen [][3]float64
	for i, want := range bars {
		cx, cy := b.Min.X+i*bw+bw/2, b.Min.Y+b.Dy()/2
		var sum [3]float64
		n := 0
		for y := cy - 8; y <= cy+8; y++ {
			for x := cx - 8; x <= cx+8; x++ {
				r, g, bl, _ := img.At(x, y).RGBA()
				sum[0], sum[1], sum[2] = sum[0]+float64(r>>8), sum[1]+float64(g>>8), sum[2]+float64(bl>>8)
				n++
			}
		}
		got := [3]float64{sum[0] / float64(n), sum[1] / float64(n), sum[2] / float64(n)}
		seen = append(seen, got)
		for c := range 3 {
			worst = math.Max(worst, math.Abs(got[c]-want[c]))
		}
	}
	return worst, seen
}

// decodeErrors are the errors FreeRDP logs from its codecs, its drawing
// and its channels, the graphics pipeline's among them.
var decodeErrors = regexp.MustCompile(`(?m)^.*\[ERROR\]\[com\.freerdp\.(codec|gdi|channels)[^\]]*\].*$`)

// avcFrames counts the AVC420 surface commands FreeRDP got.
var avcFrames = regexp.MustCompile(`Got GFX RDPGFX_CODECID_AVC420`)

func TestFreeRDPAVC(t *testing.T) {
	addr := requireVM(t)
	type result struct {
		name   string
		worst  float64
		seen   [][3]float64
		frames int64
	}
	var results []result
	for _, tt := range []struct {
		name    string
		profile vtenc.Profile
		full    bool
	}{
		{"constrained high, full range", vtenc.ProfileConstrainedHigh, true},
		{"constrained high, video range", vtenc.ProfileConstrainedHigh, false},
		{"main, full range", vtenc.ProfileMain, true},
		{"constrained baseline, full range", vtenc.ProfileConstrainedBaseline, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := logDir(t)
			src := &barSource{profile: tt.profile, full: tt.full}
			logPath := filepath.Join(dir, "mardp.log")
			port := serveHost(t, addr, host.Parts{Screen: &host.PatternScreen{}, Video: src}, host.Options{VideoFPS: 30}, logPath)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			out, err := vm(ctx, "sh", shotScript(addr, port, "/gfx:AVC420"))
			for _, f := range []string{"freerdp.log", "shot.png"} {
				if _, perr := vm(ctx, "pull", guestDir+"/"+f, filepath.Join(dir, f)); perr != nil {
					t.Logf("pulling %s: %v", f, perr)
				}
			}
			if err != nil {
				t.Fatalf("the guest's script: %v\n%s", err, out)
			}
			mlog, _ := os.ReadFile(logPath) // written by the test itself
			if !strings.Contains(out, "window ") || strings.Contains(out, "window \n") {
				t.Fatalf("xfreerdp showed no window:\n%s\nmardp's log:\n%s", out, mlog)
			}
			if !bytes.Contains(mlog, []byte("path=avc420")) {
				t.Errorf("the session did not take the H.264 path:\n%s", mlog)
			}
			src.mu.Lock()
			if src.err != nil {
				t.Error(src.err)
			}
			src.mu.Unlock()
			// The client acknowledges: without it the flow control would stop
			// after 3 frames. 5 s at 30 frames a second.
			if n := src.sent.Load(); n < 100 {
				t.Errorf("%d frames sent (%d encoded, %d skipped): the client does not acknowledge them", n, src.encoded.Load(), src.skipped.Load())
			}
			flog, _ := os.ReadFile(filepath.Join(dir, "freerdp.log")) // pulled above, or missing
			if bad := decodeErrors.FindAll(flog, 5); len(bad) > 0 {
				t.Errorf("FreeRDP's decoder failed:\n%s", bytes.Join(bad, []byte("\n")))
			}
			if n := len(avcFrames.FindAll(flog, -1)); n < 100 {
				t.Errorf("FreeRDP got %d AVC420 frames", n)
			}
			worst, seen := barError(t, filepath.Join(dir, "shot.png"))
			results = append(results, result{tt.name, worst, seen, src.sent.Load()})
			t.Logf("largest difference %.1f; bars seen %v; %d frames sent, %d skipped; files in %s",
				worst, seen, src.sent.Load(), src.skipped.Load(), dir)
			if tt.full && worst > barTolerance {
				t.Errorf("the bars are off by %.1f, more than %d", worst, barTolerance)
			}
		})
	}
	for _, r := range results {
		t.Logf("%-34s largest difference %5.1f, %d frames", r.name, r.worst, r.frames)
	}
}

// barTolerance is the largest difference of a channel allowed between a
// bar and its color: the rounding of 4:2:0 and of two conversions.
const barTolerance = 12
