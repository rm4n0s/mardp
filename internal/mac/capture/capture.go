// Package capture captures a display with ScreenCaptureKit (SPEC §7.2,
// §9.2, D9), at the session's size and without the pointer: BGRA frames
// with the rectangles that changed for the bitmap paths (§9.6), which
// Capture hands to Go, and NV12 frames for the H.264 path (§9.3, §4.2),
// which CaptureVideo hands to the encoder in C. It implements
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
// # Video
//
// CaptureVideo asks ScreenCaptureKit for NV12 in the range the encoder is
// set to, with the BT.709 matrix, and its frame callback calls the
// encoder's submit function directly, under the output's lock, so a frame
// costs no crossing into Go on the way in; the encoder's skip flag is read
// in that function. Retiring the output (step 1) is also what guarantees
// that no frame reaches the encoder after CaptureVideo returns, so the
// encoder may be closed then.
//
// # Pausing and skipping
//
// SetPaused (the client minimized its window) and SetSkip (the session's
// queue is full) drop frames before they reach the frame function; the next
// frame after either is a whole frame. Capture never waits for the network:
// it sees only these flags (SPEC §4.2). They do not touch CaptureVideo,
// whose frames the encoder's own skip flag drops.
//
// # Sound
//
// Sound (SPEC §10.1) is the system sound, captured by a stream of its own
// on the same display at 48 kHz, two channels, 32-bit float, not
// interleaved, leaving out mardp's own sound (excludesCurrentProcessAudio)
// and the apps of Options.ExcludeApps, UTM's by default (§15.3): a virtual
// machine that plays the Mac's sound back would make a loop. ScreenCaptureKit
// leaves out only the apps it lists as shareable, which are those with a
// window, so the stream is started again when the apps it would leave out
// change. A buffer of exact silence is not delivered: when nothing plays,
// nothing is sent. Sound runs beside a capture of the picture, with a
// Source of its own (NewSound).
package capture

import (
	"context"
	"image"
	"log/slog"
	"sync/atomic"
	"time"
	"unsafe"

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
	// DefaultVideoFPS is the frame rate of CaptureVideo's ScreenConfig
	// without one (§9.4).
	DefaultVideoFPS = 60
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
	// ExcludeApps are the apps whose sound is left out, by bundle
	// identifier; one that ends in "." is a prefix. nil means
	// DefaultExcludeApps.
	ExcludeApps []string
}

// DefaultExcludeApps are the apps whose sound is left out by default:
// UTM's (SPEC §15.3).
var DefaultExcludeApps = []string{"com.utmapp."}

// The stream of sound.
const (
	// soundSide is the side of the picture a sound stream asks for, which
	// it drops; ScreenCaptureKit needs one.
	soundSide = 16
	// soundRecheck is how often a sound stream checks the apps it leaves
	// out.
	soundRecheck = 5 * time.Second
)

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

// VideoSink is an encoder that takes frames in C, as vtenc.Encoder does.
type VideoSink interface {
	// Submitter returns the C function a frame goes to and its first
	// argument: long fn(void *arg, CVPixelBufferRef pb, CMTime pts), which
	// returns 0 for a frame encoded, 1 for one skipped, and otherwise
	// VideoToolbox's OSStatus.
	Submitter() (fn, arg unsafe.Pointer)
}

// VideoOutput is where CaptureVideo sends its frames.
type VideoOutput struct {
	// Encoder takes the frames.
	Encoder VideoSink
	// FullRange asks for full range NV12 (420f) instead of video range
	// (420v); it is the range the encoder was made for.
	FullRange bool
}

// VideoStats count the frames CaptureVideo handed to the encoder.
type VideoStats struct {
	// Encoded, Skipped and Refused are the frames the encoder took, skipped
	// and refused.
	Encoded, Skipped, Refused int64
	// Crossings are the calls the capture made into Go with a frame; on the
	// H.264 path there are none.
	Crossings int64
}

// streamMode is what a stream delivers: BGRA frames to the sink, or NV12
// frames to the submit function fn with arg.
type streamMode struct {
	video     bool
	fullRange bool
	fn, arg   unsafe.Pointer
	// window, when not 0, is the only window captured (a CGWindowID), for
	// tests that need a still picture on a screen that is not still.
	window uint32
	// sound makes a stream of sound instead of pictures, which leaves out
	// the apps of exclude, and the process's own sound unless includeSelf
	// (which only tests set).
	sound       bool
	includeSelf bool
	exclude     []string
}

// stream is one running ScreenCaptureKit stream.
type stream interface {
	// stop tears it down in the order of the package documentation; no
	// callback reaches the sink after it returns. It returns what the
	// stream did with its frames.
	stop() VideoStats
	// excluded is how many running apps a sound stream leaves out.
	excluded() int
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
	// start starts a stream of display id at cfg that delivers to sink, or
	// to the encoder of mode.
	start(id uint32, cfg host.ScreenConfig, sink *sink, mode streamMode) (stream, *startFailure)
	// excludedApps is how many running apps ScreenCaptureKit lists that
	// match apps (as Options.ExcludeApps); -1 when it does not answer.
	excludedApps(apps []string) int
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

	// The VideoStats of the streams that ended.
	encoded, skipped, refused, crossings atomic.Int64

	// onlyWindow is streamMode.window, and includeSelf
	// streamMode.includeSelf; only tests set them.
	onlyWindow  uint32
	includeSelf bool
	// recheck is soundRecheck; tests shorten it.
	recheck time.Duration
}

