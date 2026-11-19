//go:build darwin && mardp_mac

package aacenc

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AudioToolbox
#import <AudioToolbox/AudioToolbox.h>
#include <stdlib.h>

typedef struct {
	const uint8_t *data;
	const int32_t *sizes;
	int count, next, off;
	AudioStreamPacketDescription desc;
} mardpTestPackets;

static OSStatus mardpTestProvide(AudioConverterRef c, UInt32 *ioPackets, AudioBufferList *io,
	AudioStreamPacketDescription **desc, void *ref) {
	mardpTestPackets *p = ref;
	if (p->next >= p->count) {
		*ioPackets = 0;
		return 'mrdN';
	}
	int32_t size = p->sizes[p->next];
	io->mBuffers[0].mData = (void *)(p->data + p->off);
	io->mBuffers[0].mDataByteSize = (UInt32)size;
	io->mBuffers[0].mNumberChannels = 2;
	p->desc.mStartOffset = 0;
	p->desc.mVariableFramesInPacket = 0;
	p->desc.mDataByteSize = (UInt32)size;
	if (desc != NULL) {
		*desc = &p->desc;
	}
	p->off += size;
	p->next++;
	*ioPackets = 1;
	return noErr;
}

// mardpTestDecode decodes count raw AAC-LC packets (48 kHz, stereo) into
// out, interleaved float frames, at most frames of them; it returns the
// frames decoded, or a negative status.
static long mardpTestDecode(const uint8_t *cookie, int cookieLen, const uint8_t *data, const int32_t *sizes, int count,
	float *out, int frames) {
	AudioStreamBasicDescription in = {.mSampleRate = 48000, .mFormatID = kAudioFormatMPEG4AAC, .mChannelsPerFrame = 2,
		.mFramesPerPacket = 1024};
	AudioStreamBasicDescription pcm = {
		.mSampleRate = 48000, .mFormatID = kAudioFormatLinearPCM,
		.mFormatFlags = kAudioFormatFlagIsFloat | kAudioFormatFlagIsPacked,
		.mBytesPerPacket = 8, .mFramesPerPacket = 1, .mBytesPerFrame = 8, .mChannelsPerFrame = 2, .mBitsPerChannel = 32,
	};
	AudioConverterRef conv = NULL;
	OSStatus st = AudioConverterNew(&in, &pcm, &conv);
	if (st != noErr) {
		return st < 0 ? st : -st;
	}
	if (cookieLen > 0) {
		AudioConverterSetProperty(conv, kAudioConverterDecompressionMagicCookie, (UInt32)cookieLen, cookie);
	}
	mardpTestPackets p = {.data = data, .sizes = sizes, .count = count};
	long done = 0;
	while (done < frames) {
		AudioBufferList abl = {.mNumberBuffers = 1};
		abl.mBuffers[0].mData = out + 2 * done;
		abl.mBuffers[0].mDataByteSize = (UInt32)((frames - done) * 8);
		abl.mBuffers[0].mNumberChannels = 2;
		UInt32 np = (UInt32)(frames - done);
		st = AudioConverterFillComplexBuffer(conv, mardpTestProvide, &p, &np, &abl, NULL);
		if (st != noErr && st != 'mrdN') {
			AudioConverterDispose(conv);
			return st < 0 ? st : -st;
		}
		if (np == 0) {
			break;
		}
		done += np;
	}
	AudioConverterDispose(conv);
	return done;
}
*/
import "C"

import (
	"unsafe"

	"github.com/rm4n0s/errors"
)

// testDecode decodes raw AAC-LC packets, 48 kHz stereo, with AudioToolbox,
// into interleaved frames.
func testDecode(cookie, data []byte, sizes []int32) ([]float32, error) {
	out := make([]float32, 2*1024*(len(sizes)+4))
	var cp *C.uint8_t
	if len(cookie) > 0 {
		cp = (*C.uint8_t)(unsafe.Pointer(&cookie[0]))
	}
	n := C.mardpTestDecode(cp, C.int(len(cookie)), (*C.uint8_t)(unsafe.Pointer(&data[0])),
		(*C.int32_t)(unsafe.Pointer(&sizes[0])), C.int(len(sizes)), (*C.float)(unsafe.Pointer(&out[0])), C.int(len(out)/2))
	if n < 0 {
		return nil, errors.New("TestDecodeFailed", "AudioToolbox failed to decode", "status", int64(n))
	}
	return out[:2*n], nil
}

// cookieOf is the magic cookie AudioToolbox stated for e.
func cookieOf(e *Encoder) []byte { return e.conv.(*audioConverter).cookie }

// infoOf is the bitrate and largest packet AudioToolbox stated for e.
func infoOf(e *Encoder) (bitrate, maxPacket int) {
	a := e.conv.(*audioConverter)
	return a.bitrate, a.maxPacket
}
