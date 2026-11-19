package capture

import (
	"context"
	"testing"
	"time"
	"unsafe"

	"github.com/rm4n0s/mardp/internal/host"
)

// fakeEncoder is a VideoSink whose submit function and argument are two
// addresses the test can recognize.
type fakeEncoder struct {
	fn, arg byte
	none    bool // Submitter returns no function
}

func (e *fakeEncoder) Submitter() (fn, arg unsafe.Pointer) {
	if e.none {
		return nil, nil
	}
	return unsafe.Pointer(&e.fn), unsafe.Pointer(&e.arg)
}

func TestCaptureVideo(t *testing.T) {
	f := newFakeMac("M", map[string]uint32{"M": 1})
	s := newSource(Options{}, f)
	s.wait, s.retry = 20*time.Millisecond, time.Millisecond
	enc := &fakeEncoder{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- s.CaptureVideo(ctx, host.ScreenConfig{Width: 1280, Height: 720}, VideoOutput{Encoder: enc, FullRange: true})
	}()
	st := f.next(t)
	want := streamMode{video: true, fullRange: true, fn: unsafe.Pointer(&enc.fn), arg: unsafe.Pointer(&enc.arg)}
	if st.mode != want {
		t.Errorf("stream mode %+v, want %+v", st.mode, want)
	}
	if st.cfg != (host.ScreenConfig{Width: 1280, Height: 720, MaxFPS: DefaultVideoFPS}) {
		t.Errorf("stream config %+v", st.cfg)
	}
	if st.sk.frame != nil {
		t.Error("a video stream with a frame function")
	}
	// The display goes away: a new stream of the same mode follows it, and
	// the counts of both streams add up.
	f.set(func(f *fakeMac) { st.stats = VideoStats{Encoded: 100, Skipped: 7, Refused: 1} })
	st.sk.stop(errNoCaptureSource, "gone")
	st2 := f.next(t)
	if st2.mode != want {
		t.Errorf("second stream mode %+v", st2.mode)
	}
	f.set(func(f *fakeMac) { st2.stats = VideoStats{Encoded: 20, Skipped: 3} })
	cancel()
	if err := wait(t, done); err != nil {
		t.Fatal(err)
	}
	if v := s.VideoStats(); v != (VideoStats{Encoded: 120, Skipped: 10, Refused: 1}) {
		t.Errorf("stats %+v", v)
	}
	if _, live := f.state(); live != 0 {
		t.Errorf("%d streams left running", live)
	}
}

func TestCaptureVideoErrors(t *testing.T) {
	f := newFakeMac("M", map[string]uint32{"M": 1})
	s := newSource(Options{}, f)
	ok := host.ScreenConfig{Width: 64, Height: 64}
	for _, tt := range []struct {
		name string
		cfg  host.ScreenConfig
		out  VideoOutput
	}{
		{"no encoder", ok, VideoOutput{}},
		{"no submit function", ok, VideoOutput{Encoder: &fakeEncoder{none: true}}},
		{"too small", host.ScreenConfig{Width: 1, Height: 64}, VideoOutput{Encoder: &fakeEncoder{}}},
		{"too large", host.ScreenConfig{Width: 64, Height: host.MaxDesktopSide + 1}, VideoOutput{Encoder: &fakeEncoder{}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, s.CaptureVideo(t.Context(), tt.cfg, tt.out), "Source.CaptureVideo.CaptureConfigInvalid")
		})
	}
	f.set(func(f *fakeMac) { f.denied = true })
	assertRoute(t, s.CaptureVideo(t.Context(), ok, VideoOutput{Encoder: &fakeEncoder{}}),
		"Source.CaptureVideo->Source.run.CapturePermissionDenied")
}
