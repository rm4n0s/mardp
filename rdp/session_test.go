package rdp

import (
	"context"
	"io"
	"net"
	"reflect"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/caps"
	"github.com/rm4n0s/mardp/rdp/internal/gcc"
	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/sec"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// sessions is a Handler that hands each session to the test and says when
// its context ends.
type sessions struct {
	opened chan *Session
	ended  chan *Session
}

func newSessions() *sessions {
	return &sessions{opened: make(chan *Session, 8), ended: make(chan *Session, 8)}
}

func (h *sessions) SessionOpened(ctx context.Context, s *Session) {
	h.opened <- s
	<-ctx.Done()
	h.ended <- s
}

func (h *sessions) next(t *testing.T) *Session {
	t.Helper()
	select {
	case s := <-h.opened:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("no session opened")
	}
	return nil
}

func (h *sessions) waitEnded(t *testing.T) {
	t.Helper()
	select {
	case <-h.ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the session's context was not canceled")
	}
}

// open connects, logs in and activates with opts.
func (ts *testServer) open(t *testing.T, opts rdptest.Options) (*rdptest.Client, net.Conn) {
	t.Helper()
	c, nc, err := ts.login(t, opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	return c, nc
}

func TestSessionOpened(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h})
	c, nc := ts.open(t, rdptest.Options{Name: "linuxbox", Width: 1920, Height: 1080, Domain: "home"})
	s := h.next(t)
	got := s.Client()
	core := c.ClientData().Core
	want := Client{
		Name: "linuxbox", Remote: nc.LocalAddr(), User: testUser, Domain: "home",
		DesktopWidth: 1920, DesktopHeight: 1080,
		KeyboardLayout: core.KeyboardLayout, KeyboardType: core.KeyboardType,
		KeyboardSubType: core.KeyboardSubType, KeyboardFunctionKeys: core.KeyboardFunctionKey,
		Build:            core.ClientBuild,
		GraphicsPipeline: core.Early(gcc.EarlySupportDynvcGFXProtocol),
		Channels:         rdptest.DefaultChannels,
		FastPathOutput:   true, DesktopResize: true, SkipAlpha: true,
		InputScancodes: true, InputUnicode: true, InputExtendedMouse: true, InputHorizontalWheel: true, InputFastPath: true,
		AlphaPointer: true, PointerCacheSize: 25, ColorPointer: true, LargePointer: 384, MaxRequestSize: 0x3f0000,
		SurfaceBits: true, FrameMarker: true, FrameAcknowledge: true, MaxUnacknowledgedFrames: 2,
	}
	if got.Remote.String() != want.Remote.String() || !slices.Equal(got.Channels, want.Channels) {
		t.Errorf("remote %v channels %v, want %v %v", got.Remote, got.Channels, want.Remote, want.Channels)
	}
	g, w := *got, want
	g.Remote, g.Channels, w.Remote, w.Channels = nil, nil, nil, nil
	if !reflect.DeepEqual(g, w) {
		t.Errorf("client\n got %+v\nwant %+v", g, w)
	}
	if w, h := s.DesktopSize(); w != 1920 || h != 1080 {
		t.Errorf("desktop %dx%d", w, h)
	}
	// What the server's Demand Active said (SPEC §5.2).
	sc := c.ServerCaps
	if c.ShareID != shareID || c.ServerSource != serverInitiator {
		t.Errorf("share %#x source %d", c.ShareID, c.ServerSource)
	}
	if sc.Bitmap.BitsPerPixel != 32 || sc.Bitmap.DesktopWidth != 1920 || sc.Bitmap.DesktopHeight != 1080 || sc.Bitmap.DesktopResize {
		t.Errorf("bitmap %+v", sc.Bitmap)
	}
	if sc.General.ExtraFlags&caps.ExtraFastPathOutput == 0 || !sc.General.RefreshRect || !sc.General.SuppressOutput {
		t.Errorf("general %+v", sc.General)
	}
	if want := caps.InputScancodes | caps.InputMouseX | caps.InputUnicode | caps.InputFastPath2 | caps.InputMouseHWheel; sc.Input.Flags != want {
		t.Errorf("input %#x, want %#x", sc.Input.Flags, want)
	}
	if !sc.Pointer.Color || sc.LargePointer != caps.LargePointer96|caps.LargePointer384 || sc.ShareNodeID != serverInitiator {
		t.Errorf("pointer %+v large %d node %d", sc.Pointer, sc.LargePointer, sc.ShareNodeID)
	}
	for _, set := range []uint16{caps.SetOrder, caps.SetVirtualChannel, caps.SetFont, caps.SetMultifragmentUpdate, caps.SetSurfaceCommands, caps.SetFrameAcknowledge} {
		if !sc.Has(set) {
			t.Errorf("no capability set %#x", set)
		}
	}
	ts.log.find(t, "session opened")
}

