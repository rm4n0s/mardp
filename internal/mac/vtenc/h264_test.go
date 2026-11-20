package vtenc

import (
	"bytes"
	"testing"
)

// The H.264 syntax the tests read (ITU-T H.264 7.3, E.1): enough of the
// sequence parameter set to reach the VUI's bitstream restriction, and the
// start of a slice header for its type.

// NAL unit types.
const (
	nalSlice = 1
	nalIDR   = 5
	nalSPS   = 7
	nalPPS   = 8
)

// Slice types, modulo 5.
const (
	sliceP = 0
	sliceB = 1
	sliceI = 2
)

// splitAnnexB returns the NAL units of an Annex B stream (3- or 4-byte
// start codes).
func splitAnnexB(b []byte) [][]byte {
	var nals [][]byte
	start := -1
	for i := 0; i+3 <= len(b); {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			if start >= 0 {
				end := i
				if end > start && b[end-1] == 0 {
					end--
				}
				nals = append(nals, b[start:end])
			}
			i += 3
			start = i
			continue
		}
		i++
	}
	if start >= 0 && start <= len(b) {
		nals = append(nals, b[start:])
	}
	return nals
}

// unescape removes the emulation prevention bytes of a NAL unit.
func unescape(nal []byte) []byte {
	out := make([]byte, 0, len(nal))
	zeros := 0
	for _, c := range nal {
		if zeros >= 2 && c == 3 {
			zeros = 0
			continue
		}
		out = append(out, c)
		if c == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}

// bitReader reads an RBSP bit by bit; reading past the end sets bad.
type bitReader struct {
	b   []byte
	pos int
	bad bool
}

func (r *bitReader) u(n int) uint32 {
	var v uint32
	for range n {
		if r.pos >= 8*len(r.b) {
			r.bad = true
			return 0
		}
		v = v<<1 | uint32(r.b[r.pos/8]>>(7-r.pos%8)&1)
		r.pos++
	}
	return v
}

func (r *bitReader) flag() bool { return r.u(1) == 1 }

func (r *bitReader) ue() uint32 {
	zeros := 0
	for !r.flag() {
		if r.bad || zeros > 31 {
			r.bad = true
			return 0
		}
		zeros++
	}
	return (1<<zeros - 1) + r.u(zeros)
}

func (r *bitReader) se() int32 {
	k := r.ue()
	if k&1 == 1 {
		return int32((k + 1) / 2)
	}
	return -int32(k / 2)
}

// sps is what the tests check of a sequence parameter set.
type sps struct {
	profile, constraints, level uint8
	widthMBs, heightMBs         uint32
	signal                      bool // video_signal_type_present_flag
	fullRange                   bool
	primaries, transfer, matrix uint8
	restriction                 bool // bitstream_restriction_flag
	maxReorder, maxDecBuffering uint32
	refFrames                   uint32 // max_num_ref_frames
}

// parseSPS reads a sequence parameter set NAL unit.
func parseSPS(t *testing.T, nal []byte) sps {
	t.Helper()
	b := unescape(nal)
	if len(b) < 4 || b[0]&0x1f != nalSPS {
		t.Fatalf("not an SPS: % x", nal)
	}
	s := sps{profile: b[1], constraints: b[2], level: b[3]}
	r := &bitReader{b: b[4:]}
	r.ue() // seq_parameter_set_id
	switch s.profile {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		chroma := r.ue()
		if chroma == 3 {
			r.u(1)
		}
		r.ue()
		r.ue()
		r.u(1)
		if r.flag() {
			n := 8
			if chroma == 3 {
				n = 12
			}
			for i := range n {
				if r.flag() {
					size := 16
					if i >= 6 {
						size = 64
					}
					last, next := int32(8), int32(8)
					for range size {
						if next != 0 {
							next = (last + r.se() + 256) % 256
						}
						if next != 0 {
							last = next
						}
					}
				}
			}
		}
	}
	r.ue() // log2_max_frame_num_minus4
	switch r.ue() {
	case 0:
		r.ue()
	case 1:
		r.u(1)
		r.se()
		r.se()
		for range r.ue() {
			r.se()
		}
	}
	s.refFrames = r.ue() // max_num_ref_frames
	r.u(1)
	s.widthMBs = r.ue() + 1
	s.heightMBs = r.ue() + 1
	if !r.flag() { // frame_mbs_only_flag
		r.u(1)
	}
	r.u(1)
	if r.flag() { // frame_cropping_flag
		r.ue()
		r.ue()
		r.ue()
		r.ue()
	}
	if r.flag() { // vui_parameters_present_flag
		parseVUI(r, &s)
	}
	if r.bad {
		t.Fatalf("the SPS ends too soon: % x", nal)
	}
	return s
}

func parseVUI(r *bitReader, s *sps) {
	if r.flag() { // aspect_ratio_info_present_flag
		if r.u(8) == 255 {
			r.u(16)
			r.u(16)
		}
	}
	if r.flag() { // overscan_info_present_flag
		r.u(1)
	}
	if s.signal = r.flag(); s.signal {
		r.u(3)
		s.fullRange = r.flag()
		if r.flag() {
			s.primaries, s.transfer, s.matrix = uint8(r.u(8)), uint8(r.u(8)), uint8(r.u(8))
		}
	}
	if r.flag() { // chroma_loc_info_present_flag
		r.ue()
		r.ue()
	}
	if r.flag() { // timing_info_present_flag
		r.u(32)
		r.u(32)
		r.u(1)
	}
	nal := r.flag()
	if nal {
		parseHRD(r)
	}
	vcl := r.flag()
	if vcl {
		parseHRD(r)
	}
	if nal || vcl {
		r.u(1)
	}
	r.u(1) // pic_struct_present_flag
	if s.restriction = r.flag(); s.restriction {
		r.u(1)
		r.ue()
		r.ue()
		r.ue()
		r.ue()
		s.maxReorder = r.ue()
		s.maxDecBuffering = r.ue()
	}
}

func parseHRD(r *bitReader) {
	n := r.ue()
	r.u(4)
	r.u(4)
	for range n + 1 {
		r.ue()
		r.ue()
		r.u(1)
	}
	r.u(20)
}

// sliceType returns the type (modulo 5) of a slice NAL unit.
func sliceType(t *testing.T, nal []byte) uint32 {
	t.Helper()
	r := &bitReader{b: unescape(nal)[1:]}
	r.ue() // first_mb_in_slice
	st := r.ue()
	if r.bad {
		t.Fatalf("the slice header ends too soon: % x", nal[:min(len(nal), 16)])
	}
	return st % 5
}

// bitWriter writes the syntax bitReader reads, for its tests.
type bitWriter struct {
	b []byte
	n int
}

func (w *bitWriter) u(n int, v uint32) {
	for i := n - 1; i >= 0; i-- {
		if w.n%8 == 0 {
			w.b = append(w.b, 0)
		}
		w.b[len(w.b)-1] |= byte(v>>i&1) << (7 - w.n%8)
		w.n++
	}
}

func (w *bitWriter) flag(f bool) {
	if f {
		w.u(1, 1)
	} else {
		w.u(1, 0)
	}
}

func (w *bitWriter) ue(v uint32) {
	x := v + 1
	bits := 0
	for y := x; y > 0; y >>= 1 {
		bits++
	}
	w.u(bits-1, 0)
	w.u(bits, x)
}

func (w *bitWriter) se(v int32) {
	if v > 0 {
		w.ue(uint32(2*v - 1))
	} else {
		w.ue(uint32(-2 * v))
	}
}

// escape adds emulation prevention bytes.
func escape(b []byte) []byte {
	var out []byte
	zeros := 0
	for _, c := range b {
		if zeros >= 2 && c <= 3 {
			out = append(out, 3)
			zeros = 0
		}
		out = append(out, c)
		if c == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}

func TestParseSPS(t *testing.T) {
	for _, tt := range []struct {
		name    string
		profile uint8
		scaling bool
		poc     uint32
		hrd     bool
		want    sps
	}{
		{"constrained high", 100, false, 2, false, sps{refFrames: 1, profile: 100, constraints: 0x0c, level: 42, widthMBs: 120, heightMBs: 68,
			signal: true, fullRange: true, primaries: 1, transfer: 1, matrix: 1, restriction: true, maxReorder: 0, maxDecBuffering: 1}},
		{"high with scaling lists and HRD", 100, true, 0, true, sps{refFrames: 1, profile: 100, level: 40, widthMBs: 80, heightMBs: 45,
			signal: true, primaries: 1, transfer: 1, matrix: 1, restriction: true, maxReorder: 2, maxDecBuffering: 4}},
		{"baseline, poc type 1, no restriction", 66, false, 1, false, sps{refFrames: 1, profile: 66, constraints: 0xc0, level: 31,
			widthMBs: 40, heightMBs: 30}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := &bitWriter{}
			w.u(8, 0x67)
			w.u(8, uint32(tt.want.profile))
			w.u(8, uint32(tt.want.constraints))
			w.u(8, uint32(tt.want.level))
			w.ue(0)
			if tt.profile == 100 {
				w.ue(1) // 4:2:0
				w.ue(0)
				w.ue(0)
				w.flag(false)
				w.flag(tt.scaling)
				if tt.scaling {
					for i := range 8 {
						w.flag(i%2 == 0)
						if i%2 == 0 {
							size := 16
							if i >= 6 {
								size = 64
							}
							w.se(5)
							for range size - 2 {
								w.se(-3)
							}
							w.se(-2)
						}
					}
				}
			}
			w.ue(0)
			w.ue(tt.poc)
			switch tt.poc {
			case 0:
				w.ue(2)
			case 1:
				w.flag(false)
				w.se(-1)
				w.se(2)
				w.ue(2)
				w.se(1)
				w.se(-1)
			}
			w.ue(1)
			w.flag(false)
			w.ue(tt.want.widthMBs - 1)
			w.ue(tt.want.heightMBs - 1)
			w.flag(true)
			w.flag(true)
			w.flag(tt.want.heightMBs == 68)
			if tt.want.heightMBs == 68 {
				w.ue(0)
				w.ue(0)
				w.ue(0)
				w.ue(4)
			}
			vui := tt.want.signal || tt.want.restriction
			w.flag(vui)
			if vui {
				w.flag(true)
				w.u(8, 255)
				w.u(16, 1)
				w.u(16, 1)
				w.flag(false)
				w.flag(tt.want.signal)
				if tt.want.signal {
					w.u(3, 5)
					w.flag(tt.want.fullRange)
					w.flag(true)
					w.u(8, uint32(tt.want.primaries))
					w.u(8, uint32(tt.want.transfer))
					w.u(8, uint32(tt.want.matrix))
				}
				w.flag(true)
				w.ue(0)
				w.ue(0)
				w.flag(true)
				w.u(32, 1)
				w.u(32, 120)
				w.flag(false)
				w.flag(tt.hrd)
				if tt.hrd {
					w.ue(1)
					w.u(4, 0)
					w.u(4, 0)
					for range 2 {
						w.ue(1000)
						w.ue(2000)
						w.flag(true)
					}
					w.u(20, 0xfffff)
				}
				w.flag(false)
				if tt.hrd {
					w.flag(false)
				}
				w.flag(false)
				w.flag(tt.want.restriction)
				if tt.want.restriction {
					w.flag(true)
					w.ue(2)
					w.ue(1)
					w.ue(16)
					w.ue(16)
					w.ue(tt.want.maxReorder)
					w.ue(tt.want.maxDecBuffering)
				}
			}
			w.u(1, 1) // rbsp_stop_one_bit
			nal := escape(w.b)
			got := parseSPS(t, nal)
			if got != tt.want {
				t.Errorf("parsed %+v\nwant   %+v", got, tt.want)
			}
		})
	}
}

func TestSliceType(t *testing.T) {
	for _, tt := range []struct {
		first, typ uint32
		want       uint32
	}{{0, 7, sliceI}, {0, 5, sliceP}, {12, 1, sliceB}, {300, 0, sliceP}, {0, 2, sliceI}} {
		w := &bitWriter{}
		w.u(8, 0x41)
		w.ue(tt.first)
		w.ue(tt.typ)
		w.ue(0)
		w.u(1, 1)
		if got := sliceType(t, escape(w.b)); got != tt.want {
			t.Errorf("first %d type %d: %d, want %d", tt.first, tt.typ, got, tt.want)
		}
	}
}

func TestSplitAnnexB(t *testing.T) {
	b := []byte{0, 0, 0, 1, 0x67, 1, 2, 0, 0, 1, 0x68, 3, 0, 0, 0, 1, 0x65, 0, 0, 3, 0, 9}
	got := splitAnnexB(b)
	want := [][]byte{{0x67, 1, 2}, {0x68, 3}, {0x65, 0, 0, 3, 0, 9}}
	if len(got) != len(want) {
		t.Fatalf("%d NAL units, want %d: % x", len(got), len(want), got)
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Errorf("NAL %d: % x, want % x", i, got[i], want[i])
		}
	}
	if u := unescape(got[2]); !bytes.Equal(u, []byte{0x65, 0, 0, 0, 9}) {
		t.Errorf("unescaped % x", u)
	}
}
