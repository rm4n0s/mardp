package wire

import (
	"bytes"
	"math"
	"testing"
)

func TestBERTag(t *testing.T) {
	tests := []struct {
		tag BERTag
		enc []byte
	}{
		{BERIntegerTag, []byte{0x02}},
		{BERSequenceTag, []byte{0x30}},
		{BERTag{Class: BERApplication, Constructed: true, Number: 101}, []byte{0x7f, 0x65}}, // MCS Connect-Initial
		{BERTag{Class: BERApplication, Constructed: true, Number: 102}, []byte{0x7f, 0x66}}, // MCS Connect-Response
		{BERTag{Class: BERContext, Number: 30}, []byte{0x9e}},
		{BERTag{Class: BERContext, Number: 31}, []byte{0x9f, 0x1f}},
		{BERTag{Class: BERPrivate, Number: 200}, []byte{0xdf, 0x81, 0x48}},
		{BERTag{Class: BERUniversal, Number: 0xfffffff}, []byte{0x1f, 0xff, 0xff, 0xff, 0x7f}},
	}
	for _, tt := range tests {
		var w Writer
		w.BERTag(tt.tag)
		if !bytes.Equal(w.Bytes(), tt.enc) {
			t.Errorf("write %+v = % x, want % x", tt.tag, w.Bytes(), tt.enc)
		}
		r := NewReader(tt.enc)
		got, err := r.BERTag()
		if err != nil {
			t.Fatalf("read % x: %v", tt.enc, err)
		}
		if got != tt.tag || r.Remaining() != 0 {
			t.Errorf("read % x = %+v, want %+v", tt.enc, got, tt.tag)
		}
	}
}

func TestBERLength(t *testing.T) {
	tests := []struct {
		n   int
		enc []byte
	}{
		{0, []byte{0x00}},
		{0x7f, []byte{0x7f}},
		{0x80, []byte{0x81, 0x80}},
		{0xff, []byte{0x81, 0xff}},
		{0x100, []byte{0x82, 0x01, 0x00}},
		{0x194, []byte{0x82, 0x01, 0x94}},
		{0x10000, []byte{0x83, 0x01, 0x00, 0x00}},
	}
	for _, tt := range tests {
		var w Writer
		if err := w.BERLength(tt.n); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(w.Bytes(), tt.enc) {
			t.Errorf("write %d = % x, want % x", tt.n, w.Bytes(), tt.enc)
		}
		// The reader checks the contents are there, so give it n bytes.
		in := append(append([]byte{}, tt.enc...), make([]byte, tt.n)...)
		r := NewReader(in)
		got, err := r.BERLength()
		if err != nil {
			t.Fatalf("read % x: %v", tt.enc, err)
		}
		if got != tt.n || r.Remaining() != tt.n {
			t.Errorf("read % x = %d, want %d", tt.enc, got, tt.n)
		}
	}
	var w Writer
	if err := w.BERLength(0x1000000); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Bytes(), []byte{0x84, 0x01, 0x00, 0x00, 0x00}) {
		t.Errorf("write 0x1000000 = % x", w.Bytes())
	}
}

