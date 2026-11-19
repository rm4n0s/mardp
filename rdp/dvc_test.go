package rdp

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/dynvc"
	"github.com/rm4n0s/mardp/rdp/internal/gcc"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// dvcEvents is a dvcListener that reports what happens to its channels.
type dvcEvents struct {
	events chan string
	msgs   chan []byte
	// failing makes dvcReceived fail.
	failing bool
}

func newDVCEvents() *dvcEvents {
	return &dvcEvents{events: make(chan string, 16), msgs: make(chan []byte, 16)}
}

func (e *dvcEvents) dvcOpened(d *dvc) { e.events <- "opened " + d.name }
func (e *dvcEvents) dvcRefused(d *dvc, status int32) {
	e.events <- fmt.Sprintf("refused %s %#x", d.name, uint32(status))
}
func (e *dvcEvents) dvcClosed(d *dvc) { e.events <- "closed " + d.name }
func (e *dvcEvents) dvcReceived(d *dvc, msg []byte) error {
	if e.failing {
		return errors.New("TestListenerFailed", "the listener failed", "name", d.name)
	}
	e.msgs <- slices.Clone(msg)
	return nil
}

func (e *dvcEvents) want(t *testing.T, event string) {
	t.Helper()
	select {
	case got := <-e.events:
		if got != event {
			t.Fatalf("event %q, want %q", got, event)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("no event %q", event)
	}
}

func (e *dvcEvents) message(t *testing.T) []byte {
	t.Helper()
	select {
	case m := <-e.msgs:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no message")
	}
	return nil
}

// TestDVC opens dynamic channels and exchanges messages longer than a PDU
// each way; a refused channel is reported, and closes from either side are
// handled (MS-RDPEDYC 3.3.5).
func TestDVC(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h})
	c, _ := ts.open(t, rdptest.Options{Channels: channelTestNames, EditClientData: noGFX})
	s := h.next(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ev := newDVCEvents()

	// A channel opened before the client answered the capabilities is
	// created once it has.
	early, err := s.openDVC("early", ev)
	if err != nil {
		t.Fatal(err)
	}
	caps, err := c.AnswerDVCCaps(ctx, dynvc.Version2)
	if err != nil {
		t.Fatal(err)
	}
	if caps.Version != dynvc.Version2 || caps.PriorityCharges != dvcCharges {
		t.Errorf("capabilities %+v", caps)
	}
	req, err := c.AnswerDVCCreate(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if req.Name != "early" || req.ChannelID != 1 || early.id != 1 {
		t.Errorf("create request %+v for channel %d", req, early.id)
	}
	ev.want(t, "opened early")
	ts.log.find(t, "dynamic channels ready")

	// One opened after it is created at once.
	d, err := s.openDVC("testdvc", ev)
	if err != nil {
		t.Fatal(err)
	}
	if req, err = c.AnswerDVCCreate(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if req.Name != "testdvc" || req.ChannelID != 2 {
		t.Errorf("create request %+v", req)
	}
	ev.want(t, "opened testdvc")

	msg := randomBytes(5000, 4)
	if err := c.SendDVCMessage(ctx, d.id, msg); err != nil {
		t.Fatal(err)
	}
	if m := ev.message(t); !bytes.Equal(m, msg) {
		t.Errorf("the server joined %d bytes, not the message", len(m))
	}
	if err := d.send(queueControl, msg); err != nil {
		t.Fatal(err)
	}
	m, n, err := c.ReadDVCMessage(ctx, d.id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(m, msg) || n != 4 {
		t.Errorf("the client joined %d bytes from %d PDUs", len(m), n)
	}
	if err := d.send(queueControl, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if m, n, err = c.ReadDVCMessage(ctx, d.id); err != nil || !bytes.Equal(m, []byte{1, 2, 3}) || n != 1 {
		t.Errorf("short message %v from %d PDUs: %v", m, n, err)
	}

	// A refused channel.
	refused, err := s.openDVC("refused", ev)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AnswerDVCCreate(ctx, -0x7fffbffb); err != nil {
		t.Fatal(err)
	}
	ev.want(t, "refused refused 0x80004005")
	ts.log.find(t, "dynamic channel refused")
	assertRoute(t, refused.send(queueControl, []byte{1}), "dvc.send."+TagDVCNotOpen)

	// The server closes early: the client's data until its Close is
	// dropped, and the Close ends the channel without an event.
	if err := early.close(); err != nil {
		t.Fatal(err)
	}
	p, err := c.ReadDVC(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p.Cmd != dynvc.CmdClose || p.ChannelID != early.id {
		t.Errorf("close %+v", p)
	}
	if err := c.SendDVCMessage(ctx, early.id, []byte{9}); err != nil {
		t.Fatal(err)
	}
	ts.log.find(t, "data for a closing dynamic channel dropped")
	if err := c.SendDVC(ctx, func(w *wire.Writer) { dynvc.AppendClose(w, early.id) }); err != nil {
		t.Fatal(err)
	}
	assertRoute(t, early.close(), "dvc.close."+TagDVCNotOpen)

	// The client closes testdvc, then a channel the server does not know.
	if err := c.SendDVC(ctx, func(w *wire.Writer) { dynvc.AppendClose(w, d.id) }); err != nil {
		t.Fatal(err)
	}
	ev.want(t, "closed testdvc")
	if err := c.SendDVC(ctx, func(w *wire.Writer) { dynvc.AppendClose(w, 9) }); err != nil {
		t.Fatal(err)
	}
	ts.log.find(t, "a close for an unknown dynamic channel ignored")
	assertRoute(t, d.send(queueControl, []byte{1}), "dvc.send."+TagDVCNotOpen)

	// IDs are not reused, and a listener's error ends the session.
	fail := newDVCEvents()
	fail.failing = true
	again, err := s.openDVC("again", fail)
	if err != nil {
		t.Fatal(err)
	}
	if req, err = c.AnswerDVCCreate(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if req.ChannelID != 4 || again.id != 4 {
		t.Errorf("create request %+v for channel %d", req, again.id)
	}
	fail.want(t, "opened again")
	if err := c.SendDVCMessage(ctx, again.id, []byte{1}); err != nil {
		t.Fatal(err)
	}
	h.waitEnded(t)
	rec := ts.log.find(t, "connection ended")
	err, _ = attr(rec, "err").Any().(error)
	assertRoute(t, err, "sessionReader.static->dvcManager.receive->dvcManager.data->dvcEvents.dvcReceived.TestListenerFailed")
	if _, err := s.openDVC("late", ev); err == nil {
		t.Error("a channel opened after the session ended")
	}
}

// TestDVCUnavailable opens a dynamic channel on a session whose client did
// not join drdynvc.
func TestDVCUnavailable(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h})
	c, _ := ts.open(t, rdptest.Options{Channels: []string{"rdpsnd"}})
	s := h.next(t)
	_, err := s.openDVC("testdvc", newDVCEvents())
	assertRoute(t, err, "Session.openDVC."+TagDVCUnavailable)

	// Neither can rdptest's client.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = c.ReadDVC(ctx)
	assertRoute(t, err, "Client.ReadDVC->Client.drdynvc."+rdptest.TagDVCUnavailable)
}

// noGFX takes the support of the graphics pipeline out of a client's
// data, so that the server opens no channel of its own.
func noGFX(cd *gcc.ClientData) { cd.Core.EarlyCapabilityFlags &^= gcc.EarlySupportDynvcGFXProtocol }

// dvcServer is a server whose clients have wait to answer the dynamic
// channels' capabilities, and whose sessions h gets.
func dvcServer(t *testing.T, wait time.Duration, h Handler) (*Server, *recorder) {
	t.Helper()
	p := certs(t)
	rec := &recorder{}
	s, err := NewServer(Config{Certificate: p.server, Authenticator: StaticAuthenticator(testUser, testPassword),
		Handler: h, Logger: slog.New(rec)})
	if err != nil {
		t.Fatal(err)
	}
	s.dvcCapsWait = wait
	return s, rec
}

// dvcConnect serves one connection on s and activates a client on it that
// joined drdynvc and does not support the graphics pipeline, whose channel
// would come first; the channel gets what serveConn returns.
func dvcConnect(t *testing.T, ctx context.Context, s *Server) (*rdptest.Client, <-chan error) {
	t.Helper()
	cc, sc := tcpPair(t)
	done := make(chan error, 1)
	go func() {
		err := s.serveConn(ctx, sc, s.log, func() {})
		// A write that failed as the session ended closed it already.
		if cerr := sc.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
			t.Error(cerr)
		}
		done <- err
	}()
	t.Cleanup(func() {
		if err := cc.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	})
	c := rdptest.New(cc, rdptest.Options{TLSConfig: certs(t).client, User: testUser, Password: testPassword, Channels: channelTestNames,
		EditClientData: noGFX})
	for _, step := range []func(context.Context) error{c.Negotiate, c.StartTLS, c.ConnectMCS, c.Attach, c.JoinAll, c.Login, c.Activate} {
		if err := step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return c, done
}

// TestDVCTimeout leaves the capabilities unanswered: the channel waiting
// for them is refused, and no channel opens after.
func TestDVCTimeout(t *testing.T) {
	ev := newDVCEvents()
	opened := make(chan *Session, 1)
	s, rec := dvcServer(t, 500*time.Millisecond, HandlerFunc(func(ctx context.Context, s *Session) {
		if _, err := s.openDVC("testdvc", ev); err != nil {
			t.Error(err)
		}
		opened <- s
		<-ctx.Done()
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	c, done := dvcConnect(t, ctx, s)
	sess := <-opened
	ev.want(t, fmt.Sprintf("refused testdvc %#x", uint32(0x800705b4)))
	rec.find(t, "the client did not answer the dynamic channels' capabilities")
	_, err := sess.openDVC("late", ev)
	assertRoute(t, err, "Session.openDVC."+TagDVCUnavailable)

	// A late answer is ignored.
	if _, err := c.AnswerDVCCaps(ctx, dynvc.Version2); err != nil {
		t.Fatal(err)
	}
	rec.find(t, "a capabilities response after the time for it")
	cancel()
	if err := <-done; err != nil {
		t.Logf("serveConn: %v", err)
	}
}

// TestDVCHostile sends DVC PDUs that the protocol does not allow: each
// ends the session with its tagged error.
func TestDVCHostile(t *testing.T) {
	s, _ := dvcServer(t, dvcCapsWait, HandlerFunc(func(ctx context.Context, s *Session) {
		// A session that ended already refuses it.
		if _, err := s.openDVC("testdvc", newDVCEvents()); err != nil {
			t.Logf("openDVC: %v", err)
		}
	}))
	send := func(ctx context.Context, c *rdptest.Client, build func(w *wire.Writer)) error {
		return c.SendDVC(ctx, build)
	}
	caps := func(ctx context.Context, c *rdptest.Client) error {
		_, err := c.AnswerDVCCaps(ctx, dynvc.Version2)
		return err
	}
	// created answers the capabilities and reads the Create Request of
	// testdvc, channel 1, without answering it.
	created := func(ctx context.Context, c *rdptest.Client) error {
		if err := caps(ctx, c); err != nil {
			return err
		}
		_, err := c.ReadDVC(ctx)
		return err
	}
	open := func(ctx context.Context, c *rdptest.Client) error {
		if err := caps(ctx, c); err != nil {
			return err
		}
		_, err := c.AnswerDVCCreate(ctx, 0)
		return err
	}
	const dvcRoute = "conn.runSession->conn.readSession->sessionReader.tpkt->sessionReader.static->dvcManager.receive->"
	tests := []struct {
		name   string
		client func(ctx context.Context, c *rdptest.Client) error
		route  string
	}{
		{"caps twice", func(ctx context.Context, c *rdptest.Client) error {
			if err := caps(ctx, c); err != nil {
				return err
			}
			var werr error
			if err := send(ctx, c, func(w *wire.Writer) { werr = dynvc.AppendCapsResponse(w, dynvc.Version2) }); err != nil {
				return err
			}
			return werr
		}, dvcRoute + "dvcManager.caps." + TagDVCCapsUnexpected},
		{"newer version", func(ctx context.Context, c *rdptest.Client) error {
			_, err := c.AnswerDVCCaps(ctx, dynvc.Version3)
			return err
		}, dvcRoute + "dvcManager.caps." + TagDVCVersionInvalid},
		{"create response unknown", func(ctx context.Context, c *rdptest.Client) error {
			if err := caps(ctx, c); err != nil {
				return err
			}
			return send(ctx, c, func(w *wire.Writer) { dynvc.AppendCreateResponse(w, 7, 0) })
		}, dvcRoute + "dvcManager.created->dvcManager.lookup." + TagDVCUnknown},
		{"data unknown", func(ctx context.Context, c *rdptest.Client) error {
			if err := caps(ctx, c); err != nil {
				return err
			}
			return c.SendDVCMessage(ctx, 7, []byte{1})
		}, dvcRoute + "dvcManager.data->dvcManager.lookup." + TagDVCUnknown},
		{"data before the answer", func(ctx context.Context, c *rdptest.Client) error {
			if err := created(ctx, c); err != nil {
				return err
			}
			return c.SendDVCMessage(ctx, 1, []byte{1})
		}, dvcRoute + "dvcManager.data." + TagDVCStateInvalid},
		{"close before the answer", func(ctx context.Context, c *rdptest.Client) error {
			if err := created(ctx, c); err != nil {
				return err
			}
			return send(ctx, c, func(w *wire.Writer) { dynvc.AppendClose(w, 1) })
		}, dvcRoute + "dvcManager.closed." + TagDVCStateInvalid},
		{"answered twice", func(ctx context.Context, c *rdptest.Client) error {
			if err := open(ctx, c); err != nil {
				return err
			}
			return send(ctx, c, func(w *wire.Writer) { dynvc.AppendCreateResponse(w, 1, 0) })
		}, dvcRoute + "dvcManager.created." + TagDVCStateInvalid},
		{"data first twice", func(ctx context.Context, c *rdptest.Client) error {
			if err := open(ctx, c); err != nil {
				return err
			}
			for range 2 {
				if err := send(ctx, c, func(w *wire.Writer) { dynvc.AppendDataFirst(w, 1, 10, []byte{1}) }); err != nil {
					return err
				}
			}
			return nil
		}, dvcRoute + "dvcManager.data->Joiner.Add." + dynvc.TagSequence},
		{"compressed", func(ctx context.Context, c *rdptest.Client) error {
			if err := open(ctx, c); err != nil {
				return err
			}
			return send(ctx, c, func(w *wire.Writer) { w.Write([]byte{dynvc.CmdDataCompressed << 4, 1, 0}) })
		}, dvcRoute + "ParseClient->parseCommon." + dynvc.TagCompressed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, done := dvcConnect(t, ctx, s)
			if err := tt.client(ctx, c); err != nil {
				t.Logf("client: %v", err)
			}
			assertRoute(t, <-done, tt.route)
		})
	}
}
