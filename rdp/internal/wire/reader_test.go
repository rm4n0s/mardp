package wire

import (
	"bytes"
	"testing"

	"github.com/rm4n0s/errors"
)

// assertRoute fails the test unless err is an *errors.Error whose route
// contains route.
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

func TestReaderIntegers(t *testing.T) {
	in := []byte{
		0xab,
		0x34, 0x12,
		0x12, 0x34,
		0x78, 0x56, 0x34, 0x12,
		0x12, 0x34, 0x56, 0x78,
		0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01,
	}
	r := NewReader(in)
	tests := []struct {
		name string
		read func() (uint64, error)
		want uint64
		off  int
	}{
		{"U8", func() (uint64, error) { v, err := r.U8(); return uint64(v), err }, 0xab, 1},
		{"U16LE", func() (uint64, error) { v, err := r.U16LE(); return uint64(v), err }, 0x1234, 3},
		{"U16BE", func() (uint64, error) { v, err := r.U16BE(); return uint64(v), err }, 0x1234, 5},
		{"U32LE", func() (uint64, error) { v, err := r.U32LE(); return uint64(v), err }, 0x12345678, 9},
		{"U32BE", func() (uint64, error) { v, err := r.U32BE(); return uint64(v), err }, 0x12345678, 13},
		{"U64LE", func() (uint64, error) { v, err := r.U64LE(); return v, err }, 0x0102030405060708, 21},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.read()
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %#x, want %#x", got, tt.want)
			}
			if r.Offset() != tt.off {
				t.Errorf("offset %d, want %d", r.Offset(), tt.off)
			}
		})
	}
	if r.Remaining() != 0 || len(r.Rest()) != 0 {
		t.Errorf("remaining %d, want 0", r.Remaining())
	}
}

func TestReaderBytesSkipSub(t *testing.T) {
	r := NewReader([]byte{1, 2, 3, 4, 5, 6})
	b, err := r.Bytes(2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, []byte{1, 2}) {
		t.Errorf("Bytes = %v", b)
	}
	if cap(b) != 2 {
		t.Errorf("Bytes has capacity %d: an append could overwrite the input", cap(b))
	}
	if err := r.Skip(1); err != nil {
		t.Fatal(err)
	}
	sub, err := r.Sub(2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sub.Rest(), []byte{4, 5}) {
		t.Errorf("Sub = %v", sub.Rest())
	}
	if _, err := sub.U16BE(); err != nil {
		t.Fatal(err)
	}
	if _, err := sub.U8(); err == nil {
		t.Error("read past the end of a Sub")
	}
	if !bytes.Equal(r.Rest(), []byte{6}) {
		t.Errorf("after Sub, Rest = %v", r.Rest())
	}
	if _, err := r.Bytes(0); err != nil {
		t.Errorf("Bytes(0): %v", err)
	}
}

func TestReaderErrors(t *testing.T) {
	tests := []struct {
		name  string
		in    []byte
		read  func(r *Reader) error
		route string
	}{
		{"U8 empty", nil, func(r *Reader) error { _, err := r.U8(); return err }, "Reader.U8." + TagTruncated},
		{"U16LE short", []byte{1}, func(r *Reader) error { _, err := r.U16LE(); return err }, "Reader.U16LE." + TagTruncated},
		{"U16BE short", []byte{1}, func(r *Reader) error { _, err := r.U16BE(); return err }, "Reader.U16BE." + TagTruncated},
		{"U32LE short", []byte{1, 2, 3}, func(r *Reader) error { _, err := r.U32LE(); return err }, "Reader.U32LE." + TagTruncated},
		{"U32BE short", []byte{1, 2, 3}, func(r *Reader) error { _, err := r.U32BE(); return err }, "Reader.U32BE." + TagTruncated},
		{"U64LE short", []byte{1, 2, 3, 4, 5, 6, 7}, func(r *Reader) error { _, err := r.U64LE(); return err }, "Reader.U64LE." + TagTruncated},
		{"Bytes short", []byte{1}, func(r *Reader) error { _, err := r.Bytes(2); return err }, "Reader.Bytes." + TagTruncated},
		{"Bytes negative", []byte{1}, func(r *Reader) error { _, err := r.Bytes(-1); return err }, "Reader.Bytes." + TagLengthNegative},
		{"Skip short", []byte{1}, func(r *Reader) error { return r.Skip(2) }, "Reader.Skip." + TagTruncated},
		{"Skip negative", []byte{1}, func(r *Reader) error { return r.Skip(-1) }, "Reader.Skip." + TagLengthNegative},
		{"Sub short", []byte{1}, func(r *Reader) error { _, err := r.Sub(2); return err }, "Reader.Sub." + TagTruncated},
		{"Sub negative", []byte{1}, func(r *Reader) error { _, err := r.Sub(-1); return err }, "Reader.Sub." + TagLengthNegative},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReader(tt.in)
			err := tt.read(&r)
			assertRoute(t, err, tt.route)
			if r.Offset() != 0 {
				t.Errorf("a failed read moved the offset to %d", r.Offset())
			}
		})
	}
}

