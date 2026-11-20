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
	// Video captures the display into the H.264 encoder; nil sends the
	// picture on the bitmap paths only.
	Video VideoSource
	// Sound captures the system sound; nil offers the client none. The
	// server offers the formats of Host.AudioFormats.
	Sound SoundSource
	// AAC makes an AAC encoder (SPEC §10.1) for a session whose client
	// takes AAC; nil offers PCM alone. An encoder that implements
	// io.Closer is closed when the session's sound ends.
	AAC func() (SoundEncoder, error)
	// Output is the Mac's sound output, muted while a client gets the
	// sound, so that it plays on the client only (SPEC §10.1); nil plays
	// it on both.
	Output Output
	// Clipboard is the Mac's clipboard, shared with a client that has
	// input (SPEC §10.6); nil shares none.
	Clipboard Clipboard
}

// Options tune a Host. The zero value is the defaults.
type Options struct {
	// MaxFPS is the most frames a second on the bitmap paths; 0 means
	// DefaultMaxFPS.
	MaxFPS int
	// VideoFPS is the most frames a second on the H.264 path (SPEC §9.4):
	// 0 means DefaultVideoFPS, more is capped to it.
	VideoFPS int
	// MaxBitrate is the H.264 path's highest bitrate (SPEC §9.5): 0 means
	// DefaultMaxBitrate; it is at least MinBitrate.
	MaxBitrate int
	// MaxHeight caps the desktop's height (SPEC §9.2); 0 means no cap.
	MaxHeight int
	// Logger receives the log; nil discards it.
	Logger *slog.Logger
	// DoubleClickTime and DoubleClickDistance count double clicks
	// (SPEC §10.5); 0 means DefaultDoubleClickTime and
	// DefaultDoubleClickDistance.
	DoubleClickTime     time.Duration
	DoubleClickDistance float64
	// MuteMarker keeps which output device the host muted, so that a
	// mardp that ends without unmuting it unmutes it at its next start
	// (RecoverOutput); nil keeps no marker.
	MuteMarker MuteMarker
	// OnEvent is told when a session opens and when it ends, on the
	// session's goroutine; nil is told nothing.
	OnEvent func(Event)
	// OnFrameTiming is told when each frame of the H.264 path was
	// captured, encoded, queued and written (SPEC §14), on the session's
	// writer goroutine, which waits for it: it should return at once. nil
	// measures nothing.
	OnFrameTiming func(FrameTiming)

	// now is the clock of the double clicks; nil is time.Now.
	now func() time.Time
	// rateEvery is the H.264 path's rate period; 0 is defaultRateEvery.
	rateEvery time.Duration
	// statsEvery is how often a session's numbers are taken; 0 is
	// defaultStatsEvery.
	statsEvery time.Duration
}

// Host runs the sessions of an rdp.Server: it is its rdp.Handler, and its
// DesktopSize is the server's Config.DesktopSize.
type Host struct {
	parts Parts
	opts  Options
	log   *slog.Logger
	// open counts the sessions running now.
	open atomic.Int64
	// cur is the session that opened last, while it runs.
	cur atomic.Pointer[live]
	// mute mutes Parts.Output while sessions get the sound.
	mute muter
	// screens, videos and sounds count the captures running now.
	screens, videos, sounds atomic.Int64
}

// Captures is how many captures of each source a Host runs now.
type Captures struct {
	// Screen counts the captures of the bitmap paths, Video those into the
	// encoder (each with its encoder session open), Sound those of the
	// sound.
	Screen, Video, Sound int
}

// Captures is how many captures the host runs now: none once every
// session has ended (SPEC §15.5, `-measure`).
func (h *Host) Captures() Captures {
	return Captures{Screen: int(h.screens.Load()), Video: int(h.videos.Load()), Sound: int(h.sounds.Load())}
}

