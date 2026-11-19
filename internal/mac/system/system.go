// Package system is mardp's view of macOS outside a session's pipeline: the
// two permissions and their panes of System Settings, the displays,
// keeping the display awake (SPEC §7.1, §7.2), and the sound output's mute
// (SPEC §10.1). It implements host.System and host.Output.
package system

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/internal/host"
)

// Tags of the errors of the system.
const (
	// TagSystemUnsupported: the package runs on a system that is not macOS.
	TagSystemUnsupported = "SystemUnsupported"
	// TagPermissionUnknown: a permission that is not one of host's.
	TagPermissionUnknown = "PermissionUnknown"
	// TagSettingsOpenFailed: System Settings could not be opened.
	TagSettingsOpenFailed = "SettingsOpenFailed"
	// TagDisplaysFailed: the displays could not be listed.
	TagDisplaysFailed = "DisplaysFailed"
	// TagKeepAwakeFailed: the display could not be woken or kept awake.
	TagKeepAwakeFailed = "KeepAwakeFailed"
	// TagOutputFailed: Core Audio failed to tell or change the sound
	// output.
	TagOutputFailed = "OutputFailed"
	// TagOutputUnknown: no output device has the UID.
	TagOutputUnknown = "OutputUnknown"
	// TagOutputMuteUnsupported: the output device has no mute that can
	// be set.
	TagOutputMuteUnsupported = "OutputMuteUnsupported"
)

// The panes of System Settings for the permissions.
const (
	screenRecordingPane = "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture"
	accessibilityPane   = "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"
)

// paneURL is the address of the pane of System Settings for p.
func paneURL(p host.Permission) (string, error) {
	switch p {
	case host.ScreenRecording:
		return screenRecordingPane, nil
	case host.Accessibility:
		return accessibilityPane, nil
	}
	return "", errors.New(TagPermissionUnknown, "the permission is not one mardp knows", "permission", int(p))
}

// System implements host.System.
var _ host.System = (*System)(nil)

// System implements host.Output.
var _ host.Output = (*System)(nil)
