//go:build darwin && mardp_mac

package vtenc

import (
	"bytes"
	"testing"
	"time"
)

// These tests use VideoToolbox's hardware encoder with generated NV12
// frames; they need no permission.

// encoded is one frame the encoder delivered.
type encoded struct {
	data []byte
	key  bool
	pts  time.Duration
}

// testEncoder makes an encoder whose frames go to the returned channel.
func testEncoder(t *testing.T, opts Options) (*Encoder, chan encoded) {
	t.Helper()
	ch := make(chan encoded, 1024)
	// The tests count the frames they submit.
	opts.NoRefine = true
	e, err := New(opts, func(f *Frame) {
		ch <- encoded{data: bytes.Clone(f.Data), key: f.Key, pts: f.PTS}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e, ch
}

// frameTime is the time between frames the tests give: 30 a second.
const frameTime = time.Second / 30

// What became of a frame submitted.
type fate int

const (
	fateEncoded fate = iota
	fateSkipped
	fateDropped
)

// submit submits frame i and waits for what became of it.
func submit(t *testing.T, e *Encoder, ch chan encoded, i int, noise bool) (encoded, fate) {
	t.Helper()
	pts := time.Duration(i) * frameTime
	dropped := e.Stats().Dropped
	switch st := e.encodeTest(uint32(i), noise, pts); st {
	case 0:
	case 1:
		select {
		case f := <-ch:
			t.Fatalf("frame %d was skipped, yet a frame came out (pts %v)", i, f.pts)
		case <-time.After(50 * time.Millisecond):
		}
		return encoded{}, fateSkipped
	default:
		t.Fatalf("frame %d: status %d", i, st)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case f := <-ch:
			if f.pts != pts {
				t.Fatalf("frame %d came out with pts %v, want %v (frames out of order)", i, f.pts, pts)
			}
			return f, fateEncoded
		case <-time.After(time.Millisecond):
		}
		if e.Stats().Dropped != dropped {
			return encoded{}, fateDropped
		}
	}
	t.Fatalf("frame %d did not come out", i)
	return encoded{}, fateDropped
}

// encodeOne submits frame i and waits for it; ok is false when the
// encoder skipped it. A dropped frame fails t.
func encodeOne(t *testing.T, e *Encoder, ch chan encoded, i int, noise bool) (encoded, bool) {
	t.Helper()
	f, what := submit(t, e, ch, i, noise)
	if what == fateDropped {
		t.Fatalf("frame %d was dropped", i)
	}
	return f, what == fateEncoded
}

// nalTypes returns the NAL unit types of an access unit and checks that
// its slices are I on a key frame and P otherwise: never B.
func nalTypes(t *testing.T, f encoded) []byte {
	t.Helper()
	var types []byte
	for _, nal := range splitAnnexB(f.data) {
		if len(nal) == 0 {
			t.Fatal("an empty NAL unit")
		}
		typ := nal[0] & 0x1f
		types = append(types, typ)
		if typ == nalSlice || typ == nalIDR {
			st := sliceType(t, nal)
			if st == sliceB {
				t.Fatalf("a B slice (pts %v)", f.pts)
			}
			if (typ == nalIDR) != (st == sliceI) && st != sliceI {
				t.Errorf("NAL type %d with slice type %d (pts %v)", typ, st, f.pts)
			}
		}
	}
	return types
}

// checkKey checks that f is a key frame: SPS, PPS and IDR slices; it
// returns the SPS.
func checkKey(t *testing.T, f encoded) sps {
	t.Helper()
	if !f.key {
		t.Fatalf("frame at %v is not a key frame", f.pts)
	}
	types := nalTypes(t, f)
	if len(types) < 3 || types[0] != nalSPS || types[1] != nalPPS {
		t.Fatalf("key frame NAL types %v, want SPS, PPS, IDR...", types)
	}
	for _, typ := range types[2:] {
		if typ != nalIDR && typ != 6 && typ != 9 {
			t.Fatalf("key frame NAL types %v", types)
		}
	}
	return parseSPS(t, splitAnnexB(f.data)[0])
}

// checkDelta checks that f is a P-frame without parameter sets.
func checkDelta(t *testing.T, f encoded) {
	t.Helper()
	if f.key {
		t.Fatalf("frame at %v is a key frame", f.pts)
	}
	for _, typ := range nalTypes(t, f) {
		if typ == nalSPS || typ == nalPPS || typ == nalIDR {
			t.Fatalf("a P-frame with NAL type %d (pts %v)", typ, f.pts)
		}
	}
}

func TestMacStream(t *testing.T) {
	for _, tt := range []struct {
		name        string
		opts        Options
		profile     uint8
		constraints uint8 // the constraint flags that must be set
	}{
		{"constrained high, video range", Options{Width: 1280, Height: 720}, 100, 0x0c},
		{"constrained high, full range", Options{Width: 1920, Height: 1080, FullRange: true}, 100, 0x0c},
		{"main", Options{Width: 1280, Height: 720, Profile: ProfileMain}, 77, 0},
		{"constrained baseline", Options{Width: 1280, Height: 720, Profile: ProfileConstrainedBaseline}, 66, 0x40},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e, ch := testEncoder(t, tt.opts)
			start := time.Now()
			first, _ := encodeOne(t, e, ch, 0, false)
			t.Logf("first frame: %d bytes in %v", len(first.data), time.Since(start))
			s := checkKey(t, first)
			t.Logf("SPS %+v", s)
			if s.profile != tt.profile || s.constraints&tt.constraints != tt.constraints {
				t.Errorf("profile_idc %d, constraint flags %#02x; want %d with %#02x", s.profile, s.constraints, tt.profile, tt.constraints)
			}
			if tt.constraints == 0x0c && s.constraints&0x0c != 0x0c {
				t.Error("not Constrained High")
			}
			if w, h := int(s.widthMBs)*16, int(s.heightMBs)*16; w != (tt.opts.Width+15)&^15 || h != (tt.opts.Height+15)&^15 {
				t.Errorf("SPS size %d×%d, want %d×%d", w, h, tt.opts.Width, tt.opts.Height)
			}
			if !s.restriction || s.maxReorder != 0 {
				t.Errorf("bitstream_restriction %v, max_num_reorder_frames %d; want 0", s.restriction, s.maxReorder)
			}
			if !s.signal || s.fullRange != tt.opts.FullRange || s.primaries != 1 || s.transfer != 1 || s.matrix != 1 {
				t.Errorf("video signal %v, full range %v, primaries %d, transfer %d, matrix %d; want BT.709 and full range %v",
					s.signal, s.fullRange, s.primaries, s.transfer, s.matrix, tt.opts.FullRange)
			}
			var total time.Duration
			i := 1
			for ; i < 20; i++ {
				t0 := time.Now()
				f, _ := encodeOne(t, e, ch, i, false)
				total += time.Since(t0)
				checkDelta(t, f)
			}
			t.Logf("P-frames: %v a frame on average", total/19)
			// A forced key frame, with the parameter sets again.
			e.ForceKeyFrame()
			f, _ := encodeOne(t, e, ch, i, false)
			if s2 := checkKey(t, f); s2 != s {
				t.Errorf("the SPS changed: %+v", s2)
			}
			i++
			for end := i + 5; i < end; i++ {
				f, _ := encodeOne(t, e, ch, i, false)
				checkDelta(t, f)
			}
			// Skipped frames encode nothing; a key frame forced meanwhile
			// waits for the first frame encoded.
			e.SetSkip(true)
			e.ForceKeyFrame()
			for end := i + 3; i < end; i++ {
				if _, ok := encodeOne(t, e, ch, i, false); ok {
					t.Fatalf("frame %d was encoded while skipping", i)
				}
			}
			e.SetSkip(false)
			f, _ = encodeOne(t, e, ch, i, false)
			checkKey(t, f)
			i++
			f, _ = encodeOne(t, e, ch, i, false)
			checkDelta(t, f)
			e.Close()
			if st := e.Stats(); st.Frames != 28 || st.KeyFrames != 3 || st.Dropped != 0 || st.Failed != 0 {
				t.Errorf("stats %+v", st)
			}
			select {
			case f := <-ch:
				t.Errorf("a frame after the last one (pts %v)", f.pts)
			default:
			}
		})
	}
}

