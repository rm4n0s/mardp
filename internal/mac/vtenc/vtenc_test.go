package vtenc

import (
	"bytes"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/rm4n0s/errors"
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

// fakeVT is a backend that records what the encoder asks of its session.
type fakeVT struct {
	mu       sync.Mutex
	fail     *createFailure
	cfg      config
	enc      *Encoder
	bitrates []int
	refuse   int // the status setBitrate returns
	forced   int
	skip     bool
	closed   int
	// onClose runs in close, as VideoToolbox's last frames do.
	onClose func()
	// What the sharpening asks: pictures counts the pictures submitted,
	// isStale says the newest was skipped, repeats counts repeat calls
	// and repeatStatus is what they return.
	pictures     uint32
	isStale      bool
	repeats      int
	repeatStatus int
}

func (f *fakeVT) create(cfg config, enc *Encoder) (session, *createFailure) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	f.cfg, f.enc = cfg, enc
	return f, nil
}

func (f *fakeVT) setBitrate(bps int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse != 0 {
		return f.refuse
	}
	f.bitrates = append(f.bitrates, bps)
	return 0
}

func (f *fakeVT) forceKeyFrame() { f.mu.Lock(); f.forced++; f.mu.Unlock() }
func (f *fakeVT) setSkip(s bool) { f.mu.Lock(); f.skip = s; f.mu.Unlock() }
func (f *fakeVT) close() {
	if f.onClose != nil {
		f.onClose()
	}
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
}

func (f *fakeVT) repeat() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.repeatStatus == 0 {
		f.repeats++
	}
	return f.repeatStatus
}

func (f *fakeVT) captured() uint32 { f.mu.Lock(); defer f.mu.Unlock(); return f.pictures }
func (f *fakeVT) stale() bool      { f.mu.Lock(); defer f.mu.Unlock(); return f.isStale }

func (f *fakeVT) submitter() (fn, arg unsafe.Pointer) {
	return unsafe.Pointer(f), unsafe.Pointer(&f.forced)
}

func TestNewEncoder(t *testing.T) {
	frame := func(*Frame) {}
	for _, tt := range []struct {
		name  string
		opts  Options
		frame func(*Frame)
		fail  *createFailure
		want  config
		route string
	}{
		{name: "defaults", opts: Options{Width: 1920, Height: 1080}, frame: frame,
			want: config{width: 1920, height: 1080, bitrate: DefaultBitrate, fps: DefaultFPS}},
		{name: "everything set", opts: Options{Width: 1280, Height: 720, FullRange: true, Profile: ProfileConstrainedBaseline, Bitrate: 2e6, FPS: 30},
			frame: frame, want: config{width: 1280, height: 720, fullRange: true, profile: ProfileConstrainedBaseline, bitrate: 2e6, fps: 30}},
		{name: "no width", opts: Options{Height: 1080}, frame: frame, route: "newEncoder." + TagEncoderConfigInvalid},
		{name: "too tall", opts: Options{Width: 64, Height: 8193}, frame: frame, route: "newEncoder." + TagEncoderConfigInvalid},
		{name: "negative bitrate", opts: Options{Width: 64, Height: 64, Bitrate: -1}, frame: frame, route: "newEncoder." + TagEncoderConfigInvalid},
		{name: "negative fps", opts: Options{Width: 64, Height: 64, FPS: -1}, frame: frame, route: "newEncoder." + TagEncoderConfigInvalid},
		{name: "unknown profile", opts: Options{Width: 64, Height: 64, Profile: 3}, frame: frame, route: "newEncoder." + TagEncoderConfigInvalid},
		{name: "no frame function", opts: Options{Width: 64, Height: 64}, route: "newEncoder." + TagEncoderConfigInvalid},
		{name: "refused", opts: Options{Width: 64, Height: 64}, frame: frame, fail: &createFailure{step: "ProfileLevel", status: -12900},
			route: "newEncoder." + TagEncoderCreateFailed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeVT{fail: tt.fail}
			e, err := newEncoder(tt.opts, tt.frame, f)
			if tt.route != "" {
				if e != nil {
					t.Error("an encoder")
				}
				assertRoute(t, err, tt.route)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if f.cfg != tt.want || f.enc != e {
				t.Errorf("created %+v, want %+v", f.cfg, tt.want)
			}
		})
	}
}

