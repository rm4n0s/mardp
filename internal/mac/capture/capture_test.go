package capture

import (
	"bytes"
	"context"
	"image"
	"log/slog"
	"slices"
	"sync"
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

// fakeMac is a backend whose displays and streams the test decides.
type fakeMac struct {
	mu       sync.Mutex
	denied   bool
	displays map[string]uint32 // UUID → display ID
	main     string
	dead     map[uint32]bool
	fails    []*startFailure // the next starts fail with these
	started  []uint32
	live     int
	streams  chan *fakeStream
}

func newFakeMac(main string, displays map[string]uint32) *fakeMac {
	return &fakeMac{displays: displays, main: main, dead: map[uint32]bool{}, streams: make(chan *fakeStream, 16)}
}

func (f *fakeMac) granted() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.denied
}

func (f *fakeMac) find(uuid string) (rawDisplay, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if uuid == "" {
		uuid = f.main
	}
	id, ok := f.displays[uuid]
	if !ok {
		return rawDisplay{}, false
	}
	return rawDisplay{id: id, info: host.Display{ID: uuid, Width: 1920, Height: 1080, PointWidth: 1920, PointHeight: 1080, Main: uuid == f.main}}, true
}

func (f *fakeMac) alive(id uint32, uuid string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.dead[id] && f.displays[uuid] == id
}

func (f *fakeMac) start(id uint32, cfg host.ScreenConfig, sk *sink) (stream, *startFailure) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.fails) > 0 {
		fail := f.fails[0]
		f.fails = f.fails[1:]
		return nil, fail
	}
	f.started = append(f.started, id)
	f.live++
	st := &fakeStream{f: f, id: id, cfg: cfg, sk: sk}
	f.streams <- st
	return st, nil
}

func (f *fakeMac) set(fn func(f *fakeMac)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeMac) state() (started []uint32, live int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.started), f.live
}

// fakeStream is a stream of fakeMac.
type fakeStream struct {
	f       *fakeMac
	id      uint32
	cfg     host.ScreenConfig
	sk      *sink
	stopped bool
}

func (s *fakeStream) stop() {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	if !s.stopped {
		s.stopped = true
		s.f.live--
	}
}

// next waits for the next stream started.
func (f *fakeMac) next(t *testing.T) *fakeStream {
	t.Helper()
	select {
	case st := <-f.streams:
		return st
	case <-time.After(5 * time.Second):
		t.Fatal("no stream started")
		return nil
	}
}

// running starts Capture on a fake with short waits; done gets its result.
func running(t *testing.T, f *fakeMac, opts Options, frame func(*host.Frame)) (s *Source, cancel func(), done chan error) {
	t.Helper()
	s = newSource(opts, f)
	s.wait, s.retry = 20*time.Millisecond, time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	done = make(chan error, 1)
	if frame == nil {
		frame = func(*host.Frame) {}
	}
	go func() { done <- s.Capture(ctx, host.ScreenConfig{Width: 64, Height: 32, MaxFPS: 30}, frame) }()
	t.Cleanup(cancel)
	return s, cancel, done
}

func wait(t *testing.T, done chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Capture did not return")
		return nil
	}
}

// pixels is a 64×32 frame with stride 4*64+16.
func pixels() ([]byte, int) {
	stride := 4*64 + 16
	return make([]byte, stride*32), stride
}

func TestDeliver(t *testing.T) {
	var got [][]image.Rectangle
	calls := 0
	s := newSource(Options{}, newFakeMac("M", nil))
	sk := &sink{src: s, width: 64, height: 32, frame: func(f *host.Frame) {
		calls++
		if f.Image.Width != 64 || f.Image.Height != 32 || f.Image.Stride != 4*64+16 {
			t.Errorf("image %dx%d stride %d", f.Image.Width, f.Image.Height, f.Image.Stride)
		}
		got = append(got, slices.Clone(f.Dirty))
	}}
	pix, stride := pixels()
	r := func(xywh ...int32) []int32 { return xywh }
	steps := []struct {
		name  string
		setup func()
		w, h  int
		rects []int32
		want  []image.Rectangle // nil: whole; "none" is checked by calls
		call  bool
	}{
		{"first is whole", func() { sk.whole.Store(true) }, 64, 32, r(1, 1, 2, 2), nil, true},
		{"rectangles", nil, 64, 32, r(1, 2, 3, 4, 60, 30, 10, 10), []image.Rectangle{image.Rect(1, 2, 4, 6), image.Rect(60, 30, 64, 32)}, true},
		{"outside and empty rectangles: no frame", nil, 64, 32, r(70, 0, 5, 5, 3, 3, 0, 4), nil, false},
		{"no rectangles listed: no frame", nil, 64, 32, []int32{}, nil, false},
		{"rectangles not given: whole", nil, 64, 32, nil, nil, true},
		{"too many: whole", nil, 64, 32, make([]int32, 4*(maxDirty+1)), nil, true},
		{"wrong size: dropped", nil, 32, 32, r(0, 0, 1, 1), nil, false},
		{"after a drop: whole", nil, 64, 32, r(0, 0, 1, 1), nil, true},
		{"paused: dropped", func() { s.SetPaused(true) }, 64, 32, r(0, 0, 1, 1), nil, false},
		{"resumed: whole", func() { s.SetPaused(false) }, 64, 32, r(0, 0, 1, 1), nil, true},
		{"skipping: dropped", func() { s.SetSkip(true) }, 64, 32, r(0, 0, 1, 1), nil, false},
		{"after skipping: whole", func() { s.SetSkip(false) }, 64, 32, r(0, 0, 1, 1), nil, true},
		{"then rectangles again", nil, 64, 32, r(5, 5, 1, 1), []image.Rectangle{image.Rect(5, 5, 6, 6)}, true},
	}
	for _, st := range steps {
		if st.setup != nil {
			st.setup()
		}
		before := calls
		sk.deliver(pix, stride, st.w, st.h, st.rects)
		if (calls > before) != st.call {
			t.Fatalf("%s: called %v", st.name, calls > before)
		}
		if st.call && !slices.Equal(got[len(got)-1], st.want) {
			t.Errorf("%s: dirty %v, want %v", st.name, got[len(got)-1], st.want)
		}
	}
	// A short buffer is dropped, not read past its end.
	before := calls
	sk.deliver(pix[:len(pix)-1-stride], stride, 64, 32, nil)
	if calls != before {
		t.Error("a short buffer was delivered")
	}
}

