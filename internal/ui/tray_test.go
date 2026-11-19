package ui

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/rm4n0s/glutter/gluttertest"
)

// TestTrayMenu: the menu-bar icon's menu for each state of the server.
func TestTrayMenu(t *testing.T) {
	connected := ready()
	connected.Server, connected.Session = ServerConnected, testSession()
	notReady := ready()
	notReady.LoginSet = false
	listening := ready()
	listening.Server = ServerListening
	item := func(label string, a TrayAction, disabled bool) TrayItem {
		return TrayItem{Label: label, Action: a, Disabled: disabled}
	}
	sep := TrayItem{Separator: true}
	tail := []TrayItem{sep, item(TextShowWindow, TrayShowWindow, false), item(TextQuit, TrayQuit, false)}
	tests := []struct {
		name string
		st   Status
		busy bool
		want []TrayItem
	}{
		{"stopped", ready(), false, append([]TrayItem{item(TextStopped, TrayNone, true), sep, item(TextStart, TrayStart, false)}, tail...)},
		{"stopped, the login not set", notReady, false, append([]TrayItem{item(TextStopped, TrayNone, true), sep, item(TextStart, TrayStart, true)}, tail...)},
		{"listening", listening, false, append([]TrayItem{item(TextListening, TrayNone, true), sep, item(TextStop, TrayStop, false)}, tail...)},
		{"connected", connected, false, append([]TrayItem{item(TextConnected, TrayNone, true), sep,
			item(TextStop, TrayStop, false), item(TextDisconnect, TrayDisconnect, false)}, tail...)},
		{"connected and busy", connected, true, append([]TrayItem{item(TextConnected, TrayNone, true), sep,
			item(TextStop, TrayStop, true), item(TextDisconnect, TrayDisconnect, true)}, tail...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewTrayMenu(NewViewModel(tt.st), tt.busy); !slices.Equal(got, tt.want) {
				t.Errorf("menu\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

// TestTrayIcon: an icon for each state, different from the others, black
// as a template image is, in the tray's five sizes.
func TestTrayIcon(t *testing.T) {
	var pictures [][]byte
	for _, st := range []ServerState{ServerStopped, ServerListening, ServerConnected} {
		icon, err := TrayIcon(st)
		if err != nil {
			t.Fatal(err)
		}
		var sizes []int
		for _, img := range icon.Images {
			sizes = append(sizes, img.Size)
		}
		if !slices.Equal(sizes, []int{16, 22, 24, 32, 48}) {
			t.Errorf("state %d: sizes %v", st, sizes)
		}
		big := icon.Images[len(icon.Images)-1].RGBA
		opaque := 0
		for i := 0; i < len(big); i += 4 {
			if big[i] != 0 || big[i+1] != 0 || big[i+2] != 0 {
				t.Fatalf("state %d: a pixel that is not black", st)
			}
			if big[i+3] > 0x80 {
				opaque++
			}
		}
		if opaque == 0 {
			t.Errorf("state %d: an empty icon", st)
		}
		for i, p := range pictures {
			if slices.Equal(p, big) {
				t.Errorf("state %d draws the icon of state %d", st, i)
			}
		}
		pictures = append(pictures, big)
	}
}

// trayMenuIs waits until the fake desktop's menu has the labels want.
func trayMenuIs(t *testing.T, ts *gluttertest.Tester, fd *gluttertest.FakeDesktop, want ...string) {
	t.Helper()
	settle(t, ts, "the menu "+strings.Join(want, " | "), func() bool { return slices.Equal(fd.TrayMenu(), want) })
}

// TestTrayInWindow: the icon's menu follows the server, and its items do
// what they say.
func TestTrayInWindow(t *testing.T) {
	b := &fakeBackend{st: ready()}
	ts, fd := newDesktopTester(t, b, 900)
	if got := fd.TrayProperty("Title"); got != TextTrayTitle {
		t.Errorf("title %v", got)
	}
	stopped := fd.TrayProperty("IconPixmap")
	trayMenuIs(t, ts, fd, TextStopped, "-", TextStart, "-", TextShowWindow, TextQuit)

	fd.ClickTrayMenuItem(TextStart)
	trayMenuIs(t, ts, fd, TextListening, "-", TextStop, "-", TextShowWindow, TextQuit)
	if starts, _, _, _ := b.calls(); starts != 1 {
		t.Errorf("%d starts", starts)
	}
	if !shows(ts, TextListening) {
		t.Error("the window does not show the server listening")
	}
	listening := fd.TrayProperty("IconPixmap")
	if reflect.DeepEqual(stopped, listening) {
		t.Error("the icon did not change when the server started")
	}

	b.set(func(st *Status) { st.Server, st.Session = ServerConnected, testSession() })
	ts.Pump(PollInterval)
	trayMenuIs(t, ts, fd, TextConnected, "-", TextStop, TextDisconnect, "-", TextShowWindow, TextQuit)
	if reflect.DeepEqual(listening, fd.TrayProperty("IconPixmap")) {
		t.Error("the icon did not change when a client connected")
	}
	fd.ClickTrayMenuItem(TextDisconnect)
	trayMenuIs(t, ts, fd, TextListening, "-", TextStop, "-", TextShowWindow, TextQuit)
	b.mu.Lock()
	n := b.disconnects
	b.mu.Unlock()
	if n != 1 {
		t.Errorf("%d disconnects", n)
	}

	fd.ClickTrayMenuItem(TextStop)
	trayMenuIs(t, ts, fd, TextStopped, "-", TextStart, "-", TextShowWindow, TextQuit)
	if _, stops, _, _ := b.calls(); stops != 1 {
		t.Errorf("%d stops", stops)
	}
}

// TestCloseHidesWindow: closing the window hides it and the app goes on;
// Show window brings it back; Quit ends the app, with the window shown or
// hidden.
func TestCloseHidesWindow(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		t.Run(map[bool]string{false: "shown", true: "hidden"}[hidden], func(t *testing.T) {
			b := &fakeBackend{st: ready()}
			ts, fd := newDesktopTester(t, b, 900)
			view := ts.View.ViewID()
			if ts.Dispatcher().RequestClose(view) {
				t.Error("the window was allowed to close")
			}
			if !ts.WindowHidden(view) || ts.ExitRequests() != 0 {
				t.Errorf("hidden %v, %d exit requests", ts.WindowHidden(view), ts.ExitRequests())
			}
			fd.ClickTrayMenuItem(TextShowWindow)
			if ts.WindowHidden(view) {
				t.Error("Show window did not bring the window back")
			}
			if hidden {
				ts.Dispatcher().RequestClose(view)
			}
			fd.ClickTrayMenuItem(TextQuit)
			if n := ts.ExitRequests(); n != 1 {
				t.Errorf("%d exit requests after Quit", n)
			}
			if !ts.Dispatcher().RequestClose(view) {
				t.Error("the window cannot close after Quit")
			}
		})
	}
}
