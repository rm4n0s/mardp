package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/rm4n0s/glutter/gluttertest"
	"github.com/rm4n0s/mardp/internal/host"
)

func TestWaitText(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{time.Second, "1 second"},
		{30 * time.Second, "30 seconds"},
		{time.Minute, "1 minute"},
		{16 * time.Minute, "16 minutes"},
		{90 * time.Minute, "90 minutes"},
		{time.Hour, "1 hour"},
		{2 * time.Hour, "2 hours"},
	}
	for _, tt := range tests {
		if got := WaitText(tt.d); got != tt.want {
			t.Errorf("WaitText(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestWaitRuleText(t *testing.T) {
	got := WaitRuleText()
	for _, want := range []string{"After 5 failed logins", "waits 1 minute", "at most 1 hour", "a day without a failure"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q does not say %q", got, want)
		}
	}
}

// TestNotificationOf: the three notifications, and the events without one.
func TestNotificationOf(t *testing.T) {
	remote := "192.168.1.31:40000"
	tests := []struct {
		name        string
		e           Event
		title, body string
		ok          bool
	}{
		{"connected", Event{Kind: EventConnected, Client: "laptop", Remote: remote, User: "alice"},
			"laptop connected", `From 192.168.1.31:40000 as "alice".`, true},
		{"disconnected", Event{Kind: EventDisconnected, Client: "laptop", Remote: remote, Ended: host.EndedReplaced},
			"laptop disconnected", "Another client connected.", true},
		{"a failed login", Event{Kind: EventLoginFailed, Remote: remote, User: "root", Failures: 4}, "", "", false},
		{"a failed login that makes the address wait", Event{Kind: EventLoginFailed, Remote: remote, User: "root", Failures: 5, Wait: time.Minute},
			"Failed logins from 192.168.1.31", `5 failed logins, the last as "root": the address waits 1 minute.`, true},
		{"an IPv6 address", Event{Kind: EventLoginFailed, Remote: "[fd00::31]:40000", User: "root", Failures: 6, Wait: 2 * time.Minute},
			"Failed logins from fd00::31", `6 failed logins, the last as "root": the address waits 2 minutes.`, true},
		{"an unknown event", Event{}, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, body, ok := NotificationOf(tt.e)
			if title != tt.title || body != tt.body || ok != tt.ok {
				t.Errorf("%q, %q, %v", title, body, ok)
			}
		})
	}
}

// notifications waits until the fake desktop has n notifications and
// returns their summaries and bodies.
func notifications(t *testing.T, ts *gluttertest.Tester, fd *gluttertest.FakeDesktop, n int) []string {
	t.Helper()
	settle(t, ts, "the notifications", func() bool { return len(fd.Notifications()) >= n })
	var got []string
	for _, nt := range fd.Notifications() {
		got = append(got, nt.Summary+" | "+nt.Body)
	}
	if len(got) != n {
		t.Errorf("notifications %q, want %d", got, n)
	}
	return got
}

// TestNotifications: the window notifies of the events it had not seen,
// one failed-login notification a wait, and a click on a notification
// brings the window back. The waiting rule shows with failed logins.
func TestNotifications(t *testing.T) {
	st := ready()
	st.Server = ServerListening
	st.Events, st.EventTotal = []Event{{At: testSince, Kind: EventConnected, Client: "old", Remote: "10.0.0.9:1", User: "alice"}}, 7
	b := &fakeBackend{st: st}
	ts, fd := newDesktopTester(t, b, 1200)
	if shows(ts, WaitRuleText()) {
		t.Error("the waiting rule shows without failed logins")
	}

	connected := Event{At: testSince, Kind: EventConnected, Client: "laptop", Remote: "192.168.1.30:51234", User: "alice"}
	b.set(func(st *Status) {
		st.Server, st.Session = ServerConnected, testSession()
		st.Events, st.EventTotal = append(st.Events, connected), st.EventTotal+1
	})
	ts.Pump(PollInterval)
	got := notifications(t, ts, fd, 1)
	if got[0] != `laptop connected | From 192.168.1.30:51234 as "alice".` {
		t.Errorf("notification %q", got[0])
	}

	// Five failures, of which the last makes the address wait: one
	// notification.
	b.set(func(st *Status) {
		for i := 1; i <= 5; i++ {
			e := Event{At: testSince, Kind: EventLoginFailed, Remote: "192.168.1.31:40000", User: "root", Failures: i}
			if i == 5 {
				e.Wait = time.Minute
			}
			st.Events = append(st.Events, e)
		}
		st.EventTotal += 5
	})
	ts.Pump(PollInterval)
	got = notifications(t, ts, fd, 2)
	if got[1] != `Failed logins from 192.168.1.31 | 5 failed logins, the last as "root": the address waits 1 minute.` {
		t.Errorf("notification %q", got[1])
	}
	if !shows(ts, WaitRuleText()) {
		t.Error("the waiting rule does not show")
	}

	// A click on the notification brings the hidden window back.
	view := ts.View.ViewID()
	ts.Dispatcher().RequestClose(view)
	fd.InvokeNotificationAction(fd.Notifications()[1].ID, "default")
	settle(t, ts, "the window back", func() bool { return !ts.WindowHidden(view) })

	ts.Tap(gluttertest.ByKey(KeyDisconnect))
	got = notifications(t, ts, fd, 3)
	if got[2] != "laptop disconnected | Disconnected from this window." {
		t.Errorf("notification %q", got[2])
	}
}
