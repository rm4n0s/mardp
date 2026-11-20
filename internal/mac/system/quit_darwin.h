// The C side of WatchQuit; quit_darwin.m implements it.
#pragma once
#include <stdint.h>

// mardpWatchQuit makes -[NSApplication terminate:] (the app menu's Quit,
// Command+Q, the Dock's Quit, a logout) call mardpQuit(h) instead of
// asking the delegate. One handle is watched at a time.
void mardpWatchQuit(uintptr_t h);

// mardpUnwatchQuit gives terminate: back what it did.
void mardpUnwatchQuit(void);

// mardpSendTerminate sends terminate: to the application, as the app
// menu's Quit does, for the tests. It must not be called unwatched: it
// would end the process.
void mardpSendTerminate(void);