func TestBERIntegers(t *testing.T) {
	tests := []struct {
		v   int64
		enc []byte
	}{
		{0, []byte{0x02, 0x01, 0x00}},
		{1, []byte{0x02, 0x01, 0x01}},
		{34, []byte{0x02, 0x01, 0x22}},
		{127, []byte{0x02, 0x01, 0x7f}},
		{128, []byte{0x02, 0x02, 0x00, 0x80}},
		{65535, []byte{0x02, 0x03, 0x00, 0xff, 0xff}},
		{-1, []byte{0x02, 0x01, 0xff}},
		{-128, []byte{0x02, 0x01, 0x80}},
		{-129, []byte{0x02, 0x02, 0xff, 0x7f}},
		{math.MaxInt64, []byte{0x02, 0x08, 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},
		{math.MinInt64, []byte{0x02, 0x08, 0x80, 0, 0, 0, 0, 0, 0, 0}},
	}
	for _, tt := range tests {
		var w Writer
		w.BERInteger(tt.v)
		if !bytes.Equal(w.Bytes(), tt.enc) {
			t.Errorf("write %d = % x, want % x", tt.v, w.Bytes(), tt.enc)
		}
		r := NewReader(tt.enc)
		got, err := r.BERInteger()
		if err != nil {
			t.Fatalf("read % x: %v", tt.enc, err)
		}
		if got != tt.v || r.Remaining() != 0 {
			t.Errorf("read % x = %d, want %d", tt.enc, got, tt.v)
		}

		// ENUMERATED is the same with tag 10.
		enc := append([]byte{0x0a}, tt.enc[1:]...)
		w.Reset()
		w.BEREnumerated(tt.v)
		if !bytes.Equal(w.Bytes(), enc) {
			t.Errorf("write enumerated %d = % x, want % x", tt.v, w.Bytes(), enc)
		}
		r = NewReader(enc)
		got, err = r.BEREnumerated()
		if err != nil || got != tt.v {
			t.Errorf("read enumerated % x = %d, %v", enc, got, err)
		}
	}
	// A longer encoding than needed is still read.
	r := NewReader([]byte{0x02, 0x03, 0x00, 0x00, 0x05})
	if v, err := r.BERInteger(); err != nil || v != 5 {
		t.Errorf("padded integer = %d, %v", v, err)
	}
}

func TestBERUint(t *testing.T) {
	tests := []struct {
		enc  []byte
		want uint32
	}{
		{[]byte{0x02, 0x01, 0x22}, 34},
		{[]byte{0x02, 0x01, 0xff}, 255},
		{[]byte{0x02, 0x02, 0xff, 0xff}, 65535},
		{[]byte{0x02, 0x03, 0x00, 0xff, 0xf8}, 65528},
		{[]byte{0x02, 0x04, 0xff, 0xff, 0xff, 0xff}, 0xffffffff},
		{[]byte{0x02, 0x05, 0x00, 0xff, 0xff, 0xff, 0xff}, 0xffffffff},
	}
	for _, tt := range tests {
		r := NewReader(tt.enc)
		got, err := r.BERUint()
		if err != nil {
			t.Fatalf("read % x: %v", tt.enc, err)
		}
		if got != tt.want || r.Remaining() != 0 {
			t.Errorf("read % x = %d, want %d", tt.enc, got, tt.want)
		}
	}
}

func TestBERBooleanAndOctetString(t *testing.T) {
	for _, v := range []bool{true, false} {
		var w Writer
		w.BERBoolean(v)
		want := []byte{0x01, 0x01, 0x00}
		if v {
			want[2] = 0xff
		}
		if !bytes.Equal(w.Bytes(), want) {
			t.Errorf("write %v = % x", v, w.Bytes())
		}
		r := NewReader(w.Bytes())
		got, err := r.BERBoolean()
		if err != nil || got != v {
			t.Errorf("read %v = %v, %v", v, got, err)
		}
	}
	r := NewReader([]byte{0x01, 0x01, 0x01})
	if v, err := r.BERBoolean(); err != nil || !v {
		t.Errorf("0x01 reads as %v, %v; any non-zero byte is true", v, err)
	}

	var w Writer
	if err := w.BEROctetString([]byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Bytes(), []byte{0x04, 0x03, 1, 2, 3}) {
		t.Errorf("octet string = % x", w.Bytes())
	}
	r = NewReader(w.Bytes())
	s, err := r.BEROctetString()
	if err != nil || !bytes.Equal(s, []byte{1, 2, 3}) {
		t.Errorf("read octet string = % x, %v", s, err)
	}
}

// TestBERMicrosoftExample reads the start of the MCS Connect-Initial PDU of
// MS-RDPBCGR 4.1.3 (Client MCS Connect Initial PDU with GCC Conference Create
// Request), up to the end of targetParameters, and writes it back.
func TestBERMicrosoftExample(t *testing.T) {
	in := []byte{
		0x7f, 0x65, 0x82, 0x01, 0x94, // Connect-Initial, length 0x194
		0x04, 0x01, 0x01, // callingDomainSelector
		0x04, 0x01, 0x01, // calledDomainSelector
		0x01, 0x01, 0xff, // upwardFlag
		0x30, 0x19, // targetParameters
		0x02, 0x01, 0x22, // maxChannelIds 34
		0x02, 0x01, 0x02, // maxUserIds 2
		0x02, 0x01, 0x00, // maxTokenIds 0
		0x02, 0x01, 0x01, // numPriorities 1
		0x02, 0x01, 0x00, // minThroughput 0
		0x02, 0x01, 0x01, // maxHeight 1
		0x02, 0x02, 0xff, 0xff, // maxMCSPDUsize 65535
		0x02, 0x01, 0x02, // protocolVersion 2
	}
	head := len(in)
	// The rest of the PDU (minimum and maximum parameters, user data) is not
	// read here; pad so the stated length holds.
	in = append(in, make([]byte, 0x194-(head-5))...)

	r := NewReader(in)
	n, err := r.BERHeader(BERTag{Class: BERApplication, Constructed: true, Number: 101})
	if err != nil || n != 0x194 {
		t.Fatalf("Connect-Initial header: %d, %v", n, err)
	}
	for _, name := range []string{"callingDomainSelector", "calledDomainSelector"} {
		s, err := r.BEROctetString()
		if err != nil || !bytes.Equal(s, []byte{1}) {
			t.Fatalf("%s = % x, %v", name, s, err)
		}
	}
	up, err := r.BERBoolean()
	if err != nil || !up {
		t.Fatalf("upwardFlag = %v, %v", up, err)
	}
	n, err = r.BERHeader(BERSequenceTag)
	if err != nil || n != 0x19 {
		t.Fatalf("targetParameters: %d, %v", n, err)
	}
	params, err := r.Sub(n)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{34, 2, 0, 1, 0, 1, 65535, 2}
	var got []uint32
	for params.Remaining() > 0 {
		v, err := params.BERUint()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if len(got) != len(want) {
		t.Fatalf("targetParameters = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("targetParameters = %v, want %v", got, want)
			break
		}
	}

	// Write the same head back, with the reserved two-byte lengths
	// Microsoft's example uses. The writer encodes 65535 as strict BER
	// (02 03 00 ff ff), one byte longer than the example's 02 02 ff ff, so
	// targetParameters is 0x1a long.
	expect := append([]byte{}, in...)
	expect[15] = 0x1a
	copy(expect[34:], []byte{0x02, 0x03, 0x00, 0xff, 0xff, 0x02, 0x01, 0x02, 0x00})
	var w Writer
	w.BERTag(BERTag{Class: BERApplication, Constructed: true, Number: 101})
	lenOff := w.BERReserveLength()
	start := w.Len()
	for range 2 {
		if err := w.BEROctetString([]byte{1}); err != nil {
			t.Fatal(err)
		}
	}
	w.BERBoolean(true)
	w.BERTag(BERSequenceTag)
	if err := w.BERLength(0x1a); err != nil {
		t.Fatal(err)
	}
	for _, v := range want {
		w.BERInteger(int64(v))
	}
	w.Write(make([]byte, 0x194-(w.Len()-start)))
	if err := w.PatchBERLength(lenOff, w.Len()-start); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Bytes(), expect) {
		t.Errorf("written head = % x\nwant           % x", w.Bytes()[:head+1], expect[:head+1])
	}
}

func TestBERErrors(t *testing.T) {
	tests := []struct {
		name  string
		run   func() error
		route string
	}{
		{"tag too long", func() error {
			r := NewReader([]byte{0x1f, 0x81, 0x81, 0x81, 0x81, 0x01})
			_, err := r.BERTag()
			return err
		},
			"Reader.BERTag." + TagBERTagTooLong},
		{"tag mismatch", func() error { r := NewReader([]byte{0x04, 0x01, 0x00}); _, err := r.BERInteger(); return err },
			"Reader.BERInteger->Reader.BERHeader." + TagBERTagMismatch},
		{"constructed mismatch", func() error { r := NewReader([]byte{0x10, 0x00}); _, err := r.BERHeader(BERSequenceTag); return err },
			"Reader.BERHeader." + TagBERTagMismatch},
		{"length indefinite", func() error { r := NewReader([]byte{0x80, 0x00, 0x00}); _, err := r.BERLength(); return err },
			"Reader.BERLength." + TagBERLengthIndefinite},
		{"length too long", func() error { r := NewReader([]byte{0x85, 0, 0, 0, 0, 1}); _, err := r.BERLength(); return err },
			"Reader.BERLength." + TagBERLengthTooLong},
		{"short length beyond input", func() error { r := NewReader([]byte{0x02, 0x00}); _, err := r.BERLength(); return err },
			"Reader.BERLength." + TagBERLengthBeyondInput},
		{"long length beyond input", func() error {
			r := NewReader([]byte{0x84, 0xff, 0xff, 0xff, 0xff, 0x00})
			_, err := r.BERLength()
			return err
		},
			"Reader.BERLength." + TagBERLengthBeyondInput},
		{"long length truncated", func() error { r := NewReader([]byte{0x82, 0x01}); _, err := r.BERLength(); return err },
			"Reader.BERLength." + TagTruncated},
		{"integer of 0 bytes", func() error { r := NewReader([]byte{0x02, 0x00}); _, err := r.BERInteger(); return err },
			"Reader.BERInteger->Reader.berSigned." + TagBERIntegerLengthInvalid},
		{"integer of 9 bytes", func() error {
			r := NewReader([]byte{0x02, 0x09, 0, 0, 0, 0, 0, 0, 0, 0, 1})
			_, err := r.BERInteger()
			return err
		}, "Reader.BERInteger->Reader.berSigned." + TagBERIntegerLengthInvalid},
		{"uint of 5 bytes", func() error { r := NewReader([]byte{0x02, 0x05, 1, 0, 0, 0, 0}); _, err := r.BERUint(); return err },
			"Reader.BERUint." + TagBERIntegerLengthInvalid},
		{"uint of 0 bytes", func() error { r := NewReader([]byte{0x02, 0x00}); _, err := r.BERUint(); return err },
			"Reader.BERUint." + TagBERIntegerLengthInvalid},
		{"uint tag mismatch", func() error { r := NewReader([]byte{0x0a, 0x01, 0x00}); _, err := r.BERUint(); return err },
			"Reader.BERUint->Reader.BERHeader." + TagBERTagMismatch},
		{"enumerated of 0 bytes", func() error { r := NewReader([]byte{0x0a, 0x00}); _, err := r.BEREnumerated(); return err },
			"Reader.BEREnumerated->Reader.berSigned." + TagBERIntegerLengthInvalid},
		{"boolean of 2 bytes", func() error { r := NewReader([]byte{0x01, 0x02, 0x00, 0x00}); _, err := r.BERBoolean(); return err },
			"Reader.BERBoolean." + TagBERBooleanLengthInvalid},
		{"octet string beyond input", func() error { r := NewReader([]byte{0x04, 0x05, 0x00}); _, err := r.BEROctetString(); return err },
			"Reader.BEROctetString->Reader.BERHeader->Reader.BERLength." + TagBERLengthBeyondInput},
		{"write negative length", func() error { var w Writer; return w.BERLength(-1) },
			"Writer.BERLength." + TagBERLengthOutOfRange},
		{"patch length too large", func() error { var w Writer; off := w.BERReserveLength(); return w.PatchBERLength(off, 0x10000) },
			"Writer.PatchBERLength." + TagBERLengthOutOfRange},
		{"patch without a reserved length", func() error { var w Writer; w.U16BE(0); w.U8(0); return w.PatchBERLength(0, 1) },
			"Writer.PatchBERLength." + TagPatchOutOfRange},
		{"patch past the end", func() error { var w Writer; off := w.BERReserveLength(); return w.PatchBERLength(off+1, 1) },
			"Writer.PatchBERLength." + TagPatchOutOfRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, tt.run(), tt.route)
		})
	}
}
