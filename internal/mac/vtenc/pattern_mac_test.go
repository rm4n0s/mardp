//go:build darwin && mardp_mac

package vtenc

import (
	"testing"
	"time"
)

// TestMacPattern: the test pattern of `mardp -test-pattern` encodes at
// 1080p and 60 frames a second, the first frame a key frame, each frame
// with its capture time on the host clock, a few milliseconds before it
// came out.
func TestMacPattern(t *testing.T) {
	type out struct {
		key               bool
		size              int
		captured, encoded time.Time
	}
	ch := make(chan out, 256)
	e, err := New(Options{Width: 1920, Height: 1080, FullRange: true, FPS: 60, Bitrate: 8_000_000}, func(f *Frame) {
		ch <- out{key: f.Key, size: len(f.Data), captured: f.Captured, encoded: f.Encoded}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	const frames = 120
	start := time.Now()
	tick := time.NewTicker(time.Second / 60)
	defer tick.Stop()
	for i := range frames {
		if st := e.SubmitPattern(uint32(i)); st != 0 {
			t.Fatalf("frame %d: status %d", i, st)
		}
		<-tick.C
	}
	var got []out
	for len(got) < frames {
		select {
		case o := <-ch:
			got = append(got, o)
		case <-time.After(2 * time.Second):
			t.Fatalf("%d frames came out of %d: %+v", len(got), frames, e.Stats())
		}
	}
	var worst, sum time.Duration
	bytes := 0
	for i, o := range got {
		if o.captured.IsZero() || o.captured.Before(start) {
			t.Fatalf("frame %d: captured %v, the test started %v", i, o.captured, start)
		}
		age := o.encoded.Sub(o.captured)
		worst, sum, bytes = max(worst, age), sum+age, bytes+o.size
	}
	t.Logf("%d frames, %d bytes (%.1f Mbit/s), from capture to encoded: mean %v, worst %v", frames, bytes,
		float64(bytes)*8/(float64(frames)/60)/1e6, sum/frames, worst)
	if !got[0].key {
		t.Error("the first frame is not a key frame")
	}
	if worst > 100*time.Millisecond {
		t.Errorf("a frame took %v from capture to encoded", worst)
	}
}
