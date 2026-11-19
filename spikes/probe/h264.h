// Throwaway (M0): VideoToolbox H.264 checks for step 0.8.
typedef struct {
	double seconds;
	int width, height;
	int profile;       // 0 High, 1 Main, 2 Constrained Baseline, 3 Constrained High
	int fullRange;     // capture 420f instead of 420v
	int lowLatency;    // kVTVideoEncoderSpecification_EnableLowLatencyRateControl
	int bitrate;       // bits per second, 0 = 8 Mbit/s
	const char *outPath; // Annex B output, "" = none
} h264Opts;

int probeH264(h264Opts o);
