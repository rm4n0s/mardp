//go:build darwin

#import <Foundation/Foundation.h>
#import <CoreAudio/CoreAudio.h>
#import <CoreAudio/CATapDescription.h>
#import <CoreAudio/AudioHardwareTapping.h>
#include <stdatomic.h>
#include <math.h>
#include "tap.h"

static _Atomic float gPeak;

// tapProbe taps the whole system output for the given seconds and returns the
// peak sample level. stage says which call failed (1 create tap, 2 tap uid,
// 3 aggregate device, 4 io proc, 5 start).
int tapProbe(double seconds, int mute, float *peak, int *stage) {
	@autoreleasepool {
		atomic_store(&gPeak, 0.0f);
		CATapDescription *d = [[CATapDescription alloc] initStereoGlobalTapButExcludeProcesses:@[]];
		d.name = @"mardp probe tap";
		d.privateTap = YES;
		d.muteBehavior = mute ? CATapMuted : CATapUnmuted;
		AudioObjectID tap = kAudioObjectUnknown;
		*stage = 1;
		OSStatus st = AudioHardwareCreateProcessTap(d, &tap);
		if (st != noErr) return st;

		*stage = 2;
		AudioObjectPropertyAddress a = {kAudioTapPropertyUID, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain};
		CFStringRef uid = NULL;
		UInt32 sz = sizeof(uid);
		st = AudioObjectGetPropertyData(tap, &a, 0, NULL, &sz, &uid);
		if (st != noErr) { AudioHardwareDestroyProcessTap(tap); return st; }

		*stage = 3;
		NSDictionary *agg = @{
			@kAudioAggregateDeviceUIDKey: [[NSUUID UUID] UUIDString],
			@kAudioAggregateDeviceNameKey: @"mardp probe",
			@kAudioAggregateDeviceIsPrivateKey: @YES,
			@kAudioAggregateDeviceTapAutoStartKey: @YES,
			@kAudioAggregateDeviceTapListKey: @[ @{ @kAudioSubTapUIDKey: (__bridge NSString *)uid } ],
		};
		AudioObjectID dev = kAudioObjectUnknown;
		st = AudioHardwareCreateAggregateDevice((__bridge CFDictionaryRef)agg, &dev);
		CFRelease(uid);
		if (st != noErr) { AudioHardwareDestroyProcessTap(tap); return st; }

		*stage = 4;
		AudioDeviceIOProcID proc = NULL;
		st = AudioDeviceCreateIOProcIDWithBlock(&proc, dev, NULL,
			^(const AudioTimeStamp *now, const AudioBufferList *in, const AudioTimeStamp *inTime,
			  AudioBufferList *out, const AudioTimeStamp *outTime) {
				float p = atomic_load(&gPeak);
				for (UInt32 b = 0; b < in->mNumberBuffers; b++) {
					const float *s = in->mBuffers[b].mData;
					UInt32 n = in->mBuffers[b].mDataByteSize / sizeof(float);
					for (UInt32 i = 0; i < n; i++) { float v = fabsf(s[i]); if (v > p) p = v; }
				}
				atomic_store(&gPeak, p);
			});
		if (st == noErr) {
			*stage = 5;
			st = AudioDeviceStart(dev, proc);
			if (st == noErr) {
				[NSThread sleepForTimeInterval:seconds];
				AudioDeviceStop(dev, proc);
			}
			AudioDeviceDestroyIOProcID(dev, proc);
		}
		AudioHardwareDestroyAggregateDevice(dev);
		AudioHardwareDestroyProcessTap(tap);
		*peak = atomic_load(&gPeak);
		return st;
	}
}
