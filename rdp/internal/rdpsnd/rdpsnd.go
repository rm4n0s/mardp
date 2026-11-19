// Package rdpsnd reads and writes the PDUs of the audio output channel
// (MS-RDPEA 2.2), which travel on the rdpsnd static channel: the server's
// and the client's formats and versions, Quality Mode, Training and its
// confirm, WaveInfo and Wave, Wave2, Wave Confirm and Close. Both sides
// are here: the server's in the library, the client's in rdptest. UDP,
// encryption, volume and pitch are not. Sending the PDUs is the caller's.
package rdpsnd

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors this package returns.
const (
	// TagBodySizeInvalid: a PDU whose header states more bytes than it
	// has, or a body too short for its type.
	TagBodySizeInvalid = "RdpsndBodySizeInvalid"
	// TagTypeUnexpected: a PDU the reader does not expect from that side.
	TagTypeUnexpected = "RdpsndTypeUnexpected"
	// TagFormatsInvalid: a formats PDU whose formats do not fit its body,
	// or formats to write that do not fit one PDU.
	TagFormatsInvalid = "RdpsndFormatsInvalid"
	// TagWaveTooShort: audio of 4 bytes or fewer for WaveInfo and Wave,
	// which carry the first 4 bytes apart (3.3.5.2.1.1).
	TagWaveTooShort = "RdpsndWaveTooShort"
	// TagWaveTooLong: audio whose PDU's BodySize would not fit 16 bits.
	TagWaveTooLong = "RdpsndWaveTooLong"
)

// The msgType of the PDU header (2.2.1).
const (
	MsgClose       uint8 = 0x01
	MsgWave        uint8 = 0x02
	MsgSetVolume   uint8 = 0x03
	MsgSetPitch    uint8 = 0x04
	MsgWaveConfirm uint8 = 0x05
	MsgTraining    uint8 = 0x06
	MsgFormats     uint8 = 0x07
	MsgCryptKey    uint8 = 0x08
	MsgWaveEncrypt uint8 = 0x09
	MsgUDPWave     uint8 = 0x0a
	MsgUDPWaveLast uint8 = 0x0b
	MsgQualityMode uint8 = 0x0c
	MsgWave2       uint8 = 0x0d
)

// HeaderLength is the length of the PDU header (SNDPROLOG).
const HeaderLength = 4

// The client's capability flags (2.2.2.2).
const (
	// CapsAlive: the client plays audio; without it no audio is sent.
	CapsAlive  uint32 = 0x1
	CapsVolume uint32 = 0x2
	CapsPitch  uint32 = 0x4
)

// The quality modes of Quality Mode (2.2.2.3).
const (
	QualityDynamic uint16 = 0
	QualityMedium  uint16 = 1
	QualityHigh    uint16 = 2
)

// Versions of the protocol that change what is sent: Quality Mode from 6
// (2.2.2.3), Wave2 from 8 (SPEC §10.1).
const (
	VersionQualityMode uint16 = 6
	VersionWave2       uint16 = 8
)

// The wFormatTag values of the formats mardp offers.
const (
	// FormatPCM is WAVE_FORMAT_PCM.
	FormatPCM uint16 = 0x0001
	// FormatAAC is WAVE_FORMAT_AAC_MS, AAC in raw access units.
	FormatAAC uint16 = 0xa106
)

// waveInfoLength is the length of a WaveInfo PDU, header included, and
// waveInfoBody the part of its BodySize that is not audio (2.2.3.3).
const (
	waveInfoLength = 16
	waveInfoBody   = 8
)

// wave2Body is the part of a Wave2 PDU's BodySize that is not audio
// (2.2.3.10).
const wave2Body = 12

// formatsFixed is the length of a formats PDU's fields between the header
// and the formats (2.2.2.1, 2.2.2.2).
const formatsFixed = 20

