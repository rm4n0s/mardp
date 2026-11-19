package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"runtime/debug"
	"strconv"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/mardp/internal/config"
	"github.com/rm4n0s/mardp/internal/host"
	"github.com/rm4n0s/mardp/rdp"
)

// The failures of serve.
const (
	// TagNoPassword: no user name or password is set (SPEC §8.2).
	TagNoPassword = "MardpNoPassword"
	// TagScreenRecordingMissing: Screen Recording is not granted (SPEC §7.1).
	TagScreenRecordingMissing = "MardpScreenRecordingMissing"
	// TagListenFailed: the server's address could not be listened on; the
	// cause is kept.
	TagListenFailed = "MardpListenFailed"
)

// The exit codes.
const (
	exitOK     = 0
	exitFailed = 1
	exitUsage  = 2
)

// run is mardp's main: it parses args and runs what they ask for, until ctx
// ends. It returns the exit code.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, m machine) int {
	fs := flag.NewFlagSet("mardp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	headless := fs.Bool("headless", false, "run the server without a window, with the settings of the app")
	showVersion := fs.Bool("version", false, "print the version and exit")
	logFile := fs.String("log-file", "", "write the log to this file instead of the standard error")
	viewOnly := fs.Bool("view-only", false, "drop the client's keyboard and mouse, as without Accessibility")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 {
		complain(stderr, "mardp: unexpected arguments: %q\n", fs.Args())
		return exitUsage
	}
	if *showVersion {
		if _, err := fmt.Fprintln(stdout, "mardp", version()); err != nil {
			complain(stderr, "mardp: printing the version: %v\n", err)
			return exitFailed
		}
		return exitOK
	}
	out := stderr
	if *logFile != "" {
		f, err := os.OpenFile(*logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, config.FileMode)
		if err != nil {
			complain(stderr, "mardp: the log file: %v\n", err)
			return exitFailed
		}
		defer func() {
			if err := f.Close(); err != nil {
				complain(stderr, "mardp: closing the log file: %v\n", err)
			}
		}()
		out = f
	}
	log := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log.Info("mardp starting", "version", version(), "headless", *headless)
	runner := window
	if *headless {
		runner = serve
	}
	if err := runner(ctx, getenv, m, *viewOnly, log); err != nil {
		log.Error("mardp stopped", "err", err)
		return exitFailed
	}
	return exitOK
}

// complain writes a message for the user to stderr. A failure to write it
// has no other place to go; the exit code still says what happened.
func complain(stderr io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(stderr, format, args...)
}

// version is the module's version that the build recorded.
func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "unknown"
}

// openFolder opens mardp's folder and loads its settings.
func openFolder(ctx context.Context, getenv func(string) string, log *slog.Logger) (*config.Folder, config.Settings, error) {
	dir, err := config.Dir(getenv)
	if err != nil {
		return nil, config.Settings{}, err
	}
	folder, err := config.Open(ctx, dir, log)
	if err != nil {
		return nil, config.Settings{}, err
	}
	s, err := folder.Load(ctx)
	if err != nil {
		return nil, config.Settings{}, err
	}
	return folder, s, nil
}

// serve runs the server with the settings of mardp's folder until ctx ends
// (SPEC §13).
func serve(ctx context.Context, getenv func(string) string, m machine, viewOnly bool, log *slog.Logger) error {
	folder, s, err := openFolder(ctx, getenv, log)
	if err != nil {
		return err
	}
	sys, err := m.system()
	if err != nil {
		return err
	}
	srv, err := prepare(ctx, folder, func() config.Settings { return s }, sys, m, viewOnly, observer{}, log)
	if err != nil {
		return err
	}
	return srv.srv.Serve(ctx, srv.l)
}

// observer is told what a server does, for the window's recent events
// (SPEC §12.2); a nil func is told nothing.
type observer struct {
	// session is told when a session opens and when it ends.
	session func(host.Event)
	// refused is told of a refused login and of what the guard decided.
	refused func(user, remote string, res config.LoginResult)
}

