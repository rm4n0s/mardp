package wire

import (
	"github.com/rm4n0s/errors"
)

// BER here is the definite-length form of X.690, as MCS (T.125) uses it for
// Connect-Initial and Connect-Response.

// Tags of the BER errors.
const (
	// TagBERTagTooLong: a high tag number longer than 4 bytes.
	TagBERTagTooLong = "BERTagTooLong"
	// TagBERTagMismatch: an element whose tag is not the one expected.
	TagBERTagMismatch = "BERTagMismatch"
	// TagBERLengthIndefinite: the indefinite length form (0x80), which MCS
	// does not use.
	TagBERLengthIndefinite = "BERLengthIndefinite"
	// TagBERLengthTooLong: a long-form length of more than 4 bytes.
	TagBERLengthTooLong = "BERLengthTooLong"
	// TagBERLengthBeyondInput: a length larger than the input left.
	TagBERLengthBeyondInput = "BERLengthBeyondInput"
	// TagBERLengthOutOfRange: a length to write that is negative or does not
	// fit the form asked for.
	TagBERLengthOutOfRange = "BERLengthOutOfRange"
	// TagBERIntegerLengthInvalid: an INTEGER or ENUMERATED of 0 bytes or more
	// than 8.
	TagBERIntegerLengthInvalid = "BERIntegerLengthInvalid"
	// TagBERBooleanLengthInvalid: a BOOLEAN that is not 1 byte long.
	TagBERBooleanLengthInvalid = "BERBooleanLengthInvalid"
)

// Classes of a BER tag (the top two bits of its first byte).
const (
	BERUniversal   uint8 = 0
	BERApplication uint8 = 1
	BERContext     uint8 = 2
	BERPrivate     uint8 = 3
)

// BERTag is the identifier of a BER element.
type BERTag struct {
	Class       uint8
	Constructed bool
	Number      uint32
}

// The universal tags MCS uses.
var (
	BERBooleanTag     = BERTag{Class: BERUniversal, Number: 1}
	BERIntegerTag     = BERTag{Class: BERUniversal, Number: 2}
	BEROctetStringTag = BERTag{Class: BERUniversal, Number: 4}
	BEREnumeratedTag  = BERTag{Class: BERUniversal, Number: 10}
	BERSequenceTag    = BERTag{Class: BERUniversal, Constructed: true, Number: 16}
)

// BERTag reads an identifier, in the low form (one byte) or the high form
// (0x1f in the low bits, then the number in base 128, at most 4 bytes).
func (r *Reader) BERTag() (BERTag, error) {
	b0, err := r.U8()
	if err != nil {
		return BERTag{}, err
	}
	t := BERTag{Class: b0 >> 6, Constructed: b0&0x20 != 0, Number: uint32(b0 & 0x1f)}
	if t.Number != 0x1f {
		return t, nil
	}
	t.Number = 0
	for i := 0; ; i++ {
		if i == 4 {
			return BERTag{}, errors.New(TagBERTagTooLong, "BER tag number longer than 4 bytes", "offset", r.off)
		}
		b, err := r.U8()
		if err != nil {
			return BERTag{}, err
		}
		t.Number = t.Number<<7 | uint32(b&0x7f)
		if b&0x80 == 0 {
			return t, nil
		}
	}
}

