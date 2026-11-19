package gfx

import (
	"bytes"
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// TestAVC420 writes bitmap streams field by field against the layout of
// 2.2.4.4 and reads them back.
func TestAVC420(t *testing.T) {
	annexB := []byte{0, 0, 0, 1, 0x67, 0x64, 0, 0, 1, 0x65, 0x88}
	tests := []struct {
		name  string
		a     AVC420
		bytes string
	}{
		{"the desktop", AVC420{
			Regions: []Rect16{{0, 0, 1920, 1080}}, Quant: []QuantQuality{{QP: 22, Quality: 100}}, Bitstream: annexB},
			// numRegionRects; left, top, right, bottom; qpVal, qualityVal; the
			// Annex B data.
			"01 00 00 00  00 00 00 00 80 07 38 04  16 64  00 00 00 01 67 64 00 00 01 65 88"},
		{"two regions, one progressive", AVC420{
			Regions:   []Rect16{{1, 2, 3, 4}, {16, 32, 48, 64}},
			Quant:     []QuantQuality{{QP: 51, Quality: 0}, {QP: 0, Progressive: true, Quality: 50}},
			Bitstream: annexB[:4]},
			"02 00 00 00  01 00 02 00 03 00 04 00  10 00 20 00 30 00 40 00  33 00  80 32  00 00 00 01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w wire.Writer
			if err := AppendAVC420(&w, tt.a); err != nil {
				t.Fatal(err)
			}
			if want := unhex(t, tt.bytes); !bytes.Equal(w.Bytes(), want) {
				t.Errorf("bytes % x\nwant  % x", w.Bytes(), want)
			}
			got, err := ReadAVC420(w.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.a) {
				t.Errorf("read %+v, want %+v", got, tt.a)
			}
		})
	}

	// The reserved bit is ignored.
	got, err := ReadAVC420(unhex(t, "01 00 00 00 00 00 00 00 01 00 01 00 56 64"))
	if err != nil || got.Quant[0] != (QuantQuality{QP: 22, Quality: 100}) || len(got.Bitstream) != 0 {
		t.Errorf("read %+v, %v", got, err)
	}
}

func TestAVC420Errors(t *testing.T) {
	one := []Rect16{{0, 0, 16, 16}}
	good := []QuantQuality{{QP: 22, Quality: 100}}
	write := []struct {
		name  string
		a     AVC420
		route string
	}{
		{"no regions", AVC420{}, "AppendAVC420." + TagCountInvalid},
		{"quantizations missing", AVC420{Regions: one}, "AppendAVC420." + TagCountInvalid},
		{"empty width", AVC420{Regions: []Rect16{{4, 0, 4, 16}}, Quant: good}, "AppendAVC420." + TagRectInvalid},
		{"empty height", AVC420{Regions: []Rect16{{0, 9, 16, 8}}, Quant: good}, "AppendAVC420." + TagRectInvalid},
		{"QP too large", AVC420{Regions: one, Quant: []QuantQuality{{QP: 52}}}, "AppendAVC420." + TagQuantInvalid},
		{"quality too large", AVC420{Regions: one, Quant: []QuantQuality{{Quality: 101}}}, "AppendAVC420." + TagQuantInvalid},
	}
	for _, tt := range write {
		t.Run(tt.name, func(t *testing.T) {
			var w wire.Writer
			checkRoute(t, AppendAVC420(&w, tt.a), tt.route)
			if w.Len() != 0 {
				t.Errorf("%d bytes written", w.Len())
			}
		})
	}

	read := []struct {
		name  string
		data  string
		route string
	}{
		{"cut count", "01 00 00", "ReadAVC420->Reader.U32LE." + wire.TagTruncated},
		{"more regions than data", "02 00 00 00 00 00 00 00 10 00 10 00 16 64", "ReadAVC420." + TagCountInvalid},
		{"huge count", "ff ff ff ff", "ReadAVC420." + TagCountInvalid},
		{"empty region", "01 00 00 00 10 00 00 00 10 00 10 00 16 64", "ReadAVC420." + TagRectInvalid},
		{"QP too large", "01 00 00 00 00 00 00 00 10 00 10 00 34 64", "ReadAVC420." + TagQuantInvalid},
		{"quality too large", "01 00 00 00 00 00 00 00 10 00 10 00 16 65", "ReadAVC420." + TagQuantInvalid},
	}
	for _, tt := range read {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ReadAVC420(unhex(t, tt.data))
			checkRoute(t, err, tt.route)
		})
	}
}

// TestAVC420Hostile reads every cut and many mutations of a bitmap stream
// without a panic.
func TestAVC420Hostile(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	var w wire.Writer
	err := AppendAVC420(&w, AVC420{
		Regions:   []Rect16{{0, 0, 64, 64}, {64, 0, 128, 32}},
		Quant:     []QuantQuality{{QP: 22, Quality: 100}, {QP: 30, Quality: 80}},
		Bitstream: []byte{0, 0, 0, 1, 0x65, 1, 2, 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := w.Bytes()
	for n := range s {
		_, _ = ReadAVC420(s[:n])
	}
	for range 5000 {
		m := bytes.Clone(s)
		for range 1 + rng.IntN(4) {
			m[rng.IntN(len(m))] = byte(rng.Uint32())
		}
		_, _ = ReadAVC420(m)
	}
}
