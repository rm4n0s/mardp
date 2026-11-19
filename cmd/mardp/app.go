package main

import (
	"context"
	"log/slog"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/mardp/internal/config"
	"github.com/rm4n0s/mardp/internal/host"
	"github.com/rm4n0s/mardp/internal/ui"
	"github.com/rm4n0s/mardp/rdp"
)

// The failures of the window's backend.
const (
	// TagUserMissing: the user name to set is empty.
	TagUserMissing = "MardpUserMissing"
	// TagStopTimedOut: the server's sessions did not end in time.
	TagStopTimedOut = "MardpStopTimedOut"
	// TagAddressNotLocal: the address to save is not an address of this
	// Mac; its metadata "field" is "address", as config's TagInvalid.
	TagAddressNotLocal = "MardpAddressNotLocal"
	// TagInterfacesFailed: the Mac's addresses could not be listed, to
	// check the address to save; the cause is kept.
	TagInterfacesFailed = "MardpInterfacesFailed"
)

// restartPoll is how often the app looks whether the server can start
// again with saved settings: when no client is connected (SPEC §12.2).
const restartPoll = 250 * time.Millisecond

// maxEvents is how many recent events the window shows (SPEC §12.2).
const maxEvents = 50

// stopTimeout is how long the server may take to end its sessions when the
// app quits.
const stopTimeout = 10 * time.Second

// window runs the app's window with the settings of mardp's folder until the
// window is closed or ctx ends, and then stops the server (SPEC §12).
func window(ctx context.Context, getenv func(string) string, m machine, viewOnly bool, log *slog.Logger) error {
	folder, s, err := openFolder(ctx, getenv, log)
	if err != nil {
		return err
	}
	sys, err := m.system()
	if err != nil {
		return err
	}
	a := newApp(folder, s, sys, m, viewOnly, log)
	err = m.window(ctx, a, log)
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
	defer cancel()
	if serr := a.close(sctx); err == nil {
		err = serr
	}
	return err
}

// app is the window's ui.Backend: the server of mardp's folder.
type app struct {
	folder     *config.Folder
	sys        host.System
	m          machine
	viewOnly   bool
	log        *slog.Logger
	interfaces func() ([]net.Addr, error)
	// poll is how often the app looks whether the server can start again
	// with saved settings.
	poll time.Duration
	// now is the clock of the events.
	now func() time.Time

	mu       sync.Mutex
	settings config.Settings
	starting bool
	run      *liveServer
	problem  string
	// pending says that saved settings wait for the next session, and
	// restarting that a goroutine waits for it; closed that the app quits
	// and starts no server anymore.
	pending, restarting, closed bool
	// events are the recent events, the oldest first.
	events []ui.Event
}

// liveServer is a server that serves until cancel; done is closed when it
// returned.
type liveServer struct {
	*server
	cancel context.CancelFunc
	done   chan struct{}
}

func newApp(folder *config.Folder, s config.Settings, sys host.System, m machine, viewOnly bool, log *slog.Logger) *app {
	return &app{folder: folder, sys: sys, m: m, viewOnly: viewOnly, log: log, interfaces: net.InterfaceAddrs, poll: restartPoll, now: time.Now,
		settings: s}
}

// current returns the settings now.
func (a *app) current() config.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

// Status implements ui.Backend.
func (a *app) Status() ui.Status {
	st := ui.Status{
		ScreenRecording: a.sys.Granted(host.ScreenRecording),
		Accessibility:   a.sys.Granted(host.Accessibility),
	}
	displays, err := a.sys.Displays()
	if err != nil {
		a.log.Debug("the displays could not be listed", "err", err)
	}
	st.Displays = displays
	a.mu.Lock()
	s, r := a.settings, a.run
	st.User, st.LoginSet, st.Problem = s.User, s.Ready(), a.problem
	st.Settings, st.Pending = s, a.pending && r != nil
	st.Events = slices.Clone(a.events)
	a.mu.Unlock()
	if r == nil {
		return st
	}
	st.Server = ui.ServerListening
	if r.hst.Sessions() > 0 {
		st.Server = ui.ServerConnected
		st.Video = videoText(r.hst.VideoPath())
		st.Session, _ = r.hst.Session()
	}
	st.Fingerprint, st.ViewOnly = r.fingerprint, !r.post
	port := r.l.Addr().(*net.TCPAddr).Port
	addrs, err := a.interfaces()
	if err != nil {
		a.log.Warn("the Mac's addresses could not be listed", "err", err)
	}
	st.Addresses = addresses(addrs, s.Address, port)
	return st
}

