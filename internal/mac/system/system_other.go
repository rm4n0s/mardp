//go:build !darwin

package system

import (
	"context"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/internal/host"
)

// System is the stand-in on systems that are not macOS: New fails.
type System struct{}

// New fails: mardp's host runs only on macOS.
func New() (*System, error) {
	return nil, errors.New(TagSystemUnsupported, "mardp's host runs only on macOS")
}

// Granted says no permission is granted.
func (s *System) Granted(host.Permission) bool { return false }

// Request fails.
func (s *System) Request(p host.Permission) error {
	return errors.New(TagSystemUnsupported, "mardp's host runs only on macOS", "permission", int(p))
}

// OpenSettings fails.
func (s *System) OpenSettings(ctx context.Context, p host.Permission) error {
	if _, err := paneURL(p); err != nil {
		return err
	}
	return errors.New(TagSystemUnsupported, "mardp's host runs only on macOS", "permission", int(p))
}

// Displays fails.
func (s *System) Displays() ([]host.Display, error) {
	return nil, errors.New(TagSystemUnsupported, "mardp's host runs only on macOS")
}

// KeepAwake fails.
func (s *System) KeepAwake() (func(), error) {
	return nil, errors.New(TagSystemUnsupported, "mardp's host runs only on macOS")
}
