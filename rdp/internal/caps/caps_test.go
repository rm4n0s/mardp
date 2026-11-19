package caps

import (
	"bytes"
	"encoding/binary"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

func assertRoute(t *testing.T, err error, route string) {
	t.Helper()
	appErr, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("expected *errors.Error, got %T: %v", err, err)
	}
	if !appErr.HasRoute(route) {
		t.Errorf("unexpected failure path: %s, want %s", appErr.Route(), route)
	}
}

// example reads one of Microsoft's annotated PDUs (rdp/testdata/ms-rdpbcgr).
func example(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ms-rdpbcgr", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The server's and the client's MCS identities in Microsoft's examples.
const (
	exShareID = 0x000103ea
	exServer  = 1002
	exClient  = 1007
)

func readOne(t *testing.T, b []byte) PDU {
	t.Helper()
	r := wire.NewReader(b)
	p, err := ReadPDU(&r)
	if err != nil {
		t.Fatal(err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("%d bytes after the PDU", r.Remaining())
	}
	return p
}

// rawSets splits the capability sets of a Demand or Confirm Active PDU,
// headers included, by type.
func rawSets(t *testing.T, b []byte, confirm bool) map[uint16][]byte {
	t.Helper()
	o := 10
	if confirm {
		o += 2 // originatorID
	}
	ls := int(binary.LittleEndian.Uint16(b[o:]))
	lc := int(binary.LittleEndian.Uint16(b[o+2:]))
	o += 4 + ls
	end := o + lc
	n := int(binary.LittleEndian.Uint16(b[o:]))
	o += 4
	sets := map[uint16][]byte{}
	for range n {
		typ := binary.LittleEndian.Uint16(b[o:])
		l := int(binary.LittleEndian.Uint16(b[o+2:]))
		sets[typ] = b[o : o+l]
		o += l
	}
	if o != end {
		t.Fatalf("sets end at %d, lengthCombinedCapabilities at %d", o, end)
	}
	return sets
}

// compareMasked compares got with want, except at the offsets in mask.
func compareMasked(t *testing.T, name string, got, want []byte, mask ...int) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: %d bytes, want %d\n got %x\nwant %x", name, len(got), len(want), got, want)
		return
	}
	skip := map[int]bool{}
	for _, m := range mask {
		skip[m] = true
	}
	for i := range got {
		if got[i] != want[i] && !skip[i] {
			t.Errorf("%s: byte %d is %#02x, want %#02x\n got %x\nwant %x", name, i, got[i], want[i], got, want)
			return
		}
	}
}

func span(from, to int) []int {
	var s []int
	for i := from; i < to; i++ {
		s = append(s, i)
	}
	return s
}

// setCase is one capability set of an example and the offsets in it where
// Microsoft's bytes are uninitialized, which the writer writes as zeros.
type setCase struct {
	typ  uint16
	mask []int
}

func checkSets(t *testing.T, c *Caps, raw map[uint16][]byte, cases []setCase) {
	t.Helper()
	for _, sc := range cases {
		var w wire.Writer
		if err := writeSet(&w, sc.typ, c); err != nil {
			t.Fatal(err)
		}
		compareMasked(t, "set "+string(rune('0'+sc.typ/10))+string(rune('0'+sc.typ%10)), w.Bytes(), raw[sc.typ], sc.mask...)
	}
}

func setsOf(types ...uint16) uint64 {
	var s uint64
	for _, t := range types {
		s |= 1 << t
	}
	return s
}

