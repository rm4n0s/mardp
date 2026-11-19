#ifndef MARDP_AACENC_H
#define MARDP_AACENC_H

#include <stdint.h>

typedef struct mardpAAC mardpAAC;

// mardpAACCreated is mardpAACCreate's result: enc, or NULL with the step
// that failed and its status.
typedef struct {
	mardpAAC *enc;
	int priming;
	int maxPacket;
	int bitrate;
	char step[48];
	int32_t status;
	uint8_t cookie[64];
	int cookieLen;
} mardpAACCreated;

mardpAACCreated mardpAACCreate(int bitrate);
// mardpAACEncode gives the converter frames and writes the packets it
// completes into out (cap bytes), their sizes into sizes (at most max);
// it returns how many there are, or -1 with *status.
int mardpAACEncode(mardpAAC *e, float *left, float *right, int frames, uint8_t *out, int cap, int32_t *sizes, int max,
	int32_t *status);
int32_t mardpAACReset(mardpAAC *e);
void mardpAACClose(mardpAAC *e);

#endif
