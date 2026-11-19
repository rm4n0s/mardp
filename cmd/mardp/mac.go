package main

import (
	"log/slog"

	"github.com/rm4n0s/mardp/internal/config"
	"github.com/rm4n0s/mardp/internal/host"
	"github.com/rm4n0s/mardp/internal/mac/capture"
	"github.com/rm4n0s/mardp/internal/mac/input"
	"github.com/rm4n0s/mardp/internal/mac/system"
)

// machine is the Mac as run uses it; the tests give a fake one.
type machine interface {
	// system answers the questions about permissions and displays.
	system() (host.System, error)
	// parts makes a session's parts for the settings s. post says whether
	// the client's input is posted (Accessibility is granted); without it
	// the session is view-only (SPEC §7.1).
	parts(sys host.System, s config.Settings, post bool, log *slog.Logger) (host.Parts, error)
}

// mac is the real Mac.
type mac struct{}

func (mac) system() (host.System, error) { return system.New() }

func (mac) parts(sys host.System, s config.Settings, post bool, log *slog.Logger) (host.Parts, error) {
	screen, err := capture.New(capture.Options{Display: s.Display, Logger: log})
	if err != nil {
		return host.Parts{}, err
	}
	pointer, err := input.NewPointer(input.PointerOptions{Display: s.Display})
	if err != nil {
		return host.Parts{}, err
	}
	parts := host.Parts{Screen: screen, Pointer: pointer, System: sys}
	if post {
		in, err := input.New(input.Options{Display: s.Display, SwapCtrlCommand: s.SwapCtrlCommand, Logger: log})
		if err != nil {
			return host.Parts{}, err
		}
		parts.Input = in
	}
	return parts, nil
}
