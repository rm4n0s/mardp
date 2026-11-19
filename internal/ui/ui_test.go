package ui

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/glutter/gluttertest"
	m "github.com/rm4n0s/glutter/material"
	"github.com/rm4n0s/mardp/internal/host"
)

// fakeBackend is a Backend that the tests set the state of.
type fakeBackend struct {
	mu       sync.Mutex
	st       Status
	startErr error
	opened   []host.Permission
	logins   []string
	starts   int
	stops    int
}

func (f *fakeBackend) Status() Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.st
	st.Addresses = slices.Clone(f.st.Addresses)
	return st
}

// set changes the state.
func (f *fakeBackend) set(change func(st *Status)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(&f.st)
}

func (f *fakeBackend) Start(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	if f.startErr != nil {
		return f.startErr
	}
	f.st.Server = ServerListening
	f.st.Addresses = []string{"192.168.1.20:3389", "[fd00::20]:3389"}
	f.st.Fingerprint = "ab:cd:ef"
	f.st.ViewOnly = !f.st.Accessibility
	return nil
}

func (f *fakeBackend) Stop(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	f.st.Server, f.st.Addresses, f.st.Fingerprint, f.st.ViewOnly = ServerStopped, nil, "", false
	return nil
}

func (f *fakeBackend) SetLogin(ctx context.Context, user, password string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if utf8.RuneCountInString(password) < 8 {
		return errors.New("TestPasswordTooShort", "the password has fewer than 8 characters")
	}
	f.logins = append(f.logins, user+":"+password)
	f.st.User, f.st.LoginSet = user, true
	return nil
}

func (f *fakeBackend) OpenSettings(ctx context.Context, p host.Permission) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, p)
	return nil
}

// calls returns what the backend was asked to do.
func (f *fakeBackend) calls() (starts, stops int, opened []host.Permission, logins []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.stops, slices.Clone(f.opened), slices.Clone(f.logins)
}

// ready is the status of a Mac with everything granted and set.
func ready() Status {
	return Status{ScreenRecording: true, Accessibility: true, User: "alice", LoginSet: true}
}

func newTester(t *testing.T, b *fakeBackend) *gluttertest.Tester {
	t.Helper()
	ts := gluttertest.New(t, gluttertest.Opts{Size: m.Size{Width: 560, Height: 900}})
	ts.PumpWidget(NewApp(b))
	return ts
}