func TestEncoderControls(t *testing.T) {
	f := &fakeVT{}
	e, err := newEncoder(Options{Width: 64, Height: 64}, func(*Frame) {}, f)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SetBitrate(3e6); err != nil {
		t.Fatal(err)
	}
	assertRoute(t, e.SetBitrate(0), "Encoder.SetBitrate."+TagEncoderBitrateInvalid)
	f.refuse = -12902
	assertRoute(t, e.SetBitrate(4e6), "Encoder.SetBitrate."+TagEncoderBitrateFailed)
	f.refuse = 0
	e.ForceKeyFrame()
	e.SetSkip(true)
	if fn, arg := e.Submitter(); fn != unsafe.Pointer(f) || arg != unsafe.Pointer(&f.forced) {
		t.Error("Submitter is not the session's")
	}
	if len(f.bitrates) != 1 || f.bitrates[0] != 3e6 || f.forced != 1 || !f.skip {
		t.Errorf("session got bitrates %v, %d forced, skip %v", f.bitrates, f.forced, f.skip)
	}
	e.Close()
	e.Close()
	if f.closed != 1 {
		t.Errorf("closed %d times", f.closed)
	}
	assertRoute(t, e.SetBitrate(5e6), "Encoder.SetBitrate."+TagEncoderClosed)
	e.ForceKeyFrame()
	e.SetSkip(false)
	if f.forced != 1 || !f.skip {
		t.Error("a closed encoder reached its session")
	}
}

// TestCloseCallsBack: the frames Close waits for may call the setters from
// the frame function, on another thread, as the H.264 path does; Close
// returns, and the setters do nothing.
func TestCloseCallsBack(t *testing.T) {
	f := &fakeVT{}
	e, err := newEncoder(Options{Width: 64, Height: 64}, func(*Frame) {}, f)
	if err != nil {
		t.Fatal(err)
	}
	f.onClose = func() {
		done := make(chan error, 1)
		go func() {
			e.ForceKeyFrame()
			e.SetSkip(true)
			done <- e.SetBitrate(2e6)
		}()
		select {
		case err := <-done:
			assertRoute(t, err, "Encoder.SetBitrate."+TagEncoderClosed)
		case <-time.After(5 * time.Second):
			t.Error("the setters wait for Close")
		}
	}
	e.Close()
	if f.closed != 1 || f.forced != 0 || f.skip {
		t.Errorf("closed %d times, %d forced, skip %v", f.closed, f.forced, f.skip)
	}
}

func TestDeliver(t *testing.T) {
	type got struct {
		data []byte
		key  bool
		pts  time.Duration
		ptr  *byte
	}
	var frames []got
	f := &fakeVT{}
	e, err := newEncoder(Options{Width: 64, Height: 64}, func(fr *Frame) {
		frames = append(frames, got{data: bytes.Clone(fr.Data), key: fr.Key, pts: fr.PTS, ptr: &fr.Data[0]})
	}, f)
	if err != nil {
		t.Fatal(err)
	}
	sps, pps := []byte{0x67, 0x64, 0x00, 0x2a}, []byte{0x68, 0xee}
	idr := []byte{0, 0, 0, 5, 0x65, 1, 2, 3, 4, 0, 0, 0, 2, 0x65, 9}
	p := []byte{0, 0, 0, 3, 0x41, 7, 7}
	e.deliver(output{key: true, sets: [][]byte{sps, pps}, nalLen: 4, avcc: idr, pts: 16 * time.Millisecond})
	e.deliver(output{nalLen: 4, avcc: p, pts: 33 * time.Millisecond})
	e.deliver(output{dropped: true, status: -1})
	e.deliver(output{status: -12911})
	e.deliver(output{nalLen: 4, avcc: []byte{0, 0, 0, 9, 0x41}})
	want := []got{
		{data: []byte{0, 0, 0, 1, 0x67, 0x64, 0x00, 0x2a, 0, 0, 0, 1, 0x68, 0xee, 0, 0, 0, 1, 0x65, 1, 2, 3, 4, 0, 0, 0, 1, 0x65, 9},
			key: true, pts: 16 * time.Millisecond},
		{data: []byte{0, 0, 0, 1, 0x41, 7, 7}, pts: 33 * time.Millisecond},
	}
	if len(frames) != len(want) {
		t.Fatalf("%d frames, want %d", len(frames), len(want))
	}
	for i, w := range want {
		g := frames[i]
		if !bytes.Equal(g.data, w.data) || g.key != w.key || g.pts != w.pts {
			t.Errorf("frame %d: % x key %v pts %v, want % x key %v pts %v", i, g.data, g.key, g.pts, w.data, w.key, w.pts)
		}
	}
	if frames[0].ptr != frames[1].ptr {
		t.Error("the second frame did not reuse the first one's buffer")
	}
	if s := e.Stats(); s != (Stats{Frames: 2, KeyFrames: 1, Dropped: 1, Failed: 2}) {
		t.Errorf("stats %+v", s)
	}
	e.frame = func(*Frame) {}
	allocs := testing.AllocsPerRun(100, func() {
		e.deliver(output{nalLen: 4, avcc: p})
	})
	if allocs != 0 {
		t.Errorf("%v allocations a frame", allocs)
	}
}

