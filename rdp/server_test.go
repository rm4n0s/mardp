package rdp

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/gcc"
	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/sec"
	"github.com/rm4n0s/mardp/rdp/internal/tpkt"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/internal/x224"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

func assertRoute(t *testing.T, err error, route string) {
	t.Helper()
	appErr, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("expected *errors.Error, got %T: %v", err, err)
	}
	if !appErr.HasRoute(route) {
		t.Errorf("unexpected failure path: %s, want %s", appErr.Route(), route)
	}
}

// The credentials the test servers accept.
const (
	testUser     = "alice"
	testPassword = "correct horse battery staple"
)

// testCert is made once: an RSA key takes a while.
var testCert = sync.OnceValues(func() (certPair, error) {
	cert, client, err := rdptest.Certificate()
	return certPair{cert, client}, err
})

type certPair struct {
	server tls.Certificate
	client *tls.Config
}

func certs(t *testing.T) certPair {
	t.Helper()
	p, err := testCert()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// recorder is a slog.Handler that keeps every record.
type recorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *recorder) WithAttrs([]slog.Attr) slog.Handler       { return r }
func (r *recorder) WithGroup(string) slog.Handler            { return r }
func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec.Clone())
	return nil
}

// find returns the first record with message msg, waiting up to 2 s for it:
// the server logs from its own goroutines.
func (r *recorder) find(t *testing.T, msg string) slog.Record {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.mu.Lock()
		for _, rec := range r.records {
			if rec.Message == msg {
				r.mu.Unlock()
				return rec
			}
		}
		r.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("no log record %q", msg)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

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

type testServer struct {
	addr string
	log  *recorder
	cfg  *tls.Config
}

// start runs a server on 127.0.0.1 until the test ends, and checks that
// Serve then returns nil.
func start(t *testing.T, cfg Config) *testServer {
	t.Helper()
	p := certs(t)
	rec := &recorder{}
	cfg.Certificate = p.server
	cfg.Logger = slog.New(rec)
	if cfg.Authenticator == nil {
		cfg.Authenticator = StaticAuthenticator(testUser, testPassword)
	}
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, l) }()
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
	return &testServer{addr: l.Addr().String(), log: rec, cfg: p.client}
}

func (ts *testServer) dial(t *testing.T) net.Conn {
	t.Helper()
	nc, err := net.Dial("tcp", ts.addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := nc.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	})
	return nc
}

func (ts *testServer) connect(t *testing.T, opts rdptest.Options) (*rdptest.Client, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opts.TLSConfig = ts.cfg
	return rdptest.Connect(ctx, ts.dial(t), opts)
}

// login connects and logs in with opts' credentials, testUser and
// testPassword when it has none.
func (ts *testServer) login(t *testing.T, opts rdptest.Options) (*rdptest.Client, net.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if opts.User == "" && opts.Password == "" {
		opts.User, opts.Password = testUser, testPassword
	}
	opts.TLSConfig = ts.cfg
	nc := ts.dial(t)
	c, err := rdptest.Connect(ctx, nc, opts)
	if err != nil {
		return c, nc, err
	}
	return c, nc, c.Login(ctx)
}

// text renders every record as JSON, as a real log would hold them.
func (r *recorder) text(t *testing.T) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var buf strings.Builder
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	for _, rec := range r.records {
		if err := h.Handle(context.Background(), rec); err != nil {
			t.Fatal(err)
		}
	}
	return buf.String()
}

// stillOpen checks that the server has not closed nc after d.
func stillOpen(t *testing.T, nc net.Conn, d time.Duration) {
	t.Helper()
	if err := nc.SetReadDeadline(time.Now().Add(d)); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err := nc.Read(b[:]); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("connection closed: %v", err)
	}
}

// closedWithin checks that the server closes nc within d.
func closedWithin(t *testing.T, nc net.Conn, d time.Duration) {
	t.Helper()
	if err := nc.SetReadDeadline(time.Now().Add(d)); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err := nc.Read(b[:]); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("connection not closed within %v: %v", d, err)
	}
}

func TestServerDomainParametersAreMicrosofts(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "ms-rdpbcgr", "connect-response.bin"))
	if err != nil {
		t.Fatal(err)
	}
	cr, err := mcs.ParseConnectResponse(b[7:])
	if err != nil {
		t.Fatal(err)
	}
	if cr.Parameters != serverDomainParameters {
		t.Errorf("Microsoft's %+v, the server's %+v", cr.Parameters, serverDomainParameters)
	}
}

