package main

import (
	"log/slog"

	"github.com/rm4n0s/mardp/internal/config"
	"github.com/rm4n0s/mardp/internal/host"
)

// patternDisplay is the one display of `mardp -headless -test-pattern`.
var patternDisplay = host.Display{ID: "test-pattern", Name: "Test pattern", Width: 1920, Height: 1080,
	PointWidth: 1920, PointHeight: 1080, Main: true}

// patterned is a machine that serves a moving test pattern and a tone
// instead of the screen and its sound (SPEC §15.5): its system grants Screen
// Recording and not Accessibility, so sessions are view-only and nothing is
// posted to the Mac, and it has no output to mute, so the Mac's volume is
// never touched.
type patterned struct{ machine }

func (patterned) system() (host.System, error) {
	return host.NewFakeSystem([]host.Display{patternDisplay}, host.ScreenRecording), nil
}

func (p patterned) parts(sys host.System, s config.Settings, _ bool, log *slog.Logger) (host.Parts, error) {
	parts, err := p.pattern(s, log)
	if err != nil {
		return host.Parts{}, err
	}
	parts.System = sys
	return parts, nil
}
