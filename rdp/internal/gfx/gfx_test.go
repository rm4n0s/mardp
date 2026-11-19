package gfx

import (
	"bytes"
	"encoding/hex"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func checkRoute(t *testing.T, err error, route string) {
	t.Helper()
	appErr, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("expected *errors.Error, got %T %v", err, err)
	}
	if !appErr.HasRoute(route) {
		t.Errorf("unexpected failure path: %s, want %s", appErr.Route(), route)
	}
}

// readAll returns the PDUs of b, read by read.
func readAll(t *testing.T, b []byte, read func([]byte, func(PDU) error) error) []PDU {
	t.Helper()
	var ps []PDU
	if err := read(b, func(p PDU) error { ps = append(ps, p); return nil }); err != nil {
		t.Fatal(err)
	}
	return ps
}

// TestPDUs writes every PDU the package writes, compares the bytes with the
// layout of MS-RDPEGFX 2.2 (Microsoft gives no examples of them), and reads
// them back.
func TestPDUs(t *testing.T) {
	// The sets a client advertises: 8.0 without flags and 10.1, whose 16
	// bytes are reserved.
	advertise := []CapSet{NewCapSet(Version8, 0), NewCapSet(Version101, 0)}
	tests := []struct {
		name  string
		write func(w *wire.Writer) error
		bytes string
		read  func([]byte, func(PDU) error) error
		want  PDU
	}{
		{"caps confirm", func(w *wire.Writer) error { AppendCapsConfirm(w, NewCapSet(Version81, FlagAVC420Enabled)); return nil },
			"13 00 00 00 14 00 00 00  05 01 08 00 04 00 00 00 10 00 00 00", ReadServer,
			PDU{Cmd: CmdCapsConfirm, CapSet: CapSet{Version: Version81, Data: []byte{0x10, 0, 0, 0}}}},
		{"caps advertise", func(w *wire.Writer) error { AppendCapsAdvertise(w, advertise); return nil },
			"12 00 00 00 2e 00 00 00 02 00  04 00 08 00 04 00 00 00 00 00 00 00  00 01 0a 00 10 00 00 00" + strings.Repeat(" 00", 16), ReadClient,
			PDU{Cmd: CmdCapsAdvertise, CapSets: advertise}},
		{"create surface", func(w *wire.Writer) error { return AppendCreateSurface(w, 1, 1024, 768, PixelFormatXRGB) },
			"09 00 00 00 0f 00 00 00  01 00 00 04 00 03 20", ReadServer,
			PDU{Cmd: CmdCreateSurface, SurfaceID: 1, Width: 1024, Height: 768, PixelFormat: PixelFormatXRGB}},
		{"map surface to output", func(w *wire.Writer) error { AppendMapSurfaceToOutput(w, 1, 2, 3); return nil },
			"0f 00 00 00 14 00 00 00  01 00 00 00 02 00 00 00 03 00 00 00", ReadServer,
			PDU{Cmd: CmdMapSurfaceToOutput, SurfaceID: 1, OriginX: 2, OriginY: 3}},
		{"wire to surface 1", func(w *wire.Writer) error {
			off := BeginWireToSurface1(w, 1, PixelFormatXRGB, Rect16{1, 2, 3, 4})
			w.Write([]byte{9, 8})
			return EndWireToSurface1(w, off, CodecPlanar)
		},
			"01 00 00 00 1b 00 00 00  01 00 0a 00 20  01 00 02 00 03 00 04 00  02 00 00 00  09 08", ReadServer,
			PDU{Cmd: CmdWireToSurface1, SurfaceID: 1, CodecID: CodecPlanar, PixelFormat: PixelFormatXRGB, Rect: Rect16{1, 2, 3, 4}, Body: []byte{9, 8}}},
		{"start frame", func(w *wire.Writer) error { AppendStartFrame(w, 0x12345678, 7); return nil },
			"0b 00 00 00 10 00 00 00  78 56 34 12 07 00 00 00", ReadServer,
			PDU{Cmd: CmdStartFrame, Timestamp: 0x12345678, FrameID: 7}},
		{"end frame", func(w *wire.Writer) error { AppendEndFrame(w, 7); return nil },
			"0c 00 00 00 0c 00 00 00  07 00 00 00", ReadServer,
			PDU{Cmd: CmdEndFrame, FrameID: 7}},
		{"cache import reply", func(w *wire.Writer) error { AppendCacheImportReply(w, []uint16{5}); return nil },
			"11 00 00 00 0c 00 00 00  01 00 05 00", ReadServer,
			PDU{Cmd: CmdCacheImportReply, CacheSlots: []uint16{5}}},
		{"frame acknowledge", func(w *wire.Writer) error { AppendFrameAcknowledge(w, 0, 7, 3); return nil },
			"0d 00 00 00 14 00 00 00  00 00 00 00 07 00 00 00 03 00 00 00", ReadClient,
			PDU{Cmd: CmdFrameAcknowledge, FrameID: 7, TotalFramesDecoded: 3}},
		{"suspend", func(w *wire.Writer) error { AppendFrameAcknowledge(w, SuspendFrameAcknowledgement, 8, 4); return nil },
			"0d 00 00 00 14 00 00 00  ff ff ff ff 08 00 00 00 04 00 00 00", ReadClient,
			PDU{Cmd: CmdFrameAcknowledge, QueueDepth: SuspendFrameAcknowledgement, FrameID: 8, TotalFramesDecoded: 4}},
		{"qoe frame acknowledge", func(w *wire.Writer) error { AppendQoEFrameAcknowledge(w, 7, 5, 1, 2); return nil },
			"16 00 00 00 14 00 00 00  07 00 00 00 05 00 00 00 01 00 02 00", ReadClient,
			PDU{Cmd: CmdQoEFrameAcknowledge, FrameID: 7, Timestamp: 5}},
		{"cache import offer", func(w *wire.Writer) error { AppendCacheImportOffer(w, []uint64{1}); return nil },
			"10 00 00 00 12 00 00 00  01 00 01 00 00 00 00 00 00 00", ReadClient,
			PDU{Cmd: CmdCacheImportOffer, CacheEntries: 1}},
		{"reset graphics", func(w *wire.Writer) error {
			return AppendResetGraphics(w, 1920, 1080, []Monitor{{Right: 1919, Bottom: 1079, Flags: MonitorPrimary}})
		}, "0e 00 00 00 54 01 00 00  80 07 00 00 38 04 00 00 01 00 00 00  00 00 00 00 00 00 00 00 7f 07 00 00 37 04 00 00 01 00 00 00" +
			strings.Repeat(" 00", 340-40), ReadServer,
			PDU{Cmd: CmdResetGraphics, Width: 1920, Height: 1080, Monitors: []Monitor{{Right: 1919, Bottom: 1079, Flags: MonitorPrimary}}}},
		{"unknown command", func(w *wire.Writer) error { w.Write([]byte{0x04, 0, 0, 0, 0x0a, 0, 0, 0, 1, 2}); return nil },
			"04 00 00 00 0a 00 00 00  01 02", ReadClient,
			PDU{Cmd: CmdSolidFill, Body: []byte{1, 2}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := wire.NewWriter(nil)
			if err := tc.write(&w); err != nil {
				t.Fatal(err)
			}
			if want := unhex(t, tc.bytes); !bytes.Equal(w.Bytes(), want) {
				t.Fatalf("written % x\nwant    % x", w.Bytes(), want)
			}
			ps := readAll(t, w.Bytes(), tc.read)
			if len(ps) != 1 || !reflect.DeepEqual(ps[0], tc.want) {
				t.Errorf("read %+v, want %+v", ps, tc.want)
			}
		})
	}
}

