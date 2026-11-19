package fastpath

import (
	"bytes"
	"math/rand/v2"
	"strconv"
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/tpkt"
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

// pattern returns n bytes that differ from one position to the next.
func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i>>8)
	}
	return b
}

// pdu is one PDU as the field tables of 2.2.9.1.2 and 2.2.9.1.2.1 give it.
func pdu(length []byte, updateHeader byte, data []byte) []byte {
	b := append([]byte{0x00}, length...)
	b = append(b, updateHeader, byte(len(data)), byte(len(data)>>8))
	return append(b, data...)
}

func TestAppendUpdate(t *testing.T) {
	big := pattern(2*MaxFragment + 1)
	tests := []struct {
		name string
		code uint8
		data []byte
		want []byte
	}{
		// System Pointer Hidden: no data; a 5-byte PDU with the short
		// length.
		{"no data", UpdatePointerHidden, nil, []byte{0x00, 0x05, 0x05, 0x00, 0x00}},
		// Pointer Position at (0x10, 0x20).
		{"pointer position", UpdatePointerPosition, []byte{0x10, 0x00, 0x20, 0x00},
			[]byte{0x00, 0x09, 0x08, 0x04, 0x00, 0x10, 0x00, 0x20, 0x00}},
		// The longest PDU with a one-byte length: 127.
		{"short length", UpdateBitmap, big[:122], pdu([]byte{0x7f}, 0x01, big[:122])},
		// One byte more takes the two-byte length: 3 + 3 + 123 = 129.
		{"long length", UpdateBitmap, big[:123], pdu([]byte{0x80, 0x81}, 0x01, big[:123])},
		// The largest unfragmented update makes a PDU of 16383 bytes.
		{"one fragment", UpdateSurfaceCommands, big[:MaxFragment], pdu([]byte{0xbf, 0xff}, 0x04, big[:MaxFragment])},
		{"first and last", UpdateSurfaceCommands, big[:MaxFragment+1], append(
			pdu([]byte{0xbf, 0xff}, 0x24, big[:MaxFragment]),
			pdu([]byte{0x06}, 0x14, big[MaxFragment:MaxFragment+1])...)},
		{"first, next and last", UpdateBitmap, big, append(append(
			pdu([]byte{0xbf, 0xff}, 0x21, big[:MaxFragment]),
			pdu([]byte{0xbf, 0xff}, 0x31, big[MaxFragment:2*MaxFragment])...),
			pdu([]byte{0x06}, 0x11, big[2*MaxFragment:])...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w wire.Writer
			if err := AppendUpdate(&w, tt.code, tt.data, MaxUpdate(1<<20)); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(w.Bytes(), tt.want) {
				t.Errorf("got % x\nwant % x", head(w.Bytes()), head(tt.want))
			}
		})
	}
}

// head shortens what a failure prints.
func head(b []byte) []byte { return b[:min(len(b), 32)] }

// frames splits b into the frames tpkt reads.
func frames(t *testing.T, b []byte) [][]byte {
	t.Helper()
	var fs [][]byte
	for len(b) > 0 {
		k, n, err := tpkt.FrameLength(b[:min(len(b), 3)])
		if err != nil {
			t.Fatal(err)
		}
		if k != tpkt.FastPath || n > len(b) {
			t.Fatalf("kind %v length %d of %d", k, n, len(b))
		}
		fs = append(fs, b[:n])
		b = b[n:]
	}
	return fs
}

func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 8))
	a := Reassembler{Max: 0x3f0000}
	for i := range 300 {
		n := rng.IntN(5 * MaxFragment)
		if i%3 == 0 {
			n = rng.IntN(200)
		}
		data := make([]byte, n)
		for j := range data {
			data[j] = byte(rng.Uint32())
		}
		code := uint8(rng.IntN(13))
		var w wire.Writer
		if err := AppendUpdate(&w, code, data, a.Max); err != nil {
			t.Fatal(err)
		}
		var got []Update
		for _, f := range frames(t, w.Bytes()) {
			if len(f) > MaxPDULength {
				t.Fatalf("PDU of %d bytes", len(f))
			}
			r, err := ParsePDU(f)
			if err != nil {
				t.Fatal(err)
			}
			for r.Remaining() > 0 {
				u, err := ReadUpdate(&r)
				if err != nil {
					t.Fatal(err)
				}
				whole, ok, err := a.Add(u)
				if err != nil {
					t.Fatal(err)
				}
				if ok {
					whole.Data = bytes.Clone(whole.Data)
					got = append(got, whole)
				}
			}
		}
		if len(got) != 1 || got[0].Code != code || !bytes.Equal(got[0].Data, data) {
			t.Fatalf("update %d of %d bytes, code %d: got %d updates", i, n, code, len(got))
		}
	}
}

