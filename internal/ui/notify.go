package ui

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rm4n0s/glutter/desktop"
	"github.com/rm4n0s/mardp/internal/config"
)

// The texts of the notifications (SPEC §12.4).
const (
	// TextNotifyFailed is said when a notification could not be shown.
	TextNotifyFailed = "A notification could not be shown: "
	// textAppName is the app's name on the notifications.
	textAppName = "mardp"
)

// WaitText says a wait in words: "1 minute", "16 minutes", "1 hour".
func WaitText(d time.Duration) string {
	n, unit := int64(d.Round(time.Second)/time.Second), "second"
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		n, unit = int64(d/time.Hour), "hour"
	case d >= time.Minute:
		n, unit = int64(d.Round(time.Minute)/time.Minute), "minute"
	}
	if n != 1 {
		unit += "s"
	}
	return strconv.FormatInt(n, 10) + " " + unit
}

// WaitRuleText is the waiting rule for failed logins, as the window shows
// it (SPEC §8.2).
func WaitRuleText() string {
	return fmt.Sprintf("After %d failed logins from one address, the address waits %s, doubling to at most %s; "+
		"a login while it waits is refused. A successful login, or a day without a failure, forgets its failures.",
		config.FreeFailures, WaitText(config.FirstWait), WaitText(config.MaxWait))
}

// NotificationOf is the notification of e (SPEC §12.4): ok is false when
// e has none. A failed login has one only when it makes the address wait,
// so an address gets one a waiting period, not one an attempt.
func NotificationOf(e Event) (title, body string, ok bool) {
	switch e.Kind {
	case EventConnected:
		return e.Client + " connected", fmt.Sprintf("From %s as %q.", e.Remote, e.User), true
	case EventDisconnected:
		return e.Client + " disconnected", capitalize(endedText(e.Ended)) + ".", true
	case EventLoginFailed:
		if e.Wait <= 0 {
			return "", "", false
		}
		return "Failed logins from " + addressHost(e.Remote),
			fmt.Sprintf("%d failed logins, the last as %q: the address waits %s.", e.Failures, e.User, WaitText(e.Wait)), true
	}
	return "", "", false
}

// addressHost is an address without its port.
func addressHost(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// capitalize makes the first letter of s upper case.
func capitalize(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// notifyNew shows the notifications of the events of st the window has not
// seen.
func (s *windowState) notifyNew(st Status) {
	n := min(st.EventTotal-s.seen, len(st.Events))
	s.seen = st.EventTotal
	for _, e := range st.Events[len(st.Events)-max(n, 0):] {
		if title, body, ok := NotificationOf(e); ok {
			s.notify(title, body)
		}
	}
}

// notify shows a notification, off the UI thread; a click on it brings the
// window back.
func (s *windowState) notify(title, body string) {
	bc := s.Context()
	s.Go(func(ctx context.Context) {
		_, err := desktop.ShowNotification(ctx, bc, title, desktop.NotificationOpts{
			Body:     body,
			AppName:  textAppName,
			Actions:  []desktop.NotificationAction{{ID: "default", Label: TextShowWindow}},
			OnAction: func(string) { s.showWindow() },
		})
		if ctx.Err() != nil {
			return
		}
		s.SetState(func() {
			s.notifyErr = ""
			if err != nil {
				s.notifyErr = err.Error()
			}
		})
	})
}
