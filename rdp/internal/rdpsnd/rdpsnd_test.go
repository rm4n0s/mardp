package rdpsnd

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// unhex turns a dump of Microsoft's examples into bytes.
func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The formats of the examples 4.1.1 and 4.1.2: PCM, A-law, mu-law, ADPCM
// and IMA ADPCM, at 22050 Hz stereo.
const exampleFormats = `01 00 02 00 22 56 00 00 88 58 01 00 04 00 10 00 00 00
06 00 02 00 22 56 00 00 44 ac 00 00 02 00 08 00 00 00
07 00 02 00 22 56 00 00 44 ac 00 00 02 00 08 00 00 00
02 00 02 00 22 56 00 00 27 57 00 00 00 04 04 00 20 00 f4 03 07 00 00 01
00 00 00 02 00 ff 00 00 00 00 c0 00 40 00 f0 00 00 00 cc 01 30 ff 88 01 18 ff
11 00 02 00 22 56 00 00 b9 56 00 00 00 04 04 00 02 00 f9 03`

// MS-RDPEA 4.1.1: the Server Audio Formats and Version PDU.
// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpea/004d76f8-b88d-42b7-a0aa-1c77a39390bb
const exampleServerFormats = `07 2b 90 00 08 fb 8b 00 e0 f1 09 00 70 27 1f 77 00 00 05 00 ff 05 00 00` + "\n" + exampleFormats

// MS-RDPEA 4.1.2: the Client Audio Formats and Version PDU.
// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpea/322ca222-7cfb-4a58-b2af-a44265ebcd34
const exampleClientFormats = `07 00 90 00 03 00 00 00 ff ff ff ff 00 f7 f9 00 00 00 05 00 28 05 00 7c` + "\n" + exampleFormats

// wantFormats are the formats of the examples.
var wantFormats = []Format{
	{Tag: FormatPCM, Channels: 2, SamplesPerSec: 22050, AvgBytesPerSec: 88200, BlockAlign: 4, BitsPerSample: 16},
	{Tag: 6, Channels: 2, SamplesPerSec: 22050, AvgBytesPerSec: 44100, BlockAlign: 2, BitsPerSample: 8},
	{Tag: 7, Channels: 2, SamplesPerSec: 22050, AvgBytesPerSec: 44100, BlockAlign: 2, BitsPerSample: 8},
	{Tag: 2, Channels: 2, SamplesPerSec: 22050, AvgBytesPerSec: 22311, BlockAlign: 1024, BitsPerSample: 4,
		Extra: []byte{0xf4, 0x03, 0x07, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x02, 0x00, 0xff, 0x00, 0x00, 0x00, 0x00,
			0xc0, 0x00, 0x40, 0x00, 0xf0, 0x00, 0x00, 0x00, 0xcc, 0x01, 0x30, 0xff, 0x88, 0x01, 0x18, 0xff}},
	{Tag: 0x11, Channels: 2, SamplesPerSec: 22050, AvgBytesPerSec: 22201, BlockAlign: 1024, BitsPerSample: 4, Extra: []byte{0xf9, 0x03}},
}

// zeroPads clears the bytes at offsets that are padding, which the
// examples fill with arbitrary values and the writers with zeros.
func zeroPads(b []byte, offsets ...int) []byte {
	b = append([]byte(nil), b...)
	for _, o := range offsets {
		b[o] = 0
	}
	return b
}

