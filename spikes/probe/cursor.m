//go:build darwin

#import <AppKit/AppKit.h>
#import <CommonCrypto/CommonDigest.h>
#include "cursor.h"

static NSString *digest(NSData *d) {
	unsigned char h[CC_SHA256_DIGEST_LENGTH];
	CC_SHA256(d.bytes, (CC_LONG)d.length, h);
	return [NSString stringWithFormat:@"%02x%02x%02x%02x%02x%02x", h[0], h[1], h[2], h[3], h[4], h[5]];
}

// probeCursor samples NSCursor.currentSystemCursor ten times a second and
// prints each change of image or hotspot. With moveTest it also posts one
// mouse move of zero distance (needs Accessibility) and reports the result.
int probeCursor(double seconds, int moveTest) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
		printf("front app: %s\n", NSWorkspace.sharedWorkspace.frontmostApplication.bundleIdentifier.UTF8String ?: "?");
		if (moveTest) {
			CGEventRef here = CGEventCreate(NULL);
			CGPoint p = CGEventGetLocation(here);
			CFRelease(here);
			CGEventRef mv = CGEventCreateMouseEvent(NULL, kCGEventMouseMoved, p, kCGMouseButtonLeft);
			CGEventPost(kCGHIDEventTap, mv);
			CFRelease(mv);
			printf("posted a zero-distance mouse move at %.0f,%.0f (accessibility %d)\n", p.x, p.y, AXIsProcessTrusted());
		}
		NSString *last = nil;
		int samples = 0, nilCursor = 0, changes = 0;
		double t0 = CFAbsoluteTimeGetCurrent();
		while (CFAbsoluteTimeGetCurrent() - t0 < seconds) {
			@autoreleasepool {
				samples++;
				NSCursor *c = [NSCursor currentSystemCursor];
				if (!c) { nilCursor++; }
				else {
					NSImage *img = c.image;
					NSBitmapImageRep *rep = nil;
					for (NSImageRep *r in img.representations) if ([r isKindOfClass:NSBitmapImageRep.class]) { rep = (NSBitmapImageRep *)r; break; }
					CGImageRef cg = [img CGImageForProposedRect:NULL context:nil hints:nil];
					NSData *png = cg ? [[[NSBitmapImageRep alloc] initWithCGImage:cg] representationUsingType:NSBitmapImageFileTypePNG properties:@{}] : nil;
					NSString *key = [NSString stringWithFormat:@"%@ size %.0fx%.0f pt, %zux%zu px, hotspot %.1f,%.1f",
						png ? digest(png) : @"none", img.size.width, img.size.height,
						cg ? CGImageGetWidth(cg) : 0, cg ? CGImageGetHeight(cg) : 0, c.hotSpot.x, c.hotSpot.y];
					if (![key isEqualToString:last]) {
						changes++;
						printf("%6.2fs  %s%s\n", CFAbsoluteTimeGetCurrent() - t0, key.UTF8String, rep ? "" : " (no bitmap rep)");
						last = key;
					}
				}
			}
			CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.1, false);
		}
		printf("samples %d, nil %d, distinct images %d\n", samples, nilCursor, changes);
		return 0;
	}
}
