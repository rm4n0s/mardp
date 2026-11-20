package vtenc

import (
	"bytes"
	"log/slog"
	"testing"
	"time"
)

// frameOf is an encoder's output of n bytes: one NAL unit after its
// length.
func frameOf(n int, repeat, key bool) output {
	avcc := make([]byte, n)
	avcc[0], avcc[1], avcc[2], avcc[3] = byte((n-4)>>24), byte((n-4)>>16), byte((n-4)>>8), byte(n-4)
	avcc[4] = 1
	return output{avcc: avcc, nalLen: 4, repeat: repeat, key: key}
}

// refining makes an encoder on a fake session whose sharpening the test
// ticks by hand: starts counts how many times it started, and clock is
// its time.
func refining(t *testing.T, opts Options) (e *Encoder, f *fakeVT, starts *int, clock *time.Time, log *bytes.Buffer) {
	t.Helper()
	f = &fakeVT{}
	log = &bytes.Buffer{}
	opts.Width, opts.Height, opts.Bitrate, opts.FPS = 1920, 1080, 8_000_000, 60
	opts.Logger = slog.New(slog.NewTextHandler(log, nil))
	e, err := newEncoder(opts, func(*Frame) {}, f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	starts, clock = new(int), new(time.Time)
	*clock = time.Unix(1000, 0)
	e.ref.tick = func() { *starts++ }
	e.ref.now = func() time.Time { return *clock }
	return e, f, starts, clock, log
}

// ticks runs n ticks a tenth of a second apart and returns how many
// went before the sharpening ended, n when it did not.
func ticks(e *Encoder, clock *time.Time, n int) int {
	for i := range n {
		*clock = clock.Add(refineEvery)
		if !e.refineTick() {
			return i
		}
	}
	return n
}

func (f *fakeVT) picture() { f.mu.Lock(); f.pictures++; f.mu.Unlock() }

func (f *fakeVT) repeated() int { f.mu.Lock(); defer f.mu.Unlock(); return f.repeats }

func TestSmallFrame(t *testing.T) {
	for _, tt := range []struct{ w, h, want int }{{1920, 1080, 3049}, {1280, 720, 1924}, {3840, 2160, 9124}} {
		if got := smallFrame(tt.w, tt.h); got != tt.want {
			t.Errorf("smallFrame(%d, %d) = %d, want %d", tt.w, tt.h, got, tt.want)
		}
	}
}

// TestRefine: a picture that came out is encoded again a tenth of a
// second later, and again until three repeats in a row are small; then
// nothing runs.
func TestRefine(t *testing.T) {
	e, f, starts, clock, _ := refining(t, Options{})
	f.picture()
	e.deliver(frameOf(20_000, false, false))
	if *starts != 1 {
		t.Fatalf("started %d times", *starts)
	}
	// The first tick sees the new picture and leaves it to the capture.
	if !e.refineTick() || f.repeated() != 0 {
		t.Fatalf("a repeat in the tick that saw the picture: %d", f.repeated())
	}
	// Repeats that add to the picture do not end it.
	for i := range 4 {
		if ticks(e, clock, 1) != 1 || f.repeated() != i+1 {
			t.Fatalf("repeat %d: %d repeats", i, f.repeated())
		}
		e.deliver(frameOf(10_000, true, false))
	}
	// Two small ones and a larger: the run starts anew.
	for _, n := range []int{100, 100, 5000, 100, 100} {
		if ticks(e, clock, 1) != 1 {
			t.Fatal("ended in the middle of the run")
		}
		e.deliver(frameOf(n, true, false))
	}
	if ticks(e, clock, 1) != 1 {
		t.Fatal("ended after two small repeats")
	}
	e.deliver(frameOf(100, true, false))
	before := f.repeated()
	if ticks(e, clock, 1) != 0 || f.repeated() != before {
		t.Fatalf("not ended after three small repeats: %d more", f.repeated()-before)
	}
	if st := e.Stats(); st.Repeats != 10 || st.Frames != 11 {
		t.Errorf("stats %+v", st)
	}
	// A new picture starts it again.
	f.picture()
	e.deliver(frameOf(20_000, false, false))
	if *starts != 2 {
		t.Errorf("started %d times after the next picture", *starts)
	}
}

// TestRefineSpent: small repeats do not end the sharpening while the last
// four seconds took more than a second's bitrate: the rate control has
// nothing to spend, and the picture is still coarse.
func TestRefineSpent(t *testing.T) {
	e, f, _, clock, _ := refining(t, Options{})
	f.picture()
	e.deliver(frameOf(1_000_001, false, false)) // a second's bitrate is 1 000 000 bytes
	e.refineTick()
	for i := range 38 {
		if ticks(e, clock, 1) != 1 {
			t.Fatalf("ended after %d small repeats, %v after the large frame", i, clock.Sub(time.Unix(1000, 0)))
		}
		e.deliver(frameOf(100, true, false))
	}
	// Four seconds after the large frame it no longer counts.
	if n := ticks(e, clock, 5); n > 2 {
		t.Errorf("not ended %d ticks after the large frame left the window", n)
	}
}

// TestRefineBitrate: the bitrate the window is measured against follows
// SetBitrate.
func TestRefineBitrate(t *testing.T) {
	e, f, _, clock, _ := refining(t, Options{})
	f.picture()
	e.deliver(frameOf(300_000, false, false))
	if err := e.SetBitrate(2_000_000); err != nil { // a second is 250 000 bytes now
		t.Fatal(err)
	}
	e.refineTick()
	for range 5 {
		if ticks(e, clock, 1) != 1 {
			t.Fatal("ended although the window holds more than a second of 2 Mbit/s")
		}
		e.deliver(frameOf(100, true, false))
	}
}

// TestRefineNewPicture: while pictures come, none is repeated, and what
// was sharpened so far does not count for the next.
func TestRefineNewPicture(t *testing.T) {
	e, f, _, clock, _ := refining(t, Options{})
	f.picture()
	e.deliver(frameOf(20_000, false, false))
	e.refineTick()
	for range 2 {
		ticks(e, clock, 1)
		e.deliver(frameOf(100, true, false))
	}
	for range 5 {
		f.picture()
		before := f.repeated()
		if ticks(e, clock, 1) != 1 || f.repeated() != before {
			t.Fatal("a repeat while the screen changes")
		}
	}
	// The run of small repeats starts anew: three more are needed.
	for i := range 3 {
		if ticks(e, clock, 1) != 1 {
			t.Fatalf("ended after %d small repeats of the new picture", i)
		}
		e.deliver(frameOf(100, true, false))
	}
	if ticks(e, clock, 1) != 0 {
		t.Error("not ended after three small repeats of the new picture")
	}
}

// TestRefineEnds: the sharpening ends when the session skips, has no
// picture, refuses, or the picture was repeated for ten seconds; SetSkip
// and ForceKeyFrame start it again.
func TestRefineEnds(t *testing.T) {
	t.Run("skipping, and the newest picture skipped", func(t *testing.T) {
		e, f, starts, clock, _ := refining(t, Options{})
		f.picture()
		e.deliver(frameOf(20_000, false, false))
		e.refineTick()
		f.mu.Lock()
		f.repeatStatus, f.isStale = repeatSkipped, true
		f.mu.Unlock()
		if ticks(e, clock, 1) != 0 {
			t.Fatal("not ended while the session skips")
		}
		e.SetSkip(true)
		if *starts != 1 {
			t.Fatalf("SetSkip(true) started it")
		}
		e.SetSkip(false)
		if *starts != 2 {
			t.Fatalf("started %d times after SetSkip(false)", *starts)
		}
	})
	t.Run("skipping, what was sharpened counts", func(t *testing.T) {
		e, f, starts, clock, _ := refining(t, Options{})
		f.picture()
		e.deliver(frameOf(20_000, false, false))
		e.refineTick()
		for range 2 {
			ticks(e, clock, 1)
			e.deliver(frameOf(100, true, false))
		}
		f.mu.Lock()
		f.repeatStatus = repeatSkipped
		f.mu.Unlock()
		ticks(e, clock, 1)
		f.mu.Lock()
		f.repeatStatus = 0
		f.mu.Unlock()
		e.SetSkip(false)
		if *starts != 2 {
			t.Fatalf("started %d times", *starts)
		}
		// One more small repeat makes three.
		if ticks(e, clock, 1) != 1 {
			t.Fatal("ended before the third small repeat")
		}
		e.deliver(frameOf(100, true, false))
		if ticks(e, clock, 1) != 0 {
			t.Error("the two small repeats before the skip did not count")
		}
	})
	t.Run("no picture yet", func(t *testing.T) {
		e, f, starts, clock, _ := refining(t, Options{})
		f.repeatStatus = repeatNothing
		e.ForceKeyFrame()
		if *starts != 1 {
			t.Fatalf("ForceKeyFrame started it %d times", *starts)
		}
		if ticks(e, clock, 1) != 0 {
			t.Error("not ended without a picture")
		}
	})
	t.Run("refused", func(t *testing.T) {
		e, f, _, clock, log := refining(t, Options{})
		f.picture()
		e.deliver(frameOf(20_000, false, false))
		e.refineTick()
		f.mu.Lock()
		f.repeatStatus = -12345
		f.mu.Unlock()
		if ticks(e, clock, 1) != 0 || !bytes.Contains(log.Bytes(), []byte("refused to encode the still picture again")) {
			t.Errorf("not ended, or not logged: %s", log)
		}
	})
	t.Run("ten seconds", func(t *testing.T) {
		e, f, _, clock, _ := refining(t, Options{})
		f.picture()
		e.deliver(frameOf(20_000, false, false))
		e.refineTick()
		n := 0
		for ; n < 200 && ticks(e, clock, 1) == 1; n++ {
			e.deliver(frameOf(10_000, true, false))
		}
		if n != int(maxRefine/refineEvery) {
			t.Errorf("ended after %d repeats, want %d", n, int(maxRefine/refineEvery))
		}
	})
	t.Run("closed", func(t *testing.T) {
		e, f, _, clock, _ := refining(t, Options{})
		f.picture()
		e.deliver(frameOf(20_000, false, false))
		e.Close()
		if ticks(e, clock, 1) != 0 || f.repeated() != 0 {
			t.Error("the closed encoder's session was used")
		}
	})
}

// TestRefineKeyFrame: a forced key frame comes with a repeat on a still
// screen, starts the sharpening over, and is not a small repeat itself.
func TestRefineKeyFrame(t *testing.T) {
	e, f, starts, clock, _ := refining(t, Options{})
	f.picture()
	e.deliver(frameOf(20_000, false, false))
	e.refineTick()
	for range 3 {
		ticks(e, clock, 1)
		e.deliver(frameOf(100, true, false))
	}
	if ticks(e, clock, 1) != 0 {
		t.Fatal("not ended")
	}
	e.ForceKeyFrame()
	if *starts != 2 {
		t.Fatalf("started %d times", *starts)
	}
	before := f.repeated()
	if ticks(e, clock, 1) != 1 || f.repeated() != before+1 {
		t.Fatal("no repeat for the key frame")
	}
	e.deliver(frameOf(100, true, true))
	for i := range 3 {
		if ticks(e, clock, 1) != 1 {
			t.Fatalf("ended %d repeats after the key frame", i)
		}
		e.deliver(frameOf(100, true, false))
	}
	if ticks(e, clock, 1) != 0 {
		t.Error("not ended after three small repeats")
	}
}

// TestRefineDropped: a frame VideoToolbox dropped did not reach the
// client: its picture is encoded again.
func TestRefineDropped(t *testing.T) {
	e, f, starts, clock, _ := refining(t, Options{})
	f.picture()
	e.deliver(output{dropped: true})
	if *starts != 1 {
		t.Fatalf("started %d times", *starts)
	}
	e.refineTick()
	if ticks(e, clock, 1) != 1 || f.repeated() != 1 {
		t.Errorf("%d repeats of the dropped picture", f.repeated())
	}
}

// TestNoRefine: with NoRefine nothing is ever repeated.
func TestNoRefine(t *testing.T) {
	e, f, starts, _, _ := refining(t, Options{NoRefine: true})
	f.picture()
	e.deliver(frameOf(20_000, false, false))
	e.ForceKeyFrame()
	e.SetSkip(false)
	e.deliver(output{dropped: true})
	if *starts != 0 {
		t.Errorf("started %d times", *starts)
	}
}