// formatFixed is the length of an AUDIO_FORMAT without its extra data
// (2.2.2.1.1).
const formatFixed = 18

// Format is an audio format (AUDIO_FORMAT, 2.2.2.1.1).
type Format struct {
	Tag            uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	// Extra is the format's extra data (cbSize bytes).
	Extra []byte
}

// Equal reports whether f and g are the same format, extra data included.
func (f Format) Equal(g Format) bool {
	if f.Tag != g.Tag || f.Channels != g.Channels || f.SamplesPerSec != g.SamplesPerSec ||
		f.AvgBytesPerSec != g.AvgBytesPerSec || f.BlockAlign != g.BlockAlign ||
		f.BitsPerSample != g.BitsPerSample || len(f.Extra) != len(g.Extra) {
		return false
	}
	for i := range f.Extra {
		if f.Extra[i] != g.Extra[i] {
			return false
		}
	}
	return true
}

// Formats is a formats and version PDU of either side (2.2.2.1, 2.2.2.2).
type Formats struct {
	// Flags, Volume, Pitch and DGramPort mean something only from the
	// client; DGramPort is read big-endian.
	Flags, Volume, Pitch uint32
	DGramPort            uint16
	// LastBlock is cLastBlockConfirmed: from the server, one less than
	// the first block's number (3.3.5.2.1.1).
	LastBlock uint8
	Version   uint16
	Formats   []Format
}

// PDU is a PDU read from either side. Which fields are set depends on
// Type.
type PDU struct {
	Type uint8
	// Formats is set for MsgFormats.
	Formats Formats
	// QualityMode is set for MsgQualityMode.
	QualityMode uint16
	// TimeStamp is the wTimeStamp of Training, its confirm, WaveInfo,
	// Wave2 and Wave Confirm.
	TimeStamp uint16
	// PackSize is the wPackSize of Training and its confirm.
	PackSize uint16
	// FormatNo, Block and AudioTimeStamp are those of WaveInfo and Wave2;
	// Block is also Wave Confirm's cConfirmedBlockNo.
	FormatNo       uint16
	Block          uint8
	AudioTimeStamp uint32
	// Data is the audio of Wave2, the first 4 bytes of WaveInfo's, and
	// Training's data; it points into the input.
	Data []byte
	// WaveSize is the length of the audio a WaveInfo announces: its
	// BodySize less 8.
	WaveSize int
}

// ReadHeader reads a PDU header.
func ReadHeader(r *wire.Reader) (msgType uint8, bodySize uint16, err error) {
	if msgType, err = r.U8(); err != nil {
		return 0, 0, err
	}
	if err = r.Skip(1); err != nil {
		return 0, 0, err
	}
	bodySize, err = r.U16LE()
	return msgType, bodySize, err
}

// ReadClientPDU reads a PDU the client sends: formats, Quality Mode,
// Training Confirm or Wave Confirm.
func ReadClientPDU(data []byte) (PDU, error) {
	r := wire.NewReader(data)
	t, size, err := ReadHeader(&r)
	if err != nil {
		return PDU{}, err
	}
	p := PDU{Type: t}
	switch t {
	case MsgFormats:
		body, err := body(&r, t, size, formatsFixed)
		if err != nil {
			return PDU{}, err
		}
		p.Formats, err = readFormats(&body)
		return p, err
	case MsgQualityMode:
		body, err := body(&r, t, size, 4)
		if err != nil {
			return PDU{}, err
		}
		p.QualityMode, err = body.U16LE()
		return p, err
	case MsgTraining:
		body, err := body(&r, t, size, 4)
		if err != nil {
			return PDU{}, err
		}
		if p.TimeStamp, err = body.U16LE(); err != nil {
			return PDU{}, err
		}
		p.PackSize, err = body.U16LE()
		return p, err
	case MsgWaveConfirm:
		body, err := body(&r, t, size, 4)
		if err != nil {
			return PDU{}, err
		}
		if p.TimeStamp, err = body.U16LE(); err != nil {
			return PDU{}, err
		}
		p.Block, err = body.U8()
		return p, err
	}
	return PDU{}, errors.New(TagTypeUnexpected, "a PDU the client does not send", "type", t)
}

