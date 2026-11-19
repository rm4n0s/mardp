//go:build darwin

package system

/*
#cgo LDFLAGS: -framework CoreAudio -framework CoreFoundation
#include <CoreAudio/CoreAudio.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>

// mardpNoMute is the status of a device with no mute that can be set;
// mardpNoString of a UID that is not a string of UTF-8.
enum { mardpNoMute = 'mrdM', mardpNoString = 'mrdU' };

static OSStatus mardpDefaultOutput(AudioObjectID *id) {
	AudioObjectPropertyAddress a = {kAudioHardwarePropertyDefaultOutputDevice, kAudioObjectPropertyScopeGlobal,
		kAudioObjectPropertyElementMain};
	UInt32 sz = sizeof(*id);
	return AudioObjectGetPropertyData(kAudioObjectSystemObject, &a, 0, NULL, &sz, id);
}

static OSStatus mardpDeviceUID(AudioObjectID id, char *buf, int cap) {
	AudioObjectPropertyAddress a = {kAudioDevicePropertyDeviceUID, kAudioObjectPropertyScopeGlobal,
		kAudioObjectPropertyElementMain};
	CFStringRef s = NULL;
	UInt32 sz = sizeof(s);
	OSStatus st = AudioObjectGetPropertyData(id, &a, 0, NULL, &sz, &s);
	if (st != noErr) {
		return st;
	}
	if (s == NULL) {
		return mardpNoString;
	}
	Boolean ok = CFStringGetCString(s, buf, cap, kCFStringEncodingUTF8);
	CFRelease(s);
	return ok ? noErr : mardpNoString;
}

static OSStatus mardpDeviceOf(const char *uid, AudioObjectID *id) {
	CFStringRef s = CFStringCreateWithCString(NULL, uid, kCFStringEncodingUTF8);
	if (s == NULL) {
		return mardpNoString;
	}
	AudioObjectPropertyAddress a = {kAudioHardwarePropertyTranslateUIDToDevice, kAudioObjectPropertyScopeGlobal,
		kAudioObjectPropertyElementMain};
	UInt32 sz = sizeof(*id);
	OSStatus st = AudioObjectGetPropertyData(kAudioObjectSystemObject, &a, sizeof(s), &s, &sz, id);
	CFRelease(s);
	return st;
}

static AudioObjectPropertyAddress mardpMuteAddress(void) {
	AudioObjectPropertyAddress a = {kAudioDevicePropertyMute, kAudioDevicePropertyScopeOutput,
		kAudioObjectPropertyElementMain};
	return a;
}

static OSStatus mardpMuted(AudioObjectID id, UInt32 *muted) {
	AudioObjectPropertyAddress a = mardpMuteAddress();
	if (!AudioObjectHasProperty(id, &a)) {
		return mardpNoMute;
	}
	UInt32 sz = sizeof(*muted);
	return AudioObjectGetPropertyData(id, &a, 0, NULL, &sz, muted);
}

static OSStatus mardpSetMuted(AudioObjectID id, UInt32 muted) {
	AudioObjectPropertyAddress a = mardpMuteAddress();
	if (!AudioObjectHasProperty(id, &a)) {
		return mardpNoMute;
	}
	Boolean settable = false;
	OSStatus st = AudioObjectIsPropertySettable(id, &a, &settable);
	if (st != noErr) {
		return st;
	}
	if (!settable) {
		return mardpNoMute;
	}
	return AudioObjectSetPropertyData(id, &a, 0, NULL, sizeof(muted), &muted);
}
*/
import "C"

import (
	"unsafe"

	"github.com/rm4n0s/errors"
)

// noMute is the status of a device with no mute that can be set.
const noMute = int(C.mardpNoMute)

// coreAudio are the calls into Core Audio, so the tests can make them
// fail; status is an OSStatus, 0 on success.
type coreAudio struct {
	// defaultOutput is the default output device.
	defaultOutput func() (id uint32, status int)
	// uid is a device's UID.
	uid func(id uint32) (string, int)
	// device is the device of a UID, 0 when there is none.
	device func(uid string) (id uint32, status int)
	// muted and setMuted read and set a device's mute.
	muted    func(id uint32) (bool, int)
	setMuted func(id uint32, muted bool) int
}

// realCoreAudio calls Core Audio.
func realCoreAudio() coreAudio {
	return coreAudio{
		defaultOutput: func() (uint32, int) {
			var id C.AudioObjectID
			st := C.mardpDefaultOutput(&id)
			return uint32(id), int(st)
		},
		uid: func(id uint32) (string, int) {
			var buf [512]C.char
			if st := C.mardpDeviceUID(C.AudioObjectID(id), &buf[0], C.int(len(buf))); st != 0 {
				return "", int(st)
			}
			return C.GoString(&buf[0]), 0
		},
		device: func(uid string) (uint32, int) {
			cs := C.CString(uid)
			defer C.free(unsafe.Pointer(cs))
			var id C.AudioObjectID
			st := C.mardpDeviceOf(cs, &id)
			return uint32(id), int(st)
		},
		muted: func(id uint32) (bool, int) {
			var m C.UInt32
			st := C.mardpMuted(C.AudioObjectID(id), &m)
			return m != 0, int(st)
		},
		setMuted: func(id uint32, muted bool) int {
			var m C.UInt32
			if muted {
				m = 1
			}
			return int(C.mardpSetMuted(C.AudioObjectID(id), m))
		},
	}
}

// DefaultOutput is the UID of the default output device.
func (s *System) DefaultOutput() (string, error) {
	id, st := s.audio.defaultOutput()
	if st != 0 || id == 0 {
		return "", errors.New(TagOutputFailed, "macOS has no default output device", "status", st)
	}
	uid, st := s.audio.uid(id)
	if st != 0 {
		return "", errors.New(TagOutputFailed, "the default output device has no UID", "device", id, "status", st)
	}
	return uid, nil
}

// device is the Core Audio device of uid.
func (s *System) device(uid string) (uint32, error) {
	id, st := s.audio.device(uid)
	if st != 0 {
		return 0, errors.New(TagOutputFailed, "Core Audio did not look the device up", "device", uid, "status", st)
	}
	if id == 0 {
		return 0, errors.New(TagOutputUnknown, "no output device has the UID", "device", uid)
	}
	return id, nil
}

// OutputMuted says whether the device uid is muted.
func (s *System) OutputMuted(uid string) (bool, error) {
	id, err := s.device(uid)
	if err != nil {
		return false, err
	}
	m, st := s.audio.muted(id)
	switch {
	case st == noMute:
		return false, errors.New(TagOutputMuteUnsupported, "the output device has no mute", "device", uid)
	case st != 0:
		return false, errors.New(TagOutputFailed, "Core Audio did not read the mute", "device", uid, "status", st)
	}
	return m, nil
}

// SetOutputMuted mutes or unmutes the device uid; its volume is never
// touched.
func (s *System) SetOutputMuted(uid string, muted bool) error {
	id, err := s.device(uid)
	if err != nil {
		return err
	}
	switch st := s.audio.setMuted(id, muted); {
	case st == noMute:
		return errors.New(TagOutputMuteUnsupported, "the output device has no mute that can be set", "device", uid)
	case st != 0:
		return errors.New(TagOutputFailed, "Core Audio did not set the mute", "device", uid, "status", st,
			"muted", muted)
	}
	return nil
}
