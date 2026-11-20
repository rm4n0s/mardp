#import <AppKit/AppKit.h>
#include <stdlib.h>
#include <string.h>
#include "clipboard_darwin.h"

// board is the pasteboard of name: the general one for "".
static NSPasteboard *board(const char *name) {
	if (name == NULL || name[0] == 0) {
		return NSPasteboard.generalPasteboard;
	}
	return [NSPasteboard pasteboardWithName:[NSString stringWithUTF8String:name]];
}

int mardpClipAccess(void) {
	if (@available(macOS 15.4, *)) {
		return (int)NSPasteboard.generalPasteboard.accessBehavior;
	}
	return -1;
}

long mardpClipChanges(const char *name) {
	@autoreleasepool {
		return (long)board(name).changeCount;
	}
}

// text is the pasteboard's text in whichever text format it holds. Most
// apps write UTF-8 (NSPasteboardTypeString), from which macOS derives the
// others; but some write one of the others alone, and then asking for
// UTF-8 gives nothing: Screen Sharing puts the text of its client there as
// "traditional Mac plain text" only (seen on macOS 27.2).
static NSString *text(NSPasteboard *pb) {
	NSString *s = [pb stringForType:NSPasteboardTypeString];
	if (s != nil) {
		return s;
	}
	NSData *d = [pb dataForType:@"public.utf16-external-plain-text"];
	if (d != nil && (s = [[NSString alloc] initWithData:d encoding:NSUTF16StringEncoding]) != nil) {
		return s;
	}
	d = [pb dataForType:@"public.utf16-plain-text"];
	if (d != nil && (s = [[NSString alloc] initWithData:d encoding:NSUTF16LittleEndianStringEncoding]) != nil) {
		return s;
	}
	// The traditional text is in the system's encoding (Mac OS Roman on a
	// system in a Western language).
	d = [pb dataForType:@"com.apple.traditional-mac-plain-text"];
	if (d != nil) {
		s = CFBridgingRelease(CFStringCreateWithBytes(kCFAllocatorDefault, d.bytes, (CFIndex)d.length,
			CFStringGetSystemEncoding(), false));
		if (s != nil) {
			return s;
		}
	}
	d = [pb dataForType:NSPasteboardTypeRTF];
	if (d != nil) {
		return [[NSAttributedString alloc] initWithRTF:d documentAttributes:NULL].string;
	}
	return nil;
}

char *mardpClipText(const char *name, long *len) {
	@autoreleasepool {
		NSString *s = text(board(name));
		if (s == nil) {
			return NULL;
		}
		NSData *d = [s dataUsingEncoding:NSUTF8StringEncoding allowLossyConversion:YES];
		if (d == nil) {
			return NULL;
		}
		char *out = malloc(d.length + 1);
		if (out == NULL) {
			return NULL;
		}
		memcpy(out, d.bytes, d.length);
		out[d.length] = 0;
		*len = (long)d.length;
		return out;
	}
}

int mardpClipSetText(const char *name, const char *text, long len) {
	@autoreleasepool {
		NSString *s = [[NSString alloc] initWithBytes:text length:(NSUInteger)len encoding:NSUTF8StringEncoding];
		if (s == nil) {
			return 0;
		}
		NSPasteboard *pb = board(name);
		[pb clearContents];
		return [pb setString:s forType:NSPasteboardTypeString] ? 1 : 0;
	}
}

int mardpClipSetData(const char *name, const char *type, const void *data, long len) {
	@autoreleasepool {
		NSPasteboard *pb = board(name);
		[pb clearContents];
		return [pb setData:[NSData dataWithBytes:data length:(NSUInteger)len] forType:[NSString stringWithUTF8String:type]] ? 1 : 0;
	}
}

void mardpClipRelease(const char *name) {
	@autoreleasepool {
		[board(name) releaseGlobally];
	}
}
