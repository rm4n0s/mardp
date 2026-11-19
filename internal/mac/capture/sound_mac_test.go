//go:build darwin && mardp_mac

package capture

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/internal/host"
)

// The tone the sound tests play from their own process. The owner's output
// is never turned up or unmuted: ScreenCaptureKit captures before the
// output's volume and mute (SPEC §19 R2).
const (
	testToneFreq  = 1000.0
	testToneLevel = 0.1
)

// heard is what a sound capture got.
type heard struct {
	buffers, frames int
	// tone is the tone's amplitude in the sound, from the Goertzel filter;
	// peak the largest sample.
	tone, peak float64
	// first and last are the times of the first and last buffers.
	first, last time.Duration
	backwards   bool
}

// listen captures with s for d and measures what it gets.
func listen(t *testing.T, s *Source, d time.Duration) heard {
	t.Helper()
	var mu sync.Mutex
	var h heard
	var re, im, n float64
	w := 2 * math.Pi * testToneFreq / host.SoundRate
	ctx, cancel := context.WithTimeout(t.Context(), d)
	defer cancel()
	err := (&Sound{src: s}).Capture(ctx, func(b *host.SoundBuffer) {
		mu.Lock()
		defer mu.Unlock()
		if len(b.Left) != len(b.Right) {
			t.Errorf("channels of %d and %d samples", len(b.Left), len(b.Right))
		}
		if h.buffers == 0 {
			h.first = b.Time
		} else if b.Time < h.last {
			h.backwards = true
		}
		h.buffers++
		h.last = b.Time
		for i, v := range b.Left {
			x := float64(v+b.Right[i]) / 2
			ph := w * float64(h.frames+i)
			re += x * math.Cos(ph)
			im += x * math.Sin(ph)
			h.peak = max(h.peak, math.Abs(float64(v)), math.Abs(float64(b.Right[i])))
		}
		h.frames += len(b.Left)
		n = float64(h.frames)
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if n > 0 {
		h.tone = 2 * math.Hypot(re, im) / n
	}
	t.Logf("%d buffers, %d frames, tone %.4f, peak %.4f, from %v to %v", h.buffers, h.frames, h.tone, h.peak, h.first, h.last)
	return h
}

// TestMacSound plays a tone from the test's own process: the capture gets
// it at the level played, 48 kHz in time, when the process's own sound is
// let in; not by default (mardp's own sound is left out); and not when the
// process's app is among the apps left out, as UTM is.
func TestMacSound(t *testing.T) {
	needScreen(t)
	w := openTestWindow() // an app is left out only while it has a window
	defer w.close()
	tone := playTestTone(testToneFreq, testToneLevel)
	if tone == nil {
		t.Skip("this process cannot play sound")
	}
	defer tone.stop()
	time.Sleep(300 * time.Millisecond)

	t.Run("own sound let in", func(t *testing.T) {
		s := newSource(Options{}, macOS{})
		s.includeSelf = true
		h := listen(t, s, 2*time.Second)
		if math.Abs(h.tone-testToneLevel) > 0.02 {
			t.Errorf("tone at %.4f, want %.2f", h.tone, testToneLevel)
		}
		if h.backwards {
			t.Error("a buffer's time went backwards")
		}
		// The sample rate: the frames span the time between the first
		// and the last buffer, give or take the last buffer.
		span := float64(h.last-h.first) / float64(time.Second)
		if rate := float64(h.frames) / span; span < 1 || rate < 46000 || rate > 52000 {
			t.Errorf("%d frames in %.2f s", h.frames, span)
		}
	})
	t.Run("own sound left out", func(t *testing.T) {
		h := listen(t, newSource(Options{}, macOS{}), 1500*time.Millisecond)
		if h.tone > 0.005 {
			t.Errorf("tone at %.4f, want none", h.tone)
		}
	})
	t.Run("own app left out", func(t *testing.T) {
		id := testBundleID()
		if id == "" {
			t.Skip("not run as an app (run it with scripts/mactest.sh)")
		}
		s := newSource(Options{ExcludeApps: []string{id}}, macOS{})
		s.includeSelf = true
		h := listen(t, s, 1500*time.Millisecond)
		if h.tone > 0.005 {
			t.Errorf("tone at %.4f, want none", h.tone)
		}
		if n := (macOS{}).excludedApps([]string{id}); n != 1 {
			t.Errorf("%d apps match %s, want 1", n, id)
		}
	})
}
