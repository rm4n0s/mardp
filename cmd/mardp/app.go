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
)

// The failures of the window's backend.
const (
	// TagUserMissing: the user name to set is empty.
	TagUserMissing = "MardpUserMissing"
	// TagStopTimedOut: the server's sessions did not end in time.
	TagStopTimedOut = "MardpStopTimedOut"
)

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
	if serr := a.Stop(sctx); err == nil {
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

	mu       sync.Mutex
	settings config.Settings
	starting bool
	run      *liveServer
	problem  string
}

// liveServer is a server that serves until cancel; done is closed when it
// returned.
type liveServer struct {
	*server
	cancel context.CancelFunc
	done   chan struct{}
}

func newApp(folder *config.Folder, s config.Settings, sys host.System, m machine, viewOnly bool, log *slog.Logger) *app {
	return &app{folder: folder, sys: sys, m: m, viewOnly: viewOnly, log: log, interfaces: net.InterfaceAddrs, settings: s}
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
	a.mu.Lock()
	s, r := a.settings, a.run
	st.User, st.LoginSet, st.Problem = s.User, s.Ready(), a.problem
	a.mu.Unlock()
	if r == nil {
		return st
	}
	st.Server = ui.ServerListening
	if r.hst.Sessions() > 0 {
		st.Server = ui.ServerConnected
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

// Start implements ui.Backend: the server runs until Stop, on a context of
// its own.
func (a *app) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.run != nil || a.starting {
		a.mu.Unlock()
		return nil
	}
	a.starting, a.problem = true, ""
	a.mu.Unlock()
	srv, err := prepare(ctx, a.folder, a.current, a.sys, a.m, a.viewOnly, a.log)
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

// Stop implements ui.Backend.
func (a *app) Stop(ctx context.Context) error {
	a.mu.Lock()
	r := a.run
	a.run = nil
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