// settle pumps frames until done says so: Start, Stop and Save run on a
// goroutine, whose result reaches the UI thread at a time of its own.
func settle(t *testing.T, ts *gluttertest.Tester, what string, done func() bool) {
	t.Helper()
	for range 500 {
		ts.Pump(10 * time.Millisecond)
		if done() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("waiting for %s", what)
}

// enabled says whether the button with key k can be pressed.
func enabled(t *testing.T, ts *gluttertest.Tester, k m.ValueKey[string]) bool {
	t.Helper()
	b, ok := ts.Widget(gluttertest.ByKey(k)).(interface{ Enabled() bool })
	if !ok {
		t.Fatalf("%v is not a button", k)
	}
	return b.Enabled()
}

// shows says whether the window shows text.
func shows(ts *gluttertest.Tester, text string) bool {
	return ts.Exists(gluttertest.ByText(text))
}

func TestViewModel(t *testing.T) {
	for _, tt := range []struct {
		name               string
		st                 Status
		state              string
		canStart, canStop  bool
		checklist          bool
		addresses, viewOnl bool
	}{
		{"first start", Status{}, TextStopped, false, false, true, false, false},
		{"no login", Status{ScreenRecording: true, Accessibility: true}, TextStopped, false, false, true, false, false},
		{"no screen recording", Status{Accessibility: true, LoginSet: true}, TextStopped, false, false, true, false, false},
		{"no accessibility", Status{ScreenRecording: true, LoginSet: true}, TextStopped, true, false, true, false, false},
		{"ready", ready(), TextStopped, true, false, false, false, false},
		{"listening", func() Status {
			s := ready()
			s.Server, s.Addresses, s.Fingerprint = ServerListening, []string{"10.0.0.1:3389"}, "ab"
			return s
		}(), TextListening, false, true, false, true, false},
		{"connected view only", func() Status {
			s := ready()
			s.Server, s.Addresses, s.ViewOnly = ServerConnected, []string{"10.0.0.1:3389"}, true
			return s
		}(), TextConnected, false, true, false, true, true},
		{"stopped keeps no addresses", func() Status {
			s := ready()
			s.Addresses, s.ViewOnly = []string{"stale"}, true
			return s
		}(), TextStopped, true, false, false, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.st.Video = TextVideoPlanar
			vm := NewViewModel(tt.st)
			if want := tt.state == TextConnected; (vm.Video == TextVideoPlanar) != want || (vm.Video == "") == want {
				t.Errorf("video path %q", vm.Video)
			}
			if vm.State != tt.state || vm.CanStart != tt.canStart || vm.CanStop != tt.canStop ||
				(vm.Checklist != nil) != tt.checklist || (len(vm.Addresses) > 0) != tt.addresses || vm.ViewOnly != tt.viewOnl {
				t.Errorf("view model %+v", vm)
			}
			if vm.Checklist != nil {
				if len(vm.Checklist) != 3 {
					t.Fatalf("checklist %+v", vm.Checklist)
				}
				for i, done := range []bool{tt.st.ScreenRecording, tt.st.Accessibility, tt.st.LoginSet} {
					c := vm.Checklist[i]
					if c.Done != done || (c.Action == ActionNone) != (done && i < 2) {
						t.Errorf("line %d: %+v", i, c)
					}
				}
			}
		})
	}
}

func TestStatusEqual(t *testing.T) {
	a := ready()
	a.Addresses = []string{"x"}
	for _, tt := range []struct {
		name   string
		change func(s *Status)
		equal  bool
	}{
		{"same", func(*Status) {}, true},
		{"addresses", func(s *Status) { s.Addresses = []string{"y"} }, false},
		{"server", func(s *Status) { s.Server = ServerConnected }, false},
		{"permission", func(s *Status) { s.Accessibility = false }, false},
		{"problem", func(s *Status) { s.Problem = "p" }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := a
			b.Addresses = slices.Clone(a.Addresses)
			tt.change(&b)
			if a.Equal(b) != tt.equal {
				t.Errorf("Equal = %v", !tt.equal)
			}
		})
	}
}

// TestFirstStart: on the first start the checklist shows its three lines,
// Start cannot be pressed, the buttons open System Settings, and the lines
// follow what the user grants there, by themselves.
func TestFirstStart(t *testing.T) {
	b := &fakeBackend{}
	ts := newTester(t, b)
	for _, text := range []string{TextStopped, TextChecklist, TextScreenRecording, TextAccessibility, TextLogin} {
		if !shows(ts, text) {
			t.Errorf("the window does not show %q", text)
		}
	}
	if ts.Exists(gluttertest.ByTextContaining("bypass the system private window picker")) == false {
		t.Error("the Screen Recording line does not explain macOS's recurring question")
	}
	if enabled(t, ts, KeyStart) || enabled(t, ts, KeyStop) {
		t.Error("Start or Stop can be pressed")
	}
	if n := ts.Count(gluttertest.ByIcon(m.Icons.CheckCircle())); n != 0 {
		t.Errorf("%d lines are done", n)
	}

	ts.Tap(gluttertest.ByKey(KeyCheck(TextScreenRecording)))
	ts.Tap(gluttertest.ByKey(KeyCheck(TextAccessibility)))
	settle(t, ts, "the panes opened", func() bool { _, _, opened, _ := b.calls(); return len(opened) == 2 })
	if _, _, opened, _ := b.calls(); !slices.Equal(opened, []host.Permission{host.ScreenRecording, host.Accessibility}) &&
		!slices.Equal(opened, []host.Permission{host.ScreenRecording}) {
		t.Errorf("opened %v", opened)
	}

	b.set(func(st *Status) { st.ScreenRecording = true })
	ts.Pump(PollInterval)
	if n := ts.Count(gluttertest.ByIcon(m.Icons.CheckCircle())); n != 1 {
		t.Errorf("%d lines are done after Screen Recording was granted, want 1", n)
	}
	if ts.Exists(gluttertest.ByKey(KeyCheck(TextScreenRecording))) {
		t.Error("the granted line still has its button")
	}
	b.set(func(st *Status) { st.Accessibility = true })
	ts.Pump(PollInterval)
	if n := ts.Count(gluttertest.ByIcon(m.Icons.CheckCircle())); n != 2 {
		t.Errorf("%d lines are done after Accessibility was granted, want 2", n)
	}
	if !shows(ts, TextChecklist) || enabled(t, ts, KeyStart) {
		t.Error("the checklist went away, or Start can be pressed, without a password")
	}
}

