//go:build darwin && mardp_freerdp

package main

// The proof of step 2.7 (SPEC §15.3): FreeRDP in the test machine of
// docs/VM.md connects to mardp -headless, which shows the Mac's real
// screen. It needs the machine running and is skipped otherwise:
//
//	scripts/vm.sh start
//	go test -tags mardp_freerdp -run FreeRDP -v ./cmd/mardp/
//	scripts/vm.sh stop
//
// mardp runs as a bundle signed as the M0 probe (scripts/probe-app.sh), so
// it has the Screen Recording the owner granted, started by open(1) so that
// macOS holds it, not the test, responsible for it. It runs with -view-only:
// FreeRDP sends lock states and pointer moves by itself, and no test posts
// input into the owner's desktop (rule 11 of docs/IMPLEMENTATION.md).
//
// MARDP_FREERDP_LOGS=<folder> keeps mardp's log, FreeRDP's log and the
// screenshot there.

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
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/mardp/internal/config"
)

// vm runs scripts/vm.sh with args, in a process group of its own that is
// killed when ctx ends, and returns what it printed.
func vm(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, filepath.Join("..", "..", "scripts", "vm.sh"), args...)
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
	addr := strings.TrimSpace(out)
	if err != nil || net.ParseIP(addr) == nil {
		t.Fatalf("the Mac's address in the guest: %q, %v", out, err)
	}
	return addr
}

// logDir is where the run's files go: a temporary folder, or
// $MARDP_FREERDP_LOGS.
func logDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("MARDP_FREERDP_LOGS")
	if dir == "" {
		return t.TempDir()
	}
	dir = filepath.Join(dir, t.Name())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// command runs name with args under ctx and fails the test when it fails.
