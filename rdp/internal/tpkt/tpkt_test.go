package tpkt

import (
	"bytes"
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

func TestClassify(t *testing.T) {
	tests := []struct {
		b    byte
		want Kind
	}{
		{0x03, TPKT},
		{0x00, FastPath},
		{0x04, FastPath}, // one input event
		{0xc4, FastPath}, // encrypted, secure checksum
		{0x01, Unknown},
		{0x02, Unknown},
		{0x07, Unknown},
	}
	for _, tt := range tests {
		if got := Classify(tt.b); got != tt.want {
			t.Errorf("Classify(%#x) = %v, want %v", tt.b, got, tt.want)
		}
	}
}

func TestFrameLength(t *testing.T) {
	tests := []struct {
		name string
		hdr  []byte
		kind Kind
		size int
		n    int
	}{
		// MS-RDPBCGR 4.1.1, the client's Connection Request.
		{"Microsoft CR", []byte{0x03, 0x00, 0x00, 0x2c}, TPKT, 4, 44},
		// MS-RDPBCGR 4.1.2, the server's Connection Confirm.
		{"Microsoft CC", []byte{0x03, 0x00, 0x00, 0x13}, TPKT, 4, 19},
		{"largest TPKT", []byte{0x03, 0x00, 0xff, 0xff}, TPKT, 4, 65535},
		{"fast-path, one length byte", []byte{0x44, 0x10}, FastPath, 2, 16},
		{"fast-path, two length bytes", []byte{0x00, 0x81, 0x23}, FastPath, 3, 0x123},
		{"fast-path, two bytes, small", []byte{0x00, 0x80, 0x05}, FastPath, 3, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HeaderSize(tt.hdr[0], tt.hdr[1]); got != tt.size {
				t.Errorf("HeaderSize = %d, want %d", got, tt.size)
			}
			k, n, err := FrameLength(tt.hdr)
			if err != nil {
				t.Fatal(err)
			}
			if k != tt.kind || n != tt.n {
				t.Errorf("FrameLength = %v %d, want %v %d", k, n, tt.kind, tt.n)
			}
		})
	}
	if got := HeaderSize(0x01, 0); got != 0 {
		t.Errorf("HeaderSize of an unknown frame = %d", got)
	}
}

func TestFrameLengthErrors(t *testing.T) {
	tests := []struct {
		name  string
		hdr   []byte
		route string
	}{
		{"one byte", []byte{0x03}, "FrameLength." + TagHeaderTruncated},
		{"unknown", []byte{0x05, 0x00}, "FrameLength." + TagUnknownFrame},
		{"TPKT truncated", []byte{0x03, 0x00, 0x00}, "FrameLength." + TagHeaderTruncated},
		{"fast-path truncated", []byte{0x00, 0x80}, "FrameLength." + TagHeaderTruncated},
		{"TPKT too short", []byte{0x03, 0x00, 0x00, 0x06}, "FrameLength." + TagLengthInvalid},
		{"fast-path no body", []byte{0x00, 0x02}, "FrameLength." + TagLengthInvalid},
		{"fast-path long form no body", []byte{0x00, 0x80, 0x03}, "FrameLength." + TagLengthInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := FrameLength(tt.hdr)
			assertRoute(t, err, tt.route)
		})
	}
}

func TestBeginEnd(t *testing.T) {
	// The TPKT of MS-RDPBCGR 4.1.2 around its 15-byte Connection Confirm.
	cc := []byte{0x0e, 0xd0, 0x00, 0x00, 0x12, 0x34, 0x00, 0x02, 0x00, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00}
	w := wire.NewWriter(make([]byte, 0, 64))
	w.U8(0xaa) // something before, to check End uses the offset
	off := Begin(&w)
	w.Write(cc)
	if err := End(&w, off); err != nil {
		t.Fatal(err)
	}
	want := append([]byte{0xaa, 0x03, 0x00, 0x00, 0x13}, cc...)
	if !bytes.Equal(w.Bytes(), want) {
		t.Errorf("got % x\nwant % x", w.Bytes(), want)
	}
	if n := testing.AllocsPerRun(100, func() {
		w.Reset()
		off := Begin(&w)
		w.Write(cc)
		if err := End(&w, off); err != nil {
			t.Fatal(err)
		}
	}); n != 0 {
		t.Errorf("%v allocations, want 0", n)
	}
}

func TestEndErrors(t *testing.T) {
	t.Run("too long", func(t *testing.T) {
		var w wire.Writer
		off := Begin(&w)
		w.Zeros(MaxLength)
		assertRoute(t, End(&w, off), "End."+TagTooLong)
	})
	t.Run("offset outside", func(t *testing.T) {
		var w wire.Writer
		assertRoute(t, End(&w, 5), "End->Writer.PatchU16BE."+wire.TagPatchOutOfRange)
	})
}

func TestKindString(t *testing.T) {
	for k, want := range map[Kind]string{TPKT: "TPKT", FastPath: "fast-path", Unknown: "unknown"} {
		if k.String() != want {
			t.Errorf("%d.String() = %q, want %q", k, k.String(), want)
		}
	}
}
