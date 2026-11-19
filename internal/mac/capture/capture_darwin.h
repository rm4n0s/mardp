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
	char message[512];
} mardpCapStarted;

// mardpCapStart starts a stream of display id; handle is passed back to
// Go in each callback.
mardpCapStarted mardpCapStart(uintptr_t handle, uint32_t id, int width, int height, int fps);

// mardpCapStop tears a stream down in the order of the package
// documentation and releases it.
void mardpCapStop(void *stream, void *output);