func TestFormatsExamples(t *testing.T) {
	for _, tt := range []struct {
		name   string
		dump   string
		read   func([]byte) (PDU, error)
		write  func(*wire.Writer, Formats) error
		want   Formats
		padOff []int
	}{
		{
			name: "server, 4.1.1", dump: exampleServerFormats, read: ReadServerPDU,
			write: func(w *wire.Writer, f Formats) error {
				return AppendServerFormats(w, f.Version, f.LastBlock, f.Formats)
			},
			// The server's flags, volume and pitch are arbitrary; the
			// writer sends zeros.
			want:   Formats{Flags: 0x008bfb08, Volume: 0x0009f1e0, Pitch: 0x771f2770, LastBlock: 0xff, Version: 5, Formats: wantFormats},
			padOff: []int{1, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 23},
		},
		{
			name: "client, 4.1.2", dump: exampleClientFormats, read: ReadClientPDU, write: AppendClientFormats,
			want:   Formats{Flags: CapsAlive | CapsVolume, Volume: 0xffffffff, Pitch: 0x00f9f700, LastBlock: 0x28, Version: 5, Formats: wantFormats},
			padOff: []int{1, 23},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := unhex(t, tt.dump)
			p, err := tt.read(data)
			if err != nil {
				t.Fatal(err)
			}
			if p.Type != MsgFormats {
				t.Fatalf("type %d", p.Type)
			}
			got := p.Formats
			if got.Flags != tt.want.Flags || got.Volume != tt.want.Volume || got.Pitch != tt.want.Pitch ||
				got.DGramPort != 0 || got.LastBlock != tt.want.LastBlock || got.Version != tt.want.Version || len(got.Formats) != len(tt.want.Formats) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			for i := range got.Formats {
				if !got.Formats[i].Equal(tt.want.Formats[i]) {
					t.Errorf("format %d: %+v, want %+v", i, got.Formats[i], tt.want.Formats[i])
				}
			}
			w := wire.NewWriter(nil)
			if err := tt.write(&w, got); err != nil {
				t.Fatal(err)
			}
			if want := zeroPads(data, tt.padOff...); !bytes.Equal(w.Bytes(), want) {
				t.Errorf("written\n%x\nwant\n%x", w.Bytes(), want)
			}
		})
	}
}

func TestTrainingExamples(t *testing.T) {
	// MS-RDPEA 4.1.3, its header and fields; the data is not shown whole.
	// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpea/19f0b33d-3e6e-458c-b744-c8e55a420c65
	training := append(unhex(t, "06 23 fc 03 da 89 00 04"), make([]byte, 0x3fc-4)...)
	p, err := ReadServerPDU(training)
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != MsgTraining || p.TimeStamp != 0x89da || p.PackSize != 0x400 || len(p.Data) != 0x3fc-4 {
		t.Errorf("training: %+v", p)
	}
	// MS-RDPEA 4.1.4.
	// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpea/b6630619-933e-480c-a8c9-5d5fa5defc60
	confirm := unhex(t, "06 55 04 00 da 89 00 04")
	if p, err = ReadClientPDU(confirm); err != nil {
		t.Fatal(err)
	}
	if p.Type != MsgTraining || p.TimeStamp != 0x89da || p.PackSize != 0x400 {
		t.Errorf("confirm: %+v", p)
	}
	w := wire.NewWriter(nil)
	AppendTrainingConfirm(&w, 0x89da, 0x400)
	if want := zeroPads(confirm, 1); !bytes.Equal(w.Bytes(), want) {
		t.Errorf("confirm written %x, want %x", w.Bytes(), want)
	}
	// The server's own Training has no data, so wPackSize is 0.
	w.Reset()
	AppendTraining(&w, 0x1234)
	if want := unhex(t, "06 00 04 00 34 12 00 00"); !bytes.Equal(w.Bytes(), want) {
		t.Errorf("training written %x, want %x", w.Bytes(), want)
	}
	if p, err = ReadServerPDU(w.Bytes()); err != nil || p.TimeStamp != 0x1234 || p.PackSize != 0 || len(p.Data) != 0 {
		t.Errorf("training read back: %+v, %v", p, err)
	}
}

