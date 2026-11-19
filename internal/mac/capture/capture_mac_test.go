//go:build darwin && mardp_mac

package capture

import (
	"context"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/internal/host"
	"github.com/rm4n0s/mardp/internal/mac/vtenc"
)

// The main goroutine stays on the main thread, which runs the run loop that
// AppKit needs for the tests' window while the tests run on other
// goroutines.
func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	code := 0
	go func() {
		code = m.Run()
		stopMainLoop()
	}()
	runMainLoop()
	os.Exit(code)
}

// These tests use ScreenCaptureKit. They never ask for Screen Recording:
// without it the ones that need frames are skipped, so no question shows.
// scripts/mactest.sh runs them as a signed app that has the permission.

// needScreen skips t without Screen Recording.
func needScreen(t *testing.T) {
	t.Helper()
	if !(macOS{}).granted() {
		t.Skip("Screen Recording is not granted to this process (run it with scripts/mactest.sh)")
	}
}

// size is the main display fitted into 1280×720, as a session would ask.
func size(t *testing.T, s *Source) host.ScreenConfig {
	t.Helper()
	d, err := s.Display(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("display %+v", d)
	scale := min(1, 1280/float64(d.Width), 720/float64(d.Height))
	return host.ScreenConfig{Width: int(float64(d.Width)*scale) &^ 1, Height: int(float64(d.Height)*scale) &^ 1, MaxFPS: 30}
}

func TestMacWithoutPermission(t *testing.T) {
	if (macOS{}).granted() {
		t.Skip("Screen Recording is granted")
	}
	s, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Capture(t.Context(), host.ScreenConfig{Width: 640, Height: 360}, func(*host.Frame) {})
	assertRoute(t, err, "Source.Capture->Source.run.CapturePermissionDenied")
}

func TestMacFrames(t *testing.T) {
	needScreen(t)
	s, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := size(t, s)
	type got struct {
		at    time.Time
		whole bool
		n     int
		ok    bool // every rectangle inside the frame
		alpha bool // the pixels are opaque
		color bool // something is not black
	}
	var mu sync.Mutex
	var frames []got
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- s.Capture(ctx, cfg, func(f *host.Frame) {
			g := got{at: time.Now(), whole: f.Dirty == nil, n: len(f.Dirty), ok: true, alpha: true}
			if f.Image.Width != cfg.Width || f.Image.Height != cfg.Height {
				g.ok = false
			}
			for _, r := range f.Dirty {
				if r.Empty() || r.Min.X < 0 || r.Min.Y < 0 || r.Max.X > cfg.Width || r.Max.Y > cfg.Height {
					g.ok = false
				}
			}
			for y := 0; y < f.Image.Height; y += 7 {
				row := f.Image.Pix[y*f.Image.Stride:]
				for x := 0; x < f.Image.Width; x += 7 {
					p := row[4*x : 4*x+4]
					if p[3] != 0xff {
						g.alpha = false
					}
					if p[0]|p[1]|p[2] != 0 {
						g.color = true
					}
				}
			}
			mu.Lock()
			frames = append(frames, g)
			mu.Unlock()
		})
	}()
	time.Sleep(3 * time.Second)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(frames) == 0 {
		t.Fatal("no frame")
	}
	first := frames[0]
	t.Logf("first frame after %v; %d frames in 3 s", first.at.Sub(start), len(frames))
	if !first.whole || !first.alpha || !first.color || !first.ok {
		t.Errorf("first frame %+v", first)
	}
	if first.at.Sub(start) > 2*time.Second {
		t.Errorf("the first frame took %v", first.at.Sub(start))
	}
	// After the first, frames come only when the screen changed, each with
	// its rectangles; on a still screen there are none.
	for i, g := range frames[1:] {
		if g.whole || g.n == 0 || !g.ok {
			t.Errorf("frame %d: %+v", i+1, g)
		}
	}
	if len(frames) > 3*cfg.MaxFPS {
		t.Errorf("%d frames in 3 s at %d fps", len(frames), cfg.MaxFPS)
	}
}