// ReadServerPDU reads a PDU the server sends: formats, Training, WaveInfo,
// Wave2 or Close. A Wave PDU has no header; it is read with ReadWave.
func ReadServerPDU(data []byte) (PDU, error) {
	r := wire.NewReader(data)
	t, size, err := ReadHeader(&r)
	if err != nil {
		return PDU{}, err
	}
	p := PDU{Type: t}
	switch t {
	case MsgFormats:
		body, err := body(&r, t, size, formatsFixed)
		if err != nil {
			return PDU{}, err
		}
		p.Formats, err = readFormats(&body)
		return p, err
	case MsgTraining:
		body, err := body(&r, t, size, 4)
		if err != nil {
			return PDU{}, err
		}
		if p.TimeStamp, err = body.U16LE(); err != nil {
			return PDU{}, err
		}
		if p.PackSize, err = body.U16LE(); err != nil {
			return PDU{}, err
		}
		p.Data = body.Rest()
		return p, nil
	case MsgWave:
		// BodySize counts the Wave PDU that follows; the WaveInfo is 12
		// bytes after its header.
		if int(size) < waveInfoBody+5 {
			return PDU{}, errors.New(TagBodySizeInvalid, "a WaveInfo announcing 4 bytes of audio or fewer", "bodySize", size)
		}
		body, err := r.Sub(waveInfoLength - HeaderLength)
		if err != nil {
			return PDU{}, err
		}
		if err = readWaveHeader(&body, &p); err != nil {
			return PDU{}, err
		}
		p.Data = body.Rest()
		p.WaveSize = int(size) - waveInfoBody
		return p, nil
	case MsgWave2:
		body, err := body(&r, t, size, wave2Body)
		if err != nil {
			return PDU{}, err
		}
		if err = readWaveHeader(&body, &p); err != nil {
			return PDU{}, err
		}
		if p.AudioTimeStamp, err = body.U32LE(); err != nil {
			return PDU{}, err
		}
		p.Data = body.Rest()
		return p, nil
	case MsgClose:
		return p, nil
	}
	return PDU{}, errors.New(TagTypeUnexpected, "a PDU the server does not send", "type", t)
}

// body returns the reader of a PDU's body of at least min bytes.
func body(r *wire.Reader, t uint8, size uint16, min int) (wire.Reader, error) {
	if int(size) < min || int(size) > r.Remaining() {
		return wire.Reader{}, errors.New(TagBodySizeInvalid, "a PDU whose BodySize does not fit", "type", t,
			"bodySize", size, "have", r.Remaining(), "min", min)
	}
	return r.Sub(int(size))
}

// readWaveHeader reads the wTimeStamp, wFormatNo, cBlockNo and bPad that
// begin WaveInfo and Wave2.
func readWaveHeader(r *wire.Reader, p *PDU) error {
	var err error
	if p.TimeStamp, err = r.U16LE(); err != nil {
		return err
	}
	if p.FormatNo, err = r.U16LE(); err != nil {
		return err
	}
	if p.Block, err = r.U8(); err != nil {
		return err
	}
	return r.Skip(3)
}

