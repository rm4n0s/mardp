package tpkt

import (
	"bytes"
	"io"
	"testing"

	"github.com/rm4n0s/errors"
)

func TestReadFrame(t *testing.T) {
	tpkt := []byte{0x03, 0x00, 0x00, 0x0b, 0x06, 0xe0, 0, 0, 0, 0, 0}
	fast2 := []byte{0x04, 0x05, 1, 2, 3}
	fast3 := append([]byte{0x00, 0x81, 0x03}, make([]byte, 0x100)...)
	stream := append(append(append(bytes.Clone(tpkt), fast2...), fast3...), tpkt...)
	r := bytes.NewReader(stream)
	buf := make([]byte, MaxLength)
	for i, want := range []struct {
		kind Kind
		b    []byte
	}{{TPKT, tpkt}, {FastPath, fast2}, {FastPath, fast3}, {TPKT, tpkt}} {
		k, f, err := ReadFrame(r, buf)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if k != want.kind || !bytes.Equal(f, want.b) {
			t.Errorf("frame %d: %v % x, want %v % x", i, k, f, want.kind, want.b)
		}
	}
	_, _, err := ReadFrame(r, buf)
	if !errors.Is(err, io.EOF) {
		t.Errorf("after the last frame: %v, want io.EOF", err)
	}
}

func TestReadFrameErrors(t *testing.T) {
	big := make([]byte, MaxLength)
	tests := []struct {
		name  string
		in    []byte
		buf   []byte
		route string
		cause error
	}{
		{"small buffer", []byte{3, 0, 0, 7}, make([]byte, 100), "ReadFrame." + TagBufferTooSmall, nil},
		{"nothing", nil, big, "ReadFrame." + TagReadFailed, io.EOF},
		{"one byte", []byte{3}, big, "ReadFrame." + TagReadFailed, io.ErrUnexpectedEOF},
		{"TPKT header cut", []byte{3, 0, 0}, big, "ReadFrame." + TagReadFailed, io.ErrUnexpectedEOF},
		{"body cut", []byte{3, 0, 0, 9, 2, 0xf0, 0x80}, big, "ReadFrame." + TagReadFailed, io.ErrUnexpectedEOF},
		{"unknown kind", []byte{0x01, 0x00}, big, "ReadFrame->FrameLength." + TagUnknownFrame, nil},
		{"short TPKT", []byte{3, 0, 0, 6}, big, "ReadFrame->FrameLength." + TagLengthInvalid, nil},
		{"empty fast-path", []byte{0x04, 0x02}, big, "ReadFrame->FrameLength." + TagLengthInvalid, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ReadFrame(bytes.NewReader(tt.in), tt.buf)
			assertRoute(t, err, tt.route)
			if tt.cause != nil && !errors.Is(err, tt.cause) {
				t.Errorf("cause %v, want %v", err, tt.cause)
			}
		})
	}
}

func TestReadFrameNoAllocations(t *testing.T) {
	frame := []byte{0x03, 0x00, 0x00, 0x0b, 0x06, 0xe0, 0, 0, 0, 0, 0}
	r := bytes.NewReader(frame)
	buf := make([]byte, MaxLength)
	if n := testing.AllocsPerRun(100, func() {
		r.Reset(frame)
		if _, _, err := ReadFrame(r, buf); err != nil {
			t.Fatal(err)
		}
	}); n != 0 {
		t.Errorf("%v allocations per run, want 0", n)
	}
}
