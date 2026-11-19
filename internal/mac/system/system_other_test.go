//go:build !darwin

package system

import (
	"testing"

	"github.com/rm4n0s/mardp/internal/host"
)

func TestUnsupported(t *testing.T) {
	if s, err := New(); s != nil || err == nil {
		t.Fatalf("New() = %v, %v", s, err)
	} else {
		assertRoute(t, err, "New.SystemUnsupported")
	}
	var s System
	if s.Granted(host.ScreenRecording) {
		t.Error("granted")
	}
	assertRoute(t, s.Request(host.Accessibility), "System.Request.SystemUnsupported")
	assertRoute(t, s.OpenSettings(t.Context(), host.ScreenRecording), "System.OpenSettings.SystemUnsupported")
	_, err := s.Displays()
	assertRoute(t, err, "System.Displays.SystemUnsupported")
	_, err = s.KeepAwake()
	assertRoute(t, err, "System.KeepAwake.SystemUnsupported")
	_, err = s.DefaultOutput()
	assertRoute(t, err, "System.DefaultOutput.SystemUnsupported")
	_, err = s.OutputMuted("x")
	assertRoute(t, err, "System.OutputMuted.SystemUnsupported")
	assertRoute(t, s.SetOutputMuted("x", true), "System.SetOutputMuted.SystemUnsupported")
}
