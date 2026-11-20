//go:build darwin && mardp_freerdp

package main

// The proof of step 4.6 (SPEC §10.1, §15.3): FreeRDP in the test machine
// plays the host's sound. The host serves a 1 kHz tone (host.ToneSource),
// as PCM and as AAC through AudioToolbox; FreeRDP plays it through ALSA
// into a file plugin over the null device, so nothing is heard anywhere,
// and the test compares the recording with the tone. It needs no
// permission:
//
//	scripts/vm.sh start
//	go test -tags mardp_freerdp -run FreeRDPSound -v ./cmd/mardp/
//	scripts/vm.sh stop

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/internal/config"
	"github.com/rm4n0s/mardp/internal/host"
	"github.com/rm4n0s/mardp/internal/mac/aacenc"
	"github.com/rm4n0s/mardp/rdp"
)

// toneHz is the tone the host serves.
const toneHz = 1000

// soundScript runs xfreerdp in the guest under a virtual X display against
// addr for a few seconds, playing the sound through ALSA into sound.wav:
// the file plugin writes what FreeRDP plays, the null device under it
// plays it nowhere.
func soundScript(addr string, port int) string { return soundScriptWith(addr, port, "") }

// soundScriptWith is soundScript with more arguments for xfreerdp.
func soundScriptWith(addr string, port int, args string) string {
	return fmt.Sprintf(`set -u
D=%[1]s; rm -rf "$D"; mkdir -p "$D"; cd "$D"; export HOME="$D"
cat >.asoundrc <<'EOF'
pcm.mardpfile {
	type file
	slave.pcm "null"
	file "%[1]s/sound.wav"
	format "wav"
}
EOF
Xvfb :98 -screen 0 1280x1024x24 -nolisten tcp >xvfb.log 2>&1 & X=$!
export DISPLAY=:98
i=0; while ! xdotool getdisplaygeometry >/dev/null 2>&1; do i=$((i+1)); [ $i -gt 100 ] && break; sleep 0.1; done
timeout 60 xfreerdp /v:%[2]s:%[3]d '/u:%[4]s' '/p:%[5]s' /cert:ignore /size:1024x768 /bpp:32 /sound:sys:alsa,dev:mardpfile %[6]s /log-level:DEBUG >freerdp.log 2>&1 & F=$!
W=$(timeout 30 xdotool search --sync --onlyvisible --class freerdp | head -n 1)
echo "window $W"
[ -n "$W" ] && sleep 8
kill $F 2>/dev/null
wait $F; echo "exit $?"
kill $X 2>/dev/null; wait 2>/dev/null
ls -l sound.wav
true
`, guestDir, addr, port, testUser, testPassword, args)
}

// wav is the sound of a WAV file, as samples in [-1, 1], interleaved.
type wav struct {
	rate, channels int
	samples        []float64
}

// readWAV reads the 16-bit PCM WAV at path. The sizes in the header are
// ignored: the file plugin writes them only when the device closes, and
// the data runs to the end of the file.
func readWAV(t *testing.T, path string) wav {
	t.Helper()
	b, err := os.ReadFile(path) // pulled from the guest by the test
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		t.Fatalf("%s is not a WAV file (%d bytes)", path, len(b))
	}
	var w wav
	bits := 0
	for off := 12; off+8 <= len(b); {
		id, size := string(b[off:off+4]), int(binary.LittleEndian.Uint32(b[off+4:]))
		body := b[off+8:]
		if id == "data" {
			if bits != 16 {
				t.Fatalf("%d bits a sample, want 16", bits)
			}
			body = body[:len(body)&^1]
			w.samples = make([]float64, len(body)/2)
			for i := range w.samples {
				w.samples[i] = float64(int16(binary.LittleEndian.Uint16(body[2*i:]))) / math.MaxInt16
			}
			return w
		}
		if id == "fmt " && size >= 16 && len(body) >= 16 {
			if tag := binary.LittleEndian.Uint16(body); tag != 1 {
				t.Fatalf("the WAV's format is %#x, want PCM", tag)
			}
			w.channels = int(binary.LittleEndian.Uint16(body[2:]))
			w.rate = int(binary.LittleEndian.Uint32(body[4:]))
			bits = int(binary.LittleEndian.Uint16(body[14:]))
		}
		off += 8 + size + size&1
	}
	t.Fatalf("%s has no data", path)
	return w
}