func TestSessionClientWithoutOptionalSets(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h})
	ts.open(t, rdptest.Options{EditCaps: func(c *caps.Caps) {
		*c = caps.Caps{Sets: 1<<caps.SetGeneral | 1<<caps.SetPointer, Pointer: caps.Pointer{ColorCacheSize: 20}}
	}})
	got := h.next(t).Client()
	if got.FastPathOutput || got.LargePointer != 0 || got.MaxRequestSize != 0 || got.PointerCacheSize != 20 || got.FrameAcknowledge || got.InputUnicode {
		t.Errorf("%+v", got)
	}
}

func TestDesktopSize(t *testing.T) {
	h := newSessions()
	var asked Client
	ts := start(t, Config{Handler: h, DesktopSize: func(c *Client) (int, int) {
		asked = *c
		return c.DesktopWidth / 2, 600
	}})
	c, _ := ts.open(t, rdptest.Options{Width: 1600, Height: 1200})
	s := h.next(t)
	if w, h := s.DesktopSize(); w != 800 || h != 600 {
		t.Errorf("session %dx%d", w, h)
	}
	if c.ServerCaps.Bitmap.DesktopWidth != 800 || c.ServerCaps.Bitmap.DesktopHeight != 600 {
		t.Errorf("demand active %+v", c.ServerCaps.Bitmap)
	}
	if asked.DesktopWidth != 1600 || asked.User != testUser {
		t.Errorf("DesktopSize got %+v", asked)
	}
	// The client's capabilities are not known yet when the size is decided.
	if asked.FastPathOutput {
		t.Error("DesktopSize saw the Confirm Active")
	}
}

func TestDesktopSizeInvalid(t *testing.T) {
	for _, size := range [][2]int{{0, 600}, {800, 0}, {MaxDesktopSide + 1, 600}, {800, -1}} {
		ts := start(t, Config{DesktopSize: func(*Client) (int, int) { return size[0], size[1] }})
		_, nc, err := ts.login(t, rdptest.Options{})
		if err != nil {
			t.Fatal(err)
		}
		closedWithin(t, nc, 2*time.Second)
		rec := ts.log.find(t, "connection ended")
		err, _ = attr(rec, "err").Any().(error)
		assertRoute(t, err, "Server.serveConn->conn.activate."+TagDesktopSizeInvalid)
	}
}

func TestSessionClose(t *testing.T) {
	tests := []struct {
		name      string
		reason    CloseReason
		noErrInfo bool
		errorInfo uint32
	}{
		{"by host", CloseByHost, false, sec.ErrInfoRPCInitiatedDisconnect},
		{"replaced", CloseReplaced, false, sec.ErrInfoDisconnectedByOther},
		{"server stopped", CloseServerStopped, false, sec.ErrInfoServerShutdown},
		{"no Set Error Info support", CloseByHost, true, sec.ErrInfoNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newSessions()
			ts := start(t, Config{Handler: h})
			opts := rdptest.Options{}
			if tt.noErrInfo {
				opts.EditClientData = func(cd *gcc.ClientData) { cd.Core.EarlyCapabilityFlags &^= gcc.EarlySupportErrInfoPDU }
			}
			c, nc := ts.open(t, opts)
			s := h.next(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.Close(ctx, tt.reason); err != nil {
				t.Fatal(err)
			}
			h.waitEnded(t)
			reason, code, err := c.AwaitDisconnect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if reason != mcs.ReasonProviderInitiated || code != tt.errorInfo {
				t.Errorf("reason %d errorInfo %#x, want %d %#x", reason, code, mcs.ReasonProviderInitiated, tt.errorInfo)
			}
			// The client hangs up, as a real one does on the ultimatum.
			if err := nc.Close(); err != nil {
				t.Fatal(err)
			}
			rec := ts.log.find(t, "session closed")
			if got := attr(rec, "reason").String(); got != tt.reason.String() {
				t.Errorf("logged reason %q", got)
			}
			if err := s.Close(ctx, CloseByHost); err != nil {
				t.Errorf("second Close: %v", err)
			}
		})
	}
}