func TestConnectionSequence(t *testing.T) {
	ts := start(t, Config{})
	many := make([]string, gcc.MaxChannels)
	for i := range many {
		many[i] = string(rune('a'+i%26)) + "chan"
	}
	tests := []struct {
		name     string
		opts     rdptest.Options
		channels int
	}{
		{"FreeRDP's protocols, two channels", rdptest.Options{}, 2},
		{"TLS only, no channels", rdptest.Options{Protocols: x224.ProtocolSSL, Channels: []string{}}, 0},
		{"31 channels", rdptest.Options{Channels: many}, 31},
		{"no network block", rdptest.Options{Channels: []string{}, EditClientData: func(cd *gcc.ClientData) { cd.HasNetwork = false }}, 0},
		{"RDP 4.0 core block", rdptest.Options{EditClientData: func(cd *gcc.ClientData) { cd.Core.OptionalFields = 0 }}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := ts.connect(t, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if c.Selected != x224.ProtocolSSL {
				t.Errorf("selected %#x", c.Selected)
			}
			wantProtocols := tt.opts.Protocols
			if wantProtocols == 0 {
				wantProtocols = rdptest.DefaultProtocols
			}
			if c.Server.Core != (gcc.ServerCore{Version: gcc.VersionRDP10, OptionalFields: 2, ClientRequestedProtocols: wantProtocols}) {
				t.Errorf("server core %+v", c.Server.Core)
			}
			if !c.Server.HasSecurity || c.Server.Security.EncryptionMethod != 0 || c.Server.Security.EncryptionLevel != 0 {
				t.Errorf("server security %v %+v", c.Server.HasSecurity, c.Server.Security)
			}
			if c.Parameters != serverDomainParameters {
				t.Errorf("domain parameters %+v", c.Parameters)
			}
			want := []uint16{uint16(ioChannelID + tt.channels + 1), ioChannelID}
			for i := range tt.channels {
				want = append(want, uint16(ioChannelID+1+i))
			}
			if c.UserID != want[0] || !slices.Equal(c.Joined, want) {
				t.Errorf("user %d, joined %v, want %v", c.UserID, c.Joined, want)
			}
		})
	}
	rec := ts.log.find(t, "client data")
	if attr(rec, "name").String() != rdptest.DefaultName || attr(rec, "width").Uint64() != rdptest.DefaultWidth {
		t.Errorf("client data logged as %v", rec)
	}
	ts.log.find(t, "channels joined")
}

