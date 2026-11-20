// The C side of WatchQuit; quit_darwin.m implements it.
#pragma once
#include <stdint.h>

// mardpWatchQuit makes the application's delegate answer macOS's question
// whether the app may quit (applicationShouldTerminate:, which the app
// menu's Quit, Command+Q, the Dock's Quit, a quit Apple Event and a logout
// all ask) by calling mardpQuit(h) and saying "not now": the app then ends
// by itself. The delegate is the one the application has when it runs; it
// is looked for on the main queue until there is one. One handle is
// watched at a time.
void mardpWatchQuit(uintptr_t h);

// mardpUnwatchQuit gives the question back to the delegate.
void mardpUnwatchQuit(void);

// mardpQuitTestDelegate gives the application a delegate that lets it
// quit at once, as an app's own would, for the tests.
void mardpQuitTestDelegate(void);

// mardpSendTerminate sends terminate: to the application, as the app
// menu's Quit does, for the tests. Unwatched, with the test delegate, it
// ends the process.
void mardpSendTerminate(void);