// readFormats reads the body of a formats PDU.
func readFormats(r *wire.Reader) (Formats, error) {
	var f Formats
	var err error
	if f.Flags, err = r.U32LE(); err != nil {
		return Formats{}, err
	}
	if f.Volume, err = r.U32LE(); err != nil {
		return Formats{}, err
	}
	if f.Pitch, err = r.U32LE(); err != nil {
		return Formats{}, err
	}
	if f.DGramPort, err = r.U16BE(); err != nil {
		return Formats{}, err
	}
	n, err := r.U16LE()
	if err != nil {
		return Formats{}, err
	}
	if f.LastBlock, err = r.U8(); err != nil {
		return Formats{}, err
	}
	if f.Version, err = r.U16LE(); err != nil {
		return Formats{}, err
	}
	if err = r.Skip(1); err != nil {
		return Formats{}, err
	}
	if int(n)*formatFixed > r.Remaining() {
		return Formats{}, errors.New(TagFormatsInvalid, "more formats than the PDU holds", "formats", n, "have", r.Remaining())
	}
	f.Formats = make([]Format, 0, n)
	for range n {
		var a Format
		if a, err = readFormat(r); err != nil {
			return Formats{}, err
		}
		f.Formats = append(f.Formats, a)
	}
	return f, nil
}

// readFormat reads an AUDIO_FORMAT; its extra data is copied.
func readFormat(r *wire.Reader) (Format, error) {
	var f Format
	var err error
	if f.Tag, err = r.U16LE(); err != nil {
		return Format{}, err
	}
	if f.Channels, err = r.U16LE(); err != nil {
		return Format{}, err
	}
	if f.SamplesPerSec, err = r.U32LE(); err != nil {
		return Format{}, err
	}
	if f.AvgBytesPerSec, err = r.U32LE(); err != nil {
		return Format{}, err
	}
	if f.BlockAlign, err = r.U16LE(); err != nil {
		return Format{}, err
	}
	if f.BitsPerSample, err = r.U16LE(); err != nil {
		return Format{}, err
	}
	cb, err := r.U16LE()
	if err != nil {
		return Format{}, err
	}
	extra, err := r.Bytes(int(cb))
	if err != nil {
		return Format{}, errors.New(TagFormatsInvalid, "a format's extra data goes past the PDU", "cbSize", cb, "have", r.Remaining())
	}
	if cb > 0 {
		f.Extra = append([]byte(nil), extra...)
	}
	return f, nil
}

// appendFormats appends a formats PDU of either side.
func appendFormats(w *wire.Writer, f Formats) error {
	size := formatsFixed
	for _, a := range f.Formats {
		size += formatFixed + len(a.Extra)
	}
	if size > 0xffff || len(f.Formats) > 0xffff {
		return errors.New(TagFormatsInvalid, "formats that do not fit one PDU", "formats", len(f.Formats), "bodySize", size)
	}
	w.U8(MsgFormats)
	w.U8(0)
	w.U16LE(uint16(size))
	w.U32LE(f.Flags)
	w.U32LE(f.Volume)
	w.U32LE(f.Pitch)
	w.U16BE(f.DGramPort)
	w.U16LE(uint16(len(f.Formats)))
	w.U8(f.LastBlock)
	w.U16LE(f.Version)
	w.U8(0)
	for _, a := range f.Formats {
		w.U16LE(a.Tag)
		w.U16LE(a.Channels)
		w.U32LE(a.SamplesPerSec)
		w.U32LE(a.AvgBytesPerSec)
		w.U16LE(a.BlockAlign)
		w.U16LE(a.BitsPerSample)
		w.U16LE(uint16(len(a.Extra)))
		w.Write(a.Extra)
	}
	return nil
}

// AppendServerFormats appends the Server Audio Formats and Version PDU
// (2.2.2.1): the version, the block number before the first, and the
// formats. The fields the client ignores are zero.
func AppendServerFormats(w *wire.Writer, version uint16, lastBlock uint8, formats []Format) error {
	return appendFormats(w, Formats{Version: version, LastBlock: lastBlock, Formats: formats})
}

// AppendClientFormats appends the Client Audio Formats and Version PDU
// (2.2.2.2) of f.
func AppendClientFormats(w *wire.Writer, f Formats) error {
	return appendFormats(w, f)
}

