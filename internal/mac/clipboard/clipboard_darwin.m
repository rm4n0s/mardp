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

long mardpClipChanges(const char *name) {
	@autoreleasepool {
		return (long)board(name).changeCount;
	}
}

char *mardpClipText(const char *name, long *len) {
	@autoreleasepool {
		NSString *s = [board(name) stringForType:NSPasteboardTypeString];
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

void mardpClipRelease(const char *name) {
	@autoreleasepool {
		[board(name) releaseGlobally];
	}
}