var _ host.ScreenSource = (*Source)(nil)

// newSource makes a Source on a backend.
func newSource(opts Options, mac backend) *Source {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if opts.ExcludeApps == nil {
		opts.ExcludeApps = DefaultExcludeApps
	}
	return &Source{opts: opts, log: log, mac: mac, wait: displayWait, retry: firstRetry, recheck: soundRecheck}
}

// Sound captures the system sound; it implements host.SoundSource.
type Sound struct{ src *Source }

var _ host.SoundSource = (*Sound)(nil)

// Capture captures the system sound until ctx ends, following the display
// its stream is on, and calls buf with each buffer that is not silent.
func (k *Sound) Capture(ctx context.Context, buf func(*host.SoundBuffer)) error {
	s := k.src
	sk := &sink{src: s, sound: buf, stopped: make(chan stopEvent, 1)}
	cfg := host.ScreenConfig{Width: soundSide, Height: soundSide, MaxFPS: 1}
	return s.run(ctx, cfg, sk, streamMode{sound: true, includeSelf: s.includeSelf, exclude: s.opts.ExcludeApps})
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
	return s.run(ctx, cfg, sk, streamMode{window: s.onlyWindow})
}

// CaptureVideo captures the display at cfg in NV12 until ctx ends,
// following it through display changes, and hands each frame that changed
// to out's encoder in C (see the package documentation). The encoder must
// stay open until CaptureVideo returns.
func (s *Source) CaptureVideo(ctx context.Context, cfg host.ScreenConfig, out VideoOutput) error {
	var fn, arg unsafe.Pointer
	if out.Encoder != nil {
		fn, arg = out.Encoder.Submitter()
	}
	if cfg.Width < 2 || cfg.Height < 2 || cfg.Width > host.MaxDesktopSide || cfg.Height > host.MaxDesktopSide || fn == nil {
		return errors.New(TagCaptureConfigInvalid, "the capture's size is out of range, or it has no encoder",
			"width", cfg.Width, "height", cfg.Height, "encoder", fn != nil)
	}
	if cfg.MaxFPS <= 0 {
		cfg.MaxFPS = DefaultVideoFPS
	}
	sk := &sink{src: s, width: cfg.Width, height: cfg.Height, stopped: make(chan stopEvent, 1)}
	return s.run(ctx, cfg, sk, streamMode{video: true, fullRange: out.FullRange, fn: fn, arg: arg, window: s.onlyWindow})
}

// VideoStats returns what the streams of CaptureVideo that ended did with
// their frames.
func (s *Source) VideoStats() VideoStats {
	return VideoStats{Encoded: s.encoded.Load(), Skipped: s.skipped.Load(), Refused: s.refused.Load(), Crossings: s.crossings.Load()}
}

// run captures until ctx ends, starting a stream of mode for each display
// it follows.
func (s *Source) run(ctx context.Context, cfg host.ScreenConfig, sk *sink, mode streamMode) error {
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
		st, fail := s.mac.start(d.id, cfg, sk, mode)
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
		if mode.sound {
			s.log.Info("capturing sound", "display", d.info.ID, "id", d.id, "excludedApps", st.excluded())
		} else {
			s.log.Info("capturing", "display", d.info.ID, "id", d.id, "width", cfg.Width, "height", cfg.Height, "fps", cfg.MaxFPS,
				"video", mode.video)
		}
		started := time.Now()
		restart, err := s.watch(ctx, sk, st, d, tick, mode)
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
func (s *Source) watch(ctx context.Context, sk *sink, st stream, d rawDisplay, tick *time.Ticker, mode streamMode) (restart bool, err error) {
	checked := time.Now()
	defer func() {
		v := st.stop()
		s.encoded.Add(v.Encoded)
		s.skipped.Add(v.Skipped)
		s.refused.Add(v.Refused)
		s.crossings.Add(v.Crossings)
	}()
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
			if mode.sound && time.Since(checked) >= s.recheck {
				checked = time.Now()
				if n := s.mac.excludedApps(mode.exclude); n >= 0 && n != st.excluded() {
					s.log.Info("the apps left out of the sound changed; capturing again", "excludedApps", n, "were", st.excluded())
					return true, nil
				}
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
	sound         func(*host.SoundBuffer)
	width, height int
	stopped       chan stopEvent
	// whole makes the next frame delivered whole: the first of a stream.
	whole atomic.Bool

	// Used only by the stream's sample queue, one frame at a time.
	img   host.Image
	out   host.Frame
	dirty []image.Rectangle
	snd   host.SoundBuffer
	// first is the time of the capture's first buffer of sound, in the
	// host's clock, which all streams share; started says it is set.
	first   time.Duration
	started bool
}

// deliverSound hands one buffer of sound to the sound function, unless it
// is silent: left and right are its two channels, at the host's time t.
func (k *sink) deliverSound(left, right []float32, t time.Duration) {
	if len(left) == 0 || len(left) != len(right) {
		return
	}
	if !k.started {
		k.first, k.started = t, true
	}
	if silent(left) && silent(right) {
		return
	}
	k.snd = host.SoundBuffer{Left: left, Right: right, Time: max(t-k.first, 0)}
	k.sound(&k.snd)
	k.snd.Left, k.snd.Right = nil, nil
}

// silent says every sample of b is zero.
func silent(b []float32) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
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
