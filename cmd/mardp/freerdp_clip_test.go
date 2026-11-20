//go:build darwin && mardp_freerdp

package main

// The proof of the clipboard with a real client (SPEC §10.6, §15.3):
// FreeRDP in the test machine, a host with a clipboard in memory (the
// Mac's own is not touched), and the guest's X clipboard:
//
//	scripts/vm.sh start
//	go test -tags mardp_freerdp -run FreeRDPClipboard -v ./cmd/mardp/
//	scripts/vm.sh stop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/internal/host"
)

// xclipPy owns or reads the X clipboard in the guest, which has no xclip:
// "copy <text> <seconds>" owns it with text for that long, answering who
// asks; "paste" prints its text.
const xclipPy = `import sys, time
from ctypes import *
X = CDLL("libX11.so.6")
X.XOpenDisplay.restype = c_void_p; X.XOpenDisplay.argtypes = [c_char_p]
X.XInternAtom.restype = c_ulong; X.XInternAtom.argtypes = [c_void_p, c_char_p, c_int]
X.XDefaultRootWindow.restype = c_ulong; X.XDefaultRootWindow.argtypes = [c_void_p]
X.XCreateSimpleWindow.restype = c_ulong
X.XCreateSimpleWindow.argtypes = [c_void_p, c_ulong, c_int, c_int, c_uint, c_uint, c_uint, c_ulong, c_ulong]
X.XSetSelectionOwner.argtypes = [c_void_p, c_ulong, c_ulong, c_ulong]
X.XConvertSelection.argtypes = [c_void_p, c_ulong, c_ulong, c_ulong, c_ulong, c_ulong]
X.XNextEvent.argtypes = [c_void_p, c_void_p]
X.XPending.argtypes = [c_void_p]
X.XFlush.argtypes = [c_void_p]
X.XChangeProperty.argtypes = [c_void_p, c_ulong, c_ulong, c_ulong, c_int, c_int, c_void_p, c_int]
X.XSendEvent.argtypes = [c_void_p, c_ulong, c_int, c_long, c_void_p]
X.XGetWindowProperty.argtypes = [c_void_p, c_ulong, c_ulong, c_long, c_long, c_int, c_ulong,
    POINTER(c_ulong), POINTER(c_int), POINTER(c_ulong), POINTER(c_ulong), POINTER(c_void_p)]
class Request(Structure):
    _fields_ = [("type", c_int), ("serial", c_ulong), ("send_event", c_int), ("display", c_void_p), ("owner", c_ulong),
                ("requestor", c_ulong), ("selection", c_ulong), ("target", c_ulong), ("property", c_ulong), ("time", c_ulong)]
class Notify(Structure):
    _fields_ = [("type", c_int), ("serial", c_ulong), ("send_event", c_int), ("display", c_void_p), ("requestor", c_ulong),
                ("selection", c_ulong), ("target", c_ulong), ("property", c_ulong), ("time", c_ulong)]
class Event(Union):
    _fields_ = [("type", c_int), ("request", Request), ("notify", Notify), ("pad", c_long * 24)]
d = X.XOpenDisplay(None)
atom = lambda n: X.XInternAtom(d, n.encode(), 0)
CLIPBOARD, UTF8, TARGETS, PROP = atom("CLIPBOARD"), atom("UTF8_STRING"), atom("TARGETS"), atom("MARDP_SEL")
w = X.XCreateSimpleWindow(d, X.XDefaultRootWindow(d), 0, 0, 1, 1, 0, 0, 0)
ev = Event()
def wait(seconds, handle):
    end = time.time() + seconds
    while time.time() < end:
        if X.XPending(d) == 0:
            time.sleep(0.02)
            continue
        X.XNextEvent(d, byref(ev))
        if handle(ev):
            return True
    return False
if sys.argv[1] == "copy":
    text = sys.argv[2].encode()
    X.XSetSelectionOwner(d, CLIPBOARD, w, 0)
    X.XFlush(d)
    def serve(ev):
        if ev.type != 30:
            return False
        r = ev.request
        prop = r.property
        if r.target == TARGETS:
            atoms = (c_ulong * 2)(TARGETS, UTF8)
            X.XChangeProperty(d, r.requestor, prop, 4, 32, 0, atoms, 2)
        elif r.target == UTF8:
            X.XChangeProperty(d, r.requestor, prop, UTF8, 8, 0, text, len(text))
        else:
            prop = 0
        n = Event()
        n.notify.type, n.notify.display, n.notify.requestor = 31, d, r.requestor
        n.notify.selection, n.notify.target, n.notify.property, n.notify.time = r.selection, r.target, prop, r.time
        X.XSendEvent(d, r.requestor, 0, 0, byref(n))
        X.XFlush(d)
        return False
    wait(float(sys.argv[3]), serve)
else:
    X.XConvertSelection(d, CLIPBOARD, UTF8, PROP, w, 0)
    X.XFlush(d)
    if not wait(5, lambda ev: ev.type == 31) or ev.notify.property == 0:
        sys.exit("no text on the clipboard")
    typ, fmt, n, left, data = c_ulong(), c_int(), c_ulong(), c_ulong(), c_void_p()
    X.XGetWindowProperty(d, w, PROP, 0, 1 << 20, 0, 0, byref(typ), byref(fmt), byref(n), byref(left), byref(data))
    sys.stdout.buffer.write(string_at(data, n.value))
`

