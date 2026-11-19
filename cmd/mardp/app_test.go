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
	"github.com/rm4n0s/mardp/rdp"
	"github.com/rm4n0s/mardp/rdp/rdptest"
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
	if st := seen[2]; st.Server != ui.ServerConnected || st.Video != ui.TextVideoBitmaps {
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

func TestVideoText(t *testing.T) {
	for _, tt := range []struct {
		p    rdp.VideoPath
		want string
	}{
		{rdp.VideoAVC420, ui.TextVideoAVC420}, {rdp.VideoPlanar, ui.TextVideoPlanar},
		{rdp.VideoBitmaps, ui.TextVideoBitmaps}, {0, ""},
	} {
		if got := videoText(tt.p); got != tt.want {
			t.Errorf("videoText(%v) = %q, want %q", tt.p, got, tt.want)
		}
	}
}

// loadSettings reads h's config.json.
func loadSettings(t *testing.T, h *home) config.Settings {
	t.Helper()
	folder, err := config.Open(context.Background(), h.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := folder.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// samePlain says whether a and b are the same settings, but for the
// password, a pointer that each load makes anew.
func samePlain(a, b config.Settings) bool {
	a.Password, b.Password = nil, nil
	return a == b
}

// loopback is the Mac's addresses in the tests.
func loopback() ([]net.Addr, error) {
	return []net.Addr{
		&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
		&net.IPNet{IP: net.ParseIP("192.168.1.20"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("fd00::20"), Mask: net.CIDRMask(64, 128)},
	}, nil
}

// TestAppSaveSettings: the settings are saved with the login kept; with the
// server stopped nothing is pending.
func TestAppSaveSettings(t *testing.T) {
	displays := []host.Display{{ID: "1", Name: "Built-in", Width: 3024, Height: 1964, Main: true}}
	h := newHome(t, nil)
	m := newFakeMac(host.ScreenRecording, host.Accessibility)
	m.sys = host.NewFakeSystem(displays, host.ScreenRecording, host.Accessibility)
	a := newTestApp(t, h, m, false, slog.New(slog.DiscardHandler))
	a.interfaces = loopback
	ctx := context.Background()
	st := a.Status()
	if !samePlain(st.Settings, h.s) || !slices.Equal(st.Displays, displays) || st.Pending {
		t.Fatalf("status %+v", st)
	}
	s := st.Settings
	s.User, s.Password = "mallory", nil // not saved: SetLogin sets them
	s.Address, s.MaxFPS, s.MaxBitrate, s.Sound, s.Display, s.SwapCtrlCommand = "192.168.1.20", 30, 12_500_000, config.SoundBoth, "1", true
	s.OpenAtLogin, s.StartServer, s.MaxHeight = true, true, 1080
	if err := a.SaveSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	want := s
	want.User, want.Password = h.s.User, h.s.Password
	if got := loadSettings(t, h); got.User != testUser || !got.CheckLogin(testUser, testPassword) {
		t.Errorf("the login changed: %+v", got)
	} else if !samePlain(got, want) {
		t.Errorf("config.json has %+v, want %+v", got, want)
	}
	if st := a.Status(); !samePlain(st.Settings, want) || st.Pending {
		t.Errorf("status %+v", st)
	}
}

func TestAppSaveSettingsRefused(t *testing.T) {
	h := newHome(t, nil)
	m := newFakeMac()
	a := newTestApp(t, h, m, false, slog.New(slog.DiscardHandler))
	a.interfaces = loopback
	ctx := context.Background()
	for _, tt := range []struct {
		name  string
		edit  func(s *config.Settings)
		route string
		field string
	}{
		{"address not of this Mac", func(s *config.Settings) { s.Address = "10.9.9.9" }, "app.SaveSettings->app.checkLocal." + TagAddressNotLocal, "address"},
		{"address not an IP", func(s *config.Settings) { s.Address = "my-mac" }, "app.SaveSettings->app.checkLocal." + TagAddressNotLocal, "address"},
		{"port", func(s *config.Settings) { s.Port = 0 }, "app.SaveSettings->Folder.Save", "port"},
		{"bitrate", func(s *config.Settings) { s.MaxBitrate = 1 }, "app.SaveSettings->Folder.Save", "maxBitrate"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := h.s
			tt.edit(&s)
			err := a.SaveSettings(ctx, s)
			assertRoute(t, err, tt.route)
			if field, ok := ui.FieldOf(err); !ok || field != tt.field {
				t.Errorf("field %q, want %q", field, tt.field)
			}
			if !samePlain(a.Status().Settings, h.s) || !samePlain(loadSettings(t, h), h.s) {
				t.Error("refused settings were saved")
			}
		})
	}
	a.interfaces = func() ([]net.Addr, error) { return nil, errors.New("TestNoInterfaces", "none") }
	s := h.s
	s.Address = "127.0.0.1"
	assertRoute(t, a.SaveSettings(ctx, s), "app.SaveSettings->app.checkLocal."+TagInterfacesFailed)
	s.Address = "" // every address: nothing to check
	if err := a.SaveSettings(ctx, s); err != nil {
		t.Error(err)
	}
}

// TestAppRestartWhenIdle: settings saved while a client is connected wait
// for the session to end; then the server starts again with them.
func TestAppRestartWhenIdle(t *testing.T) {
	h := newHome(t, nil)
	m := newFakeMac(host.ScreenRecording, host.Accessibility)
	a := newTestApp(t, h, m, false, slog.New(slog.DiscardHandler))
	a.poll = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	running := func() *liveServer {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.run
	}
	first := running()

	// What applies when the app opens or at login does not restart it.
	s := h.s
	s.OpenAtLogin, s.StartServer = !s.OpenAtLogin, !s.StartServer
	if err := a.SaveSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	if st := a.Status(); st.Pending {
		t.Errorf("pending after %+v", st.Settings)
	}

	c, err := connect(t, h, testUser, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the session", func() bool { return a.Status().Server == ui.ServerConnected })
	s.MaxFPS = 30
	if err := a.SaveSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := a.NewCertificate(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // several polls
	if st := a.Status(); !st.Pending || st.Server != ui.ServerConnected || running() != first {
		t.Fatalf("the server did not wait for the session: %+v", st)
	}
	oldPrint := a.Status().Fingerprint
	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the restart", func() bool { r := running(); return r != nil && r != first })
	st := a.Status()
	if st.Pending || st.Server != ui.ServerListening || st.Fingerprint == oldPrint || st.Problem != "" {
		t.Errorf("after the restart %+v", st)
	}
	if r := running(); r.settings.MaxFPS != 30 {
		t.Errorf("the server runs with %+v", r.settings)
	}
}

// TestAppPendingStop: stopping the server drops what was pending; a stopped
// server has nothing pending.
func TestAppPendingStop(t *testing.T) {
	h := newHome(t, nil)
	m := newFakeMac(host.ScreenRecording, host.Accessibility)
	a := newTestApp(t, h, m, false, slog.New(slog.DiscardHandler))
	a.poll = time.Hour
	ctx := context.Background()
	if err := a.NewCertificate(ctx); err != nil {
		t.Fatal(err)
	}
	if a.Status().Pending {
		t.Error("pending with the server stopped")
	}
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.NewCertificate(ctx); err != nil {
		t.Fatal(err)
	}
	if !a.Status().Pending {
		t.Error("a new certificate is not pending")
	}
	if err := a.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if a.Status().Pending {
		t.Error("still pending after a stop and a start")
	}
}

func TestAppNewCertificateFails(t *testing.T) {
	h := newHome(t, nil)
	a := newTestApp(t, h, newFakeMac(), false, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assertRoute(t, a.NewCertificate(ctx), "app.NewCertificate->Folder.NewCertificate")
}

// TestAppClose: after the app closed, nothing starts the server again.
func TestAppClose(t *testing.T) {
	h := newHome(t, nil)
	m := newFakeMac(host.ScreenRecording, host.Accessibility)
	a := newTestApp(t, h, m, false, slog.New(slog.DiscardHandler))
	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if st := a.Status(); st.Server != ui.ServerStopped {
		t.Errorf("started after closing: %+v", st)
	}
}

// TestAppSessionAndEvents: the connected session's details, Disconnect,
// and the recent events: a refused login, the connections and why each
// session ended.
func TestAppSessionAndEvents(t *testing.T) {
	h := newHome(t, nil)
	m := newFakeMac(host.ScreenRecording, host.Accessibility)
	a := newTestApp(t, h, m, false, slog.New(slog.DiscardHandler))
	at := time.Date(2026, 11, 19, 14, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return at }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.Disconnect(ctx); err != nil {
		t.Errorf("Disconnect with the server stopped: %v", err)
	}
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Disconnect(ctx); err != nil {
		t.Errorf("Disconnect without a session: %v", err)
	}
	if _, err := connect(t, h, testUser, "wrong password"); err == nil {
		t.Fatal("a wrong password logged in")
	}
	session := func() *rdptest.Client {
		c, err := connect(t, h, testUser, testPassword)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Activate(ctx); err != nil {
			t.Fatal(err)
		}
		eventually(t, "the session", func() bool { return a.Status().Server == ui.ServerConnected })
		return c
	}
	c := session()
	st := a.Status()
	if s := st.Session; s.Client != rdptest.DefaultName || s.Remote == "" || s.Width <= 0 || s.Height <= 0 || s.Video != rdp.VideoBitmaps {
		t.Errorf("session %+v", s)
	}
	remote := st.Session.Remote
	if err := a.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, info, err := c.AwaitDisconnect(ctx); err != nil || info == 0 {
		t.Fatalf("the client was not told: %d %v", info, err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the session's end", func() bool { return len(a.Status().Events) == 3 })
	if st := a.Status(); st.Server != ui.ServerListening || st.Session != (host.SessionInfo{}) {
		t.Errorf("after Disconnect %+v", st)
	}

	// A session the server's stop ends.
	session()
	if err := a.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	got := a.Status().Events
	host1 := func(remote string) string { h, _, _ := net.SplitHostPort(remote); return h }
	for i := range got {
		if host1(got[i].Remote) != host1(remote) {
			t.Errorf("event %d from %q", i, got[i].Remote)
		}
		got[i].Remote = ""
	}
	conn := ui.Event{At: at, Kind: ui.EventConnected, Client: rdptest.DefaultName, User: testUser}
	byOwner, stopped := conn, conn
	byOwner.Kind, byOwner.Ended = ui.EventDisconnected, host.EndedByOwner
	stopped.Kind, stopped.Ended = ui.EventDisconnected, host.EndedServerStopped
	want := []ui.Event{{At: at, Kind: ui.EventLoginFailed, User: testUser, Failures: 1}, conn, byOwner, conn, stopped}
	if !slices.Equal(got, want) {
		t.Errorf("events\n%+v\nwant\n%+v", got, want)
	}
}

// TestAppEventsLimit keeps the newest maxEvents.
func TestAppEventsLimit(t *testing.T) {
	h := newHome(t, nil)
	m := newFakeMac(host.ScreenRecording, host.Accessibility)
	a := newTestApp(t, h, m, false, slog.New(slog.DiscardHandler))
	for i := range maxEvents + 10 {
		a.refused("user"+strconv.Itoa(i), "10.0.0.1:1", config.LoginResult{Checked: true, Failures: i + 1})
	}
	// A login refused because the address waits is no event.
	a.refused("waiting", "10.0.0.1:1", config.LoginResult{Failures: maxEvents + 10, Wait: time.Hour})
	st := a.Status()
	ev := st.Events
	if st.EventTotal != maxEvents+10 {
		t.Errorf("%d events in all, want %d", st.EventTotal, maxEvents+10)
	}
	if len(ev) != maxEvents || ev[0].User != "user10" || ev[len(ev)-1].User != "user"+strconv.Itoa(maxEvents+9) {
		t.Errorf("%d events, from %q to %q", len(ev), ev[0].User, ev[len(ev)-1].User)
	}
}
