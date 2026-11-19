package aacenc

import (
	"slices"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/internal/host"
)

// assertRoute fails t unless err is an *errors.Error with route.
func assertRoute(t *testing.T, err error, route string) {
	t.Helper()
	e, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("expected *errors.Error, got %T: %v", err, err)
	}
	if !e.HasRoute(route) {
		t.Errorf("route %s, want %s", e.Route(), route)
	}
}

// chunk is what the fake converter was given at once: n frames, silent
// when every one is 0.
type chunk struct {
	n      int
	silent bool
}

// fakeConverter makes a packet of each PacketFrames frames it is given;
// a packet holds its number.
type fakeConverter struct {
	pending int
	packets int
	chunks  []chunk
	resets  int
	closes  int
	err     error
}

func (f *fakeConverter) encode(left, right []float32, out []byte, sizes []int32) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	silent := true
	for i := range left {
		if left[i] != 0 || right[i] != 0 {
			silent = false
		}
	}
	f.chunks = append(f.chunks, chunk{len(left), silent})
	f.pending += len(left)
	k := 0
	for ; f.pending >= PacketFrames; k++ {
		out[k] = byte(f.packets)
		sizes[k] = 1
		f.packets++
		f.pending -= PacketFrames
	}
	return k, nil
}

func (f *fakeConverter) reset() error {
	f.resets++
	f.pending = 0
	return nil
}

func (f *fakeConverter) close() { f.closes++ }

// buf is n frames of sound at t.
func buf(t time.Duration, n int) *host.SoundBuffer {
	b := &host.SoundBuffer{Left: make([]float32, n), Right: make([]float32, n), Time: t}
	for i := range n {
		b.Left[i], b.Right[i] = 0.5, -0.5
	}
	return b
}

// frames are the times of these numbers of frames.
func frames(n ...int) []time.Duration {
	ts := make([]time.Duration, len(n))
	for i, k := range n {
		ts[i] = frameTime(k)
	}
	return ts
}

func TestEncode(t *testing.T) {
	tests := []struct {
		name    string
		priming int
		bufs    []*host.SoundBuffer
		chunks  []chunk
		resets  int
		times   []time.Duration
	}{
		{"the delay", 2112, []*host.SoundBuffer{buf(0, 4096)}, []chunk{{4096, false}}, 0,
			frames(0, 0, 0, 960)},
		{"a start later than 0", 2112, []*host.SoundBuffer{buf(time.Second, 4096)}, []chunk{{4096, false}}, 0,
			[]time.Duration{time.Second - frameTime(2112), time.Second - frameTime(1088), time.Second - frameTime(64),
				time.Second + frameTime(960)}},
		{"chunks", 0, []*host.SoundBuffer{buf(0, 10000)}, []chunk{{4096, false}, {4096, false}, {1808, false}}, 0,
			frames(0, 1024, 2048, 3072, 4096, 5120, 6144, 7168, 8192)},
		{"jitter", 0, []*host.SoundBuffer{buf(0, 960), buf(25*time.Millisecond, 960)},
			[]chunk{{960, false}, {960, false}}, 0, frames(0)},
		{"a short gap", 0, []*host.SoundBuffer{buf(0, 960), buf(50*time.Millisecond, 960)},
			[]chunk{{960, false}, {1440, true}, {960, false}}, 0, frames(0, 1024, 2048)},
		{"the longest gap filled", 0, []*host.SoundBuffer{buf(0, 1024), buf(frameTime(1024+4096), 1024)},
			[]chunk{{1024, false}, {4096, true}, {1024, false}}, 0, frames(0, 1024, 2048, 3072, 4096, 5120)},
		{"a long gap", 0, []*host.SoundBuffer{buf(0, 1024), buf(time.Second, 1024)},
			[]chunk{{1024, false}, {4096, true}, {1024, false}}, 1,
			append(frames(0, 1024, 2048, 3072, 4096), time.Second)},
		{"back in time", 0, []*host.SoundBuffer{buf(0, 1024), buf(0, 1024)},
			[]chunk{{1024, false}, {4096, true}, {1024, false}}, 1, frames(0, 1024, 2048, 3072, 4096, 0)},
		{"nothing", 0, []*host.SoundBuffer{buf(0, 0), buf(time.Hour, 0)}, nil, 0, nil},
		{"channels of different lengths", 0, []*host.SoundBuffer{{Left: make([]float32, 2048),
			Right: make([]float32, 1500)}}, []chunk{{1500, true}}, 0, frames(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeConverter{}
			e := newEncoder(f, tt.priming)
			var times []time.Duration
			for _, b := range tt.bufs {
				err := e.Encode(b, func(data []byte, ts time.Duration) {
					if len(data) != 1 || int(data[0]) != len(times) {
						t.Errorf("packet %d is %v", len(times), data)
					}
					times = append(times, ts)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if !slices.Equal(f.chunks, tt.chunks) {
				t.Errorf("chunks %v, want %v", f.chunks, tt.chunks)
			}
			if f.resets != tt.resets {
				t.Errorf("%d resets, want %d", f.resets, tt.resets)
			}
			if !slices.Equal(times, tt.times) {
				t.Errorf("times %v, want %v", times, tt.times)
			}
		})
	}
}

func TestEncodeFails(t *testing.T) {
	f := &fakeConverter{err: errors.New(TagEncodeFailed, "encoding failed")}
	e := newEncoder(f, 0)
	err := e.Encode(buf(0, 960), func([]byte, time.Duration) { t.Error("a packet") })
	// The converter's error, untouched: its route is where it was made.
	assertRoute(t, err, "TestEncodeFails.AACEncodeFailed")
}

func TestClose(t *testing.T) {
	f := &fakeConverter{}
	e := newEncoder(f, 0)
	for range 2 {
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if f.closes != 1 {
		t.Errorf("closed %d times", f.closes)
	}
	err := e.Encode(buf(0, 960), func([]byte, time.Duration) { t.Error("a packet") })
	assertRoute(t, err, "Encoder.Encode.AACEncoderClosed")
}

func TestEncodeAllocs(t *testing.T) {
	e := newEncoder(&fakeConverter{chunks: make([]chunk, 0, 1000)}, 2112)
	b := buf(0, 480)
	n := 0
	allocs := testing.AllocsPerRun(200, func() {
		b.Time = frameTime(480 * n)
		if n%50 == 49 {
			b.Time += time.Second
		}
		n++
		if err := e.Encode(b, func([]byte, time.Duration) {}); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Errorf("%v allocations an Encode", allocs)
	}
}
