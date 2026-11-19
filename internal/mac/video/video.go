// Package video is the host's VideoSource on macOS (SPEC §4.2): a capture
// of the display in NV12 (internal/mac/capture) straight into a hardware
// H.264 encoder (internal/mac/vtenc), made anew for each capture with the
// session's size, frame rate and bitrate. The frames are full range BT.709,
// which FreeRDP shows right (SPEC §9.3).
package video

import (
	"context"
	"log/slog"
	"time"

	"github.com/rm4n0s/mardp/internal/host"
	"github.com/rm4n0s/mardp/internal/mac/capture"
	"github.com/rm4n0s/mardp/internal/mac/vtenc"
)

// Options configure a Source.
type Options struct {
	// Profile is the H.264 profile of the stream.
	Profile vtenc.Profile
	// Logger receives the log; nil discards it.
	Logger *slog.Logger
}

// capturer is what a Source needs of the capture.
type capturer interface {
	CaptureVideo(ctx context.Context, cfg host.ScreenConfig, out capture.VideoOutput) error
}

// encoder is what a Source needs of an encoder.
type encoder interface {
	host.VideoEncoder
	capture.VideoSink
	// SubmitPattern encodes picture n of the test pattern.
	SubmitPattern(n uint32) int
	Close()
}

// newEncoderFunc makes an encoder; vtenc.New, or a fake in the tests.
type newEncoderFunc func(opts vtenc.Options, frame func(*vtenc.Frame)) (encoder, error)

// Source implements host.VideoSource.
type Source struct {
	// capture is the capture of the display; nil for the test pattern.
	capture    capturer
	newEncoder newEncoderFunc
	opts       Options
	// tick makes the test pattern's clock; time.NewTicker, or a fake in
	// the tests.
	tick func(d time.Duration) (<-chan time.Time, func())
}

var _ host.VideoSource = (*Source)(nil)

// New makes a Source that captures with c.
func New(c *capture.Source, opts Options) *Source {
	return &Source{capture: c, opts: opts, newEncoder: newVT}
}

// NewPattern makes a Source of a moving test pattern instead of the
// display, drawn into the encoder's buffers at the frame rate's cap and
// encoded by VideoToolbox as the display would be (`mardp -test-pattern`,
// SPEC §15.5). It needs no permission.
func NewPattern(opts Options) *Source {
	return &Source{opts: opts, newEncoder: newVT, tick: func(d time.Duration) (<-chan time.Time, func()) {
		t := time.NewTicker(d)
		return t.C, t.Stop
	}}
}

// newVT makes a VideoToolbox encoder.
func newVT(o vtenc.Options, frame func(*vtenc.Frame)) (encoder, error) {
	e, err := vtenc.New(o, frame)
	if err != nil {
		return nil, err
	}
	return e, nil
}

// CaptureVideo implements host.VideoSource: it makes an encoder for cfg,
// hands it to started, and captures into it until ctx ends or the capture
// fails; frame gets each encoded frame on a thread of VideoToolbox, its
// data valid during the call. The encoder is closed after the capture
// stops.
func (s *Source) CaptureVideo(ctx context.Context, cfg host.VideoConfig, started func(host.VideoEncoder), frame func(*host.EncodedFrame)) error {
	// The calls come one at a time, so one EncodedFrame serves them all.
	var out host.EncodedFrame
	enc, err := s.newEncoder(vtenc.Options{
		Width: cfg.Width, Height: cfg.Height, FullRange: true, Profile: s.opts.Profile,
		Bitrate: cfg.Bitrate, FPS: cfg.MaxFPS, Logger: s.opts.Logger,
	}, func(f *vtenc.Frame) {
		out.Data, out.Key, out.Captured, out.Encoded = f.Data, f.Key, f.Captured, f.Encoded
		frame(&out)
	})
	if err != nil {
		return err
	}
	defer enc.Close()
	started(enc)
	if s.capture == nil {
		s.pattern(ctx, enc, max(cfg.MaxFPS, 1))
		return nil
	}
	return s.capture.CaptureVideo(ctx, host.ScreenConfig{Width: cfg.Width, Height: cfg.Height, MaxFPS: cfg.MaxFPS},
		capture.VideoOutput{Encoder: enc, FullRange: true})
}

// pattern submits the test pattern to enc fps times a second until ctx
// ends. A picture the encoder refuses is logged, the first of a run of
// them as a warning.
func (s *Source) pattern(ctx context.Context, enc encoder, fps int) {
	c, stop := s.tick(time.Second / time.Duration(fps))
	defer stop()
	refused := 0
	for n := uint32(0); ; n++ {
		select {
		case <-ctx.Done():
			return
		case <-c:
		}
		switch st := enc.SubmitPattern(n); st {
		case 0, 1: // encoded or skipped
			refused = 0
		default:
			if refused == 0 && s.opts.Logger != nil {
				s.opts.Logger.Warn("the encoder refused a picture of the test pattern", "status", st, "picture", n)
			}
			refused++
		}
	}
}
