package ui

import "github.com/rm4n0s/mardp/internal/host"

// The texts of the window, which the tests find it by.
const (
	TextStopped   = "Stopped"
	TextListening = "Listening"
	TextConnected = "Connected"

	TextStart = "Start"
	TextStop  = "Stop"

	TextChecklist       = "Before the server can start"
	TextScreenRecording = "Screen Recording"
	TextAccessibility   = "Accessibility"
	TextLogin           = "User name and password"
	TextOpenSettings    = "Open System Settings"
	TextSetLogin        = "Set"
	TextChangeLogin     = "Change"
	TextSave            = "Save"
	TextCancel          = "Cancel"
	TextUser            = "User name"
	TextPassword        = "Password"

	TextAddresses   = "Type one of these into Remmina:"
	TextFingerprint = "Certificate fingerprint (SHA-256), as Remmina shows it on the first connection:"
	TextViewOnly    = "View only: the client's keyboard and mouse are dropped, because Accessibility was not granted when the server started. Stop and Start the server to use it."
	TextWhyStopped  = "The server stopped: "
	TextVideo       = "Video: "

	// The video paths' names (SPEC §9.1).
	TextVideoAVC420  = "H.264 (AVC420) through the graphics pipeline"
	TextVideoPlanar  = "Planar through the graphics pipeline"
	TextVideoBitmaps = "bitmap updates"

	textScreenRecordingNeeded = "Needed to show the screen; the server does not start without it. macOS asks again from time to time (\"… is requesting to bypass the system private window picker …\"): answer it at the Mac. A running session goes on while the question waits."
	textAccessibilityNeeded   = "Needed for the client's keyboard and mouse; without it the sessions are view only."
	textLoginNeeded           = "The user name and password a client logs in with; the password has at least 8 characters."
	textGranted               = "Granted."
)

// Action is what a checklist line's button does.
type Action int

// The actions of the checklist's buttons.
const (
	// ActionNone: the line has no button.
	ActionNone Action = iota
	// ActionOpenSettings opens the line's pane of System Settings.
	ActionOpenSettings
	// ActionSetLogin shows the form for the user name and the password.
	ActionSetLogin
)

// Check is a line of the checklist (SPEC §12.2).
type Check struct {
	// Title names what is checked; Detail says why it is needed, or that it
	// is done.
	Title, Detail string
	// Done says that it is granted or set.
	Done bool
	// Action is what the button does, and Button its text.
	Action Action
	Button string
	// Permission is the permission ActionOpenSettings asks for.
	Permission host.Permission
}

// ViewModel is what the window shows of a Status.
type ViewModel struct {
	// State names the server's state.
	State string
	// Addresses and Fingerprint are shown while the server runs.
	Addresses   []string
	Fingerprint string
	// ViewOnly says that the sessions are view only.
	ViewOnly bool
	// Problem is why the server stopped by itself.
	Problem string
	// Video names the connected session's video path; "" when none.
	Video string
	// Session are the lines of the connected session's details and
	// numbers; nil when no client is connected.
	Session []string
	// Events are the lines of the recent events, the newest first.
	Events []string
	// FailedLogins says that the events have a failed login, which shows
	// the waiting rule.
	FailedLogins bool
	// CanStart and CanStop say which of Start and Stop is shown enabled.
	CanStart, CanStop bool
	// Checklist is shown until everything is done; nil then.
	Checklist []Check
}

// NewViewModel makes the view model of st.
func NewViewModel(st Status) ViewModel {
	vm := ViewModel{Problem: st.Problem}
	switch st.Server {
	case ServerListening:
		vm.State = TextListening
	case ServerConnected:
		vm.State = TextConnected
	default:
		vm.State = TextStopped
	}
	running := st.Server != ServerStopped
	if running {
		vm.Addresses, vm.Fingerprint, vm.ViewOnly = st.Addresses, st.Fingerprint, st.ViewOnly
	}
	vm.CanStop = running
	if st.Server == ServerConnected {
		vm.Video = st.Video
		vm.Session = SessionLines(st.Session, st.Video)
	}
	for i := len(st.Events) - 1; i >= 0; i-- {
		vm.Events = append(vm.Events, EventText(st.Events[i]))
		vm.FailedLogins = vm.FailedLogins || st.Events[i].Kind == EventLoginFailed
	}
	vm.CanStart = !running && st.ScreenRecording && st.LoginSet
	checks := []Check{
		permissionCheck(TextScreenRecording, textScreenRecordingNeeded, host.ScreenRecording, st.ScreenRecording),
		permissionCheck(TextAccessibility, textAccessibilityNeeded, host.Accessibility, st.Accessibility),
		{Title: TextLogin, Detail: textLoginNeeded, Done: st.LoginSet, Action: ActionSetLogin, Button: TextSetLogin},
	}
	if st.LoginSet {
		checks[2].Detail, checks[2].Button = "Set, for the user "+st.User+".", TextChangeLogin
	}
	for _, c := range checks {
		if !c.Done {
			vm.Checklist = checks
			break
		}
	}
	return vm
}

// permissionCheck is the checklist's line of the permission p.
func permissionCheck(title, needed string, p host.Permission, granted bool) Check {
	if granted {
		return Check{Title: title, Detail: textGranted, Done: true, Permission: p}
	}
	return Check{Title: title, Detail: needed, Action: ActionOpenSettings, Button: TextOpenSettings, Permission: p}
}
