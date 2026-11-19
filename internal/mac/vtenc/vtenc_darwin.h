// The C side of package vtenc on macOS; vtenc_darwin.m implements it.
#pragma once
#include <stdint.h>
#include <CoreMedia/CoreMedia.h>
#include <CoreVideo/CoreVideo.h>

// mardpEnc is one encoder: a VTCompressionSession and its flags.
typedef struct mardpEnc mardpEnc;

// mardpEncCreated is what mardpEncCreate did: the encoder, or the step
// VideoToolbox refused and its OSStatus.
typedef struct {
	mardpEnc *enc;
	long status;
	char step[64];
} mardpEncCreated;

// mardpEncCreate makes an encoder; handle is passed back to Go with each
// encoded frame. profile: 0 Constrained High, 1 Main, 2 Constrained
// Baseline.
mardpEncCreated mardpEncCreate(uintptr_t handle, int width, int height, int fullRange, int profile, int bitrate, int fps);

// mardpEncSetBitrate sets the average bitrate; it returns the OSStatus.
long mardpEncSetBitrate(mardpEnc *e, int bitrate);

// mardpEncForceKey makes the next frame encoded a key frame.
void mardpEncForceKey(mardpEnc *e);

// mardpEncSetSkip makes mardpEncSubmit skip frames (1) or encode them (0).
void mardpEncSetSkip(mardpEnc *e, int skip);

// mardpEncSubmit encodes pb at pts unless the encoder skips: it returns
// the OSStatus of VideoToolbox, or 1 when the frame was skipped. It may be
// called from any thread until mardpEncClose.
long mardpEncSubmit(mardpEnc *e, CVPixelBufferRef pb, CMTime pts);

// mardpEncPool is the session's pool of pixel buffers, NV12 of its size.
CVPixelBufferPoolRef mardpEncPool(mardpEnc *e);

// mardpEncSubmitPattern fills a pixel buffer of the encoder's pool with
// picture n of a moving test pattern (bands, and a square of noise that
// moves across) and submits it, stamped with the host clock as a capture
// stamps its frames. It returns what mardpEncSubmit returns, or -1 when
// the pool gave no buffer.
long mardpEncSubmitPattern(mardpEnc *e, uint32_t n);

// mardpHostNanos is the host clock, CMClockGetHostTimeClock, in
// nanoseconds: the clock of ScreenCaptureKit's presentation times.
int64_t mardpHostNanos(void);

// mardpEncClose waits for the frames being encoded, invalidates the
// session and frees the encoder.
void mardpEncClose(mardpEnc *e);