// TestFrame reads several PDUs from one message, the length patched by End.
func TestFrame(t *testing.T) {
	w := wire.NewWriter(nil)
	AppendStartFrame(&w, 1, 9)
	off := Begin(&w, CmdSolidFill)
	w.Write([]byte{1, 2, 3})
	if err := End(&w, off); err != nil {
		t.Fatal(err)
	}
	AppendEndFrame(&w, 9)
	ps := readAll(t, w.Bytes(), ReadServer)
	want := []PDU{{Cmd: CmdStartFrame, Timestamp: 1, FrameID: 9}, {Cmd: CmdSolidFill, Body: []byte{1, 2, 3}}, {Cmd: CmdEndFrame, FrameID: 9}}
	if !reflect.DeepEqual(ps, want) {
		t.Errorf("read %+v", ps)
	}
	// each's error stops the reading.
	stop := errors.New("TestStop", "stop")
	n := 0
	err := ReadServer(w.Bytes(), func(PDU) error { n++; return stop })
	if !errors.Is(err, stop) || n != 1 {
		t.Errorf("err %v after %d PDUs", err, n)
	}
}

// TestCapSets checks which sets are valid and which allow H.264.
func TestCapSets(t *testing.T) {
	tests := []struct {
		set    CapSet
		valid  bool
		avc420 bool
	}{
		{NewCapSet(Version8, 0), true, false},
		{NewCapSet(Version81, 0), true, false},
		{NewCapSet(Version81, FlagAVC420Enabled), true, true},
		{NewCapSet(Version10, 0), true, true},
		{NewCapSet(Version10, FlagAVCDisabled), true, false},
		{NewCapSet(Version101, 0), true, true},
		{NewCapSet(Version102, FlagSmallCache), true, true},
		{NewCapSet(Version103, FlagAVCDisabled|FlagThinClient), true, false},
		{NewCapSet(Version104, 0), true, true},
		{NewCapSet(Version105, 0), true, true},
		{NewCapSet(Version106, FlagAVCThinClient), true, true},
		{NewCapSet(Version107, FlagScaledMapDisable), true, true},
		{CapSet{Version: Version101, Data: make([]byte, 4)}, false, true},
		{CapSet{Version: Version107, Data: make([]byte, 2)}, false, true},
		{CapSet{Version: 0x000B0000, Data: make([]byte, 4)}, false, false},
	}
	for _, tc := range tests {
		if tc.set.Valid() != tc.valid || tc.set.AVC420() != tc.avc420 {
			t.Errorf("%#x % x: valid %v, avc420 %v", tc.set.Version, tc.set.Data, tc.set.Valid(), tc.set.AVC420())
		}
	}
	if f := NewCapSet(Version106, 0x01020304).Flags(); f != 0x01020304 {
		t.Errorf("flags %#x", f)
	}
	if f := (CapSet{Version: Version8}).Flags(); f != 0 {
		t.Errorf("flags of no data %#x", f)
	}
}

