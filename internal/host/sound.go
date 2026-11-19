package host

import (
	"context"
	"io"
	"log/slog"
	"math"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp"
)

// The gap rule (SPEC §10.1): when the oldest block the client has not
// confirmed is older than gapStart, the host drops sound until it is
// younger than gapEnd.
const (
	gapStart = 500 * time.Millisecond
	gapEnd   = 100 * time.Millisecond
)

// PCMPacket is how long a PCM packet is (SPEC §10.1).
const PCMPacket = 20 * time.Millisecond

// pcmFrames is the frames in a PCM packet.
const pcmFrames = SoundRate * int(PCMPacket) / int(time.Second)

// pcmGap is how far a buffer's time may be from where the one before it
// ended before the encoder takes it as a gap in the sound.
const pcmGap = 10 * time.Millisecond

// AACBitrate is the bitrate of the AAC the host sends (SPEC §10.1).
const AACBitrate = 192_000

// AACPacket is how long an AAC packet is: 1024 frames.
const AACPacket = 1024 * time.Second / SoundRate

// AACFormat is the AAC format the host offers: AAC-LC, 48 kHz, two
// channels, AACBitrate.
func AACFormat() rdp.AudioFormat { return rdp.AACFormat(SoundRate, 2, AACBitrate) }

// PCMFormat is the PCM format the host offers: 16 bits, 48 kHz, two
// channels.
func PCMFormat() rdp.AudioFormat { return rdp.PCMFormat(SoundRate, 2) }

// PCMEncoder turns captured sound into packets of 20 ms of 16-bit PCM,
// left and right interleaved, little-endian (SPEC §10.1). When a buffer
// does not follow on from the one before, the packet being filled is
// padded with silence and sent, and the next starts at the buffer's time.
// It allocates nothing after it is made.
type PCMEncoder struct {
	buf []byte
	// n is the frames in buf; start is the time of its first; next is
	// the time of the frame after the last one taken.
	n     int
	start time.Duration
	next  time.Duration
}

// NewPCMEncoder makes a PCMEncoder.
func NewPCMEncoder() *PCMEncoder { return &PCMEncoder{buf: make([]byte, pcmFrames*4)} }

// frameTime is how long n frames last.
func frameTime(n int) time.Duration { return time.Duration(n) * time.Second / SoundRate }

// pcmSample is v as a 16-bit sample, clipped to full scale; NaN is
// silence.
func pcmSample(v float32) int16 {
	if v != v {
		return 0
	}
	return int16(math.Round(float64(min(max(v, -1), 1)) * math.MaxInt16))
}

// Encode implements SoundEncoder. Channels of different lengths are cut
// to the shorter.
func (e *PCMEncoder) Encode(b *SoundBuffer, packet func(data []byte, t time.Duration)) error {
	frames := min(len(b.Left), len(b.Right))
	if frames == 0 {
		return nil
	}
	if e.n > 0 && (b.Time-e.next > pcmGap || e.next-b.Time > pcmGap) {
		clear(e.buf[e.n*4:])
		packet(e.buf, e.start)
		e.n = 0
	}
	if e.n == 0 {
		e.start = b.Time
		e.next = b.Time
	}
	for i := range frames {
		l, r := pcmSample(b.Left[i]), pcmSample(b.Right[i])
		o := e.n * 4
		e.buf[o], e.buf[o+1], e.buf[o+2], e.buf[o+3] = byte(l), byte(l>>8), byte(r), byte(r>>8)
		e.n++
		if e.n == pcmFrames {
			packet(e.buf, e.start)
			e.n = 0
			e.start += PCMPacket
		}
	}
	e.next += frameTime(frames)
	return nil
}

// soundOut is where the sound path writes: an *rdp.Audio.
type soundOut interface {
	Write(data []byte, timestamp time.Duration) error
	Backlog() (unconfirmed int, oldest time.Duration)
}

