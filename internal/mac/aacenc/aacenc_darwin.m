//go:build darwin

#import <AudioToolbox/AudioToolbox.h>
#include <stdlib.h>
#include <string.h>
#include "aacenc_darwin.h"

// noMore is what the input procedure returns when it has given all the
// frames of the call: the converter keeps what it has and returns.
static const OSStatus noMore = 'mrdN';

// noRoom is the status of a call whose out or sizes had no room for the
// packets of the frames it was given.
static const OSStatus noRoom = 'mrdR';

struct mardpAAC {
	AudioConverterRef conv;
	float *left, *right;
	UInt32 avail;
	// maxPacket is the largest packet the converter makes.
	int maxPacket;
};

static OSStatus provide(AudioConverterRef c, UInt32 *ioPackets, AudioBufferList *io, AudioStreamPacketDescription **desc,
	void *ref) {
	mardpAAC *e = ref;
	if (e->avail == 0) {
		*ioPackets = 0;
		return noMore;
	}
	UInt32 n = *ioPackets < e->avail ? *ioPackets : e->avail;
	io->mBuffers[0].mData = e->left;
	io->mBuffers[0].mDataByteSize = n * sizeof(float);
	io->mBuffers[0].mNumberChannels = 1;
	io->mBuffers[1].mData = e->right;
	io->mBuffers[1].mDataByteSize = n * sizeof(float);
	io->mBuffers[1].mNumberChannels = 1;
	e->left += n;
	e->right += n;
	e->avail -= n;
	*ioPackets = n;
	return noErr;
}

static mardpAACCreated failed(mardpAACCreated r, AudioConverterRef conv, const char *step, OSStatus st) {
	if (conv != NULL) {
		AudioConverterDispose(conv);
	}
	r.enc = NULL;
	strlcpy(r.step, step, sizeof(r.step));
	r.status = st;
	return r;
}

mardpAACCreated mardpAACCreate(int bitrate) {
	mardpAACCreated r = {0};
	AudioStreamBasicDescription in = {
		.mSampleRate = 48000,
		.mFormatID = kAudioFormatLinearPCM,
		.mFormatFlags = kAudioFormatFlagIsFloat | kAudioFormatFlagIsPacked | kAudioFormatFlagIsNonInterleaved,
		.mBytesPerPacket = 4,
		.mFramesPerPacket = 1,
		.mBytesPerFrame = 4,
		.mChannelsPerFrame = 2,
		.mBitsPerChannel = 32,
	};
	AudioStreamBasicDescription out = {.mSampleRate = 48000, .mFormatID = kAudioFormatMPEG4AAC, .mChannelsPerFrame = 2};
	UInt32 sz = sizeof(out);
	OSStatus st = AudioFormatGetProperty(kAudioFormatProperty_FormatInfo, 0, NULL, &sz, &out);
	if (st != noErr) {
		return failed(r, NULL, "format info", st);
	}
	AudioConverterRef conv = NULL;
	st = AudioConverterNew(&in, &out, &conv);
	if (st != noErr) {
		return failed(r, NULL, "AudioConverterNew", st);
	}
	UInt32 br = bitrate;
	st = AudioConverterSetProperty(conv, kAudioConverterEncodeBitRate, sizeof(br), &br);
	if (st != noErr) {
		return failed(r, conv, "set the bitrate", st);
	}
	sz = sizeof(br);
	if (AudioConverterGetProperty(conv, kAudioConverterEncodeBitRate, &sz, &br) == noErr) {
		r.bitrate = (int)br;
	}
	UInt32 maxPkt = 0;
	sz = sizeof(maxPkt);
	if (AudioConverterGetProperty(conv, kAudioConverterPropertyMaximumOutputPacketSize, &sz, &maxPkt) == noErr) {
		r.maxPacket = (int)maxPkt;
	}
	AudioConverterPrimeInfo prime = {0};
	sz = sizeof(prime);
	if (AudioConverterGetProperty(conv, kAudioConverterPrimeInfo, &sz, &prime) == noErr) {
		r.priming = (int)prime.leadingFrames;
	}
	UInt32 cookieSize = sizeof(r.cookie);
	if (AudioConverterGetProperty(conv, kAudioConverterCompressionMagicCookie, &cookieSize, r.cookie) == noErr) {
		r.cookieLen = (int)cookieSize;
	}
	mardpAAC *e = calloc(1, sizeof(mardpAAC));
	if (e == NULL) {
		return failed(r, conv, "allocate", 0);
	}
	e->conv = conv;
	e->maxPacket = r.maxPacket > 0 ? r.maxPacket : 1536;
	r.enc = e;
	return r;
}

int mardpAACEncode(mardpAAC *e, float *left, float *right, int frames, uint8_t *out, int cap, int32_t *sizes, int max,
	int32_t *status) {
	e->left = left;
	e->right = right;
	e->avail = (UInt32)frames;
	int n = 0, off = 0;
	while (n < max) {
		if (cap - off < e->maxPacket) {
			// With no room for a packet, the converter takes the frames
			// and makes nothing of them.
			break;
		}
		AudioBufferList abl = {.mNumberBuffers = 1};
		abl.mBuffers[0].mData = out + off;
		abl.mBuffers[0].mDataByteSize = (UInt32)(cap - off);
		abl.mBuffers[0].mNumberChannels = 2;
		AudioStreamPacketDescription desc = {0};
		UInt32 np = 1;
		OSStatus st = AudioConverterFillComplexBuffer(e->conv, provide, e, &np, &abl, &desc);
		if (st != noErr && st != noMore) {
			*status = st;
			e->avail = 0;
			return -1;
		}
		if (np == 0) {
			break;
		}
		sizes[n++] = (int32_t)desc.mDataByteSize;
		off += (int)desc.mDataByteSize;
		if (st == noMore) {
			break;
		}
	}
	if (e->avail > 0) {
		// Out or sizes ran out before the converter took every frame.
		*status = noRoom;
		e->left = e->right = NULL;
		e->avail = 0;
		return -1;
	}
	// The frames are the caller's: none is used after the call.
	e->left = e->right = NULL;
	e->avail = 0;
	return n;
}

int32_t mardpAACReset(mardpAAC *e) { return AudioConverterReset(e->conv); }

void mardpAACClose(mardpAAC *e) {
	AudioConverterDispose(e->conv);
	free(e);
}