func TestMacStillScreen(t *testing.T) {
	needScreen(t)
	s, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := size(t, s)
	// The owner's screen is not still while they work, so the test captures
	// its own still window alone (a display filter would report the
	// display's changes even with only this window in it).
	w := openTestWindow()
	defer w.close()
	time.Sleep(200 * time.Millisecond) // ScreenCaptureKit lists it
	s.onlyWindow = w.id()
	var mu sync.Mutex
	frames, whole := 0, 0
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err = s.Capture(ctx, cfg, func(f *host.Frame) {
		mu.Lock()
		defer mu.Unlock()
		frames++
		if f.Dirty == nil {
			whole++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Logf("%d frames in 3 s of a still window, %d whole", frames, whole)
	if frames == 0 || whole == 0 {
		t.Error("no whole first frame")
	}
	if frames > 3 {
		t.Errorf("%d frames in 3 s of a still window", frames)
	}
}

func TestMacStopThousand(t *testing.T) {
	needScreen(t)
	s, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := size(t, s)
	const runs = 1000
	var late, framesSeen atomic.Int64
	begin := time.Now()
	for i := range runs {
		var over atomic.Bool
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			done <- s.Capture(ctx, cfg, func(*host.Frame) {
				framesSeen.Add(1)
				if over.Load() {
					late.Add(1)
				}
			})
		}()
		// Some runs stop at once, some after a frame or two.
		if i%10 == 0 {
			time.Sleep(50 * time.Millisecond)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		over.Store(true)
	}
	time.Sleep(200 * time.Millisecond)
	t.Logf("%d starts and stops in %v, %d frames", runs, time.Since(begin), framesSeen.Load())
	if late.Load() != 0 {
		t.Errorf("%d callbacks after Capture returned", late.Load())
	}
}

func TestMacVideo(t *testing.T) {
	needScreen(t)
	s, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := size(t, s)
	cfg.MaxFPS = 60
	var frames, keys, bad atomic.Int64
	var first atomic.Bool
	first.Store(true)
	enc, err := vtenc.New(vtenc.Options{Width: cfg.Width, Height: cfg.Height, FullRange: true, FPS: 60}, func(f *vtenc.Frame) {
		frames.Add(1)
		if f.Key {
			keys.Add(1)
		}
		if first.Swap(false) && !f.Key {
			bad.Add(1)
		}
		if len(f.Data) < 5 || f.Data[0] != 0 || f.Data[1] != 0 || f.Data[2] != 0 || f.Data[3] != 1 {
			bad.Add(1)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	w := openTestWindow()
	defer w.close()
	// Only the test's window is captured: the rest of the screen need not
	// be still (the owner's apps may be drawing), and a display filter
	// would report its changes even with only this window in it.
	time.Sleep(200 * time.Millisecond) // ScreenCaptureKit lists it
	s.onlyWindow = w.id()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.CaptureVideo(ctx, cfg, VideoOutput{Encoder: enc, FullRange: true}) }()
	// count returns the frames encoded during d.
	count := func(d time.Duration) int64 {
		n := frames.Load()
		time.Sleep(d)
		return frames.Load() - n
	}
	w.animate(true)
	time.Sleep(time.Second) // the stream starts, the capture indicator settles
	moving := count(2 * time.Second)
	enc.SetSkip(true)
	time.Sleep(100 * time.Millisecond)
	skipping := count(time.Second)
	enc.SetSkip(false)
	resumed := count(time.Second)
	w.animate(false)
	time.Sleep(time.Second)
	still := count(2 * time.Second)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	enc.Close()
	v, es := s.VideoStats(), enc.Stats()
	t.Logf("frames encoded: %d in 2 s moving, %d in 1 s skipping, %d in 1 s after, %d in 2 s still; capture %+v; encoder %+v",
		moving, skipping, resumed, still, v, es)
	if moving < 60 {
		t.Errorf("%d frames in 2 s of motion", moving)
	}
	if skipping != 0 {
		t.Errorf("%d frames encoded while skipping", skipping)
	}
	if resumed < 20 {
		t.Errorf("%d frames in 1 s after skipping", resumed)
	}
	// ScreenCaptureKit sends the odd frame of an unchanged window (one in
	// 2 s here); a picture that changes would give 120.
	if still > 2 {
		t.Errorf("%d frames encoded in 2 s of a still window", still)
	}
	// One crossing into Go a frame: the encoder's output. The capture
	// makes none.
	if v.Crossings != 0 {
		t.Errorf("the capture crossed into Go %d times", v.Crossings)
	}
	if outputs := es.Frames + es.Dropped + es.Failed; outputs != v.Encoded || es.Frames != frames.Load() {
		t.Errorf("%d frames went to the encoder, %d came out to Go (%d delivered)", v.Encoded, outputs, frames.Load())
	}
	if v.Skipped == 0 || v.Refused != 0 || es.Failed != 0 {
		t.Errorf("capture %+v, encoder %+v", v, es)
	}
	if keys.Load() != 1 || bad.Load() != 0 {
		t.Errorf("%d key frames, %d bad frames", keys.Load(), bad.Load())
	}
}
