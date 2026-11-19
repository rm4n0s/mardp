//go:build mardp_freerdp

package rdp

// Tests against a real client (SPEC §15.3): FreeRDP in the test machine of
// docs/VM.md connects to the library's server on the Mac. They need the
// machine running (scripts/vm.sh start) and are skipped otherwise:
//
//	scripts/vm.sh start
//	go test -tags mardp_freerdp -run FreeRDP -v ./rdp/
//	scripts/vm.sh stop
//
// MARDP_FREERDP_LOGS=<folder> keeps each run's files (FreeRDP's log, the
// screenshot) in a folder of that name per subtest, and the server's log.
//
// MARDP_SERVE=<duration> runs TestServe instead: the server shows the test
// pattern for that long, for Remmina in the machine's window
// (docs/MANUAL-CHECKS.md).

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// vm runs scripts/vm.sh with args, in a process group of its own that is
// killed when ctx ends, and returns what it printed.
func vm(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, filepath.Join("..", "scripts", "vm.sh"), args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// requireVM skips the test when the test machine is not running, and
// returns the Mac's address as the guest reaches it.
func requireVM(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := vm(ctx, "status"); err != nil || strings.TrimSpace(out) != "started" {
		t.Skipf("the test machine is not running (%q, %v): scripts/vm.sh start, see docs/VM.md", strings.TrimSpace(out), err)
	}
	out, err := vm(ctx, "host-address")
	host := strings.TrimSpace(out)
	if err != nil || net.ParseIP(host) == nil {
		t.Fatalf("the Mac's address in the guest: %q, %v", out, err)
	}
	return host
}

// guestDir is the guest's folder for a run; it is made again each time.
const guestDir = "/tmp/mardp-freerdp"

// freerdpRun is one run of xfreerdp in the guest.
type freerdpRun struct {
	host string
	port int
	user string
	pass string
	// args are more arguments of xfreerdp.
	args []string
	// interact takes a screenshot of xfreerdp's window, then types a, b
	// and Return, moves the mouse to 300, 400 and 100, 200, clicks there,
	// and moves it to 500, 600: FreeRDP sends each move only when the next
	// pointer event comes (docs/INTEROP.md), so the last move pushes out the
	// one before.
	interact bool
}

// script is the shell script that runs r in the guest under a virtual X
// display, and prints xfreerdp's exit status as "exit N".
func (r freerdpRun) script() string {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	args := []string{
		fmt.Sprintf("/v:%s:%d", r.host, r.port), q("/u:" + r.user), q("/p:" + r.pass),
		"/cert:ignore", "/size:1024x768", "/bpp:32", "/log-level:DEBUG",
	}
	for _, a := range r.args {
		args = append(args, q(a))
	}
	var b strings.Builder
	fmt.Fprintf(&b, `set -u
D=%s; rm -rf "$D"; mkdir -p "$D"; cd "$D"; export HOME="$D"
Xvfb :99 -screen 0 1280x1024x24 -nolisten tcp >xvfb.log 2>&1 & X=$!
export DISPLAY=:99
i=0; while ! xdotool getdisplaygeometry >/dev/null 2>&1; do i=$((i+1)); [ $i -gt 100 ] && break; sleep 0.1; done
timeout 60 xfreerdp %s >freerdp.log 2>&1 & F=$!
`, guestDir, strings.Join(args, " "))
	if r.interact {
		b.WriteString(`W=$(timeout 30 xdotool search --sync --onlyvisible --class freerdp | head -n 1)
echo "window $W"
if [ -n "$W" ]; then
	sleep 4
	import -window "$W" png24:shot.png
	xdotool windowfocus --sync "$W"; sleep 0.5
	xdotool key --delay 150 a b Return
	xdotool mousemove --sync 300 400; sleep 0.3
	xdotool mousemove --sync 100 200; sleep 0.5
	xdotool click 1; sleep 0.5
	xdotool mousemove --sync 500 600
	sleep 2
fi
kill $F 2>/dev/null
`)
	}
	b.WriteString(`wait $F; echo "exit $?"
kill $X 2>/dev/null; wait 2>/dev/null
true
`)
	return b.String()
}

// run runs r in the guest and returns the script's output and xfreerdp's
// exit status; the run's files are copied into dir.
func (r freerdpRun) run(t *testing.T, dir string, files ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := vm(ctx, "sh", r.script())
	for _, f := range append([]string{"freerdp.log"}, files...) {
		if _, perr := vm(ctx, "pull", guestDir+"/"+f, filepath.Join(dir, f)); perr != nil {
			t.Logf("pulling %s: %v", f, perr)
		}
	}
	if err != nil {
		t.Fatalf("the guest's script: %v\n%s", err, out)
	}
	i := strings.LastIndex(out, "exit ")
	if i < 0 {
		t.Fatalf("no exit status:\n%s", out)
	}
	code, err := strconv.Atoi(strings.TrimSpace(out[i+5:]))
	if err != nil {
		t.Fatalf("exit status: %v\n%s", err, out)
	}
	return out, code
}

// runDir is the folder for a run's files: a temporary one, or one in
// $MARDP_FREERDP_LOGS.
func runDir(t *testing.T) string {
	t.Helper()
	root := os.Getenv("MARDP_FREERDP_LOGS")
	if root == "" {
		return t.TempDir()
	}
	dir := filepath.Join(root, strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// logTail is the end of the file at path, for a failure message.
func logTail(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return err.Error()
	}
	lines := strings.Split(string(b), "\n")
	return strings.Join(lines[max(0, len(lines)-40):], "\n")
}

// testPattern is a picture whose halves differ: eight bars of color on
// top, gradients below, so that a picture upside down, shifted or with its
// colors swapped does not match.
func testPattern(w, h int) *Image {
	bars := [8][3]byte{ // blue, green, red
		{0xff, 0xff, 0xff}, {0x00, 0xff, 0xff}, {0xff, 0xff, 0x00}, {0x00, 0xff, 0x00},
		{0xff, 0x00, 0xff}, {0x00, 0x00, 0xff}, {0xff, 0x00, 0x00}, {0x20, 0x20, 0x20},
	}
	img := &Image{Pix: make([]byte, 4*w*h), Stride: 4 * w, Width: w, Height: h}
	for y := range h {
		for x := range w {
			p := img.Pix[y*img.Stride+4*x:]
			if y < h/2 {
				copy(p, bars[x*8/w][:])
			} else {
				p[0], p[1], p[2] = byte(x*255/w), byte((y-h/2)*255/(h-h/2)), byte(255-x*255/w)
			}
			p[3] = 0xff
		}
	}
	return img
}

// compareShot counts the pixels of the screenshot at path that differ from
// img, and says where the first is.
func compareShot(t *testing.T, path string, img *Image) (int, string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }() // read only
	shot, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if b := shot.Bounds(); b != image.Rect(0, 0, img.Width, img.Height) {
		t.Fatalf("the screenshot is %v, the picture %dx%d", b, img.Width, img.Height)
	}
	n, first := 0, ""
	for y := range img.Height {
		for x := range img.Width {
			r, g, b, _ := shot.At(x, y).RGBA()
			p := img.Pix[y*img.Stride+4*x:]
			if byte(r>>8) != p[2] || byte(g>>8) != p[1] || byte(b>>8) != p[0] {
				if n == 0 {
					first = fmt.Sprintf("at %d,%d: %02x%02x%02x, want %02x%02x%02x", x, y, r>>8, g>>8, b>>8, p[2], p[1], p[0])
				}
				n++
			}
		}
	}
	return n, first
}

// inputLog is an InputSink that keeps what arrives, as text.
type inputLog struct {
	mu     sync.Mutex
	events []string
}

func (l *inputLog) add(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, fmt.Sprintf(format, args...))
}