// videoText names the video path p for the window; "" for none.
func videoText(p rdp.VideoPath) string {
	switch p {
	case rdp.VideoAVC420:
		return ui.TextVideoAVC420
	case rdp.VideoPlanar:
		return ui.TextVideoPlanar
	case rdp.VideoBitmaps:
		return ui.TextVideoBitmaps
	}
	return ""
}

// Start implements ui.Backend: the server runs until Stop, on a context of
// its own.
func (a *app) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.run != nil || a.starting || a.closed {
		a.mu.Unlock()
		return nil
	}
	a.starting, a.problem = true, ""
	a.mu.Unlock()
	srv, err := prepare(ctx, a.folder, a.current, a.sys, a.m, a.viewOnly, observer{session: a.sessionEvent, refused: a.refused}, a.log)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.starting = false
	if err != nil {
		a.log.Error("the server did not start", "err", err)
		return err
	}
	sctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r := &liveServer{server: srv, cancel: cancel, done: make(chan struct{})}
	a.run = r
	go func() {
		defer close(r.done)
		err := srv.srv.Serve(sctx, srv.l)
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.run == r {
			a.run = nil
		}
		if err != nil {
			a.log.Error("the server stopped", "err", err)
			a.problem = err.Error()
		}
	}()
	return nil
}

// Stop implements ui.Backend. The next start takes the saved settings, so
// none are pending anymore.
func (a *app) Stop(ctx context.Context) error {
	a.mu.Lock()
	r := a.run
	a.run, a.pending = nil, false
	a.mu.Unlock()
	if r == nil {
		return nil
	}
	r.cancel()
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return errors.NewErr(TagStopTimedOut, ctx.Err())
	}
}

// Disconnect implements ui.Backend: it ends the connected client's
// session.
func (a *app) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	r := a.run
	a.mu.Unlock()
	if r == nil {
		return nil
	}
	return r.hst.Disconnect(ctx)
}

// sessionEvent records a session that opened or ended.
func (a *app) sessionEvent(e host.Event) {
	ev := ui.Event{Kind: ui.EventConnected, Client: e.Client, Remote: e.Remote, User: e.User, Ended: e.Ended}
	if e.Ended != 0 {
		ev.Kind = ui.EventDisconnected
	}
	a.record(ev)
}

// refused records a refused login.
func (a *app) refused(user, remote string) {
	a.record(ui.Event{Kind: ui.EventLoginFailed, User: user, Remote: remote})
}

// record adds e to the recent events, now, dropping the oldest beyond
// maxEvents.
func (a *app) record(e ui.Event) {
	e.At = a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
	if n := len(a.events) - maxEvents; n > 0 {
		a.events = slices.Delete(a.events, 0, n)
	}
}

// SetLogin implements ui.Backend: it saves the user name and the password's
// hash. A running server checks the next login against them.
func (a *app) SetLogin(ctx context.Context, user, password string) error {
	user = strings.TrimSpace(user)
	if user == "" {
		return errors.New(TagUserMissing, "the user name is empty")
	}
	hash, err := config.HashPassword(password)
	if err != nil {
		return err
	}
	s := a.current()
	s.User, s.Password = user, hash
	if err := a.folder.Save(ctx, s); err != nil {
		return err
	}
	a.mu.Lock()
	a.settings = s
	a.mu.Unlock()
	a.log.Info("the user name and password are set", "user", user)
	return nil
}

// close stops the server for good, when the app quits: a restart for
// saved settings cannot start it again.
func (a *app) close(ctx context.Context) error {
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	return a.Stop(ctx)
}