// soundPath sends a session's sound: it encodes the captured buffers and
// writes the packets, applying the gap rule (SPEC §10.1).
type soundPath struct {
	out soundOut
	enc SoundEncoder
	log *slog.Logger
	// stop ends the capture, when the session has no sound anymore.
	stop func()
	// length is how long a packet is.
	length time.Duration
	// dropping says the gap rule drops sound now; dropped counts the
	// packets it dropped.
	dropping bool
	dropped  int
	// full counts the packets the full queue dropped since the last
	// that went out.
	full int
	// sent counts the packets written, for the tests.
	sent int
}

// buffer encodes b.
func (p *soundPath) buffer(b *SoundBuffer) {
	if err := p.enc.Encode(b, p.packet); err != nil {
		p.log.Warn("encoding the sound", "err", err)
		p.stop()
	}
}

// packet writes a packet, or drops it under the gap rule or when the queue
// is full.
func (p *soundPath) packet(data []byte, t time.Duration) {
	n, oldest := p.out.Backlog()
	switch {
	case p.dropping && oldest < gapEnd:
		p.dropping = false
		p.log.Info("sound resumed", "dropped", p.dropped, "dropTime", time.Duration(p.dropped)*p.length)
		p.dropped = 0
	case p.dropping:
		p.dropped++
		return
	case oldest > gapStart:
		p.dropping = true
		p.dropped = 1
		p.log.Info("the sound is behind: dropping it", "oldest", oldest, "unconfirmed", n)
		return
	}
	err := p.out.Write(data, t)
	if err == nil {
		if p.full > 0 {
			p.log.Debug("sound packets dropped: the queue was full", "dropped", p.full)
			p.full = 0
		}
		p.sent++
		return
	}
	if e, ok := errors.FromError(err); ok && e.Tag == rdp.TagQueueFull {
		p.full++
		return
	}
	p.log.Info("the sound stopped", "err", err)
	p.stop()
}

// runSound sends the session's sound until ctx ends or the session has no
// sound anymore.
func (h *Host) runSound(ctx context.Context, s *rdp.Session, l *live, log *slog.Logger) {
	a := s.Audio()
	f, err := a.Wait(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Info("no sound for this session", "err", err)
		}
		return
	}
	var enc SoundEncoder
	length := PCMPacket
	switch {
	case f.Tag == rdp.AudioTagPCM:
		enc = NewPCMEncoder()
	case f.Tag == rdp.AudioTagAAC && h.parts.AAC != nil:
		enc, err = h.parts.AAC()
		if err != nil {
			log.Warn("no AAC encoder", "err", err)
			return
		}
		length = AACPacket
	default:
		log.Warn("no encoder for the sound format", "tag", f.Tag)
		return
	}
	if c, ok := enc.(io.Closer); ok {
		defer func() {
			if err := c.Close(); err != nil {
				log.Warn("closing the sound encoder", "err", err)
			}
		}()
	}
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p := &soundPath{out: a, enc: enc, log: log, stop: cancel, length: length}
	log.Info("sending sound", "format", f.Tag)
	if length == AACPacket {
		l.setSound("AAC")
	} else {
		l.setSound("PCM")
	}
	defer l.setSound("")
	defer h.mute.hold(ctx)()
	if err := h.parts.Sound.Capture(sctx, p.buffer); err != nil && sctx.Err() == nil {
		log.Warn("the sound capture stopped", "err", err)
	}
}

// AudioFormats are the sound formats the host offers, for the server's
// Config.AudioFormats: none without a sound source, AAC first when the
// host has an AAC encoder, then PCM.
func (h *Host) AudioFormats() []rdp.AudioFormat {
	switch {
	case h.parts.Sound == nil:
		return nil
	case h.parts.AAC != nil:
		return []rdp.AudioFormat{AACFormat(), PCMFormat()}
	}
	return []rdp.AudioFormat{PCMFormat()}
}
