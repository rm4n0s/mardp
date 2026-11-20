// The C side of package vtdec on macOS; vtdec_darwin.m implements it.
#pragma once
#include <stddef.h>
#include <stdint.h>

// mardpDec is one decoder: a VTDecompressionSession and its format.
typedef struct mardpDec mardpDec;

// mardpDecCreated is what mardpDecCreate did: the decoder, or the step
// VideoToolbox refused and its OSStatus.
typedef struct {
	mardpDec *dec;
	long status;
	char step[64];
} mardpDecCreated;

// mardpDecCreate makes a decoder of a stream with these parameter sets.
mardpDecCreated mardpDecCreate(const uint8_t *sps, size_t spsLen, const uint8_t *pps, size_t ppsLen);

// mardpDecWidth and mardpDecHeight are the pictures' size in pixels.
int mardpDecWidth(mardpDec *d);
int mardpDecHeight(mardpDec *d);

// mardpDecDecode decodes one frame, its NAL units each after its length
// in 4 bytes, and writes the picture as RGBA, 8 bits each, into out, whose
// rows are stride bytes apart. It returns the OSStatus, or -1 when no
// picture came out.
long mardpDecDecode(mardpDec *d, const uint8_t *avcc, size_t len, uint8_t *out, size_t stride);

// mardpDecClose ends the session and frees the decoder.
void mardpDecClose(mardpDec *d);
