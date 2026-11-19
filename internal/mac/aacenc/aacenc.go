// Package aacenc is the AAC encoder of the sound (SPEC §10.1, §4.2):
// AudioToolbox's AudioConverter from the capture's format (48 kHz, two
// channels, 32-bit float, not interleaved) to AAC-LC at 192 kbit/s. Its
// packets are raw access units of 1024 frames, with no ADTS header, which
// go out one per Wave2 PDU. It implements host.SoundEncoder, for host.AACFormat.
//
// # Time
//
// A packet's time is the time of the captured frames it decodes to: the
// time of the frame the packet starts at, less the encoder's delay (2112
// frames, 44 ms), never less than 0. When a buffer does not follow on from
// the one before (a gap of more than 10 ms, such as the silence the
// capture drops), the gap is filled with silence when it is short; a
// longer one gets enough silence to bring the sound before it out, and the
// encoder starts again at the buffer's time.
package aacenc

import (
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/internal/host"
)

// Tags of the errors of the encoder.
const (
	// TagEncoderUnsupported: the package runs on a system that is not
	// macOS.
	TagEncoderUnsupported = "AACEncoderUnsupported"
	// TagEncoderCreateFailed: AudioToolbox refused the converter or one
	// of its settings.
	TagEncoderCreateFailed = "AACEncoderCreateFailed"
	// TagEncodeFailed: AudioToolbox failed to encode.
	TagEncodeFailed = "AACEncodeFailed"
	// TagEncoderClosed: the encoder is closed.
	TagEncoderClosed = "AACEncoderClosed"
)

// The encoder's form (SPEC §10.1).
const (
	// Bitrate is the bitrate of the AAC the host sends.
	Bitrate = host.AACBitrate
	// PacketFrames is the frames in a packet.
	PacketFrames = 1024
)

const (
	// gap is how far a buffer's time may be from where the one before
	// ended before the encoder takes it as a gap in the sound.
	gap = 10 * time.Millisecond
	// flushFrames is the longest gap filled with silence, and the
	// silence that brings the sound out before a longer gap: more than
	// the encoder's delay and a packet.
	flushFrames = 4096
	// chunkFrames is the most frames given to the converter at once, so
	// that its packets fit in maxPackets.
	chunkFrames = 4096
	// maxPackets is room for the packets of one chunk.
	maxPackets = 16
	// maxPacketBytes is room for one packet: 6144 bits a channel at most
	// (ISO/IEC 14496-3 4.5.3.2).
	maxPacketBytes = 768 * 2
)

// converter is AudioToolbox's AudioConverter, or a fake in the tests.
type converter interface {
	// encode gives the converter frames and writes the packets it
	// completes into out, one after another, their sizes into sizes, and
	// returns how many there are.
	encode(left, right []float32, out []byte, sizes []int32) (int, error)
	// reset drops what the converter holds.
	reset() error
	close()
}

// Encoder encodes the captured sound into AAC (see the package
// documentation). Its methods are called from one goroutine at a time. It
// allocates nothing after it is made.
type Encoder struct {
	conv converter
	// priming is the encoder's delay in frames.
	priming int
	out     []byte
	sizes   []int32
	silence []float32
	// started says base is set: the time of the first frame fed since
	// the converter started; fed counts the frames fed since then, and
	// packets the packets out.
	started bool
	base    time.Duration
	fed     int
	packets int
	closed  bool
}

func newEncoder(conv converter, priming int) *Encoder {
	return &Encoder{conv: conv, priming: priming, out: make([]byte, maxPackets*maxPacketBytes),
		sizes: make([]int32, maxPackets), silence: make([]float32, flushFrames)}
}

// Priming is the encoder's delay in frames, as AudioToolbox states it.
func (e *Encoder) Priming() int { return e.priming }

// frameTime is how long n frames last; negative for a negative n.
func frameTime(n int) time.Duration { return time.Duration(n) * time.Second / host.SoundRate }

// Encode implements host.SoundEncoder. Channels of different lengths are
// cut to the shorter. After Close it fails with TagEncoderClosed.
func (e *Encoder) Encode(b *host.SoundBuffer, packet func(data []byte, t time.Duration)) error {
	if e.closed {
		return errors.New(TagEncoderClosed, "the AAC encoder is closed")
	}
	frames := min(len(b.Left), len(b.Right))
	if frames == 0 {
		return nil
	}
	if !e.started {
		e.started, e.base = true, b.Time
	}
	if d := b.Time - (e.base + frameTime(e.fed)); d > gap || d < -gap {
		if d > 0 && d < frameTime(flushFrames+1) {
			n := min(int((d*host.SoundRate+time.Second/2)/time.Second), flushFrames)
			if err := e.feed(e.silence[:n], e.silence[:n], packet); err != nil {
				return err
			}
		} else {
			if err := e.feed(e.silence, e.silence, packet); err != nil {
				return err
			}
			if err := e.conv.reset(); err != nil {
				return err
			}
			e.base, e.fed, e.packets = b.Time, 0, 0
		}
	}
	return e.feed(b.Left[:frames], b.Right[:frames], packet)
}

// feed gives the converter frames, a chunk at a time, and calls packet
// with each packet it completes.
func (e *Encoder) feed(left, right []float32, packet func(data []byte, t time.Duration)) error {
	for len(left) > 0 {
		n := min(len(left), chunkFrames)
		k, err := e.conv.encode(left[:n], right[:n], e.out, e.sizes)
		if err != nil {
			return err
		}
		off := 0
		for _, size := range e.sizes[:k] {
			t := max(e.base+frameTime(e.packets*PacketFrames-e.priming), 0)
			packet(e.out[off:off+int(size)], t)
			off += int(size)
			e.packets++
		}
		e.fed += n
		left, right = left[n:], right[n:]
	}
	return nil
}

// Close frees the converter; Encode fails after it. Closing twice does
// nothing.
func (e *Encoder) Close() error {
	if !e.closed {
		e.closed = true
		e.conv.close()
	}
	return nil
}
