package wire

import (
	"encoding/binary"

	"github.com/rm4n0s/errors"
)

// Writer appends values to a byte slice. Give it a slice with enough capacity
// (from a pool) and it does not allocate. The zero value is ready to use.
type Writer struct {
	b []byte
}

// NewWriter returns a Writer that appends to buf[:0], reusing its capacity.
func NewWriter(buf []byte) Writer {
	return Writer{b: buf[:0]}
}

// Bytes returns what was written. The slice is the Writer's own; it changes
// with the next write.
func (w *Writer) Bytes() []byte { return w.b }

// Len is the number of bytes written.
func (w *Writer) Len() int { return len(w.b) }

// Reset drops what was written and keeps the capacity.
func (w *Writer) Reset() { w.b = w.b[:0] }

// U8 appends one byte.
func (w *Writer) U8(v uint8) { w.b = append(w.b, v) }

// U16LE appends a little-endian 16-bit integer.
func (w *Writer) U16LE(v uint16) { w.b = binary.LittleEndian.AppendUint16(w.b, v) }

// U16BE appends a big-endian 16-bit integer.
func (w *Writer) U16BE(v uint16) { w.b = binary.BigEndian.AppendUint16(w.b, v) }

// U32LE appends a little-endian 32-bit integer.
func (w *Writer) U32LE(v uint32) { w.b = binary.LittleEndian.AppendUint32(w.b, v) }

// U32BE appends a big-endian 32-bit integer.
func (w *Writer) U32BE(v uint32) { w.b = binary.BigEndian.AppendUint32(w.b, v) }

// U64LE appends a little-endian 64-bit integer.
func (w *Writer) U64LE(v uint64) { w.b = binary.LittleEndian.AppendUint64(w.b, v) }

// Write appends p.
func (w *Writer) Write(p []byte) { w.b = append(w.b, p...) }

// Zeros appends n zero bytes; a negative n appends nothing.
func (w *Writer) Zeros(n int) {
	for range n {
		w.b = append(w.b, 0)
	}
}

// Reserve appends n zero bytes and returns their offset, for a length that is
// known only after what follows is written (patch it with PatchU16BE and the
// like).
func (w *Writer) Reserve(n int) int {
	off := len(w.b)
	w.Zeros(n)
	return off
}

// PatchU16BE overwrites the two bytes at off with v, big-endian.
func (w *Writer) PatchU16BE(off int, v uint16) error {
	if off < 0 || off+2 > len(w.b) {
		return errors.New(TagPatchOutOfRange, "patch outside the written bytes", "offset", off, "size", 2, "len", len(w.b))
	}
	binary.BigEndian.PutUint16(w.b[off:], v)
	return nil
}

// PatchU16LE overwrites the two bytes at off with v, little-endian.
func (w *Writer) PatchU16LE(off int, v uint16) error {
	if off < 0 || off+2 > len(w.b) {
		return errors.New(TagPatchOutOfRange, "patch outside the written bytes", "offset", off, "size", 2, "len", len(w.b))
	}
	binary.LittleEndian.PutUint16(w.b[off:], v)
	return nil
}

// PatchU32LE overwrites the four bytes at off with v, little-endian.
func (w *Writer) PatchU32LE(off int, v uint32) error {
	if off < 0 || off+4 > len(w.b) {
		return errors.New(TagPatchOutOfRange, "patch outside the written bytes", "offset", off, "size", 4, "len", len(w.b))
	}
	binary.LittleEndian.PutUint32(w.b[off:], v)
	return nil
}