// SaveSettings implements ui.Backend: it saves s with the user name and the
// password kept, and a running server starts again with them once no client
// is connected.
func (a *app) SaveSettings(ctx context.Context, s config.Settings) error {
	if s.Address != "" {
		if err := a.checkLocal(s.Address); err != nil {
			return err
		}
	}
	a.mu.Lock()
	old := a.settings
	a.mu.Unlock()
	s.Version, s.User, s.Password = old.Version, old.User, old.Password
	if err := a.folder.Save(ctx, s); err != nil {
		return err
	}
	a.mu.Lock()
	a.settings = s
	a.mu.Unlock()
	a.log.Info("the settings are saved", "port", s.Port, "address", s.Address, "display", s.Display,
		"maxHeight", s.MaxHeight, "maxFps", s.MaxFPS, "maxBitrate", s.MaxBitrate, "sound", s.Sound,
		"swapCtrlCommand", s.SwapCtrlCommand, "startServer", s.StartServer, "openAtLogin", s.OpenAtLogin)
	if forSession(old) != forSession(s) {
		a.restartWhenIdle()
	}
	return nil
}

// forSession is s without what does not apply to a session: the login,
// which a running server reads at each login, and what applies when the
// app opens or at login.
func forSession(s config.Settings) config.Settings {
	s.User, s.Password, s.StartServer, s.OpenAtLogin = "", nil, false, false
	return s
}

// checkLocal says whether address is an address of this Mac.
func (a *app) checkLocal(address string) error {
	ip := net.ParseIP(address)
	addrs, err := a.interfaces()
	if err != nil {
		return errors.NewErr(TagInterfacesFailed, err)
	}
	for _, ad := range addrs {
		if n, ok := ad.(*net.IPNet); ok && ip != nil && n.IP.Equal(ip) {
			return nil
		}
	}
	return errors.New(TagAddressNotLocal, "the address is not an address of this Mac", "field", "address", "address", address)
}

// NewCertificate implements ui.Backend: a running server starts again with
// the new certificate once no client is connected.
func (a *app) NewCertificate(ctx context.Context) error {
	c, err := a.folder.NewCertificate(ctx)
	if err != nil {
		return err
	}
	a.log.Info("a new certificate", "fingerprint", c.Fingerprint)
	a.restartWhenIdle()
	return nil
}

// restartWhenIdle makes the running server start again with the saved
// settings and certificate as soon as no client is connected: settings
// pending while it waits.
func (a *app) restartWhenIdle() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.run == nil {
		return
	}
	a.pending = true
	if a.restarting {
		return
	}
	a.restarting = true
	go a.waitIdle()
}

// waitIdle restarts the server once no client is connected; it returns
// when it did, or when nothing is pending anymore (the server stopped).
func (a *app) waitIdle() {
	t := time.NewTicker(a.poll)
	defer t.Stop()
	for range t.C {
		a.mu.Lock()
		r := a.run
		if !a.pending || r == nil || a.closed {
			a.pending, a.restarting = false, false
			a.mu.Unlock()
			return
		}
		if r.hst.Sessions() > 0 {
			a.mu.Unlock()
			continue
		}
		a.restarting = false
		a.mu.Unlock()
		a.log.Info("starting the server again with the saved settings")
		ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		err := a.Stop(ctx)
		if err == nil {
			err = a.Start(ctx)
		}
		cancel()
		if err != nil {
			a.mu.Lock()
			a.problem = err.Error()
			a.mu.Unlock()
		}
		return
	}
}

// OpenSettings implements ui.Backend.
func (a *app) OpenSettings(ctx context.Context, p host.Permission) error {
	if err := a.sys.Request(p); err != nil {
		return err
	}
	return a.sys.OpenSettings(ctx, p)
}

// addresses are what to type into Remmina for a server on address and port:
// address itself, or with every address ("") each address of the Mac's
// interfaces but the loopback and link-local ones, IPv4 first.
func addresses(addrs []net.Addr, address string, port int) []string {
	p := strconv.Itoa(port)
	if address != "" {
		return []string{net.JoinHostPort(address, p)}
	}
	var v4, v6 []string
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.IsLoopback() || n.IP.IsLinkLocalUnicast() || n.IP.IsUnspecified() || n.IP.IsMulticast() {
			continue
		}
		hp := net.JoinHostPort(n.IP.String(), p)
		if n.IP.To4() != nil {
			v4 = append(v4, hp)
		} else {
			v6 = append(v6, hp)
		}
	}
	return slices.Concat(v4, v6)
}
