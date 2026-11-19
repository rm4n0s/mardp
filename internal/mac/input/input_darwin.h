#pragma once

#include <stdint.h>

// mardpInEvent is one event to post; see event in input.go.
typedef struct {
	int kind; // 1 key, 2 flags, 3 mouse, 4 scroll, 5 text
	uint64_t flags;
	uint16_t key;
	int down;
	int repeat;
	uint32_t mouse;
	uint32_t button;
	double x, y;
	int clicks;
	int32_t dy, dx;
	uint16_t text[2];
	int n;
} mardpInEvent;

int mardpInTrusted(void);
int mardpInOrigin(const char *uuid, double *x, double *y);
int mardpInPost(const mardpInEvent *e);

// mardpPtrInfo is what mardpPtrRead read; see pointerInfo in pointer.go.
typedef struct {
	int status; // 0 read, 1 no image, 2 too large, 3 not drawn
	int visible;
	int width, height, hotX, hotY;
	int onDisplay;
	double x, y;
} mardpPtrInfo;

mardpPtrInfo mardpPtrRead(const char *uuid, uint8_t *buf, int cap);