// TestTruncatedEveryRead cuts a valid encoding of every read at every length
// and checks the read fails, with a route through that read, and never
// panics.
func TestTruncatedEveryRead(t *testing.T) {
	tests := []struct {
		name  string
		in    []byte
		read  func(r *Reader) error
		route string
	}{
		{"U8", []byte{1}, func(r *Reader) error { _, err := r.U8(); return err }, "Reader.U8."},
		{"U16LE", []byte{1, 2}, func(r *Reader) error { _, err := r.U16LE(); return err }, "Reader.U16LE."},
		{"U16BE", []byte{1, 2}, func(r *Reader) error { _, err := r.U16BE(); return err }, "Reader.U16BE."},
		{"U32LE", []byte{1, 2, 3, 4}, func(r *Reader) error { _, err := r.U32LE(); return err }, "Reader.U32LE."},
		{"U32BE", []byte{1, 2, 3, 4}, func(r *Reader) error { _, err := r.U32BE(); return err }, "Reader.U32BE."},
		{"U64LE", []byte{1, 2, 3, 4, 5, 6, 7, 8}, func(r *Reader) error { _, err := r.U64LE(); return err }, "Reader.U64LE."},
		{"Bytes", []byte{1, 2, 3}, func(r *Reader) error { _, err := r.Bytes(3); return err }, "Reader.Bytes."},
		{"Skip", []byte{1, 2, 3}, func(r *Reader) error { return r.Skip(3) }, "Reader.Skip."},
		{"Sub", []byte{1, 2, 3}, func(r *Reader) error { _, err := r.Sub(3); return err }, "Reader.Sub."},
		{"PERLength short form", []byte{0x05}, func(r *Reader) error { _, err := r.PERLength(); return err }, "Reader.PERLength->"},
		{"PERLength long form", []byte{0x81, 0x00}, func(r *Reader) error { _, err := r.PERLength(); return err }, "Reader.PERLength"},
		{"PERInteger16", []byte{0x00, 0x06}, func(r *Reader) error { _, err := r.PERInteger16(1001); return err }, "Reader.PERInteger16->"},
		{"PERUint", []byte{0x02, 0x01, 0x00}, func(r *Reader) error { _, err := r.PERUint(); return err }, "Reader.PERUint"},
		{"BERTag high form", []byte{0x7f, 0x81, 0x01}, func(r *Reader) error { _, err := r.BERTag(); return err }, "Reader.BERTag"},
		{"BERLength long form", []byte{0x82, 0x00, 0x01, 0xaa}, func(r *Reader) error {
			n, err := r.BERLength()
			if err != nil {
				return err
			}
			return r.Skip(n)
		}, "Reader.BERLength"},
		{"BERHeader", []byte{0x30, 0x01, 0x00}, func(r *Reader) error {
			n, err := r.BERHeader(BERSequenceTag)
			if err != nil {
				return err
			}
			return r.Skip(n)
		}, "Reader.BERHeader->"},
		{"BERUint", []byte{0x02, 0x02, 0xff, 0xff}, func(r *Reader) error { _, err := r.BERUint(); return err }, "Reader.BERUint->"},
		{"BERInteger", []byte{0x02, 0x02, 0x00, 0x80}, func(r *Reader) error { _, err := r.BERInteger(); return err }, "Reader.BERInteger->"},
		{"BEREnumerated", []byte{0x0a, 0x01, 0x00}, func(r *Reader) error { _, err := r.BEREnumerated(); return err }, "Reader.BEREnumerated->"},
		{"BERBoolean", []byte{0x01, 0x01, 0xff}, func(r *Reader) error { _, err := r.BERBoolean(); return err }, "Reader.BERBoolean->"},
		{"BEROctetString", []byte{0x04, 0x02, 0x01, 0x02}, func(r *Reader) error { _, err := r.BEROctetString(); return err }, "Reader.BEROctetString->"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			full := NewReader(tt.in)
			if err := tt.read(&full); err != nil {
				t.Fatalf("the whole encoding fails: %v", err)
			}
			if full.Remaining() != 0 {
				t.Fatalf("the whole encoding leaves %d bytes", full.Remaining())
			}
			for n := range len(tt.in) {
				r := NewReader(tt.in[:n])
				err := tt.read(&r)
				appErr, ok := errors.FromError(err)
				if !ok {
					t.Fatalf("cut at %d: expected *errors.Error, got %T: %v", n, err, err)
				}
				if appErr.Tag != TagTruncated && appErr.Tag != TagBERLengthBeyondInput {
					t.Errorf("cut at %d: tag %s", n, appErr.Tag)
				}
				if !appErr.HasRoute(tt.route) {
					t.Errorf("cut at %d: route %s, want it through %s", n, appErr.Route(), tt.route)
				}
			}
		})
	}
}
