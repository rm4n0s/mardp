//go:build darwin

package system

import (
	"testing"
	"time"
)

// TestLoginItemStatus only reads the state: a test never changes the
// owner's login items. A test binary is not an app bundle, so macOS does
// not find its app, or says it is not a login item; it answers at once,
// since the window asks once a second.
func TestLoginItemStatus(t *testing.T) {
	start := time.Now()
	st := LoginItem{}.Status()
	if st != LoginItemNotFound && st != LoginItemOff {
		t.Errorf("status %d", st)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("the status took %v", d)
	}
	t.Logf("status %d", st)
}
