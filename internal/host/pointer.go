package host

import (
	"context"
	"log/slog"
	"math"
	"sync/atomic"
	"time"

	"github.com/rm4n0s/mardp/rdp"
)

// When the pointer is read (SPEC §10.3): on a timer, after each frame, and
// this long after each mouse event, when the app under the pointer has
// changed it.
const (
	pointerInterval   = 250 * time.Millisecond
	pointerAfterMouse = 30 * time.Millisecond
)

// When the pointer's position is sent (SPEC §10.3): only when something
// on the Mac moved it, so not this soon after the client's last mouse
// event, and not when it is within this many points of where the client
// put it.
const (
	pointerSettle = 200 * time.Millisecond
	pointerSlack  = 1.5
)

// pointerTarget is what the poller sends to: the session's *rdp.Pointer.
type pointerTarget interface {
	SetShape(img *rdp.Image, hotX, hotY int) error
	Hide() error
	Move(x, y int) error
}

// pointerPoll reads the system pointer and sends it when it changed.
type pointerPoll struct {
	src   PointerSource
	dst   pointerTarget
	log   *slog.Logger
	kick  chan struct{}
	mouse *time.Timer
	now   func() time.Time

	// px and py turn the display's points into desktop pixels; zero sends
	// no position.
	px, py float64
	// client gives where the client last put the pointer, in the display's
	// points, and a count of its moves; nil when input is off. It is set
	// before run starts.
	client func() (x, y float64, moves uint64)
	// lastMouse is when the client's last mouse event came, in UnixNano.
	lastMouse atomic.Int64

	// Only run's goroutine uses these.
	sent      bool
	last      PointerShape // what the client shows: Visible, and which shape
	hiddenAt  int64        // when the Mac's pointer was first read hidden, in UnixNano; 0 while it shows
	over      bool         // the client's mouse moved since: its pointer shows all the same
	kept      PointerShape // a copy of the last shape read with its picture; Image is nil before the first
	keptImg   rdp.Image
	failures  int
	off       bool
	moves     uint64
	known     bool    // kx, ky is where the client's pointer is
	kx, ky    float64 // in the display's points
	moveFails int
}

// newPointerPoll makes the poller of a session.
func newPointerPoll(src PointerSource, dst pointerTarget, log *slog.Logger, now func() time.Time) *pointerPoll {
	p := &pointerPoll{src: src, dst: dst, log: log, now: now, kick: make(chan struct{}, 1)}
	p.mouse = time.AfterFunc(time.Hour, p.kickNow)
	p.mouse.Stop()
	return p
}

// kickNow asks for a read now.
func (p *pointerPoll) kickNow() {
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

// afterMouse asks for a read a moment after a mouse event.
func (p *pointerPoll) afterMouse() {
	p.lastMouse.Store(p.now().UnixNano())
	p.mouse.Reset(pointerAfterMouse)
}

// run reads the pointer until ctx ends.
func (p *pointerPoll) run(ctx context.Context) {
	t := time.NewTicker(pointerInterval)
	defer t.Stop()
	defer p.mouse.Stop()
	p.poll()
	for !p.off {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-p.kick:
		}
		p.poll()
	}
}

// poll reads the pointer once and sends it if it changed.
func (p *pointerPoll) poll() {
	sh, err := p.src.Current()
	if err != nil {
		if p.failures++; p.failures == 1 {
			p.log.Warn("the pointer could not be read", "err", err)
		}
		return
	}
	p.position(sh)
	sh = p.keep(sh)
	show := p.shown(sh)
	if p.off || p.sent && show == p.last.Visible && (!show || sh.Hash == p.last.Hash) {
		return
	}
	if show {
		err = p.dst.SetShape(sh.Image, sh.HotX, sh.HotY)
	} else {
		err = p.dst.Hide()
	}
	switch {
	case err == nil:
		p.sent, p.last = true, PointerShape{Visible: show, Hash: sh.Hash}
	case hasTag(err, rdp.TagQueueFull):
		// The next read tries again.
	case hasTag(err, rdp.TagFastPathUnsupported), hasTag(err, rdp.TagSessionOver):
		p.log.Info("the pointer is not sent", "err", err)
		p.off = true
	default:
		p.log.Warn("the pointer was not sent", "err", err)
		p.sent, p.last = true, PointerShape{Visible: show, Hash: sh.Hash}
	}
}

