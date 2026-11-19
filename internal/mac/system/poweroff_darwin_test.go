//go:build darwin

package system

import (
	"sync/atomic"
	"testing"
)

// TestWatchPowerOff: fn is called when macOS says it powers off, and not
// once stop is called.
func TestWatchPowerOff(t *testing.T) {
	var calls atomic.Int32
	stop := WatchPowerOff(func() { calls.Add(1) })
	postPowerOff()
	if n := calls.Load(); n != 1 {
		t.Errorf("%d calls, want 1", n)
	}
	stop()
	stop()
	postPowerOff()
	if n := calls.Load(); n != 1 {
		t.Errorf("%d calls after stop, want 1", n)
	}
}