func TestMicrosoftDemandActive(t *testing.T) {
	b := example(t, "demand-active.bin")
	p := readOne(t, b)
	if p.Type != TypeDemandActive || p.Source != exServer {
		t.Fatalf("type %d source %d", p.Type, p.Source)
	}
	d, err := ParseDemandActive(p)
	if err != nil {
		t.Fatal(err)
	}
	if d.ShareID != exShareID || string(d.SourceDescriptor) != "RDP\x00" || d.SessionID != 0 {
		t.Errorf("share %#x descriptor %q session %d", d.ShareID, d.SourceDescriptor, d.SessionID)
	}
	c := d.Caps
	if want := setsOf(SetShare, SetGeneral, SetVirtualChannel, SetDrawGDIPlus, SetFont, SetBitmap, SetOrder,
		SetColorCache, SetBitmapCacheHostSupport, SetPointer, SetInput, SetRAIL, SetWindow); c.Sets != want {
		t.Errorf("sets %#x, want %#x", c.Sets, want)
	}
	if want := (General{OSMajor: 1, OSMinor: 3, ExtraFlags: 0x041d, RefreshRect: true, SuppressOutput: true}); c.General != want {
		t.Errorf("general %+v, want %+v", c.General, want)
	}
	if want := (Bitmap{BitsPerPixel: 24, DesktopWidth: 1280, DesktopHeight: 1024, DesktopResize: true}); c.Bitmap != want {
		t.Errorf("bitmap %+v, want %+v", c.Bitmap, want)
	}
	if want := (Pointer{Color: true, ColorCacheSize: 25, CacheSize: 25, HasCacheSize: true}); c.Pointer != want {
		t.Errorf("pointer %+v, want %+v", c.Pointer, want)
	}
	if c.Input.Flags != 0x35 || c.VirtualChannel != (VirtualChannel{Flags: VCCompressionCS8K}) || c.ShareNodeID != exServer || c.FontFlags != 0 {
		t.Errorf("input %#x vc %+v node %d font %d", c.Input.Flags, c.VirtualChannel, c.ShareNodeID, c.FontFlags)
	}
	// The Share set's padding and the Input set's keyboard fields and IME
	// file name are uninitialized in the example; the server writes zeros
	// in the keyboard fields (2.2.7.1.6), here the parsed values are written
	// back. The Font set has 4 bytes in the example, against 8 in its
	// structure (2.2.7.2.5), so it is not compared.
	checkSets(t, &c, rawSets(t, b, false), []setCase{
		{SetGeneral, nil},
		{SetBitmap, nil},
		{SetPointer, nil},
		{SetVirtualChannel, nil},
		{SetShare, []int{6, 7}},
		{SetInput, span(24, 88)},
	})
}

func TestMicrosoftConfirmActive(t *testing.T) {
	b := example(t, "confirm-active.bin")
	p := readOne(t, b)
	if p.Type != TypeConfirmActive || p.Source != exClient {
		t.Fatalf("type %d source %d", p.Type, p.Source)
	}
	ca, err := ParseConfirmActive(p)
	if err != nil {
		t.Fatal(err)
	}
	if ca.ShareID != exShareID || ca.Originator != exServer || string(ca.SourceDescriptor) != "MSTSC\x00" {
		t.Errorf("share %#x originator %d descriptor %q", ca.ShareID, ca.Originator, ca.SourceDescriptor)
	}
	c := ca.Caps
	if want := setsOf(SetGeneral, SetBitmap, SetOrder, SetBitmapCacheRev2, SetColorCache, SetActivation, SetControl,
		SetPointer, SetShare, SetInput, SetSound, SetFont, SetGlyphCache, SetBrush, SetOffscreenCache,
		SetVirtualChannel, SetDrawNineGridCache, SetDrawGDIPlus); c.Sets != want {
		t.Errorf("sets %#x, want %#x", c.Sets, want)
	}
	if want := (General{OSMajor: 1, OSMinor: 3, ExtraFlags: 0x041d}); c.General != want {
		t.Errorf("general %+v, want %+v", c.General, want)
	}
	if want := (Bitmap{BitsPerPixel: 24, DesktopWidth: 1280, DesktopHeight: 1024, DesktopResize: true}); c.Bitmap != want {
		t.Errorf("bitmap %+v, want %+v", c.Bitmap, want)
	}
	if want := (Pointer{Color: true, ColorCacheSize: 20, CacheSize: 21, HasCacheSize: true}); c.Pointer != want {
		t.Errorf("pointer %+v, want %+v", c.Pointer, want)
	}
	if want := (Input{Flags: 0x15, KeyboardLayout: 0x409, KeyboardType: 4, KeyboardFunctionKeys: 12}); c.Input != want {
		t.Errorf("input %+v, want %+v", c.Input, want)
	}
	if c.SoundFlags != SoundBeeps || c.FontFlags != FontSupportList || c.VirtualChannel != (VirtualChannel{Flags: VCCompressionSC}) || c.ShareNodeID != 0 {
		t.Errorf("sound %d font %d vc %+v node %d", c.SoundFlags, c.FontFlags, c.VirtualChannel, c.ShareNodeID)
	}
	// The Input set's pad2octetsA is 0x0020 in the example.
	checkSets(t, &c, rawSets(t, b, true), []setCase{
		{SetGeneral, nil},
		{SetBitmap, nil},
		{SetPointer, nil},
		{SetShare, nil},
		{SetInput, []int{6, 7}},
		{SetSound, nil},
		{SetFont, nil},
		{SetVirtualChannel, nil},
	})
}

