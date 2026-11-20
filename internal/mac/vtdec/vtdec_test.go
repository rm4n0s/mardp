package vtdec

import (
	"bytes"
	"testing"
)

func TestSplit(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   []byte
		want [][]byte
	}{
		{"nothing", nil, nil},
		{"no start code", []byte{1, 2, 3}, nil},
		{"one, 4 bytes", []byte{0, 0, 0, 1, 0x65, 9}, [][]byte{{0x65, 9}}},
		{"one, 3 bytes", []byte{0, 0, 1, 0x65, 9}, [][]byte{{0x65, 9}}},
		{"three, mixed", []byte{0, 0, 0, 1, 0x67, 1, 0, 0, 1, 0x68, 2, 0, 0, 0, 1, 0x65, 3, 4}, [][]byte{{0x67, 1}, {0x68, 2}, {0x65, 3, 4}}},
		{"an empty one is left out", []byte{0, 0, 1, 0, 0, 1, 0x41, 7}, [][]byte{{0x41, 7}}},
		{"a start code at the end", []byte{0, 0, 1, 0x41, 7, 0, 0, 1}, [][]byte{{0x41, 7}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := split(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("%d NAL units %v, want %d", len(got), got, len(tt.want))
			}
			for i := range got {
				if !bytes.Equal(got[i], tt.want[i]) {
					t.Errorf("NAL unit %d = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParse(t *testing.T) {
	// A key frame as the host sends it: SPS, PPS, a note the decoder does
	// not need (SEI), and a picture in two slices.
	u := parse([]byte{0, 0, 0, 1, 0x67, 1, 2, 0, 0, 0, 1, 0x68, 3, 0, 0, 0, 1, 0x06, 9, 0, 0, 0, 1, 0x65, 4, 5, 0, 0, 1, 0x65, 6})
	if !bytes.Equal(u.sps, []byte{0x67, 1, 2}) || !bytes.Equal(u.pps, []byte{0x68, 3}) {
		t.Errorf("sps %v, pps %v", u.sps, u.pps)
	}
	if want := []byte{0, 0, 0, 3, 0x65, 4, 5, 0, 0, 0, 2, 0x65, 6}; !bytes.Equal(u.avcc, want) {
		t.Errorf("avcc %v, want %v", u.avcc, want)
	}
	// A frame after it: one slice, no parameter sets.
	u = parse([]byte{0, 0, 0, 1, 0x41, 7, 8})
	if u.sps != nil || u.pps != nil || !bytes.Equal(u.avcc, []byte{0, 0, 0, 3, 0x41, 7, 8}) {
		t.Errorf("%+v", u)
	}
}
