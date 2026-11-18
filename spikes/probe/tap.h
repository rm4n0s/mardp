// Throwaway (M0): a Core Audio process tap of the system output.
// tone plays a 440 Hz tone from this process while tapping.
// excludePid leaves that process out of the tap (0 = none).
int tapProbe(double seconds, int mute, int tone, int excludePid, float *peak, int *stage);
