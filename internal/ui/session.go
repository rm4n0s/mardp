package ui

import (
	"fmt"
	"strconv"
	"time"

	"github.com/rm4n0s/mardp/internal/host"
)

// The texts of the connected session and of the recent events (SPEC §12.2).
const (
	TextDisconnect = "Disconnect"
	TextEvents     = "Recent events"
	TextNoEvents   = "Nothing yet."
	TextSoundNone  = "none"
	// TextSoundHint follows "Sound: none" when the server offers sound and
	// the client did not ask for it: Remmina's sound is off until its
	// profile says otherwise.
	TextSoundHint    = ". In Remmina: Audio output mode › Local"
	TextDelayBitmaps = "not measured on bitmap updates"

	// Why a session ended.
	TextEndedByClient      = "the client disconnected"
	TextEndedByOwner       = "disconnected from this window"
	TextEndedByHost        = "the server could not go on with it (see the log)"
	TextEndedReplaced      = "another client connected"
	TextEndedServerStopped = "the server stopped"
)

// timeLayout is how the window writes a time of today.
const timeLayout = "15:04:05"

// EventKind is what an event is.
type EventKind int

// The kinds of events.
const (
	// EventConnected: a client connected.
	EventConnected EventKind = iota + 1
	// EventDisconnected: a client's session ended, for Event.Ended.
	EventDisconnected
	// EventLoginFailed: a login was refused.
	EventLoginFailed
)

// Event is a line of the recent events (SPEC §12.2).
type Event struct {
	// At is when it happened.
	At time.Time
	// Kind is what happened.
	Kind EventKind
	// Client is the client's computer name, Remote its address, User the
	// user name it logged in, or tried to log in, with.
	Client, Remote, User string
	// Ended is why a session ended.
	Ended host.Ended
	// Failures is the address's count of failed logins with this one, and
	// Wait the wait this failure made the address start; 0 when none
	// (SPEC §8.2).
	Failures int
	Wait     time.Duration
}

// EventText is the line of the recent events that says e.
func EventText(e Event) string {
	at := e.At.Format(timeLayout) + "  "
	switch e.Kind {
	case EventConnected:
		return at + fmt.Sprintf("%s connected from %s as %q", e.Client, e.Remote, e.User)
	case EventDisconnected:
		return at + e.Client + " disconnected: " + endedText(e.Ended)
	case EventLoginFailed:
		line := at + fmt.Sprintf("Failed login as %q from %s", e.User, e.Remote)
		if e.Wait > 0 {
			line += fmt.Sprintf(": %d failures, the address waits %s", e.Failures, WaitText(e.Wait))
		}
		return line
	}
	return at + "?"
}

// endedText says why a session ended.
func endedText(e host.Ended) string {
	switch e {
	case host.EndedByOwner:
		return TextEndedByOwner
	case host.EndedByHost:
		return TextEndedByHost
	case host.EndedReplaced:
		return TextEndedReplaced
	case host.EndedServerStopped:
		return TextEndedServerStopped
	}
	return TextEndedByClient
}

// sessionShort is the longest line of SessionLines, in characters, that a
// wide window shows beside another; a longer one has a row of its own.
const sessionShort = 34

// SessionLines are the lines of the connected session's details and
// numbers; video names its video path, and offered says the server offers
// sound, so that a client without it is one that did not ask for it.
func SessionLines(info host.SessionInfo, video string, offered bool) []string {
	sound := info.Sound
	if sound == "" {
		sound = TextSoundNone
		if offered {
			sound += TextSoundHint
		}
	}
	delay := TextDelayBitmaps
	if info.Delay > 0 {
		delay = strconv.FormatInt(info.Delay.Round(time.Millisecond).Milliseconds(), 10) + " ms"
	}
	return []string{
		"Client: " + info.Client + " at " + info.Remote,
		"Connected since " + info.Since.Format(timeLayout),
		fmt.Sprintf("Desktop: %d×%d", info.Width, info.Height),
		TextVideo + video,
		"Sound: " + sound,
		fmt.Sprintf("Frames a second: %.0f", info.FPS),
		"Bitrate: " + strconv.FormatFloat(float64(info.Bitrate)/1e6, 'f', 1, 64) + " Mbit/s",
		"Delay: " + delay,
	}
}
