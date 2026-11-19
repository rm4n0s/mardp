//go:build darwin

package system

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework CoreFoundation -framework CoreGraphics -framework ApplicationServices -framework IOKit -framework AppKit
#include <CoreFoundation/CoreFoundation.h>
#include <CoreGraphics/CoreGraphics.h>
#include <ApplicationServices/ApplicationServices.h>
#include <IOKit/pwr_mgt/IOPMLib.h>
#import <AppKit/AppKit.h>

static int mardpScreenGranted(void) { return CGPreflightScreenCaptureAccess() ? 1 : 0; }

static void mardpScreenRequest(void) { CGRequestScreenCaptureAccess(); }

static int mardpAXGranted(void) { return AXIsProcessTrusted() ? 1 : 0; }

static void mardpAXRequest(void) {
	@autoreleasepool {
		NSDictionary *opts = @{(__bridge NSString *)kAXTrustedCheckOptionPrompt: @YES};
		AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)opts);
	}
}

typedef struct {
	uint32_t id;
	int main;
	long pixelWidth, pixelHeight;
	double pointWidth, pointHeight;
} mardpDisplay;

// mardpDisplays lists up to max active displays into out.
static CGError mardpDisplays(mardpDisplay *out, uint32_t max, uint32_t *count) {
	CGDirectDisplayID ids[32];
	if (max > 32) {
		max = 32;
	}
	CGError err = CGGetActiveDisplayList(max, ids, count);
	if (err != kCGErrorSuccess) {
		return err;
	}
	for (uint32_t i = 0; i < *count; i++) {
		CGDirectDisplayID id = ids[i];
		out[i].id = id;
		out[i].main = CGDisplayIsMain(id) ? 1 : 0;
		CGRect b = CGDisplayBounds(id);
		out[i].pointWidth = b.size.width;
		out[i].pointHeight = b.size.height;
		CGDisplayModeRef mode = CGDisplayCopyDisplayMode(id);
		if (mode != NULL) {
			out[i].pixelWidth = (long)CGDisplayModeGetPixelWidth(mode);
			out[i].pixelHeight = (long)CGDisplayModeGetPixelHeight(mode);
			CGDisplayModeRelease(mode);
		} else {
			out[i].pixelWidth = (long)CGDisplayPixelsWide(id);
			out[i].pixelHeight = (long)CGDisplayPixelsHigh(id);
		}
	}
	return kCGErrorSuccess;
}

// mardpDisplayUUID writes the UUID of the display into buf; 0 is a failure.
static int mardpDisplayUUID(uint32_t id, char *buf, int len) {
	CFUUIDRef u = CGDisplayCreateUUIDFromDisplayID(id);
	if (u == NULL) {
		return 0;
	}
	CFStringRef s = CFUUIDCreateString(NULL, u);
	CFRelease(u);
	if (s == NULL) {
		return 0;
	}
	Boolean ok = CFStringGetCString(s, buf, len, kCFStringEncodingUTF8);
	CFRelease(s);
	return ok ? 1 : 0;
}

// mardpDisplayName writes what macOS calls the display into buf; 0 means
// it has no name.
static int mardpDisplayName(uint32_t id, char *buf, int len) {
	@autoreleasepool {
		for (NSScreen *screen in [NSScreen screens]) {
			NSNumber *n = screen.deviceDescription[@"NSScreenNumber"];
			if (n == nil || n.unsignedIntValue != id) {
				continue;
			}
			NSString *name = screen.localizedName;
			if (name == nil || name.length == 0) {
				return 0;
			}
			return [name getCString:buf maxLength:len encoding:NSUTF8StringEncoding] ? 1 : 0;
		}
		return 0;
	}
}

// mardpWake declares user activity, which wakes the display; the assertion
// it returns ends the activity when released.
static IOReturn mardpWake(IOPMAssertionID *id) {
	return IOPMAssertionDeclareUserActivity(CFSTR("mardp: a client is connected"), kIOPMUserActiveLocal, id);
}

// mardpKeepDisplayAwake holds an assertion that keeps the display awake.
static IOReturn mardpKeepDisplayAwake(IOPMAssertionID *id) {
	return IOPMAssertionCreateWithName(kIOPMAssertPreventUserIdleDisplaySleep, kIOPMAssertionLevelOn,
		CFSTR("mardp: a client is connected"), id);
}

static IOReturn mardpRelease(IOPMAssertionID id) { return IOPMAssertionRelease(id); }
*/
import "C"

import (
	"context"
	"os/exec"
	"strconv"
	"sync"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/internal/host"
)

// maxDisplays is the most displays listed.
const maxDisplays = 32

// rawDisplay is what CoreGraphics tells of one display.
type rawDisplay struct {
	id                      uint32
	main                    bool
	pixelWidth, pixelHeight int
	pointWidth, pointHeight int
}