// TestDeliverTimes: a frame's capture time is its encode time less the age
// the C side measured on the host clock; an age that comes from another
// clock leaves it unknown.
func TestDeliverTimes(t *testing.T) {
	p := []byte{0, 0, 0, 3, 0x41, 7, 7}
	for _, tt := range []struct {
		age   time.Duration
		known bool
	}{
		{12 * time.Millisecond, true},
		{0, true},
		{-1, false},
		{-time.Second, false},
		{maxAge, false},
		{24 * time.Hour, false},
	} {
		t.Run(tt.age.String(), func(t *testing.T) {
			var got Frame
			e, err := newEncoder(Options{Width: 64, Height: 64}, func(fr *Frame) { got = *fr }, &fakeVT{})
			if err != nil {
				t.Fatal(err)
			}
			before := time.Now()
			e.deliver(output{nalLen: 4, avcc: p, age: tt.age})
			if got.Encoded.Before(before) || got.Encoded.After(time.Now()) {
				t.Errorf("encoded at %v, not during the call", got.Encoded)
			}
			switch {
			case !tt.known && !got.Captured.IsZero():
				t.Errorf("captured %v, want unknown", got.Captured)
			case tt.known && got.Encoded.Sub(got.Captured) != tt.age:
				t.Errorf("captured %v before encoded, want %v", got.Encoded.Sub(got.Captured), tt.age)
			}
		})
	}
}

func TestAppendAnnexB(t *testing.T) {
	for _, tt := range []struct {
		name   string
		nalLen int
		avcc   []byte
		want   []byte
		bad    bool
	}{
		{name: "lengths of 4", nalLen: 4, avcc: []byte{0, 0, 0, 2, 0x41, 1, 0, 0, 0, 1, 0x41}, want: []byte{0, 0, 0, 1, 0x41, 1, 0, 0, 0, 1, 0x41}},
		{name: "lengths of 2", nalLen: 2, avcc: []byte{0, 3, 0x65, 0, 3}, want: []byte{0, 0, 0, 1, 0x65, 0, 3}},
		{name: "lengths of 1", nalLen: 1, avcc: []byte{1, 0x41, 2, 0x41, 5}, want: []byte{0, 0, 0, 1, 0x41, 0, 0, 0, 1, 0x41, 5}},
		{name: "nothing", nalLen: 4},
		{name: "length size 3", nalLen: 3, avcc: []byte{0, 0, 1, 0x41}, bad: true},
		{name: "length cut short", nalLen: 4, avcc: []byte{0, 0, 0, 1, 0x41, 0, 0}, bad: true},
		{name: "past the end", nalLen: 4, avcc: []byte{0, 0, 0, 3, 0x41, 1}, bad: true},
		{name: "empty NAL unit", nalLen: 2, avcc: []byte{0, 0, 0, 1, 0x41}, bad: true},
		{name: "huge length", nalLen: 4, avcc: []byte{0xff, 0xff, 0xff, 0xff, 0x41}, bad: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := appendAnnexB(nil, nil, tt.nalLen, tt.avcc)
			if tt.bad {
				assertRoute(t, err, "appendAnnexB."+TagEncoderSampleInvalid)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("% x, want % x", got, tt.want)
			}
		})
	}
}

func TestProfileString(t *testing.T) {
	for p, want := range map[Profile]string{ProfileConstrainedHigh: "constrained-high", ProfileMain: "main",
		ProfileConstrainedBaseline: "constrained-baseline", 7: "unknown"} {
		if p.String() != want {
			t.Errorf("%d: %s, want %s", int(p), p, want)
		}
	}
}
