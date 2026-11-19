// The C side of package capture on macOS; capture_darwin.m implements it.
#pragma once
#include <stdint.h>

typedef struct {
	uint32_t id;
	int main;
	long pixelWidth, pixelHeight;
	double pointWidth, pointHeight;
	char uuid[64];
	char name[256];
} mardpCapDisplay;

// mardpCapGranted says whether Screen Recording is granted, without asking.
int mardpCapGranted(void);

// mardpCapFind finds the display with the UUID, or the main display when
// uuid is empty; 0 means none.
int mardpCapFind(const char *uuid, mardpCapDisplay *out);

// mardpCapAlive says whether display id is active and has the UUID.
int mardpCapAlive(uint32_t id, const char *uuid);

// mardpCapStarted is what mardpCapStart did: code 0 and the retained
// stream and output, or an SCStreamErrorCode (or -1) and a message.
typedef struct {
	long code;
	void *stream, *output;
	long excluded;
	char message[512];
} mardpCapStarted;

// mardpCapStart starts a stream of display id; handle is passed back to
// Go in each callback. With video set the stream is NV12 (full range with
// fullRange set) and each frame goes to submit(arg, pixelBuffer, pts), a
// long (*)(void *, CVPixelBufferRef, CMTime), instead of Go.
// With window not 0 only that window is captured.
// With sound set the stream captures the system sound instead, which goes
// to Go: 48 kHz, two channels, float, without this process's own sound
// unless includeSelf, and without the running apps that match apps (bundle
// identifiers, one a line; one that ends in "." is a prefix), whose number
// is excluded.
mardpCapStarted mardpCapStart(uintptr_t handle, uint32_t id, int width, int height, int fps,
	int video, int fullRange, void *submit, void *arg, uint32_t window, int sound, int includeSelf, const char *apps);

// mardpCapExcluded is how many of the applications ScreenCaptureKit lists
// match apps, as mardpCapStart counts them; -1 when it does not answer.
long mardpCapExcluded(const char *apps);

// mardpCapCounts is what a stream did with its frames: handed to the
// encoder, which took, skipped or refused them, and handed to Go.
typedef struct {
	long encoded, skipped, refused, crossings;
} mardpCapCounts;

// mardpCapStop tears a stream down in the order of the package
// documentation, releases it, and fills counts unless it is NULL.
void mardpCapStop(void *stream, void *output, mardpCapCounts *counts);
