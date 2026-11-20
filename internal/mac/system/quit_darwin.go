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

// WatchQuit calls fn, on the thread that asks, when the app is told to
// quit: the app menu's Quit, Command+Q, the Dock's Quit, or a logout
// (-[NSApplication terminate:]), until stop is called. The request does
// not go on to the app's delegate: mardp ends itself.
//
// Glutter: Glutter passes a quit on as a request to close the windows,
// which it cannot tell from the close button, and mardp hides its window
// on a close (SPEC §7.3); so Quit hid the window and the app went on.
// Glutter should say what asked the app to exit (docs/GLUTTER-ISSUES.md).
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

// sendTerminate tells the app to quit, as its menu's Quit does, for the
// tests; only while a watcher watches.
func sendTerminate() { C.mardpSendTerminate() }
