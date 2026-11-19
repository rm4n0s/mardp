//go:build darwin

package system

import (
	"context"
	"slices"
	"testing"

	"github.com/rm4n0s/mardp/internal/host"
)

// fakeMacOS is a macOS whose calls the test decides; it records the
// assertions held.
type fakeMacOS struct {
	ds        []rawDisplay
	cgerr     int
	noUUID    uint32
	noName    uint32
	wakeErr   int
	awakeErr  int
	next      uint32
	held      []uint32
	releases  int
	doubleRel bool
}

func (f *fakeMacOS) mac() macOS {
	hold := func(r int) (uint32, int) {
		if r != 0 {
			return 0, r
		}
		f.next++
		f.held = append(f.held, f.next)
		return f.next, 0
	}
	return macOS{
		displays: func() ([]rawDisplay, int) { return f.ds, f.cgerr },
		uuid: func(id uint32) (string, bool) {
			return "UUID-" + string(rune('A'+id)), id != f.noUUID
		},
		name: func(id uint32) (string, bool) {
			return "Screen " + string(rune('A'+id)), id != f.noName
		},
		wake:      func() (uint32, int) { return hold(f.wakeErr) },
		keepAwake: func() (uint32, int) { return hold(f.awakeErr) },
		release: func(a uint32) {
			f.releases++
			i := slices.Index(f.held, a)
			if i < 0 {
				f.doubleRel = true
				return
			}
			f.held = slices.Delete(f.held, i, i+1)
		},
	}
}

func TestDisplays(t *testing.T) {
	f := &fakeMacOS{
		ds: []rawDisplay{
			{id: 1, main: true, pixelWidth: 2880, pixelHeight: 1800, pointWidth: 1440, pointHeight: 900},
			{id: 2, pixelWidth: 1920, pixelHeight: 1080, pointWidth: 1920, pointHeight: 1080},
		},
		noName: 2,
	}
	s := &System{mac: f.mac()}
	got, err := s.Displays()
	if err != nil {
		t.Fatal(err)
	}
	want := []host.Display{
		{ID: "UUID-B", Name: "Screen B", Width: 2880, Height: 1800, PointWidth: 1440, PointHeight: 900, Main: true},
		{ID: "UUID-C", Name: "Display 2", Width: 1920, Height: 1080, PointWidth: 1920, PointHeight: 1080},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Displays() = %+v, want %+v", got, want)
	}
}

func TestDisplaysFailed(t *testing.T) {
	tests := []struct {
		name string
		f    *fakeMacOS
	}{
		{"list", &fakeMacOS{cgerr: 1001}},
		{"uuid", &fakeMacOS{ds: []rawDisplay{{id: 1}, {id: 2}}, noUUID: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &System{mac: tt.f.mac()}
			ds, err := s.Displays()
			if ds != nil {
				t.Errorf("displays %v", ds)
			}
			assertRoute(t, err, "System.Displays.DisplaysFailed")
		})
	}
}

func TestKeepAwake(t *testing.T) {
	f := &fakeMacOS{}
	s := &System{mac: f.mac()}
	release, err := s.KeepAwake()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.held) != 2 {
		t.Fatalf("%d assertions held, want 2", len(f.held))
	}
	release()
	release()
	if len(f.held) != 0 || f.releases != 2 || f.doubleRel {
		t.Errorf("after release: held %v, %d releases, double %v", f.held, f.releases, f.doubleRel)
	}
}

func TestKeepAwakeFailed(t *testing.T) {
	tests := []struct {
		name string
		f    *fakeMacOS
	}{
		{"wake", &fakeMacOS{wakeErr: 0x2c2}},
		// The wake's assertion is let go when the second one fails.
		{"keep", &fakeMacOS{awakeErr: 0x2c2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &System{mac: tt.f.mac()}
			release, err := s.KeepAwake()
			if release != nil {
				t.Error("a release func")
			}
			assertRoute(t, err, "System.KeepAwake.KeepAwakeFailed")
			if len(tt.f.held) != 0 {
				t.Errorf("assertions held: %v", tt.f.held)
			}
		})
	}
}

func TestRequestUnknown(t *testing.T) {
	var s System
	assertRoute(t, s.Request(0), "System.Request.PermissionUnknown")
	if s.Granted(0) {
		t.Error("an unknown permission is granted")
	}
}

func TestOpenSettingsFailed(t *testing.T) {
	// A canceled context fails before /usr/bin/open starts, so no window
	// opens.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var s System
	assertRoute(t, s.OpenSettings(ctx, host.ScreenRecording), "System.OpenSettings.SettingsOpenFailed")
}

func TestNew(t *testing.T) {
	s, err := New()
	if err != nil || s == nil || s.mac.displays == nil {
		t.Fatalf("New() = %v, %v", s, err)
	}
}
