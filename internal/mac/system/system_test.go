package system

import (
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/internal/host"
)

// assertRoute fails t unless err is an *errors.Error with route.
func assertRoute(t *testing.T, err error, route string) {
	t.Helper()
	e, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("expected *errors.Error, got %T: %v", err, err)
	}
	if !e.HasRoute(route) {
		t.Errorf("route %s, want %s", e.Route(), route)
	}
}

func TestPaneURL(t *testing.T) {
	tests := []struct {
		p    host.Permission
		want string
	}{
		{host.ScreenRecording, "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture"},
		{host.Accessibility, "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"},
	}
	for _, tt := range tests {
		got, err := paneURL(tt.p)
		if err != nil || got != tt.want {
			t.Errorf("paneURL(%d) = %q, %v; want %q", tt.p, got, err, tt.want)
		}
	}
	for _, p := range []host.Permission{0, 3, -1} {
		_, err := paneURL(p)
		assertRoute(t, err, "paneURL.PermissionUnknown")
	}
}

func TestOpenSettingsUnknown(t *testing.T) {
	// An unknown permission fails before anything opens, on every system.
	var s System
	assertRoute(t, s.OpenSettings(t.Context(), 99), "System.OpenSettings->paneURL.PermissionUnknown")
}
