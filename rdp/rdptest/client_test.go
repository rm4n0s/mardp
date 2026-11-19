package rdptest

import (
	"context"
	"crypto/tls"
	"io"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/gcc"
	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/tpkt"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/internal/x224"
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

type pair struct {
	server tls.Certificate
	client *tls.Config
}

// Two certificates, made once: one the client trusts, one it does not.
var testCerts = sync.OnceValues(func() ([2]pair, error) {
	var ps [2]pair
	for i := range ps {
		cert, cfg, err := Certificate()
		if err != nil {
			return ps, err
		}
		ps[i] = pair{cert, cfg}
	}
	return ps, nil
})

// fake is a scripted server that answers with what a test wants.
type fake struct {
	t    *testing.T
	cert tls.Certificate
	nc   net.Conn
	rw   io.ReadWriter
	buf  []byte
}

func (f *fake) read() []byte {
	k, b, err := tpkt.ReadFrame(f.rw, f.buf)
	if err != nil || k != tpkt.TPKT {
		f.t.Errorf("fake server: %v %v", k, err)
		return nil
	}
	return b[tpkt.HeaderLen:]
}

func (f *fake) write(b []byte) {
	if _, err := f.rw.Write(b); err != nil {
		f.t.Errorf("fake server: %v", err)
	}
}

func (f *fake) send(data bool, body func(w *wire.Writer) error) {
	var w wire.Writer
	off := tpkt.Begin(&w)
	if data {
		x224.WriteDataHeader(&w)
	}
	if err := body(&w); err != nil {
		f.t.Errorf("fake server: %v", err)
	}
	if err := tpkt.End(&w, off); err != nil {
		f.t.Errorf("fake server: %v", err)
	}
	f.write(w.Bytes())
}

func (f *fake) confirm(cc x224.ConnectionConfirm) {
	f.read()
	f.send(false, func(w *wire.Writer) error { x224.WriteConnectionConfirm(w, &cc); return nil })
}

func (f *fake) startTLS() {
	f.confirm(x224.ConnectionConfirm{Negotiation: x224.NegResponse, SelectedProtocol: x224.ProtocolSSL})
	tc := tls.Server(f.nc, &tls.Config{Certificates: []tls.Certificate{f.cert}, MinVersion: tls.VersionTLS12})
	if err := tc.Handshake(); err != nil {
		f.t.Logf("fake server: %v", err)
	}
	f.rw = tc
}

func (f *fake) connectResponse(result, gccResult uint8, sd gcc.ServerData) {
	f.read()
	var sw, gw wire.Writer
	if err := gcc.WriteServerData(&sw, &sd); err != nil {
		f.t.Error(err)
	}
	if err := gcc.WriteConferenceCreateResponse(&gw, &gcc.ConferenceCreateResponse{NodeID: 1001, Tag: 1, Result: gccResult, UserData: sw.Bytes()}); err != nil {
		f.t.Error(err)
	}
	f.send(true, func(w *wire.Writer) error {
		return mcs.WriteConnectResponse(w, &mcs.ConnectResponse{Result: result, UserData: gw.Bytes()})
	})
}

// goodServerData answers the two default channels.
var goodServerData = gcc.ServerData{
	Core:        gcc.ServerCore{Version: gcc.VersionRDP10},
	HasNetwork:  true,
	Network:     gcc.ServerNetwork{MCSChannelID: 1003, ChannelCount: 2, ChannelIDs: [gcc.MaxChannels]uint16{1004, 1005}},
	HasSecurity: true,
}

func (f *fake) attach(a mcs.AttachUserConfirm) {
	f.connectResponse(0, 0, goodServerData)
	f.read() // Erect Domain
	f.read() // Attach User
	f.send(true, func(w *wire.Writer) error { return mcs.WriteAttachUserConfirm(w, &a) })
}

// run starts script as the server of a TCP connection on 127.0.0.1 and
// returns the client's end.
func run(t *testing.T, cert tls.Certificate, script func(f *fake)) net.Conn {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		nc, err := l.Accept()
		if err != nil {
			t.Error(err)
			return
		}
		f := &fake{t: t, cert: cert, nc: nc, rw: nc, buf: make([]byte, tpkt.MaxLength)}
		script(f)
		if err := nc.Close(); err != nil {
			t.Error(err)
		}
	}()
	nc, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := nc.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
		<-done
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	})
	return nc
}

