// The C side of package clipboard on macOS; clipboard_darwin.m implements
// it. name is the pasteboard's name; "" is the general pasteboard.
#pragma once

// mardpClipAccess is what macOS does when this app reads the general
// pasteboard without the user pasting (NSPasteboardAccessBehavior, macOS
// 15.4): 0 the default, 1 it asks the user, 2 it allows, 3 it refuses; -1
// on a macOS without the setting.
int mardpClipAccess(void);

// mardpClipChanges is the pasteboard's change count.
long mardpClipChanges(const char *name);

// mardpClipText is the pasteboard's text as UTF-8, len bytes that the
// caller frees; NULL when it holds no text. The text is taken from the
// first of these the pasteboard holds: UTF-8, UTF-16, traditional Mac
// plain text, RTF.
char *mardpClipText(const char *name, long *len);

// mardpClipSetText puts the len bytes of UTF-8 at text on the pasteboard,
// in the place of what it held. It returns 1, or 0 when the pasteboard
// did not take it.
int mardpClipSetText(const char *name, const char *text, long len);

// mardpClipSetData puts len bytes of data on the pasteboard as the type
// named, alone, in the place of what it held: as an app that writes one
// format only. It returns 1, or 0 when the pasteboard did not take it.
int mardpClipSetData(const char *name, const char *type, const void *data, long len);

// mardpClipRelease frees the named pasteboard and what it holds.
void mardpClipRelease(const char *name);
