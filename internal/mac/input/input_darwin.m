//go:build darwin

#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <CoreGraphics/CoreGraphics.h>

#include "input_darwin.h"

int mardpInTrusted(void) { return AXIsProcessTrusted() ? 1 : 0; }

// findDisplay finds the display with the UUID, or the main display for an
// empty one.
static int findDisplay(const char *uuid, CGDirectDisplayID *out) {
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
			*out = ids[i];
			return 1;
		}
	}
	return 0;
}

int mardpInOrigin(const char *uuid, double *x, double *y) {
	CGDirectDisplayID id;
	if (!findDisplay(uuid, &id)) {
		return 0;
	}
	CGRect b = CGDisplayBounds(id);
	*x = b.origin.x;
	*y = b.origin.y;
	return 1;
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

mardpPtrInfo mardpPtrRead(const char *uuid, uint8_t *buf, int cap) {
	mardpPtrInfo r = {0};
	double scale = 1;
	CGDirectDisplayID id;
	if (findDisplay(uuid, &id)) {
		CGRect b = CGDisplayBounds(id);
		CGDisplayModeRef mode = CGDisplayCopyDisplayMode(id);
		if (mode != NULL) {
			if (b.size.width > 0) {
				scale = (double)CGDisplayModeGetPixelWidth(mode) / b.size.width;
			}
			CGDisplayModeRelease(mode);
		}
		CGEventRef e = CGEventCreate(NULL);
		if (e != NULL) {
			CGPoint p = CGEventGetLocation(e);
			CFRelease(e);
			if (CGRectContainsPoint(b, p)) {
				r.onDisplay = 1;
				r.x = p.x - b.origin.x;
				r.y = p.y - b.origin.y;
			}
		}
	}
	// Deprecated, and still the only answer (SPEC §19 R4).
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
	r.visible = CGCursorIsVisible() ? 1 : 0;
#pragma clang diagnostic pop
	// The image is read when the pointer is hidden too: the host shows it
	// to a client whose mouse moves while macOS still says hidden.
	@autoreleasepool {
		NSCursor *c = [NSCursor currentSystemCursor];
		NSImage *img = c.image;
		if (img == nil || img.size.width <= 0 || img.size.height <= 0) {
			r.status = 1;
			return r;
		}
		NSSize size = img.size;
		int w = (int)lround(size.width * scale), h = (int)lround(size.height * scale);
		if (w < 1 || h < 1 || (long)w * h * 4 > cap) {
			r.status = 2;
			r.width = w;
			r.height = h;
			return r;
		}
		// The representation whose pixels match the display's scale, else
		// the largest, drawn at the display's scale.
		NSImageRep *best = nil;
		for (NSImageRep *rep in img.representations) {
			if (rep.pixelsWide == w && rep.pixelsHigh == h) {
				best = rep;
				break;
			}
			if (best == nil || rep.pixelsWide > best.pixelsWide) {
				best = rep;
			}
		}
		CGImageRef cg = [best CGImageForProposedRect:NULL context:nil hints:nil];
		if (cg == NULL) {
			NSRect rect = NSMakeRect(0, 0, size.width, size.height);
			cg = [img CGImageForProposedRect:&rect context:nil hints:nil];
		}
		if (cg == NULL) {
			r.status = 1;
			return r;
		}
		CGColorSpaceRef space = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
		CGContextRef ctx = CGBitmapContextCreate(buf, w, h, 8, (size_t)w * 4, space,
			kCGImageAlphaPremultipliedFirst | kCGBitmapByteOrder32Little);
		CGColorSpaceRelease(space);
		if (ctx == NULL) {
			r.status = 3;
			return r;
		}
		CGContextClearRect(ctx, CGRectMake(0, 0, w, h));
		CGContextSetInterpolationQuality(ctx, kCGInterpolationHigh);
		CGContextDrawImage(ctx, CGRectMake(0, 0, w, h), cg);
		CGContextRelease(ctx);
		// Straight alpha, as the pointer's encoder takes it.
		for (long i = 0; i < (long)w * h * 4; i += 4) {
			int a = buf[i + 3];
			if (a == 0 || a == 255) {
				continue;
			}
			for (int k = 0; k < 3; k++) {
				int v = (buf[i + k] * 255 + a / 2) / a;
				buf[i + k] = v > 255 ? 255 : (uint8_t)v;
			}
		}
		NSPoint hot = c.hotSpot;
		r.width = w;
		r.height = h;
		r.hotX = (int)lround(hot.x * scale);
		r.hotY = (int)lround(hot.y * scale);
		r.hotX = r.hotX < 0 ? 0 : (r.hotX >= w ? w - 1 : r.hotX);
		r.hotY = r.hotY < 0 ? 0 : (r.hotY >= h ? h - 1 : r.hotY);
	}
	return r;
}
