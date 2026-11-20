// The C side of package clipboard on macOS; clipboard_darwin.m implements
// it. name is the pasteboard's name; "" is the general pasteboard.
#pragma once

// mardpClipChanges is the pasteboard's change count.
long mardpClipChanges(const char *name);

// mardpClipText is the pasteboard's text as UTF-8, len bytes that the
// caller frees; NULL when it holds no text.
char *mardpClipText(const char *name, long *len);

// mardpClipSetText puts the len bytes of UTF-8 at text on the pasteboard,
// in the place of what it held. It returns 1, or 0 when the pasteboard
// did not take it.
int mardpClipSetText(const char *name, const char *text, long len);

// mardpClipRelease frees the named pasteboard and what it holds.
void mardpClipRelease(const char *name);
