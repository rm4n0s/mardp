package host

import (
	"context"
	"sync"
	"time"

	"github.com/rm4n0s/mardp/rdp"
)

// defaultStatsEvery is how often a session's numbers are taken (SPEC §12.2).
const defaultStatsEvery = time.Second

// Ended is why a session ended (SPEC §12.2, the recent events).
type Ended int

// The reasons a session ends.
const (
	// EndedByClient: the client disconnected, or its connection was lost.
	EndedByClient Ended = iota + 1
	// EndedByOwner: the Mac's owner disconnected it (Host.Disconnect).
	EndedByOwner
	// EndedByHost: the host could not go on with it, as when the capture
	// failed.
	EndedByHost
	// EndedReplaced: another client connected.
	EndedReplaced
	// EndedServerStopped: the server stopped.
	EndedServerStopped
)

// Event is a session that opened or ended, for the window's recent events
// (SPEC §12.2).
type Event struct {
	// Ended is why the session ended; 0 when it opened.
	Ended Ended
	// Client is the client's computer name; Remote its address; User the
	// user name it logged in with.
	Client, Remote, User string
}

// SessionInfo is the running session's details and numbers, for the window
// (SPEC §12.2).
type SessionInfo struct {
	// Client is the client's computer name; Remote its address.
	Client, Remote string
	// Since is when the session opened.
	Since time.Time
	// Width and Height are the desktop's size.
	Width, Height int
	// Video is the video path now (SPEC §9.1).
	Video rdp.VideoPath
	// Sound is the sound format the client takes, "AAC" or "PCM"; "" while
	// it takes none.
	Sound string
	// FPS is the frames a second the client got over the last period: the
	// frames it acknowledged on the graphics pipeline, the updates sent on
	// the bitmap paths.
	FPS float64
	// Bitrate is the bits a second sent to the client over the last period.
	Bitrate int
	// Delay is how long the client takes to acknowledge a frame, smoothed;
	// 0 on the bitmap paths, which have no acknowledgements.
	Delay time.Duration
}

// live is a running session as the host keeps it for the window.
type live struct {
	s *rdp.Session

	mu   sync.Mutex
	info SessionInfo
	// byOwner says that Disconnect ended the session.
	byOwner bool
	// last are the session's counters at the last sample, taken at at.
	last rdp.Stats
	at   time.Time
}

// sample takes the session's numbers since the last sample.
func (l *live) sample(now time.Time) {
	st := l.s.Stats()
	l.mu.Lock()
	defer l.mu.Unlock()
	if secs := now.Sub(l.at).Seconds(); secs > 0 {
		frames := st.Frames - l.last.Frames
		if st.Acknowledged > 0 {
			frames = st.Acknowledged - l.last.Acknowledged
		}
		l.info.FPS = float64(frames) / secs
		l.info.Bitrate = int(float64(st.Bytes-l.last.Bytes) * 8 / secs)
	}
	l.info.Delay = st.Delay
	l.last, l.at = st, now
}

// setSound records the sound format the client takes.
func (l *live) setSound(format string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.info.Sound = format
}

// track makes s the running session, tells OnEvent that it opened, and
// takes its numbers until ctx ends; end undoes it.
func (h *Host) track(ctx context.Context, s *rdp.Session) (l *live, end func()) {
	c := s.Client()
	remote := ""
	if c.Remote != nil {
		remote = c.Remote.String()
	}
	w, ht := s.DesktopSize()
	now := h.opts.now()
	l = &live{s: s, at: now, info: SessionInfo{Client: c.Name, Remote: remote, Since: now, Width: w, Height: ht}}
	h.cur.Store(l)
	h.event(Event{Client: c.Name, Remote: remote, User: c.User})
	sctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() {
		t := time.NewTicker(h.opts.statsEvery)
		defer t.Stop()
		for {
			select {
			case <-sctx.Done():
				return
			case <-t.C:
				l.sample(h.opts.now())
			}
		}
	})
	return l, func() {
		cancel()
		wg.Wait()
		h.cur.CompareAndSwap(l, nil)
		h.event(Event{Ended: l.ended(), Client: c.Name, Remote: remote, User: c.User})
	}
}

// ended is why the session ended, once it has.
func (l *live) ended() Ended {
	l.mu.Lock()
	byOwner := l.byOwner
	l.mu.Unlock()
	switch r := l.s.ClosedBy(); {
	case byOwner:
		return EndedByOwner
	case r == rdp.CloseReplaced:
		return EndedReplaced
	case r == rdp.CloseServerStopped:
		return EndedServerStopped
	case r != 0:
		return EndedByHost
	}
	return EndedByClient
}

// event hands e to Options.OnEvent.
func (h *Host) event(e Event) {
	if h.opts.OnEvent != nil {
		h.opts.OnEvent(e)
	}
}

// Session returns the running session's details and numbers; ok is false
// when no session runs.
func (h *Host) Session() (info SessionInfo, ok bool) {
	l := h.cur.Load()
	if l == nil {
		return SessionInfo{}, false
	}
	l.mu.Lock()
	info = l.info
	l.mu.Unlock()
	info.Video = l.s.Video().Path()
	return info, true
}

// Disconnect ends the running session for the Mac's owner (SPEC §12.2): the
// client is told that the server ended it. It does nothing when no session
// runs.
func (h *Host) Disconnect(ctx context.Context) error {
	l := h.cur.Load()
	if l == nil {
		return nil
	}
	l.mu.Lock()
	l.byOwner = l.s.ClosedBy() == 0
	l.mu.Unlock()
	return l.s.Close(ctx, rdp.CloseByHost)
}
