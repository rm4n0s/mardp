package rdp

import (
	"bytes"
	"context"
	"math/rand/v2"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/svc"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// channelTestNames are the static channels the tests' clients ask for:
// cliprdr, which the server ignores, and the two it reads.
var channelTestNames = []string{"cliprdr", "rdpsnd", "drdynvc"}

func randomBytes(n int, seed uint64) []byte {
	rng := rand.New(rand.NewPCG(seed, seed))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.Uint32())
	}
	return b
}

// TestStaticChannels sends a message longer than a chunk each way on
// rdpsnd: the server joins the client's chunks into the message, and its
// answer arrives in chunks of 1,600 bytes that rdptest joins.
func TestStaticChannels(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h})
	c, _ := ts.open(t, rdptest.Options{Channels: channelTestNames})
	s := h.next(t)
	if s.channel("cliprdr") != nil || s.channel("drdynvc") == nil {
		t.Fatalf("channels the server reads: %v", s.channels)
	}
	ch := s.channel("rdpsnd")
	if ch == nil || ch.id != c.ChannelID("rdpsnd") {
		t.Fatalf("rdpsnd: %+v, the client's ID %d", ch, c.ChannelID("rdpsnd"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// What arrives on cliprdr is ignored, and on rdpsnd, while it has no
	// handler, dropped once joined.
	if err := c.SendChannel(ctx, c.ChannelID("cliprdr"), randomBytes(3000, 1)); err != nil {
		t.Fatal(err)
	}
	if err := c.SendChannel(ctx, ch.id, randomBytes(3000, 2)); err != nil {
		t.Fatal(err)
	}
	ts.log.find(t, "static channel message dropped")

	got := make(chan []byte, 1)
	ch.setHandler(func(msg []byte) error {
		got <- slices.Clone(msg)
		return s.sendChannel(queueControl, ch, msg)
	})
	msg := randomBytes(5000, 3)
	if err := c.SendChannel(ctx, ch.id, msg); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-got:
		if !bytes.Equal(m, msg) {
			t.Fatalf("the server joined %d bytes, not the message", len(m))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server got no message")
	}
	m, err := c.ReadChannel(ctx, ch.id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(m.Data, msg) {
		t.Errorf("the client joined %d bytes, not the message", len(m.Data))
	}
	wantChunks := []svc.Header{{Length: 5000, Flags: svc.FlagFirst}, {Length: 5000}, {Length: 5000}, {Length: 5000, Flags: svc.FlagLast}}
	if !slices.Equal(m.Chunks, wantChunks) || !slices.Equal(m.Lengths, []int{1600, 1600, 1600, 200}) {
		t.Errorf("chunks %+v of %v", m.Chunks, m.Lengths)
	}
	if ts.log.count("static channel data ignored") != 2 || ts.log.count("static channel message dropped") != 1 {
		t.Errorf("ignored %d, dropped %d", ts.log.count("static channel data ignored"), ts.log.count("static channel message dropped"))
	}

	// A short message goes in one chunk.
	if err := s.sendChannel(queueControl, ch, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	m, err = c.ReadChannel(ctx, ch.id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(m.Data, []byte{1, 2, 3}) || !slices.Equal(m.Chunks, []svc.Header{{Length: 3, Flags: svc.FlagFirst | svc.FlagLast}}) {
		t.Errorf("short message %+v", m)
	}

	// A handler's error ends the session.
	ch.setHandler(func([]byte) error { return errors.New("TestHandlerFailed", "the handler failed") })
	if err := c.SendChannel(ctx, ch.id, []byte{1}); err != nil {
		t.Fatal(err)
	}
	h.waitEnded(t)
	rec := ts.log.find(t, "connection ended")
	err, _ = attr(rec, "err").Any().(error)
	assertRoute(t, err, "sessionReader.static->TestStaticChannels.func2.TestHandlerFailed")
}

// TestStaticChannelHandlerUnset checks that a handler set to nil drops the
// messages again.
func TestStaticChannelHandlerUnset(t *testing.T) {
	ch := &staticChannel{}
	ch.setHandler(func([]byte) error { return nil })
	ch.setHandler(nil)
	if ch.handler.Load() != nil {
		t.Error("a handler is still set")
	}
}

// TestStaticChannelHostile sends chunks out of order: each ends the session
// with its tagged error.
func TestStaticChannelHostile(t *testing.T) {
	p := certs(t)
	s, err := NewServer(Config{Certificate: p.server, Authenticator: StaticAuthenticator(testUser, testPassword), Handler: nopHandler})
	if err != nil {
		t.Fatal(err)
	}
	chunk := func(h svc.Header, data ...byte) func(ctx context.Context, c *rdptest.Client) error {
		return func(ctx context.Context, c *rdptest.Client) error {
			return c.SendChannelChunk(ctx, c.ChannelID("drdynvc"), h, data)
		}
	}
	const static = "conn.runSession->conn.readSession->sessionReader.tpkt->sessionReader.static->"
	tests := []struct {
		name   string
		client func(ctx context.Context, c *rdptest.Client) error
		route  string
	}{
		{"compressed", chunk(svc.Header{Length: 1, Flags: svc.FlagFirst | svc.FlagLast | svc.FlagCompressed}, 1), static + "Joiner.Add." + svc.TagCompressed},
		{"no first chunk", chunk(svc.Header{Length: 2, Flags: svc.FlagLast}, 1), static + "Joiner.Add." + svc.TagChunkSequence},
		{"too long", chunk(svc.Header{Length: channelMaxMessage + 1, Flags: svc.FlagFirst}, 1), static + "Joiner.Add." + svc.TagMessageTooLarge},
		{"lengths", chunk(svc.Header{Length: 3, Flags: svc.FlagFirst | svc.FlagLast}, 1), static + "Joiner.Add." + svc.TagLengthInvalid},
		{"header cut", func(ctx context.Context, c *rdptest.Client) error {
			return c.SendData(ctx, func(w *wire.Writer) error {
				return mcs.WriteSendDataRequest(w, &mcs.SendData{Initiator: c.UserID, ChannelID: c.ChannelID("rdpsnd"), Data: []byte{1, 0, 0}})
			})
		}, static + "ReadHeader->Reader.U32LE." + wire.TagTruncated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc, sc := tcpPair(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				err := s.serveConn(ctx, sc, s.log, func() {})
				// A write that failed as the session ended closed it already.
				if cerr := sc.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
					t.Error(cerr)
				}
				done <- err
			}()
			c := rdptest.New(cc, rdptest.Options{TLSConfig: p.client, User: testUser, Password: testPassword, Channels: channelTestNames})
			for _, step := range []func(context.Context) error{c.Negotiate, c.StartTLS, c.ConnectMCS, c.Attach, c.JoinAll, c.Login, c.Activate} {
				if err := step(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if err := tt.client(ctx, c); err != nil {
				t.Logf("client: %v", err)
			}
			assertRoute(t, <-done, tt.route)
		})
	}
}
