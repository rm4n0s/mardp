package rdp

import (
	"image"
	"math/rand/v2"
	"runtime"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/rdp/internal/gfx"
)

// The allocations of the frame paths (SPEC §11 rule 2): an H.264 frame, a
// frame of tiles and a sound packet allocate nothing once the session's
// packets have grown to their size.

// avcSession is a session on the H.264 path whose writer writes to sig:
// the graphics pipeline's channel is open and ready, and has had its key
// frame. The write hook, when set, is the measurement's (§15.5).
func avcSession(sig signal, hook bool) (*Session, func()) {
	wr := newWriter(sig, func(error) {})
	go wr.run()
	s := &Session{wr: wr, width: 1920, height: 1080, client: Client{FastPathOutput: true, MaxRequestSize: 0x3f0000}}
	s.video.s = s
	m := &dvcManager{s: s, ch: &staticChannel{name: "drdynvc", id: 1004}, ready: true, version: 3, channels: map[uint32]*dvc{}}
	d := &dvc{m: m, id: 1, name: gfxChannelName, state: dvcOpen}
	m.channels[d.id] = d
	s.gfx = &gfxPipeline{s: s, now: time.Now, next: 1, d: d, ready: true, avc420: true, keyed: true}
	if hook {
		var n int
		s.Video().SetWriteHook(func(w VideoWrite) { n += w.Bytes })
	}
	return s, wr.halt
}

// sendAcked sends frame and has the client acknowledge it once written.
func sendAcked(s *Session, sig signal, frame []byte) error {
	id := s.gfx.next
	if err := s.Video().SendFrame(frame, false); err != nil {
		return err
	}
	<-sig
	s.gfx.ack(gfx.PDU{Cmd: gfx.CmdFrameAcknowledge, FrameID: id})
	return nil
}

// TestSendFrameAllocs: an H.264 frame, framed for the graphics pipeline,
// queued, written and acknowledged, allocates nothing, with the write hook
// set or not.
func TestSendFrameAllocs(t *testing.T) {
	frame := annexB(rand.New(rand.NewPCG(1, 2)), 64<<10)
	for _, hook := range []bool{false, true} {
		sig := make(signal)
		s, halt := avcSession(sig, hook)
		var err error
		for range videoDepth + 1 {
			if err = sendAcked(s, sig, frame); err != nil {
				t.Fatal(err)
			}
		}
		n := testing.AllocsPerRun(100, func() {
			if e := sendAcked(s, sig, frame); e != nil {
				err = e
			}
		})
		halt()
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("write hook %v: %v allocations per frame, want 0", hook, n)
		}
	}
}

// benchNoAllocs runs op in b's loop, after a warm-up in which the
// session's packets grow to the size op needs, and fails b when the loop
// allocated, in any goroutine, more than once in a thousand operations: the
// runtime and the testing package allocate now and then by themselves
// (about once in 100 000 operations here), and an operation that allocates
// allocates every time.
func benchNoAllocs(b *testing.B, op func() error) {
	b.Helper()
	for range 2 * controlDepth {
		if err := op(); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	n := 0
	for b.Loop() {
		if err := op(); err != nil {
			b.Fatal(err)
		}
		n++
	}
	runtime.ReadMemStats(&after)
	if m := after.Mallocs - before.Mallocs; m > uint64(n/1000) {
		b.Errorf("%d allocations in %d operations, want 0", m, n)
	}
}

// BenchmarkSendFrame: an H.264 frame sent, written and acknowledged, with
// the measurement's write hook.
func BenchmarkSendFrame(b *testing.B) {
	for _, size := range []int{4 << 10, 64 << 10, 512 << 10} {
		b.Run(byteSize(size), func(b *testing.B) {
			frame := annexB(rand.New(rand.NewPCG(3, 4)), size)
			sig := make(signal)
			s, halt := avcSession(sig, true)
			defer halt()
			b.SetBytes(int64(size))
			benchNoAllocs(b, func() error { return sendAcked(s, sig, frame) })
		})
	}
}

// BenchmarkSendTiles: a frame of tiles on the graphics pipeline (a
// client without H.264), sent, written and acknowledged.
func BenchmarkSendTiles(b *testing.B) {
	sig := make(signal)
	s, halt := avcSession(sig, false)
	defer halt()
	s.gfx.avc420 = false
	img := testImage(rand.New(rand.NewPCG(5, 6)), 1920, 1080)
	rects := []image.Rectangle{image.Rect(0, 0, 640, 360), image.Rect(1000, 500, 1064, 564)}
	b.SetBytes(4 * (640*360 + 64*64))
	benchNoAllocs(b, func() error {
		id := s.gfx.next
		if err := s.Video().SendRects(img, rects); err != nil {
			return err
		}
		<-sig
		s.gfx.ack(gfx.PDU{Cmd: gfx.CmdFrameAcknowledge, FrameID: id})
		return nil
	})
}

// BenchmarkAudioWrite: a sound packet of 20 ms of PCM, or of an AAC
// frame's size, written on the sound channel.
func BenchmarkAudioWrite(b *testing.B) {
	for _, size := range []int{3840, 400} {
		b.Run(byteSize(size), func(b *testing.B) {
			sig := make(signal)
			wr := newWriter(sig, func(error) {})
			go wr.run()
			defer wr.halt()
			s := &Session{wr: wr, client: Client{FastPathOutput: true, MaxRequestSize: 0x3f0000}}
			a := s.Audio()
			a.s, a.ch, a.state, a.version = s, &staticChannel{name: "rdpsnd", id: 1004}, audioReady, 8
			data := make([]byte, size)
			b.SetBytes(int64(size))
			benchNoAllocs(b, func() error {
				if err := a.Write(data, time.Second); err != nil {
					return err
				}
				<-sig
				return nil
			})
		})
	}
}
