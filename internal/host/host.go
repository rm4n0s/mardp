// Package host is one session's pipeline (SPEC §4.1, §4.2): it takes the
// picture, the pointer and the sound from the Mac's parts, hands them to an
// rdp.Session, and posts the client's input back. It is written against the
// interfaces of parts.go, so it is tested with the fakes of fake.go.
package host

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp"
)

// TagScreenMissing: New was given no screen source.
const TagScreenMissing = "HostScreenMissing"

// The defaults of Options.
const (
	DefaultMaxFPS = 30
	// closeTimeout is how long telling the client why its session ends may
	// take.
	closeTimeout = 5 * time.Second
	// displayTimeout is how long DesktopSize waits for the display.
	displayTimeout = 2 * time.Second
)

// Parts are the Mac's parts a session uses.
type Parts struct {
	// Screen captures the display. It is required.
	Screen ScreenSource
	// Input posts the client's input; nil drops it.
	Input Injector
	// Pointer reads the system pointer; nil leaves the client's own.
	Pointer PointerSource
	// System keeps the display awake during a session; nil does not.
	System System
}

// Options tune a Host. The zero value is the defaults.
type Options struct {
	// MaxFPS is the most frames a second; 0 means DefaultMaxFPS.
	MaxFPS int
	// MaxHeight caps the desktop's height (SPEC §9.2); 0 means no cap.
	MaxHeight int
	// Logger receives the log; nil discards it.
	Logger *slog.Logger
	// DoubleClickTime and DoubleClickDistance count double clicks
	// (SPEC §10.5); 0 means DefaultDoubleClickTime and
	// DefaultDoubleClickDistance.
	DoubleClickTime     time.Duration
	DoubleClickDistance float64

	// now is the clock of the double clicks; nil is time.Now.
	now func() time.Time
}

// Host runs the sessions of an rdp.Server: it is its rdp.Handler, and its
// DesktopSize is the server's Config.DesktopSize.
type Host struct {
	parts Parts
	opts  Options
	log   *slog.Logger
	// open counts the sessions running now.
	open atomic.Int64
}

// New makes a Host of the parts.
func New(parts Parts, opts Options) (*Host, error) {
	if parts.Screen == nil {
		return nil, errors.New(TagScreenMissing, "the host has no screen source")
	}
	if opts.MaxFPS <= 0 {
		opts.MaxFPS = DefaultMaxFPS
	}
	if opts.MaxHeight < 0 {
		opts.MaxHeight = 0
	}
	if opts.DoubleClickTime <= 0 {
		opts.DoubleClickTime = DefaultDoubleClickTime
	}
	if opts.DoubleClickDistance <= 0 {
		opts.DoubleClickDistance = DefaultDoubleClickDistance
	}
	if opts.now == nil {
		opts.now = time.Now
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Host{parts: parts, opts: opts, log: log}, nil
}

// DesktopSize decides a session's desktop size (SPEC §9.2): the display's
// size scaled down to fit inside the size the client asked for, with the
// display's aspect ratio, never scaled up, its height capped by MaxHeight,
// even on both sides. When the display cannot be read, the client gets the
// size it asked for, capped and made even.
func (h *Host) DesktopSize(c *rdp.Client) (width, height int) {
	ctx, cancel := context.WithTimeout(context.Background(), displayTimeout)
	defer cancel()
	d, err := h.parts.Screen.Display(ctx)
	if err != nil || d.Width <= 0 || d.Height <= 0 {
		if err != nil {
			h.log.Warn("the display could not be read: the client gets the size it asked for", "err", err)
		}
		d = Display{Width: c.DesktopWidth, Height: c.DesktopHeight}
	}
	return fitSize(d.Width, d.Height, c.DesktopWidth, c.DesktopHeight, h.opts.MaxHeight)
}

// Sessions is the number of sessions running now, for the window's status
// (SPEC §12.2).
func (h *Host) Sessions() int { return int(h.open.Load()) }

// fitSize fits a display of dw×dh inside a client's cw×ch.
func fitSize(dw, dh, cw, ch, maxHeight int) (width, height int) {
	if cw <= 0 || ch <= 0 {
		cw, ch = dw, dh
	}
	scale := min(1, float64(cw)/float64(dw), float64(ch)/float64(dh))
	if maxHeight > 0 {
		scale = min(scale, float64(maxHeight)/float64(dh))
	}
	side := func(v int) int {
		n := int(float64(v)*scale) &^ 1
		return min(max(n, 2), rdp.MaxDesktopSide)
	}
	return side(dw), side(dh)
}

// SessionOpened implements rdp.Handler: it runs the session until ctx ends.
func (h *Host) SessionOpened(ctx context.Context, s *rdp.Session) {
	h.open.Add(1)
	defer h.open.Add(-1)
	c := s.Client()
	log := h.log.With("client", c.Name, "remote", c.Remote)
	d, err := h.parts.Screen.Display(ctx)
	if err != nil {
		log.Error("the display could not be read", "err", err)
		h.close(ctx, s, log)
		return
	}
	width, height := s.DesktopSize()
	if h.parts.System != nil {
		release, err := h.parts.System.KeepAwake()
		if err != nil {
			log.Warn("the display may sleep during the session", "err", err)
		} else {
			defer release()
		}
	}

	pic := newBitmapPath(s.Video().SendRects, log, width, height, time.Second/time.Duration(h.opts.MaxFPS))
	defer pic.stop()

	var poll *pointerPoll
	if h.parts.Pointer != nil {
		poll = newPointerPoll(h.parts.Pointer, s.Pointer(), log, h.opts.now)
		if d.PointWidth > 0 && d.PointHeight > 0 {
			poll.px, poll.py = float64(width)/float64(d.PointWidth), float64(height)/float64(d.PointHeight)
		}
		pic.onFrame = poll.kickNow
	}

	if h.parts.Input != nil {
		r := newRelay(h.parts.Input, log, h.opts.now, d, width, height, h.opts.DoubleClickTime, h.opts.DoubleClickDistance)
		if poll != nil {
			r.onMouse = poll.afterMouse
			poll.client = r.position
		}
		s.SetInputSink(r)
		defer func() {
			s.SetInputSink(nil)
			r.release()
		}()
	}

	if poll != nil {
		pctx, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		wg.Go(func() { poll.run(pctx) })
		defer func() {
			cancel()
			wg.Wait()
		}()
	}

	s.SetFlowSink(pic)
	defer s.SetFlowSink(nil)

	err = h.parts.Screen.Capture(ctx, ScreenConfig{Width: width, Height: height, MaxFPS: h.opts.MaxFPS}, pic.frame)
	if err != nil && ctx.Err() == nil {
		log.Error("the capture stopped", "err", err)
		h.close(ctx, s, log)
	}
}

// close ends s because the host cannot go on with it.
func (h *Host) close(ctx context.Context, s *rdp.Session, log *slog.Logger) {
	// The session's own context ends with Close, which would abandon the
	// write at once.
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()
	if err := s.Close(cctx, rdp.CloseByHost); err != nil {
		log.Debug("closing the session", "err", err)
	}
}
