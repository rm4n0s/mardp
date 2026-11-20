package ui

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rm4n0s/glutter/gluttertest"
	m "github.com/rm4n0s/glutter/material"
	gui "github.com/rm4n0s/glutter/ui"
	"github.com/rm4n0s/mardp/internal/config"
	"github.com/rm4n0s/mardp/internal/host"
)

// busiest is the status that shows the most: a session, every line of the
// status card, as many events as the window lists and more, a pending
// change.
func busiest() Status {
	st := ready()
	st.Version = "v1.0.4"
	st.Server = ServerConnected
	st.Addresses = []string{"192.168.1.20:3389", "10.0.0.7:3389", "[fd00:1234:5678:9abc::20]:3389"}
	st.Fingerprint = strings.TrimSuffix(strings.Repeat("ab:", 32), ":")
	st.ViewOnly = true
	st.Video = TextVideoAVC420
	st.Pending = true
	st.LoginApproval = true
	st.Settings = config.Settings{Port: 3389, MaxFPS: 60, MaxBitrate: 20_000_000, Sound: config.SoundClient, SwapCtrlCommand: true}
	st.Displays = []host.Display{{ID: "d", Name: "Studio Display", Width: 5120, Height: 2880, PointWidth: 2560, PointHeight: 1440, Main: true}}
	at := time.Date(2026, 11, 20, 21, 30, 0, 0, time.Local)
	st.Session = host.SessionInfo{Client: "linux-desktop", Remote: "192.168.1.55", Since: at, Width: 1920, Height: 1080,
		Sound: "AAC", FPS: 60, Bitrate: 12_300_000, Delay: 42 * time.Millisecond}
	for i := range EventsShown + 4 {
		e := Event{At: at.Add(time.Duration(i) * time.Minute), Kind: EventLoginFailed, User: "somebody", Remote: "192.168.1.77", Failures: 5 + i, Wait: time.Minute}
		switch i % 3 {
		case 1:
			e = Event{At: e.At, Kind: EventConnected, Client: "linux-desktop", Remote: "192.168.1.55", User: "alice"}
		case 2:
			e = Event{At: e.At, Kind: EventDisconnected, Client: "linux-desktop", Ended: host.EndedReplaced}
		}
		st.Events = append(st.Events, e)
	}
	st.EventTotal = len(st.Events)
	return st
}

