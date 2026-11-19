package host

import (
	"log/slog"
	"runtime"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/rdp"
)

// The allocations of the host's paths (SPEC §11 rule 2): an H.264 frame
// handed to the session, measured or not, and a captured sound buffer
// encoded and written allocate nothing. Each benchmark fails when its loop
// allocates; the tests say the same with testing.AllocsPerRun, in the
// default run.

// writingOut is a videoOut that queues nothing: each frame sent is
// "written" at once, telling the write hook as the session's writer does.
type writingOut struct {
	hook  func(rdp.VideoWrite)
	sent  int
	bytes int
}

func (o *writingOut) SendFrame(data []byte, _ bool) error {
	o.sent++
	o.bytes += len(data)
	if o.hook != nil {
		now := time.Now()
		o.hook(rdp.VideoWrite{Queued: now, Written: now, Bytes: len(data)})
	}
	return nil
}

func (o *writingOut) Backlog() rdp.Backlog                 { return rdp.Backlog{} }
func (o *writingOut) Path() rdp.VideoPath                  { return rdp.VideoAVC420 }
func (o *writingOut) SetWriteHook(fn func(rdp.VideoWrite)) { o.hook = fn }

// countingEncoder is a VideoEncoder that only counts.
type countingEncoder struct{ forced, skips int }

func (e *countingEncoder) SetBitrate(int) error { return nil }
func (e *countingEncoder) ForceKeyFrame()       { e.forced++ }
func (e *countingEncoder) SetSkip(bool)         { e.skips++ }

// countingSoundOut is a soundOut that only counts.
type countingSoundOut struct{ writes int }

func (o *countingSoundOut) Write([]byte, time.Duration) error { o.writes++; return nil }
func (o *countingSoundOut) Backlog() (int, time.Duration)     { return 1, 0 }

// avcFrames is an H.264 path with its encoder started, measured into tm
// when tm is set, and the frame it is handed.
func avcFrames(tm *Timings) (*avcPath, *writingOut, func()) {
	out := &writingOut{}
	a := &avcPath{out: out, log: slog.New(slog.DiscardHandler), rate: &rateControl{bitrate: StartBitrate, max: DefaultMaxBitrate},
		stop: func() {}, interval: time.Second / 60}
	if tm != nil {
		a.times = &frameTimes{tell: tm.Add}
		out.SetWriteHook(a.times.written)
	}
	a.started(&countingEncoder{})
	now := time.Now()
	f := &EncodedFrame{Data: make([]byte, 64<<10), Captured: now, Encoded: now}
	return a, out, func() { a.frame(f) }
}

// soundPackets is the sound path with the PCM encoder, and a captured
// buffer of 10 ms each call encodes and writes.
func soundPackets() (*soundPath, *countingSoundOut, func()) {
	out := &countingSoundOut{}
	p := &soundPath{out: out, enc: NewPCMEncoder(), log: slog.New(slog.DiscardHandler), stop: func() {}, length: PCMPacket}
	b := ramp(0, SoundRate/100, 0)
	return p, out, func() {
		b.Time += frameTime(len(b.Left))
		p.buffer(&b)
	}
}

func TestPathAllocs(t *testing.T) {
	var tm Timings
	tests := []struct {
		name string
		make func() (op func(), done func() int)
	}{
		{"H.264 frame", func() (func(), func() int) {
			_, out, op := avcFrames(nil)
			return op, func() int { return out.sent }
		}},
		{"H.264 frame measured", func() (func(), func() int) {
			_, out, op := avcFrames(&tm)
			return op, func() int { return out.sent }
		}},
		{"sound buffer", func() (func(), func() int) {
			_, out, op := soundPackets()
			return op, func() int { return out.writes }
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, done := tt.make()
			op()
			if n := testing.AllocsPerRun(200, op); n != 0 {
				t.Errorf("%v allocations each, want 0", n)
			}
			if done() < 100 {
				t.Errorf("%d sent", done())
			}
		})
	}
	if s := tm.Take(); s.Frames < 200 {
		t.Errorf("measured %d frames", s.Frames)
	}
}

// benchNoAllocs runs op in b's loop, after a warm-up, and fails b when
// the loop allocated, in any goroutine, more than once in a thousand
// operations: the runtime and the testing package allocate now and then
// by themselves, and an operation that allocates allocates every time.
func benchNoAllocs(b *testing.B, op func()) {
	b.Helper()
	for range 16 {
		op()
	}
	b.ReportAllocs()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	n := 0
	for b.Loop() {
		op()
		n++
	}
	runtime.ReadMemStats(&after)
	if m := after.Mallocs - before.Mallocs; m > uint64(n/1000) {
		b.Errorf("%d allocations in %d operations, want 0", m, n)
	}
}

// BenchmarkAVCFrame: an encoded frame handed to the session by the H.264
// path, with its flow decisions, measured as -measure does.
func BenchmarkAVCFrame(b *testing.B) {
	var tm Timings
	_, _, op := avcFrames(&tm)
	benchNoAllocs(b, op)
}

// BenchmarkSoundBuffer: 10 ms of captured sound encoded as PCM and written.
func BenchmarkSoundBuffer(b *testing.B) {
	_, _, op := soundPackets()
	benchNoAllocs(b, op)
}
