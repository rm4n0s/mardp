package video

import (
	"context"
	"testing"
	"unsafe"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/internal/host"
	"github.com/rm4n0s/mardp/internal/mac/capture"
	"github.com/rm4n0s/mardp/internal/mac/vtenc"
)

// fakeEncoder records what it was asked; frame is its frame function.
type fakeEncoder struct {
	opts   vtenc.Options
	frame  func(*vtenc.Frame)
	closed int
	forced int
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
			c := &fakeCapture{enc: enc, err: tt.err, deliver: []vtenc.Frame{{Data: []byte{1}, Key: true}, {Data: []byte{2}}}}
			s := &Source{capture: c, opts: tt.opts, newEncoder: func(o vtenc.Options, frame func(*vtenc.Frame)) (encoder, error) {
				enc.opts, enc.frame = o, frame
				return enc, nil
			}}
			var got []host.EncodedFrame
			var startedWith host.VideoEncoder
			err := s.CaptureVideo(context.Background(), host.VideoConfig{Width: 1280, Height: 720, MaxFPS: 30, Bitrate: 5e6},
				func(e host.VideoEncoder) { startedWith = e },
				func(f *host.EncodedFrame) {
					got = append(got, host.EncodedFrame{Data: append([]byte(nil), f.Data...), Key: f.Key})
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
