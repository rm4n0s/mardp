// Package rdp is an RDP server: it accepts RDP clients over TLS, runs the
// connection sequence of MS-RDPBCGR and gives the application a session it
// sends the picture and sound to and gets input from (SPEC §5, §6).
//
// It imports only the standard library and github.com/rm4n0s/errors, so it
// builds and is tested on any system.
package rdp

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/rdpsnd"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors NewServer and Serve return.
const (
	// TagCertificateMissing: a Config without a TLS certificate.
	TagCertificateMissing = "ServerCertificateMissing"
	// TagAuthenticatorMissing: a Config without an Authenticator.
	TagAuthenticatorMissing = "ServerAuthenticatorMissing"
	// TagHandlerMissing: a Config without a Handler.
	TagHandlerMissing = "ServerHandlerMissing"
	// TagAcceptFailed: the listener failed for another reason than the
	// server being stopped; the cause is kept.
	TagAcceptFailed = "ServerAcceptFailed"
)

// The limits of SPEC §5.1 that apply when a Config leaves them at 0.
const (
	DefaultMaxPending   = 8
	DefaultLoginTimeout = 10 * time.Second
)

// Config is what a Server needs.
type Config struct {
	// Certificate is the server's TLS certificate and key (SPEC §8.1).
	Certificate tls.Certificate
	// Authenticator decides who may log in (SPEC §8.2). It is required.
	Authenticator Authenticator
	// Handler gets the sessions (SPEC §6). It is required.
	Handler Handler
	// DesktopSize decides a session's desktop size from what the client
	// asked for (SPEC §9.2), before the server states it in Demand Active.
	// Both sides have to be from 1 to MaxDesktopSide. Nil gives the client
	// the size it asked for.
	DesktopSize func(c *Client) (width, height int)
	// Logger receives the server's log. Nil discards it.
	Logger *slog.Logger
	// MaxPending is how many connections may be open before they log in;
	// one more is closed as soon as it is accepted. 0 means
	// DefaultMaxPending.
	MaxPending int
	// LoginTimeout is how long a connection has, from TCP accept, to log
	// in; a connection still not logged in then is closed. 0 means
	// DefaultLoginTimeout.
	LoginTimeout time.Duration
	// AudioFormats are the sound formats the server offers on rdpsnd, the
	// one it prefers first (SPEC §10.1); a session takes the first the
	// client has. None means no sound.
	AudioFormats []AudioFormat
}

// Server is an RDP server. Make one with NewServer and run it with Serve.
type Server struct {
	tls          *tls.Config
	auth         Authenticator
	handler      Handler
	desktopSize  func(*Client) (int, int)
	log          *slog.Logger
	maxPending   int
	loginTimeout time.Duration
	open         openSession
	// dvcCapsWait is how long a client has to answer the capabilities of
	// dynamic channels: dvcCapsWait, shorter in tests.
	dvcCapsWait time.Duration
	// audioFormats are Config.AudioFormats, copied.
	audioFormats []AudioFormat
}

// openSession is the session a server has open: one at a time, a new one
// replaces it (SPEC D13).
type openSession struct {
	mu  sync.Mutex
	cur *liveSession
}

// liveSession is an open session; done is closed when it has ended and its
// handler has returned.
type liveSession struct {
	s    *Session
	done <-chan struct{}
}

// replace makes s the open session and returns the one it replaces, nil
// when there is none.
func (o *openSession) replace(s *Session, done <-chan struct{}) *liveSession {
	o.mu.Lock()
	defer o.mu.Unlock()
	prev := o.cur
	o.cur = &liveSession{s: s, done: done}
	return prev
}

// leave forgets s, unless another session has replaced it.
func (o *openSession) leave(s *Session) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cur != nil && o.cur.s == s {
		o.cur = nil
	}
}