// TestWindowFits: the window as it opens shows everything without
// scrolling (SPEC §12.2), whatever the app has to show. With
// MARDP_UI_SHOTS=<folder> it writes a picture of each state there.
func TestWindowFits(t *testing.T) {
	first := Status{Version: "v1.0.4", Settings: busiest().Settings}
	tests := []struct {
		name string
		st   Status
		do   func(t *testing.T, ts *gluttertest.Tester)
	}{
		{"first run", first, nil},
		{"first run, the login's form", first, func(t *testing.T, ts *gluttertest.Tester) {
			ts.Tap(gluttertest.ByKey(KeyCheck(TextLogin)))
			ts.Pump()
			ts.EnterText(gluttertest.ByKey(KeyPassword), "short")
			ts.Tap(gluttertest.ByKey(KeySave))
			settle(t, ts, "the password refused", func() bool { return shows(ts, "the password has fewer than 8 characters") })
		}},
		{"listening", func() Status {
			st := busiest()
			st.Server, st.Session, st.Video, st.Pending = ServerListening, host.SessionInfo{}, "", false
			return st
		}(), nil},
		{"connected", busiest(), nil},
		{"connected, the login's form", busiest(), func(t *testing.T, ts *gluttertest.Tester) {
			ts.Tap(gluttertest.ByKey(KeyChangeLogin))
			ts.Pump()
			ts.EnterText(gluttertest.ByKey(KeyPassword), "short")
			ts.Tap(gluttertest.ByKey(KeySave))
			settle(t, ts, "the password refused", func() bool { return shows(ts, "the password has fewer than 8 characters") })
		}},
		{"connected, fields refused", busiest(), func(t *testing.T, ts *gluttertest.Tester) {
			ts.EnterText(gluttertest.ByKey(KeyPort), "0")
			ts.EnterText(gluttertest.ByKey(KeyAddress), "nowhere")
			ts.EnterText(gluttertest.ByKey(KeyBitrate), "0")
			ts.Tap(gluttertest.ByKey(KeySaveSettings))
			ts.Pump()
			if !shows(ts, FieldMessage(FieldPort)) {
				t.Fatal("the port was not refused")
			}
		}},
		{"stopped with problems", func() Status {
			st := busiest()
			st.Server, st.Session, st.Video, st.Addresses, st.Fingerprint, st.ViewOnly = ServerStopped, host.SessionInfo{}, "", nil, "", false
			st.Problem = "the display went away while a client was connected, and no other display is there to show"
			st.StartProblem = "listen tcp 0.0.0.0:3389: bind: address already in use"
			st.LoginProblem = "macOS refused to add mardp to the login items"
			return st
		}(), nil},
	}
	shots := os.Getenv("MARDP_UI_SHOTS")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &fakeBackend{st: tt.st}
			ts, _ := newSizedTester(t, b, m.Size{Width: WindowWidth, Height: WindowHeight})
			if tt.do != nil {
				tt.do(t, ts)
			}
			ts.PumpAndSettle()
			view, content := ts.Size(gluttertest.ByKey(KeyScroll)), ts.Size(gluttertest.ByKey(KeyContent))
			t.Logf("the window is %d tall, what it holds %.0f", WindowHeight, content.Height)
			if content.Height > view.Height || content.Height > WindowHeight {
				t.Errorf("the window scrolls: it holds %.0f pixels in %.0f", content.Height, view.Height)
			}
			if n := ts.Count(gluttertest.ByTextContaining("  ")); len(tt.st.Events) > 0 && n != EventsShown {
				t.Errorf("%d events listed, want %d", n, EventsShown)
			}
			if shots == "" {
				return
			}
			var buf bytes.Buffer
			if err := png.Encode(&buf, ts.Screenshot()); err != nil {
				t.Fatal(err)
			}
			name := fmt.Sprintf("%s.png", strings.NewReplacer(" ", "-", ",", "", "'", "").Replace(tt.name))
			if err := os.WriteFile(filepath.Join(shots, name), buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestWindowNarrow: a window made narrow stacks its cards and scrolls, the
// login's form in the checklist's card.
func TestWindowNarrow(t *testing.T) {
	b := &fakeBackend{st: busiest()}
	ts, _ := newSizedTester(t, b, m.Size{Width: wideFrom - 1, Height: 3000})
	if content := ts.Size(gluttertest.ByKey(KeyContent)); content.Height <= WindowHeight {
		t.Errorf("the narrow window holds %.0f pixels: its cards are not one under the other", content.Height)
	}
	if shows(ts, TextChecklist) {
		t.Fatal("the checklist shows with everything done")
	}
	ts.Tap(gluttertest.ByKey(KeyChangeLogin))
	ts.Pump()
	if !shows(ts, TextChecklist) || !ts.Exists(gluttertest.ByKey(KeyPassword)) {
		t.Error("no form in the checklist's card")
	}
}

// TestExitRequests: the close button hides the window and the app goes
// on; a quit and the end of the session let the app end (SPEC §7.3).
func TestExitRequests(t *testing.T) {
	tests := []struct {
		reason gui.AppExitReason
		closes bool
	}{
		{gui.AppExitReasonWindowClose, false},
		{gui.AppExitReasonUnspecified, false},
		{gui.AppExitReasonQuit, true},
		{gui.AppExitReasonSessionEnd, true},
	}
	for _, tt := range tests {
		t.Run(tt.reason.String(), func(t *testing.T) {
			ts, _ := newSizedTester(t, &fakeBackend{st: ready()}, m.Size{Width: WindowWidth, Height: WindowHeight})
			if got := ts.RequestWindowCloseFor(gui.ImplicitViewID, tt.reason); got != tt.closes {
				t.Errorf("the window closes: %v, want %v", got, tt.closes)
			}
		})
	}
}