// macOS are the calls into the frameworks that can fail, so the tests can
// make them fail.
type macOS struct {
	// displays lists the active displays; cgerr is 0 on success.
	displays func() (ds []rawDisplay, cgerr int)
	// uuid is the UUID of a display.
	uuid func(id uint32) (string, bool)
	// name is what macOS calls a display.
	name func(id uint32) (string, bool)
	// wake declares user activity; keepAwake holds the assertion that keeps
	// the display awake. ioreturn is 0 on success.
	wake, keepAwake func() (assertion uint32, ioreturn int)
	// release lets go of an assertion.
	release func(assertion uint32)
}

// realMacOS calls the frameworks.
func realMacOS() macOS {
	return macOS{
		displays: func() ([]rawDisplay, int) {
			var ds [maxDisplays]C.mardpDisplay
			var n C.uint32_t
			if cerr := C.mardpDisplays(&ds[0], maxDisplays, &n); cerr != C.kCGErrorSuccess {
				return nil, int(cerr)
			}
			out := make([]rawDisplay, int(n))
			for i := range out {
				d := ds[i]
				out[i] = rawDisplay{
					id:          uint32(d.id),
					main:        d.main == 1,
					pixelWidth:  int(d.pixelWidth),
					pixelHeight: int(d.pixelHeight),
					pointWidth:  int(d.pointWidth),
					pointHeight: int(d.pointHeight),
				}
			}
			return out, 0
		},
		uuid: func(id uint32) (string, bool) {
			var buf [64]C.char
			if C.mardpDisplayUUID(C.uint32_t(id), &buf[0], C.int(len(buf))) == 0 {
				return "", false
			}
			return C.GoString(&buf[0]), true
		},
		name: func(id uint32) (string, bool) {
			var buf [256]C.char
			if C.mardpDisplayName(C.uint32_t(id), &buf[0], C.int(len(buf))) == 0 {
				return "", false
			}
			return C.GoString(&buf[0]), true
		},
		wake: func() (uint32, int) {
			var a C.IOPMAssertionID
			r := C.mardpWake(&a)
			return uint32(a), int(r)
		},
		keepAwake: func() (uint32, int) {
			var a C.IOPMAssertionID
			r := C.mardpKeepDisplayAwake(&a)
			return uint32(a), int(r)
		},
		release: func(a uint32) { C.mardpRelease(C.IOPMAssertionID(a)) },
	}
}

// System is macOS.
type System struct {
	mac   macOS
	audio coreAudio
}

// New returns the system.
func New() (*System, error) {
	return &System{mac: realMacOS(), audio: realCoreAudio()}, nil
}

// Granted says whether p is granted, without asking.
func (s *System) Granted(p host.Permission) bool {
	switch p {
	case host.ScreenRecording:
		return C.mardpScreenGranted() == 1
	case host.Accessibility:
		return C.mardpAXGranted() == 1
	}
	return false
}

// Request asks macOS for p; macOS shows its question only the first time,
// and Granted tells the outcome.
func (s *System) Request(p host.Permission) error {
	switch p {
	case host.ScreenRecording:
		C.mardpScreenRequest()
		return nil
	case host.Accessibility:
		C.mardpAXRequest()
		return nil
	}
	return errors.New(TagPermissionUnknown, "the permission is not one mardp knows", "permission", int(p))
}

// OpenSettings opens the pane of System Settings for p.
func (s *System) OpenSettings(ctx context.Context, p host.Permission) error {
	url, err := paneURL(p)
	if err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, "/usr/bin/open", url).CombinedOutput()
	if err != nil {
		return errors.NewErr(TagSettingsOpenFailed, err).SetMetadata("url", url, "output", string(out))
	}
	return nil
}

// Displays lists the active displays.
func (s *System) Displays() ([]host.Display, error) {
	ds, cgerr := s.mac.displays()
	if cgerr != 0 {
		return nil, errors.New(TagDisplaysFailed, "macOS did not list the displays", "cgerror", cgerr)
	}
	out := make([]host.Display, 0, len(ds))
	for i, d := range ds {
		id, ok := s.mac.uuid(d.id)
		if !ok {
			return nil, errors.New(TagDisplaysFailed, "a display has no UUID", "display", d.id)
		}
		name, ok := s.mac.name(d.id)
		if !ok {
			name = "Display " + strconv.Itoa(i+1)
		}
		out = append(out, host.Display{
			ID:          id,
			Name:        name,
			Width:       d.pixelWidth,
			Height:      d.pixelHeight,
			PointWidth:  d.pointWidth,
			PointHeight: d.pointHeight,
			Main:        d.main,
		})
	}
	return out, nil
}

// KeepAwake wakes the display and keeps it awake until release is called;
// release may be called more than once.
func (s *System) KeepAwake() (release func(), err error) {
	wake, r := s.mac.wake()
	if r != 0 {
		return nil, errors.New(TagKeepAwakeFailed, "macOS did not wake the display", "ioreturn", r)
	}
	awake, r := s.mac.keepAwake()
	if r != 0 {
		s.mac.release(wake)
		return nil, errors.New(TagKeepAwakeFailed, "macOS did not keep the display awake", "ioreturn", r)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mac.release(awake)
			s.mac.release(wake)
		})
	}, nil
}