func TestOpenSettingsOnce(t *testing.T) {
	b := &fakeBackend{st: Status{Accessibility: true, LoginSet: true}}
	ts := newTester(t, b)
	ts.Tap(gluttertest.ByKey(KeyCheck(TextScreenRecording)))
	settle(t, ts, "the pane opened", func() bool { _, _, opened, _ := b.calls(); return len(opened) == 1 })
	ts.PumpAndSettle()
	if _, _, opened, _ := b.calls(); !slices.Equal(opened, []host.Permission{host.ScreenRecording}) {
		t.Errorf("opened %v", opened)
	}
}

func TestSetLogin(t *testing.T) {
	b := &fakeBackend{st: Status{ScreenRecording: true, Accessibility: true}}
	ts := newTester(t, b)
	if ts.Exists(gluttertest.ByKey(KeyPassword)) {
		t.Fatal("the form shows before Set is pressed")
	}
	ts.Tap(gluttertest.ByKey(KeyCheck(TextLogin)))
	ts.PumpAndSettle()
	if !ts.Exists(gluttertest.ByKey(KeyPassword)) {
		t.Fatal("Set shows no form")
	}

	ts.EnterText(gluttertest.ByKey(KeyUser), "alice")
	ts.EnterText(gluttertest.ByKey(KeyPassword), "short")
	ts.Tap(gluttertest.ByKey(KeySave))
	settle(t, ts, "the refusal", func() bool { return shows(ts, "the password has fewer than 8 characters") })
	if !ts.Exists(gluttertest.ByKey(KeyPassword)) {
		t.Error("the form closed after a refusal")
	}

	ts.EnterText(gluttertest.ByKey(KeyPassword), "a long password")
	ts.Tap(gluttertest.ByKey(KeySave))
	settle(t, ts, "the form closed", func() bool { return !ts.Exists(gluttertest.ByKey(KeyPassword)) })
	if _, _, _, logins := b.calls(); !slices.Equal(logins, []string{"alice:a long password"}) {
		t.Errorf("logins %q", logins)
	}
	ts.PumpAndSettle()
	if shows(ts, TextChecklist) {
		t.Error("the checklist stays when everything is done")
	}
	if !enabled(t, ts, KeyStart) {
		t.Error("Start cannot be pressed when everything is done")
	}
	if shows(ts, "a long password") {
		t.Error("the window shows the password")
	}
}

func TestCancelLogin(t *testing.T) {
	b := &fakeBackend{st: Status{ScreenRecording: true, User: "bob", LoginSet: true}}
	ts := newTester(t, b)
	if !shows(ts, TextChangeLogin) {
		t.Fatal("a set login has no Change button")
	}
	ts.Tap(gluttertest.ByKey(KeyCheck(TextLogin)))
	ts.PumpAndSettle()
	ts.EnterText(gluttertest.ByKey(KeyPassword), "never saved")
	ts.Tap(gluttertest.ByKey(KeyCancel))
	ts.PumpAndSettle()
	if ts.Exists(gluttertest.ByKey(KeyPassword)) {
		t.Error("Cancel leaves the form open")
	}
	if _, _, _, logins := b.calls(); len(logins) != 0 {
		t.Errorf("logins %q", logins)
	}
}