func TestReadUpdateCompressed(t *testing.T) {
	// A compressed Bitmap update: compression 2 in the top bits, then
	// compressionFlags, size and data.
	r := wire.NewReader([]byte{0x81, 0x61, 0x02, 0x00, 0xaa, 0xbb})
	u, err := ReadUpdate(&r)
	if err != nil {
		t.Fatal(err)
	}
	if u.Code != UpdateBitmap || u.Fragmentation != FragmentSingle || !u.Compressed || u.CompressionFlags != 0x61 || !bytes.Equal(u.Data, []byte{0xaa, 0xbb}) {
		t.Errorf("got %+v", u)
	}
}

func TestReassemblerSequences(t *testing.T) {
	frag := func(f uint8, data ...byte) Update { return Update{Code: UpdateBitmap, Fragmentation: f, Data: data} }
	tests := []struct {
		name  string
		seq   []Update
		route string // the failure of the last one, "" for the whole update
		want  []byte
	}{
		{"single", []Update{frag(FragmentSingle, 1)}, "", []byte{1}},
		{"first, last", []Update{frag(FragmentFirst, 1), frag(FragmentLast, 2)}, "", []byte{1, 2}},
		{"first, next, next, last", []Update{frag(FragmentFirst, 1), frag(FragmentNext, 2), frag(FragmentNext, 3), frag(FragmentLast, 4)}, "", []byte{1, 2, 3, 4}},
		{"single inside", []Update{frag(FragmentFirst, 1), frag(FragmentSingle, 2)}, "Reassembler.Add." + TagFragmentSequence, nil},
		{"first inside", []Update{frag(FragmentFirst, 1), frag(FragmentFirst, 2)}, "Reassembler.Add." + TagFragmentSequence, nil},
		{"next first", []Update{frag(FragmentNext, 1)}, "Reassembler.Add." + TagFragmentSequence, nil},
		{"last first", []Update{frag(FragmentLast, 1)}, "Reassembler.Add." + TagFragmentSequence, nil},
		{"another code", []Update{frag(FragmentFirst, 1), {Code: UpdateOrders, Fragmentation: FragmentLast}}, "Reassembler.Add." + TagFragmentSequence, nil},
		{"another compression", []Update{frag(FragmentFirst, 1), {Code: UpdateBitmap, Fragmentation: FragmentLast, Compressed: true}}, "Reassembler.Add." + TagFragmentSequence, nil},
		{"too large", []Update{frag(FragmentFirst, 1, 2, 3), frag(FragmentLast, 4, 5)}, "Reassembler.Add." + TagReassemblyTooLarge, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := Reassembler{Max: 4}
			for i, u := range tt.seq {
				whole, ok, err := a.Add(u)
				last := i == len(tt.seq)-1
				if !last {
					if err != nil || ok {
						t.Fatalf("fragment %d: ok %v err %v", i, ok, err)
					}
					continue
				}
				if tt.route != "" {
					assertRoute(t, err, tt.route)
					return
				}
				if err != nil || !ok || !bytes.Equal(whole.Data, tt.want) || whole.Fragmentation != FragmentSingle {
					t.Fatalf("got %+v %v %v", whole, ok, err)
				}
			}
			// After a whole update a new sequence starts.
			if _, ok, err := a.Add(frag(FragmentSingle, 9)); err != nil || !ok {
				t.Errorf("after the sequence: %v %v", ok, err)
			}
		})
	}
}