func TestWaveExamples(t *testing.T) {
	// MS-RDPEA 4.2.1 and 4.2.2: a WaveInfo announcing 585 bytes of audio,
	// and the start of its Wave (the dump leaves the middle out).
	// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpea/16aac026-93fb-4de4-aa37-f02410c61ce8
	// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpea/8d0b1873-88c9-40f7-9eb3-08a4813733ef
	info := unhex(t, "02 7e 51 02 d7 ad 0f 00 08 00 00 00 20 48 17 d6")
	waveStart := unhex(t, `00 00 00 00 84 02 80 24 49 92 24 89 02 80 24 49
		92 24 89 02 80 24 49 92 24 89 02 80 24 49 92 24`)
	p, err := ReadServerPDU(info)
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != MsgWave || p.TimeStamp != 0xadd7 || p.FormatNo != 15 || p.Block != 8 || p.WaveSize != 585 ||
		!bytes.Equal(p.Data, []byte{0x20, 0x48, 0x17, 0xd6}) {
		t.Errorf("WaveInfo: %+v", p)
	}
	audio := make([]byte, 585)
	copy(audio, p.Data)
	copy(audio[4:], waveStart[4:])
	for i := len(waveStart); i < len(audio); i++ {
		audio[i] = byte(i)
	}
	w := wire.NewWriter(nil)
	if err := AppendWaveInfo(&w, 0xadd7, 15, 8, audio); err != nil {
		t.Fatal(err)
	}
	if want := zeroPads(info, 1); !bytes.Equal(w.Bytes(), want) {
		t.Errorf("WaveInfo written %x, want %x", w.Bytes(), want)
	}
	w.Reset()
	AppendWave(&w, audio)
	if !bytes.HasPrefix(w.Bytes(), waveStart) || w.Len() != len(audio) {
		t.Errorf("Wave written %x…, %d bytes", w.Bytes()[:32], w.Len())
	}
	rest, err := ReadWave(w.Bytes(), p.WaveSize)
	if err != nil || !bytes.Equal(rest, audio[4:]) {
		t.Errorf("Wave read back: %v", err)
	}

	// MS-RDPEA 4.2.4: the header of a Wave2 of 248 bytes of audio.
	// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpea/ecafc3ff-cf98-4864-bb1a-2996d8e3a3b3
	head := unhex(t, "0d 00 04 01 16 a1 03 00 02 00 00 00 c2 b8 ac 0d")
	audio = make([]byte, 248)
	for i := range audio {
		audio[i] = byte(i * 7)
	}
	w.Reset()
	if err := AppendWave2(&w, 0xa116, 3, 2, 0x0dacb8c2, audio); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Bytes()[:16], head) || !bytes.Equal(w.Bytes()[16:], audio) {
		t.Errorf("Wave2 written %x…, want %x…", w.Bytes()[:16], head)
	}
	if p, err = ReadServerPDU(w.Bytes()); err != nil {
		t.Fatal(err)
	}
	if p.Type != MsgWave2 || p.TimeStamp != 0xa116 || p.FormatNo != 3 || p.Block != 2 || p.AudioTimeStamp != 229423298 || !bytes.Equal(p.Data, audio) {
		t.Errorf("Wave2 read back: %+v", p)
	}

	// MS-RDPEA 4.2.3.
	// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpea/793a8fc0-4c77-49c0-b79a-a6a5738eb087
	confirm := unhex(t, "05 39 04 00 b7 5a 08 77")
	if p, err = ReadClientPDU(confirm); err != nil {
		t.Fatal(err)
	}
	if p.Type != MsgWaveConfirm || p.TimeStamp != 0x5ab7 || p.Block != 8 {
		t.Errorf("Wave Confirm: %+v", p)
	}
	w.Reset()
	AppendWaveConfirm(&w, 0x5ab7, 8)
	if want := zeroPads(confirm, 1, 7); !bytes.Equal(w.Bytes(), want) {
		t.Errorf("Wave Confirm written %x, want %x", w.Bytes(), want)
	}
}

func TestQualityModeAndClose(t *testing.T) {
	w := wire.NewWriter(nil)
	AppendQualityMode(&w, QualityHigh)
	if want := unhex(t, "0c 00 04 00 02 00 00 00"); !bytes.Equal(w.Bytes(), want) {
		t.Errorf("Quality Mode %x, want %x", w.Bytes(), want)
	}
	p, err := ReadClientPDU(w.Bytes())
	if err != nil || p.Type != MsgQualityMode || p.QualityMode != QualityHigh {
		t.Errorf("Quality Mode read back: %+v, %v", p, err)
	}
	w.Reset()
	AppendClose(&w)
	if want := unhex(t, "01 00 00 00"); !bytes.Equal(w.Bytes(), want) {
		t.Errorf("Close %x, want %x", w.Bytes(), want)
	}
	if p, err = ReadServerPDU(w.Bytes()); err != nil || p.Type != MsgClose {
		t.Errorf("Close read back: %+v, %v", p, err)
	}
}

func TestFormatEqual(t *testing.T) {
	a := wantFormats[3]
	for _, tt := range []struct {
		name string
		b    Format
		want bool
	}{
		{"same", wantFormats[3], true},
		{"another tag", wantFormats[0], false},
		{"other extra", Format{Tag: a.Tag, Channels: a.Channels, SamplesPerSec: a.SamplesPerSec, AvgBytesPerSec: a.AvgBytesPerSec,
			BlockAlign: a.BlockAlign, BitsPerSample: a.BitsPerSample, Extra: append(append([]byte(nil), a.Extra[:31]...), 0)}, false},
		{"no extra", Format{Tag: a.Tag, Channels: a.Channels, SamplesPerSec: a.SamplesPerSec, AvgBytesPerSec: a.AvgBytesPerSec,
			BlockAlign: a.BlockAlign, BitsPerSample: a.BitsPerSample}, false},
	} {
		if got := a.Equal(tt.b); got != tt.want {
			t.Errorf("%s: %v", tt.name, got)
		}
	}
}