func command(t *testing.T, ctx context.Context, name string, args ...string) {
	t.Helper()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// pids are the processes running exe.
func pids(ctx context.Context, exe string) []int {
	out, _ := exec.CommandContext(ctx, "pgrep", "-f", exe).Output() // exit 1: none
	var ps []int
	for _, f := range strings.Fields(string(out)) {
		if p, err := strconv.Atoi(f); err == nil {
			ps = append(ps, p)
		}
	}
	return ps
}

// startApp builds mardp into a probe-signed bundle and opens it with
// -headless -view-only and MARDP_HOME=home; it is stopped when the test
// ends. It returns the log's path once mardp listens.
func startApp(t *testing.T, home, dir string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(root, "build", "freerdp", "MardpHeadless.app")
	exe := filepath.Join(app, "Contents", "MacOS", "mardp")
	if ps := pids(ctx, exe); len(ps) > 0 {
		t.Fatalf("mardp of an earlier run still runs: %v", ps)
	}
	if err := os.RemoveAll(app); err != nil {
		t.Fatal(err)
	}
	command(t, ctx, "go", "build", "-o", exe, ".")
	command(t, ctx, filepath.Join(root, "scripts", "probe-app.sh"), app, "mardp")
	logPath := filepath.Join(dir, "mardp.log")
	command(t, ctx, "open", "-n", "--env", config.HomeEnv+"="+home, "--stdout", logPath, "--stderr", logPath,
		app, "--args", "-headless", "-view-only")
	t.Cleanup(func() { stopApp(t, exe) })
	for {
		b, _ := os.ReadFile(logPath) // not there yet at first
		switch {
		case bytes.Contains(b, []byte("msg=listening")):
			return logPath
		case bytes.Contains(b, []byte("mardp stopped")):
			t.Fatalf("mardp did not start:\n%s", b)
		case ctx.Err() != nil:
			t.Fatalf("mardp does not listen:\n%s", b)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// stopApp ends the mardp at exe with SIGTERM, and SIGKILL after 10 s.
func stopApp(t *testing.T, exe string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sig, killed := syscall.SIGTERM, false
	for deadline := time.Now().Add(10 * time.Second); ; {
		ps := pids(ctx, exe)
		if len(ps) == 0 {
			return
		}
		if time.Now().After(deadline) {
			if killed {
				t.Errorf("mardp %v does not stop", ps)
				return
			}
			t.Errorf("mardp %v did not stop on SIGTERM", ps)
			sig, killed, deadline = syscall.SIGKILL, true, time.Now().Add(5*time.Second)
		}
		if sig != 0 {
			for _, p := range ps {
				if err := syscall.Kill(p, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
					t.Error(err)
				}
			}
			sig = 0 // sent once; then wait until the deadline
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// guestDir is the guest's folder for the run.
const guestDir = "/tmp/mardp-headless"

// shotScript runs xfreerdp in the guest under a virtual X display against
// addr, takes a screenshot of its window after a few seconds and prints
// xfreerdp's exit status as "exit N".
func shotScript(addr string, port int) string {
	return fmt.Sprintf(`set -u
D=%s; rm -rf "$D"; mkdir -p "$D"; cd "$D"; export HOME="$D"
Xvfb :98 -screen 0 1280x1024x24 -nolisten tcp >xvfb.log 2>&1 & X=$!
export DISPLAY=:98
i=0; while ! xdotool getdisplaygeometry >/dev/null 2>&1; do i=$((i+1)); [ $i -gt 100 ] && break; sleep 0.1; done
timeout 60 xfreerdp /v:%s:%d '/u:%s' '/p:%s' /cert:ignore /size:1024x768 /bpp:32 /log-level:DEBUG >freerdp.log 2>&1 & F=$!
W=$(timeout 30 xdotool search --sync --onlyvisible --class freerdp | head -n 1)
echo "window $W"
if [ -n "$W" ]; then
	sleep 5
	import -window "$W" png24:shot.png
fi
kill $F 2>/dev/null
wait $F; echo "exit $?"
kill $X 2>/dev/null; wait 2>/dev/null
true
`, guestDir, addr, port, testUser, testPassword)
}

// colors counts the distinct colors of the image at path, up to limit.
func colors(t *testing.T, path string, limit int) (int, image.Rectangle) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }() // read only
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[[3]uint32]bool{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y && len(seen) < limit; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			seen[[3]uint32{r, g, bl}] = true
		}
	}
	return len(seen), b
}

func TestFreeRDPHeadless(t *testing.T) {
	addr := requireVM(t)
	dir := logDir(t)
	l, err := net.Listen("tcp", net.JoinHostPort(addr, "0"))
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	h := newHome(t, func(s *config.Settings) { s.Address, s.Port = addr, port })
	logPath := startApp(t, h.dir, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := vm(ctx, "sh", shotScript(addr, port))
	for _, f := range []string{"freerdp.log", "shot.png"} {
		if _, perr := vm(ctx, "pull", guestDir+"/"+f, filepath.Join(dir, f)); perr != nil {
			t.Logf("pulling %s: %v", f, perr)
		}
	}
	if err != nil {
		t.Fatalf("the guest's script: %v\n%s", err, out)
	}
	if !strings.Contains(out, "window ") || strings.Contains(out, "window \n") {
		b, _ := os.ReadFile(logPath)
		t.Fatalf("xfreerdp showed no window:\n%s\nmardp's log:\n%s", out, b)
	}
	n, bounds := colors(t, filepath.Join(dir, "shot.png"), 1000)
	t.Logf("the screenshot is %v with %d colors or more; files in %s", bounds, n, dir)
	if n < 16 {
		b, _ := os.ReadFile(logPath)
		t.Errorf("the screenshot has %d colors: not the Mac's desktop\nmardp's log:\n%s", n, b)
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"logged in", "session opened", "view-only (-view-only)"} {
		if !bytes.Contains(b, []byte(want)) {
			t.Errorf("mardp's log has no %q:\n%s", want, b)
		}
	}
	if bytes.Contains(b, []byte(testPassword)) {
		t.Error("mardp's log has the password")
	}
}
