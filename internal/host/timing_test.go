package host

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/rdp"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// TestFrameTimes: each write is paired with the oldest frame sent, a frame
// that was not queued is forgotten, and a write queued before the oldest
// frame was sent (video of another path) is ignored.
func TestFrameTimes(t *testing.T) {
	var told []FrameTiming
	ft := &frameTimes{tell: func(f FrameTiming) { told = append(told, f) }}
	base := time.Now()
	early := time.Now() // queued before any frame was sent
	ft.written(rdp.VideoWrite{Queued: early, Written: early, Bytes: 1})
	frame := func(i int, key bool) *EncodedFrame {
		c := base.Add(time.Duration(i) * time.Millisecond)
		return &EncodedFrame{Key: key, Captured: c, Encoded: c.Add(time.Millisecond)}
	}
	ft.sending(frame(1, true))
	ft.written(rdp.VideoWrite{Queued: early, Written: time.Now(), Bytes: 2}) // another path's
	q1 := time.Now()
	ft.sending(frame(2, false))
	ft.notSent() // the queue was full
	ft.sending(frame(3, false))
	q3 := time.Now()
	w1 := time.Now()
	ft.written(rdp.VideoWrite{Queued: q1, Written: w1, Bytes: 100})
	ft.written(rdp.VideoWrite{Queued: q3, Written: w1.Add(time.Millisecond), Bytes: 50})
	ft.written(rdp.VideoWrite{Queued: q3, Written: w1.Add(time.Millisecond), Bytes: 7}) // none left
	want := []FrameTiming{
		{Captured: frame(1, true).Captured, Encoded: frame(1, true).Encoded, Queued: q1, Written: w1, Bytes: 100, Key: true},
		{Captured: frame(3, false).Captured, Encoded: frame(3, false).Encoded, Queued: q3, Written: w1.Add(time.Millisecond), Bytes: 50},
	}
	if len(told) != len(want) {
		t.Fatalf("told %+v", told)
	}
	for i := range want {
		if told[i] != want[i] {
			t.Errorf("frame %d: %+v, want %+v", i, told[i], want[i])
		}
	}
	// More frames than the ring holds: the oldest go.
	told = nil
	for i := range timingRing + 3 {
		ft.sending(frame(10+i, false))
	}
	now := time.Now()
	for range timingRing + 3 {
		ft.written(rdp.VideoWrite{Queued: now, Written: now})
	}
	if len(told) != timingRing || !told[0].Captured.Equal(frame(13, false).Captured) {
		t.Errorf("told %d frames, the first captured %v", len(told), told[0].Captured)
	}
}

// TestTimings: the percentiles of each stage, rounded up to the step,
// the frames without a capture time left out of the stages that need it,
// and Take starting anew.
func TestTimings(t *testing.T) {
	var tm Timings
	start := time.Now()
	for i := range 100 {
		c := start.Add(time.Duration(i) * 16 * time.Millisecond)
		f := FrameTiming{Captured: c, Encoded: c.Add(5 * time.Millisecond), Queued: c.Add(5*time.Millisecond + 50*time.Microsecond),
			Written: c.Add(time.Duration(6+i/10) * time.Millisecond), Bytes: 1000, Key: i == 0}
		if i == 99 {
			f.Written = c.Add(3 * time.Second) // beyond the histogram
		}
		tm.Add(f)
	}
	tm.Add(FrameTiming{Encoded: start, Queued: start, Written: start.Add(1600 * time.Millisecond), Bytes: 1000}) // no capture time
	s := tm.Take()
	if s.Frames != 101 || s.KeyFrames != 1 || s.NoCapture != 1 || s.Bytes != 101000 {
		t.Errorf("summary %+v", s)
	}
	if s.Encode != (Spread{P50: 5 * time.Millisecond, P95: 5 * time.Millisecond, P99: 5 * time.Millisecond, Max: 5 * time.Millisecond}) {
		t.Errorf("encode %+v", s.Encode)
	}
	if s.Host != (Spread{P50: 50 * time.Microsecond, P95: 50 * time.Microsecond, P99: 50 * time.Microsecond, Max: 50 * time.Microsecond}) {
		t.Errorf("host %+v", s.Host)
	}
	// Totals: 6 ms for frames 0-9, 7 ms for 10-19, ... 15 ms for 90-98, then 3 s.
	if s.Total.P50 != 10100*time.Microsecond || s.Total.P95 != 15100*time.Microsecond || s.Total.P99 != 15100*time.Microsecond ||
		s.Total.Max != 3*time.Second {
		t.Errorf("total %+v", s.Total)
	}
	if s.Span != 1600*time.Millisecond-start.Add(6*time.Millisecond).Sub(start) {
		t.Errorf("span %v", s.Span)
	}
	if again := tm.Take(); again.Frames != 0 || again.Total != (Spread{}) || again.Bitrate() != 0 || again.FPS() != 0 {
		t.Errorf("after Take: %+v", again)
	}
	n := testing.AllocsPerRun(100, func() { tm.Add(FrameTiming{Captured: start, Encoded: start, Queued: start, Written: start}) })
	if n != 0 {
		t.Errorf("%v allocations a frame", n)
	}
}

// TestSummaryRates: the bitrate and the frame rate over the span.
func TestSummaryRates(t *testing.T) {
	s := TimingSummary{Frames: 61, Bytes: 1_000_000, Span: time.Second}
	if s.Bitrate() != 8_000_000 || s.FPS() != 60 {
		t.Errorf("bitrate %d, fps %v", s.Bitrate(), s.FPS())
	}
}

// TestSessionTiming: a session on the H.264 path tells OnFrameTiming of
// every frame the client gets, in order, each written after it was
// queued, encoded and captured.
func TestSessionTiming(t *testing.T) {
	var mu sync.Mutex
	var told []FrameTiming
	video := &PatternVideo{}
	parts := Parts{Screen: &PatternScreen{Frames: 1}, Video: video}
	s := serve(t, parts, Options{OnFrameTiming: func(f FrameTiming) {
		mu.Lock()
		told = append(told, f)
		mu.Unlock()
	}}, 640, 480)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.c.KeepChannel(s.c.ChannelID("drdynvc"))
	g, err := s.c.StartGFX(ctx, rdptest.AVC420Caps())
	if err != nil {
		t.Fatal(err)
	}
	a := &avcClient{t: t, c: s.c, ch: g.ChannelID}
	got := a.run(500*time.Millisecond, 0)
	time.Sleep(50 * time.Millisecond) // the last frame's hook runs after its write
	mu.Lock()
	defer mu.Unlock()
	if len(told) < len(got) {
		t.Fatalf("told of %d frames, the client got %d", len(told), len(got))
	}
	for i, f := range told {
		if f.Captured.IsZero() || f.Encoded.Before(f.Captured) || f.Queued.Before(f.Encoded) || f.Written.Before(f.Queued) || f.Bytes == 0 {
			t.Fatalf("frame %d: %+v", i, f)
		}
		if i > 0 && f.Written.Before(told[i-1].Written) {
			t.Fatalf("frame %d written before frame %d", i, i-1)
		}
	}
	if !told[0].Key {
		t.Error("the first frame told is not a key frame")
	}
	var tm Timings
	for _, f := range told {
		tm.Add(f)
	}
	sum := tm.Take()
	t.Logf("%d frames: %+v", len(told), sum)
	if sum.Total.P95 > 50*time.Millisecond {
		t.Errorf("95%% of frames took up to %v from capture to written, on the loopback", sum.Total.P95)
	}
}