func TestUnknownChannelRefused(t *testing.T) {
	ts := start(t, Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := rdptest.New(ts.dial(t), rdptest.Options{TLSConfig: ts.cfg})
	for _, step := range []func(context.Context) error{c.Negotiate, c.StartTLS, c.ConnectMCS, c.Attach} {
		if err := step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uint16{999, ioChannelID + 4} { // after the user channel
		cf, err := c.Join(ctx, c.UserID, id)
		if err != nil {
			t.Fatal(err)
		}
		if cf.Result != mcs.ResultNoSuchChannel || cf.HasChannelID || cf.Requested != id || cf.Initiator != c.UserID {
			t.Errorf("join of %d: %+v", id, cf)
		}
	}
	// Joining a channel twice is answered again, and counted once.
	if _, err := c.Join(ctx, c.UserID, ioChannelID); err != nil {
		t.Fatal(err)
	}
	if err := c.JoinAll(ctx); err != nil {
		t.Fatal(err)
	}
	ts.log.find(t, "channels joined")
}

func TestTLSRequired(t *testing.T) {
	ts := start(t, Config{})
	tests := []struct {
		name string
		opts rdptest.Options
	}{
		{"no negotiation (/sec:rdp)", rdptest.Options{NoNegotiation: true}},
		{"CredSSP only", rdptest.Options{Protocols: x224.ProtocolHybrid}},
		{"CredSSP and RDSTLS", rdptest.Options{Protocols: x224.ProtocolHybrid | x224.ProtocolHybridEx | x224.ProtocolRDSTLS}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			nc := ts.dial(t)
			opts := tt.opts
			opts.TLSConfig = ts.cfg
			c := rdptest.New(nc, opts)
			err := c.Negotiate(ctx)
			assertRoute(t, err, "Client.Negotiate."+rdptest.TagNegotiationFailed)
			if appErr, ok := errors.FromError(err); !ok || appErr.Metadata["failure"] != x224.FailureSSLRequiredByServer {
				t.Errorf("failure code %v", err)
			}
			closedWithin(t, nc, 2*time.Second)
		})
	}
	rec := ts.log.find(t, "connection ended")
	err, _ := attr(rec, "err").Any().(error)
	assertRoute(t, err, "Server.serveConn->conn.negotiate."+TagTLSRequired)
}

func TestPendingLimit(t *testing.T) {
	ts := start(t, Config{})
	var clients []*rdptest.Client
	for i := range DefaultMaxPending {
		c, err := ts.connect(t, rdptest.Options{})
		if err != nil {
			t.Fatalf("connection %d: %v", i+1, err)
		}
		clients = append(clients, c)
	}
	closedWithin(t, ts.dial(t), 2*time.Second)
	rec := ts.log.find(t, "connection refused: too many connections have not logged in")
	if attr(rec, "max").Int64() != DefaultMaxPending {
		t.Errorf("logged %v", rec)
	}
	// A connection that ends gives its place back.
	if err := clients[0].Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := ts.connect(t, rdptest.Options{})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no place after a connection ended: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLoginTimeout(t *testing.T) {
	const timeout = 300 * time.Millisecond
	ts := start(t, Config{LoginTimeout: timeout})
	tests := []struct {
		name string
		run  func(t *testing.T) net.Conn
	}{
		{"silent", func(t *testing.T) net.Conn { return ts.dial(t) }},
		{"half a header", func(t *testing.T) net.Conn {
			nc := ts.dial(t)
			if _, err := nc.Write([]byte{3, 0}); err != nil {
				t.Fatal(err)
			}
			return nc
		}},
		{"no TLS handshake", func(t *testing.T) net.Conn {
			nc := ts.dial(t)
			if err := rdptest.New(nc, rdptest.Options{}).Negotiate(context.Background()); err != nil {
				t.Fatal(err)
			}
			return nc
		}},
		{"no Client Info after the joins", func(t *testing.T) net.Conn {
			nc := ts.dial(t)
			if _, err := rdptest.Connect(context.Background(), nc, rdptest.Options{TLSConfig: ts.cfg}); err != nil {
				t.Fatal(err)
			}
			return nc
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			begin := time.Now()
			nc := tt.run(t)
			closedWithin(t, nc, 3*time.Second)
			if d := time.Since(begin); d < timeout-50*time.Millisecond {
				t.Errorf("closed after %v, before the timeout", d)
			}
		})
	}
	ts.log.find(t, "connection closed: no login in time")
}

func TestServeStops(t *testing.T) {
	p := certs(t)
	s, err := NewServer(Config{Certificate: p.server, Authenticator: StaticAuthenticator(testUser, testPassword)})
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, l) }()
	nc, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := nc.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := rdptest.Connect(ctx, nc, rdptest.Options{TLSConfig: p.client}); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
	closedWithin(t, nc, time.Second)
	if _, err := net.Dial("tcp", l.Addr().String()); err == nil {
		t.Error("the listener is still open")
	}
}

// failingListener's Accept fails at once.
type failingListener struct{ net.Listener }

func (failingListener) Accept() (net.Conn, error) { return nil, io.ErrClosedPipe }

// oneDeadline is a connection on which only the first SetDeadline works.
type oneDeadline struct {
	net.Conn
	calls int
}

func (o *oneDeadline) SetDeadline(t time.Time) error {
	o.calls++
	if o.calls > 1 {
		return io.ErrClosedPipe
	}
	return o.Conn.SetDeadline(t)
}

// tcpPair returns both ends of a TCP connection on 127.0.0.1.
func tcpPair(t *testing.T) (client, server net.Conn) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	}()
	client, err = net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if server, err = l.Accept(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, c := range []net.Conn{client, server} {
			if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				t.Error(err)
			}
		}
	})
	return client, server
}

