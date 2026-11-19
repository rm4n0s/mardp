package host

import (
	"context"
	"log/slog"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

const (
	testUser     = "user"
	testPassword = "secret"
)

// assertRoute fails t unless err is an *errors.Error with the route.
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

// recorder is a slog.Handler that keeps the records.
type recorder struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *recorder) WithAttrs([]slog.Attr) slog.Handler       { return r }
func (r *recorder) WithGroup(string) slog.Handler            { return r }

func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, rec.Clone())
	return nil
}

// find returns the records with the message.
func (r *recorder) find(msg string) []slog.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []slog.Record
	for _, rec := range r.recs {
		if rec.Message == msg {
			out = append(out, rec)
		}
	}
	return out
}

// attr returns the attribute key of rec.
func attr(rec slog.Record, key string) slog.Value {
	var v slog.Value
	rec.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v = a.Value
			return false
		}
		return true
	})
	return v
}

// errScreen is a screen source that fails.
type errScreen struct {
	display, capture error
	d                Display
}

func (s *errScreen) Display(context.Context) (Display, error) { return s.d, s.display }

func (s *errScreen) Capture(ctx context.Context, cfg ScreenConfig, frame func(*Frame)) error {
	return s.capture
}

func TestNew(t *testing.T) {
	_, err := New(Parts{}, Options{})
	assertRoute(t, err, "New."+TagScreenMissing)

	h, err := New(Parts{Screen: &PatternScreen{}}, Options{MaxFPS: -1, MaxHeight: -5, VideoFPS: 120})
	if err != nil {
		t.Fatal(err)
	}
	o := h.opts
	if o.MaxFPS != DefaultMaxFPS || o.MaxHeight != 0 || o.DoubleClickTime != DefaultDoubleClickTime ||
		o.DoubleClickDistance != DefaultDoubleClickDistance || o.now == nil ||
		o.VideoFPS != DefaultVideoFPS || o.MaxBitrate != DefaultMaxBitrate || o.rateEvery != defaultRateEvery {
		t.Errorf("defaults: %+v", o)
	}
	h, err = New(Parts{Screen: &PatternScreen{}}, Options{VideoFPS: 30, MaxBitrate: 1})
	if err != nil {
		t.Fatal(err)
	}
	if o := h.opts; o.VideoFPS != 30 || o.MaxBitrate != MinBitrate {
		t.Errorf("options: %+v", o)
	}
}

func TestFitSize(t *testing.T) {
	tests := []struct {
		name                 string
		dw, dh, cw, ch, maxH int
		wantW, wantH         int
	}{
		{"same size", 1920, 1080, 1920, 1080, 0, 1920, 1080},
		{"never scaled up", 1280, 800, 2560, 1600, 0, 1280, 800},
		{"scaled to width", 3024, 1964, 1920, 1080, 0, 1662, 1080},
		{"scaled to height", 1920, 1080, 1024, 768, 0, 1024, 576},
		{"odd made even", 1001, 701, 1001, 701, 0, 1000, 700},
		{"height cap", 3840, 2160, 3840, 2160, 1080, 1920, 1080},
		{"cap above the size", 1920, 1080, 1920, 1080, 1440, 1920, 1080},
		{"client asked nothing", 1920, 1080, 0, 0, 0, 1920, 1080},
		{"tiny", 1920, 1080, 1, 1, 0, 2, 2},
		{"over the largest side", 16384, 2048, 16384, 2048, 0, 8192, 2048},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, h := fitSize(tt.dw, tt.dh, tt.cw, tt.ch, tt.maxH)
			if w != tt.wantW || h != tt.wantH {
				t.Errorf("fitSize = %d×%d, want %d×%d", w, h, tt.wantW, tt.wantH)
			}
		})
	}
}

func TestDesktopSize(t *testing.T) {
	tests := []struct {
		name         string
		screen       ScreenSource
		maxHeight    int
		cw, ch       int
		wantW, wantH int
		warned       bool
	}{
		{"display", &PatternScreen{Info: Display{Width: 2560, Height: 1440}}, 0, 1920, 1200, 1920, 1080, false},
		{"display with a cap", &PatternScreen{Info: Display{Width: 2560, Height: 1440}}, 720, 1920, 1200, 1280, 720, false},
		{"display fails", &errScreen{display: errors.New("TestDisplayFailed", "no display")}, 0, 1025, 767, 1024, 766, true},
		{"display fails, with a cap", &errScreen{display: errors.New("TestDisplayFailed", "no display")}, 600, 1600, 1200, 800, 600, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{}
			h, err := New(Parts{Screen: tt.screen}, Options{MaxHeight: tt.maxHeight, Logger: slog.New(rec)})
			if err != nil {
				t.Fatal(err)
			}
			w, hh := h.DesktopSize(&rdp.Client{DesktopWidth: tt.cw, DesktopHeight: tt.ch})
			if w != tt.wantW || hh != tt.wantH {
				t.Errorf("DesktopSize = %d×%d, want %d×%d", w, hh, tt.wantW, tt.wantH)
			}
			warns := rec.find("the display could not be read: the client gets the size it asked for")
			if (len(warns) > 0) != tt.warned {
				t.Errorf("warned %d times, want %v", len(warns), tt.warned)
			}
			if tt.warned && len(warns) > 0 {
				err, _ := attr(warns[0], "err").Any().(error)
				assertRoute(t, err, "TestDesktopSize.TestDisplayFailed")
			}
		})
	}
}

