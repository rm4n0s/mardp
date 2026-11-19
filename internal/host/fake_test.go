package host

import (
	"bytes"
	"context"
	"image"
	"math"
	"slices"
	"testing"
	"time"
)

func TestPatternScreen(t *testing.T) {
	s := &PatternScreen{Frames: 4}
	d, err := s.Display(context.Background())
	if err != nil || d.Width != 1920 || d.PointHeight != 1080 || !d.Main {
		t.Fatalf("Display = %+v, %v", d, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const w, h = 320, 200
	shadow := solid(w, h, 0)
	want := solid(w, h, 0)
	frames := 0
	done := make(chan error, 1)
	go func() {
		done <- s.Capture(ctx, ScreenConfig{Width: w, Height: h, MaxFPS: 200}, func(f *Frame) {
			dirty := f.Dirty
			if frames == 0 && dirty != nil {
				t.Error("the first frame is not all of it")
			}
			if dirty == nil {
				dirty = []image.Rectangle{image.Rect(0, 0, w, h)}
			}
			for _, r := range dirty {
				copyRect(shadow, f.Image, r)
			}
			PatternFrame(want, frames)
			if !bytes.Equal(shadow.Pix, want.Pix) {
				t.Errorf("frame %d: the changed rectangles do not make the pattern", frames)
			}
			if frames++; frames == s.Frames {
				cancel()
			}
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Capture did not return")
	}
	if frames != 4 {
		t.Errorf("%d frames, want 4", frames)
	}
}

func TestPatternSquare(t *testing.T) {
	tests := []struct {
		w, h, n int
		want    image.Rectangle
	}{
		{320, 200, 0, image.Rect(0, 68, 64, 132)},
		{320, 200, 2, image.Rect(16, 68, 80, 132)},
		{320, 200, 33, image.Rect(7, 68, 71, 132)}, // wrapped: 264 % 257
		{40, 30, 5, image.Rect(7, 0, 37, 30)},      // 40 % 11
	}
	for _, tt := range tests {
		if got := patternSquare(tt.w, tt.h, tt.n); got != tt.want {
			t.Errorf("patternSquare(%d, %d, %d) = %v, want %v", tt.w, tt.h, tt.n, got, tt.want)
		}
	}
}

func TestToneSource(t *testing.T) {
	s := &ToneSource{Frequency: 1000}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var times []time.Duration
	var first []float32
	err := s.Capture(ctx, func(b *SoundBuffer) {
		if len(b.Left) != 960 || len(b.Right) != 960 {
			t.Fatalf("buffer of %d samples, want 960", len(b.Left))
		}
		if first == nil {
			first = slices.Clone(b.Left)
		}
		times = append(times, b.Time)
		if len(times) == 3 {
			cancel()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(times, []time.Duration{0, 20 * time.Millisecond, 40 * time.Millisecond}) {
		t.Errorf("times %v", times)
	}
	// 1 kHz at 48 kHz: a quarter period is 12 samples.
	if first[0] != 0 || math.Abs(float64(first[12])-0.25) > 1e-6 {
		t.Errorf("samples 0 and 12 are %v and %v", first[0], first[12])
	}
}

// TestQuietSource: nothing is delivered, and the end of ctx ends the
// capture without an error.
func TestQuietSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	got := 0
	if err := (QuietSource{}).Capture(ctx, func(*SoundBuffer) { got++ }); err != nil || got != 0 {
		t.Errorf("err %v, %d buffers", err, got)
	}
}

func TestFakeSystem(t *testing.T) {
	s := NewFakeSystem([]Display{{ID: "a"}}, ScreenRecording)
	if !s.Granted(ScreenRecording) || s.Granted(Accessibility) {
		t.Error("granted permissions")
	}
	s.Grant(Accessibility, true)
	s.Grant(ScreenRecording, false)
	if s.Granted(ScreenRecording) || !s.Granted(Accessibility) {
		t.Error("Grant")
	}
	if err := s.Request(Accessibility); err != nil {
		t.Fatal(err)
	}
	if err := s.OpenSettings(context.Background(), ScreenRecording); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.Requests(), []Permission{Accessibility}) || !slices.Equal(s.Opened(), []Permission{ScreenRecording}) {
		t.Error("requests and panes")
	}
	ds, err := s.Displays()
	if err != nil || len(ds) != 1 || ds[0].ID != "a" {
		t.Errorf("Displays = %v, %v", ds, err)
	}
	r1, _ := s.KeepAwake()
	r2, _ := s.KeepAwake()
	r1()
	r1()
	if s.Awake() != 1 {
		t.Errorf("Awake = %d, want 1", s.Awake())
	}
	r2()
	if s.Awake() != 0 {
		t.Errorf("Awake = %d, want 0", s.Awake())
	}
}
