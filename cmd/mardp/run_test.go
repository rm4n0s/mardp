package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/mardp/internal/config"
	"github.com/rm4n0s/mardp/internal/host"
	"github.com/rm4n0s/mardp/internal/mac/system"
	"github.com/rm4n0s/mardp/internal/ui"
	"github.com/rm4n0s/mardp/rdp"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

const (
	testUser      = "alice"
	testPassword  = "a password that is never logged"
	wrongPassword = "another password, never logged"
)

func assertRoute(t *testing.T, err error, route string) {
	t.Helper()
	e, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("expected *errors.Error, got %T: %v", err, err)
	}
	if !e.HasRoute(route) {
		t.Errorf("route %s, want %s", e.Route(), route)
	}
}

// syncBuffer is a bytes.Buffer that goroutines may share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// fakeMac is a machine of fakes: a test pattern, an input recorder.
type fakeMac struct {
	sys *host.FakeSystem
	// out, when set, is the system's sound output; sound gives the
	// sessions a tone.
	out      *host.FakeOutput
	sound    bool
	sysErr   error
	partsErr error
	noScreen bool
	in       *host.RecordingInjector
	// win is the window; nil waits until ctx ends.
	win func(ctx context.Context, b ui.Backend) error

	login *fakeLogin

	mu       sync.Mutex
	posts    []bool
	patterns int
}

func newFakeMac(granted ...host.Permission) *fakeMac {
	return &fakeMac{sys: host.NewFakeSystem(nil, granted...), in: &host.RecordingInjector{}, login: &fakeLogin{}}
}

func (f *fakeMac) loginItem() loginItem {
	if f.login == nil {
		f.login = &fakeLogin{}
	}
	return f.login
}

// fakeLogin is the login items of a test: it records what it is asked.
type fakeLogin struct {
	mu     sync.Mutex
	status system.LoginItemStatus
	err    error
	sets   []bool
}

func (l *fakeLogin) Status() system.LoginItemStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status
}

func (l *fakeLogin) Set(on bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sets = append(l.sets, on)
	if l.err != nil {
		return l.err
	}
	l.status = map[bool]system.LoginItemStatus{false: system.LoginItemOff, true: system.LoginItemOn}[on]
	return nil
}

// set changes the state, as the user in System Settings.
func (l *fakeLogin) set(st system.LoginItemStatus) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.status = st
}

// asked is what the app asked so far.
func (l *fakeLogin) asked() []bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.sets)
}

func (f *fakeMac) system() (host.System, error) {
	if f.sysErr != nil {
		return nil, f.sysErr
	}
	if f.out != nil {
		return struct {
			*host.FakeSystem
			*host.FakeOutput
		}{f.sys, f.out}, nil
	}
	return f.sys, nil
}

func (f *fakeMac) parts(sys host.System, _ config.Settings, post bool, _ *slog.Logger) (host.Parts, error) {
	f.mu.Lock()
	f.posts = append(f.posts, post)
	f.mu.Unlock()
	if f.partsErr != nil {
		return host.Parts{}, f.partsErr
	}
	p := host.Parts{Screen: &host.PatternScreen{Frames: 1}, System: sys}
	if f.noScreen {
		p.Screen = nil
	}
	if post {
		p.Input = f.in
	}
	if f.sound {
		p.Sound = &host.ToneSource{}
	}
	return p, nil
}

// pattern is the fake test pattern: host.PatternVideo, and a tone unless
// the sound is off.
func (f *fakeMac) pattern(s config.Settings, _ *slog.Logger) (host.Parts, error) {
	f.mu.Lock()
	f.patterns++
	f.mu.Unlock()
	p := host.Parts{Screen: &host.PatternScreen{Frames: 1}, Video: &host.PatternVideo{}}
	if s.Sound != config.SoundOff {
		p.Sound = &host.ToneSource{}
	}
	return p, nil
}

func (f *fakeMac) window(ctx context.Context, b ui.Backend, _ *slog.Logger) error {
	if f.win == nil {
		<-ctx.Done()
		return nil
	}
	return f.win(ctx, b)
}

