// Throwaway (M0): R4, the pointer's image from an app that is not in front.
int probeCursor(double seconds, int moveTest);

// probeCursorSet makes this app the front one and sets a different system
// cursor every 1.5 s for the given seconds (no mouse events are posted).
int probeCursorSet(double seconds);