// server is a server that listens, ready to serve.
type server struct {
	srv *rdp.Server
	l   net.Listener
	hst *host.Host
	// fingerprint is the certificate's.
	fingerprint string
	// post says that the client's input is posted; false is view only.
	post bool
	// settings are the settings it was made of.
	settings config.Settings
}

// prepare makes a server of the settings and listens where they say: it
// checks that a user name and a password are set and that Screen Recording
// is granted, and makes the session's parts of m, view-only without
// Accessibility or with viewOnly (SPEC §7.1, §7.3, §8, §13). It unmutes
// the output a mardp that ended left muted, and, when the sound plays on
// the client only, gives the host the output to mute (SPEC §10.1). Logins are
// checked against what settings returns at the time; obs is told of the
// sessions and the refused logins.
func prepare(ctx context.Context, folder *config.Folder, settings func() config.Settings, sys host.System,
	m machine, viewOnly bool, obs observer, log *slog.Logger) (*server, error) {
	s := settings()
	if !s.Ready() {
		return nil, errors.New(TagNoPassword, "no user name and password are set: set them in mardp's window")
	}
	if !sys.Granted(host.ScreenRecording) {
		return nil, errors.New(TagScreenRecordingMissing,
			"mardp may not record the screen: grant it Screen Recording in System Settings, Privacy & Security")
	}
	post := !viewOnly && sys.Granted(host.Accessibility)
	switch {
	case viewOnly:
		log.Info("sessions are view-only (-view-only)")
	case !post:
		log.Warn("Accessibility is not granted: sessions are view-only until the server starts again with it")
	}
	cert, created, err := folder.Certificate(ctx)
	if err != nil {
		return nil, err
	}
	log.Info("certificate", "fingerprint", cert.Fingerprint, "created", created)
	out, _ := sys.(host.Output)
	if out != nil {
		host.RecoverOutput(ctx, out, folder, log)
	}
	parts, err := m.parts(sys, s, post, log)
	if err != nil {
		return nil, err
	}
	if out != nil && parts.Sound != nil && s.Sound == config.SoundClient {
		parts.Output = out
	}
	hst, err := host.New(parts, host.Options{VideoFPS: s.MaxFPS, MaxBitrate: s.MaxBitrate, MaxHeight: s.MaxHeight,
		MuteMarker: folder, Logger: log, OnEvent: obs.session})
	if err != nil {
		return nil, err
	}
	srv, err := rdp.NewServer(rdp.Config{
		Certificate:   cert.TLS,
		Authenticator: authenticator(settings, config.NewGuard(nil), obs.refused, log),
		Handler:       hst,
		DesktopSize:   hst.DesktopSize,
		AudioFormats:  hst.AudioFormats(),
		Logger:        log,
	})
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(s.Address, strconv.Itoa(s.Port))
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, errors.NewErr(TagListenFailed, err).SetMetadata("addr", addr)
	}
	return &server{srv: srv, l: l, hst: hst, fingerprint: cert.Fingerprint, post: post, settings: s}, nil
}

// authenticator checks a login against the settings that settings returns,
// with guard making an address wait after failed logins (SPEC §8.2). It logs
// a refusal, never the password, and tells refused of it; refused may be nil.
func authenticator(settings func() config.Settings, guard *config.Guard, refused func(user, remote string, res config.LoginResult), log *slog.Logger) rdp.Authenticator {
	return func(r rdp.LoginRequest) bool {
		remote := ""
		if r.Remote != nil {
			remote = r.Remote.String()
		}
		res := guard.Login(remote, func() bool { return settings().CheckLogin(r.User, r.Password) })
		if !res.OK {
			log.Warn("login refused", "user", r.User, "remote", remote, "failures", res.Failures, "wait", res.Wait)
			if refused != nil {
				refused(r.User, remote, res)
			}
		}
		return res.OK
	}
}