// toneReport is how a recording compares with the tone.
type toneReport struct {
	// length is how long the sound lasts, from its first loud sample to
	// its last.
	length time.Duration
	// windows counts the 10 ms windows measured, every 5 ms; bad lists the times of
	// those that are not the tone (silence, a jump, noise), from the
	// sound's start, the first of them.
	windows int
	bad     []time.Duration
	nbad    int
	// rms and purity are their averages: the level, and the share of it at
	// toneHz.
	rms, purity float64
}

// compareTone measures the recording against the tone: between the sound's
// first and last loud sample (less 50 ms at each end, where it starts and
// stops), every 10 ms window of each channel must be the tone at its level
// (a quarter of full scale, an RMS of 0.177) and nothing else: 99 % of it
// at toneHz. The windows overlap by half, so that a jump in the tone falls
// well inside one of them.
func compareTone(w wav) toneReport {
	const (
		loud   = 0.05
		window = 480 // 10 ms: 10 periods of the tone
	)
	frames := len(w.samples) / w.channels
	frame := func(i int) float64 {
		m := 0.0
		for c := range w.channels {
			m = math.Max(m, math.Abs(w.samples[i*w.channels+c]))
		}
		return m
	}
	first, last := -1, -1
	for i := range frames {
		if frame(i) > loud {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	var r toneReport
	if first < 0 {
		return r
	}
	r.length = time.Duration(last-first) * time.Second / time.Duration(w.rate)
	edge := w.rate / 20
	for s := first + edge; s+window <= last-edge; s += window / 2 {
		r.windows++
		ok := true
		for c := range w.channels {
			var sq, re, im float64
			for i := range window {
				v := w.samples[(s+i)*w.channels+c]
				sq += v * v
				a := 2 * math.Pi * toneHz * float64(i) / float64(w.rate)
				re += v * math.Cos(a)
				im -= v * math.Sin(a)
			}
			ms := sq / window
			rms := math.Sqrt(ms)
			purity := 0.0
			if ms > 0 {
				purity = 2 * (re*re + im*im) / (window * window) / ms
			}
			r.rms += rms
			r.purity += purity
			if rms < 0.15 || rms > 0.2 || purity < 0.99 {
				ok = false
			}
		}
		if !ok {
			r.nbad++
			if len(r.bad) < 10 {
				r.bad = append(r.bad, time.Duration(s-first)*time.Second/time.Duration(w.rate))
			}
		}
	}
	if n := float64(r.windows * w.channels); n > 0 {
		r.rms /= n
		r.purity /= n
	}
	return r
}

// soundFormat finds the format of the session's sound in mardp's log.
var soundFormat = regexp.MustCompile(`msg="sending sound" .*format=(\d+)`)

func TestFreeRDPSound(t *testing.T) {
	addr := requireVM(t)
	for _, tt := range []struct {
		name string
		aac  bool
		tag  uint16
	}{
		{"pcm", false, rdp.AudioTagPCM},
		{"aac", true, rdp.AudioTagAAC},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := logDir(t)
			parts := host.Parts{Screen: &host.PatternScreen{}, Sound: &host.ToneSource{Frequency: toneHz}}
			if tt.aac {
				parts.AAC = func() (host.SoundEncoder, error) { return aacenc.New() }
			}
			logPath := filepath.Join(dir, "mardp.log")
			port := serveHost(t, addr, parts, host.Options{}, logPath)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			out, err := vm(ctx, "sh", soundScript(addr, port))
			for _, f := range []string{"freerdp.log", "sound.wav"} {
				if _, perr := vm(ctx, "pull", guestDir+"/"+f, filepath.Join(dir, f)); perr != nil {
					t.Logf("pulling %s: %v", f, perr)
				}
			}
			if err != nil {
				t.Fatalf("the guest's script: %v\n%s", err, out)
			}
			mlog, _ := os.ReadFile(logPath) // written by the test itself
			if !strings.Contains(out, "window ") || strings.Contains(out, "window \n") {
				t.Fatalf("xfreerdp showed no window:\n%s\nmardp's log:\n%s", out, mlog)
			}
			m := soundFormat.FindSubmatch(mlog)
			if m == nil {
				t.Fatalf("the session sent no sound:\n%s", mlog)
			}
			if got, _ := strconv.Atoi(string(m[1])); got != int(tt.tag) {
				t.Errorf("the sound went out as format %#x, want %#x", got, tt.tag)
			}
			w := readWAV(t, filepath.Join(dir, "sound.wav"))
			if w.rate != host.SoundRate || w.channels != 2 {
				t.Errorf("FreeRDP played %d Hz, %d channels; want %d Hz, 2", w.rate, w.channels, host.SoundRate)
			}
			if w.channels == 0 || w.rate == 0 {
				t.FailNow()
			}
			r := compareTone(w)
			t.Logf("%s: %v of sound, %d windows, %d not the tone, RMS %.3f, %.4f of it at %d Hz",
				tt.name, r.length, r.windows, r.nbad, r.rms, r.purity, toneHz)
			if r.length < 4*time.Second {
				t.Errorf("%v of sound, want at least 4 s", r.length)
			}
			if r.nbad > 0 {
				t.Errorf("%d of %d windows are not the tone, at %v", r.nbad, r.windows, r.bad)
			}
			// A dropped packet of whole periods of the tone leaves no mark
			// in the recording: the host says when it drops one.
			for _, l := range []string{"the sound is behind", "sound packets dropped", "the sound stopped", "encoding the sound"} {
				if strings.Contains(string(mlog), l) {
					t.Errorf("mardp's log has %q", l)
				}
			}
		})
	}
}

