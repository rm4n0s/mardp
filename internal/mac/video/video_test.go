package video

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"
	"unsafe"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/internal/host"
	"github.com/rm4n0s/mardp/internal/mac/capture"
	"github.com/rm4n0s/mardp/internal/mac/vtenc"
)

// fakeEncoder records what it was asked; frame is its frame function.
// SubmitPattern records the pictures and answers picture n with
// statuses[n], 0 beyond them.
type fakeEncoder struct {
	opts     vtenc.Options
	frame    func(*vtenc.Frame)
	closed   int
	forced   int
	pictures []uint32
	statuses []int
}

func (e *fakeEncoder) SubmitPattern(n uint32) int {
	e.pictures = append(e.pictures, n)
	if int(n) < len(e.statuses) {
		return e.statuses[n]
	}
	return 0
}

func (e *fakeEncoder) SetBitrate(int) error { return nil }
func (e *fakeEncoder) ForceKeyFrame()       { e.forced++ }
func (e *fakeEncoder) SetSkip(bool)         {}
func (e *fakeEncoder) Close()               { e.closed++ }
func (e *fakeEncoder) Submitter() (fn, arg unsafe.Pointer) {
	return unsafe.Pointer(e), nil
}

// fakeCapture delivers frames through the encoder's frame function while it
// runs, and fails with err.
type fakeCapture struct {
	enc     *fakeEncoder
	cfg     host.ScreenConfig
	out     capture.VideoOutput
	closed  int // the encoder's closes seen while capturing
	err     error
	deliver []vtenc.Frame
}

func (c *fakeCapture) CaptureVideo(_ context.Context, cfg host.ScreenConfig, out capture.VideoOutput) error {
	c.cfg, c.out, c.closed = cfg, out, c.enc.closed
	for i := range c.deliver {
		c.enc.frame(&c.deliver[i])
	}
	return c.err
}

func TestCaptureVideo(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts Options
		err  error
	}{
		{"main", Options{Profile: vtenc.ProfileMain}, nil},
		{"constrained high", Options{}, nil},
		{"capture fails", Options{}, errors.New("TestCaptureFailed", "stopped")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			enc := &fakeEncoder{}
			captured := time.Now()
			c := &fakeCapture{enc: enc, err: tt.err, deliver: []vtenc.Frame{
				{Data: []byte{1}, Key: true, Captured: captured, Encoded: captured.Add(5 * time.Millisecond)}, {Data: []byte{2}}}}
			s := &Source{capture: c, opts: tt.opts, newEncoder: func(o vtenc.Options, frame func(*vtenc.Frame)) (encoder, error) {
				enc.opts, enc.frame = o, frame
				return enc, nil
			}}
			var got []host.EncodedFrame
			var startedWith host.VideoEncoder
			err := s.CaptureVideo(context.Background(), host.VideoConfig{Width: 1280, Height: 720, MaxFPS: 30, Bitrate: 5e6},
				func(e host.VideoEncoder) { startedWith = e },
				func(f *host.EncodedFrame) {
					got = append(got, host.EncodedFrame{Data: append([]byte(nil), f.Data...), Key: f.Key, Captured: f.Captured, Encoded: f.Encoded})
				})
			if tt.err != nil {
				assertRoute(t, err, "TestCaptureVideo.TestCaptureFailed")
			} else if err != nil {
				t.Fatal(err)
			}
			want := vtenc.Options{Width: 1280, Height: 720, FullRange: true, Profile: tt.opts.Profile, Bitrate: 5e6, FPS: 30}
			if enc.opts != want {
				t.Errorf("encoder options %+v", enc.opts)
			}
			if startedWith != enc || c.out.Encoder != enc || !c.out.FullRange {
				t.Error("the session or the capture did not get the encoder")
			}
			if c.cfg != (host.ScreenConfig{Width: 1280, Height: 720, MaxFPS: 30}) {
				t.Errorf("capture config %+v", c.cfg)
			}
			if c.closed != 0 || enc.closed != 1 {
				t.Errorf("closed %d times while capturing, %d in all", c.closed, enc.closed)
			}
			if len(got) != 2 || !got[0].Key || got[0].Data[0] != 1 || got[1].Key || got[1].Data[0] != 2 {
				t.Errorf("frames %+v", got)
			}
			if len(got) == 2 && (!got[0].Captured.Equal(captured) || got[0].Encoded.Sub(got[0].Captured) != 5*time.Millisecond ||
				!got[1].Captured.IsZero() || !got[1].Encoded.IsZero()) {
				t.Errorf("the frames' times %+v", got)
			}
		})
	}
}

// TestEncoderFails: an encoder that cannot be made ends the capture with
// its error, before anything starts.
func TestEncoderFails(t *testing.T) {
	c := &fakeCapture{enc: &fakeEncoder{}}
	s := &Source{capture: c, newEncoder: func(vtenc.Options, func(*vtenc.Frame)) (encoder, error) {
		return nil, errors.New("TestEncoderRefused", "no encoder")
	}}
	started := false
	err := s.CaptureVideo(context.Background(), host.VideoConfig{Width: 64, Height: 64}, func(host.VideoEncoder) { started = true }, func(*host.EncodedFrame) {})
	assertRoute(t, err, "TestEncoderFails.func1.TestEncoderRefused")
	if started || c.out.Encoder != nil {
		t.Error("a capture started without an encoder")
	}
}

