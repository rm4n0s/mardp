// The C side of WatchPowerOff; poweroff_darwin.m implements it.
#pragma once
#include <stdint.h>

// mardpWatchPowerOff observes NSWorkspaceWillPowerOffNotification and
// calls mardpPowerOff(h) on the posting thread; it returns the observer.
void *mardpWatchPowerOff(uintptr_t h);

// mardpUnwatchPowerOff removes and releases the observer.
void mardpUnwatchPowerOff(void *observer);

// mardpPostPowerOff posts the notification in this process, for the tests.
void mardpPostPowerOff(void);