// toneWAV is secs seconds of the tone as FreeRDP records it, after edit.
func toneWAV(secs float64, edit func([]float64) []float64) wav {
	n := int(secs * host.SoundRate)
	s := make([]float64, 0, 2*n)
	for i := range n {
		v := 0.25 * math.Sin(2*math.Pi*toneHz*float64(i)/host.SoundRate)
		s = append(s, v, v)
	}
	return wav{rate: host.SoundRate, channels: 2, samples: edit(s)}
}

// TestCompareTone checks the check: it needs no test machine.
func TestCompareTone(t *testing.T) {
	const mid = 2 * 2 * host.SoundRate // 2 s in, interleaved
	for _, tt := range []struct {
		name string
		edit func([]float64) []float64
		bad  bool
	}{
		{"the tone", func(s []float64) []float64 { return s }, false},
		{"20 ms of silence", func(s []float64) []float64 { clear(s[mid : mid+2*960]); return s }, true},
		{"half a period dropped", func(s []float64) []float64 { return append(s[:mid], s[mid+2*24:]...) }, true},
		{"too loud", func(s []float64) []float64 {
			for i := range s {
				s[i] *= 1.5
			}
			return s
		}, true},
		{"noise", func(s []float64) []float64 {
			for i := range s {
				s[i] += 0.05 * math.Sin(float64(i)*float64(i))
			}
			return s
		}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := compareTone(toneWAV(4, tt.edit))
			if r.length < 3900*time.Millisecond {
				t.Errorf("%v of sound", r.length)
			}
			if got := r.nbad > 0; got != tt.bad {
				t.Errorf("%d windows not the tone (%v), want any: %v", r.nbad, r.bad, tt.bad)
			}
		})
	}
	if r := compareTone(toneWAV(1, func(s []float64) []float64 { clear(s); return s })); r.length != 0 || r.windows != 0 {
		t.Errorf("silence: %+v", r)
	}
}

// The tone TestFreeRDPRealSound plays on the Mac: quiet, since it comes
// out of the Mac's own output too unless the owner has muted it. The
// output's volume and mute are never touched.
const (
	realToneLevel = 0.05
	realToneSecs  = 4
)

