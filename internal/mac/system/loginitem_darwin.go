//go:build darwin

package system

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework ServiceManagement
#include <stdlib.h>
#include "loginitem_darwin.h"
*/
import "C"

import "unsafe"

// Status says whether the app opens at login.
func (LoginItem) Status() LoginItemStatus {
	return LoginItemStatus(C.mardpLoginItemStatus())
}

// Set adds the running app to the login items (on), or takes it out.
// macOS tells the user, with a notification, that an item was added.
func (LoginItem) Set(on bool) error {
	return setLoginItem(on, func(on bool) (int, string) {
		v := C.int(0)
		if on {
			v = 1
		}
		r := C.mardpSetLoginItem(v)
		if r.code == 0 {
			return 0, ""
		}
		defer C.free(unsafe.Pointer(r.message))
		return int(r.code), C.GoString(r.message)
	})
}