func TestErrors(t *testing.T) {
	p := certs(t)
	s, err := NewServer(Config{Certificate: p.server, Authenticator: StaticAuthenticator(testUser, testPassword)})
	if err != nil {
		t.Fatal(err)
	}
	upTo := func(n int) func(ctx context.Context, c *rdptest.Client) error {
		return func(ctx context.Context, c *rdptest.Client) error {
			for _, step := range []func(context.Context) error{c.Negotiate, c.StartTLS, c.ConnectMCS, c.Attach, c.JoinAll, c.Login}[:n] {
				if err := step(ctx); err != nil {
					return err
				}
			}
			return nil
		}
	}
	then := func(first, second func(ctx context.Context, c *rdptest.Client) error) func(ctx context.Context, c *rdptest.Client) error {
		return func(ctx context.Context, c *rdptest.Client) error {
			if err := first(ctx, c); err != nil {
				return err
			}
			return second(ctx, c)
		}
	}
	raw := func(b ...byte) func(ctx context.Context, c *rdptest.Client) error {
		return func(ctx context.Context, c *rdptest.Client) error { return c.WriteFrame(ctx, b) }
	}
	data := func(body func(w *wire.Writer) error) func(ctx context.Context, c *rdptest.Client) error {
		return func(ctx context.Context, c *rdptest.Client) error { return c.SendData(ctx, body) }
	}
	tests := []struct {
		name   string
		opts   rdptest.Options
		client func(ctx context.Context, c *rdptest.Client) error
		route  string
	}{
		{"fast-path first", rdptest.Options{}, raw(0x04, 0x03, 0x00), "Server.serveConn->conn.negotiate->conn.readTPKT." + TagFrameUnexpected},
		{"no TLS offered", rdptest.Options{NoNegotiation: true}, upTo(1), "Server.serveConn->conn.negotiate." + TagTLSRequired},
		{"not TLS after the confirm", rdptest.Options{}, then(upTo(1), raw(3, 0, 0, 7, 2, 0xf0, 0x80)),
			"Server.serveConn->conn.startTLS." + TagTLSHandshakeFailed},
		{"selected protocol changed", rdptest.Options{EditClientData: func(cd *gcc.ClientData) { cd.Core.ServerSelectedProtocol = x224.ProtocolRDP }},
			upTo(3), "Server.serveConn->conn.connectMCS." + TagProtocolMismatch},
		{"join as another user", rdptest.Options{}, then(upTo(4), func(ctx context.Context, c *rdptest.Client) error {
			_, err := c.Join(ctx, c.UserID+1, ioChannelID)
			return err
		}), "Server.serveConn->conn.joinChannels." + TagInitiatorInvalid},
		{"fast-path after TLS", rdptest.Options{}, then(upTo(2), raw(0x04, 0x03, 0x00)),
			"Server.serveConn->conn.connectMCS->conn.readData->conn.readTPKT." + TagFrameUnexpected},
		{"disconnect request instead of data", rdptest.Options{}, then(upTo(2), raw(3, 0, 0, 7, 2, 0x80, 0x80)),
			"Server.serveConn->conn.connectMCS->conn.readData->ReadDataHeader." + x224.TagUnexpectedCode},
		{"attach before erect domain", rdptest.Options{}, then(upTo(3), data(func(w *wire.Writer) error { mcs.WriteAttachUserRequest(w); return nil })),
			"Server.serveConn->conn.attachUser->ReadErectDomainRequest." + mcs.TagUnexpectedPDU},
		{"wrong password", rdptest.Options{User: testUser, Password: "wrong"}, upTo(6), "Server.serveConn->conn.login." + TagLoginFailed},
		{"client info on a static channel", rdptest.Options{}, then(upTo(5), data(func(w *wire.Writer) error {
			return mcs.WriteSendDataRequest(w, &mcs.SendData{Initiator: ioChannelID + 3, ChannelID: ioChannelID + 1, Data: []byte{0x40, 0, 0, 0}})
		})), "Server.serveConn->conn.login." + TagChannelInvalid},
		{"client info from another user", rdptest.Options{}, then(upTo(5), data(func(w *wire.Writer) error {
			return mcs.WriteSendDataRequest(w, &mcs.SendData{Initiator: ioChannelID + 4, ChannelID: ioChannelID, Data: []byte{0x40, 0, 0, 0}})
		})), "Server.serveConn->conn.login." + TagChannelInvalid},
		{"client info encrypted", rdptest.Options{}, then(upTo(5), data(func(w *wire.Writer) error {
			return mcs.WriteSendDataRequest(w, &mcs.SendData{Initiator: ioChannelID + 3, ChannelID: ioChannelID, Data: []byte{0x48, 0, 0, 0}})
		})), "Server.serveConn->conn.login->ParseClientInfo->readHeader." + sec.TagEncrypted},
		{"join request instead of client info", rdptest.Options{}, then(upTo(5), data(func(w *wire.Writer) error {
			return mcs.WriteChannelJoinRequest(w, &mcs.ChannelJoinRequest{Initiator: ioChannelID + 3, ChannelID: ioChannelID})
		})), "Server.serveConn->conn.login->ReadSendDataRequest->readSendData." + mcs.TagUnexpectedPDU},
		{"connection request cut", rdptest.Options{}, then(raw(3, 0, 0, 20, 14, 0xe0), func(_ context.Context, c *rdptest.Client) error { return c.CloseWrite() }),
			"Server.serveConn->conn.negotiate->conn.readTPKT->ReadFrame." + tpkt.TagReadFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc, sc := tcpPair(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				err := s.serveConn(ctx, sc, s.log, func() {})
				if cerr := sc.Close(); cerr != nil {
					t.Error(cerr)
				}
				done <- err
			}()
			opts := tt.opts
			opts.TLSConfig = p.client
			c := rdptest.New(cc, opts)
			if err := tt.client(ctx, c); err != nil {
				t.Logf("client: %v", err)
			}
			assertRoute(t, <-done, tt.route)
		})
	}

	t.Run("write fails", func(t *testing.T) {
		cc, sc := net.Pipe()
		done := make(chan error, 1)
		go func() { done <- s.serveConn(context.Background(), sc, s.log, func() {}) }()
		var w wire.Writer
		cr := x224.ConnectionRequest{HasNegReq: true, RequestedProtocols: x224.ProtocolSSL}
		off := w.Len()
		w.Write([]byte{3, 0, 0, 0})
		if err := x224.WriteConnectionRequest(&w, &cr); err != nil {
			t.Fatal(err)
		}
		if err := w.PatchU16BE(off+2, uint16(w.Len())); err != nil {
			t.Fatal(err)
		}
		if _, err := cc.Write(w.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err := cc.Close(); err != nil {
			t.Fatal(err)
		}
		assertRoute(t, <-done, "Server.serveConn->conn.negotiate->conn.send."+TagWriteFailed)
	})
	t.Run("deadline fails", func(t *testing.T) {
		cc, sc := net.Pipe()
		for _, c := range []net.Conn{cc, sc} {
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
		}
		assertRoute(t, s.serveConn(context.Background(), sc, s.log, func() {}), "Server.serveConn."+TagDeadlineFailed)
	})
	t.Run("no certificate", func(t *testing.T) {
		_, err := NewServer(Config{Authenticator: StaticAuthenticator("u", "p")})
		assertRoute(t, err, "NewServer."+TagCertificateMissing)
	})
	t.Run("no authenticator", func(t *testing.T) {
		_, err := NewServer(Config{Certificate: p.server})
		assertRoute(t, err, "NewServer."+TagAuthenticatorMissing)
	})
	t.Run("deadline cannot be cleared", func(t *testing.T) {
		cc, sc := tcpPair(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- s.serveConn(ctx, &oneDeadline{Conn: sc}, s.log, func() {}) }()
		c := rdptest.New(cc, rdptest.Options{TLSConfig: p.client, User: testUser, Password: testPassword})
		if err := upTo(6)(ctx, c); err != nil {
			t.Logf("client: %v", err)
		}
		assertRoute(t, <-done, "Server.serveConn->conn.login."+TagClearDeadlineFailed)
	})
	t.Run("accept fails", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := l.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				t.Error(err)
			}
		}()
		err = s.Serve(context.Background(), failingListener{l})
		assertRoute(t, err, "Server.Serve."+TagAcceptFailed)
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Errorf("cause %v", err)
		}
	})
}