// session is a host serving one rdptest client.
type session struct {
	h   *Host
	c   *rdptest.Client
	rec *recorder
	sc  *rdptest.Screen
	cur *rdptest.Cursor
}

// serve runs a server of parts with a client logged in and activated, of
// the size w×h.
func serve(t *testing.T, parts Parts, opts Options, w, h int) *session {
	t.Helper()
	cert, ccfg, err := rdptest.Certificate()
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	opts.Logger = slog.New(rec)
	hst, err := New(parts, opts)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := rdp.NewServer(rdp.Config{
		Certificate: cert, Authenticator: rdp.StaticAuthenticator(testUser, testPassword),
		Handler: hst, DesktopSize: hst.DesktopSize, Logger: opts.Logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return")
		}
	})
	nc, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := nc.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	})
	cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ccancel()
	c, err := rdptest.Connect(cctx, nc, rdptest.Options{
		TLSConfig: ccfg, User: testUser, Password: testPassword, Width: uint16(w), Height: uint16(h),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login(cctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Activate(cctx); err != nil {
		t.Fatal(err)
	}
	return &session{h: hst, c: c, rec: rec, cur: c.NewCursor()}
}

// sameColors says the screen shows img, alpha aside.
func sameColors(sc *rdptest.Screen, img *rdp.Image) bool {
	if sc.Width != img.Width || sc.Height != img.Height {
		return false
	}
	for y := range sc.Height {
		a, b := sc.Pix[y*sc.Stride:], img.Pix[y*img.Stride:]
		for x := range sc.Width {
			if a[4*x] != b[4*x] || a[4*x+1] != b[4*x+1] || a[4*x+2] != b[4*x+2] {
				return false
			}
		}
	}
	return true
}

// readUntil reads updates, drawing them, until done says so.
func (s *session) readUntil(t *testing.T, what string, done func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for !done() {
		u, err := s.c.ReadUpdate(ctx)
		if err != nil {
			t.Fatalf("waiting for %s: %v", what, err)
		}
		if _, err := s.sc.Draw(u); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.cur.Apply(u); err != nil {
			t.Fatal(err)
		}
	}
}

// testShape is a pointer shape of 8×8, half transparent.
func testShape(hash uint64) PointerShape {
	img := &rdp.Image{Pix: make([]byte, 4*8*8), Stride: 4 * 8, Width: 8, Height: 8}
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(hash), 0x40, 0x80, 0xff*byte(i/4%2)
	}
	return PointerShape{Visible: true, Image: img, HotX: 1, HotY: 2, Hash: hash}
}

func TestSession(t *testing.T) {
	in := &RecordingInjector{}
	ptr := &StaticPointer{}
	ptr.Set(testShape(1), nil)
	disp := Display{ID: "d", Width: 3840, Height: 2160, PointWidth: 1920, PointHeight: 1080, Main: true}
	sys := NewFakeSystem([]Display{disp})
	var clock atomic.Int64
	clock.Store(time.Unix(1000, 0).UnixNano())
	parts := Parts{Screen: &PatternScreen{Info: disp, Frames: 1}, Input: in, Pointer: ptr, System: sys}
	s := serve(t, parts, Options{now: func() time.Time { return time.Unix(0, clock.Load()) }}, 1280, 800)
	// 3840×2160 fits in 1280×800 as 1280×720: one desktop pixel is 1.5
	// points.
	s.sc = rdptest.NewScreen(1280, 720)
	want := &rdp.Image{Pix: make([]byte, 4*1280*720), Stride: 4 * 1280, Width: 1280, Height: 720}
	PatternFrame(want, 0)

	t.Run("pattern and pointer", func(t *testing.T) {
		s.readUntil(t, "the pattern and the pointer", func() bool {
			return sameColors(s.sc, want) && s.cur.Shapes > 0
		})
		if s.cur.Hidden || s.cur.Default || s.cur.Shape.Width != 8 || s.cur.Shape.HotX != 1 || s.cur.Shape.HotY != 2 {
			t.Errorf("cursor %+v", s.cur)
		}
		if sys.Awake() != 1 {
			t.Errorf("Awake = %d, want 1", sys.Awake())
		}
		if n := s.h.Sessions(); n != 1 {
			t.Errorf("Sessions = %d, want 1", n)
		}
	})

	t.Run("refresh", func(t *testing.T) {
		clear(s.sc.Pix)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.c.SendRefresh(ctx, 0, 0, 1280, 720); err != nil {
			t.Fatal(err)
		}
		s.readUntil(t, "the desktop again", func() bool { return sameColors(s.sc, want) })
	})

	t.Run("pointer change", func(t *testing.T) {
		ptr.Set(PointerShape{}, nil)
		s.readUntil(t, "the pointer hidden", func() bool { return s.cur.Hidden })
	})

	t.Run("input", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := s.c.SendInput(ctx,
			rdptest.Key(0x1e, false, true), rdptest.Key(0x1e, false, false),
			rdptest.Key(0x1d, true, true), // right Ctrl, held
			rdptest.Move(640, 360),
			rdptest.Button(1, true, 640, 360), rdptest.Button(1, false, 640, 360),
			rdptest.Button(1, true, 641, 361), rdptest.Button(1, false, 641, 361),
			rdptest.Move(5000, 5000),          // clamped to the display
			rdptest.Button(2, true, 100, 200), // held
			rdptest.Unicode('é', true),        // held
		)
		if err != nil {
			t.Fatal(err)
		}
		got := in.Wait(ctx, 11)
		wantEv := []InputEvent{
			{Kind: InputKey, Scancode: 0x1e, Down: true},
			{Kind: InputKey, Scancode: 0x1e},
			{Kind: InputKey, Scancode: 0x1d, Extended: rdp.ExtendedE0, Down: true},
			{Kind: InputMove, X: 960, Y: 540},
			{Kind: InputButton, Button: rdp.ButtonLeft, Down: true, X: 960, Y: 540, Clicks: 1},
			{Kind: InputButton, Button: rdp.ButtonLeft, X: 960, Y: 540, Clicks: 1},
			{Kind: InputButton, Button: rdp.ButtonLeft, Down: true, X: 961.5, Y: 541.5, Clicks: 2},
			{Kind: InputButton, Button: rdp.ButtonLeft, X: 961.5, Y: 541.5, Clicks: 2},
			{Kind: InputMove, X: 1919, Y: 1079},
			{Kind: InputButton, Button: rdp.ButtonRight, Down: true, X: 150, Y: 300, Clicks: 1},
			{Kind: InputUnicode, Rune: 'é', Down: true},
		}
		got = slices.DeleteFunc(got, func(e InputEvent) bool { return e.Kind == InputLocks })
		if !slices.Equal(got, wantEv) {
			t.Errorf("injector got\n%+v\nwant\n%+v", got, wantEv)
		}
	})

	t.Run("the Mac moves the pointer", func(t *testing.T) {
		// The client's mouse has been still, and something on the Mac moves
		// the pointer: the client gets the position, in desktop pixels.
		clock.Add(int64(time.Second))
		ptr.Set(PointerShape{OnDisplay: true, X: 600, Y: 300}, nil)
		s.readUntil(t, "the pointer's position", func() bool { return s.cur.X == 400 && s.cur.Y == 200 })
	})

	t.Run("released at the end", func(t *testing.T) {
		n := len(in.Events())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.c.Disconnect(ctx); err != nil {
			t.Fatal(err)
		}
		got := in.Wait(ctx, n+3)[n:]
		slices.SortFunc(got, func(a, b InputEvent) int { return int(a.Kind) - int(b.Kind) })
		wantEv := []InputEvent{
			{Kind: InputKey, Scancode: 0x1d, Extended: rdp.ExtendedE0},
			{Kind: InputUnicode, Rune: 'é'},
			{Kind: InputButton, Button: rdp.ButtonRight, X: 150, Y: 300, Clicks: 1},
		}
		if !slices.Equal(got, wantEv) {
			t.Errorf("released\n%+v\nwant\n%+v", got, wantEv)
		}
		deadline := time.Now().Add(5 * time.Second)
		for sys.Awake() != 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if sys.Awake() != 0 {
			t.Errorf("Awake = %d after the session, want 0", sys.Awake())
		}
		for s.h.Sessions() != 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if n := s.h.Sessions(); n != 0 {
			t.Errorf("Sessions = %d after the session, want 0", n)
		}
	})
}

func TestSessionCaptureFails(t *testing.T) {
	tests := []struct {
		name   string
		screen *errScreen
		msg    string
		route  string
	}{
		{"display", &errScreen{display: errors.New("TestDisplayFailed", "no display")}, "the display could not be read", "TestSessionCaptureFails.TestDisplayFailed"},
		{"capture", &errScreen{d: Display{Width: 640, Height: 480, PointWidth: 640, PointHeight: 480}, capture: errors.New("TestCaptureFailed", "no capture")}, "the capture stopped", "TestSessionCaptureFails.TestCaptureFailed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := serve(t, Parts{Screen: tt.screen}, Options{}, 640, 480)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, info, err := s.c.AwaitDisconnect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if info == 0 {
				t.Errorf("no Set Error Info")
			}
			recs := s.rec.find(tt.msg)
			if len(recs) != 1 {
				t.Fatalf("%q logged %d times", tt.msg, len(recs))
			}
			err, _ = attr(recs[0], "err").Any().(error)
			assertRoute(t, err, tt.route)
		})
	}
}
