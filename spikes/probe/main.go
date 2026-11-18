//go:build darwin

// Command probe holds the throwaway checks of milestone M0 (IMPLEMENTATION.md
// steps 0.5 to 0.8). It runs as a signed bundle, spikes/out/Probe.app, so the
// owner grants Screen Recording once and rebuilds keep it. Start it with
// `open spikes/out/Probe.app --args <command>` so macOS asks for the bundle,
// not for the program that started it. Deleted in step 0.9.
package main

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework CoreGraphics -framework ApplicationServices -framework CoreFoundation -framework CoreAudio -framework Foundation -framework AppKit -framework ScreenCaptureKit -framework CoreMedia -framework CoreVideo -framework IOKit -framework AVFoundation
#include "capture.h"
#include "cursor.h"
#include <stdlib.h>
#include "tap.h"
#include <CoreGraphics/CoreGraphics.h>
#include <ApplicationServices/ApplicationServices.h>

static int axRequest(void) {
	const void *keys[] = { kAXTrustedCheckOptionPrompt };
	const void *vals[] = { kCFBooleanTrue };
	CFDictionaryRef opts = CFDictionaryCreate(NULL, keys, vals, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	int ok = AXIsProcessTrustedWithOptions(opts);
	CFRelease(opts);
	return ok;
}
*/
import "C"

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"unsafe"
)

// AppKit and the run loop want the main thread.
func init() { runtime.LockOSThread() }

func b2i(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: probe request|request-ax|tap|capture|cursor|status")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "status":
		fmt.Println("screen recording granted:", bool(C.CGPreflightScreenCaptureAccess()))
		fmt.Println("accessibility granted:", C.AXIsProcessTrusted() != 0)
	case "capture":
		fs := flag.NewFlagSet("capture", flag.ExitOnError)
		secs := fs.Float64("s", 30, "seconds")
		w := fs.Int("w", 0, "width")
		h := fs.Int("h", 0, "height")
		anim := fs.Bool("animate", false, "animate a small window")
		audio := fs.Bool("audio", false, "capture system sound")
		exSelf := fs.Bool("exclude-self", false, "excludesCurrentProcessAudio")
		exApp := fs.String("exclude-app", "", "bundle id to leave out")
		tone := fs.Bool("tone", false, "play a tone from this process")
		exPid := fs.Int("exclude-pid", 0, "process ID of a running app to leave out")
		every := fs.Float64("report", 0, "print counts every this many seconds")
		moveAt := fs.Float64("move-at", 0, "post one zero-distance mouse move at this second")
		_ = fs.Parse(os.Args[2:])
		cs := C.CString(*exApp)
		defer C.free(unsafe.Pointer(cs))
		o := C.probeOpts{seconds: C.double(*secs), width: C.int(*w), height: C.int(*h),
			animate: b2i(*anim), audio: b2i(*audio), excludeSelf: b2i(*exSelf), excludeBundle: cs, tone: b2i(*tone), excludePid: C.int(*exPid), reportEvery: C.double(*every), moveAt: C.double(*moveAt)}
		os.Exit(int(C.probeCapture(o)))
	case "cursor-set":
		fs := flag.NewFlagSet("cursor-set", flag.ExitOnError)
		secs := fs.Float64("s", 10, "seconds")
		_ = fs.Parse(os.Args[2:])
		os.Exit(int(C.probeCursorSet(C.double(*secs))))
	case "tone":
		fs := flag.NewFlagSet("tone", flag.ExitOnError)
		secs := fs.Float64("s", 10, "seconds")
		_ = fs.Parse(os.Args[2:])
		os.Exit(int(C.probeTone(C.double(*secs))))
	case "cursor":
		fs := flag.NewFlagSet("cursor", flag.ExitOnError)
		secs := fs.Float64("s", 20, "seconds")
		move := fs.Bool("move", false, "post one zero-distance mouse move")
		_ = fs.Parse(os.Args[2:])
		os.Exit(int(C.probeCursor(C.double(*secs), b2i(*move))))
	case "tap":
		fs := flag.NewFlagSet("tap", flag.ExitOnError)
		secs := fs.Float64("s", 3, "seconds")
		mute := fs.Bool("mute", false, "a tap that mutes what it taps")
		tone := fs.Bool("tone", false, "play a tone from this process")
		exSelf := fs.Bool("exclude-self", false, "leave this process out")
		exPid := fs.Int("exclude-pid", 0, "leave this process out")
		_ = fs.Parse(os.Args[2:])
		if *exSelf {
			*exPid = os.Getpid()
		}
		var peak C.float
		var stage C.int
		st := C.tapProbe(C.double(*secs), b2i(*mute), b2i(*tone), C.int(*exPid), &peak, &stage)
		fmt.Printf("tap status %d at stage %d, peak %.4f\n", int(st), int(stage), float32(peak))
	case "request-ax":
		fmt.Println("accessibility granted:", C.axRequest() != 0)
	case "request":
		fmt.Println("screen recording granted before:", bool(C.CGPreflightScreenCaptureAccess()))
		fmt.Println("request returned:", bool(C.CGRequestScreenCaptureAccess()))
	default:
		fmt.Fprintln(os.Stderr, "unknown command", os.Args[1])
		os.Exit(2)
	}
}
