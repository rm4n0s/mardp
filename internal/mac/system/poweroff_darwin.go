//go:build darwin

package system

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#include "poweroff_darwin.h"
*/
import "C"

import (
	"runtime/cgo"
	"sync"
)

// WatchPowerOff calls fn, on a thread of its own, when the user logs out,
// restarts or shuts down the Mac (NSWorkspaceWillPowerOffNotification),
// until stop is called. mardp quits then: closing its window only hides it
// (SPEC §7.3), so it would otherwise keep the logout waiting.
func WatchPowerOff(fn func()) (stop func()) {
	h := cgo.NewHandle(fn)
	o := C.mardpWatchPowerOff(C.uintptr_t(h))
	var once sync.Once
	return func() {
		once.Do(func() {
			C.mardpUnwatchPowerOff(o)
			h.Delete()
		})
	}
}

//export mardpPowerOff
func mardpPowerOff(h C.uintptr_t) {
	cgo.Handle(h).Value().(func())()
}

// postPowerOff posts the notification in this process, as macOS does, for
// the tests.
func postPowerOff() { C.mardpPostPowerOff() }
