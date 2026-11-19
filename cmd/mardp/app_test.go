package main

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/mardp/internal/config"
	"github.com/rm4n0s/mardp/internal/host"
	"github.com/rm4n0s/mardp/internal/ui"
)

// newTestApp is the backend of h's folder on m.
func newTestApp(t *testing.T, h *home, m *fakeMac, viewOnly bool, log *slog.Logger) *app {
	t.Helper()
	ctx := context.Background()
	folder, s, err := openFolder(ctx, h.getenv, log)
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(folder, s, m.sys, m, viewOnly, log)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	return a
}

// eventually waits until done says so.
func eventually(t *testing.T, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !done(); {
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestWindow runs mardp without -headless: the window's backend starts the
// server, a client logs in, and closing the window stops the server.
func TestWindow(t *testing.T) {
	h := newHome(t, nil)
	m := newFakeMac(host.ScreenRecording, host.Accessibility)
	var seen []ui.Status
	m.win = func(ctx context.Context, b ui.Backend) error {
		seen = append(seen, b.Status())
		if err := b.Start(ctx); err != nil {
			return err
		}
		if err := b.Start(ctx); err != nil { // running already: nothing
			return err
		}
		seen = append(seen, b.Status())
		c, err := connect(t, h, testUser, testPassword)
		if err != nil {
			return err
		}
		if err := c.Activate(ctx); err != nil {
			return err
		}
		eventually(t, "the session", func() bool { return b.Status().Server == ui.ServerConnected })
		seen = append(seen, b.Status())
		return nil // the window is closed
	}
	var stderr syncBuffer
	code := run(context.Background(), nil, h.getenv, &bytes.Buffer{}, &stderr, m)
	if code != exitOK {
		t.Fatalf("exit code %d\n%s", code, stderr.String())
	}
	if len(seen) != 3 {
		t.Fatalf("statuses %+v", seen)
	}
	addr := "127.0.0.1:" + strconv.Itoa(h.s.Port)
	if st := seen[0]; st.Server != ui.ServerStopped || !st.LoginSet || st.User != testUser || !st.ScreenRecording || !st.Accessibility {
		t.Errorf("before Start %+v", st)
	}
	if st := seen[1]; st.Server != ui.ServerListening || !slices.Equal(st.Addresses, []string{addr}) || st.Fingerprint == "" || st.ViewOnly {
		t.Errorf("after Start %+v", st)
	}
	if st := seen[2]; st.Server != ui.ServerConnected {
		t.Errorf("with a client %+v", st)
	}
	if nc, err := net.Dial("tcp", addr); err == nil {
		_ = nc.Close() // only the dial matters
		t.Error("the server still listens after the window closed")
	}
	if !strings.Contains(stderr.String(), "headless=false") {
		t.Errorf("the log:\n%s", stderr.String())
	}
}

func TestWindowRefuses(t *testing.T) {
	tests := []struct {
		name  string
		home  func(t *testing.T) *home
		m     *fakeMac
		route string
	}{
		{"config.json corrupt", func(t *testing.T) *home {
			h := newHome(t, nil)
			corrupt(t, h)
			return h
		}, newFakeMac(), "window->openFolder->Folder.Load." + config.TagCorrupt},
		{"the system", func(t *testing.T) *home { return newHome(t, nil) },
			&fakeMac{sysErr: errors.New("TestSystemFailed", "no system")}, "TestWindowRefuses.TestSystemFailed"},
		{"the window", func(t *testing.T) *home { return newHome(t, nil) },
			&fakeMac{sys: host.NewFakeSystem(nil), win: func(context.Context, ui.Backend) error {
				return errors.New("TestNoDisplay", "no display")
			}}, "window->fakeMac.window->TestWindowRefuses.func4.TestNoDisplay"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := window(ctx, tt.home(t).getenv, tt.m, false, slog.New(slog.DiscardHandler))
			assertRoute(t, err, tt.route)
		})
	}
}

func TestAppStartRefused(t *testing.T) {
	h := newHome(t, nil)
	m := newFakeMac(host.Accessibility)
	var log syncBuffer
	a := newTestApp(t, h, m, false, slog.New(slog.NewTextHandler(&log, nil)))
	err := a.Start(context.Background())
	assertRoute(t, err, "app.Start->prepare."+TagScreenRecordingMissing)
	if st := a.Status(); st.Server != ui.ServerStopped || st.ScreenRecording {
		t.Errorf("status %+v", st)
	}
	if !strings.Contains(log.String(), "the server did not start") {
		t.Errorf("the log:\n%s", log.String())
	}
	// Granted later, the server starts, view only without Accessibility.
	m.sys.Grant(host.ScreenRecording, true)
	m.sys.Grant(host.Accessibility, false)
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := a.Status(); st.Server != ui.ServerListening || !st.ViewOnly {
		t.Errorf("status %+v", st)
	}
}

func TestAppViewOnlyFlag(t *testing.T) {
	h := newHome(t, nil)
	m := newFakeMac(host.ScreenRecording, host.Accessibility)
	a := newTestApp(t, h, m, true, slog.New(slog.DiscardHandler))
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := a.Status(); !st.ViewOnly {
		t.Errorf("status %+v", st)
	}
}

func TestAppSetLogin(t *testing.T) {
	h := newHome(t, func(s *config.Settings) { s.User, s.Password = "", nil })
	m := newFakeMac(host.ScreenRecording, host.Accessibility)
	var log syncBuffer
	a := newTestApp(t, h, m, false, slog.New(slog.NewTextHandler(&log, nil)))
	ctx := context.Background()
	if st := a.Status(); st.LoginSet {
		t.Fatalf("status %+v", st)
	}
	assertRoute(t, a.Start(ctx), "app.Start->prepare."+TagNoPassword)

	for _, tt := range []struct {
		name, user, password, route string
	}{
		{"no user", "  ", "a long password", "app.SetLogin." + TagUserMissing},
		{"short password", "bob", "short", "app.SetLogin->HashPassword"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := a.SetLogin(ctx, tt.user, tt.password)
			assertRoute(t, err, tt.route)
		})
	}
	if e, ok := errors.FromError(a.SetLogin(ctx, "bob", "short")); !ok || e.Tag != config.TagPasswordTooShort {
		t.Errorf("a short password: %v", e)
	}

	if err := a.SetLogin(ctx, " bob ", testPassword); err != nil {
		t.Fatal(err)
	}
	if st := a.Status(); !st.LoginSet || st.User != "bob" {
		t.Errorf("status %+v", st)
	}
	folder, err := config.Open(ctx, h.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := folder.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !s.CheckLogin("bob", testPassword) {
		t.Error("config.json does not have the login")
	}

	// A running server checks the next login against a login set later.
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.SetLogin(ctx, "carol", wrongPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := connect(t, h, "carol", wrongPassword); err != nil {
		t.Errorf("the new login: %v", err)
	}
	if _, err := connect(t, h, "bob", testPassword); err == nil {
		t.Error("the old login still works")
	}
	for _, secret := range []string{testPassword, wrongPassword} {
		if strings.Contains(log.String(), secret) {
			t.Errorf("the log has a password:\n%s", log.String())
		}
	}
}

func TestAppSetLoginSaveFails(t *testing.T) {
	h := newHome(t, nil)
	m := newFakeMac()
	a := newTestApp(t, h, m, false, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.SetLogin(ctx, "bob", testPassword); err == nil {
		t.Error("saved with a canceled context")
	} else if st := a.Status(); st.User != testUser {
		t.Errorf("the login changed without being saved: %+v", st)
	}
}

func TestAppStopsByItself(t *testing.T) {
	h := newHome(t, nil)
	m := newFakeMac(host.ScreenRecording, host.Accessibility)
	a := newTestApp(t, h, m, false, slog.New(slog.DiscardHandler))
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	l := a.run.l
	a.mu.Unlock()
	if err := l.Close(); err != nil { // the listener fails
		t.Fatal(err)
	}
	eventually(t, "the server stopped", func() bool { return a.Status().Server == ui.ServerStopped })
	if st := a.Status(); st.Problem == "" {
		t.Errorf("status %+v", st)
	}
	// A new start forgets the problem.
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := a.Status(); st.Problem != "" || st.Server != ui.ServerListening {
		t.Errorf("status %+v", st)
	}
}

func TestAppStopTimedOut(t *testing.T) {
	a := &app{run: &liveServer{cancel: func() {}, done: make(chan struct{})}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assertRoute(t, a.Stop(ctx), "app.Stop."+TagStopTimedOut)
	if err := a.Stop(ctx); err != nil {
		t.Errorf("stopping a stopped server: %v", err)
	}
}

func TestAppOpenSettings(t *testing.T) {
	h := newHome(t, nil)
	m := newFakeMac()
	a := newTestApp(t, h, m, false, slog.New(slog.DiscardHandler))
	for _, p := range []host.Permission{host.ScreenRecording, host.Accessibility} {
		if err := a.OpenSettings(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	want := []host.Permission{host.ScreenRecording, host.Accessibility}
	if got := m.sys.Requests(); !slices.Equal(got, want) {
		t.Errorf("requested %v", got)
	}
	if got := m.sys.Opened(); !slices.Equal(got, want) {
		t.Errorf("opened %v", got)
	}
}

func TestAppAddressesFail(t *testing.T) {
	h := newHome(t, func(s *config.Settings) { s.Address = "" })
	m := newFakeMac(host.ScreenRecording)
	var log syncBuffer
	a := newTestApp(t, h, m, false, slog.New(slog.NewTextHandler(&log, nil)))
	a.interfaces = func() ([]net.Addr, error) { return nil, errors.New("TestNoInterfaces", "none") }
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := a.Status(); st.Server != ui.ServerListening || len(st.Addresses) != 0 {
		t.Errorf("status %+v", st)
	}
	if !strings.Contains(log.String(), "addresses could not be listed") {
		t.Errorf("the log:\n%s", log.String())
	}
}

func TestAddresses(t *testing.T) {
	ipnet := func(s string) net.Addr {
		ip, n, err := net.ParseCIDR(s)
		if err != nil {
			t.Fatal(err)
		}
		n.IP = ip
		return n
	}
	all := []net.Addr{
		ipnet("127.0.0.1/8"), ipnet("::1/128"), ipnet("fe80::1/64"), ipnet("169.254.3.4/16"),
		ipnet("fd00::20/64"), ipnet("192.168.1.20/24"), ipnet("10.0.0.5/8"),
		&net.IPAddr{IP: net.IPv4(1, 2, 3, 4)},
	}
	for _, tt := range []struct {
		name    string
		addrs   []net.Addr
		address string
		want    []string
	}{
		{"every address", all, "", []string{"192.168.1.20:3389", "10.0.0.5:3389", "[fd00::20]:3389"}},
		{"one address", all, "10.0.0.5", []string{"10.0.0.5:3389"}},
		{"one IPv6 address", nil, "fd00::20", []string{"[fd00::20]:3389"}},
		{"none", nil, "", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := addresses(tt.addrs, tt.address, 3389); !slices.Equal(got, tt.want) {
				t.Errorf("addresses %q, want %q", got, tt.want)
			}
		})
	}
}