func (f *fakeMac) postsSeen() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.posts)
}

// freePort is a TCP port of 127.0.0.1 that nothing listens on.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

// home is a mardp folder for a test.
type home struct {
	dir string
	s   config.Settings
	// der is the server's certificate.
	der []byte
}

func (h *home) getenv(k string) string {
	if k == config.HomeEnv {
		return h.dir
	}
	return ""
}

func (h *home) addr() string { return net.JoinHostPort(h.s.Address, strconv.Itoa(h.s.Port)) }

// newHome makes a mardp folder whose settings have a user and a password,
// with edit's changes, listening on a free port of 127.0.0.1.
func newHome(t *testing.T, edit func(s *config.Settings)) *home {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	f, err := config.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := config.Defaults()
	s.User = testUser
	if s.Password, err = config.HashPassword(testPassword); err != nil {
		t.Fatal(err)
	}
	s.Address, s.Port = "127.0.0.1", freePort(t)
	if edit != nil {
		edit(&s)
	}
	if err := f.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	c, _, err := f.Certificate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return &home{dir: dir, s: s, der: c.TLS.Certificate[0]}
}

// corrupt breaks h's config.json.
func corrupt(t *testing.T, h *home) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.dir, config.FileName), []byte("{"), config.FileMode); err != nil {
		t.Fatal(err)
	}
}

// running is mardp running in a goroutine.
type running struct {
	log    *syncBuffer
	cancel context.CancelFunc
	code   chan int
}

