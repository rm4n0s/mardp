package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/rm4n0s/glutter/gluttertest"
	"github.com/rm4n0s/mardp/internal/host"
)

// testSince is when the test sessions opened, in the zone the window
// writes times in.
var testSince = time.Date(2026, 11, 19, 14, 3, 21, 0, time.Local)

func testSession() host.SessionInfo {
	return host.SessionInfo{Client: "laptop", Remote: "192.168.1.30:51234", Since: testSince, Width: 1920, Height: 1080,
		Sound: "AAC", FPS: 29.6, Bitrate: 4_250_000, Delay: 34600 * time.Microsecond}
}

func TestSessionLines(t *testing.T) {
	tests := []struct {
		name string
		edit func(i *host.SessionInfo)
		want []string
	}{
		{"H.264", nil, []string{
			"Client: laptop at 192.168.1.30:51234", "Connected since 14:03:21", "Desktop: 1920×1080",
			TextVideo + TextVideoAVC420, "Sound: AAC", "Frames a second: 30", "Bitrate: 4.2 Mbit/s", "Delay: 35 ms",
		}},
		{"bitmaps without sound", func(i *host.SessionInfo) { i.Sound, i.Delay, i.FPS, i.Bitrate = "", 0, 0, 0 }, []string{
			"Client: laptop at 192.168.1.30:51234", "Connected since 14:03:21", "Desktop: 1920×1080",
			TextVideo + TextVideoAVC420, "Sound: " + TextSoundNone, "Frames a second: 0", "Bitrate: 0.0 Mbit/s",
			"Delay: " + TextDelayBitmaps,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := testSession()
			if tt.edit != nil {
				tt.edit(&info)
			}
			if got := SessionLines(info, TextVideoAVC420); !slices.Equal(got, tt.want) {
				t.Errorf("lines\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestEventText(t *testing.T) {
	ev := func(kind EventKind, ended host.Ended) Event {
		return Event{At: testSince, Kind: kind, Client: "laptop", Remote: "192.168.1.30:51234", User: "alice", Ended: ended}
	}
	tests := []struct {
		e    Event
		want string
	}{
		{ev(EventConnected, 0), `14:03:21  laptop connected from 192.168.1.30:51234 as "alice"`},
		{ev(EventDisconnected, host.EndedByClient), "14:03:21  laptop disconnected: " + TextEndedByClient},
		{ev(EventDisconnected, host.EndedByOwner), "14:03:21  laptop disconnected: " + TextEndedByOwner},
		{ev(EventDisconnected, host.EndedByHost), "14:03:21  laptop disconnected: " + TextEndedByHost},
		{ev(EventDisconnected, host.EndedReplaced), "14:03:21  laptop disconnected: " + TextEndedReplaced},
		{ev(EventDisconnected, host.EndedServerStopped), "14:03:21  laptop disconnected: " + TextEndedServerStopped},
		{ev(EventLoginFailed, 0), `14:03:21  Failed login as "alice" from 192.168.1.30:51234`},
		{Event{At: testSince, Kind: EventLoginFailed, Remote: "192.168.1.31:40000", User: "root", Failures: 6, Wait: 2 * time.Minute},
			`14:03:21  Failed login as "root" from 192.168.1.31:40000: 6 failures, the address waits 2 minutes`},
		{ev(0, 0), "14:03:21  ?"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := EventText(tt.e); got != tt.want {
				t.Errorf("got %q", got)
			}
		})
	}
}

// TestSessionInWindow: the connected session's details and numbers, which
// follow the backend, Disconnect, and the recent events, the newest first.
func TestSessionInWindow(t *testing.T) {
	st := ready()
	st.Server, st.Addresses, st.Video, st.Session = ServerConnected, []string{"10.0.0.1:3389"}, TextVideoAVC420, testSession()
	failed := Event{At: testSince.Add(-time.Minute), Kind: EventLoginFailed, Remote: "192.168.1.31:40000", User: "root"}
	connected := Event{At: testSince, Kind: EventConnected, Client: "laptop", Remote: "192.168.1.30:51234", User: "alice"}
	st.Events = []Event{failed, connected}
	b := &fakeBackend{st: st}
	ts := newTester(t, b)
	for _, l := range append(SessionLines(testSession(), TextVideoAVC420), EventText(failed), EventText(connected)) {
		if !shows(ts, l) {
			t.Errorf("the window does not show %q", l)
		}
	}
	if shows(ts, TextNoEvents) {
		t.Error("no events, it says")
	}
	if ts.TopLeft(gluttertest.ByText(EventText(connected))).Dy >= ts.TopLeft(gluttertest.ByText(EventText(failed))).Dy {
		t.Error("the newest event is not first")
	}

	b.set(func(st *Status) {
		st.Session.FPS, st.Video, st.Session.Delay = 60, TextVideoBitmaps, 0
	})
	ts.Pump(PollInterval)
	for _, l := range []string{"Frames a second: 60", TextVideo + TextVideoBitmaps, "Delay: " + TextDelayBitmaps} {
		if !shows(ts, l) {
			t.Errorf("the window does not show %q after the change", l)
		}
	}

	ts.Tap(gluttertest.ByKey(KeyDisconnect))
	settle(t, ts, "the session ended", func() bool { return shows(ts, TextListening) })
	if ts.Exists(gluttertest.ByKey(KeyDisconnect)) || shows(ts, "Client: laptop at 192.168.1.30:51234") {
		t.Error("the ended session's details or its Disconnect show")
	}
	if !shows(ts, "14:04:21  laptop disconnected: "+TextEndedByOwner) {
		t.Error("the window does not show the disconnection")
	}
	b.mu.Lock()
	n := b.disconnects
	b.mu.Unlock()
	if n != 1 {
		t.Errorf("%d disconnects", n)
	}
}

// TestNoEvents says so.
func TestNoEvents(t *testing.T) {
	ts := newTester(t, &fakeBackend{st: ready()})
	if !shows(ts, TextEvents) || !shows(ts, TextNoEvents) {
		t.Error("the window does not say that nothing happened yet")
	}
	if ts.Exists(gluttertest.ByKey(KeyDisconnect)) {
		t.Error("Disconnect shows without a session")
	}
}
