package main

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/mardp/internal/config"
	"github.com/rm4n0s/mardp/internal/host"
	"github.com/rm4n0s/mardp/internal/mac/system"
	"github.com/rm4n0s/mardp/internal/ui"
)

// TestStartOnOpen: "start the server when the app opens" starts it before
// the window shows, if a login is set and Screen Recording granted; a
// start that fails is said in the window (SPEC §7.3).
func TestStartOnOpen(t *testing.T) {
	tests := []struct {
		name     string
		start    bool
		granted  []host.Permission
		noLogin  bool
		taken    bool
		want     ui.ServerState
		problem  bool
		inTheLog string
	}{
		{"starts", true, []host.Permission{host.ScreenRecording}, false, false, ui.ServerListening, false, "the server started as the app opened"},
		{"the setting off", false, []host.Permission{host.ScreenRecording}, false, false, ui.ServerStopped, false, ""},
		{"without Screen Recording", true, nil, false, false, ui.ServerStopped, false, "a login or Screen Recording is missing"},
		{"without a login", true, []host.Permission{host.ScreenRecording}, true, false, ui.ServerStopped, false, "a login or Screen Recording is missing"},
		{"the port taken", true, []host.Permission{host.ScreenRecording}, false, true, ui.ServerStopped, true, "the server did not start"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHome(t, func(s *config.Settings) {
				s.StartServer = tt.start
				if tt.noLogin {
					s.User, s.Password = "", nil
				}
			})
			if tt.taken {
				l, err := net.Listen("tcp", h.addr())
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := l.Close(); err != nil {
						t.Error(err)
					}
				}()
			}
			m := newFakeMac(tt.granted...)
			var st ui.Status
			m.win = func(ctx context.Context, b ui.Backend) error {
				st = b.Status()
				return nil
			}
			var stderr syncBuffer
			if code := run(context.Background(), nil, h.getenv, &bytes.Buffer{}, &stderr, m); code != exitOK {
				t.Fatalf("exit %d\n%s", code, stderr.String())
			}
			if st.Server != tt.want {
				t.Errorf("server %d, want %d", st.Server, tt.want)
			}
			if (st.StartProblem != "") != tt.problem {
				t.Errorf("start problem %q", st.StartProblem)
			}
			if tt.inTheLog != "" && !strings.Contains(stderr.String(), tt.inTheLog) {
				t.Errorf("the log does not say %q:\n%s", tt.inTheLog, stderr.String())
			}
		})
	}
}

// TestSyncLogin: the app is made a login item, or taken out, as the
// setting says, and only when it is not already; an item that waits for
// the user's approval is left, and the window asks for it.
func TestSyncLogin(t *testing.T) {
	refused := errors.New("LoginItemFailed", "Operation not permitted")
	tests := []struct {
		name     string
		want     bool
		status   system.LoginItemStatus
		err      error
		asked    []bool
		approval bool
		problem  string
	}{
		{"added", true, system.LoginItemOff, nil, []bool{true}, false, ""},
		{"added already", true, system.LoginItemOn, nil, nil, false, ""},
		{"waits for approval", true, system.LoginItemNeedsApproval, nil, nil, true, ""},
		{"not found", true, system.LoginItemNotFound, refused, []bool{true}, false, refused.Error()},
		{"taken out", false, system.LoginItemOn, nil, []bool{false}, false, ""},
		{"taken out while it waits", false, system.LoginItemNeedsApproval, nil, []bool{false}, false, ""},
		{"out already", false, system.LoginItemOff, nil, nil, false, ""},
		{"out, not found", false, system.LoginItemNotFound, nil, nil, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHome(t, func(s *config.Settings) { s.OpenAtLogin = tt.want })
			m := newFakeMac(host.ScreenRecording)
			m.login.status, m.login.err = tt.status, tt.err
			a := newTestApp(t, h, m, false, slog.New(slog.DiscardHandler))
			a.syncLogin(tt.want)
			if got := m.login.asked(); !slices.Equal(got, tt.asked) {
				t.Errorf("asked %v, want %v", got, tt.asked)
			}
			st := a.Status()
			if st.LoginApproval != tt.approval || st.LoginProblem != tt.problem {
				t.Errorf("approval %v, problem %q", st.LoginApproval, st.LoginProblem)
			}
		})
	}
}

// TestWindowSyncsLogin: the app makes itself a login item as it opens, if
// the setting says so.
func TestWindowSyncsLogin(t *testing.T) {
	h := newHome(t, func(s *config.Settings) { s.OpenAtLogin = true })
	m := newFakeMac(host.ScreenRecording)
	m.win = func(context.Context, ui.Backend) error { return nil }
	var stderr syncBuffer
	if code := run(context.Background(), nil, h.getenv, &bytes.Buffer{}, &stderr, m); code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}
	if got := m.login.asked(); !slices.Equal(got, []bool{true}) {
		t.Errorf("asked %v", got)
	}
}

// TestAppSaveSettingsLogin: saving "open the app at login" changes the
// login item at once; saving other settings leaves it.
func TestAppSaveSettingsLogin(t *testing.T) {
	h := newHome(t, nil)
	m := newFakeMac(host.ScreenRecording)
	a := newTestApp(t, h, m, false, slog.New(slog.DiscardHandler))
	ctx := context.Background()
	steps := []struct {
		edit  func(s *config.Settings)
		asked []bool
	}{
		{func(s *config.Settings) { s.OpenAtLogin = true }, []bool{true}},
		{func(s *config.Settings) { s.Port++ }, []bool{true}},
		{func(s *config.Settings) { s.OpenAtLogin = false }, []bool{true, false}},
		{func(s *config.Settings) { s.StartServer = true }, []bool{true, false}},
	}
	for i, st := range steps {
		s := a.current()
		st.edit(&s)
		if err := a.SaveSettings(ctx, s); err != nil {
			t.Fatal(err)
		}
		if got := m.login.asked(); !slices.Equal(got, st.asked) {
			t.Errorf("step %d: asked %v, want %v", i, got, st.asked)
		}
	}
}

// TestWatchLogin: the window sees the user allow the login item in System
// Settings, and taking the app out there turns the setting off, so the
// next opening does not add it again.
func TestWatchLogin(t *testing.T) {
	h := newHome(t, func(s *config.Settings) { s.OpenAtLogin = true })
	m := newFakeMac(host.ScreenRecording)
	m.login.status = system.LoginItemNeedsApproval
	log := slog.New(slog.DiscardHandler)
	a := newTestApp(t, h, m, false, log)
	a.loginPoll = 10 * time.Millisecond
	a.syncLogin(true)
	if !a.Status().LoginApproval {
		t.Fatal("the window does not ask for the approval")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.watchLogin(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	m.login.set(system.LoginItemOn)
	eventually(t, "the approval", func() bool { return !a.Status().LoginApproval })
	m.login.set(system.LoginItemOff)
	eventually(t, "the setting off", func() bool { return !a.current().OpenAtLogin })
	_, saved, err := openFolder(context.Background(), h.getenv, log)
	if err != nil {
		t.Fatal(err)
	}
	if saved.OpenAtLogin || !saved.Ready() {
		t.Errorf("saved: open at login %v, login set %v", saved.OpenAtLogin, saved.Ready())
	}
	if got := m.login.asked(); len(got) != 0 {
		t.Errorf("asked %v", got)
	}
}