func TestClientErrors(t *testing.T) {
	certs, err := testCerts()
	if err != nil {
		t.Fatal(err)
	}
	trusted, other := certs[0], certs[1]
	tests := []struct {
		name   string
		cert   tls.Certificate
		script func(f *fake)
		route  string
	}{
		{"negotiation failure", trusted.server, func(f *fake) {
			f.confirm(x224.ConnectionConfirm{Negotiation: x224.NegFailure, FailureCode: x224.FailureHybridRequiredByServer})
		}, "Client.Negotiate." + TagNegotiationFailed},
		{"standard RDP selected", trusted.server, func(f *fake) {
			f.confirm(x224.ConnectionConfirm{Negotiation: x224.NegResponse, SelectedProtocol: x224.ProtocolRDP})
		}, "Client.Negotiate." + TagNegotiationFailed},
		{"fast-path confirm", trusted.server, func(f *fake) {
			f.read()
			f.write([]byte{0x00, 0x03, 0x00})
		}, "Client.Negotiate->Client.readTPKT." + TagFrameUnexpected},
		{"untrusted certificate", other.server, func(f *fake) { f.startTLS() }, "Client.StartTLS." + TagTLSHandshakeFailed},
		{"MCS refuses", trusted.server, func(f *fake) {
			f.startTLS()
			f.connectResponse(mcs.ResultDomainNotHierarchical, 0, goodServerData)
		}, "Client.ConnectMCS." + TagConnectRefused},
		{"GCC refuses", trusted.server, func(f *fake) {
			f.startTLS()
			f.connectResponse(0, gcc.ResultUserRejected, goodServerData)
		}, "Client.ConnectMCS." + TagConnectRefused},
		{"one channel of two", trusted.server, func(f *fake) {
			f.startTLS()
			sd := goodServerData
			sd.Network.ChannelCount = 1
			f.connectResponse(0, 0, sd)
		}, "Client.ConnectMCS." + TagChannelsMismatch},
		{"no network block", trusted.server, func(f *fake) {
			f.startTLS()
			sd := goodServerData
			sd.HasNetwork = false
			f.connectResponse(0, 0, sd)
		}, "Client.ConnectMCS." + TagChannelsMismatch},
		{"attach refused", trusted.server, func(f *fake) {
			f.startTLS()
			f.attach(mcs.AttachUserConfirm{Result: mcs.ResultTooManyUsers})
		}, "Client.Attach." + TagAttachRefused},
		{"join refused", trusted.server, func(f *fake) {
			f.startTLS()
			f.attach(mcs.AttachUserConfirm{HasInitiator: true, Initiator: 1006})
			f.read()
			f.send(true, func(w *wire.Writer) error {
				return mcs.WriteChannelJoinConfirm(w, &mcs.ChannelJoinConfirm{Result: mcs.ResultNoSuchChannel, Initiator: 1006, Requested: 1006})
			})
		}, "Client.JoinAll." + TagJoinRefused},
		{"join confirms another channel", trusted.server, func(f *fake) {
			f.startTLS()
			f.attach(mcs.AttachUserConfirm{HasInitiator: true, Initiator: 1006})
			f.read()
			f.send(true, func(w *wire.Writer) error {
				return mcs.WriteChannelJoinConfirm(w, &mcs.ChannelJoinConfirm{Initiator: 1006, Requested: 1006, HasChannelID: true, ChannelID: 1003})
			})
		}, "Client.JoinAll." + TagJoinRefused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			nc := run(t, tt.cert, tt.script)
			_, err := Connect(ctx, nc, Options{TLSConfig: trusted.client})
			assertRoute(t, err, tt.route)
		})
	}

	t.Run("deadline fails", func(t *testing.T) {
		nc := run(t, trusted.server, func(*fake) {})
		if err := nc.Close(); err != nil {
			t.Fatal(err)
		}
		c := New(nc, Options{})
		assertRoute(t, c.Negotiate(context.Background()), "Client.Negotiate->Client.watch."+TagDeadlineFailed)
	})
	t.Run("write fails", func(t *testing.T) {
		cc, sc := net.Pipe()
		defer func() {
			if err := sc.Close(); err != nil {
				t.Error(err)
			}
		}()
		c := New(failingWrites{cc}, Options{})
		assertRoute(t, c.Negotiate(context.Background()), "Client.Negotiate->Client.send."+TagWriteFailed)
		assertRoute(t, c.WriteFrame(context.Background(), []byte{1}), "Client.WriteFrame."+TagWriteFailed)
		if err := cc.Close(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("canceled read", func(t *testing.T) {
		nc := run(t, trusted.server, func(f *fake) { f.read() })
		ctx, cancel := context.WithCancel(context.Background())
		c := New(nc, Options{})
		time.AfterFunc(50*time.Millisecond, cancel)
		err := c.Negotiate(ctx)
		assertRoute(t, err, "Client.Negotiate->Client.readTPKT->ReadFrame."+tpkt.TagReadFailed)
	})
	t.Run("certificate fails", func(t *testing.T) {
		_, _, err := certificate(big.NewInt(-1))
		assertRoute(t, err, "certificate."+TagCertificateFailed)
	})
}

// failingWrites is a connection whose writes fail.
type failingWrites struct{ net.Conn }

func (failingWrites) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestClientData(t *testing.T) {
	c := New(nil, Options{Name: "a-very-long-client-name-of-more-than-15", Channels: []string{"cliprdr", "rdpsnd-and-more"}})
	c.Selected = x224.ProtocolSSL
	cd := c.ClientData()
	if got := cd.Core.Name(); got != "a-very-long-cli" {
		t.Errorf("name %q", got)
	}
	if cd.Core.ServerSelectedProtocol != x224.ProtocolSSL || cd.Core.DesktopWidth != DefaultWidth || cd.Core.DesktopHeight != DefaultHeight {
		t.Errorf("core %+v", cd.Core)
	}
	if cd.Network.ChannelCount != 2 || cd.Network.Channels[0].NameString() != "cliprdr" || cd.Network.Channels[1].NameString() != "rdpsnd-" {
		t.Errorf("network %+v", cd.Network)
	}
	var w wire.Writer
	if err := gcc.WriteClientData(&w, &cd); err != nil {
		t.Fatal(err)
	}
	if _, err := gcc.ParseClientData(w.Bytes()); err != nil {
		t.Fatal(err)
	}
}
