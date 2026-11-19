package rdp

import (
	"context"
	"image"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/sec"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// awaitReplaced checks that c was told it was replaced.
func awaitReplaced(t *testing.T, c *rdptest.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reason, code, err := c.AwaitDisconnect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reason != mcs.ReasonProviderInitiated || code != sec.ErrInfoDisconnectedByOther {
		t.Errorf("reason %d errorInfo %#x, want the session replaced", reason, code)
	}
}

func TestReplace(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h})
	c1, nc1 := ts.open(t, rdptest.Options{})
	s1 := h.next(t)

	c2, _ := ts.open(t, rdptest.Options{})
	awaitReplaced(t, c1)
	// The client hangs up on the ultimatum, as a real one does.
	if err := nc1.Close(); err != nil {
		t.Fatal(err)
	}
	// The first session ends before the second reaches the application.
	select {
	case s := <-h.ended:
		if s != s1 {
			t.Fatal("another session ended")
		}
	case s := <-h.opened:
		t.Fatalf("session %p opened before the first ended", s)
	case <-time.After(5 * time.Second):
		t.Fatal("the first session did not end")
	}
	s2 := h.next(t)
	if s2 == s1 {
		t.Fatal("the same session")
	}
	ts.log.find(t, "replacing the open session")
	if got := attr(ts.log.find(t, "session closed"), "reason").String(); got != CloseReplaced.String() {
		t.Errorf("logged reason %q", got)
	}

	// The second session is a session like any other, and when it is over
	// a third replaces nothing.
	if err := s2.Pointer().Default(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c2.ReadUpdate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c2.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	h.waitEnded(t)
	ts.open(t, rdptest.Options{})
	h.next(t)
	if n := ts.log.count("replacing the open session"); n != 1 {
		t.Errorf("%d replacements logged, want 1", n)
	}
}

func TestReplaceStalledClient(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h})
	ts.open(t, rdptest.Options{})
	s1 := h.next(t)
	// The first client reads nothing, so its connection fills and the
	// ultimatum cannot be written.
	dw, dh := s1.DesktopSize()
	img := testImage(rand.New(rand.NewPCG(1, 1)), dw, dh)
	for range 1000 {
		if s1.Video().SendRects(img, []image.Rectangle{image.Rect(0, 0, dw, dh)}) != nil {
			break
		}
	}
	begin := time.Now()
	ts.open(t, rdptest.Options{})
	h.waitEnded(t)
	h.next(t)
	if d := time.Since(begin); d > closeWait+3*time.Second {
		t.Errorf("the second session opened after %v", d)
	}
}

// blockingHandler hands sessions to the test; the first one's handler
// returns only when release is closed, whatever its context.
type blockingHandler struct {
	opened  chan *Session
	release chan struct{}
	first   chan struct{}
}

func (h *blockingHandler) SessionOpened(ctx context.Context, s *Session) {
	h.opened <- s
	select {
	case <-h.first:
		<-h.release
	default:
	}
	<-ctx.Done()
}

func TestReplacedBeforeOpening(t *testing.T) {
	h := &blockingHandler{opened: make(chan *Session, 8), release: make(chan struct{}), first: make(chan struct{}, 1)}
	h.first <- struct{}{}
	ts := start(t, Config{Handler: h})
	_, nc1 := ts.open(t, rdptest.Options{})
	<-h.opened

	// The second session waits for the first, whose handler does not
	// return; the third replaces it before it opens.
	c2, nc2 := ts.open(t, rdptest.Options{})
	// The first client does not hang up; it reads until the server does.
	c1Closed := make(chan error, 1)
	go func() {
		if err := nc1.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
			c1Closed <- err
			return
		}
		_, err := io.Copy(io.Discard, nc1)
		c1Closed <- err
	}()
	ts.log.find(t, "replacing the open session")
	_, nc3 := ts.open(t, rdptest.Options{})
	awaitReplaced(t, c2)
	if err := nc2.Close(); err != nil {
		t.Fatal(err)
	}
	close(h.release)
	if err := <-c1Closed; err != nil && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("the first connection was not closed: %v", err)
	}
	select {
	case s := <-h.opened:
		if s.Client().Remote.String() != nc3.LocalAddr().String() {
			t.Errorf("session of %v opened, want the third client's %v", s.Client().Remote, nc3.LocalAddr())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the third session did not open")
	}
	select {
	case s := <-h.opened:
		t.Errorf("another session opened: %v", s.Client().Remote)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestKeepAlive(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }() // its error says nothing here
	client, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }() // its error says nothing here
	nc, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	log := slog.New(rec)
	keepAlive(nc, log)
	on, idle, interval, count := keepAliveSettings(t, nc.(*net.TCPConn))
	if !on || idle != int(keepAliveIdle/time.Second) || interval != int(keepAliveInterval/time.Second) || count != keepAliveCount {
		t.Errorf("keep-alive %v, idle %d s, interval %d s, %d probes", on, idle, interval, count)
	}

	// A connection that is not TCP has none, and one where it fails is
	// logged.
	p1, p2 := net.Pipe()
	defer func() { _ = p2.Close() }() // its error says nothing here
	keepAlive(p1, log)
	if err := nc.Close(); err != nil {
		t.Fatal(err)
	}
	keepAlive(nc, log)
	rec.find(t, "TCP keep-alive could not be turned on")
	if n := rec.count("TCP keep-alive could not be turned on"); n != 1 {
		t.Errorf("%d warnings, want 1", n)
	}
}