func TestErrors(t *testing.T) {
	long := make([]byte, 0x10000)
	for _, tt := range []struct {
		name  string
		run   func() error
		route string
	}{
		{"client PDU type", func() error { _, err := ReadClientPDU([]byte{MsgWave2, 0, 0, 0}); return err },
			"ReadClientPDU." + TagTypeUnexpected},
		{"server PDU type", func() error { _, err := ReadServerPDU([]byte{MsgWaveConfirm, 0, 4, 0, 0, 0, 0, 0}); return err },
			"ReadServerPDU." + TagTypeUnexpected},
		{"body past the end", func() error { _, err := ReadClientPDU([]byte{MsgWaveConfirm, 0, 8, 0, 0, 0, 0, 0}); return err },
			"ReadClientPDU->body." + TagBodySizeInvalid},
		{"body too short", func() error { _, err := ReadClientPDU([]byte{MsgQualityMode, 0, 2, 0, 0, 0}); return err },
			"ReadClientPDU->body." + TagBodySizeInvalid},
		{"WaveInfo of 4 bytes", func() error {
			_, err := ReadServerPDU([]byte{MsgWave, 0, 12, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3, 4})
			return err
		}, "ReadServerPDU." + TagBodySizeInvalid},
		{"Wave of another length", func() error { _, err := ReadWave(make([]byte, 9), 10); return err },
			"ReadWave." + TagBodySizeInvalid},
		{"more formats than bytes", func() error {
			b := unhex(t, exampleClientFormats)
			b[18] = 0xff
			_, err := ReadClientPDU(b)
			return err
		}, "ReadClientPDU->readFormats." + TagFormatsInvalid},
		{"extra data past the end", func() error {
			b := unhex(t, exampleClientFormats)
			b[len(b)-3] = 3 // the last format's cbSize
			_, err := ReadClientPDU(b)
			return err
		}, "ReadClientPDU->readFormats->readFormat." + TagFormatsInvalid},
		{"formats too long", func() error {
			w := wire.NewWriter(nil)
			return AppendServerFormats(&w, VersionWave2, 0, []Format{{Tag: FormatAAC, Extra: long}})
		}, "AppendServerFormats->appendFormats." + TagFormatsInvalid},
		{"WaveInfo of 4 bytes written", func() error { w := wire.NewWriter(nil); return AppendWaveInfo(&w, 0, 0, 0, []byte{1, 2, 3, 4}) },
			"AppendWaveInfo." + TagWaveTooShort},
		{"WaveInfo too long", func() error { w := wire.NewWriter(nil); return AppendWaveInfo(&w, 0, 0, 0, long) },
			"AppendWaveInfo." + TagWaveTooLong},
		{"Wave2 too long", func() error { w := wire.NewWriter(nil); return AppendWave2(&w, 0, 0, 0, 0, long) },
			"AppendWave2." + TagWaveTooLong},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			appErr, ok := errors.FromError(err)
			if !ok {
				t.Fatalf("expected *errors.Error, got %T (%v)", err, err)
			}
			if !appErr.HasRoute(tt.route) {
				t.Errorf("unexpected failure path: %s", appErr.Route())
			}
		})
	}
}

func TestTruncated(t *testing.T) {
	// Every prefix of every PDU fails without a panic.
	w := wire.NewWriter(nil)
	if err := AppendWave2(&w, 1, 2, 3, 4, []byte{5, 6, 7, 8, 9}); err != nil {
		t.Fatal(err)
	}
	pdus := [][]byte{unhex(t, exampleClientFormats), unhex(t, exampleServerFormats), w.Bytes(),
		unhex(t, "02 7e 51 02 d7 ad 0f 00 08 00 00 00 20 48 17 d6"), unhex(t, "05 39 04 00 b7 5a 08 77")}
	for _, pdu := range pdus {
		for n := range len(pdu) {
			if _, err := ReadClientPDU(pdu[:n]); err == nil {
				t.Errorf("client: %x accepted", pdu[:n])
			}
			_, _ = ReadServerPDU(pdu[:n]) // must not panic
		}
	}
}