func TestErrors(t *testing.T) {
	ok := []byte{0x00, 0x09, 0x08, 0x04, 0x00, 0x10, 0x00, 0x20, 0x00}
	long := pdu([]byte{0x80, 0x81}, 0x01, pattern(123))
	tests := []struct {
		name  string
		run   func() error
		route string
	}{
		{"code too wide", func() error { return AppendUpdate(&wire.Writer{}, 0x10, nil, 0) }, "AppendUpdate." + TagUpdateCodeInvalid},
		{"larger than the client takes", func() error {
			return AppendUpdate(&wire.Writer{}, UpdateBitmap, make([]byte, 100001), 100000)
		}, "AppendUpdate." + TagUpdateTooLarge},
		{"larger than a PDU without multifragment", func() error {
			return AppendUpdate(&wire.Writer{}, UpdateBitmap, make([]byte, MaxFragment+1), 0)
		}, "AppendUpdate." + TagUpdateTooLarge},
		{"slow-path action", func() error { _, err := ParsePDU([]byte{0x03, 0x00, 0x00, 0x07}); return err }, "ParsePDU." + TagHeaderInvalid},
		{"encrypted", func() error { _, err := ParsePDU([]byte{0x80, 0x02}); return err }, "ParsePDU." + TagHeaderInvalid},
		{"checksum flag", func() error { _, err := ParsePDU([]byte{0x40, 0x02}); return err }, "ParsePDU." + TagHeaderInvalid},
		{"length too short", func() error { _, err := ParsePDU(ok[:8]); return err }, "ParsePDU." + TagLengthInvalid},
		{"long length wrong", func() error { _, err := ParsePDU(long[:len(long)-1]); return err }, "ParsePDU." + TagLengthInvalid},
		{"empty", func() error { _, err := ParsePDU(nil); return err }, "ParsePDU->Reader.U8." + wire.TagTruncated},
		{"no length", func() error { _, err := ParsePDU([]byte{0x00}); return err }, "ParsePDU->Reader.U8." + wire.TagTruncated},
		{"no second length byte", func() error { _, err := ParsePDU([]byte{0x00, 0x80}); return err }, "ParsePDU->Reader.U8." + wire.TagTruncated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assertRoute(t, tt.run(), tt.route) })
	}
	// Every cut of an update fails as truncated.
	upd := []byte{0x81, 0x61, 0x02, 0x00, 0xaa, 0xbb}
	for n := range len(upd) {
		r := wire.NewReader(upd[:n])
		_, err := ReadUpdate(&r)
		appErr, isApp := errors.FromError(err)
		if !isApp || appErr.Tag != wire.TagTruncated {
			t.Errorf("cut at %d: %v", n, err)
		}
		if isApp && !appErr.HasRoute("ReadUpdate->Reader.") {
			t.Errorf("cut at %d: route %s", n, appErr.Route())
		}
	}
}

func TestMutations(t *testing.T) {
	var w wire.Writer
	if err := AppendUpdate(&w, UpdateBitmap, pattern(MaxFragment+500), 1<<20); err != nil {
		t.Fatal(err)
	}
	base := w.Bytes()
	rng := rand.New(rand.NewPCG(5, 6))
	for range 5000 {
		b := bytes.Clone(base)
		for range 1 + rng.IntN(4) {
			b[rng.IntN(len(b))] = byte(rng.Uint32())
		}
		b = b[:rng.IntN(len(b)+1)]
		a := Reassembler{Max: 1 << 16}
		for len(b) >= 2 {
			k, n, err := tpkt.FrameLength(b[:min(len(b), 3)])
			if err != nil || k != tpkt.FastPath || n > len(b) {
				break
			}
			r, err := ParsePDU(b[:n])
			b = b[n:]
			if err != nil {
				continue
			}
			for r.Remaining() > 0 {
				u, err := ReadUpdate(&r)
				if err != nil {
					break
				}
				if _, _, err := a.Add(u); err != nil {
					break
				}
			}
		}
	}
}

func TestAllocs(t *testing.T) {
	data := pattern(3 * MaxFragment)
	w := wire.NewWriter(make([]byte, 0, 4*MaxPDULength))
	var frame []byte
	if err := AppendUpdate(&w, UpdateBitmap, data[:1000], 0); err != nil {
		t.Fatal(err)
	}
	frame = bytes.Clone(w.Bytes())
	a := Reassembler{Max: 1 << 20}
	if _, _, err := a.Add(Update{Fragmentation: FragmentFirst, Data: data}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Add(Update{Fragmentation: FragmentLast}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		run  func() error
	}{
		{"append fragmented", func() error {
			w.Reset()
			return AppendUpdate(&w, UpdateBitmap, data, 1<<20)
		}},
		{"parse and reassemble", func() error {
			r, err := ParsePDU(frame)
			if err != nil {
				return err
			}
			u, err := ReadUpdate(&r)
			if err != nil {
				return err
			}
			if _, _, err := a.Add(Update{Fragmentation: FragmentFirst, Data: u.Data}); err != nil {
				return err
			}
			_, _, err = a.Add(Update{Fragmentation: FragmentLast, Data: u.Data})
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

func BenchmarkAppendUpdate(b *testing.B) {
	for _, n := range []int{16, 1000, 64 << 10, 1 << 20} {
		data := pattern(n)
		w := wire.NewWriter(make([]byte, 0, n+n/MaxFragment*6+64))
		b.Run(byteSize(n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(n))
			for b.Loop() {
				w.Reset()
				if err := AppendUpdate(&w, UpdateBitmap, data, 1<<21); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func byteSize(n int) string {
	switch {
	case n >= 1<<20:
		return strconv.Itoa(n>>20) + "MiB"
	case n >= 1<<10:
		return strconv.Itoa(n>>10) + "KiB"
	default:
		return strconv.Itoa(n) + "B"
	}
}