// keep copies the shape read when it has a picture, and gives the last
// copy to a hidden one that came without: macOS may give no picture of a
// pointer it hides, and the client's may have to show all the same.
func (p *pointerPoll) keep(sh PointerShape) PointerShape {
	img := sh.Image
	if img == nil {
		if !sh.Visible && p.kept.Image != nil {
			sh.Image, sh.HotX, sh.HotY, sh.Hash = p.kept.Image, p.kept.HotX, p.kept.HotY, p.kept.Hash
		}
		return sh
	}
	if p.kept.Image != nil && p.kept.Hash == sh.Hash {
		return sh
	}
	n := img.Width * 4
	if img.Width <= 0 || img.Height <= 0 || img.Stride < n || len(img.Pix) < (img.Height-1)*img.Stride+n {
		return sh // SetShape refuses it
	}
	if cap(p.keptImg.Pix) < n*img.Height {
		p.keptImg.Pix = make([]byte, n*img.Height)
	}
	p.keptImg.Pix = p.keptImg.Pix[:n*img.Height]
	for y := range img.Height {
		copy(p.keptImg.Pix[y*n:(y+1)*n], img.Pix[y*img.Stride:])
	}
	p.keptImg.Stride, p.keptImg.Width, p.keptImg.Height = n, img.Width, img.Height
	p.kept = PointerShape{Image: &p.keptImg, HotX: sh.HotX, HotY: sh.HotY, Hash: sh.Hash}
	return sh
}

// shown says whether the client shows the pointer (SPEC §10.3): when the
// Mac shows its own, and also when the Mac hides it but the client's mouse
// has moved since it hid, until the Mac shows it again. macOS hides the
// pointer while keys are typed, and goes on saying hidden while the
// client's mouse moves it again: a user who moves the mouse must see where
// it is.
func (p *pointerPoll) shown(sh PointerShape) bool {
	if sh.Visible {
		p.hiddenAt, p.over = 0, false
		return true
	}
	if p.hiddenAt == 0 {
		p.hiddenAt = p.now().UnixNano()
	}
	if !p.over && p.lastMouse.Load() > p.hiddenAt {
		p.over = true
		p.log.Debug("the pointer is shown though macOS hides it: the client's mouse moved",
			"hidden", time.Duration(p.now().UnixNano()-p.hiddenAt))
	}
	return p.over && sh.Image != nil
}

// position sends the pointer's position when something on the Mac moved
// it: it is away from where the client put it, and the client's mouse has
// been still for a moment. At the start of a session the client's pointer
// is put where the Mac's is.
func (p *pointerPoll) position(sh PointerShape) {
	if !sh.OnDisplay || p.px == 0 || p.py == 0 {
		return
	}
	if p.client != nil {
		if x, y, moves := p.client(); moves != p.moves {
			p.moves, p.known, p.kx, p.ky = moves, true, x, y
		}
	}
	if at := p.lastMouse.Load(); at != 0 && p.now().Sub(time.Unix(0, at)) < pointerSettle {
		return
	}
	if p.known && math.Abs(sh.X-p.kx) <= pointerSlack && math.Abs(sh.Y-p.ky) <= pointerSlack {
		return
	}
	err := p.dst.Move(int(math.Round(sh.X*p.px)), int(math.Round(sh.Y*p.py)))
	switch {
	case err == nil:
	case hasTag(err, rdp.TagQueueFull):
		return // the next read tries again
	case hasTag(err, rdp.TagFastPathUnsupported), hasTag(err, rdp.TagSessionOver):
		p.log.Info("the pointer is not sent", "err", err)
		p.off = true
		return
	default:
		if p.moveFails++; p.moveFails == 1 {
			p.log.Warn("the pointer's position was not sent", "err", err)
		}
	}
	p.known, p.kx, p.ky = true, sh.X, sh.Y
}