// Offsets of the Share Data Header fields that Microsoft's server examples
// fill with uninitialized bytes or values the documents do not give:
// pad1, streamID and uncompressedLength.
var serverHeaderMask = []int{10, 11, 12, 13}

func TestMicrosoftFinalization(t *testing.T) {
	tests := []struct {
		file  string
		write func(w *wire.Writer) error
		mask  []int
		type2 uint8
		check func(t *testing.T, body []byte)
	}{
		{
			file:  "client-synchronize.bin",
			write: func(w *wire.Writer) error { return WriteSynchronize(w, exShareID, exClient, exServer) },
			type2: Type2Synchronize,
			check: func(t *testing.T, body []byte) {
				s, err := ParseSynchronize(body)
				if err != nil || s != (Synchronize{MessageType: 1, TargetUser: exServer}) {
					t.Errorf("%+v %v", s, err)
				}
			},
		},
		{
			file: "client-control-cooperate.bin",
			write: func(w *wire.Writer) error {
				return WriteControl(w, exShareID, exClient, Control{Action: ActionCooperate})
			},
			type2: Type2Control,
			check: func(t *testing.T, body []byte) {
				c, err := ParseControl(body)
				if err != nil || c != (Control{Action: ActionCooperate}) {
					t.Errorf("%+v %v", c, err)
				}
			},
		},
		{
			file: "client-control-request.bin",
			write: func(w *wire.Writer) error {
				return WriteControl(w, exShareID, exClient, Control{Action: ActionRequestControl})
			},
			type2: Type2Control,
			check: func(t *testing.T, body []byte) {
				c, err := ParseControl(body)
				if err != nil || c != (Control{Action: ActionRequestControl}) {
					t.Errorf("%+v %v", c, err)
				}
			},
		},
		{
			// uncompressedLength is uninitialized in the example.
			file:  "client-font-list.bin",
			write: func(w *wire.Writer) error { return WriteFontList(w, exShareID, exClient) },
			mask:  []int{12, 13},
			type2: Type2FontList,
			check: func(t *testing.T, body []byte) {
				f, err := ParseFonts(body)
				if err != nil || f != (Fonts{Flags: 3, EntrySize: 50}) {
					t.Errorf("%+v %v", f, err)
				}
			},
		},
		{
			// targetUser is uninitialized in the example (0x4463); the
			// server writes the client's user channel, as 2.2.1.14.1 says.
			file:  "server-synchronize.bin",
			write: func(w *wire.Writer) error { return WriteSynchronize(w, exShareID, exServer, exClient) },
			mask:  append(serverHeaderMask, 20, 21),
			type2: Type2Synchronize,
			check: func(t *testing.T, body []byte) {
				s, err := ParseSynchronize(body)
				if err != nil || s.MessageType != 1 {
					t.Errorf("%+v %v", s, err)
				}
			},
		},
		{
			file: "server-control-cooperate.bin",
			write: func(w *wire.Writer) error {
				return WriteControl(w, exShareID, exServer, Control{Action: ActionCooperate})
			},
			mask:  serverHeaderMask,
			type2: Type2Control,
			check: func(t *testing.T, body []byte) {
				c, err := ParseControl(body)
				if err != nil || c != (Control{Action: ActionCooperate}) {
					t.Errorf("%+v %v", c, err)
				}
			},
		},
		{
			file: "server-control-granted.bin",
			write: func(w *wire.Writer) error {
				return WriteControl(w, exShareID, exServer, Control{Action: ActionGrantedControl, GrantID: exClient, ControlID: exServer})
			},
			mask:  serverHeaderMask,
			type2: Type2Control,
			check: func(t *testing.T, body []byte) {
				c, err := ParseControl(body)
				if err != nil || c != (Control{Action: ActionGrantedControl, GrantID: exClient, ControlID: exServer}) {
					t.Errorf("%+v %v", c, err)
				}
			},
		},
		{
			file:  "server-font-map.bin",
			write: func(w *wire.Writer) error { return WriteFontMap(w, exShareID, exServer) },
			mask:  serverHeaderMask,
			type2: Type2FontMap,
			check: func(t *testing.T, body []byte) {
				f, err := ParseFonts(body)
				if err != nil || f != (Fonts{Flags: 3, EntrySize: 4}) {
					t.Errorf("%+v %v", f, err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			b := example(t, tt.file)
			var w wire.Writer
			if err := tt.write(&w); err != nil {
				t.Fatal(err)
			}
			compareMasked(t, tt.file, w.Bytes(), b, tt.mask...)
			for _, in := range [][]byte{b, w.Bytes()} {
				p := readOne(t, in)
				if p.Type != TypeData {
					t.Fatalf("type %d", p.Type)
				}
				d, err := ParseData(p.Body)
				if err != nil {
					t.Fatal(err)
				}
				if d.ShareID != exShareID || d.Type2 != tt.type2 {
					t.Fatalf("share %#x type2 %#x", d.ShareID, d.Type2)
				}
				tt.check(t, d.Body)
			}
			// What the writer puts in the fields the examples get wrong.
			if got := w.Bytes(); got[10] != 0 || got[11] != StreamLow || int(binary.LittleEndian.Uint16(got[12:])) != len(got)-14 {
				t.Errorf("pad1 %d streamID %d uncompressedLength %d", got[10], got[11], binary.LittleEndian.Uint16(got[12:]))
			}
		})
	}
}

// randomCaps returns Caps with a random choice of the sets the package
// writes, and random values in them.
func randomCaps(rng *rand.Rand) Caps {
	var c Caps
	for _, t := range writeOrder {
		if rng.IntN(3) > 0 {
			c.Add(t)
		}
	}
	u16 := func() uint16 { return uint16(rng.Uint32()) }
	if c.Has(SetGeneral) {
		c.General = General{OSMajor: u16(), OSMinor: u16(), ExtraFlags: u16(), RefreshRect: rng.IntN(2) == 0, SuppressOutput: rng.IntN(2) == 0}
	}
	if c.Has(SetBitmap) {
		c.Bitmap = Bitmap{BitsPerPixel: u16(), DesktopWidth: u16(), DesktopHeight: u16(), DesktopResize: rng.IntN(2) == 0, DrawingFlags: uint8(rng.Uint32())}
	}
	if c.Has(SetPointer) {
		c.Pointer = Pointer{Color: rng.IntN(2) == 0, ColorCacheSize: u16()}
		if rng.IntN(2) == 0 {
			c.Pointer.CacheSize, c.Pointer.HasCacheSize = u16(), true
		}
	}
	if c.Has(SetInput) {
		c.Input = Input{Flags: u16(), KeyboardLayout: rng.Uint32(), KeyboardType: rng.Uint32(), KeyboardSubType: rng.Uint32(), KeyboardFunctionKeys: rng.Uint32()}
	}
	if c.Has(SetVirtualChannel) {
		c.VirtualChannel = VirtualChannel{Flags: rng.Uint32()}
		if rng.IntN(2) == 0 {
			c.VirtualChannel.ChunkSize = 1 + rng.Uint32N(16256)
		}
	}
	if c.Has(SetSound) {
		c.SoundFlags = u16()
	}
	if c.Has(SetShare) {
		c.ShareNodeID = u16()
	}
	if c.Has(SetFont) {
		c.FontFlags = u16()
	}
	if c.Has(SetMultifragmentUpdate) {
		c.MaxRequestSize = rng.Uint32()
	}
	if c.Has(SetLargePointer) {
		c.LargePointer = u16()
	}
	if c.Has(SetSurfaceCommands) {
		c.SurfaceCommands = rng.Uint32()
	}
	if c.Has(SetFrameAcknowledge) {
		c.MaxUnacknowledgedFrames = rng.Uint32()
	}
	return c
}

func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 7))
	for i := range 2000 {
		c := randomCaps(rng)
		d := DemandActive{ShareID: rng.Uint32(), Source: uint16(rng.Uint32()), SourceDescriptor: []byte("RDP\x00"), Caps: c, SessionID: rng.Uint32()}
		ca := ConfirmActive{ShareID: rng.Uint32(), Source: uint16(rng.Uint32()), Originator: exServer, SourceDescriptor: []byte("MSTSC\x00")[:rng.IntN(7)], Caps: c}
		var w wire.Writer
		if err := WriteDemandActive(&w, &d); err != nil {
			t.Fatal(err)
		}
		if err := WriteConfirmActive(&w, &ca); err != nil {
			t.Fatal(err)
		}
		// Both PDUs in one buffer, as a Send Data PDU may carry them.
		r := wire.NewReader(w.Bytes())
		p, err := ReadPDU(&r)
		if err != nil {
			t.Fatal(err)
		}
		gd, err := ParseDemandActive(p)
		if err != nil {
			t.Fatal(err)
		}
		if p.Source != d.Source || gd.ShareID != d.ShareID || gd.SessionID != d.SessionID || !bytes.Equal(gd.SourceDescriptor, d.SourceDescriptor) || gd.Caps != c {
			t.Fatalf("%d: demand active\n got %+v\nwant %+v", i, gd, d)
		}
		if p, err = ReadPDU(&r); err != nil {
			t.Fatal(err)
		}
		gc, err := ParseConfirmActive(p)
		if err != nil {
			t.Fatal(err)
		}
		if p.Source != ca.Source || gc.ShareID != ca.ShareID || gc.Originator != ca.Originator || !bytes.Equal(gc.SourceDescriptor, ca.SourceDescriptor) || gc.Caps != c {
			t.Fatalf("%d: confirm active\n got %+v\nwant %+v", i, gc, ca)
		}
		if r.Remaining() != 0 {
			t.Fatalf("%d bytes left", r.Remaining())
		}
	}
}

