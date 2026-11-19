// Package capture captures a display with ScreenCaptureKit for the bitmap
// path (SPEC §7.2, §9.2, §9.6, D9): BGRA frames at the session's size,
// without the pointer, with the rectangles that changed. It implements
// host.ScreenSource.
//
// # The display
//
// The display is remembered by its UUID, never by its display ID, which
// changes when the monitor comes back from sleep (§19 R1). A capture
// follows the display through changes: when the stream stops because its
// display went away (SCStreamError -3815), or the display ID it captures is
// no longer the display with the UUID, it finds the display again by its
// UUID, or the main display, and starts a new stream. The first frame of
// every stream is a whole frame (Dirty nil), so the client gets a full
// picture after a pause.
//
// # Callbacks and teardown
//
// ScreenCaptureKit calls back on its own queues: one call per frame on the
// stream's sample queue, and one when the stream stops with an error. Each
// call holds the stream's output object's lock while it is in Go. A stream
// is torn down in this order, and nothing else frees what a callback uses:
//
//  1. Retire the output: under its lock, mark it dead. When that returns no
//     callback is in Go, and every later one returns at once.
//  2. Stop the stream and wait for ScreenCaptureKit's answer (a few seconds
//     at most).
//  3. Remove the output from the stream and release both.
//  4. Delete the cgo handle the callbacks used to reach Go.
//
// The callback for a stopped stream only posts to a buffered channel, never
// waits, so retiring cannot deadlock with it.
//
// # Pausing and skipping
//
// SetPaused (the client minimized its window) and SetSkip (the session's
// queue is full) drop frames before they reach the frame function; the next
// frame after either is a whole frame. Capture never waits for the network:
// it sees only these flags (SPEC §4.2).
package capture

import (
	"context"
	"image"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/internal/host"
)

// Tags of the errors of the capture.
const (
	// TagCaptureUnsupported: the package runs on a system that is not
	// macOS.
	TagCaptureUnsupported = "CaptureUnsupported"
	// TagCapturePermissionDenied: Screen Recording is not granted, or was
	// withdrawn while the stream ran (SPEC §19 R6). The session reports it
	// as its reason.
	TagCapturePermissionDenied = "CapturePermissionDenied"
	// TagCaptureDisplayMissing: there is no display to describe.
	TagCaptureDisplayMissing = "CaptureDisplayMissing"
	// TagCaptureConfigInvalid: a capture asked for a size that is not one.
	TagCaptureConfigInvalid = "CaptureConfigInvalid"
	// TagCaptureStartFailed: a stream would not start, after retries.
	TagCaptureStartFailed = "CaptureStartFailed"
	// TagCaptureStopped: the stream stopped with an error a new stream does
	// not cure, or kept stopping.
	TagCaptureStopped = "CaptureStopped"
)

// The ScreenCaptureKit errors the capture tells apart (SCStreamErrorCode).
const (
	errUserDeclined       = -3801
	errFailedToStart      = -3802
	errConnectionInvalid  = -3804
	errConnectionInterrup = -3805
	errNoCaptureSource    = -3815
	errSystemStopped      = -3821
)

// How the capture retries.
const (
	// maxAttempts is how many streams in a row may fail to start, or stop
	// soon after starting, before the capture gives up.
	maxAttempts = 5
	// steadyAfter is how long a stream runs before its start counts as a
	// success.
	steadyAfter = 5 * time.Second
	// firstRetry is the wait after the first failure; it doubles each time.
	firstRetry = 250 * time.Millisecond
	// displayWait is how often a capture without a display looks again,
	// and how often a running one checks that its display is still the
	// one with the UUID.
	displayWait = time.Second
	// DefaultMaxFPS is the frame rate of a ScreenConfig without one (§9.6).
	DefaultMaxFPS = 30
	// maxDirty is the most rectangles a frame lists; a frame with more is
	// sent whole.
	maxDirty = 64
)

// Options configure a Source.
type Options struct {
	// Display is the UUID of the display to capture; empty is the main
	// display.
	Display string
	// Logger receives the log; nil discards it.
	Logger *slog.Logger
}

// rawDisplay is a display as the backend sees it.
type rawDisplay struct {
	id   uint32
	info host.Display
}

// startFailure says why a stream did not start.
type startFailure struct {
	code    int // an SCStreamErrorCode, or 0
	message string
}