// TestTimestamp checks the layout of 2.2.2.11.
func TestTimestamp(t *testing.T) {
	at := time.Date(2026, 11, 19, 13, 45, 30, 123_456_789, time.FixedZone("", 2*3600))
	want := uint32(11)<<22 | 45<<16 | 30<<10 | 123
	if got := Timestamp(at); got != want {
		t.Errorf("%#x, want %#x", got, want)
	}
}

// TestWrap wraps data of the sizes around a segment's and unwraps it.
func TestWrap(t *testing.T) {
	w := wire.NewWriter(nil)
	Wrap(&w, []byte{1, 2, 3})
	if want := unhex(t, "e0 04 01 02 03"); !bytes.Equal(w.Bytes(), want) {
		t.Errorf("single % x", w.Bytes())
	}
	w.Reset()
	Wrap(&w, make([]byte, MaxSegment+1))
	// Two segments: 65,535 bytes and 1, each with its header byte.
	if want := unhex(t, "e1 02 00 00 00 01 00 00 00 01 00 04"); !bytes.Equal(w.Bytes()[:12], want) {
		t.Errorf("multipart % x", w.Bytes()[:12])
	}
	if want := unhex(t, "02 00 00 00 04 00"); !bytes.Equal(w.Bytes()[12+MaxSegment:], want) {
		t.Errorf("last segment % x", w.Bytes()[12+MaxSegment:])
	}

	rng := rand.New(rand.NewPCG(1, 2))
	for _, n := range []int{0, 1, MaxSegment - 1, MaxSegment, MaxSegment + 1, 2 * MaxSegment, 3*MaxSegment + 7} {
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(rng.Uint32())
		}
		w.Reset()
		Wrap(&w, data)
		if w.Len() != WrappedLen(n) {
			t.Errorf("%d bytes wrapped in %d, WrappedLen %d", n, w.Len(), WrappedLen(n))
		}
		got, err := Unwrap(w.Bytes(), nil)
		if err != nil {
			t.Fatalf("%d bytes: %v", n, err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("%d bytes unwrapped into %d", n, len(got))
		}
	}
}

