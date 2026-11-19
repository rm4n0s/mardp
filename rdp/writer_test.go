package rdp

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/caps"
	"github.com/rm4n0s/mardp/rdp/internal/fastpath"
	"github.com/rm4n0s/mardp/rdp/internal/sec"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// gate is an io.Writer that records each write and holds it until the test
// lets it through.
type gate struct {
	started chan []byte
	release chan struct{}
}

func newGate() *gate { return &gate{started: make(chan []byte, 64), release: make(chan struct{})} }

func (g *gate) Write(b []byte) (int, error) {
	g.started <- bytes.Clone(b)
	<-g.release
	return len(b), nil
}

func (g *gate) next(t *testing.T) []byte {
	t.Helper()
	select {
	case b := <-g.started:
		return b
	case <-time.After(5 * time.Second):
		t.Fatal("no write")
	}
	return nil
}

// mark queues on q a packet of the one byte b.
func mark(t *testing.T, wr *writer, q queue, b byte) {
	t.Helper()
	if err := wr.send(q, func(p *packet) error { p.w.U8(b); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestWriterControlFirst(t *testing.T) {
	g := newGate()
	wr := newWriter(g, func(error) {})
	go wr.run()
	defer func() {
		close(g.release)
		wr.halt()
	}()
	// The writer is busy with video 1 while video 2 and 3 and then control
	// 9 are queued: control goes next, then the rest of the video in order.
	mark(t, wr, queueVideo, 1)
	if b := g.next(t); b[0] != 1 {
		t.Fatalf("first write %v", b)
	}
	mark(t, wr, queueVideo, 2)
	mark(t, wr, queueVideo, 3)
	mark(t, wr, queueControl, 9)
	mark(t, wr, queueControl, 8)
	var order []byte
	for range 4 {
		g.release <- struct{}{}
		order = append(order, g.next(t)[0])
	}
	if want := []byte{9, 8, 2, 3}; !bytes.Equal(order, want) {
		t.Errorf("order %v, want %v", order, want)
	}
	g.release <- struct{}{}
}

func TestWriterQueueFull(t *testing.T) {
	for _, tt := range []struct {
		q     queue
		depth int
	}{{queueControl, controlDepth}, {queueVideo, videoDepth}} {
		t.Run(tt.q.String(), func(t *testing.T) {
			wr := newWriter(io.Discard, func(error) {}) // not running: nothing leaves the queue
			for range tt.depth {
				mark(t, wr, tt.q, 1)
			}
			err := wr.send(tt.q, func(p *packet) error { return nil })
			assertRoute(t, err, "writer.send->writer.get."+TagQueueFull)
			// The other queue is not affected.
			mark(t, wr, 1-tt.q, 1)
		})
	}
}

func TestWriterBuildFails(t *testing.T) {
	wr := newWriter(io.Discard, func(error) {})
	for range videoDepth + 1 {
		err := wr.send(queueVideo, func(p *packet) error {
			p.done = make(chan error, 1)
			return fastpath.AppendUpdate(&p.w, 0x10, nil, 0)
		})
		assertRoute(t, err, "AppendUpdate."+fastpath.TagUpdateCodeInvalid)
	}
	// The packets came back, emptied.
	for range videoDepth {
		if err := wr.send(queueVideo, func(p *packet) error {
			if p.w.Len() != 0 || p.done != nil {
				t.Errorf("packet not emptied: %d bytes, done %v", p.w.Len(), p.done != nil)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// brokenWriter fails every write.
type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestWriterFails(t *testing.T) {
	failed := make(chan error, 1)
	wr := newWriter(brokenWriter{}, func(err error) { failed <- err })
	go wr.run()
	done := make(chan error, 1)
	if err := wr.send(queueControl, func(p *packet) error { p.done = done; p.w.U8(1); return nil }); err != nil {
		t.Fatal(err)
	}
	assertRoute(t, <-done, "writer.run."+TagWriteFailed)
	assertRoute(t, <-failed, "writer.run."+TagWriteFailed)
	<-wr.exited
	assertRoute(t, wr.failure(), "writer.run."+TagWriteFailed)
	if !errors.Is(wr.failure(), io.ErrClosedPipe) {
		t.Errorf("cause %v", wr.failure())
	}
	assertRoute(t, wr.send(queueVideo, func(p *packet) error { return nil }), "writer.send->writer.get."+TagSessionOver)
	wr.halt() // after the writer stopped by itself
}

// signal is an io.Writer that says when each write is done.
type signal chan struct{}

func (s signal) Write(b []byte) (int, error) {
	s <- struct{}{}
	return len(b), nil
}

func TestWriterAllocs(t *testing.T) {
	sig := make(signal)
	wr := newWriter(sig, func(error) {})
	go wr.run()
	defer wr.halt()
	s := &Session{wr: wr, client: Client{FastPathOutput: true, MaxRequestSize: 0x3f0000}}
	frame := make([]byte, 200<<10)
	small := make([]byte, 40)
	tests := []struct {
		name string
		run  func() error
	}{
		{"fragmented video update", func() error { return s.sendUpdate(queueVideo, fastpath.UpdateBitmap, frame) }},
		{"small control update", func() error { return s.sendUpdate(queueControl, fastpath.UpdatePointerPosition, small) }},
		{"share PDU", func() error {
			return s.sendShare(queueControl, func(w *wire.Writer) error { sec.WriteSetErrorInfo(w, shareID, 1); return nil })
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			// Warm up: the first packets grow to the size they need.
			for range controlDepth {
				if err = tt.run(); err != nil {
					t.Fatal(err)
				}
				<-sig
			}
			n := testing.AllocsPerRun(100, func() {
				if err = tt.run(); err == nil {
					<-sig
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("%v allocations per update, want 0", n)
			}
		})
	}
}

func BenchmarkWriter(b *testing.B) {
	for _, n := range []int{64, 16 << 10, 256 << 10} {
		b.Run(byteSize(n), func(b *testing.B) {
			sig := make(signal)
			wr := newWriter(sig, func(error) {})
			go wr.run()
			defer wr.halt()
			s := &Session{wr: wr, client: Client{FastPathOutput: true, MaxRequestSize: 0x3f0000}}
			data := make([]byte, n)
			b.ReportAllocs()
			b.SetBytes(int64(n))
			for b.Loop() {
				if err := s.sendUpdate(queueVideo, fastpath.UpdateBitmap, data); err != nil {
					b.Fatal(err)
				}
				<-sig
			}
		})
	}
}

func byteSize(n int) string {
	switch {
	case n >= 1<<10:
		return itoa(n>>10) + "KiB"
	default:
		return itoa(n) + "B"
	}
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*13 + i>>9)
	}
	return b
}

func TestUpdates(t *testing.T) {
	noMultifragment := func(cc *caps.Caps) {
		cc.Sets &^= 1 << caps.SetMultifragmentUpdate
		cc.MaxRequestSize = 0
	}
	tests := []struct {
		name string
		edit func(*caps.Caps)
		code uint8
		size int
	}{
		{"no data", nil, fastpath.UpdatePointerHidden, 0},
		{"small", nil, fastpath.UpdatePointerPosition, 4},
		{"one fragment", nil, fastpath.UpdateBitmap, fastpath.MaxFragment},
		{"two fragments", nil, fastpath.UpdateBitmap, fastpath.MaxFragment + 1},
		{"many fragments", nil, fastpath.UpdateSurfaceCommands, 300000},
		{"the client's largest", nil, fastpath.UpdateBitmap, 0x3f0000},
		{"without multifragment", noMultifragment, fastpath.UpdateBitmap, fastpath.MaxFragment},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newSessions()
			ts := start(t, Config{Handler: h})
			c, _ := ts.open(t, rdptest.Options{EditCaps: tt.edit})
			s := h.next(t)
			data := pattern(tt.size)
			if err := s.sendUpdate(queueVideo, tt.code, data); err != nil {
				t.Fatal(err)
			}
			// A control PDU queued after it arrives too; the client skips it.
			if err := s.sendShare(queueControl, func(w *wire.Writer) error { sec.WriteSetErrorInfo(w, shareID, 0); return nil }); err != nil {
				t.Fatal(err)
			}
			if err := s.sendUpdate(queueControl, fastpath.UpdatePointerDefault, nil); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			seen := map[uint8][]byte{}
			for range 2 {
				u, err := c.ReadUpdate(ctx)
				if err != nil {
					t.Fatal(err)
				}
				seen[u.Code] = bytes.Clone(u.Data)
			}
			if got, ok := seen[tt.code]; !ok || !bytes.Equal(got, data) {
				t.Errorf("update %d: %d bytes arrived, want %d", tt.code, len(got), len(data))
			}
			if _, ok := seen[fastpath.UpdatePointerDefault]; !ok {
				t.Error("the control update did not arrive")
			}
		})
	}
}

func TestUpdateErrors(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*caps.Caps)
		size  int
		route string
	}{
		{"larger than the client takes", nil, 0x3f0001, "Session.sendUpdate->writer.send->Session.sendUpdate.func1->AppendUpdate." + fastpath.TagUpdateTooLarge},
		{"fragments without multifragment", func(cc *caps.Caps) {
			cc.Sets &^= 1 << caps.SetMultifragmentUpdate
			cc.MaxRequestSize = 0
		}, fastpath.MaxFragment + 1, "AppendUpdate." + fastpath.TagUpdateTooLarge},
		{"no fast-path output", func(cc *caps.Caps) { cc.General.ExtraFlags &^= caps.ExtraFastPathOutput }, 1, "Session.sendUpdate." + TagFastPathUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newSessions()
			ts := start(t, Config{Handler: h})
			ts.open(t, rdptest.Options{EditCaps: tt.edit})
			s := h.next(t)
			assertRoute(t, s.sendUpdate(queueVideo, fastpath.UpdateBitmap, make([]byte, tt.size)), tt.route)
		})
	}
	t.Run("session over", func(t *testing.T) {
		h := newSessions()
		ts := start(t, Config{Handler: h})
		c, _ := ts.open(t, rdptest.Options{})
		s := h.next(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Disconnect(ctx); err != nil {
			t.Fatal(err)
		}
		h.waitEnded(t)
		<-s.wr.exited
		assertRoute(t, s.sendUpdate(queueVideo, fastpath.UpdateBitmap, nil), "Session.sendUpdate->writer.send->writer.get."+TagSessionOver)
		assertRoute(t, s.sendShare(queueControl, func(*wire.Writer) error { return nil }), "Session.sendShare->writer.send->writer.get."+TagSessionOver)
	})
}

func TestSessionCloseAbandoned(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h})
	c, _ := ts.open(t, rdptest.Options{})
	s := h.next(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assertRoute(t, s.Close(ctx, CloseByHost), "Session.Close."+TagCloseAbandoned)
	h.waitEnded(t)
	// The connection is closed without the ultimatum.
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()
	if _, _, err := c.AwaitDisconnect(rctx); err == nil {
		t.Error("the client got an ultimatum")
	}
}

// failingWrites is a connection whose writes fail once armed.
type failingWrites struct {
	net.Conn
	armed *atomic.Bool
}

func (f failingWrites) Write(b []byte) (int, error) {
	if f.armed.Load() {
		return 0, io.ErrClosedPipe
	}
	return f.Conn.Write(b)
}

func TestSessionWriteFails(t *testing.T) {
	p := certs(t)
	opened := make(chan *Session, 1)
	srv, err := NewServer(Config{Certificate: p.server, Authenticator: StaticAuthenticator(testUser, testPassword),
		Handler: HandlerFunc(func(ctx context.Context, s *Session) { opened <- s; <-ctx.Done() })})
	if err != nil {
		t.Fatal(err)
	}
	cc, sc := tcpPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var armed atomic.Bool
	done := make(chan error, 1)
	go func() { done <- srv.serveConn(ctx, failingWrites{sc, &armed}, srv.log, func() {}) }()
	c := rdptest.New(cc, rdptest.Options{TLSConfig: p.client, User: testUser, Password: testPassword})
	for _, step := range []func(context.Context) error{c.Negotiate, c.StartTLS, c.ConnectMCS, c.Attach, c.JoinAll, c.Login, c.Activate} {
		if err := step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	s := <-opened
	armed.Store(true)
	if err := s.sendUpdate(queueVideo, fastpath.UpdateBitmap, []byte{1}); err != nil {
		t.Fatal(err)
	}
	// The failed write ends the session, with its error.
	assertRoute(t, <-done, "writer.run."+TagWriteFailed)
}
