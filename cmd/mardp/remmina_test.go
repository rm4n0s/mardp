//go:build darwin && mardp_freerdp

package main

// The proof with Remmina itself (SPEC §15.3): the Remmina of the test
// machine, not FreeRDP's own client, connects with a profile as its
// window writes one, and the host serves a test pattern, a tone and a
// clipboard in memory:
//
//	scripts/vm.sh start
//	go test -tags mardp_freerdp -run Remmina -v ./cmd/mardp/
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
	"github.com/rm4n0s/mardp/internal/mac/aacenc"
)

// remminaScript runs Remmina in the guest under a virtual X display with
// a profile for addr: sound "local", the clipboard on, and colordepth as
// Remmina's menu of the color depth stores it. Twelve seconds after it
// starts, the guest's clipboard gets clipFromLinux for six seconds; then
// the script prints what the clipboard holds between "pasted<" and
// ">pasted".
func remminaScript(addr string, port int, colordepth string) string {
	return fmt.Sprintf(`set -u
D=%[1]s; rm -rf "$D"; mkdir -p "$D"; cd "$D"; export HOME="$D" XDG_RUNTIME_DIR="$D/run"
mkdir -p "$XDG_RUNTIME_DIR"; chmod 700 "$XDG_RUNTIME_DIR"
cat >xclip.py <<'PYEOF'
%[6]sPYEOF
cat >mardp.remmina <<PROFILE
[remmina]
name=mardp
protocol=RDP
server=%[2]s:%[3]d
username=%[4]s
colordepth=%[8]s
sound=local
cert_ignore=1
disableclipboard=0
window_maximize=0
viewmode=1
PROFILE
Xvfb :98 -screen 0 1280x1024x24 -nolisten tcp >xvfb.log 2>&1 & X=$!
export DISPLAY=:98
i=0; while ! xdotool getdisplaygeometry >/dev/null 2>&1; do i=$((i+1)); [ $i -gt 100 ] && break; sleep 0.1; done
cat >session.sh <<'SESSION'
# A sound server as a desktop has one: PipeWire with its PulseAudio
# server, and a sink that plays nowhere, whose monitor is recorded.
pipewire >pipewire.log 2>&1 & P1=$!
wireplumber >wireplumber.log 2>&1 & P2=$!
pipewire-pulse >pulse.log 2>&1 & P3=$!
i=0; while ! pactl info >/dev/null 2>&1; do i=$((i+1)); [ $i -gt 100 ] && break; sleep 0.1; done
# Every sink the machine has is muted first: nothing of this is to be heard.
for k in $(pactl list short sinks 2>/dev/null | cut -f2); do pactl set-sink-mute "$k" 1 2>>sink.log; done
pw-cli create-node adapter '{ factory.name=support.null-audio-sink node.name=mardp media.class=Audio/Sink object.linger=true audio.position=[FL FR] }' >/dev/null 2>>sink.log
i=0; while ! pactl list short sinks 2>/dev/null | grep -qw mardp; do i=$((i+1)); [ $i -gt 50 ] && break; sleep 0.1; done
pactl set-default-sink mardp 2>>sink.log
parecord --device=mardp.monitor --file-format=wav --rate=48000 --channels=2 --format=s16le sound.wav 2>record.log & REC=$!
printf '%%s\n' "$1" | remmina --update-profile mardp.remmina --set-option password >update.log 2>&1
timeout 40 remmina -c "$PWD/mardp.remmina" >remmina.log 2>&1 &
R=$!
sleep 12
python3 xclip.py copy "$2" 6 2>copy.log
sleep 1
echo "pasted<"; python3 xclip.py paste 2>paste.log; echo; echo ">pasted"
sleep 3
echo "--- sound server"; pactl info 2>&1 | grep -E "Server Name|Default Sink"; pactl list short sinks 2>&1; pactl list short sink-inputs 2>&1
kill $R 2>/dev/null
wait $R 2>/dev/null
kill $REC 2>/dev/null; wait $REC 2>/dev/null
kill $P3 $P2 $P1 2>/dev/null
SESSION
dbus-run-session -- sh session.sh '%[5]s' '%[7]s'
echo "--- copy/paste"; cat copy.log paste.log
echo "--- sound"; cat sink.log record.log; ls -l sound.wav
kill $X 2>/dev/null; wait 2>/dev/null
true
`, guestDir, addr, port, testUser, testPassword, xclipPy, clipFromLinux, colordepth)
}