// TestErrors checks the route of every failure.
func TestErrors(t *testing.T) {
	appendTo := func(f func(w *wire.Writer)) []byte {
		w := wire.NewWriter(nil)
		f(&w)
		return w.Bytes()
	}
	reset := appendTo(func(w *wire.Writer) {
		if err := AppendResetGraphics(w, 800, 600, nil); err != nil {
			t.Fatal(err)
		}
	})
	monitors := bytes.Clone(reset)
	monitors[16] = MaxMonitors + 1
	tests := []struct {
		name  string
		run   func() error
		route string
	}{
		{"length shorter than the header", func() error {
			return ReadClient(unhex(t, "0d 00 00 00 07 00 00 00"), func(PDU) error { return nil })
		}, "ReadClient->readPDUs." + TagLengthInvalid},
		{"length longer than the data", func() error {
			return ReadClient(unhex(t, "0d 00 00 00 14 00 00 00 00"), func(PDU) error { return nil })
		}, "ReadClient->readPDUs." + TagLengthInvalid},
		{"header cut", func() error {
			return ReadClient(unhex(t, "0d 00 00"), func(PDU) error { return nil })
		}, "ReadClient->readPDUs->Reader.U16LE." + wire.TagTruncated},
		{"fields cut", func() error {
			return ReadClient(unhex(t, "0d 00 00 00 0c 00 00 00 00 00 00 00"), func(PDU) error { return nil })
		}, "ReadClient->readPDUs->parseClient->Reader.U32LE." + wire.TagTruncated},
		{"capability set too long", func() error {
			return ReadClient(unhex(t, "12 00 00 00 12 00 00 00 01 00 04 00 08 00 05 00 00 00"), func(PDU) error { return nil })
		}, "ReadClient->readPDUs->parseClient->readCapSet." + TagCapsInvalid},
		{"too many entries offered", func() error {
			return ReadClient(unhex(t, "10 00 00 00 0a 00 00 00 56 15"), func(PDU) error { return nil })
		}, "ReadClient->readPDUs->parseClient." + TagCountInvalid},
		{"bitmap data length wrong", func() error {
			return ReadServer(unhex(t, "01 00 00 00 1b 00 00 00 01 00 0a 00 20 01 00 02 00 03 00 04 00 03 00 00 00 09 08"), func(PDU) error { return nil })
		}, "ReadServer->readPDUs->parseServer->parseWireToSurface1." + TagLengthInvalid},
		{"wire to surface 1 cut", func() error {
			return ReadServer(unhex(t, "01 00 00 00 0c 00 00 00 01 00 0a 00"), func(PDU) error { return nil })
		}, "ReadServer->readPDUs->parseServer->parseWireToSurface1->Reader.U8." + wire.TagTruncated},
		{"too many slots imported", func() error {
			return ReadServer(unhex(t, "11 00 00 00 0a 00 00 00 56 15"), func(PDU) error { return nil })
		}, "ReadServer->readPDUs->parseServer." + TagCountInvalid},
		{"reset graphics not 340 bytes", func() error {
			return ReadServer(unhex(t, "0e 00 00 00 14 00 00 00 20 03 00 00 58 02 00 00 00 00 00 00"), func(PDU) error { return nil })
		}, "ReadServer->readPDUs->parseServer->parseResetGraphics." + TagLengthInvalid},
		{"reset graphics with too many monitors", func() error {
			return ReadServer(monitors, func(PDU) error { return nil })
		}, "ReadServer->readPDUs->parseServer->parseResetGraphics." + TagCountInvalid},
		{"output too large", func() error {
			w := wire.NewWriter(nil)
			return AppendResetGraphics(&w, MaxOutput+1, 600, nil)
		}, "AppendResetGraphics." + TagSizeInvalid},
		{"output empty", func() error {
			w := wire.NewWriter(nil)
			return AppendResetGraphics(&w, 800, 0, nil)
		}, "AppendResetGraphics." + TagSizeInvalid},
		{"too many monitors", func() error {
			w := wire.NewWriter(nil)
			return AppendResetGraphics(&w, 800, 600, make([]Monitor, MaxMonitors+1))
		}, "AppendResetGraphics." + TagCountInvalid},
		{"surface too large", func() error {
			w := wire.NewWriter(nil)
			return AppendCreateSurface(&w, 1, 800, MaxOutput+1, PixelFormatXRGB)
		}, "AppendCreateSurface." + TagSizeInvalid},
		{"empty", func() error { _, err := Unwrap(nil, nil); return err }, "Unwrap->Reader.U8." + wire.TagTruncated},
		{"unknown descriptor", func() error { _, err := Unwrap(unhex(t, "e2 04"), nil); return err }, "Unwrap." + TagSegmentInvalid},
		{"segment without its header", func() error { _, err := Unwrap(unhex(t, "e0"), nil); return err }, "Unwrap->segment." + TagSegmentInvalid},
		{"compressed", func() error { _, err := Unwrap(unhex(t, "e0 24 01"), nil); return err }, "Unwrap->segment." + TagCompressed},
		{"another compression type", func() error { _, err := Unwrap(unhex(t, "e0 03 01"), nil); return err }, "Unwrap->segment." + TagSegmentInvalid},
		{"size larger than the data", func() error {
			_, err := Unwrap(unhex(t, "e1 01 00 09 00 00 00 02 00 00 00 04 01"), nil)
			return err
		}, "Unwrap." + TagSegmentInvalid},
		{"segment longer than the data", func() error {
			_, err := Unwrap(unhex(t, "e1 01 00 01 00 00 00 03 00 00 00 04 01"), nil)
			return err
		}, "Unwrap." + TagSegmentInvalid},
		{"segments shorter than the size", func() error {
			_, err := Unwrap(unhex(t, "e1 01 00 02 00 00 00 02 00 00 00 04 01 00"), nil)
			return err
		}, "Unwrap." + TagSegmentInvalid},
		{"compressed part", func() error {
			_, err := Unwrap(unhex(t, "e1 01 00 01 00 00 00 02 00 00 00 24 01"), nil)
			return err
		}, "Unwrap->segment." + TagCompressed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkRoute(t, tc.run(), tc.route)
		})
	}
}