func TestDeliverAllocs(t *testing.T) {
	s := newSource(Options{}, newFakeMac("M", nil))
	sk := &sink{src: s, width: 64, height: 32, frame: func(*host.Frame) {}}
	pix, stride := pixels()
	rects := []int32{1, 1, 4, 4, 10, 10, 4, 4}
	sk.deliver(pix, stride, 64, 32, rects)
	if n := testing.AllocsPerRun(100, func() { sk.deliver(pix, stride, 64, 32, rects) }); n != 0 {
		t.Errorf("%v allocations per frame", n)
	}
}

func TestCaptureStops(t *testing.T) {
	f := newFakeMac("M", map[string]uint32{"M": 1})
	_, cancel, done := running(t, f, Options{}, nil)
	st := f.next(t)
	if st.id != 1 || st.cfg != (host.ScreenConfig{Width: 64, Height: 32, MaxFPS: 30}) {
		t.Errorf("stream of %d at %+v", st.id, st.cfg)
	}
	cancel()
	if err := wait(t, done); err != nil {
		t.Fatal(err)
	}
	if _, live := f.state(); live != 0 {
		t.Errorf("%d streams left running", live)
	}
}

func TestCaptureDefaultFPS(t *testing.T) {
	f := newFakeMac("M", map[string]uint32{"M": 1})
	s := newSource(Options{}, f)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Capture(ctx, host.ScreenConfig{Width: 64, Height: 32}, func(*host.Frame) {}) }()
	if st := f.next(t); st.cfg.MaxFPS != DefaultMaxFPS {
		t.Errorf("fps %d", st.cfg.MaxFPS)
	}
	cancel()
	if err := wait(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureRestart(t *testing.T) {
	// The display comes back with a new display ID; the stream stops with
	// -3815 and a new one captures the display with the same UUID, even
	// though the main display is now another one.
	f := newFakeMac("M", map[string]uint32{"M": 1, "N": 2})
	frames := make(chan []image.Rectangle, 4)
	_, cancel, done := running(t, f, Options{}, func(fr *host.Frame) { frames <- slices.Clone(fr.Dirty) })
	first := f.next(t)
	pix, stride := pixels()
	first.sk.deliver(pix, stride, 64, 32, []int32{0, 0, 1, 1})
	first.sk.deliver(pix, stride, 64, 32, []int32{0, 0, 1, 1})
	if d := <-frames; d != nil {
		t.Errorf("first frame %v, want whole", d)
	}
	if d := <-frames; len(d) != 1 {
		t.Errorf("second frame %v", d)
	}
	f.set(func(f *fakeMac) { f.displays["M"] = 7; f.main = "N" })
	first.sk.stop(errNoCaptureSource, "failed to find any displays or windows to capture")
	second := f.next(t)
	if second.id != 7 {
		t.Errorf("restarted on display %d, want 7", second.id)
	}
	if !first.stopped {
		t.Error("the old stream was not stopped")
	}
	second.sk.deliver(pix, stride, 64, 32, []int32{0, 0, 1, 1})
	if d := <-frames; d != nil {
		t.Errorf("first frame after the restart %v, want whole", d)
	}
	// The display changes without the stream noticing: the check finds it.
	f.set(func(f *fakeMac) { f.displays["M"] = 8 })
	if third := f.next(t); third.id != 8 {
		t.Errorf("restarted on display %d, want 8", third.id)
	}
	cancel()
	if err := wait(t, done); err != nil {
		t.Fatal(err)
	}
	if started, live := f.state(); !slices.Equal(started, []uint32{1, 7, 8}) || live != 0 {
		t.Errorf("started %v, %d live", started, live)
	}
}

func TestCaptureChosenDisplay(t *testing.T) {
	f := newFakeMac("M", map[string]uint32{"M": 1, "N": 2})
	_, cancel, done := running(t, f, Options{Display: "N"}, nil)
	if st := f.next(t); st.id != 2 {
		t.Errorf("display %d, want 2", st.id)
	}
	// The chosen display goes away: the main one is captured.
	f.set(func(f *fakeMac) { delete(f.displays, "N") })
	if st := f.next(t); st.id != 1 {
		t.Errorf("display %d, want the main display 1", st.id)
	}
	cancel()
	if err := wait(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureWaitsForDisplay(t *testing.T) {
	var logs bytes.Buffer
	f := newFakeMac("M", map[string]uint32{})
	_, cancel, done := running(t, f, Options{Logger: slog.New(slog.NewTextHandler(&logs, nil))}, nil)
	time.Sleep(100 * time.Millisecond)
	if started, _ := f.state(); len(started) != 0 {
		t.Fatalf("started %v without a display", started)
	}
	f.set(func(f *fakeMac) { f.displays["M"] = 3 })
	if st := f.next(t); st.id != 3 {
		t.Errorf("display %d", st.id)
	}
	cancel()
	if err := wait(t, done); err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(logs.Bytes(), []byte("no display to capture")); n != 1 {
		t.Errorf("the missing display was logged %d times:\n%s", n, logs.String())
	}
	// Canceled while waiting for a display.
	f.set(func(f *fakeMac) { delete(f.displays, "M") })
	_, cancel, done = running(t, f, Options{}, nil)
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := wait(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureStartRetries(t *testing.T) {
	f := newFakeMac("M", map[string]uint32{"M": 1})
	f.fails = []*startFailure{{code: errFailedToStart}, {code: errNoCaptureSource}, {code: -1}}
	_, cancel, done := running(t, f, Options{}, nil)
	f.next(t)
	cancel()
	if err := wait(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureErrors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(f *fakeMac)
		stop  *stopEvent // sent to the first stream
		route string
	}{
		{"not granted", func(f *fakeMac) { f.denied = true }, nil, "Source.Capture.CapturePermissionDenied"},
		{"refused at the start", func(f *fakeMac) { f.fails = []*startFailure{{code: errUserDeclined}} }, nil,
			"Source.Capture.CapturePermissionDenied"},
		{"withdrawn while running", nil, &stopEvent{code: errUserDeclined}, "Source.Capture->Source.watch.CapturePermissionDenied"},
		{"stopped by the user", nil, &stopEvent{code: -3817, message: "stopped"}, "Source.Capture->Source.watch.CaptureStopped"},
		{"never starts", func(f *fakeMac) {
			for range maxAttempts {
				f.fails = append(f.fails, &startFailure{code: errFailedToStart})
			}
		}, nil, "Source.Capture.CaptureStartFailed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeMac("M", map[string]uint32{"M": 1})
			if tt.setup != nil {
				tt.setup(f)
			}
			_, _, done := running(t, f, Options{}, nil)
			if tt.stop != nil {
				f.next(t).sk.stop(tt.stop.code, tt.stop.message)
			}
			assertRoute(t, wait(t, done), tt.route)
			if _, live := f.state(); live != 0 {
				t.Errorf("%d streams left running", live)
			}
		})
	}
}

func TestCaptureKeepsStopping(t *testing.T) {
	f := newFakeMac("M", map[string]uint32{"M": 1})
	_, _, done := running(t, f, Options{}, nil)
	for range maxAttempts {
		f.next(t).sk.stop(errSystemStopped, "stopped by the system")
	}
	assertRoute(t, wait(t, done), "Source.Capture.CaptureStopped")
}

func TestCaptureConfigInvalid(t *testing.T) {
	s := newSource(Options{}, newFakeMac("M", map[string]uint32{"M": 1}))
	for _, cfg := range []host.ScreenConfig{{Width: 0, Height: 10}, {Width: 10, Height: 1}, {Width: host.MaxDesktopSide + 1, Height: 10}, {Width: 10, Height: host.MaxDesktopSide + 2}} {
		assertRoute(t, s.Capture(t.Context(), cfg, func(*host.Frame) {}), "Source.Capture.CaptureConfigInvalid")
	}
}

func TestDisplay(t *testing.T) {
	f := newFakeMac("M", map[string]uint32{"M": 1, "N": 2})
	tests := []struct {
		chosen, want string
	}{
		{"", "M"},
		{"N", "N"},
		{"gone", "M"},
	}
	for _, tt := range tests {
		d, err := newSource(Options{Display: tt.chosen}, f).Display(t.Context())
		if err != nil || d.ID != tt.want {
			t.Errorf("Display with %q = %+v, %v; want %s", tt.chosen, d, err, tt.want)
		}
	}
	_, err := newSource(Options{Display: "N"}, newFakeMac("M", nil)).Display(t.Context())
	assertRoute(t, err, "Source.Display.CaptureDisplayMissing")
}