// TestPattern: the test pattern needs no capture: each tick of the frame
// rate's clock submits the next picture to the encoder, a run of refused
// pictures is logged once, and the end of ctx ends it with the encoder
// closed.
func TestPattern(t *testing.T) {
	// Encoded twice, refused twice, skipped, refused twice.
	enc := &fakeEncoder{statuses: []int{0, 0, -12902, -12902, 1, -1, -1}}
	ticks := make(chan time.Time)
	var every time.Duration
	stopped := 0
	var logged bytes.Buffer
	s := NewPattern(Options{Logger: slog.New(slog.NewTextHandler(&logged, nil))})
	s.newEncoder = func(o vtenc.Options, frame func(*vtenc.Frame)) (encoder, error) {
		enc.opts, enc.frame = o, frame
		return enc, nil
	}
	s.tick = func(d time.Duration) (<-chan time.Time, func()) {
		every = d
		return ticks, func() { stopped++ }
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var started host.VideoEncoder
	go func() {
		done <- s.CaptureVideo(ctx, host.VideoConfig{Width: 1920, Height: 1080, MaxFPS: 60, Bitrate: 8e6},
			func(e host.VideoEncoder) { started = e }, func(*host.EncodedFrame) {})
	}()
	for range len(enc.statuses) {
		ticks <- time.Now()
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if every != time.Second/60 || stopped != 1 {
		t.Errorf("ticks every %v, stopped %d times", every, stopped)
	}
	want := []uint32{0, 1, 2, 3, 4, 5, 6}
	if len(enc.pictures) != len(want) {
		t.Fatalf("pictures %v, want %v", enc.pictures, want)
	}
	for i := range want {
		if enc.pictures[i] != want[i] {
			t.Fatalf("pictures %v, want %v", enc.pictures, want)
		}
	}
	if n := bytes.Count(logged.Bytes(), []byte("refused a picture")); n != 2 {
		t.Errorf("%d warnings for two runs of refused pictures:\n%s", n, logged.String())
	}
	if started != enc || enc.closed != 1 || enc.opts.Width != 1920 || enc.opts.FPS != 60 || !enc.opts.FullRange {
		t.Errorf("encoder %+v started %v", enc, started)
	}
}

// TestStill: the still pattern submits picture 0 at once and again for
// each forced key frame, through the encoder the session got, and nothing
// else; each refusal is logged; the end of ctx ends it with the encoder closed.
func TestStill(t *testing.T) {
	enc := &fakeEncoder{statuses: []int{-12902}}
	var logged bytes.Buffer
	s := NewPattern(Options{Still: true, Logger: slog.New(slog.NewTextHandler(&logged, nil))})
	submitted := make(chan struct{}, 10)
	s.newEncoder = func(o vtenc.Options, frame func(*vtenc.Frame)) (encoder, error) {
		enc.opts, enc.frame = o, frame
		return &submitSignal{fakeEncoder: enc, done: submitted}, nil
	}
	s.tick = func(time.Duration) (<-chan time.Time, func()) {
		t.Error("the still pattern made a clock")
		return nil, func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	startedWith := make(chan host.VideoEncoder, 1)
	go func() {
		done <- s.CaptureVideo(ctx, host.VideoConfig{Width: 1920, Height: 1080, MaxFPS: 60, Bitrate: 8e6},
			func(e host.VideoEncoder) { startedWith <- e }, func(*host.EncodedFrame) {})
	}()
	e := <-startedWith
	<-submitted
	e.ForceKeyFrame()
	<-submitted
	e.ForceKeyFrame()
	<-submitted
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(enc.pictures) != 3 || enc.pictures[0] != 0 || enc.pictures[1] != 0 || enc.pictures[2] != 0 || enc.forced != 2 {
		t.Errorf("pictures %v, %d key frames forced", enc.pictures, enc.forced)
	}
	if n := bytes.Count(logged.Bytes(), []byte("refused the still test pattern")); n != 3 {
		t.Errorf("%d warnings for three refused pictures:\n%s", n, logged.String())
	}
	if enc.closed != 1 {
		t.Errorf("the encoder was closed %d times", enc.closed)
	}
}

// submitSignal tells done of each picture submitted.
type submitSignal struct {
	*fakeEncoder
	done chan struct{}
}

func (e *submitSignal) SubmitPattern(n uint32) int {
	st := e.fakeEncoder.SubmitPattern(n)
	e.done <- struct{}{}
	return st
}

// TestNew: New's encoder is VideoToolbox's, which refuses a bad size on
// every system.
func TestNew(t *testing.T) {
	s := New(nil, Options{})
	_, err := s.newEncoder(vtenc.Options{Width: 0, Height: 1}, func(*vtenc.Frame) {})
	if err == nil {
		t.Fatal("an encoder 0 pixels wide")
	}
	appErr, ok := errors.FromError(err)
	if !ok || (appErr.Tag != vtenc.TagEncoderConfigInvalid && appErr.Tag != vtenc.TagEncoderUnsupported) {
		t.Errorf("error %v", err)
	}
}

func assertRoute(t *testing.T, err error, route string) {
	t.Helper()
	appErr, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("expected *errors.Error, got %T (%v)", err, err)
	}
	if !appErr.HasRoute(route) {
		t.Errorf("route %s, want %s", appErr.Route(), route)
	}
}