func TestFlowPDU(t *testing.T) {
	var w wire.Writer
	w.Write([]byte{0x00, 0x80, 0, 0x42, 1, 2, 0xea, 0x03}) // FlowPDU: flowMarker, pad, pduTypeFlow, flowIdentifier, flowNumber, pduSource
	if err := WriteFontMap(&w, exShareID, exServer); err != nil {
		t.Fatal(err)
	}
	r := wire.NewReader(w.Bytes())
	p, err := ReadPDU(&r)
	if err != nil || !p.Flow {
		t.Fatalf("%+v %v", p, err)
	}
	if p, err = ReadPDU(&r); err != nil || p.Type != TypeData || r.Remaining() != 0 {
		t.Fatalf("%+v %v, %d left", p, err, r.Remaining())
	}
	// A FlowPDU cut short is read to the end.
	r = wire.NewReader([]byte{0x00, 0x80, 0})
	if p, err = ReadPDU(&r); err != nil || !p.Flow || r.Remaining() != 0 {
		t.Fatalf("%+v %v, %d left", p, err, r.Remaining())
	}
}

// parseAll parses b as a share PDU of any type the package reads.
func parseAll(b []byte) error {
	r := wire.NewReader(b)
	for r.Remaining() > 0 {
		p, err := ReadPDU(&r)
		if err != nil {
			return err
		}
		switch p.Type {
		case TypeDemandActive:
			_, err = ParseDemandActive(p)
		case TypeConfirmActive:
			_, err = ParseConfirmActive(p)
		case TypeData:
			var d Data
			if d, err = ParseData(p.Body); err != nil {
				return err
			}
			switch d.Type2 {
			case Type2Synchronize:
				_, err = ParseSynchronize(d.Body)
			case Type2Control:
				_, err = ParseControl(d.Body)
			default:
				_, err = ParseFonts(d.Body)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func TestTruncatedAndHostile(t *testing.T) {
	files := []string{
		"demand-active.bin", "confirm-active.bin", "client-synchronize.bin", "client-control-cooperate.bin",
		"client-control-request.bin", "client-font-list.bin", "server-synchronize.bin",
		"server-control-cooperate.bin", "server-control-granted.bin", "server-font-map.bin",
	}
	rng := rand.New(rand.NewPCG(1, 8))
	for _, f := range files {
		b := example(t, f)
		if err := parseAll(b); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for n := range len(b) {
			if err := parseAll(b[:n]); err != nil {
				if _, ok := errors.FromError(err); !ok {
					t.Fatalf("%s length %d: %T %v", f, n, err, err)
				}
			}
		}
		for range 5000 {
			m := bytes.Clone(b)
			for range 1 + rng.IntN(8) {
				m[rng.IntN(len(m))] = byte(rng.Uint32())
			}
			m = m[:rng.IntN(len(m)+1)]
			if err := parseAll(m); err != nil {
				if _, ok := errors.FromError(err); !ok {
					t.Fatalf("%s: %T %v", f, err, err)
				}
			}
		}
	}
}

func TestAllocations(t *testing.T) {
	w := wire.NewWriter(make([]byte, 0, 1024))
	c := randomCaps(rand.New(rand.NewPCG(2, 3)))
	for _, typ := range writeOrder {
		c.Add(typ)
	}
	d := DemandActive{ShareID: exShareID, Source: exServer, SourceDescriptor: []byte("RDP\x00"), Caps: c}
	ca := ConfirmActive{ShareID: exShareID, Source: exClient, Originator: exServer, SourceDescriptor: []byte("MSTSC\x00"), Caps: c}
	var cw wire.Writer
	if err := WriteConfirmActive(&cw, &ca); err != nil {
		t.Fatal(err)
	}
	confirm := cw.Bytes()
	tests := []struct {
		name string
		run  func() error
	}{
		{"demand active", func() error { w.Reset(); return WriteDemandActive(&w, &d) }},
		{"confirm active", func() error { w.Reset(); return WriteConfirmActive(&w, &ca) }},
		{"synchronize", func() error { w.Reset(); return WriteSynchronize(&w, exShareID, exServer, exClient) }},
		{"control", func() error {
			w.Reset()
			return WriteControl(&w, exShareID, exServer, Control{Action: ActionCooperate})
		}},
		{"font map", func() error { w.Reset(); return WriteFontMap(&w, exShareID, exServer) }},
		{"parse confirm active", func() error {
			r := wire.NewReader(confirm)
			p, err := ReadPDU(&r)
			if err != nil {
				return err
			}
			_, err = ParseConfirmActive(p)
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if n := testing.AllocsPerRun(100, func() { err = tt.run() }); n != 0 {
				t.Errorf("%v allocations per run, want 0", n)
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestErrors(t *testing.T) {
	ca := example(t, "confirm-active.bin")
	// with returns a copy of b with the 16-bit value at off set to v.
	with := func(b []byte, off int, v uint16) []byte {
		m := bytes.Clone(b)
		binary.LittleEndian.PutUint16(m[off:], v)
		return m
	}
	read := func(b []byte) error {
		r := wire.NewReader(b)
		_, err := ReadPDU(&r)
		return err
	}
	confirm := func(b []byte) error {
		r := wire.NewReader(b)
		p, err := ReadPDU(&r)
		if err != nil {
			return err
		}
		_, err = ParseConfirmActive(p)
		return err
	}
	demand := func(b []byte) error {
		r := wire.NewReader(b)
		p, err := ReadPDU(&r)
		if err != nil {
			return err
		}
		_, err = ParseDemandActive(p)
		return err
	}
	data := func(b []byte) error { _, err := ParseData(b); return err }
	// The first set of the example (General) starts at 26: header 6,
	// shareID 4, originatorID 2, two lengths 4, "MSTSC\x00" 6, count 2 and
	// pad 2.
	const firstSet = 26
	var long wire.Writer
	off := BeginControl(&long, TypeData, exServer)
	long.Zeros(0x10000)
	var longData wire.Writer
	doff := BeginData(&longData, exShareID, exServer, Type2Update)
	longData.Zeros(0xffff)
	var flowLong wire.Writer
	foff := BeginControl(&flowLong, TypeData, exServer)
	flowLong.Zeros(flowMarker - ShareControlLen)
	tests := []struct {
		name  string
		err   error
		route string
	}{
		{"total too small", read([]byte{4, 0, 0x17, 0, 0xea, 3}), "ReadPDU.CapsLengthInvalid"},
		{"total too large", read(with(ca, 0, uint16(len(ca)+1))), "ReadPDU.CapsLengthInvalid"},
		{"no header", read([]byte{6}), "ReadPDU->Reader.U16LE.WireTruncated"},
		{"version 0", read(with(ca, 2, TypeConfirmActive)), "ReadPDU.CapsVersionInvalid"},
		{"version 2", read(with(ca, 2, 0x20|TypeConfirmActive)), "ReadPDU.CapsVersionInvalid"},
		{"compressed", data([]byte{0xea, 3, 1, 0, 0, 1, 8, 0, Type2Synchronize, 0x21, 0, 0}), "ParseData.CapsCompressed"},
		{"data truncated", data([]byte{0xea, 3, 1, 0, 0, 1, 8, 0, Type2Synchronize, 0}), "ParseData->Reader.Skip.WireTruncated"},
		{"control too long", EndControl(&long, off), "EndControl.CapsTooLong"},
		{"data too long", EndData(&longData, doff), "EndData->EndControl.CapsTooLong"},
		{"flow marker length", EndControl(&flowLong, foff), "EndControl.CapsTooLong"},
		{"count too large", confirm(with(ca, firstSet-4, 19)), "ParseConfirmActive->readActive->readSets.CapsSetInvalid"},
		{"set length 2", confirm(with(ca, firstSet+2, 2)), "ParseConfirmActive->readActive->readSets.CapsSetInvalid"},
		{"set past the end", confirm(with(ca, firstSet+2, 0x1000)), "ParseConfirmActive->readActive->readSets.CapsSetInvalid"},
		{"general short", confirm(with(with(ca, firstSet+2, 8), firstSet-4, 1)), "readActive->readSets->readSet.CapsSetShort"},
		{"combined past the end", confirm(with(ca, 14, 0x1000)), "ParseConfirmActive->readActive->Reader.Sub.WireTruncated"},
		{"descriptor past the end", confirm(with(ca, 12, 0x1000)), "ParseConfirmActive->readActive->Reader.Bytes.WireTruncated"},
		{"confirm no originator", confirm([]byte{10, 0, 0x13, 0, 0xef, 3, 0xea, 3, 1, 0}), "ParseConfirmActive->Reader.U16LE.WireTruncated"},
		{"demand no share", demand([]byte{6, 0, 0x11, 0, 0xea, 3}), "ParseDemandActive->Reader.U32LE.WireTruncated"},
		{"synchronize truncated", func() error { _, err := ParseSynchronize([]byte{1, 0, 0xea}); return err }(), "ParseSynchronize->Reader.U16LE.WireTruncated"},
		{"control truncated", func() error { _, err := ParseControl([]byte{4, 0, 0, 0, 0}); return err }(), "ParseControl->Reader.U32LE.WireTruncated"},
		{"fonts truncated", func() error { _, err := ParseFonts([]byte{0, 0, 0, 0, 3, 0, 0x32}); return err }(), "ParseFonts->Reader.U16LE.WireTruncated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				t.Fatal("no error")
			}
			assertRoute(t, tt.err, tt.route)
		})
	}
}
