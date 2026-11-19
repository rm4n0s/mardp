package host

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/rm4n0s/mardp/rdp"
)

// Flow and rate control of the H.264 path (SPEC §9.5). The numbers are
// the first guesses; M6 tunes them against the owner's network.
const (
	// DefaultVideoFPS is the H.264 path's frame-rate cap (SPEC §9.4).
	DefaultVideoFPS = 60
	// StartBitrate, MinBitrate and DefaultMaxBitrate bound the bitrate.
	StartBitrate      = 8_000_000
	MinBitrate        = 1_000_000
	DefaultMaxBitrate = 20_000_000
	// maxUnacked is how many frames may be on their way before the next
	// captured frame is skipped.
	maxUnacked = 2
	// fastAck and slowAck are the acknowledgement delays below which the
	// bitrate may rise and above which it falls: the age of the oldest
	// frame on its way, the most a rate period saw.
	fastAck = 50 * time.Millisecond
	slowAck = 150 * time.Millisecond
	// defaultRateEvery is how often the bitrate is decided.
	defaultRateEvery = time.Second
)

// rateControl decides the bitrate once a period from what the period saw
// (SPEC §9.5): a quarter less when frames were skipped or acknowledgements
// were slow, a sixteenth more when frames went and acknowledgements came
// back fast, the same otherwise.
type rateControl struct {
	bitrate, max int
}

// update returns the bitrate after a period in which sent frames went,
// skipped says the flow control skipped frames (or a frame was lost to a
// full queue), and oldest is the oldest age of a frame on its way.
func (r *rateControl) update(sent int, skipped bool, oldest time.Duration) int {
	switch {
	case skipped || oldest > slowAck:
		r.bitrate = max(MinBitrate, r.bitrate-r.bitrate/4)
	case sent > 0 && oldest < fastAck:
		r.bitrate = min(r.max, r.bitrate+r.bitrate/16)
	}
	return r.bitrate
}

// videoOut is what the H.264 path needs of the session's video.
type videoOut interface {
	SendFrame(annexB []byte, keyFrame bool) error
	Backlog() rdp.Backlog
	Path() rdp.VideoPath
}

// avcPath sends a session's picture on the H.264 path while one capture
// into the encoder runs. It is the flow sink then: a Refresh Rect and a
// resume force a key frame, and the encoder skips while output is
// suppressed. A ticker reads the backlog: the encoder skips while more
// than maxUnacked frames are on their way or video waits in the queue, so
// that an encoded frame always goes and none waits (SPEC §9.5); and once a
// period it sets the bitrate. The path ends (stop is called) when the
// session leaves the H.264 path.
type avcPath struct {
	out  videoOut
	log  *slog.Logger
	rate *rateControl
	stop func()
	// interval is the time between two captured frames at the cap.
	interval time.Duration
	onFrame  func()

	mu       sync.Mutex
	enc      VideoEncoder
	skipping bool
	// since is when the encoder began to skip because of the backlog.
	since      time.Time
	suppressed bool
	closed     bool
	// What the current rate period saw.
	sent    int
	skipped bool
	oldest  time.Duration
}

// started takes the encoder's controls: the source's started callback.
func (a *avcPath) started(enc VideoEncoder) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.enc = enc
	a.skipping = a.suppressed
	enc.SetSkip(a.skipping)
}

// frame sends an encoded frame: the source's frame callback.
func (a *avcPath) frame(f *EncodedFrame) {
	a.mu.Lock()
	if a.closed || a.enc == nil {
		a.mu.Unlock()
		return
	}
	err := a.out.SendFrame(f.Data, f.Key)
	switch {
	case err == nil:
		a.sent++
	case hasTag(err, rdp.TagKeyFrameWanted):
		a.enc.ForceKeyFrame()
	case hasTag(err, rdp.TagQueueFull):
		// The frames after this one would depend on it: start again
		// from a key frame, and slow down.
		a.enc.ForceKeyFrame()
		a.skipped = true
		a.log.Debug("an H.264 frame was lost to a full queue", "bytes", len(f.Data))
	case hasTag(err, rdp.TagH264Unavailable):
		a.closed = true
		a.stop()
	case hasTag(err, rdp.TagSessionOver):
		a.closed = true
	default:
		a.enc.ForceKeyFrame()
		a.log.Warn("an H.264 frame was not sent", "err", err)
	}
	// Decided now, so that the next captured frame is already skipped.
	a.decide(true)
	a.mu.Unlock()
	if a.onFrame != nil {
		a.onFrame()
	}
}