func TestLogin(t *testing.T) {
	const timeout = 300 * time.Millisecond
	ts := start(t, Config{LoginTimeout: timeout})
	const wrong = "a wrong password, never logged"
	tests := []struct {
		name      string
		opts      rdptest.Options
		reason    string // "" for a login
		errorInfo uint32
	}{
		{"right password", rdptest.Options{}, "", 0},
		{"wrong password", rdptest.Options{User: testUser, Password: wrong}, reasonWrongCredentials, sec.ErrInfoFreshCredentialsRequired},
		{"wrong user", rdptest.Options{User: "bob", Password: testPassword}, reasonWrongCredentials, sec.ErrInfoFreshCredentialsRequired},
		{"no password", rdptest.Options{User: testUser}, reasonNoCredentials, sec.ErrInfoFreshCredentialsRequired},
		{"no user", rdptest.Options{Password: wrong}, reasonNoCredentials, sec.ErrInfoFreshCredentialsRequired},
		{"smart card PIN", rdptest.Options{EditClientInfo: func(ci *sec.ClientInfo) { ci.Flags |= sec.InfoPasswordIsSCPin }},
			reasonSmartCard, sec.ErrInfoFreshCredentialsRequired},
		{"ANSI strings", rdptest.Options{EditClientInfo: func(ci *sec.ClientInfo) { ci.Flags &^= sec.InfoUnicode }}, "", 0},
		{"no Set Error Info support", rdptest.Options{User: testUser, Password: wrong,
			EditClientData: func(cd *gcc.ClientData) { cd.Core.EarlyCapabilityFlags &^= gcc.EarlySupportErrInfoPDU }}, reasonWrongCredentials, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, nc, err := ts.login(t, tt.opts)
			if tt.reason == "" {
				if err != nil {
					t.Fatal(err)
				}
				// The login deadline no longer applies.
				stillOpen(t, nc, 2*timeout)
				return
			}
			assertRoute(t, err, "Client.Login."+rdptest.TagLoginRefused)
			appErr, _ := errors.FromError(err)
			if appErr.Metadata["errorInfo"] != tt.errorInfo || appErr.Metadata["reason"] != mcs.ReasonProviderInitiated {
				t.Errorf("refused with %v", appErr.Metadata)
			}
			closedWithin(t, nc, 2*time.Second)
		})
	}
	ts.log.find(t, "logged in")
	rec := ts.log.find(t, "login failed")
	err, _ := attr(rec, "err").Any().(error)
	assertRoute(t, err, "Server.serveConn->conn.login."+TagLoginFailed)
	ts.log.find(t, "client info")
	log := ts.log.text(t)
	for _, pw := range []string{testPassword, wrong} {
		if strings.Contains(log, pw) {
			t.Errorf("the log holds the password %q", pw)
		}
	}
	for _, want := range []string{`"reason":"wrong credentials"`, `"reason":"no credentials"`, `"user":"bob"`, `"password":true`} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %s", want)
		}
	}
}

