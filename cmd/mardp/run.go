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
	if !*headless {
		// The window is step 2.8 of docs/IMPLEMENTATION.md.
		complain(stderr, "mardp: the app's window is not built yet; run mardp -headless\n")
		return exitUsage
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
	log.Info("mardp starting", "version", version())
	if err := serve(ctx, getenv, m, *viewOnly, log); err != nil {
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

// serve runs the server with the settings of mardp's folder until ctx ends:
// it checks that a password is set and Screen Recording granted, makes the
// session's parts of m, view-only without Accessibility or with viewOnly,
// and listens where the settings say (SPEC §7.1, §7.3, §8, §13).
func serve(ctx context.Context, getenv func(string) string, m machine, viewOnly bool, log *slog.Logger) error {
	dir, err := config.Dir(getenv)
	if err != nil {
		return err
	}
	folder, err := config.Open(ctx, dir, log)
	if err != nil {
		return err
	}
	s, err := folder.Load(ctx)
	if err != nil {
		return err
	}
	if !s.Ready() {
		return errors.New(TagNoPassword, "no user name and password are set: set them in mardp's window", "folder", dir)
	}
	sys, err := m.system()
	if err != nil {
		return err
	}
	if !sys.Granted(host.ScreenRecording) {
		return errors.New(TagScreenRecordingMissing,
			"mardp may not record the screen: grant it Screen Recording in System Settings, Privacy & Security")
	}
	post := !viewOnly && sys.Granted(host.Accessibility)
	switch {
	case viewOnly:
		log.Info("sessions are view-only (-view-only)")
	case !post:
		log.Warn("Accessibility is not granted: sessions are view-only until mardp starts again with it")
	}
	cert, created, err := folder.Certificate(ctx)
	if err != nil {
		return err
	}
	log.Info("certificate", "fingerprint", cert.Fingerprint, "created", created)
	parts, err := m.parts(sys, s, post, log)
	if err != nil {
		return err
	}
	hst, err := host.New(parts, host.Options{MaxFPS: s.MaxFPS, MaxHeight: s.MaxHeight, Logger: log})
	if err != nil {
		return err
	}
	srv, err := rdp.NewServer(rdp.Config{
		Certificate:   cert.TLS,
		Authenticator: authenticator(s, config.NewGuard(nil), log),
		Handler:       hst,
		DesktopSize:   hst.DesktopSize,
		Logger:        log,
	})
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(s.Address, strconv.Itoa(s.Port))
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return errors.NewErr(TagListenFailed, err).SetMetadata("addr", addr)
	}
	return srv.Serve(ctx, l)
}

// authenticator checks a login against s, with guard making an address wait
// after failed logins (SPEC §8.2). It logs a refusal, never the password.
func authenticator(s config.Settings, guard *config.Guard, log *slog.Logger) rdp.Authenticator {
	return func(r rdp.LoginRequest) bool {
		remote := ""
		if r.Remote != nil {
			remote = r.Remote.String()
		}
		res := guard.Login(remote, func() bool { return s.CheckLogin(r.User, r.Password) })
		if !res.OK {
			log.Warn("login refused", "user", r.User, "remote", remote, "failures", res.Failures, "wait", res.Wait)
		}
		return res.OK
	}
}