// decide sets the encoder's skip flag from the backlog. Right after a
// send the frame just queued is not counted: the writer is about to take
// it; a tick counts what waits in the queue. A skip that lasted a frame's
// time skipped a frame, which counts against the bitrate; a shorter one,
// while the writer finished a frame, does not. The caller holds mu.
func (a *avcPath) decide(afterSend bool) {
	if a.closed || a.enc == nil {
		return
	}
	b := a.out.Backlog()
	a.oldest = max(a.oldest, b.Oldest)
	busy := b.Frames > maxUnacked || (!afterSend && b.Queued > 0)
	switch {
	case !busy:
		a.since = time.Time{}
	case a.since.IsZero():
		a.since = time.Now()
	case !a.suppressed && time.Since(a.since) >= a.interval:
		a.skipped = true
	}
	if skip := busy || a.suppressed; skip != a.skipping {
		a.skipping = skip
		a.enc.SetSkip(skip)
	}
}

// tick is the ticker's work: the skip flag, and the end of the path when
// the session left it.
func (a *avcPath) tick() {
	if a.out.Path() != rdp.VideoAVC420 {
		a.mu.Lock()
		closed := a.closed
		a.closed = true
		a.mu.Unlock()
		if !closed {
			a.stop()
		}
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.decide(false)
}

// period ends a rate period: the bitrate is decided from what it saw.
func (a *avcPath) period() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.enc == nil {
		return
	}
	if a.suppressed {
		a.sent, a.skipped, a.oldest = 0, false, 0
		return
	}
	old := a.rate.bitrate
	now := a.rate.update(a.sent, a.skipped, a.oldest)
	a.sent, a.skipped, a.oldest = 0, false, 0
	if now != old {
		if err := a.enc.SetBitrate(now); err != nil {
			a.log.Warn("the bitrate was not changed", "err", err, "bitrate", now)
		}
	}
}

// run ticks every tick and ends a rate period every rateEvery, until ctx
// ends.
func (a *avcPath) run(ctx context.Context, tick, rateEvery time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	r := time.NewTicker(rateEvery)
	defer r.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.tick()
		case <-r.C:
			a.period()
		}
	}
}

// KeyFrameWanted implements rdp.FlowSink.
func (a *avcPath) KeyFrameWanted() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.enc != nil {
		a.enc.ForceKeyFrame()
	}
}

// OutputSuppressed implements rdp.FlowSink: the encoder skips every frame
// while the client shows nothing.
func (a *avcPath) OutputSuppressed(suppressed bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.suppressed = suppressed
	if suppressed && a.enc != nil && !a.skipping {
		a.skipping = true
		a.enc.SetSkip(true)
	}
	a.decide(false)
}

// end ends the path: nothing more is sent.
func (a *avcPath) end() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
}

// flowSwitch is the session's rdp.FlowSink: it passes the client's
// requests to the path running now, and ends that path when a key frame
// is wanted on another (the graphics pipeline's surface was created, or
// the client's path changed while it showed nothing).
type flowSwitch struct {
	want func() bool // whether the H.264 path is the one to run

	mu         sync.Mutex
	cur        rdp.FlowSink
	avc        bool
	cancel     func()
	suppressed bool
}

// set makes sink, of the H.264 path when avc, the running path, which
// cancel ends. When the path to run has changed already, since the caller
// chose it, cancel is called at once: the key frame wanted when it changed
// came while no path ran.
func (f *flowSwitch) set(sink rdp.FlowSink, avc bool, cancel func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cur, f.avc, f.cancel = sink, avc, cancel
	if sink == nil {
		return
	}
	if f.want() != avc {
		cancel()
		return
	}
	if f.suppressed {
		sink.OutputSuppressed(true)
	}
}

// KeyFrameWanted implements rdp.FlowSink.
func (f *flowSwitch) KeyFrameWanted() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cur == nil {
		return
	}
	if f.want() != f.avc {
		f.cancel()
		return
	}
	f.cur.KeyFrameWanted()
}

// OutputSuppressed implements rdp.FlowSink.
func (f *flowSwitch) OutputSuppressed(suppressed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.suppressed = suppressed
	if f.cur != nil {
		f.cur.OutputSuppressed(suppressed)
	}
}