func TestSessionCloseWaitsForTheClient(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h})
	_, nc := ts.open(t, rdptest.Options{})
	s := h.next(t)
	begin := time.Now()
	if err := s.Close(context.Background(), CloseByHost); err != nil {
		t.Fatal(err)
	}
	// A client that does not hang up is hung up on after closeWait.
	if err := nc.SetReadDeadline(time.Now().Add(closeWait + 2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, nc); err != nil && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("connection not closed: %v", err)
	}
	if d := time.Since(begin); d < closeWait-100*time.Millisecond {
		t.Errorf("closed after %v, before closeWait", d)
	}
	ts.log.find(t, "session closed")
}

func TestClientDisconnects(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h})
	c, nc := ts.open(t, rdptest.Options{})
	s := h.next(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	h.waitEnded(t)
	closedWithin(t, nc, 2*time.Second)
	rec := ts.log.find(t, "session closed by the client")
	if got := attr(rec, "reason").Uint64(); got != uint64(mcs.ReasonUserRequested) {
		t.Errorf("reason %d", got)
	}
	// Close after the end does nothing.
	if err := s.Close(ctx, CloseByHost); err != nil {
		t.Errorf("Close after the end: %v", err)
	}
}

func TestClientHangsUp(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h})
	_, nc := ts.open(t, rdptest.Options{})
	h.next(t)
	if err := nc.Close(); err != nil {
		t.Fatal(err)
	}
	h.waitEnded(t)
	ts.log.find(t, "connection closed by the client")
}

func TestServerStopClosesSessions(t *testing.T) {
	p := certs(t)
	h := newSessions()
	s, err := NewServer(Config{Certificate: p.server, Authenticator: StaticAuthenticator(testUser, testPassword), Handler: h})
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, l) }()
	ts := &testServer{addr: l.Addr().String(), log: &recorder{}, cfg: p.client}
	c, nc := ts.open(t, rdptest.Options{})
	h.next(t)
	cancel()
	h.waitEnded(t)
	cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ccancel()
	reason, code, err := c.AwaitDisconnect(cctx)
	if err != nil {
		t.Fatal(err)
	}
	if reason != mcs.ReasonProviderInitiated || code != sec.ErrInfoServerShutdown {
		t.Errorf("reason %d errorInfo %#x", reason, code)
	}
	if err := nc.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
}

// noReadDeadline is a connection whose read deadline cannot be set.
type noReadDeadline struct{ net.Conn }

func (noReadDeadline) SetReadDeadline(time.Time) error { return io.ErrClosedPipe }

func TestSessionErrors(t *testing.T) {
	p := certs(t)
	closed := make(chan error, 1)
	s, err := NewServer(Config{Certificate: p.server, Authenticator: StaticAuthenticator(testUser, testPassword),
		Handler: HandlerFunc(func(ctx context.Context, s *Session) { closed <- s.Close(ctx, CloseByHost) })})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("close deadline fails", func(t *testing.T) {
		cc, sc := tcpPair(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- s.serveConn(ctx, noReadDeadline{sc}, s.log, func() {}) }()
		c := rdptest.New(cc, rdptest.Options{TLSConfig: p.client, User: testUser, Password: testPassword})
		for _, step := range []func(context.Context) error{c.Negotiate, c.StartTLS, c.ConnectMCS, c.Attach, c.JoinAll, c.Login, c.Activate} {
			if err := step(ctx); err != nil {
				t.Fatal(err)
			}
		}
		assertRoute(t, <-closed, "Session.Close."+TagCloseDeadlineFailed)
		if err := <-done; err != nil {
			t.Errorf("serveConn: %v", err)
		}
	})
}
