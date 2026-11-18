// Throwaway (M0): ScreenCaptureKit checks for R1 and R2.
typedef struct {
	double seconds;
	int width, height;   // 0 = the display's size in pixels
	int animate;         // show a small window that changes every frame
	int audio;           // capture the system sound too
	int excludeSelf;     // excludesCurrentProcessAudio
	const char *excludeBundle; // leave this app out of the capture ("" = none)
	int tone;            // play a 440 Hz tone from this process
	int excludePid;      // leave the running app with this process ID out (0 = none)
} probeOpts;

// probeTone plays the 440 Hz tone for the given seconds.
int probeTone(double seconds);

int probeCapture(probeOpts o);
