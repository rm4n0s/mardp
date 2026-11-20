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
// the OSStatus of VideoToolbox, or 1 when the frame was skipped. It keeps
// pb, encoded or skipped, as the newest picture, until the next one. It
// may be called from any thread until mardpEncClose.
long mardpEncSubmit(mardpEnc *e, CVPixelBufferRef pb, CMTime pts);

// mardpEncRepeat encodes the newest picture handed to mardpEncSubmit
// again, encoded or skipped, stamped with the host clock: a still picture
// encoded again gets sharper, since the encoder spends the frame on what
// the last one left coarse. It returns the OSStatus of VideoToolbox, 1
// when the encoder skips, and 2 when no picture was handed over yet.
long mardpEncRepeat(mardpEnc *e);

// mardpEncCaptured counts the pictures handed to mardpEncSubmit.
unsigned mardpEncCaptured(mardpEnc *e);

// mardpEncStale says the newest picture handed over was skipped, so what
// the client shows is older than the screen.
int mardpEncStale(mardpEnc *e);

// mardpEncPool is the session's pool of pixel buffers, NV12 of its size.
CVPixelBufferPoolRef mardpEncPool(mardpEnc *e);

// mardpEncSubmitPattern fills a pixel buffer of the encoder's pool with
// picture n of a moving test pattern (bands, and a square of noise that
// moves across) and submits it, stamped with the host clock as a capture
// stamps its frames. It returns what mardpEncSubmit returns, or -1 when
// the pool gave no buffer.
long mardpEncSubmitPattern(mardpEnc *e, uint32_t n);

// mardpEncSubmitText submits page of the text pattern as
// mardpEncSubmitPattern submits a picture: lines of text as a desktop
// shows them, each page differing from the others in a few letters of
// every line.
long mardpEncSubmitText(mardpEnc *e, uint32_t page);

// mardpTextPage writes page of the text pattern, width by height, into
// out as RGBA, 8 bits each, the first row on top: what mardpEncSubmitText
// gives the encoder. It returns 0, or -1 when it could not be drawn.
int mardpTextPage(int width, int height, uint32_t page, uint8_t *out);

// mardpHostNanos is the host clock, CMClockGetHostTimeClock, in
// nanoseconds: the clock of ScreenCaptureKit's presentation times.
int64_t mardpHostNanos(void);

// mardpEncClose waits for the frames being encoded, invalidates the
// session and frees the encoder.
void mardpEncClose(mardpEnc *e);
