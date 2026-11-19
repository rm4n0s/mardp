// Package ui is mardp's window, built with Glutter (SPEC §12). It is a view of
// a Backend, which cmd/mardp makes of internal/host and internal/config; the
// window decides nothing of its own, and its tests use a fake Backend.
package ui

import (
	"context"
	"slices"

	"github.com/rm4n0s/mardp/internal/config"
	"github.com/rm4n0s/mardp/internal/host"
)

// ServerState is what the server is doing (SPEC §12.2).
type ServerState int

// The states of the server.
const (
	// ServerStopped: the server does not listen.
	ServerStopped ServerState = iota
	// ServerListening: the server listens and no client is connected.
	ServerListening
	// ServerConnected: a client is connected.
	ServerConnected
)

// Status is the state that the window shows.
type Status struct {
	// Server is what the server is doing.
	Server ServerState
	// Addresses are what to type into Remmina, "ip:port", while the server
	// runs.
	Addresses []string
	// Fingerprint is the certificate's SHA-256 fingerprint while the server
	// runs (SPEC §8.1).
	Fingerprint string
	// ViewOnly says that the running server drops the client's keyboard and
	// mouse: Accessibility was not granted when it started, or mardp runs
	// with -view-only (SPEC §7.1).
	ViewOnly bool
	// ScreenRecording and Accessibility say whether macOS grants them now.
	ScreenRecording, Accessibility bool
	// User is the user name a client logs in with; "" when none is set.
	User string
	// LoginSet says that a user name and a password are set (SPEC §8.2).
	LoginSet bool
	// Problem is why the server stopped by itself; "" when it did not.
	Problem string
	// Video names the connected session's video path (SPEC §9.1), one of
	// the TextVideo texts; "" when no client is connected.
	Video string
	// Settings are the saved settings.
	Settings config.Settings
	// Displays are the Mac's displays now, for the setting of the display.
	Displays []host.Display
	// Pending says that saved settings wait for the next session: the
	// running server starts again with them once no client is connected.
	Pending bool
	// Session is the connected session's details and numbers; its zero
	// value when no client is connected.
	Session host.SessionInfo
	// Events are the recent events, the oldest first.
	Events []Event
	// EventTotal is how many events were recorded since the app started,
	// of which Events are the last: the window notifies of those it has
	// not seen.
	EventTotal int
}

// Equal says whether s and o are the same status.
func (s Status) Equal(o Status) bool {
	return s.Server == o.Server && slices.Equal(s.Addresses, o.Addresses) && s.Fingerprint == o.Fingerprint &&
		s.ViewOnly == o.ViewOnly && s.ScreenRecording == o.ScreenRecording && s.Accessibility == o.Accessibility &&
		s.User == o.User && s.LoginSet == o.LoginSet && s.Problem == o.Problem && s.Video == o.Video &&
		s.Settings == o.Settings && slices.Equal(s.Displays, o.Displays) && s.Pending == o.Pending &&
		s.Session == o.Session && slices.Equal(s.Events, o.Events) && s.EventTotal == o.EventTotal
}

// Backend is what the window shows and does. Status is called on the UI
// thread, about once a second, and returns at once; the other methods are
// called on goroutines of their own and may take a while.
type Backend interface {
	// Status is the state now.
	Status() Status
	// Start starts the server: it returns once the server listens, or why it
	// cannot. The server runs until Stop, not until ctx ends.
	Start(ctx context.Context) error
	// Stop stops the server and ends its sessions; it does nothing when the
	// server does not run.
	Stop(ctx context.Context) error
	// SetLogin sets the user name and the password a client logs in with.
	SetLogin(ctx context.Context, user, password string) error
	// OpenSettings asks macOS for p, which shows its question the first time,
	// and opens p's pane of System Settings (SPEC §7.1).
	OpenSettings(ctx context.Context, p host.Permission) error
	// SaveSettings checks and saves s, all but its user name and password,
	// which SetLogin sets (SPEC §12.2). What changes the server applies at
	// the next session (Pending).
	SaveSettings(ctx context.Context, s config.Settings) error
	// NewCertificate replaces the server's certificate; it applies at the
	// next session, like the settings.
	NewCertificate(ctx context.Context) error
	// Disconnect ends the connected client's session; it does nothing when
	// no client is connected.
	Disconnect(ctx context.Context) error
}