func TestLoggedInLeavesThePendingCount(t *testing.T) {
	ts := start(t, Config{MaxPending: 2})
	for i := range 5 {
		if _, _, err := ts.login(t, rdptest.Options{}); err != nil {
			t.Fatalf("login %d: %v", i+1, err)
		}
	}
}

func TestLoginRequestHidesThePassword(t *testing.T) {
	r := LoginRequest{User: "u", Domain: "d", Password: testPassword, Remote: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}}
	var buf strings.Builder
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "req", r)
	for _, s := range []string{fmt.Sprint(r), fmt.Sprintf("%+v", r), fmt.Sprintf("%#v", r), buf.String()} {
		if strings.Contains(s, testPassword) || !strings.Contains(s, "127.0.0.1:9") {
			t.Errorf("printed as %s", s)
		}
	}
	if (LoginRequest{}).String() != "LoginRequest{User: Domain: Remote:}" {
		t.Errorf("empty request %s", LoginRequest{})
	}
}

func TestStaticAuthenticator(t *testing.T) {
	auth := StaticAuthenticator("u", "pw")
	tests := []struct {
		user, password string
		want           bool
	}{
		{"u", "pw", true},
		{"u", "pw2", false},
		{"u", "", false},
		{"U", "pw", false},
		{"", "", false},
	}
	for _, tt := range tests {
		if got := auth(LoginRequest{User: tt.user, Password: tt.password}); got != tt.want {
			t.Errorf("%q %q: %v", tt.user, tt.password, got)
		}
	}
}
