package host

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/rm4n0s/mardp/rdp"
)

// clipboardInterval is how often the Mac's clipboard is looked at during a
// session (SPEC §10.6): macOS has no notice of a change, only a count that
// changes with it.
const clipboardInterval = 500 * time.Millisecond

// Clipboard is the Mac's clipboard, its text alone (SPEC §10.6). Its
// methods may be called from any goroutine.
type Clipboard interface {
	// Changes is a count that changes whenever the clipboard's contents
	// do, whoever changed them.
	Changes() int64
	// Text is the clipboard's text now; ok is false when it holds none.
	Text() (text string, ok bool)
	// SetText puts text on the clipboard, in the place of what it held.
	SetText(text string) error
}

// Clipboard says whether the host shares the Mac's clipboard with its
// clients, for the server's Config.Clipboard: it has one, and it has the
// client's input. A client that only watches gets no clipboard: reading
// it is more than watching the screen, and writing it is input.
func (h *Host) Clipboard() bool { return h.parts.Clipboard != nil && h.parts.Input != nil }

// clipboardRelay joins the Mac's clipboard and a session's (SPEC §10.6):
// the text the client's user copies is put on the Mac's clipboard, and a
// change of the Mac's clipboard is told to the client, which asks for the
// text when its user pastes. It is the session's rdp.ClipboardHandler.
type clipboardRelay struct {
	mac clipboardTarget
	dst clipboardOffer
	log *slog.Logger

	mu sync.Mutex
	// seen is the count of the Mac's clipboard that the client knows of:
	// offered to it, or its own text.
	seen int64
	// failures counts the texts that could not be put on the Mac's
	// clipboard, to log the first.
	failures int
}

// clipboardTarget is the Mac's clipboard: Clipboard.
type clipboardTarget = Clipboard

// clipboardOffer is where a change is offered: the session's
// *rdp.Clipboard.
type clipboardOffer interface {
	Offer(hasText bool) error
}

// The relay is the session's handler.
var _ rdp.ClipboardHandler = (*clipboardRelay)(nil)

// ServerText is the Mac's text, for the client's paste.
func (r *clipboardRelay) ServerText() (string, bool) { return r.mac.Text() }

// ClientText puts the text the client's user copied on the Mac's
// clipboard; that change is the client's own, and is not offered back.
func (r *clipboardRelay) ClientText(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.mac.SetText(text); err != nil {
		if r.failures++; r.failures == 1 {
			r.log.Warn("the client's text was not put on the Mac's clipboard", "err", err)
		}
		return
	}
	r.seen = r.mac.Changes()
	r.log.Debug("the client's text is on the Mac's clipboard", "bytes", len(text))
}

// check offers the Mac's clipboard to the client when it changed since
// the client last knew of it.
func (r *clipboardRelay) check() {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.mac.Changes()
	if n == r.seen {
		return
	}
	_, has := r.mac.Text()
	err := r.dst.Offer(has)
	switch {
	case err == nil:
		r.seen = n
		r.log.Debug("the Mac's clipboard is offered to the client", "text", has)
	case hasTag(err, rdp.TagQueueFull):
		// The next look tries again.
	default:
		r.seen = n
		r.log.Debug("the Mac's clipboard was not offered", "err", err)
	}
}

// start takes the Mac's clipboard as it is for known: what it held before
// the session is not offered, so that the client's own clipboard, which a
// client sends when it connects, is not taken from it.
func (r *clipboardRelay) start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = r.mac.Changes()
}

// run looks at the Mac's clipboard until ctx ends.
func (r *clipboardRelay) run(ctx context.Context) {
	t := time.NewTicker(clipboardInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		r.check()
	}
}
