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
	"time"

	"github.com/rm4n0s/errors"
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
	err := s.serveConn(ctx, nc, log, release)
	switch {
	case err == nil:
		log.Info("connection closed")
	case ctx.Err() != nil:
		log.Info("connection closed: the server stopped")
	case errors.Is(err, io.EOF):
		log.Info("connection closed by the client", "err", err)
	case hasTag(err, TagLoginFailed):
		log.Warn("login failed", "err", err)
	case errors.Is(err, os.ErrDeadlineExceeded):
		log.Info("connection closed: no login in time", "timeout", s.loginTimeout, "err", err)
	default:
		log.Warn("connection ended", "err", err)
	}
}

// hasTag says whether err is an *errors.Error with tag.
func hasTag(err error, tag string) bool {
	appErr, ok := errors.FromError(err)
	return ok && appErr.Tag == tag
}