// The texts that travel: with letters outside ASCII, and two lines.
const (
	clipFromLinux = "from Linux: μ ✓"
	clipFromMac   = "from the Mac: é 日本\nline 2"
)

// clipScript runs xfreerdp with the clipboard in the guest; two seconds
// after its window shows, the guest's clipboard gets clipFromLinux for
// six seconds, and then the script prints what the clipboard holds between
// the lines "pasted<" and ">pasted".
func clipScript(addr string, port int) string {
	return fmt.Sprintf(`set -u
D=%[1]s; rm -rf "$D"; mkdir -p "$D"; cd "$D"; export HOME="$D"
cat >xclip.py <<'EOF'
%[6]sEOF
Xvfb :98 -screen 0 1280x1024x24 -nolisten tcp >xvfb.log 2>&1 & X=$!
export DISPLAY=:98
i=0; while ! xdotool getdisplaygeometry >/dev/null 2>&1; do i=$((i+1)); [ $i -gt 100 ] && break; sleep 0.1; done
timeout 60 xfreerdp /v:%[2]s:%[3]d '/u:%[4]s' '/p:%[5]s' /cert:ignore /size:1024x768 /bpp:32 +clipboard /log-level:DEBUG >freerdp.log 2>&1 & F=$!
W=$(timeout 30 xdotool search --sync --onlyvisible --class freerdp | head -n 1)
echo "window $W"
if [ -n "$W" ]; then
	sleep 2
	python3 xclip.py copy '%[7]s' 6 2>copy.log
	sleep 1
	echo "pasted<"; python3 xclip.py paste 2>paste.log; echo; echo ">pasted"
	cat copy.log paste.log
fi
kill $F 2>/dev/null
wait $F; echo "exit $?"
kill $X 2>/dev/null; wait 2>/dev/null
true
`, guestDir, addr, port, testUser, testPassword, xclipPy, clipFromLinux)
}

// TestFreeRDPClipboard: text copied in the guest reaches the host's
// clipboard, and text the host copies afterwards is what the guest pastes.
func TestFreeRDPClipboard(t *testing.T) {
	addr := requireVM(t)
	dir := logDir(t)
	mac := &host.FakeClipboard{}
	mac.Put("held before the session", true)
	parts := host.Parts{Screen: &host.PatternScreen{}, Input: &host.RecordingInjector{}, Clipboard: mac}
	logPath := filepath.Join(dir, "mardp.log")
	port := serveHost(t, addr, parts, host.Options{}, logPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// When the guest's text has arrived, the host copies its own.
	arrived := make(chan string, 1)
	go func() {
		for ctx.Err() == nil {
			if text, _ := mac.Text(); text != "held before the session" {
				arrived <- text
				mac.Put(clipFromMac, true)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		arrived <- ""
	}()
	out, err := vm(ctx, "sh", clipScript(addr, port))
	if _, perr := vm(ctx, "pull", guestDir+"/freerdp.log", filepath.Join(dir, "freerdp.log")); perr != nil {
		t.Logf("pulling freerdp.log: %v", perr)
	}
	t.Logf("the guest:\n%s", out)
	if err != nil {
		t.Fatalf("the guest's script: %v", err)
	}
	cancel()
	mlog, _ := os.ReadFile(logPath) // written by the test itself
	for l := range strings.Lines(string(mlog)) {
		if strings.Contains(l, "clipboard") {
			t.Logf("mardp: %s", strings.TrimSpace(l))
		}
	}
	if got := <-arrived; got != clipFromLinux {
		t.Errorf("the host's clipboard got %q from the guest, want %q", got, clipFromLinux)
	}
	_, rest, _ := strings.Cut(out, "pasted<\n")
	pasted, _, _ := strings.Cut(rest, "\n>pasted")
	if strings.ReplaceAll(pasted, "\r\n", "\n") != clipFromMac {
		t.Errorf("the guest pasted %q, want %q", pasted, clipFromMac)
	}
}