// BERLength reads a definite length, short (one byte below 0x80) or long
// (0x81 to 0x84 followed by that many big-endian bytes), and checks that the
// input holds that many bytes.
func (r *Reader) BERLength() (int, error) {
	b0, err := r.U8()
	if err != nil {
		return 0, err
	}
	if b0 < 0x80 {
		if int(b0) > r.Remaining() {
			return 0, errors.New(TagBERLengthBeyondInput, "BER length beyond the input", "length", int(b0), "have", r.Remaining(), "offset", r.off)
		}
		return int(b0), nil
	}
	k := int(b0 & 0x7f)
	if k == 0 {
		return 0, errors.New(TagBERLengthIndefinite, "indefinite BER length", "offset", r.off-1)
	}
	if k > 4 {
		return 0, errors.New(TagBERLengthTooLong, "BER length of more than 4 bytes", "bytes", k, "offset", r.off-1)
	}
	if r.Remaining() < k {
		return 0, errors.New(TagTruncated, "input ends before a BER length", "need", k, "have", r.Remaining(), "offset", r.off)
	}
	var n uint64
	for _, c := range r.b[r.off : r.off+k] {
		n = n<<8 | uint64(c)
	}
	r.off += k
	if n > uint64(r.Remaining()) {
		return 0, errors.New(TagBERLengthBeyondInput, "BER length beyond the input", "length", n, "have", r.Remaining(), "offset", r.off)
	}
	return int(n), nil
}

// BERHeader reads an element's tag and length, checks the tag is want, and
// returns the length of the element's contents.
func (r *Reader) BERHeader(want BERTag) (int, error) {
	start := r.off
	t, err := r.BERTag()
	if err != nil {
		return 0, err
	}
	if t != want {
		return 0, errors.New(TagBERTagMismatch, "unexpected BER tag",
			"class", int(t.Class), "constructed", t.Constructed, "number", t.Number,
			"wantClass", int(want.Class), "wantConstructed", want.Constructed, "wantNumber", want.Number,
			"offset", start)
	}
	return r.BERLength()
}

// BERInteger reads an INTEGER element: 1 to 8 bytes of two's complement.
func (r *Reader) BERInteger() (int64, error) {
	n, err := r.BERHeader(BERIntegerTag)
	if err != nil {
		return 0, err
	}
	return r.berSigned(n)
}

// BERUint reads an INTEGER element as an unsigned value of 1 to 4 bytes,
// ignoring the sign bit. MCS's DomainParameters are all non-negative, and
// real encoders write them so: MS-RDPBCGR's own Connect-Initial example has
// maxMCSPDUsize 65535 as 02 02 ff ff, which strict BER would read as -1.
// A leading zero byte (02 03 00 ff ff) is accepted too.
func (r *Reader) BERUint() (uint32, error) {
	n, err := r.BERHeader(BERIntegerTag)
	if err != nil {
		return 0, err
	}
	c := r.b[r.off : r.off+n]
	if n == 5 && c[0] == 0 {
		c = c[1:]
	}
	if len(c) < 1 || len(c) > 4 {
		return 0, errors.New(TagBERIntegerLengthInvalid, "BER unsigned integer length not 1 to 4", "length", n, "offset", r.off)
	}
	var v uint32
	for _, b := range c {
		v = v<<8 | uint32(b)
	}
	r.off += n
	return v, nil
}

// BEREnumerated reads an ENUMERATED element, encoded like an INTEGER.
func (r *Reader) BEREnumerated() (int64, error) {
	n, err := r.BERHeader(BEREnumeratedTag)
	if err != nil {
		return 0, err
	}
	return r.berSigned(n)
}

// berSigned reads n content bytes of two's complement. BERHeader has already
// checked that the input holds them.
func (r *Reader) berSigned(n int) (int64, error) {
	if n < 1 || n > 8 {
		return 0, errors.New(TagBERIntegerLengthInvalid, "BER integer length not 1 to 8", "length", n, "offset", r.off)
	}
	c := r.b[r.off : r.off+n]
	v := int64(int8(c[0]))
	for _, b := range c[1:] {
		v = v<<8 | int64(b)
	}
	r.off += n
	return v, nil
}

// BERBoolean reads a BOOLEAN element; any non-zero byte is true.
func (r *Reader) BERBoolean() (bool, error) {
	n, err := r.BERHeader(BERBooleanTag)
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, errors.New(TagBERBooleanLengthInvalid, "BER boolean length not 1", "length", n, "offset", r.off)
	}
	v := r.b[r.off] != 0
	r.off++
	return v, nil
}