// NewServer checks cfg and returns a server for it.
func NewServer(cfg Config) (*Server, error) {
	if len(cfg.Certificate.Certificate) == 0 || cfg.Certificate.PrivateKey == nil {
		return nil, errors.New(TagCertificateMissing, "the server needs a TLS certificate and its key")
	}
	if cfg.Authenticator == nil {
		return nil, errors.New(TagAuthenticatorMissing, "the server needs an Authenticator")
	}
	if cfg.Handler == nil {
		return nil, errors.New(TagHandlerMissing, "the server needs a Handler")
	}
	s := &Server{
		tls: &tls.Config{
			Certificates: []tls.Certificate{cfg.Certificate},
			MinVersion:   tls.VersionTLS12,
		},
		auth:         cfg.Authenticator,
		handler:      cfg.Handler,
		desktopSize:  cfg.DesktopSize,
		log:          cfg.Logger,
		maxPending:   cfg.MaxPending,
		loginTimeout: cfg.LoginTimeout,
		dvcCapsWait:  dvcCapsWait,
	}
	wf := make([]rdpsnd.Format, 0, len(cfg.AudioFormats))
	for _, f := range cfg.AudioFormats {
		s.audioFormats = append(s.audioFormats, f.clone())
		wf = append(wf, f.wire())
	}
	w := wire.NewWriter(nil)
	if err := rdpsnd.AppendServerFormats(&w, audioVersion, audioLastBlock, wf); err != nil {
		return nil, err
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.maxPending <= 0 {
		s.maxPending = DefaultMaxPending
	}
	if s.loginTimeout <= 0 {
		s.loginTimeout = DefaultLoginTimeout
	}
	return s, nil
}

// Serve accepts connections on l and serves each on goroutines of its own
// until ctx is canceled or l fails. It closes l, ends every connection and
// waits for them before it returns. It returns nil when ctx was canceled.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() {
		if err := l.Close(); err != nil {
			s.log.Debug("closing the listener", "err", err)
		}
	})
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	// One token for each connection that has not logged in yet.
	pending := make(chan struct{}, s.maxPending)
	s.log.Info("listening", "addr", l.Addr().String())
	for {
		nc, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				s.log.Info("stopped")
				return nil
			}
			return errors.NewErr(TagAcceptFailed, err).SetMetadata("addr", l.Addr().String())
		}
		log := s.log.With("remote", nc.RemoteAddr().String())
		select {
		case pending <- struct{}{}:
		default:
			log.Warn("connection refused: too many connections have not logged in", "max", s.maxPending)
			if err := nc.Close(); err != nil {
				log.Debug("closing the refused connection", "err", err)
			}
			continue
		}
		wg.Go(func() {
			s.handle(ctx, nc, log, sync.OnceFunc(func() { <-pending }))
		})
	}
}

// handle serves one connection and logs how it ended. release gives back the
// connection's place among those not logged in; it may be called earlier.
func (s *Server) handle(ctx context.Context, nc net.Conn, log *slog.Logger, release func()) {
	defer release()
	defer func() {
		if err := nc.Close(); err != nil {
			log.Debug("closing the connection", "err", err)
		}
	}()
	log.Debug("connection accepted")
	keepAlive(nc, log)
	err := s.serveConn(ctx, nc, log, release)
	switch {
	case err == nil:
		log.Info("connection closed")
	case ctx.Err() != nil:
		log.Info("connection closed: the server stopped")
	// A client that hangs up with data from the server unread resets
	// the connection.
	case errors.Is(err, io.EOF), errors.Is(err, syscall.ECONNRESET):
		log.Info("connection closed by the client", "err", err)
	case hasTag(err, TagLoginFailed):
		log.Warn("login failed", "err", err)
	case errors.Is(err, os.ErrDeadlineExceeded):
		log.Info("connection closed: no login in time", "timeout", s.loginTimeout, "err", err)
	default:
		log.Warn("connection ended", "err", err)
	}
}

// The TCP keep-alive of SPEC §5.4: a connection that goes silent is closed
// after about 30 seconds.
const (
	keepAliveIdle     = 15 * time.Second
	keepAliveInterval = 5 * time.Second
	keepAliveCount    = 3
)

// keepAlive turns on TCP keep-alive on nc, when it is a TCP connection. A
// connection where it fails is served all the same.
func keepAlive(nc net.Conn, log *slog.Logger) {
	tc, ok := nc.(*net.TCPConn)
	if !ok {
		return
	}
	if err := tc.SetKeepAliveConfig(net.KeepAliveConfig{
		Enable: true, Idle: keepAliveIdle, Interval: keepAliveInterval, Count: keepAliveCount,
	}); err != nil {
		log.Warn("TCP keep-alive could not be turned on", "err", err)
	}
}

// hasTag says whether err is an *errors.Error with tag.
func hasTag(err error, tag string) bool {
	appErr, ok := errors.FromError(err)
	return ok && appErr.Tag == tag
}
