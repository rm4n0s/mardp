//go:build darwin

#import <ApplicationServices/ApplicationServices.h>
#import <CoreGraphics/CoreGraphics.h>

#include "input_darwin.h"

int mardpInTrusted(void) { return AXIsProcessTrusted() ? 1 : 0; }

int mardpInOrigin(const char *uuid, double *x, double *y) {
	CGDirectDisplayID ids[32];
	uint32_t n = 0;
	if (CGGetActiveDisplayList(32, ids, &n) != kCGErrorSuccess) {
		return 0;
	}
	for (uint32_t i = 0; i < n; i++) {
		int match = 0;
		if (uuid[0] == 0) {
			match = CGDisplayIsMain(ids[i]);
		} else {
			CFUUIDRef u = CGDisplayCreateUUIDFromDisplayID(ids[i]);
			if (u == NULL) {
				continue;
			}
			CFStringRef s = CFUUIDCreateString(NULL, u);
			CFRelease(u);
			if (s == NULL) {
				continue;
			}
			char buf[64];
			match = CFStringGetCString(s, buf, sizeof(buf), kCFStringEncodingUTF8) && strcmp(buf, uuid) == 0;
			CFRelease(s);
		}
		if (match) {
			CGRect b = CGDisplayBounds(ids[i]);
			*x = b.origin.x;
			*y = b.origin.y;
			return 1;
		}
	}
	return 0;
}

int mardpInPost(const mardpInEvent *e) {
	CGEventRef ev = NULL;
	switch (e->kind) {
	case 1:
		ev = CGEventCreateKeyboardEvent(NULL, e->key, e->down ? true : false);
		if (ev != NULL && e->repeat) {
			CGEventSetIntegerValueField(ev, kCGKeyboardEventAutorepeat, 1);
		}
		break;
	case 2:
		ev = CGEventCreateKeyboardEvent(NULL, e->key, true);
		if (ev != NULL) {
			CGEventSetType(ev, kCGEventFlagsChanged);
		}
		break;
	case 3:
		ev = CGEventCreateMouseEvent(NULL, (CGEventType)e->mouse, CGPointMake(e->x, e->y), (CGMouseButton)e->button);
		if (ev != NULL && e->clicks > 0) {
			CGEventSetIntegerValueField(ev, kCGMouseEventClickState, e->clicks);
		}
		break;
	case 4:
		ev = CGEventCreateScrollWheelEvent2(NULL, kCGScrollEventUnitLine, 2, e->dy, e->dx, 0);
		break;
	case 5:
		ev = CGEventCreateKeyboardEvent(NULL, 0, e->down ? true : false);
		if (ev != NULL) {
			CGEventKeyboardSetUnicodeString(ev, e->n, e->text);
		}
		break;
	}
	if (ev == NULL) {
		return 0;
	}
	CGEventSetFlags(ev, (CGEventFlags)e->flags);
	CGEventPost(kCGHIDEventTap, ev);
	CFRelease(ev);
	return 1;
}