// start runs mardp with args until the test ends, and waits until it
// listens.
func start(t *testing.T, h *home, m machine, args ...string) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{log: &syncBuffer{}, cancel: cancel, code: make(chan int, 1)}
	go func() { r.code <- run(ctx, args, h.getenv, &syncBuffer{}, r.log, m) }()
	t.Cleanup(func() { r.stop(t) })
	deadline := time.Now().Add(10 * time.Second)
	for {
		nc, err := net.Dial("tcp", h.addr())
		if err == nil {
			if err := nc.Close(); err != nil {
				t.Fatal(err)
			}
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("mardp does not listen on %s: %v\n%s", h.addr(), err, r.log)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// stop ends mardp and checks that it exits with 0, once.
func (r *running) stop(t *testing.T) {
	t.Helper()
	if r.cancel == nil {
		return
	}
	r.cancel()
	r.cancel = nil
	select {
	case code := <-r.code:
		if code != exitOK {
			t.Errorf("exit code %d\n%s", code, r.log)
		}
	case <-time.After(10 * time.Second):
		t.Error("mardp did not stop")
	}
}

// connect connects a client to h's server, trusting only its certificate.
func connect(t *testing.T, h *home, user, password string) (*rdptest.Client, error) {
	t.Helper()
	return connectSized(t, h, user, password, 1280, 720)
}

// connectSized is connect, asking for a desktop of w×ht.
func connectSized(t *testing.T, h *home, user, password string, w, ht uint16) (*rdptest.Client, error) {
	t.Helper()
	nc, err := net.Dial("tcp", h.addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := nc.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	})
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		// The certificate is self-signed (SPEC §8.1): it is pinned instead.
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 || !bytes.Equal(cs.PeerCertificates[0].Raw, h.der) {
				return errors.New("TestCertificateUnknown", "not the server's certificate")
			}
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := rdptest.Connect(ctx, nc, rdptest.Options{TLSConfig: cfg, User: user, Password: password, Width: w, Height: ht})
	if err != nil {
		t.Fatal(err)
	}
	return c, c.Login(ctx)
}

// sameColors says the screen shows img, alpha aside.
func sameColors(sc *rdptest.Screen, img *rdp.Image) bool {
	if sc.Width != img.Width || sc.Height != img.Height {
		return false
	}
	for y := range sc.Height {
		a, b := sc.Pix[y*sc.Stride:], img.Pix[y*img.Stride:]
		for x := range sc.Width {
			if a[4*x] != b[4*x] || a[4*x+1] != b[4*x+1] || a[4*x+2] != b[4*x+2] {
				return false
			}
		}
	}
	return true
}

func TestRunFlags(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no folder", "mardp.log")
	tests := []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"version", []string{"-version"}, exitOK, "mardp ", ""},
		{"unknown flag", []string{"-nope"}, exitUsage, "", "flag provided but not defined"},
		{"arguments", []string{"-headless", "more"}, exitUsage, "", "unexpected arguments"},
		{"log file", []string{"-headless", "-log-file", missing}, exitFailed, "", "the log file"},
		{"test pattern without -headless", []string{"-test-pattern"}, exitUsage, "", "need -headless"},
		{"measure without -headless", []string{"-measure"}, exitUsage, "", "need -headless"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			h := &home{dir: t.TempDir()}
			code := run(context.Background(), tt.args, h.getenv, &stdout, &stderr, newFakeMac())
			if code != tt.code {
				t.Errorf("code %d, want %d", code, tt.code)
			}
			if !strings.Contains(stdout.String(), tt.stdout) {
				t.Errorf("stdout %q, want %q in it", stdout.String(), tt.stdout)
			}
			if !strings.Contains(stderr.String(), tt.stderr) {
				t.Errorf("stderr %q, want %q in it", stderr.String(), tt.stderr)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	if v := version(); v == "" {
		t.Error("no version")
	}
}

func TestServeRefuses(t *testing.T) {
	busy := func(t *testing.T, h *home) {
		l, err := net.Listen("tcp", h.addr())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := l.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	both := []host.Permission{host.ScreenRecording, host.Accessibility}
	tests := []struct {
		name    string
		edit    func(s *config.Settings)
		prepare func(t *testing.T, h *home)
		granted []host.Permission
		sysErr  error
		parts   error
		noScr   bool
		route   string
	}{
		{name: "no password", edit: func(s *config.Settings) { s.Password = nil }, granted: both,
			route: "serve->prepare." + TagNoPassword},
		{name: "no user", edit: func(s *config.Settings) { s.User = "" }, granted: both,
			route: "serve->prepare." + TagNoPassword},
		{name: "no Screen Recording", granted: []host.Permission{host.Accessibility},
			route: "serve->prepare." + TagScreenRecordingMissing},
		{name: "config.json corrupt", prepare: corrupt, granted: both,
			route: "serve->openFolder->Folder.Load." + config.TagCorrupt},
		{name: "the system", granted: both, sysErr: errors.New("TestSystemFailed", "no system"),
			route: "TestServeRefuses.TestSystemFailed"},
		{name: "the parts", granted: both, parts: errors.New("TestPartsFailed", "no parts"),
			route: "TestServeRefuses.TestPartsFailed"},
		{name: "no screen", granted: both, noScr: true,
			route: "serve->prepare->New." + host.TagScreenMissing},
		{name: "port taken", prepare: busy, granted: both,
			route: "serve->prepare." + TagListenFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHome(t, tt.edit)
			if tt.prepare != nil {
				tt.prepare(t, h)
			}
			m := newFakeMac(tt.granted...)
			m.sysErr, m.partsErr, m.noScreen = tt.sysErr, tt.parts, tt.noScr
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := serve(ctx, h.getenv, m, serveOptions{}, slog.New(slog.DiscardHandler))
			if err == nil {
				t.Fatal("serve started")
			}
			assertRoute(t, err, tt.route)
		})
	}
}

func TestServeSession(t *testing.T) {
	h := newHome(t, nil)
	m := newFakeMac(host.ScreenRecording, host.Accessibility)
	r := start(t, h, m, "-headless")

	c, err := connect(t, h, testUser, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	sc := rdptest.NewScreen(1280, 720)
	want := &rdp.Image{Pix: make([]byte, 4*1280*720), Stride: 4 * 1280, Width: 1280, Height: 720}
	host.PatternFrame(want, 0)
	for !sameColors(sc, want) {
		u, err := c.ReadUpdate(ctx)
		if err != nil {
			t.Fatalf("waiting for the pattern: %v", err)
		}
		if _, err := sc.Draw(u); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.SendInput(ctx, rdptest.Key(0x1e, false, true)); err != nil {
		t.Fatal(err)
	}
	if got := m.in.Wait(ctx, 1); len(got) != 1 || got[0].Kind != host.InputKey || got[0].Scancode != 0x1e {
		t.Errorf("input %+v", got)
	}

	if _, err := connect(t, h, testUser, wrongPassword); err == nil {
		t.Error("a wrong password logged in")
	}
	r.stop(t)
	log := r.log.String()
	for _, want := range []string{"mardp starting", "certificate", "fingerprint=", "listening", "login refused", "user=" + testUser} {
		if !strings.Contains(log, want) {
			t.Errorf("the log has no %q:\n%s", want, log)
		}
	}
	for _, secret := range []string{testPassword, wrongPassword} {
		if strings.Contains(log, secret) {
			t.Errorf("the log has the password %q:\n%s", secret, log)
		}
	}
	if got := m.postsSeen(); !slices.Equal(got, []bool{true}) {
		t.Errorf("parts made with post %v, want [true]", got)
	}
}

func TestServeViewOnly(t *testing.T) {
	tests := []struct {
		name    string
		granted []host.Permission
		args    []string
		log     string
	}{
		{"no Accessibility", []host.Permission{host.ScreenRecording}, nil, "Accessibility is not granted"},
		{"-view-only", []host.Permission{host.ScreenRecording, host.Accessibility}, []string{"-view-only"}, "view-only (-view-only)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHome(t, nil)
			m := newFakeMac(tt.granted...)
			r := start(t, h, m, append([]string{"-headless"}, tt.args...)...)
			r.stop(t)
			if got := m.postsSeen(); !slices.Equal(got, []bool{false}) {
				t.Errorf("parts made with post %v, want [false]", got)
			}
			if !strings.Contains(r.log.String(), tt.log) {
				t.Errorf("the log does not say %q:\n%s", tt.log, r.log)
			}
		})
	}
}

func TestLogFile(t *testing.T) {
	h := newHome(t, func(s *config.Settings) { s.Password = nil })
	path := filepath.Join(t.TempDir(), "mardp.log")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"-headless", "-log-file", path}, h.getenv, &stdout, &stderr, newFakeMac())
	if code != exitFailed {
		t.Errorf("code %d, want %d", code, exitFailed)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr %q, want nothing", stderr.String())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "mardp stopped") || !strings.Contains(string(b), TagNoPassword) {
		t.Errorf("the log file:\n%s", b)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != config.FileMode {
		t.Errorf("mode %v, want %v", fi.Mode().Perm(), config.FileMode)
	}
}

func TestAuthenticator(t *testing.T) {
	s := config.Defaults()
	s.User = testUser
	var err error
	if s.Password, err = config.HashPassword(testPassword); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	guard := config.NewGuard(func() time.Time { return now })
	var log syncBuffer
	var refused []config.LoginResult
	auth := authenticator(func() config.Settings { return s }, guard, func(user, remote string, res config.LoginResult) {
		refused = append(refused, res)
	}, slog.New(slog.NewTextHandler(&log, nil)))
	remote := &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 50000}
	steps := []struct {
		name string
		req  rdp.LoginRequest
		want bool
	}{
		{"right", rdp.LoginRequest{User: testUser, Password: testPassword, Remote: remote}, true},
		{"no address", rdp.LoginRequest{User: testUser, Password: testPassword}, true},
		{"wrong user", rdp.LoginRequest{User: "bob", Password: testPassword, Remote: remote}, false},
	}
	for range config.FreeFailures - 1 {
		steps = append(steps, struct {
			name string
			req  rdp.LoginRequest
			want bool
		}{"wrong password", rdp.LoginRequest{User: testUser, Password: "wrong", Remote: remote}, false})
	}
	steps = append(steps, struct {
		name string
		req  rdp.LoginRequest
		want bool
	}{"right but waiting", rdp.LoginRequest{User: testUser, Password: testPassword, Remote: remote}, false})
	for i, st := range steps {
		if got := auth(st.req); got != st.want {
			t.Errorf("step %d (%s): %v, want %v", i, st.name, got, st.want)
		}
	}
	out := log.String()
	if strings.Contains(out, testPassword) || strings.Contains(out, "password=") {
		t.Errorf("the log has a password:\n%s", out)
	}
	if !strings.Contains(out, "wait=") || !strings.Contains(out, "remote=192.0.2.1:50000") {
		t.Errorf("the log:\n%s", out)
	}
	// refused is told of each refusal: the fifth failure starts the wait,
	// and the login while the address waits is not checked.
	if len(refused) != config.FreeFailures+1 {
		t.Fatalf("%d refusals: %+v", len(refused), refused)
	}
	fifth, waiting := refused[config.FreeFailures-1], refused[config.FreeFailures]
	if !fifth.Checked || fifth.Failures != config.FreeFailures || fifth.Wait != config.FirstWait {
		t.Errorf("the fifth failure %+v", fifth)
	}
	if waiting.Checked || waiting.Wait != config.FirstWait {
		t.Errorf("the login while waiting %+v", waiting)
	}
	if auth(rdp.LoginRequest{User: "bob"}); len(refused) != config.FreeFailures+2 {
		t.Error("no refusal told for a login without an address")
	}
}

func TestMac(t *testing.T) {
	sys, err := mac{}.system()
	if runtime.GOOS != "darwin" {
		if err == nil {
			t.Error("a System on " + runtime.GOOS)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	// Making the parts posts nothing, records nothing and asks for
	// nothing (rule 11 of docs/IMPLEMENTATION.md).
	for _, post := range []bool{false, true} {
		p, err := mac{}.parts(sys, config.Defaults(), post, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		if p.Screen == nil || p.Pointer == nil || p.System == nil || (p.Input != nil) != post {
			t.Errorf("post %v: parts %+v", post, p)
		}
	}
}

// TestServeMute: the server unmutes the output a mardp that ended left
// muted, then mutes it while a client gets the sound when it plays on the
// client only, and leaves it alone when it plays on both (SPEC §10.1).
func TestServeMute(t *testing.T) {
	tests := []struct {
		name  string
		sound config.Sound
		muted bool
	}{
		{"on the client only", config.SoundClient, true},
		{"on both", config.SoundBoth, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			h := newHome(t, func(s *config.Settings) { s.Sound = tt.sound })
			folder, err := config.Open(ctx, h.dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			m := newFakeMac(host.ScreenRecording, host.Accessibility)
			m.out, m.sound = host.NewFakeOutput("Speakers"), true
			m.out.Mute("Speakers", true)
			if err := folder.MarkMuted(ctx, "Speakers"); err != nil {
				t.Fatal(err)
			}
			r := start(t, h, m, "-headless")
			if _, ok, err := folder.MutedMark(ctx); ok || err != nil || m.out.Muted("Speakers") {
				t.Fatalf("not recovered: marked %v (%v), muted %v", ok, err, m.out.Muted("Speakers"))
			}
			c, err := connect(t, h, testUser, testPassword)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			a, err := c.NegotiateAudio(ctx, rdptest.AudioOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.ReadBlock(ctx); err != nil {
				t.Fatal(err)
			}
			for m.out.Muted("Speakers") != tt.muted {
				if ctx.Err() != nil {
					t.Fatalf("muted %v, want %v", m.out.Muted("Speakers"), tt.muted)
				}
				time.Sleep(5 * time.Millisecond)
			}
			if uid, _, _ := folder.MutedMark(ctx); tt.muted != (uid == "Speakers") {
				t.Errorf("marked %q", uid)
			}
			r.stop(t)
			if _, ok, _ := folder.MutedMark(ctx); ok || m.out.Muted("Speakers") {
				t.Errorf("after the server: marked %v, muted %v", ok, m.out.Muted("Speakers"))
			}
		})
	}
}
