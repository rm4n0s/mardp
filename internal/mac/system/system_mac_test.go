//go:build darwin && mardp_mac

package system

import (
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/rm4n0s/mardp/internal/host"
)

// These tests use the real frameworks. They never call Request (it shows
// macOS's question) or OpenSettings (it opens System Settings).

func TestMacDisplays(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	ds, err := s.Displays()
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) == 0 {
		t.Fatal("no display")
	}
	mains := 0
	for _, d := range ds {
		t.Logf("%+v", d)
		if d.Main {
			mains++
		}
		if len(d.ID) != 36 || strings.Count(d.ID, "-") != 4 {
			t.Errorf("display %q: the ID is not a UUID", d.ID)
		}
		if d.Name == "" || d.Width <= 0 || d.Height <= 0 || d.PointWidth <= 0 || d.PointHeight <= 0 {
			t.Errorf("display %+v", d)
		}
		if d.Width < d.PointWidth || d.Height < d.PointHeight {
			t.Errorf("display %+v: fewer pixels than points", d)
		}
	}
	if mains != 1 {
		t.Errorf("%d main displays", mains)
	}
	again, err := s.Displays()
	if err != nil || !slices.Equal(ds, again) {
		t.Errorf("a second listing differs: %+v, %v", again, err)
	}
}

func TestMacGranted(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	// Only that it answers without asking.
	t.Logf("screen recording %v, accessibility %v", s.Granted(host.ScreenRecording), s.Granted(host.Accessibility))
}

// assertions is how many of this process's power assertions pmset shows
// with mardp's name.
func assertions(t *testing.T) int {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "/usr/bin/pmset", "-g", "assertions").Output()
	if err != nil {
		t.Fatal(err)
	}
	pid := "pid " + strconv.Itoa(os.Getpid()) + "("
	n := 0
	for line := range strings.Lines(string(out)) {
		if strings.Contains(line, pid) && strings.Contains(line, "mardp: a client is connected") {
			n++
		}
	}
	return n
}

func TestMacKeepAwake(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	before := assertions(t)
	release, err := s.KeepAwake()
	if err != nil {
		t.Fatal(err)
	}
	held := assertions(t)
	release()
	release()
	after := assertions(t)
	t.Logf("assertions: %d before, %d held, %d after", before, held, after)
	if held != before+2 {
		t.Errorf("the two assertions are not held: %d before, %d held", before, held)
	}
	if after != before {
		t.Errorf("%d assertions left after release, %d before", after, before)
	}
}
