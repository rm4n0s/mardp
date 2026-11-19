package rdptest

import (
	"context"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/rdpsnd"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// TagAudioUnavailable: a sound helper on a client that did not ask for
// rdpsnd.
const TagAudioUnavailable = "ClientAudioUnavailable"

// AudioOptions is how the client answers the server's sound formats.
type AudioOptions struct {
	// Version is the client's version; 0 means 8, which has Wave2.
	Version uint16
	// Flags are the client's capability flags; 0 means
	// rdpsnd.CapsAlive|rdpsnd.CapsVolume.
	Flags uint32
	// Formats picks the formats the client answers with from the
	// server's, in its order; nil answers with all of them.
	Formats func(server []rdpsnd.Format) []rdpsnd.Format
	// Quality is sent in Quality Mode when both versions are 6 or more.
	Quality uint16
	// NoTraining leaves the server's Training unanswered.
	NoTraining bool
}

// Audio is the client's end of the sound channel, set up by
// NegotiateAudio.
type Audio struct {
	c  *Client
	id uint16
	// Server is the server's formats PDU.
	Server rdpsnd.Formats
	// Formats are the client's, which a block's FormatNo indexes.
	Formats []rdpsnd.Format
	// Version is the client's.
	Version uint16
	// Training is the server's Training PDU, unset with NoTraining.
	Training rdpsnd.PDU
}

// AudioBlock is a block of sound the server sent.
type AudioBlock struct {
	// Wave2 is set when it came in a Wave2 PDU, unset for WaveInfo and
	// Wave.
	Wave2          bool
	TimeStamp      uint16
	FormatNo       uint16
	Block          uint8
	AudioTimeStamp uint32
	// Data is the sound, whole.
	Data []byte
	// Received is when its last PDU arrived.
	Received time.Time
}

// NegotiateAudio reads the server's formats on rdpsnd, answers with the
// client's (and Quality Mode when both versions are 6 or more), and
// answers the Training that follows, unless the client plays nothing or
// has no format. Another PDU first fails it with TagPDUUnexpected.
func (c *Client) NegotiateAudio(ctx context.Context, opts AudioOptions) (*Audio, error) {
	id := c.ChannelID("rdpsnd")
	if id == 0 {
		return nil, errors.New(TagAudioUnavailable, "the client did not ask for rdpsnd")
	}
	a := &Audio{c: c, id: id, Version: opts.Version}
	if a.Version == 0 {
		a.Version = rdpsnd.VersionWave2
	}
	p, err := a.read(ctx)
	if err != nil {
		return nil, err
	}
	if p.Type != rdpsnd.MsgFormats {
		return nil, errors.New(TagPDUUnexpected, "another sound PDU than the server's formats", "type", p.Type)
	}
	a.Server = p.Formats
	a.Formats = p.Formats.Formats
	if opts.Formats != nil {
		a.Formats = opts.Formats(p.Formats.Formats)
	}
	flags := opts.Flags
	if flags == 0 {
		flags = rdpsnd.CapsAlive | rdpsnd.CapsVolume
	}
	f := rdpsnd.Formats{Flags: flags, Volume: 0xffffffff, Pitch: 0x10000, Version: a.Version, Formats: a.Formats}
	if err := a.Send(ctx, func(w *wire.Writer) error { return rdpsnd.AppendClientFormats(w, f) }); err != nil {
		return nil, err
	}
	if a.Version >= rdpsnd.VersionQualityMode && a.Server.Version >= rdpsnd.VersionQualityMode {
		err := a.Send(ctx, func(w *wire.Writer) error { rdpsnd.AppendQualityMode(w, opts.Quality); return nil })
		if err != nil {
			return nil, err
		}
	}
	if flags&rdpsnd.CapsAlive == 0 || len(a.Formats) == 0 || opts.NoTraining {
		return a, nil
	}
	if a.Training, err = a.read(ctx); err != nil {
		return nil, err
	}
	if a.Training.Type != rdpsnd.MsgTraining {
		return nil, errors.New(TagPDUUnexpected, "another sound PDU than Training", "type", a.Training.Type)
	}
	t := a.Training
	err = a.Send(ctx, func(w *wire.Writer) error { rdpsnd.AppendTrainingConfirm(w, t.TimeStamp, t.PackSize); return nil })
	return a, err
}

// read reads the server's next PDU on rdpsnd.
func (a *Audio) read(ctx context.Context) (rdpsnd.PDU, error) {
	m, err := a.c.ReadChannel(ctx, a.id)
	if err != nil {
		return rdpsnd.PDU{}, err
	}
	return rdpsnd.ReadServerPDU(m.Data)
}

// Read reads the server's next PDU on rdpsnd, skipping what it sends
// elsewhere. A WaveInfo's Wave is read with ReadBlock instead.
func (a *Audio) Read(ctx context.Context) (rdpsnd.PDU, error) { return a.read(ctx) }

// Send sends on rdpsnd the PDU that build appends.
func (a *Audio) Send(ctx context.Context, build func(w *wire.Writer) error) error {
	w := wire.NewWriter(nil)
	if err := build(&w); err != nil {
		return err
	}
	return a.c.SendChannel(ctx, a.id, w.Bytes())
}

// ReadBlock reads the next block of sound: a Wave2, or a WaveInfo and the
// Wave after it. Another PDU fails it with TagPDUUnexpected (a Close
// among them: its Type is in the error's metadata).
func (a *Audio) ReadBlock(ctx context.Context) (AudioBlock, error) {
	p, err := a.read(ctx)
	if err != nil {
		return AudioBlock{}, err
	}
	b := AudioBlock{TimeStamp: p.TimeStamp, FormatNo: p.FormatNo, Block: p.Block, AudioTimeStamp: p.AudioTimeStamp}
	switch p.Type {
	case rdpsnd.MsgWave2:
		b.Wave2 = true
		b.Data = append([]byte(nil), p.Data...)
	case rdpsnd.MsgWave:
		b.Data = append([]byte(nil), p.Data...)
		m, err := a.c.ReadChannel(ctx, a.id)
		if err != nil {
			return AudioBlock{}, err
		}
		rest, err := rdpsnd.ReadWave(m.Data, p.WaveSize)
		if err != nil {
			return AudioBlock{}, err
		}
		b.Data = append(b.Data, rest...)
	default:
		return AudioBlock{}, errors.New(TagPDUUnexpected, "another sound PDU than a block", "type", p.Type)
	}
	b.Received = time.Now()
	return b, nil
}

// Confirm sends the Wave Confirm of b, as a client that took played to
// play it after it arrived (MS-RDPEA 3.2.5.2.1.6).
func (a *Audio) Confirm(ctx context.Context, b AudioBlock, played time.Duration) error {
	ts := b.TimeStamp + uint16(played.Milliseconds())
	return a.Send(ctx, func(w *wire.Writer) error { rdpsnd.AppendWaveConfirm(w, ts, b.Block); return nil })
}
