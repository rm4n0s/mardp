//go:build darwin

package system

import (
	"sync/atomic"
	"testing"
)

// TestWatchQuit: fn is called when the app is told to quit, each time, and
// the process goes on: mardp ends itself.
func TestWatchQuit(t *testing.T) {
	var calls atomic.Int32
	stop := WatchQuit(func() { calls.Add(1) })
	defer stop()
	sendTerminate()
	sendTerminate()
	if n := calls.Load(); n != 2 {
		t.Errorf("%d calls, want 2", n)
	}
	// A second watcher takes over.
	var later atomic.Int32
	stop2 := WatchQuit(func() { later.Add(1) })
	defer stop2()
	sendTerminate()
	if calls.Load() != 2 || later.Load() != 1 {
		t.Errorf("%d and %d calls, want 2 and 1", calls.Load(), later.Load())
	}
}