func (l *inputLog) Key(sc uint8, ext Extended, down bool) { l.add("key %#02x %d %v", sc, ext, down) }
func (l *inputLog) Unicode(r rune, down bool)             { l.add("unicode %q %v", r, down) }
func (l *inputLog) MouseMove(x, y int)                    { l.add("move %d,%d", x, y) }
func (l *inputLog) MouseButton(b MouseButton, down bool, x, y int) {
	l.add("button %d %v %d,%d", b, down, x, y)
}
func (l *inputLog) Wheel(dx, dy int)                 { l.add("wheel %d,%d", dx, dy) }
func (l *inputLog) SyncLocks(caps, num, scroll bool) { l.add("locks %v %v %v", caps, num, scroll) }

func (l *inputLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.events)
}

func TestFreeRDP(t *testing.T) {
	host := requireVM(t)
	pattern := testPattern(1024, 768)
	in := &inputLog{}
	var mu sync.Mutex
	var opened []*Session
	var sendErr error
	h := HandlerFunc(func(ctx context.Context, s *Session) {
		s.SetInputSink(in)
		w, ht := s.DesktopSize()
		err := s.Video().SendRects(pattern, []image.Rectangle{image.Rect(0, 0, w, ht)})
		mu.Lock()
		opened = append(opened, s)
		sendErr = err
		mu.Unlock()
		<-ctx.Done()
	})
	ts := startAt(t, Config{Handler: h}, net.JoinHostPort(host, "0"))
	_, portStr, err := net.SplitHostPort(ts.addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	base := freerdpRun{host: host, port: port, user: testUser, pass: testPassword}
	t.Cleanup(func() {
		if root := os.Getenv("MARDP_FREERDP_LOGS"); root != "" {
			if err := os.WriteFile(filepath.Join(root, "server.log"), []byte(ts.log.text(t)), 0o644); err != nil {
				t.Error(err)
			}
		}
	})

	t.Run("password", func(t *testing.T) {
		dir := runDir(t)
		r := base
		r.interact = true
		out, code := r.run(t, dir, "shot.png")
		t.Logf("guest: %s(xfreerdp's exit status %d after it was stopped)", out, code)
		mu.Lock()
		n, serr := len(opened), sendErr
		var c *Client
		if n > 0 {
			c = opened[0].Client()
		}
		mu.Unlock()
		if n != 1 {
			t.Fatalf("%d sessions opened, want 1; FreeRDP's log:\n%s\nserver:\n%s", n, logTail(filepath.Join(dir, "freerdp.log")), ts.log.text(t))
		}
		if serr != nil {
			t.Fatalf("sending the pattern: %v", serr)
		}
		t.Logf("client: name %q build %d, %dx%d, layout %#x, fast-path output %v, alpha pointer %v, pointer cache %d, large pointer %d, max request %d, skip alpha %v, channels %v",
			c.Name, c.Build, c.DesktopWidth, c.DesktopHeight, c.KeyboardLayout, c.FastPathOutput, c.AlphaPointer,
			c.PointerCacheSize, c.LargePointer, c.MaxRequestSize, c.SkipAlpha, c.Channels)
		if d, first := compareShot(t, filepath.Join(dir, "shot.png"), pattern); d != 0 {
			t.Errorf("%d pixels differ from the pattern, the first %s", d, first)
		}
		events := in.all()
		t.Logf("input: %v", events)
		for _, want := range []string{
			"key 0x1e 0 true", "key 0x1e 0 false", "key 0x30 0 true", "key 0x30 0 false",
			"key 0x1c 0 true", "key 0x1c 0 false", "move 300,400", "move 100,200",
			"button 1 true 100,200", "button 1 false 100,200",
		} {
			if !slices.Contains(events, want) {
				t.Errorf("no %q in the input", want)
			}
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		dir := runDir(t)
		r := base
		r.pass = "wrong"
		out, code := r.run(t, dir)
		t.Logf("guest: %s", out)
		if code == 0 {
			t.Errorf("xfreerdp exited with 0; its log:\n%s", logTail(filepath.Join(dir, "freerdp.log")))
		}
		ts.log.find(t, "login failed")
	})

	t.Run("without TLS", func(t *testing.T) {
		dir := runDir(t)
		r := base
		r.args = []string{"/sec:rdp"}
		out, code := r.run(t, dir)
		t.Logf("guest: %s", out)
		if code == 0 {
			t.Errorf("xfreerdp exited with 0; its log:\n%s", logTail(filepath.Join(dir, "freerdp.log")))
		}
		rec := ts.log.find(t, "connection ended")
		err, _ := attr(rec, "err").Any().(error)
		assertRoute(t, err, "conn.negotiate."+TagTLSRequired)
	})
}

// TestServe serves the test pattern for $MARDP_SERVE, for Remmina in the
// machine's own window, and prints what each session sent.
func TestServe(t *testing.T) {
	d, err := time.ParseDuration(os.Getenv("MARDP_SERVE"))
	if err != nil {
		t.Skip("MARDP_SERVE=<duration> serves the test pattern for that long")
	}
	host := requireVM(t)
	h := HandlerFunc(func(ctx context.Context, s *Session) {
		in := &inputLog{}
		s.SetInputSink(in)
		w, ht := s.DesktopSize()
		c := s.Client()
		fmt.Printf("session opened: %q, %dx%d, layout %#x, channels %v\n", c.Name, w, ht, c.KeyboardLayout, c.Channels)
		if err := s.Video().SendRects(testPattern(w, ht), []image.Rectangle{image.Rect(0, 0, w, ht)}); err != nil {
			fmt.Printf("sending the pattern: %v\n", err)
		}
		<-ctx.Done()
		fmt.Printf("session ended; input: %v\n", in.all())
	})
	ts := startAt(t, Config{Handler: h}, net.JoinHostPort(host, "0"))
	fmt.Printf("serving for %v at %s, user %q, password %q\n", d, ts.addr, testUser, testPassword)
	time.Sleep(d)
	fmt.Println(ts.log.text(t))
}
