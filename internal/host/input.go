package host

import (
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/rm4n0s/mardp/rdp"
)

// Double clicks (SPEC §10.5): a press of the same button within this time
// and distance of the last one counts as its next click.
const (
	DefaultDoubleClickTime     = 500 * time.Millisecond
	DefaultDoubleClickDistance = 4.0 // points
)

// keyID is a key as the client names it.
type keyID struct {
	code uint8
	ext  rdp.Extended
}

// relay is a session's rdp.InputSink: it scales positions to the display's
// points, counts clicks, keeps what is held down, and hands everything to
// the injector. Its calls come on the session's reading goroutine; release
// may come from another, so a mutex guards it.
type relay struct {
	in  Injector
	log *slog.Logger
	now func() time.Time
	// sx and sy turn desktop pixels into points.
	sx, sy        float64
	maxX, maxY    float64
	clickTime     time.Duration
	clickDistance float64
	onMouse       func() // after each mouse event, for the pointer
	mu            sync.Mutex
	closed        bool
	keys          map[keyID]struct{}
	runes         map[rune]struct{}
	buttons       map[rdp.MouseButton]int // held, with their click count
	x, y          float64                 // the last position
	lastButton    rdp.MouseButton
	lastDown      time.Time
	lastX, lastY  float64
	clicks        int
	failures      int
}

// newRelay makes the relay of a session of the desktop size on display d.
func newRelay(in Injector, log *slog.Logger, now func() time.Time, d Display, width, height int, clickTime time.Duration, clickDistance float64) *relay {
	return &relay{
		in: in, log: log, now: now,
		sx: float64(d.PointWidth) / float64(width), sy: float64(d.PointHeight) / float64(height),
		maxX: float64(d.PointWidth) - 1, maxY: float64(d.PointHeight) - 1,
		clickTime: clickTime, clickDistance: clickDistance,
		keys: map[keyID]struct{}{}, runes: map[rune]struct{}{}, buttons: map[rdp.MouseButton]int{},
	}
}

// failed logs a failure of the injector: the first at Warn, the rest at
// Debug, since one missing permission fails every event.
func (r *relay) failed(what string, err error) {
	if err == nil {
		return
	}
	r.failures++
	if r.failures == 1 {
		r.log.Warn("input not posted", "event", what, "err", err)
		return
	}
	r.log.Debug("input not posted", "event", what, "failures", r.failures, "err", err)
}

// point scales a desktop position to the display's points.
func (r *relay) point(x, y int) (float64, float64) {
	return min(max(float64(x)*r.sx, 0), r.maxX), min(max(float64(y)*r.sy, 0), r.maxY)
}

// Key implements rdp.InputSink.
func (r *relay) Key(code uint8, ext rdp.Extended, down bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	k := keyID{code, ext}
	if down {
		r.keys[k] = struct{}{}
	} else {
		delete(r.keys, k)
	}
	r.failed("key", r.in.Key(code, ext, down))
}

// Unicode implements rdp.InputSink.
func (r *relay) Unicode(c rune, down bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if down {
		r.runes[c] = struct{}{}
	} else {
		delete(r.runes, c)
	}
	r.failed("unicode", r.in.Unicode(c, down))
}

// MouseMove implements rdp.InputSink.
func (r *relay) MouseMove(x, y int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.x, r.y = r.point(x, y)
	r.failed("move", r.in.MouseMove(r.x, r.y))
	r.mouseDone()
}

// MouseButton implements rdp.InputSink.
func (r *relay) MouseButton(b rdp.MouseButton, down bool, x, y int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.x, r.y = r.point(x, y)
	var clicks int
	if down {
		now := r.now()
		if b == r.lastButton && now.Sub(r.lastDown) <= r.clickTime &&
			math.Hypot(r.x-r.lastX, r.y-r.lastY) <= r.clickDistance {
			r.clicks++
		} else {
			r.clicks = 1
		}
		r.lastButton, r.lastDown, r.lastX, r.lastY = b, now, r.x, r.y
		clicks = r.clicks
		r.buttons[b] = clicks
	} else {
		var held bool
		if clicks, held = r.buttons[b]; !held {
			clicks = 1
		}
		delete(r.buttons, b)
	}
	r.failed("button", r.in.MouseButton(b, down, r.x, r.y, clicks))
	r.mouseDone()
}

// Wheel implements rdp.InputSink.
func (r *relay) Wheel(dx, dy int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.failed("wheel", r.in.Wheel(dx, dy))
	r.mouseDone()
}

// SyncLocks implements rdp.InputSink. A Synchronize event also means every
// key is up (MS-RDPBCGR 2.2.8.1.1.3.1.1.5), so the keys still held are
// released first.
func (r *relay) SyncLocks(caps, num, scroll bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.releaseKeys()
	r.failed("locks", r.in.SetLocks(caps, num, scroll))
}

// mouseDone tells the pointer that the mouse did something.
func (r *relay) mouseDone() {
	if r.onMouse != nil {
		r.onMouse()
	}
}

// releaseKeys releases the keys and characters held down.
func (r *relay) releaseKeys() {
	for k := range r.keys {
		r.failed("key", r.in.Key(k.code, k.ext, false))
	}
	clear(r.keys)
	for c := range r.runes {
		r.failed("unicode", r.in.Unicode(c, false))
	}
	clear(r.runes)
}

// release releases everything still held down, at the end of the session
// (SPEC §10.4), and drops the input that comes after.
func (r *relay) release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	r.releaseKeys()
	for b, clicks := range r.buttons {
		r.failed("button", r.in.MouseButton(b, false, r.x, r.y, clicks))
	}
	clear(r.buttons)
}
