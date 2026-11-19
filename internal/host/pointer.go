package host

import (
	"context"
	"log/slog"
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

// pointerTarget is what the poller sends to: the session's *rdp.Pointer.
type pointerTarget interface {
	SetShape(img *rdp.Image, hotX, hotY int) error
	Hide() error
}

// pointerPoll reads the system pointer and sends it when it changed.
type pointerPoll struct {
	src   PointerSource
	dst   pointerTarget
	log   *slog.Logger
	kick  chan struct{}
	mouse *time.Timer

	// Only run's goroutine uses these.
	sent     bool
	last     PointerShape
	failures int
	off      bool
}

// newPointerPoll makes the poller of a session.
func newPointerPoll(src PointerSource, dst pointerTarget, log *slog.Logger) *pointerPoll {
	p := &pointerPoll{src: src, dst: dst, log: log, kick: make(chan struct{}, 1)}
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
func (p *pointerPoll) afterMouse() { p.mouse.Reset(pointerAfterMouse) }

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
	if p.sent && sh.Visible == p.last.Visible && (!sh.Visible || sh.Hash == p.last.Hash) {
		return
	}
	if sh.Visible {
		err = p.dst.SetShape(sh.Image, sh.HotX, sh.HotY)
	} else {
		err = p.dst.Hide()
	}
	switch {
	case err == nil:
		p.sent, p.last = true, PointerShape{Visible: sh.Visible, Hash: sh.Hash}
	case hasTag(err, rdp.TagQueueFull):
		// The next read tries again.
	case hasTag(err, rdp.TagFastPathUnsupported), hasTag(err, rdp.TagSessionOver):
		p.log.Info("the pointer is not sent", "err", err)
		p.off = true
	default:
		p.log.Warn("the pointer was not sent", "err", err)
		p.sent, p.last = true, PointerShape{Visible: sh.Visible, Hash: sh.Hash}
	}
}
