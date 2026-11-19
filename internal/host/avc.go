package host

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/rm4n0s/mardp/rdp"
)

// Flow and rate control of the H.264 path (SPEC §9.5).
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
	// minMissed is the fewest frames a rate period must have missed to
	// count as held back.
	minMissed = 4
	// firstHold and maxHold are how many periods the bitrate stays after
	// a fall did not help: the first time, and at most; each time in a
	// row doubles it.
	firstHold = 10
	maxHold   = 320
	// defaultRateEvery is how often the bitrate is decided.
	defaultRateEvery = time.Second
)

// seen is what a rate period saw.
type seen struct {
	// sent are the frames that went.
	sent int
	// missed are the frames the backlog kept from being encoded: the
	// time it held the encoder back, in frames at the cap.
	missed int
	// lost says a frame was lost to a full queue.
	lost bool
}

// heldBack says the period missed at least an eighth of the frames the
// screen offered, and at least minMissed: the client showed fewer frames
// than the host had.
func (p seen) heldBack() bool {
	return p.missed >= minMissed && p.missed*8 >= p.sent+p.missed
}

// betterThan says a larger share of the offered frames went in p than in
// q, by an eighth or more.
func (p seen) betterThan(q seen) bool {
	return p.sent*(q.sent+q.missed)*8 >= q.sent*(p.sent+p.missed)*9
}

// rateControl decides the bitrate once a period from what the period saw
// (SPEC §9.5). Frames are missed either because the network carries fewer
// bytes than the stream has, and then a lower bitrate brings the frames
// back, or because the client decodes and draws slower than the host
// captures, and then a lower bitrate only spoils the picture. Both look
// the same from here, so a fall is a trial: the bitrate drops a quarter
// after a period that was held back, and the next period says whether
// that helped.
//   - Still held back, but a larger share of the frames went: it helped,
//     and the bitrate falls again, as the next trial of the same run.
//   - No longer held back, and at least half of the missed frames came
//     back: it helped, and the run of trials ends.
//   - Still held back, and no larger share went: the client sets the
//     pace. The bitrate goes back to where the run began, since a trial
//     of the run that seemed to help may have been chance, and stays for
//     firstHold periods, twice as long each time in a row, whatever is
//     missed.
//   - No longer held back, without the frames coming back: the screen
//     changed less, nothing is learned, and this trial's fall is undone.
//
// A frame lost to a full queue always costs a quarter. A period that
// missed fewer than minMissed frames raises the bitrate a sixteenth.
type rateControl struct {
	bitrate, max int
	// tried says the last period's fall is a trial; from is the bitrate
	// before it, origin the bitrate before the first trial of its run,
	// and was is what that period saw.
	tried        bool
	from, origin int
	was          seen
	// hold counts the periods the bitrate still stays whatever is
	// missed; nextHold is how long it stays the next time.
	hold, nextHold int
}

// fall drops the bitrate a quarter, not below MinBitrate.
func (r *rateControl) fall() { r.bitrate = max(MinBitrate, r.bitrate-r.bitrate/4) }

// update returns the bitrate after a period that saw p; clientPace says
// it found that the last fall did not help, so the client sets the pace
// and the bitrate went back.
func (r *rateControl) update(p seen) (bitrate int, clientPace bool) {
	if p.lost {
		// The bytes did not get out: that is the network.
		r.tried = false
		r.fall()
		return r.bitrate, false
	}
	held := p.heldBack()
	again := false
	if r.tried {
		r.tried = false
		switch {
		case held && p.betterThan(r.was):
			again = true
		case held:
			r.bitrate = r.origin
			r.hold = max(firstHold, r.nextHold)
			r.nextHold = min(maxHold, 2*r.hold)
			return r.bitrate, true
		case p.sent*2 >= r.was.sent*2+r.was.missed:
			r.nextHold = 0
		default:
			r.bitrate = r.from
			return r.bitrate, false
		}
	}
	switch {
	case held && r.hold > 0:
		r.hold--
	case held:
		if !again {
			r.origin = r.bitrate
		}
		r.tried, r.from, r.was = true, r.bitrate, p
		r.fall()
	default:
		r.hold = max(0, r.hold-1)
		if p.sent > 0 && p.missed < minMissed {
			r.bitrate = min(r.max, r.bitrate+r.bitrate/16)
		}
	}
	return r.bitrate, false
}

// videoOut is what the H.264 path needs of the session's video.
type videoOut interface {
	SendFrame(annexB []byte, keyFrame bool) error
	Backlog() rdp.Backlog
	Path() rdp.VideoPath
	SetWriteHook(fn func(rdp.VideoWrite))
}