// New makes a Host of the parts.
func New(parts Parts, opts Options) (*Host, error) {
	if parts.Screen == nil {
		return nil, errors.New(TagScreenMissing, "the host has no screen source")
	}
	if opts.MaxFPS <= 0 {
		opts.MaxFPS = DefaultMaxFPS
	}
	if opts.VideoFPS <= 0 || opts.VideoFPS > DefaultVideoFPS {
		opts.VideoFPS = DefaultVideoFPS
	}
	if opts.MaxBitrate <= 0 {
		opts.MaxBitrate = DefaultMaxBitrate
	}
	opts.MaxBitrate = max(opts.MaxBitrate, MinBitrate)
	if opts.rateEvery <= 0 {
		opts.rateEvery = defaultRateEvery
	}
	if opts.statsEvery <= 0 {
		opts.statsEvery = defaultStatsEvery
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
	return &Host{parts: parts, opts: opts, log: log,
		mute: muter{out: parts.Output, marker: opts.MuteMarker, log: log}}, nil
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

// VideoPath is the video path of the session running now (SPEC §9.1), for
// the window; 0 when none runs.
func (h *Host) VideoPath() rdp.VideoPath {
	if l := h.cur.Load(); l != nil {
		return l.s.Video().Path()
	}
	return 0
}

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
	l, untrack := h.track(ctx, s)
	defer untrack()
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

	var poll *pointerPoll
	if h.parts.Pointer != nil {
		poll = newPointerPoll(h.parts.Pointer, s.Pointer(), log, h.opts.now)
		if d.PointWidth > 0 && d.PointHeight > 0 {
			poll.px, poll.py = float64(width)/float64(d.PointWidth), float64(height)/float64(d.PointHeight)
		}
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

	if h.parts.Sound != nil {
		var wg sync.WaitGroup
		wg.Go(func() { h.runSound(ctx, s, l, log) })
		defer wg.Wait()
	}

	if h.Clipboard() && s.Clipboard().Available() {
		relay := &clipboardRelay{mac: h.parts.Clipboard, dst: s.Clipboard(), log: log}
		relay.start()
		s.Clipboard().SetHandler(relay)
		log.Info("the clipboard's text is shared with the client")
		var wg sync.WaitGroup
		wg.Go(func() { relay.run(ctx) })
		defer func() {
			wg.Wait()
			s.Clipboard().SetHandler(nil)
		}()
	}

	p := picture{h: h, s: s, log: log, width: width, height: height, poll: poll,
		rate: &rateControl{bitrate: startBitrate(width, height, h.opts.MaxBitrate), max: h.opts.MaxBitrate,
			floor: floorBitrate(width, height, h.opts.MaxBitrate)}}
	p.sw.want = p.wantAVC
	s.SetFlowSink(&p.sw)
	defer s.SetFlowSink(nil)
	if err := p.run(ctx); err != nil && ctx.Err() == nil {
		log.Error("the capture stopped", "err", err)
		h.close(ctx, s, log)
	}
}

// picture sends a session's picture: on the H.264 path while the session
// is on it and the host has a video source, on the bitmap path otherwise
// (SPEC §9.1), switching when the session's path changes.
type picture struct {
	h             *Host
	s             *rdp.Session
	log           *slog.Logger
	width, height int
	poll          *pointerPoll
	rate          *rateControl
	sw            flowSwitch
}

// wantAVC says whether the H.264 path is the one to run.
func (p *picture) wantAVC() bool {
	return p.h.parts.Video != nil && p.s.Video().Path() == rdp.VideoAVC420
}

// run runs a capture of the path to run until ctx ends, or the capture
// fails, and starts another when the path changes.
func (p *picture) run(ctx context.Context) error {
	for {
		avc := p.wantAVC()
		rctx, cancel := context.WithCancel(ctx)
		var err error
		if avc {
			err = p.runAVC(rctx, cancel)
		} else {
			err = p.runBitmaps(rctx, cancel)
		}
		p.sw.set(nil, false, nil)
		switched := rctx.Err() != nil
		cancel()
		if ctx.Err() != nil || (err != nil && !switched) {
			return err
		}
		if err != nil {
			p.log.Debug("the capture stopped as the path changed", "err", err)
		}
		p.log.Info("video path", "path", p.s.Video().Path().String())
	}
}

// runBitmaps runs a capture for the bitmap paths.
func (p *picture) runBitmaps(ctx context.Context, cancel func()) error {
	h := p.h
	pic := newBitmapPath(p.s.Video().SendRects, p.log, p.width, p.height, time.Second/time.Duration(h.opts.MaxFPS))
	defer pic.stop()
	if p.poll != nil {
		pic.onFrame = p.poll.kickNow
	}
	p.sw.set(pic, false, cancel)
	h.screens.Add(1)
	defer h.screens.Add(-1)
	return h.parts.Screen.Capture(ctx, ScreenConfig{Width: p.width, Height: p.height, MaxFPS: h.opts.MaxFPS}, pic.frame)
}

// runAVC runs a capture into the encoder for the H.264 path.
func (p *picture) runAVC(ctx context.Context, cancel func()) error {
	h := p.h
	interval := time.Second / time.Duration(h.opts.VideoFPS)
	a := &avcPath{out: p.s.Video(), log: p.log, rate: p.rate, stop: cancel, interval: interval,
		wake: make(chan struct{}, 1)}
	defer a.end()
	if h.opts.OnFrameTiming != nil {
		a.times = &frameTimes{tell: h.opts.OnFrameTiming}
		a.out.SetWriteHook(a.times.written)
		defer a.out.SetWriteHook(nil)
	}
	if p.poll != nil {
		a.onFrame = p.poll.kickNow
	}
	p.sw.set(a, true, cancel)
	cfg := VideoConfig{Width: p.width, Height: p.height, MaxFPS: h.opts.VideoFPS, Bitrate: p.rate.bitrate}
	// The ticker stops when the capture does, for whatever reason.
	tctx, stopTicker := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { a.run(tctx, interval/2, h.opts.rateEvery) })
	defer wg.Wait()
	defer stopTicker()
	h.videos.Add(1)
	defer h.videos.Add(-1)
	return h.parts.Video.CaptureVideo(ctx, cfg, a.started, a.frame)
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