// BEROctetString reads an OCTET STRING element and returns its contents as a
// slice of the input.
func (r *Reader) BEROctetString() ([]byte, error) {
	n, err := r.BERHeader(BEROctetStringTag)
	if err != nil {
		return nil, err
	}
	v := r.b[r.off : r.off+n : r.off+n]
	r.off += n
	return v, nil
}

// BERTag appends an identifier in its shortest form.
func (w *Writer) BERTag(t BERTag) {
	b0 := t.Class << 6
	if t.Constructed {
		b0 |= 0x20
	}
	if t.Number < 0x1f {
		w.U8(b0 | uint8(t.Number))
		return
	}
	w.U8(b0 | 0x1f)
	n := 1
	for n < 5 && t.Number>>(7*n) != 0 {
		n++
	}
	for i := n - 1; i >= 0; i-- {
		b := uint8(t.Number>>(7*i)) & 0x7f
		if i > 0 {
			b |= 0x80
		}
		w.U8(b)
	}
}

// BERLength appends a definite length in its shortest form.
func (w *Writer) BERLength(n int) error {
	if n < 0 || uint64(n) > 0xffffffff {
		return errors.New(TagBERLengthOutOfRange, "BER length out of range", "length", n)
	}
	if n < 0x80 {
		w.U8(uint8(n))
		return nil
	}
	k := 1
	for k < 4 && uint64(n)>>(8*k) != 0 {
		k++
	}
	w.U8(0x80 | uint8(k))
	for i := k - 1; i >= 0; i-- {
		w.U8(uint8(n >> (8 * i)))
	}
	return nil
}

// BERReserveLength appends a two-byte long-form length (0x82 0 0) to be filled
// in by PatchBERLength once the contents are written, and returns its offset.
// The long form for a short length is valid BER, and Microsoft's own examples
// of MCS use it.
func (w *Writer) BERReserveLength() int {
	off := len(w.b)
	w.U8(0x82)
	w.U16BE(0)
	return off
}

// PatchBERLength fills in a length reserved by BERReserveLength.
func (w *Writer) PatchBERLength(off, n int) error {
	if n < 0 || n > 0xffff {
		return errors.New(TagBERLengthOutOfRange, "BER length does not fit two bytes", "length", n)
	}
	if off < 0 || off+3 > len(w.b) || w.b[off] != 0x82 {
		return errors.New(TagPatchOutOfRange, "no reserved BER length at the offset", "offset", off, "len", len(w.b))
	}
	w.b[off+1] = uint8(n >> 8)
	w.b[off+2] = uint8(n)
	return nil
}

// BERInteger appends an INTEGER element in the fewest bytes of two's
// complement.
func (w *Writer) BERInteger(v int64) {
	w.BERTag(BERIntegerTag)
	w.berSigned(v)
}

// BEREnumerated appends an ENUMERATED element.
func (w *Writer) BEREnumerated(v int64) {
	w.BERTag(BEREnumeratedTag)
	w.berSigned(v)
}

// berSigned appends the length and the fewest bytes of two's complement of v.
func (w *Writer) berSigned(v int64) {
	n := 1
	// Grow while the bytes left out would not be a pure sign extension.
	for n < 8 && (v>>(8*n-1) != 0 && v>>(8*n-1) != -1) {
		n++
	}
	w.U8(uint8(n))
	for i := n - 1; i >= 0; i-- {
		w.U8(uint8(v >> (8 * i)))
	}
}

// BERBoolean appends a BOOLEAN element (0xff for true).
func (w *Writer) BERBoolean(v bool) {
	w.BERTag(BERBooleanTag)
	w.U8(1)
	if v {
		w.U8(0xff)
	} else {
		w.U8(0)
	}
}

// BEROctetString appends an OCTET STRING element.
func (w *Writer) BEROctetString(p []byte) error {
	w.BERTag(BEROctetStringTag)
	if err := w.BERLength(len(p)); err != nil {
		return err
	}
	w.Write(p)
	return nil
}
