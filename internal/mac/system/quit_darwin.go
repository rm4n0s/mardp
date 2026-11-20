//go:build darwin

package system

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#include "quit_darwin.h"
*/
import "C"

import (
	"runtime/cgo"
	"sync"
)

// WatchQuit calls fn, on the main thread, when macOS asks the app whether
// it may quit: the app menu's Quit, Command+Q, the Dock's Quit, a quit
// Apple Event, or a logout (applicationShouldTerminate:), until stop is
// called. macOS is told "not now": mardp stops its server and ends by
// itself. It takes the place of the answer of the application's delegate,
// which it looks for once the app runs.
//
// Glutter: Glutter's delegate passes the question on as a request to close
// the windows, which it cannot tell from the close button, and mardp hides
// its window on a close (SPEC §7.3); so Quit hid the window and the app
// went on. Glutter should say what asked the app to exit
// (docs/GLUTTER-ISSUES.md).
//
// Only one watcher counts at a time: the last.
func WatchQuit(fn func()) (stop func()) {
	h := cgo.NewHandle(fn)
	C.mardpWatchQuit(C.uintptr_t(h))
	var once sync.Once
	return func() {
		once.Do(func() {
			C.mardpUnwatchQuit()
			h.Delete()
		})
	}
}

//export mardpQuit
func mardpQuit(h C.uintptr_t) {
	cgo.Handle(h).Value().(func())()
}

// testDelegate gives the application a delegate that lets it quit at
// once, as an app's own would, for the tests.
func testDelegate() { C.mardpQuitTestDelegate() }

// sendTerminate tells the app to quit, as its menu's Quit does, for the
// tests; only while a watcher watches.
func sendTerminate() { C.mardpSendTerminate() }
