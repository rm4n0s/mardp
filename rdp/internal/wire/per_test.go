package wire

import (
	"bytes"
	"testing"
)

func TestPERLength(t *testing.T) {
	tests := []struct {
		n   int
		enc []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{0x7f, []byte{0x7f}},
		{0x80, []byte{0x80, 0x80}},
		{0x100, []byte{0x81, 0x00}},
		{0x3fff, []byte{0xbf, 0xff}},
	}
	for _, tt := range tests {
		var w Writer
		if err := w.PERLength(tt.n); err != nil {
			t.Fatalf("write %d: %v", tt.n, err)
		}
		if !bytes.Equal(w.Bytes(), tt.enc) {
			t.Errorf("write %d = % x, want % x", tt.n, w.Bytes(), tt.enc)
		}
		r := NewReader(tt.enc)
		got, err := r.PERLength()
		if err != nil {
			t.Fatalf("read % x: %v", tt.enc, err)
		}
		if got != tt.n || r.Remaining() != 0 {
			t.Errorf("read % x = %d (left %d), want %d", tt.enc, got, r.Remaining(), tt.n)
		}
	}
}

func TestPERInteger16(t *testing.T) {
	tests := []struct {
		v, min uint16
		enc    []byte
	}{
		{1007, 1001, []byte{0x00, 0x06}},
		{1003, 0, []byte{0x03, 0xeb}},
		{65535, 1001, []byte{0xfc, 0x16}},
		{0, 0, []byte{0x00, 0x00}},
	}
	for _, tt := range tests {
		var w Writer
		if err := w.PERInteger16(tt.v, tt.min); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(w.Bytes(), tt.enc) {
			t.Errorf("write %d-%d = % x, want % x", tt.v, tt.min, w.Bytes(), tt.enc)
		}
		r := NewReader(tt.enc)
		got, err := r.PERInteger16(tt.min)
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.v {
			t.Errorf("read % x with min %d = %d, want %d", tt.enc, tt.min, got, tt.v)
		}
	}
}

func TestPERUint(t *testing.T) {
	tests := []struct {
		v   uint32
		enc []byte
	}{
		{0, []byte{0x01, 0x00}},
		{0xff, []byte{0x01, 0xff}},
		{0x100, []byte{0x02, 0x01, 0x00}},
		{0x10000, []byte{0x03, 0x01, 0x00, 0x00}},
		{0xffffffff, []byte{0x04, 0xff, 0xff, 0xff, 0xff}},
	}
	for _, tt := range tests {
		var w Writer
		w.PERUint(tt.v)
		if !bytes.Equal(w.Bytes(), tt.enc) {
			t.Errorf("write %#x = % x, want % x", tt.v, w.Bytes(), tt.enc)
		}
		r := NewReader(tt.enc)
		got, err := r.PERUint()
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.v || r.Remaining() != 0 {
			t.Errorf("read % x = %#x, want %#x", tt.enc, got, tt.v)
		}
	}
}

// TestPERMicrosoftExamples reads MCS domain PDUs from MS-RDPBCGR's annotated
// connection sequence (4.1.5 Erect Domain Request, 4.1.7 Attach User Confirm,
// 4.1.8 Channel Join Request), field by field.
func TestPERMicrosoftExamples(t *testing.T) {
	t.Run("Erect Domain Request", func(t *testing.T) {
		r := NewReader([]byte{0x04, 0x01, 0x00, 0x01, 0x00})
		choice, err := r.U8()
		if err != nil || choice>>2 != 1 {
			t.Fatalf("choice %#x, %v", choice, err)
		}
		subHeight, err := r.PERUint()
		if err != nil || subHeight != 0 {
			t.Fatalf("subHeight %d, %v", subHeight, err)
		}
		subInterval, err := r.PERUint()
		if err != nil || subInterval != 0 || r.Remaining() != 0 {
			t.Fatalf("subInterval %d, %v", subInterval, err)
		}
	})
	t.Run("Attach User Confirm", func(t *testing.T) {
		r := NewReader([]byte{0x2e, 0x00, 0x00, 0x06})
		choice, err := r.U8()
		if err != nil || choice>>2 != 11 || choice&0x02 == 0 {
			t.Fatalf("choice %#x, %v", choice, err)
		}
		result, err := r.U8()
		if err != nil || result != 0 {
			t.Fatalf("result %d, %v", result, err)
		}
		initiator, err := r.PERInteger16(1001)
		if err != nil || initiator != 1007 {
			t.Fatalf("initiator %d, %v", initiator, err)
		}
	})
	t.Run("Channel Join Request", func(t *testing.T) {
		r := NewReader([]byte{0x38, 0x00, 0x06, 0x03, 0xeb})
		choice, err := r.U8()
		if err != nil || choice>>2 != 14 {
			t.Fatalf("choice %#x, %v", choice, err)
		}
		initiator, err := r.PERInteger16(1001)
		if err != nil || initiator != 1007 {
			t.Fatalf("initiator %d, %v", initiator, err)
		}
		channel, err := r.PERInteger16(0)
		if err != nil || channel != 1003 {
			t.Fatalf("channel %d, %v", channel, err)
		}
		var w Writer
		w.U8(0x38)
		if err := w.PERInteger16(1007, 1001); err != nil {
			t.Fatal(err)
		}
		if err := w.PERInteger16(1003, 0); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(w.Bytes(), []byte{0x38, 0x00, 0x06, 0x03, 0xeb}) {
			t.Errorf("written % x", w.Bytes())
		}
	})
}

func TestPERErrors(t *testing.T) {
	tests := []struct {
		name  string
		run   func() error
		route string
	}{
		{"fragmented length", func() error { r := NewReader([]byte{0xc1}); _, err := r.PERLength(); return err },
			"Reader.PERLength." + TagPERLengthFragmented},
		{"integer past 65535", func() error { r := NewReader([]byte{0xfc, 0x17}); _, err := r.PERInteger16(1001); return err },
			"Reader.PERInteger16." + TagPERIntegerOutOfRange},
		{"uint length 0", func() error { r := NewReader([]byte{0x00}); _, err := r.PERUint(); return err },
			"Reader.PERUint." + TagPERIntegerLengthInvalid},
		{"uint length 5", func() error { r := NewReader([]byte{0x05, 1, 2, 3, 4, 5}); _, err := r.PERUint(); return err },
			"Reader.PERUint." + TagPERIntegerLengthInvalid},
		{"uint fragmented length", func() error { r := NewReader([]byte{0xc0}); _, err := r.PERUint(); return err },
			"Reader.PERUint->Reader.PERLength." + TagPERLengthFragmented},
		{"uint truncated", func() error { r := NewReader([]byte{0x02, 0x01}); _, err := r.PERUint(); return err },
			"Reader.PERUint." + TagTruncated},
		{"length truncated", func() error { r := NewReader([]byte{0x81}); _, err := r.PERLength(); return err },
			"Reader.PERLength->Reader.U8." + TagTruncated},
		{"write length too large", func() error { var w Writer; return w.PERLength(0x4000) },
			"Writer.PERLength." + TagPERLengthOutOfRange},
		{"write length negative", func() error { var w Writer; return w.PERLength(-1) },
			"Writer.PERLength." + TagPERLengthOutOfRange},
		{"write integer below minimum", func() error { var w Writer; return w.PERInteger16(1000, 1001) },
			"Writer.PERInteger16." + TagPERIntegerBelowMinimum},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, tt.run(), tt.route)
		})
	}
}
