package wire

import (
	"github.com/rm4n0s/errors"
)

// PER here is the aligned variant of X.691, as T.125 (MCS domain PDUs) and
// T.124 (GCC) use it inside RDP.

// Tags of the PER errors.
const (
	// TagPERLengthFragmented: a length determinant in the fragmented form
	// (first byte 11xxxxxx), which nothing in RDP uses.
	TagPERLengthFragmented = "PERLengthFragmented"
	// TagPERLengthOutOfRange: a length that a single determinant cannot hold
	// (more than 16383) or a negative one.
	TagPERLengthOutOfRange = "PERLengthOutOfRange"
	// TagPERIntegerOutOfRange: a constrained integer whose offset from its
	// lower bound takes it past 65535.
	TagPERIntegerOutOfRange = "PERIntegerOutOfRange"
	// TagPERIntegerBelowMinimum: a constrained integer to write that is below
	// its lower bound.
	TagPERIntegerBelowMinimum = "PERIntegerBelowMinimum"
	// TagPERIntegerLengthInvalid: a semi-constrained integer that is not 1 to
	// 4 bytes long.
	TagPERIntegerLengthInvalid = "PERIntegerLengthInvalid"
)

// PERMaxLength is the largest length one determinant holds (14 bits).
const PERMaxLength = 0x3fff

// PERLength reads a length determinant: one byte 0xxxxxxx for 0 to 127, or two
// bytes 10xxxxxx xxxxxxxx for up to 16383 (X.691 §10.9.3.6 and 10.9.3.7).
func (r *Reader) PERLength() (int, error) {
	b0, err := r.U8()
	if err != nil {
		return 0, err
	}
	switch {
	case b0&0x80 == 0:
		return int(b0), nil
	case b0&0xc0 == 0x80:
		b1, err := r.U8()
		if err != nil {
			return 0, err
		}
		return int(b0&0x3f)<<8 | int(b1), nil
	default:
		return 0, errors.New(TagPERLengthFragmented, "fragmented PER length", "byte", b0, "offset", r.off-1)
	}
}

// PERInteger16 reads a constrained whole number whose range is wider than 256
// and at most 65536 values, such as MCS's ChannelId (0..65535) or UserId
// (1001..65535): two big-endian bytes holding the offset from min
// (X.691 §10.5.7.3).
func (r *Reader) PERInteger16(min uint16) (uint16, error) {
	v, err := r.U16BE()
	if err != nil {
		return 0, err
	}
	if uint32(v)+uint32(min) > 0xffff {
		return 0, errors.New(TagPERIntegerOutOfRange, "PER integer past 65535", "offset", int(v), "min", int(min))
	}
	return v + min, nil
}

// PERUint reads a semi-constrained whole number with lower bound 0: a length
// determinant, then that many bytes of the value, big-endian and unsigned
// (X.691 §10.7). Lengths of 1 to 4 bytes are accepted.
func (r *Reader) PERUint() (uint32, error) {
	n, err := r.PERLength()
	if err != nil {
		return 0, err
	}
	if n < 1 || n > 4 {
		return 0, errors.New(TagPERIntegerLengthInvalid, "PER integer length not 1 to 4", "length", n, "offset", r.off)
	}
	if r.Remaining() < n {
		return 0, errors.New(TagTruncated, "input ends before a PER integer", "need", n, "have", r.Remaining(), "offset", r.off)
	}
	var v uint32
	for _, c := range r.b[r.off : r.off+n] {
		v = v<<8 | uint32(c)
	}
	r.off += n
	return v, nil
}

// PERLength appends a length determinant in its shortest form.
func (w *Writer) PERLength(n int) error {
	switch {
	case n < 0 || n > PERMaxLength:
		return errors.New(TagPERLengthOutOfRange, "length does not fit one PER determinant", "length", n)
	case n < 0x80:
		w.U8(uint8(n))
	default:
		w.U16BE(0x8000 | uint16(n))
	}
	return nil
}

// PERInteger16 appends a constrained whole number with lower bound min as two
// big-endian bytes (see Reader.PERInteger16).
func (w *Writer) PERInteger16(v, min uint16) error {
	if v < min {
		return errors.New(TagPERIntegerBelowMinimum, "PER integer below its lower bound", "value", int(v), "min", int(min))
	}
	w.U16BE(v - min)
	return nil
}

// PERUint appends a semi-constrained whole number with lower bound 0 in the
// fewest bytes, 1 to 4 (see Reader.PERUint).
func (w *Writer) PERUint(v uint32) {
	n := 1
	for n < 4 && v>>(8*n) != 0 {
		n++
	}
	w.U8(uint8(n))
	for i := n - 1; i >= 0; i-- {
		w.U8(uint8(v >> (8 * i)))
	}
}