// FuzzRead reads random messages as both sides and unwraps them.
func FuzzRead(f *testing.F) {
	w := wire.NewWriter(nil)
	AppendCapsAdvertise(&w, []CapSet{NewCapSet(Version81, FlagAVC420Enabled), NewCapSet(Version101, 0)})
	AppendFrameAcknowledge(&w, 1, 2, 3)
	AppendQoEFrameAcknowledge(&w, 1, 2, 3, 4)
	AppendCacheImportOffer(&w, []uint64{1, 2})
	f.Add(bytes.Clone(w.Bytes()))
	w.Reset()
	AppendCapsConfirm(&w, NewCapSet(Version107, 0))
	if err := AppendResetGraphics(&w, 800, 600, []Monitor{{Right: 799, Bottom: 599, Flags: MonitorPrimary}}); err != nil {
		f.Fatal(err)
	}
	f.Add(bytes.Clone(w.Bytes()))
	w.Reset()
	Wrap(&w, make([]byte, MaxSegment+2))
	f.Add(bytes.Clone(w.Bytes()))
	f.Fuzz(func(t *testing.T, b []byte) {
		nop := func(PDU) error { return nil }
		_ = ReadClient(b, nop)
		_ = ReadServer(b, nop)
		if data, err := Unwrap(b, nil); err == nil && len(data) > len(b) {
			t.Errorf("%d bytes unwrapped from %d", len(data), len(b))
		}
	})
}
