package rdp

import (
	"sync"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/rdpsnd"
	"github.com/rm4n0s/mardp/rdp/internal/svc"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// TagAudioUnavailable: sound for a session that has none: the client
// joined no rdpsnd, the server offers no formats, the client plays no
// sound or none of the server's formats, the channel is still being set
// up, or the server closed it.
const TagAudioUnavailable = "AudioUnavailable"

// The wFormatTag of the formats the server may offer (SPEC §10.1).
const (
	// AudioTagPCM is WAVE_FORMAT_PCM.
	AudioTagPCM = rdpsnd.FormatPCM
	// AudioTagAAC is WAVE_FORMAT_AAC_MS: AAC in raw access units.
	AudioTagAAC = rdpsnd.FormatAAC
)

// audioVersion is the version of the sound channel the server states:
// 8, which has Wave2 (SPEC §10.1).
const audioVersion = rdpsnd.VersionWave2

// audioLastBlock is the server's cLastBlockConfirmed: the first block is
// one more, 0 (MS-RDPEA 3.3.5.2.1.1).
const audioLastBlock uint8 = 0xff

// AudioFormat is a sound format (MS-RDPEA AUDIO_FORMAT): a WAVEFORMATEX
// without its cbSize, whose extra bytes are Extra.
type AudioFormat struct {
	Tag            uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Extra          []byte
}

// PCMFormat is 16-bit PCM at samplesPerSec frames a second of channels
// channels.
func PCMFormat(samplesPerSec, channels int) AudioFormat {
	block := uint16(channels * 2)
	return AudioFormat{Tag: AudioTagPCM, Channels: uint16(channels), SamplesPerSec: uint32(samplesPerSec),
		AvgBytesPerSec: uint32(samplesPerSec) * uint32(block), BlockAlign: block, BitsPerSample: 16}
}

func (f AudioFormat) wire() rdpsnd.Format {
	return rdpsnd.Format{Tag: f.Tag, Channels: f.Channels, SamplesPerSec: f.SamplesPerSec, AvgBytesPerSec: f.AvgBytesPerSec,
		BlockAlign: f.BlockAlign, BitsPerSample: f.BitsPerSample, Extra: f.Extra}
}

// clone returns f with its own Extra.
func (f AudioFormat) clone() AudioFormat {
	f.Extra = append([]byte(nil), f.Extra...)
	if len(f.Extra) == 0 {
		f.Extra = nil
	}
	return f
}

// audioState is where a session's sound channel is in its life.
type audioState int

const (
	// audioOff: no sound, ever (no rdpsnd, no formats, or the client has
	// no format of the server's or plays nothing).
	audioOff audioState = iota
	// audioFormats: the server's formats are sent; the client's are
	// awaited.
	audioFormats
	// audioTraining: Training is sent; its confirm is awaited.
	audioTraining
	audioReady
	// audioClosed: the server sent Close.
	audioClosed
)

// audioBlock is a block sent and not confirmed yet.
type audioBlock struct {
	sent    time.Time
	pending bool
}

// Audio is a session's sound output on the rdpsnd channel (SPEC §6,
// §10.1, MS-RDPEA). The server offers its formats when the session opens,
// takes the first of them the client has, and trains; then Write sends
// sound until Close. Its methods may be called from any goroutine.
type Audio struct {
	s       *Session
	ch      *staticChannel
	offered []AudioFormat

	mu    sync.Mutex
	state audioState
	// start is when the server's formats were sent: wTimeStamp counts
	// milliseconds from it.
	start time.Time
	// format is the chosen format, formatNo its index in the client's
	// list; version and quality are the client's.
	format   AudioFormat
	formatNo uint16
	version  uint16
	quality  uint16
	training uint16
	// next is the next block's number.
	next        uint8
	blocks      [256]audioBlock
	unconfirmed int
	delay       time.Duration
	// buf holds the PDUs of a block while it is queued.
	buf wire.Writer
}

// Audio returns the session's sound output.
func (s *Session) Audio() *Audio { return &s.audio }

// startAudio sends the server's formats on rdpsnd when the client joined
// it and the server offers formats.
func (s *Session) startAudio(formats []AudioFormat) {
	a := &s.audio
	a.ch = s.channel("rdpsnd")
	if a.ch == nil || len(formats) == 0 {
		return
	}
	a.offered = formats
	a.ch.setHandler(a.receive)
	a.mu.Lock()
	defer a.mu.Unlock()
	wf := make([]rdpsnd.Format, len(formats))
	for i, f := range formats {
		wf[i] = f.wire()
	}
	a.buf.Reset()
	if err := rdpsnd.AppendServerFormats(&a.buf, audioVersion, audioLastBlock, wf); err != nil {
		s.c.log.Warn("the sound formats", "err", err)
		return
	}
	if err := s.sendChannel(queueControl, a.ch, a.buf.Bytes()); err != nil {
		s.c.log.Debug("sending the sound formats", "err", err)
		return
	}
	a.start = time.Now()
	a.next = audioLastBlock
	a.next++
	a.state = audioFormats
}

// receive handles what the client sends on rdpsnd, on the session's
// reading goroutine. Malformed PDUs and PDUs out of turn are ignored
// (MS-RDPEA 3.3.5.2).
func (a *Audio) receive(msg []byte) error {
	log := a.s.c.log
	p, err := rdpsnd.ReadClientPDU(msg)
	if err != nil {
		log.Debug("a sound PDU ignored", "err", err)
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case p.Type == rdpsnd.MsgFormats && a.state == audioFormats:
		a.clientFormats(p.Formats)
	case p.Type == rdpsnd.MsgQualityMode:
		a.quality = p.QualityMode
	case p.Type == rdpsnd.MsgTraining && a.state == audioTraining:
		if p.TimeStamp != a.training {
			log.Debug("a Training Confirm of another Training ignored", "timeStamp", p.TimeStamp, "want", a.training)
			return nil
		}
		a.state = audioReady
		log.Info("sound ready", "format", a.format.Tag, "samplesPerSec", a.format.SamplesPerSec,
			"channels", a.format.Channels, "clientVersion", a.version, "quality", a.quality,
			"training", time.Duration(a.clock()-p.TimeStamp)*time.Millisecond)
	case p.Type == rdpsnd.MsgWaveConfirm && (a.state == audioReady || a.state == audioClosed):
		b := &a.blocks[p.Block]
		if !b.pending {
			log.Debug("a Wave Confirm of no block ignored", "block", p.Block)
			return nil
		}
		b.pending = false
		a.unconfirmed--
		a.delay = time.Since(b.sent)
	default:
		log.Debug("a sound PDU out of turn ignored", "type", p.Type, "state", int(a.state))
	}
	return nil
}

// clientFormats takes the client's formats: the first of the server's
// formats that the client has is the session's, then Training is sent.
// The caller holds mu.
func (a *Audio) clientFormats(f rdpsnd.Formats) {
	log := a.s.c.log
	a.version = f.Version
	if f.Flags&rdpsnd.CapsAlive == 0 {
		a.state = audioOff
		log.Info("no sound: the client plays none", "flags", f.Flags)
		return
	}
	found := false
	for _, o := range a.offered {
		for i, c := range f.Formats {
			if o.wire().Equal(c) {
				a.format, a.formatNo, found = o, uint16(i), true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		a.state = audioOff
		log.Info("no sound: the client has none of the server's formats", "formats", len(f.Formats))
		return
	}
	a.training = a.clock()
	a.buf.Reset()
	rdpsnd.AppendTraining(&a.buf, a.training)
	if err := a.s.sendChannel(queueControl, a.ch, a.buf.Bytes()); err != nil {
		a.state = audioOff
		log.Debug("sending Training", "err", err)
		return
	}
	a.state = audioTraining
}

// clock is the time for wTimeStamp: milliseconds since the server's
// formats were sent, in 16 bits.
func (a *Audio) clock() uint16 { return uint16(time.Since(a.start).Milliseconds()) }

// Format returns the session's sound format; ok is false until the
// channel is set up, and when the session has no sound.
func (a *Audio) Format() (f AudioFormat, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != audioReady {
		return AudioFormat{}, false
	}
	return a.format.clone(), true
}

// Write queues data, a block of sound in the session's format, captured at
// timestamp (the Wave2 PDU's dwAudioTimeStamp, in milliseconds; a client
// older than version 8 gets WaveInfo and Wave, which have none). It does
// not wait for the network: it fails with TagQueueFull when the control
// queue is full, and with TagAudioUnavailable when the session has no
// sound now.
func (a *Audio) Write(data []byte, timestamp time.Duration) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != audioReady {
		return errors.New(TagAudioUnavailable, "the session has no sound now", "state", int(a.state))
	}
	block := a.next
	a.buf.Reset()
	split := 0
	if a.version >= rdpsnd.VersionWave2 {
		if err := rdpsnd.AppendWave2(&a.buf, a.clock(), a.formatNo, block, uint32(timestamp.Milliseconds()), data); err != nil {
			return err
		}
	} else {
		if err := rdpsnd.AppendWaveInfo(&a.buf, a.clock(), a.formatNo, block, data); err != nil {
			return err
		}
		split = a.buf.Len()
		rdpsnd.AppendWave(&a.buf, data)
	}
	msg := a.buf.Bytes()
	err := a.s.wr.send(queueControl, func(p *packet) error {
		if split == 0 {
			return appendChannelMessage(p, a.ch, msg)
		}
		if err := appendChannelMessage(p, a.ch, msg[:split]); err != nil {
			return err
		}
		return appendChannelMessage(p, a.ch, msg[split:])
	})
	if err != nil {
		return err
	}
	b := &a.blocks[block]
	if !b.pending {
		a.unconfirmed++
	}
	b.sent, b.pending = time.Now(), true
	a.next++
	return nil
}

// Delay returns how long the client took to confirm the last block it
// confirmed, from Write to its Wave Confirm: the time the sound waited in
// the queue, crossed the network, and played on the client (a client
// confirms a block once it has played it); and how many blocks are sent
// and not confirmed.
func (a *Audio) Delay() (delay time.Duration, unconfirmed int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.delay, a.unconfirmed
}

// Close tells the client the server stops sending sound (the Close PDU);
// Write fails after it. It does nothing on a session without sound.
func (a *Audio) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != audioReady && a.state != audioTraining {
		return nil
	}
	a.state = audioClosed
	a.buf.Reset()
	rdpsnd.AppendClose(&a.buf)
	return a.s.sendChannel(queueControl, a.ch, a.buf.Bytes())
}

// appendChannelMessage appends to p the message msg on the static channel
// ch, in chunks of at most svc.ChunkLength bytes.
func appendChannelMessage(p *packet, ch *staticChannel, msg []byte) error {
	return svc.Split(msg, svc.ChunkLength, func(h svc.Header, chunk []byte) error {
		p.tmp.Reset()
		svc.WriteHeader(&p.tmp, h)
		p.tmp.Write(chunk)
		return appendChannelData(p, ch)
	})
}
