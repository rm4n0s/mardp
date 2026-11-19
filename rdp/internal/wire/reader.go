// Package wire reads and writes the byte forms every layer of RDP is built
// from: little- and big-endian integers, and the PER (X.691, aligned) and BER
// (X.690) lengths and integers that MCS (T.125) and GCC (T.124) use.
//
// A Reader never reads past the end of its input: every read checks the
// bounds first and fails with a tagged error, so a parser built on it cannot
// panic on short input. After a failed read the Reader's position is not
// defined; a parser stops at the first error. A Writer appends to a byte slice. Neither allocates
// on success (SPEC §11 rule 2); a Writer allocates only when its slice has to
// grow, and an error is allocated only when there is one.
package wire

import (
	"encoding/binary"

	"github.com/rm4n0s/errors"
)

// Tags of the errors this package returns.
const (
	// TagTruncated: the input ends before the value being read.
	TagTruncated = "WireTruncated"
	// TagLengthNegative: a caller asked for a negative number of bytes.
	TagLengthNegative = "WireLengthNegative"
	// TagPatchOutOfRange: a Writer was asked to patch bytes it does not have.
	TagPatchOutOfRange = "WirePatchOutOfRange"
)

// Reader reads values from a byte slice, front to back. The zero value reads
// nothing. Slices it returns point into the input; they are not copies.
type Reader struct {
	b   []byte
	off int
}

// NewReader returns a Reader over b.
func NewReader(b []byte) Reader {
	return Reader{b: b}
}

// Offset is the number of bytes read so far.
func (r *Reader) Offset() int { return r.off }

// Remaining is the number of bytes not read yet.
func (r *Reader) Remaining() int { return len(r.b) - r.off }

// Rest returns the bytes not read yet, without consuming them.
func (r *Reader) Rest() []byte { return r.b[r.off:] }

// U8 reads one byte.
func (r *Reader) U8() (uint8, error) {
	if r.Remaining() < 1 {
		return 0, errors.New(TagTruncated, "input ends before a byte", "need", 1, "have", r.Remaining(), "offset", r.off)
	}
	v := r.b[r.off]
	r.off++
	return v, nil
}

// U16LE reads a little-endian 16-bit integer.
func (r *Reader) U16LE() (uint16, error) {
	if r.Remaining() < 2 {
		return 0, errors.New(TagTruncated, "input ends before a 16-bit integer", "need", 2, "have", r.Remaining(), "offset", r.off)
	}
	v := binary.LittleEndian.Uint16(r.b[r.off:])
	r.off += 2
	return v, nil
}

// U16BE reads a big-endian 16-bit integer.
func (r *Reader) U16BE() (uint16, error) {
	if r.Remaining() < 2 {
		return 0, errors.New(TagTruncated, "input ends before a 16-bit integer", "need", 2, "have", r.Remaining(), "offset", r.off)
	}
	v := binary.BigEndian.Uint16(r.b[r.off:])
	r.off += 2
	return v, nil
}

// U32LE reads a little-endian 32-bit integer.
func (r *Reader) U32LE() (uint32, error) {
	if r.Remaining() < 4 {
		return 0, errors.New(TagTruncated, "input ends before a 32-bit integer", "need", 4, "have", r.Remaining(), "offset", r.off)
	}
	v := binary.LittleEndian.Uint32(r.b[r.off:])
	r.off += 4
	return v, nil
}

// U32BE reads a big-endian 32-bit integer.
func (r *Reader) U32BE() (uint32, error) {
	if r.Remaining() < 4 {
		return 0, errors.New(TagTruncated, "input ends before a 32-bit integer", "need", 4, "have", r.Remaining(), "offset", r.off)
	}
	v := binary.BigEndian.Uint32(r.b[r.off:])
	r.off += 4
	return v, nil
}

// U64LE reads a little-endian 64-bit integer.
func (r *Reader) U64LE() (uint64, error) {
	if r.Remaining() < 8 {
		return 0, errors.New(TagTruncated, "input ends before a 64-bit integer", "need", 8, "have", r.Remaining(), "offset", r.off)
	}
	v := binary.LittleEndian.Uint64(r.b[r.off:])
	r.off += 8
	return v, nil
}

// Bytes reads n bytes and returns them as a slice of the input.
func (r *Reader) Bytes(n int) ([]byte, error) {
	if n < 0 {
		return nil, errors.New(TagLengthNegative, "negative byte count", "n", n, "offset", r.off)
	}
	if r.Remaining() < n {
		return nil, errors.New(TagTruncated, "input ends before the bytes", "need", n, "have", r.Remaining(), "offset", r.off)
	}
	v := r.b[r.off : r.off+n : r.off+n]
	r.off += n
	return v, nil
}

// Skip consumes n bytes.
func (r *Reader) Skip(n int) error {
	if n < 0 {
		return errors.New(TagLengthNegative, "negative byte count", "n", n, "offset", r.off)
	}
	if r.Remaining() < n {
		return errors.New(TagTruncated, "input ends before the bytes to skip", "need", n, "have", r.Remaining(), "offset", r.off)
	}
	r.off += n
	return nil
}

// Sub consumes n bytes and returns a Reader over just those bytes, for a
// structure whose length the input states.
func (r *Reader) Sub(n int) (Reader, error) {
	if n < 0 {
		return Reader{}, errors.New(TagLengthNegative, "negative byte count", "n", n, "offset", r.off)
	}
	if r.Remaining() < n {
		return Reader{}, errors.New(TagTruncated, "input ends before the stated structure", "need", n, "have", r.Remaining(), "offset", r.off)
	}
	sub := Reader{b: r.b[r.off : r.off+n : r.off+n]}
	r.off += n
	return sub, nil
}