// avcPath sends a session's picture on the H.264 path while one capture
// into the encoder runs. It is the flow sink then: a Refresh Rect and a
// resume force a key frame, and the encoder skips while output is
// suppressed. The backlog is read after each frame sent: the encoder skips
// while more than maxUnacked frames are on their way or video waits in the
// queue, so that an encoded frame always goes and none waits (SPEC §9.5).
// While it skips for the backlog, a ticker reads the backlog again until
// it clears; otherwise the path wakes only once a period, to set the
// bitrate (SPEC §11 rule 6). The time the backlog holds the encoder back
// is what the bitrate is decided from: the frames the period missed. The
// path ends (stop is called) when the session leaves the H.264 path.
type avcPath struct {
	out  videoOut
	log  *slog.Logger
	rate *rateControl
	stop func()
	// interval is the time between two captured frames at the cap.
	interval time.Duration
	onFrame  func()
	// times, when set, measures the frames sent (Options.OnFrameTiming).
	times *frameTimes
	// wake tells run that the backlog began to need watching; nil in
	// tests that tick by hand.
	wake chan struct{}
	// now is the clock; nil is time.Now.
	now func() time.Time

	mu       sync.Mutex
	enc      VideoEncoder
	skipping bool
	// since is when the encoder began to skip because of the backlog.
	since      time.Time
	suppressed bool
	closed     bool
	// busy says the backlog held the encoder back when last read.
	busy bool
	// What the current rate period saw: the frames sent, whether one was
	// lost to a full queue, and how long the backlog held the encoder
	// back.
	sent int
	lost bool
	held time.Duration
}

// clock is the time now.
func (a *avcPath) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
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
	if a.times != nil {
		a.times.sending(f)
	}
	err := a.out.SendFrame(f.Data, f.Key)
	if err != nil && a.times != nil {
		a.times.notSent()
	}
	switch {
	case err == nil:
		a.sent++
	case hasTag(err, rdp.TagKeyFrameWanted):
		a.enc.ForceKeyFrame()
	case hasTag(err, rdp.TagQueueFull):
		// The frames after this one would depend on it: start again
		// from a key frame, and slow down.
		a.enc.ForceKeyFrame()
		a.lost = true
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
// it; a tick counts what waits in the queue. The time the backlog holds
// the encoder back counts as frames the period missed, unless output is
// suppressed. The caller holds mu.
func (a *avcPath) decide(afterSend bool) {
	if a.closed || a.enc == nil {
		return
	}
	b := a.out.Backlog()
	busy := b.Frames > maxUnacked || (!afterSend && b.Queued > 0)
	if busy && !a.busy {
		select {
		case a.wake <- struct{}{}:
		default:
		}
	}
	a.busy = busy
	switch {
	case !busy && !a.since.IsZero():
		if !a.suppressed {
			a.held += a.clock().Sub(a.since)
		}
		a.since = time.Time{}
	case busy && a.since.IsZero():
		a.since = a.clock()
	}
	if skip := busy || a.suppressed; skip != a.skipping {
		a.skipping = skip
		a.enc.SetSkip(skip)
	}
}

// tick is the ticker's work: the skip flag, and the end of the path when
// the session left it. watch says the backlog still holds the encoder
// back, so the ticker should go on.
func (a *avcPath) tick() (watch bool) {
	if a.out.Path() != rdp.VideoAVC420 {
		a.mu.Lock()
		closed := a.closed
		a.closed = true
		a.mu.Unlock()
		if !closed {
			a.stop()
		}
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.decide(false)
	return a.busy && !a.closed
}

// period ends a rate period: the bitrate is decided from what it saw.
func (a *avcPath) period() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.enc == nil {
		return
	}
	if !a.since.IsZero() {
		// Held back across the period's end: its part so far counts now.
		now := a.clock()
		a.held += now.Sub(a.since)
		a.since = now
	}
	p := seen{sent: a.sent, lost: a.lost}
	if a.interval > 0 {
		p.missed = int(a.held / a.interval)
	}
	a.sent, a.lost, a.held = 0, false, 0
	if a.suppressed {
		return
	}
	old := a.rate.bitrate
	now, clientPace := a.rate.update(p)
	if clientPace {
		a.log.Info("a lower bitrate did not bring more frames: the client sets the pace, and the bitrate stays",
			"bitrate", now, "sent", p.sent, "missed", p.missed, "periods", a.rate.hold)
	}
	if now != old {
		a.log.Debug("the bitrate changed", "from", old, "to", now, "sent", p.sent, "missed", p.missed)
		if err := a.enc.SetBitrate(now); err != nil {
			a.log.Warn("the bitrate was not changed", "err", err, "bitrate", now)
		}
	}
}

// run ends a rate period every rateEvery, with a tick, and ticks every
// tick while the backlog holds the encoder back, until ctx ends. While
// frames go freely, or none are captured, it wakes once a period.
func (a *avcPath) run(ctx context.Context, tick, rateEvery time.Duration) {
	t := time.NewTicker(tick)
	t.Stop()
	defer t.Stop()
	r := time.NewTicker(rateEvery)
	defer r.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.wake:
			t.Reset(tick)
		case <-t.C:
			if !a.tick() {
				t.Stop()
			}
		case <-r.C:
			if a.tick() {
				t.Reset(tick)
			}
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