// AppendQualityMode appends a Quality Mode PDU (2.2.2.3).
func AppendQualityMode(w *wire.Writer, mode uint16) {
	w.U8(MsgQualityMode)
	w.U8(0)
	w.U16LE(4)
	w.U16LE(mode)
	w.U16LE(0)
}

// AppendTraining appends a Training PDU without data (2.2.3.1), whose
// wPackSize is then 0.
func AppendTraining(w *wire.Writer, timeStamp uint16) {
	w.U8(MsgTraining)
	w.U8(0)
	w.U16LE(4)
	w.U16LE(timeStamp)
	w.U16LE(0)
}

// AppendTrainingConfirm appends a Training Confirm PDU (2.2.3.2).
func AppendTrainingConfirm(w *wire.Writer, timeStamp, packSize uint16) {
	w.U8(MsgTraining)
	w.U8(0)
	w.U16LE(4)
	w.U16LE(timeStamp)
	w.U16LE(packSize)
}

// AppendWaveInfo appends the WaveInfo PDU of audio (2.2.3.3), which
// carries its first 4 bytes; the rest goes in the Wave PDU after it
// (AppendWave). audio must be longer than 4 bytes (3.3.5.2.1.1).
func AppendWaveInfo(w *wire.Writer, timeStamp, formatNo uint16, block uint8, audio []byte) error {
	if len(audio) <= 4 {
		return errors.New(TagWaveTooShort, "WaveInfo needs more than 4 bytes of audio", "length", len(audio))
	}
	if len(audio)+waveInfoBody > 0xffff {
		return errors.New(TagWaveTooLong, "audio too long for one WaveInfo", "length", len(audio))
	}
	w.U8(MsgWave)
	w.U8(0)
	w.U16LE(uint16(len(audio) + waveInfoBody))
	w.U16LE(timeStamp)
	w.U16LE(formatNo)
	w.U8(block)
	w.Zeros(3)
	w.Write(audio[:4])
	return nil
}

// AppendWave appends the Wave PDU that follows the WaveInfo of audio
// (2.2.3.4): 4 zero bytes in place of the first 4, then the rest.
func AppendWave(w *wire.Writer, audio []byte) {
	w.Zeros(4)
	if len(audio) > 4 {
		w.Write(audio[4:])
	}
}

// ReadWave reads a Wave PDU and returns its audio after the first 4
// bytes, which the WaveInfo carried; size is the length the WaveInfo
// announced.
func ReadWave(data []byte, size int) ([]byte, error) {
	if len(data) != size {
		return nil, errors.New(TagBodySizeInvalid, "a Wave PDU of another length than its WaveInfo announced",
			"length", len(data), "announced", size)
	}
	return data[4:], nil
}

// AppendWave2 appends a Wave2 PDU (2.2.3.10).
func AppendWave2(w *wire.Writer, timeStamp, formatNo uint16, block uint8, audioTimeStamp uint32, audio []byte) error {
	if len(audio)+wave2Body > 0xffff {
		return errors.New(TagWaveTooLong, "audio too long for one Wave2", "length", len(audio))
	}
	w.U8(MsgWave2)
	w.U8(0)
	w.U16LE(uint16(len(audio) + wave2Body))
	w.U16LE(timeStamp)
	w.U16LE(formatNo)
	w.U8(block)
	w.Zeros(3)
	w.U32LE(audioTimeStamp)
	w.Write(audio)
	return nil
}

// AppendWaveConfirm appends a Wave Confirm PDU (2.2.3.8).
func AppendWaveConfirm(w *wire.Writer, timeStamp uint16, block uint8) {
	w.U8(MsgWaveConfirm)
	w.U8(0)
	w.U16LE(4)
	w.U16LE(timeStamp)
	w.U8(block)
	w.U8(0)
}

// AppendClose appends a Close PDU (2.2.3.9).
func AppendClose(w *wire.Writer) {
	w.U8(MsgClose)
	w.U8(0)
	w.U16LE(0)
}
