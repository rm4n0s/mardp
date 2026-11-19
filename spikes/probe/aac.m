//go:build darwin

#import <Foundation/Foundation.h>
#import <AudioToolbox/AudioToolbox.h>
#include <math.h>
#include "aac.h"

// The input has ScreenCaptureKit's audio format: 48 kHz, 2 channels, 32-bit
// float, not interleaved. A 440 Hz tone at level 0.25 stands in for the
// captured sound (the form of the output does not depend on the content).
typedef struct {
	double phase;
	float left[4096], right[4096];
	UInt32 framesLeft;
} toneSrc;

static OSStatus feed(AudioConverterRef c, UInt32 *ioPackets, AudioBufferList *io, AudioStreamPacketDescription **desc, void *ref) {
	toneSrc *t = ref;
	UInt32 n = *ioPackets;
	if (n > 4096) n = 4096;
	if (n > t->framesLeft) n = t->framesLeft;
	for (UInt32 i = 0; i < n; i++) {
		float v = 0.25f * sinf((float)t->phase);
		t->phase += 2 * M_PI * 440 / 48000;
		if (t->phase > 2 * M_PI) t->phase -= 2 * M_PI;
		t->left[i] = v;
		t->right[i] = v;
	}
	t->framesLeft -= n;
	io->mBuffers[0].mData = t->left;
	io->mBuffers[0].mDataByteSize = n * sizeof(float);
	io->mBuffers[0].mNumberChannels = 1;
	io->mBuffers[1].mData = t->right;
	io->mBuffers[1].mDataByteSize = n * sizeof(float);
	io->mBuffers[1].mNumberChannels = 1;
	*ioPackets = n;
	return noErr;
}

int probeAAC(double seconds, int bitrate, int sbr) {
	@autoreleasepool {
		AudioStreamBasicDescription in = {
			.mSampleRate = 48000, .mFormatID = kAudioFormatLinearPCM,
			.mFormatFlags = kAudioFormatFlagIsFloat | kAudioFormatFlagIsPacked | kAudioFormatFlagIsNonInterleaved,
			.mBytesPerPacket = 4, .mFramesPerPacket = 1, .mBytesPerFrame = 4, .mChannelsPerFrame = 2, .mBitsPerChannel = 32,
		};
		AudioStreamBasicDescription out = {.mSampleRate = 48000, .mFormatID = sbr ? kAudioFormatMPEG4AAC_HE : kAudioFormatMPEG4AAC, .mChannelsPerFrame = 2};
		UInt32 sz = sizeof(out);
		OSStatus st = AudioFormatGetProperty(kAudioFormatProperty_FormatInfo, 0, NULL, &sz, &out);
		if (st != noErr) { printf("format info: %d\n", (int)st); return 1; }
		AudioConverterRef conv = NULL;
		st = AudioConverterNew(&in, &out, &conv);
		if (st != noErr) { printf("AudioConverterNew: %d\n", (int)st); return 2; }
		UInt32 br = bitrate;
		st = AudioConverterSetProperty(conv, kAudioConverterEncodeBitRate, sizeof(br), &br);
		if (st != noErr) printf("set bit rate: %d\n", (int)st);
		sz = sizeof(out);
		AudioConverterGetProperty(conv, kAudioConverterCurrentOutputStreamDescription, &sz, &out);
		UInt32 maxPkt = 0;
		sz = sizeof(maxPkt);
		AudioConverterGetProperty(conv, kAudioConverterPropertyMaximumOutputPacketSize, &sz, &maxPkt);
		br = 0;
		sz = sizeof(br);
		AudioConverterGetProperty(conv, kAudioConverterEncodeBitRate, &sz, &br);
		printf("output: format '%c%c%c%c', %.0f Hz, %u channels, %u frames per packet, max packet %u bytes, bit rate %u\n",
			(char)(out.mFormatID >> 24), (char)(out.mFormatID >> 16), (char)(out.mFormatID >> 8), (char)out.mFormatID,
			out.mSampleRate, out.mChannelsPerFrame, out.mFramesPerPacket, maxPkt, br);

		UInt32 cookieSize = 0;
		if (AudioConverterGetPropertyInfo(conv, kAudioConverterCompressionMagicCookie, &cookieSize, NULL) == noErr && cookieSize > 0) {
			uint8_t *cookie = malloc(cookieSize);
			AudioConverterGetProperty(conv, kAudioConverterCompressionMagicCookie, &cookieSize, cookie);
			printf("magic cookie (%u bytes):", cookieSize);
			for (UInt32 i = 0; i < cookieSize; i++) printf(" %02x", cookie[i]);
			printf("\n");
			free(cookie);
		}
		UInt32 primeMethod = 0;
		AudioConverterPrimeInfo prime = {0};
		sz = sizeof(prime);
		if (AudioConverterGetProperty(conv, kAudioConverterPrimeInfo, &sz, &prime) == noErr)
			printf("priming: %u frames leading, %u trailing\n", prime.leadingFrames, prime.trailingFrames);
		(void)primeMethod;

		toneSrc *t = calloc(1, sizeof(toneSrc));
		t->framesLeft = (UInt32)(seconds * 48000);
		UInt32 bufSize = maxPkt * 8;
		uint8_t *buf = malloc(bufSize);
		AudioStreamPacketDescription descs[8];
		int packets = 0;
		long long bytes = 0;
		UInt32 minP = UINT32_MAX, maxP = 0;
		int adts = 0;
		const uint8_t *first = NULL;
		uint8_t firstCopy[16];
		UInt32 firstLen = 0;
		double t0 = CFAbsoluteTimeGetCurrent();
		for (;;) {
			AudioBufferList abl = {.mNumberBuffers = 1};
			abl.mBuffers[0].mData = buf;
			abl.mBuffers[0].mDataByteSize = bufSize;
			abl.mBuffers[0].mNumberChannels = 2;
			UInt32 np = 8;
			st = AudioConverterFillComplexBuffer(conv, feed, t, &np, &abl, descs);
			if (st != noErr) { printf("fill: %d\n", (int)st); break; }
			if (np == 0) break;
			for (UInt32 i = 0; i < np; i++) {
				UInt32 l = descs[i].mDataByteSize;
				const uint8_t *p = buf + descs[i].mStartOffset;
				if (l < minP) minP = l;
				if (l > maxP) maxP = l;
				if (l >= 2 && p[0] == 0xff && (p[1] & 0xf0) == 0xf0) adts++;
				if (!first) { first = p; firstLen = l < 16 ? l : 16; memcpy(firstCopy, p, firstLen); }
				bytes += l;
				packets++;
			}
		}
		double el = CFAbsoluteTimeGetCurrent() - t0;
		printf("packets %d for %.1fs of sound (%.2f ms each), sizes %u to %u bytes, %.0f kbit/s, packets starting with an ADTS sync word %d\n",
			packets, seconds, 1000.0 * out.mFramesPerPacket / out.mSampleRate, minP, maxP, bytes * 8 / seconds / 1000, adts);
		printf("first packet starts:");
		for (UInt32 i = 0; i < firstLen; i++) printf(" %02x", firstCopy[i]);
		printf("\nencode time %.1f ms for %.1fs of sound\n", el * 1000, seconds);
		free(buf);
		free(t);
		AudioConverterDispose(conv);
		return 0;
	}
}