func TestStartStop(t *testing.T) {
	b := &fakeBackend{st: ready()}
	ts := newTester(t, b)
	if shows(ts, TextChecklist) {
		t.Error("the checklist shows when everything is done")
	}
	ts.Tap(gluttertest.ByKey(KeyStart))
	settle(t, ts, "listening", func() bool { return shows(ts, TextListening) })
	for _, text := range []string{TextAddresses, "192.168.1.20:3389", "[fd00::20]:3389", TextFingerprint, "ab:cd:ef"} {
		if !shows(ts, text) {
			t.Errorf("the window does not show %q", text)
		}
	}
	if shows(ts, TextViewOnly) {
		t.Error("the sessions are said to be view only")
	}
	if enabled(t, ts, KeyStart) || !enabled(t, ts, KeyStop) {
		t.Error("Start can be pressed, or Stop cannot, while listening")
	}

	b.set(func(st *Status) { st.Server, st.Video = ServerConnected, TextVideoAVC420 })
	ts.Pump(PollInterval)
	if !shows(ts, TextConnected) || !shows(ts, TextVideo+TextVideoAVC420) {
		t.Error("the window does not show the connection and its video path")
	}

	ts.Tap(gluttertest.ByKey(KeyStop))
	settle(t, ts, "stopped", func() bool { return shows(ts, TextStopped) })
	if shows(ts, "ab:cd:ef") || shows(ts, TextAddresses) {
		t.Error("the stopped server's fingerprint or addresses show")
	}
	if starts, stops, _, _ := b.calls(); starts != 1 || stops != 1 {
		t.Errorf("%d starts and %d stops", starts, stops)
	}
}

func TestStartFails(t *testing.T) {
	b := &fakeBackend{st: ready(), startErr: errors.New("TestListenFailed", "port 3389 is taken")}
	ts := newTester(t, b)
	ts.Tap(gluttertest.ByKey(KeyStart))
	settle(t, ts, "the failure", func() bool { return shows(ts, "port 3389 is taken") })
	if !shows(ts, TextStopped) || !enabled(t, ts, KeyStart) {
		t.Error("after a failed start the server is not stopped, or Start cannot be pressed again")
	}
}

func TestViewOnlyWithoutAccessibility(t *testing.T) {
	st := ready()
	st.Accessibility = false
	b := &fakeBackend{st: st}
	ts := newTester(t, b)
	if !shows(ts, TextChecklist) || !enabled(t, ts, KeyStart) {
		t.Fatal("without Accessibility the checklist hides, or Start cannot be pressed")
	}
	ts.Tap(gluttertest.ByKey(KeyStart))
	settle(t, ts, "listening", func() bool { return shows(ts, TextListening) })
	if !shows(ts, TextViewOnly) {
		t.Error("the window does not say that the sessions are view only")
	}
}

// TestPermissionWithdrawn: the checklist comes back when a permission is
// withdrawn, and the server's own stop shows why.
func TestPermissionWithdrawn(t *testing.T) {
	st := ready()
	st.Server, st.Addresses = ServerListening, []string{"10.0.0.1:3389"}
	b := &fakeBackend{st: st}
	ts := newTester(t, b)
	if shows(ts, TextChecklist) {
		t.Fatal("the checklist shows when everything is done")
	}
	b.set(func(st *Status) {
		st.ScreenRecording = false
		st.Server, st.Addresses, st.Problem = ServerStopped, nil, "Screen Recording was withdrawn"
	})
	ts.Pump(PollInterval)
	if !shows(ts, TextChecklist) || !ts.Exists(gluttertest.ByKey(KeyCheck(TextScreenRecording))) {
		t.Error("the checklist does not come back")
	}
	if !shows(ts, TextWhyStopped+"Screen Recording was withdrawn") {
		t.Error("the window does not say why the server stopped")
	}
	if enabled(t, ts, KeyStart) {
		t.Error("Start can be pressed without Screen Recording")
	}
}