func TestMacBitrate(t *testing.T) {
	e, ch := testEncoder(t, Options{Width: 1280, Height: 720, Bitrate: 2_000_000, FPS: 30})
	i := 0
	// measure encodes n frames of noise and returns the bitrate of the
	// last half.
	measure := func(n int) (float64, int) {
		bits, drops := 0, 0
		for k := range n {
			f, what := submit(t, e, ch, i, true)
			i++
			if what != fateEncoded {
				drops++
				continue
			}
			if i == 1 {
				checkKey(t, f)
			} else {
				checkDelta(t, f)
			}
			if k >= n/2 {
				bits += 8 * len(f.data)
			}
		}
		return float64(bits) / (float64(n-n/2) * frameTime.Seconds()), drops
	}
	// The bitrate rises, and falls by a quarter at a time, as the rate
	// control of SPEC §9.5 moves it; the stream follows each step without
	// dropping a frame or a new key frame.
	for k, bitrate := range []int{2_000_000, 8_000_000, 6_000_000, 4_500_000, 3_375_000, 6_000_000} {
		if k > 0 {
			if err := e.SetBitrate(bitrate); err != nil {
				t.Fatal(err)
			}
		}
		got, drops := measure(90)
		t.Logf("bitrate %d: %.2f Mbit/s, %d frames dropped", bitrate, got/1e6, drops)
		if r := got / float64(bitrate); r < 0.75 || r > 1.25 {
			t.Errorf("bitrate %d: the stream ran at %.2f Mbit/s (%.2f×)", bitrate, got/1e6, r)
		}
		if drops != 0 {
			t.Errorf("bitrate %d: %d frames dropped", bitrate, drops)
		}
	}
	// A fall to an eighth at once makes VideoToolbox drop frames until its
	// buffer drains: the reason §9.5 falls by a quarter. Logged, not
	// asserted.
	if err := e.SetBitrate(750_000); err != nil {
		t.Fatal(err)
	}
	got, drops := measure(90)
	t.Logf("a fall to 750000 at once: %.2f Mbit/s, %d of 90 frames dropped", got/1e6, drops)
	if st := e.Stats(); st.KeyFrames != 1 || st.Failed != 0 {
		t.Errorf("stats %+v: a new bitrate restarted the stream or failed frames", st)
	}
}