// TestRemmina: Remmina gets the host's sound and shares its clipboard.
func TestRemmina(t *testing.T) {
	addr := requireVM(t)
	for _, tt := range []struct{ name, colordepth string }{
		{"GFX AVC420", "66"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := logDir(t)
			mac := &host.FakeClipboard{}
			mac.Put("held before the session", true)
			parts := host.Parts{Screen: &host.PatternScreen{}, Input: &host.RecordingInjector{}, Clipboard: mac,
				Sound: &host.ToneSource{Frequency: toneHz}, AAC: func() (host.SoundEncoder, error) { return aacenc.New() }}
			logPath := filepath.Join(dir, "mardp.log")
			port := serveHost(t, addr, parts, host.Options{}, logPath)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
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
			out, err := vm(ctx, "sh", remminaScript(addr, port, tt.colordepth))
			for _, f := range []string{"remmina.log", "sound.wav"} {
				if _, perr := vm(ctx, "pull", guestDir+"/"+f, filepath.Join(dir, f)); perr != nil {
					t.Logf("pulling %s: %v", f, perr)
				}
			}
			t.Logf("the guest:\n%s", out)
			if err != nil {
				t.Fatalf("the guest's script: %v", err)
			}
			cancel()
			mlog, _ := os.ReadFile(logPath) // written by the test itself
			for l := range strings.Lines(string(mlog)) {
				for _, w := range []string{"clipboard", "sound", "client data", "session opened", "video path", "channels", "login", "ended", "closed"} {
					if strings.Contains(l, w) {
						t.Logf("mardp: %s", strings.TrimSpace(l))
						break
					}
				}
			}
			if !strings.Contains(string(mlog), "sound ready") {
				t.Errorf("Remmina did not set the sound up")
			}
			// The sound: Remmina plays the tone into the sound server, all
			// the while, and the host drops none of it.
			for _, l := range []string{"the sound is behind", "sound packets dropped", "the sound stopped", "encoding the sound"} {
				if n := strings.Count(string(mlog), l); n > 0 {
					t.Errorf("mardp's log has %q %d times", l, n)
				}
			}
			w := readWAV(t, filepath.Join(dir, "sound.wav"))
			if w.channels == 0 || w.rate == 0 {
				t.Fatalf("the recording: %d Hz, %d channels", w.rate, w.channels)
			}
			r := compareTone(w)
			t.Logf("Remmina played %v of sound (%d Hz, %d channels): %d windows, %d not the tone, RMS %.3f, %.4f of it at %d Hz",
				r.length, w.rate, w.channels, r.windows, r.nbad, r.rms, r.purity, toneHz)
			if r.length < 15*time.Second {
				t.Errorf("%v of sound, want the session's 20 s or so", r.length)
			}
			if r.nbad*50 > r.windows {
				t.Errorf("%d of %d windows are not the tone, the first at %v", r.nbad, r.windows, r.bad)
			}
			if got := <-arrived; got != clipFromLinux {
				t.Errorf("the host's clipboard got %q from the guest, want %q", got, clipFromLinux)
			}
			_, rest, _ := strings.Cut(out, "pasted<\n")
			pasted, _, _ := strings.Cut(rest, "\n>pasted")
			if strings.ReplaceAll(pasted, "\r\n", "\n") != clipFromMac {
				t.Errorf("the guest pasted %q, want %q", pasted, clipFromMac)
			}
		})
	}
}