// stopEvent says a running stream stopped.
type stopEvent struct {
	code    int // an SCStreamErrorCode, or 0
	message string
}

// stream is one running ScreenCaptureKit stream.
type stream interface {
	// stop tears it down in the order of the package documentation; no
	// callback reaches the sink after it returns.
	stop()
}

// backend is ScreenCaptureKit and Core Graphics, or a fake.
type backend interface {
	// granted says whether Screen Recording is granted, without asking.
	granted() bool
	// find finds the display with the UUID, or the main display when uuid
	// is empty.
	find(uuid string) (rawDisplay, bool)
	// alive says whether display id is still active and still the display
	// with the UUID.
	alive(id uint32, uuid string) bool
	// start starts a stream of display id at cfg that delivers to sink.
	start(id uint32, cfg host.ScreenConfig, sink *sink) (stream, *startFailure)
}

// Source captures one display. One Capture runs at a time.
type Source struct {
	opts Options
	log  *slog.Logger
	mac  backend

	// wait is displayWait and retry firstRetry; tests shorten them.
	wait, retry time.Duration

	paused, skip atomic.Bool
	// gap says frames were dropped since the last one delivered.
	gap atomic.Bool
}

var _ host.ScreenSource = (*Source)(nil)

// newSource makes a Source on a backend.
func newSource(opts Options, mac backend) *Source {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Source{opts: opts, log: log, mac: mac, wait: displayWait, retry: firstRetry}
}

// SetPaused stops or resumes the delivery of frames; the first frame after
// a pause is whole.
func (s *Source) SetPaused(paused bool) { s.paused.Store(paused) }

// SetSkip makes the capture drop frames, or deliver them again; the first
// frame after skipping is whole.
func (s *Source) SetSkip(skip bool) { s.skip.Store(skip) }

// Display describes the display a capture would capture now: the one with
// the UUID of the options, or the main display when it is missing.
func (s *Source) Display(ctx context.Context) (host.Display, error) {
	if s.opts.Display != "" {
		if d, ok := s.mac.find(s.opts.Display); ok {
			return d.info, nil
		}
	}
	d, ok := s.mac.find("")
	if !ok {
		return host.Display{}, errors.New(TagCaptureDisplayMissing, "macOS lists no display", "display", s.opts.Display)
	}
	return d.info, nil
}

// Capture captures the display at cfg until ctx ends, following it through
// display changes, and calls frame with each frame that changed.
func (s *Source) Capture(ctx context.Context, cfg host.ScreenConfig, frame func(*host.Frame)) error {
	if cfg.Width < 2 || cfg.Height < 2 || cfg.Width > host.MaxDesktopSide || cfg.Height > host.MaxDesktopSide {
		return errors.New(TagCaptureConfigInvalid, "the capture's size is out of range", "width", cfg.Width, "height", cfg.Height)
	}
	if cfg.MaxFPS <= 0 {
		cfg.MaxFPS = DefaultMaxFPS
	}
	sk := &sink{src: s, frame: frame, width: cfg.Width, height: cfg.Height, stopped: make(chan stopEvent, 1)}
	uuid := s.opts.Display
	attempts := 0
	noDisplay := false
	tick := time.NewTicker(s.wait)
	defer tick.Stop()
	for {
		if !s.mac.granted() {
			return errors.New(TagCapturePermissionDenied, "Screen Recording is not granted")
		}
		d, ok := s.mac.find(uuid)
		if !ok && uuid != "" {
			d, ok = s.mac.find("")
		}
		if !ok {
			if !noDisplay {
				s.log.Warn("no display to capture; waiting for one", "display", uuid)
				noDisplay = true
			}
			if !sleep(ctx, s.wait) {
				return nil
			}
			continue
		}
		noDisplay = false
		if uuid == "" {
			uuid = d.info.ID
		}
		sk.whole.Store(true)
		st, fail := s.mac.start(d.id, cfg, sk)
		if fail != nil {
			if fail.code == errUserDeclined {
				return errors.New(TagCapturePermissionDenied, "macOS refused the capture", "code", fail.code, "reason", fail.message)
			}
			attempts++
			if attempts >= maxAttempts {
				return errors.New(TagCaptureStartFailed, "the capture would not start", "code", fail.code, "reason", fail.message, "attempts", attempts)
			}
			s.log.Warn("the capture did not start; trying again", "code", fail.code, "reason", fail.message, "attempt", attempts)
			if !sleep(ctx, s.retryDelay(attempts)) {
				return nil
			}
			continue
		}
		s.log.Info("capturing", "display", d.info.ID, "id", d.id, "width", cfg.Width, "height", cfg.Height, "fps", cfg.MaxFPS)
		started := time.Now()
		restart, err := s.watch(ctx, sk, st, d, tick)
		if err != nil || !restart {
			return err
		}
		if time.Since(started) >= steadyAfter {
			attempts = 0
		}
		attempts++
		if attempts >= maxAttempts {
			return errors.New(TagCaptureStopped, "the capture keeps stopping", "attempts", attempts)
		}
		if !sleep(ctx, s.retryDelay(attempts)) {
			return nil
		}
	}
}

