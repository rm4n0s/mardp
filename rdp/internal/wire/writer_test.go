package wire

import (
	"bytes"
	"testing"
)

func TestWriterIntegers(t *testing.T) {
	w := NewWriter(make([]byte, 5, 64))
	if w.Len() != 0 {
		t.Fatalf("NewWriter keeps %d old bytes", w.Len())
	}
	w.U8(0xab)
	w.U16LE(0x1234)
	w.U16BE(0x1234)
	w.U32LE(0x12345678)
	w.U32BE(0x12345678)
	w.U64LE(0x0102030405060708)
	w.Write([]byte{9, 9})
	w.Zeros(2)
	want := []byte{
		0xab,
		0x34, 0x12,
		0x12, 0x34,
		0x78, 0x56, 0x34, 0x12,
		0x12, 0x34, 0x56, 0x78,
		0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01,
		9, 9, 0, 0,
	}
	if !bytes.Equal(w.Bytes(), want) {
		t.Errorf("written % x\nwant    % x", w.Bytes(), want)
	}
	w.Reset()
	if w.Len() != 0 || cap(w.Bytes()) != 64 {
		t.Errorf("after Reset: len %d cap %d", w.Len(), cap(w.Bytes()))
	}
}

func TestWriterPatch(t *testing.T) {
	var w Writer
	w.U8(0x03)
	off := w.Reserve(2)
	w.U32LE(0)
	w.U8(0)
	if err := w.PatchU16BE(off, 0x0102); err != nil {
		t.Fatal(err)
	}
	if err := w.PatchU16LE(off+2, 0x0304); err != nil {
		t.Fatal(err)
	}
	if err := w.PatchU32LE(off+3, 0x05060708); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x03, 0x01, 0x02, 0x04, 0x08, 0x07, 0x06, 0x05}
	if !bytes.Equal(w.Bytes(), want) {
		t.Errorf("patched % x, want % x", w.Bytes(), want)
	}
}

func TestWriterErrors(t *testing.T) {
	tests := []struct {
		name  string
		run   func(w *Writer) error
		route string
	}{
		{"PatchU16BE past the end", func(w *Writer) error { return w.PatchU16BE(3, 1) }, "Writer.PatchU16BE." + TagPatchOutOfRange},
		{"PatchU16BE negative", func(w *Writer) error { return w.PatchU16BE(-1, 1) }, "Writer.PatchU16BE." + TagPatchOutOfRange},
		{"PatchU16LE past the end", func(w *Writer) error { return w.PatchU16LE(3, 1) }, "Writer.PatchU16LE." + TagPatchOutOfRange},
		{"PatchU32LE past the end", func(w *Writer) error { return w.PatchU32LE(1, 1) }, "Writer.PatchU32LE." + TagPatchOutOfRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w Writer
			w.U32LE(0)
			assertRoute(t, tt.run(&w), tt.route)
		})
	}
}
