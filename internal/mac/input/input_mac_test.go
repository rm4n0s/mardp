//go:build darwin && mardp_mac

package input

import "testing"

// These read the system; none posts an event (CLAUDE.md, rule 11):
// posting is a manual check.

func TestMacTrusted(t *testing.T) {
	t.Logf("Accessibility granted: %v", macOS{}.trusted())
}

func TestMacOrigin(t *testing.T) {
	x, y, ok := macOS{}.origin("")
	if !ok || x != 0 || y != 0 {
		t.Errorf("main display at %v,%v (%v), want 0,0", x, y, ok)
	}
	if _, _, ok := (macOS{}).origin("00000000-0000-0000-0000-000000000000"); ok {
		t.Error("found a display that is not there")
	}
}