// watch waits while stream st of display d runs, and stops it. restart says
// a new stream should start; err ends the capture.
func (s *Source) watch(ctx context.Context, sk *sink, st stream, d rawDisplay, tick *time.Ticker) (restart bool, err error) {
	defer st.stop()
	for {
		select {
		case <-ctx.Done():
			return false, nil
		case ev := <-sk.stopped:
			switch ev.code {
			case errUserDeclined:
				return false, errors.New(TagCapturePermissionDenied, "macOS stopped the capture: Screen Recording was withdrawn",
					"code", ev.code, "reason", ev.message)
			case errNoCaptureSource, errFailedToStart, errConnectionInvalid, errConnectionInterrup, errSystemStopped:
				s.log.Info("the stream stopped; capturing again", "code", ev.code, "reason", ev.message)
				return true, nil
			}
			return false, errors.New(TagCaptureStopped, "the capture stopped", "code", ev.code, "reason", ev.message)
		case <-tick.C:
			if !s.mac.alive(d.id, d.info.ID) {
				s.log.Info("the display changed; capturing again", "display", d.info.ID, "id", d.id)
				return true, nil
			}
		}
	}
}

// retryDelay is the wait after the attempts-th failure in a row.
func (s *Source) retryDelay(attempts int) time.Duration {
	return s.retry << min(attempts-1, 4)
}

// sleep waits d, or until ctx ends; it says whether ctx is still alive.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// sink takes the callbacks of the streams of one Capture.
type sink struct {
	src           *Source
	frame         func(*host.Frame)
	width, height int
	stopped       chan stopEvent
	// whole makes the next frame delivered whole: the first of a stream.
	whole atomic.Bool

	// Used only by the stream's sample queue, one frame at a time.
	img   host.Image
	out   host.Frame
	dirty []image.Rectangle
}

// deliver hands one frame to the frame function: pix holds height rows of
// stride bytes; rects are x, y, width, height of each changed rectangle,
// nil when the frame did not say.
func (k *sink) deliver(pix []byte, stride, width, height int, rects []int32) {
	if k.src.paused.Load() || k.src.skip.Load() {
		k.src.gap.Store(true)
		return
	}
	if width != k.width || height != k.height || stride < 4*width || len(pix) < stride*(height-1)+4*width {
		k.src.gap.Store(true)
		return
	}
	k.img = host.Image{Pix: pix, Stride: stride, Width: width, Height: height}
	k.out.Image = &k.img
	whole := k.whole.Swap(false)
	if k.src.gap.Swap(false) || whole || rects == nil || len(rects) > 4*maxDirty {
		k.out.Dirty = nil
	} else {
		bounds := image.Rect(0, 0, width, height)
		k.dirty = k.dirty[:0]
		for i := 0; i+3 < len(rects); i += 4 {
			x, y, w, h := int(rects[i]), int(rects[i+1]), int(rects[i+2]), int(rects[i+3])
			if r := image.Rect(x, y, x+w, y+h).Intersect(bounds); !r.Empty() {
				k.dirty = append(k.dirty, r)
			}
		}
		if len(k.dirty) == 0 {
			return
		}
		k.out.Dirty = k.dirty
	}
	k.frame(&k.out)
	k.out.Image, k.img.Pix = nil, nil
}

// stop records that the stream stopped; it never waits.
func (k *sink) stop(code int, message string) {
	select {
	case k.stopped <- stopEvent{code: code, message: message}:
	default:
	}
}
