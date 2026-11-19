//go:build darwin && mardp_mac

package capture

import (
	"context"
	"image"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/internal/host"
)

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
	assertRoute(t, err, "Source.Capture.CapturePermissionDenied")
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
	// While a stream runs, macOS animates its capture indicator in the menu
	// bar: those frames change only the top of the screen. Frames that
	// change anything below it mean the screen was not still.
	menuBar := cfg.Height / 30
	var mu sync.Mutex
	frames, top := 0, 0
	var below image.Rectangle
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err = s.Capture(ctx, cfg, func(f *host.Frame) {
		mu.Lock()
		defer mu.Unlock()
		frames++
		if f.Dirty == nil {
			return
		}
		onlyTop := true
		for _, r := range f.Dirty {
			if r.Max.Y > menuBar {
				onlyTop = false
				below = below.Union(r)
			}
		}
		if onlyTop {
			top++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Logf("%d frames in 3 s: the whole first, %d that change only the top %d rows, %d other (changed %v)",
		frames, top, menuBar, frames-1-top, below)
	if frames == 0 {
		t.Error("no frame")
	}
	if frames > 3*cfg.MaxFPS/2 {
		t.Errorf("%d frames in 3 s: more than half the cap on a nearly still screen", frames)
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