// writeTone writes the tone as a WAV file for afplay.
func writeTone(t *testing.T, path string) {
	t.Helper()
	n := realToneSecs * host.SoundRate
	b := make([]byte, 44+4*n)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+4*n))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 2)
	binary.LittleEndian.PutUint32(b[24:], host.SoundRate)
	binary.LittleEndian.PutUint32(b[28:], 4*host.SoundRate)
	binary.LittleEndian.PutUint16(b[32:], 4)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(4*n))
	for i := range n {
		v := uint16(int16(math.Round(realToneLevel * math.MaxInt16 * math.Sin(2*math.Pi*toneHz*float64(i)/host.SoundRate))))
		binary.LittleEndian.PutUint16(b[44+4*i:], v)
		binary.LittleEndian.PutUint16(b[46+4*i:], v)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFreeRDPRealSound is the whole way of the sound with a real client
// (SPEC §10.1, §15.3): mardp -headless captures the Mac's real sound, with
// the sound on both so that it mutes nothing, another program on the Mac
// (afplay) plays a quiet tone, and FreeRDP in the test machine plays what
// it gets into a file. The test finds the tone in that file.
func TestFreeRDPRealSound(t *testing.T) {
	addr := requireVM(t)
	for _, tt := range []struct{ name, args, path string }{
		{"bitmaps", "", "graphicsPipeline=false"},
		{"h264", "/gfx:AVC420", "path=avc420"},
	} {
		t.Run(tt.name, func(t *testing.T) { freeRDPRealSound(t, addr, tt.args, tt.path) })
	}
}

// freeRDPRealSound is one run of TestFreeRDPRealSound: xfreerdp with args,
// and mardp's log must have path, the session's video path.
func freeRDPRealSound(t *testing.T, addr, args, path string) {
	dir := logDir(t)
	l, err := net.Listen("tcp", net.JoinHostPort(addr, "0"))
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	h := newHome(t, func(s *config.Settings) { s.Address, s.Port, s.Sound = addr, port, config.SoundBoth })
	logPath := startApp(t, h.dir, dir)
	tone := filepath.Join(t.TempDir(), "tone.wav")
	writeTone(t, tone)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// The tone starts two seconds into the session.
	played := make(chan error, 1)
	go func() {
		for {
			b, _ := os.ReadFile(logPath) // read again until the line is there
			if bytes.Contains(b, []byte("session opened")) {
				break
			}
			if ctx.Err() != nil {
				played <- ctx.Err()
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		time.Sleep(2 * time.Second)
		played <- exec.CommandContext(ctx, "afplay", tone).Run()
	}()
	out, err := vm(ctx, "sh", soundScriptWith(addr, port, args))
	for _, f := range []string{"freerdp.log", "sound.wav"} {
		if _, perr := vm(ctx, "pull", guestDir+"/"+f, filepath.Join(dir, f)); perr != nil {
			t.Logf("pulling %s: %v", f, perr)
		}
	}
	if err != nil {
		t.Fatalf("the guest's script: %v\n%s", err, out)
	}
	if perr := <-played; perr != nil {
		t.Errorf("playing the tone: %v", perr)
	}
	mlog, _ := os.ReadFile(logPath) // written by mardp of this test
	for line := range bytes.Lines(mlog) {
		if bytes.Contains(line, []byte("sound")) || bytes.Contains(line, []byte("session")) || bytes.Contains(line, []byte("video path")) {
			t.Logf("mardp: %s", bytes.TrimSpace(line))
		}
	}
	if !bytes.Contains(mlog, []byte(path)) {
		t.Errorf("mardp's log has no %q: not the video path this run is about", path)
	}
	w := readWAV(t, filepath.Join(dir, "sound.wav"))
	if w.channels == 0 || w.rate == 0 {
		t.Fatalf("FreeRDP played nothing it could write: %d Hz, %d channels", w.rate, w.channels)
	}
	// The tone's level in each 10 ms of the left channel.
	const window = 480
	var with, peak = 0, 0.0
	frames := len(w.samples) / w.channels
	for s := 0; s+window <= frames; s += window {
		var re, im float64
		for i := range window {
			v := w.samples[(s+i)*w.channels]
			a := 2 * math.Pi * toneHz * float64(i) / float64(w.rate)
			re += v * math.Cos(a)
			im -= v * math.Sin(a)
			peak = math.Max(peak, math.Abs(v))
		}
		if level := 2 * math.Hypot(re, im) / window; math.Abs(level-realToneLevel) < realToneLevel/4 {
			with++
		}
	}
	got := time.Duration(with) * 10 * time.Millisecond
	t.Logf("FreeRDP played %v of sound (%d Hz, %d channels, peak %.3f), %v of it the tone at its level",
		time.Duration(frames)*time.Second/time.Duration(w.rate), w.rate, w.channels, peak, got)
	if got < (realToneSecs-1)*time.Second {
		t.Errorf("%v of the tone reached the client, want about %d s", got, realToneSecs)
	}
}
