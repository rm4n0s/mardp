//go:build darwin && mardp_perf

package main

// The measurement of step 6.1 (SPEC §14, §15.5): mardp -headless serves its
// test pattern, through VideoToolbox, and a tone, to rdptest as the client,
// which acknowledges every frame and confirms every sound block. It prints
// what a machine can measure by itself: frames delivered a second, the
// delay the host adds (from mardp's -measure log), the bitrate, the CPU and
// the memory of the mardp process. It needs no permission and posts
// nothing to the Mac. scripts/perf.sh runs it:
//
//	scripts/perf.sh            # 60 seconds
//	MARDP_PERF_SECONDS=300 scripts/perf.sh

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/internal/config"
	"github.com/rm4n0s/mardp/internal/mac/vtdec"
	"github.com/rm4n0s/mardp/internal/mac/vtenc"
	"github.com/rm4n0s/mardp/rdp/rdptest"
	"image"
	"image/png"
	"math"
)

// perfSeconds is how long the client watches, from MARDP_PERF_SECONDS,
// or def.
func perfSeconds(t *testing.T, def time.Duration) time.Duration {
	t.Helper()
	v := os.Getenv("MARDP_PERF_SECONDS")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 5 {
		t.Fatalf("MARDP_PERF_SECONDS=%q: a number of seconds, at least 5", v)
	}
	return time.Duration(n) * time.Second
}

// perfMardp is mardp running for the measurement, in a process group of
// its own.
type perfMardp struct {
	cmd *exec.Cmd
	log string
}

// startPerf builds mardp and runs it on h with the test pattern, -measure
// and args, until it listens.
func startPerf(t *testing.T, h *home, args ...string) *perfMardp {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "mardp")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	p := &perfMardp{log: filepath.Join(dir, "mardp.log")}
	p.cmd = exec.Command(exe, append([]string{"-headless", "-test-pattern", "-measure", "-log-file", p.log}, args...)...)
	p.cmd.Env = append(os.Environ(), config.HomeEnv+"="+h.dir)
	p.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if p.cmd.ProcessState == nil {
			_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL) // the test failed before stop
			_ = p.cmd.Wait()
		}
	})
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		b, _ := os.ReadFile(p.log) // not there yet at first
		if bytes.Contains(b, []byte("msg=listening")) {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("mardp does not listen:\n%s", b)
		}
	}
}

// stop ends mardp with SIGTERM and returns its CPU time and its largest
// resident memory.
func (p *perfMardp) stop(t *testing.T) (cpu time.Duration, maxRSS int64) {
	t.Helper()
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("mardp ended with %v", err)
		}
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
		t.Fatalf("mardp did not stop on SIGTERM; killed: %v", <-done)
	}
	ru, ok := p.cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok {
		t.Fatal("no resource usage")
	}
	cpu = time.Duration(syscall.TimevalToNsec(ru.Utime) + syscall.TimevalToNsec(ru.Stime))
	return cpu, ru.Maxrss // bytes on macOS
}

// rss is the resident memory of pid in bytes, from ps.
func rss(t *testing.T, pid int) int64 {
	t.Helper()
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	kb, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		t.Fatalf("ps: %q", out)
	}
	return kb * 1024
}

// logFields is the key=value pairs of the last line of the log at path
// whose message is msg.
func logFields(t *testing.T, path, msg string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var last string
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		// slog quotes a message only when it has spaces.
		if strings.Contains(sc.Text(), "msg=\""+msg+"\"") || strings.Contains(sc.Text(), "msg="+msg+" ") {
			last = sc.Text()
		}
	}
	if last == "" {
		t.Fatalf("no %q in mardp's log:\n%s", msg, b)
	}
	fields := map[string]string{}
	for _, kv := range strings.Fields(last) {
		if k, v, ok := strings.Cut(kv, "="); ok {
			fields[k] = v
		}
	}
	return fields
}

// ms is a field of milliseconds as a duration.
func ms(t *testing.T, fields map[string]string, key string) time.Duration {
	t.Helper()
	f, err := strconv.ParseFloat(fields[key], 64)
	if err != nil {
		t.Fatalf("%s=%q", key, fields[key])
	}
	return time.Duration(f * float64(time.Millisecond))
}

func TestPerf(t *testing.T) {
	watch := perfSeconds(t, 60*time.Second)
	h := newHome(t, nil) // 60 frames a second, 20 Mbit/s, the sound on the client
	p := startPerf(t, h)
	dialed := time.Now()
	c, err := connectSized(t, h, testUser, testPassword, 1920, 1080)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), watch+time.Minute)
	defer cancel()
	if err := c.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	c.KeepChannel(c.ChannelID("rdpsnd"))
	c.KeepChannel(c.ChannelID("drdynvc"))
	g, err := c.StartGFX(ctx, rdptest.AVC420Caps())
	if err != nil {
		t.Fatal(err)
	}
	snd, err := c.NegotiateAudio(ctx, rdptest.AudioOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Each second of the watch: frames of H.264 acknowledged, and the
	// memory of mardp at its end.
	var (
		perSecond         []int
		memory            []int64
		total             uint32
		frames, blocks    int
		videoBytes        int64
		second            = time.Now().Add(time.Second)
		end               = time.Now().Add(watch)
		inSecond, keyless int
		firstPicture      time.Duration
	)
	for time.Now().Before(end) {
		f, err := c.ReadGFXFrame(ctx, g.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		total++
		if err := c.AckFrame(ctx, g.ChannelID, 0, f.ID, total); err != nil {
			t.Fatal(err)
		}
		for _, d := range f.H264 {
			videoBytes += int64(len(d))
		}
		if len(f.H264) > 0 {
			if frames == 0 {
				firstPicture = time.Since(dialed)
			}
			frames++
			inSecond++
		} else {
			keyless++
		}
		for snd.Pending() {
			b, err := snd.ReadBlock(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := snd.Confirm(ctx, b, 0); err != nil {
				t.Fatal(err)
			}
			blocks++
		}
		for now := time.Now(); !now.Before(second); second = second.Add(time.Second) {
			perSecond = append(perSecond, inSecond)
			memory = append(memory, rss(t, p.cmd.Process.Pid))
			inSecond = 0
		}
	}
	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	for b, _ := os.ReadFile(p.log); !bytes.Contains(b, []byte("session video timing")); b, _ = os.ReadFile(p.log) {
		if ctx.Err() != nil {
			t.Fatalf("no session timing in mardp's log:\n%s", b)
		}
		time.Sleep(50 * time.Millisecond)
	}
	cpu, maxRSS := p.stop(t)
	timing := logFields(t, p.log, "session video timing")

	// The first second holds the start of the session.
	steady := perSecond[1:]
	fewest := slices.Min(steady)
	var sum int
	for _, n := range steady {
		sum += n
	}
	mean := float64(sum) / float64(len(steady))
	under := 0
	for _, n := range steady {
		if n < 57 {
			under++
		}
	}
	mb := func(b int64) float64 { return float64(b) / (1 << 20) }
	t.Logf("watched %v at 1920×1080, 60 fps cap, %d bit/s cap", watch, h.s.MaxBitrate)
	t.Logf("from the connection to the first picture: %v (TLS, login, activation, the graphics pipeline, sound)",
		firstPicture.Round(time.Millisecond))
	t.Logf("frames delivered: %d (%d without H.264); a second, after the first: mean %.1f, fewest %d, %d of %d seconds under 57",
		frames, keyless, mean, fewest, under, len(steady))
	t.Logf("sound blocks confirmed: %d (%.1f a second)", blocks, float64(blocks)/watch.Seconds())
	t.Logf("host delay, capture to written (ms): p50 %s, p95 %s, p99 %s, max %s",
		timing["timing.total.p50"], timing["timing.total.p95"], timing["timing.total.p99"], timing["timing.total.max"])
	t.Logf("  capture to encoded: p50 %s, p95 %s; encoded to queued: p95 %s; queued to written: p95 %s",
		timing["timing.encode.p50"], timing["timing.encode.p95"], timing["timing.host.p95"], timing["timing.write.p95"])
	t.Logf("bitrate: %s bit/s by mardp's log, %.0f bit/s of H.264 at the client; key frames %s",
		timing["timing.bitrate"], float64(videoBytes)*8/watch.Seconds(), timing["timing.keyFrames"])
	t.Logf("CPU of mardp: %v in %v, %.1f%% of one core", cpu.Round(time.Millisecond), watch, 100*cpu.Seconds()/watch.Seconds())
	t.Logf("memory of mardp: %.1f MB after 1 s, %.1f MB at the end, %.1f MB at most", mb(memory[0]), mb(memory[len(memory)-1]), mb(maxRSS))

	// The targets of SPEC §14 that the machine measures by itself.
	if p95 := ms(t, timing, "timing.total.p95"); p95 > 25*time.Millisecond {
		t.Errorf("95%% of frames took up to %v from capture to written, the target is 25 ms", p95)
	}
	if firstPicture > 2*time.Second {
		t.Errorf("the first picture came %v after the connection, the target is under 2 s", firstPicture)
	}
	if mean < 57 {
		t.Errorf("%.1f frames a second delivered, the target is 57", mean)
	}
	if cpu.Seconds() > 0.6*watch.Seconds() {
		t.Errorf("mardp used %.0f%% of one core, the target is under 60%%", 100*cpu.Seconds()/watch.Seconds())
	}
	if maxRSS > 300<<20 {
		t.Errorf("mardp held %.0f MB, the target is under 300 MB", mb(maxRSS))
	}
	if bitrate, err := strconv.Atoi(timing["timing.bitrate"]); err != nil || bitrate > h.s.MaxBitrate {
		t.Errorf("bitrate %q, the cap is %d", timing["timing.bitrate"], h.s.MaxBitrate)
	}
}

// The measurements of step 6.4 (SPEC §11 rule 6, §14): an idle session,
// connected on the H.264 path with sound, while the picture is still and
// nothing plays. scripts/perf.sh runs them:
//
//	scripts/perf.sh idle     # mardp -test-pattern -still, 300 seconds
//	scripts/perf.sh screen   # the real screen, 300 seconds
//
// The first needs no permission and fails above the target. The second
// runs mardp as a bundle signed as the M0 probe (scripts/probe-app.sh),
// whose Screen Recording the owner granted; it is -view-only and plays the
// sound on both, so it posts nothing and never touches the Mac's output.
// It only reports: the screen may not be still.

// idleApp builds mardp into a probe-signed bundle, opens it on h with
// -headless -view-only -measure and returns its pid and its log's path once
// it listens. It is stopped when the test ends.
func idleApp(t *testing.T, h *home) (pid int, logPath string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(root, "build", "perf", "MardpIdle.app")
	exe := filepath.Join(app, "Contents", "MacOS", "mardp")
	if ps := exePids(t, exe); len(ps) > 0 {
		t.Fatalf("mardp of an earlier run still runs: %v", ps)
	}
	if err := os.RemoveAll(app); err != nil {
		t.Fatal(err)
	}
	run := func(name string, args ...string) {
		if out, err := exec.CommandContext(ctx, name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
	}
	run("go", "build", "-o", exe, ".")
	run(filepath.Join(root, "scripts", "probe-app.sh"), app, "mardp")
	logPath = filepath.Join(t.TempDir(), "mardp.log")
	run("open", "-n", "--env", config.HomeEnv+"="+h.dir, "--stdout", logPath, "--stderr", logPath,
		app, "--args", "-headless", "-view-only", "-measure")
	t.Cleanup(func() { stopExe(t, exe) })
	for {
		b, _ := os.ReadFile(logPath) // not there yet at first
		switch {
		case bytes.Contains(b, []byte("msg=listening")):
			ps := exePids(t, exe)
			if len(ps) != 1 {
				t.Fatalf("mardp runs as %v", ps)
			}
			return ps[0], logPath
		case bytes.Contains(b, []byte("mardp stopped")):
			t.Fatalf("mardp did not start:\n%s", b)
		case ctx.Err() != nil:
			t.Fatalf("mardp does not listen:\n%s", b)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// exePids is the processes running exe.
func exePids(t *testing.T, exe string) []int {
	t.Helper()
	out, _ := exec.Command("pgrep", "-f", exe).Output() // exit 1: none
	var ps []int
	for f := range strings.FieldsSeq(string(out)) {
		if p, err := strconv.Atoi(f); err == nil {
			ps = append(ps, p)
		}
	}
	return ps
}

// stopExe ends the processes running exe with SIGTERM, and SIGKILL after
// 10 s.
func stopExe(t *testing.T, exe string) {
	t.Helper()
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		for _, p := range exePids(t, exe) {
			if err := syscall.Kill(p, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
				t.Error(err)
			}
		}
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if len(exePids(t, exe)) == 0 {
				return
			}
		}
		t.Errorf("mardp did not stop on %v", sig)
	}
}

// cpuTime is the CPU time pid has used, from ps.
func cpuTime(t *testing.T, pid int) time.Duration {
	t.Helper()
	out, err := exec.Command("ps", "-o", "time=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	// [hours:]minutes:seconds.hundredths
	var d time.Duration
	parts := strings.Split(strings.TrimSpace(string(out)), ":")
	for i, p := range parts {
		f, err := strconv.ParseFloat(p, 64)
		if err != nil {
			t.Fatalf("ps: %q", out)
		}
		unit := time.Second
		for range len(parts) - 1 - i {
			unit *= 60
		}
		d += time.Duration(f * float64(unit))
	}
	return d
}

// wakeups is how many times pid was woken from idle in a sample of
// seconds, by top.
func wakeups(t *testing.T, pid int, seconds int) int {
	t.Helper()
	out, err := exec.Command("top", "-l", "2", "-s", strconv.Itoa(seconds), "-pid", strconv.Itoa(pid), "-stats", "pid,idlew").Output()
	if err != nil {
		t.Fatalf("top: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	f := strings.Fields(lines[len(lines)-1])
	if len(f) != 2 || f[0] != strconv.Itoa(pid) {
		t.Fatalf("top: %q", lines[len(lines)-1])
	}
	n, err := strconv.Atoi(strings.TrimSuffix(f[1], "+"))
	if err != nil {
		t.Fatalf("top: %q", lines[len(lines)-1])
	}
	return n
}

// idleResult is what an idle session measured.
type idleResult struct {
	span, cpu      time.Duration
	frames, blocks int64
	woken, sample  int
}

// share is the CPU used, in percent of one core.
func (r idleResult) share() float64 { return 100 * r.cpu.Seconds() / r.span.Seconds() }

// idleSession connects to mardp at pid on h on the H.264 path with sound,
// acknowledges every frame and confirms every block of sound, and measures
// the CPU of mardp and its wake-ups for watch, after 10 s to settle.
func idleSession(t *testing.T, h *home, pid int, watch time.Duration) idleResult {
	t.Helper()
	c, err := connectSized(t, h, testUser, testPassword, 1920, 1080)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), watch+2*time.Minute)
	defer cancel()
	if err := c.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	c.KeepChannel(c.ChannelID("rdpsnd"))
	c.KeepChannel(c.ChannelID("drdynvc"))
	g, err := c.StartGFX(ctx, rdptest.AVC420Caps())
	if err != nil {
		t.Fatal(err)
	}
	snd, err := c.NegotiateAudio(ctx, rdptest.AudioOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// The client reads until the end of the measurement closes its
	// connection.
	var frames, blocks atomic.Int64
	rctx, stopReading := context.WithCancel(ctx)
	read := make(chan error, 1)
	go func() {
		var total uint32
		for {
			f, err := c.ReadGFXFrame(rctx, g.ChannelID)
			if err != nil {
				read <- err
				return
			}
			total++
			if err := c.AckFrame(rctx, g.ChannelID, 0, f.ID, total); err != nil {
				read <- err
				return
			}
			if len(f.H264) > 0 {
				frames.Add(1)
			}
			for snd.Pending() {
				b, err := snd.ReadBlock(rctx)
				if err != nil {
					read <- err
					return
				}
				if err := snd.Confirm(rctx, b, 0); err != nil {
					read <- err
					return
				}
				blocks.Add(1)
			}
		}
	}()
	for frames.Load() == 0 {
		select {
		case err := <-read:
			t.Fatalf("the client stopped before the first picture: %v", err)
		case <-ctx.Done():
			t.Fatal("no picture came")
		case <-time.After(10 * time.Millisecond):
		}
	}
	time.Sleep(10 * time.Second) // the session's start

	r := idleResult{sample: 30}
	framesBefore, blocksBefore := frames.Load(), blocks.Load()
	cpuBefore, start := cpuTime(t, pid), time.Now()
	r.woken = wakeups(t, pid, r.sample)
	if rest := watch - time.Since(start); rest > 0 {
		select {
		case err := <-read:
			t.Fatalf("the client stopped: %v", err)
		case <-time.After(rest):
		}
	}
	r.cpu, r.span = cpuTime(t, pid)-cpuBefore, time.Since(start)
	r.frames, r.blocks = frames.Load()-framesBefore, blocks.Load()-blocksBefore
	stopReading()
	t.Logf("the client stopped reading: %v", <-read) // the end of rctx
	t.Logf("idle %v at 1920×1080 on the H.264 path, with sound, after 10 s to settle", r.span.Round(time.Second))
	t.Logf("frames the client got meanwhile: %d; blocks of sound: %d", r.frames, r.blocks)
	t.Logf("CPU of mardp: %v, %.2f%% of one core", r.cpu.Round(10*time.Millisecond), r.share())
	t.Logf("woken from idle: %d times in %d s, %.1f a second", r.woken, r.sample, float64(r.woken)/float64(r.sample))
	return r
}

func TestPerfIdle(t *testing.T) {
	watch := perfSeconds(t, 300*time.Second)
	h := newHome(t, nil) // the sound on the client: the still pattern has no output to mute
	p := startPerf(t, h, "-still")
	r := idleSession(t, h, p.cmd.Process.Pid, watch)
	p.stop(t)
	if r.frames > 0 || r.blocks > 0 {
		t.Errorf("%d frames and %d blocks of sound from a still pattern where nothing plays", r.frames, r.blocks)
	}
	if r.share() >= 2 {
		t.Errorf("mardp used %.2f%% of one core while idle, the target is under 2%%", r.share())
	}
}

func TestPerfScreen(t *testing.T) {
	watch := perfSeconds(t, 300*time.Second)
	h := newHome(t, func(s *config.Settings) { s.Sound = config.SoundBoth })
	pid, logPath := idleApp(t, h)
	r := idleSession(t, h, pid, watch)
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("msg=\"capturing sound\"")) {
		t.Errorf("mardp did not capture sound:\n%s", b)
	}
	if r.frames > int64(r.span.Seconds()) {
		t.Logf("the screen was not still: %d frames in %v", r.frames, r.span.Round(time.Second))
	}
}

// The measurement of step 6.5: a long run. mardp -headless -test-pattern
// -measure serves 30 minutes of video and sound to one session, then 100
// sessions in turn that each connect, get the first picture and
// disconnect. It checks that the memory does not grow, that neither the
// video nor the sound drops out, and that once the sessions have ended no
// session, capture (and so no encoder session) or goroutine is left, from
// mardp's "process" log line. scripts/perf.sh long runs it:
//
//	scripts/perf.sh long                        # 1800 seconds
//	MARDP_PERF_SECONDS=300 scripts/perf.sh long

// processLine is mardp's "process" log line (-measure).
type processLine struct {
	goroutines, sessions, captures int
	heap                           int64
}

// awaitProcess waits for a "process" line that mardp logs after the call
// and that settled accepts, and returns it; after three such lines (30 s)
// it returns the last one, settled or not.
func awaitProcess(t *testing.T, path string, settled func(processLine) bool) processLine {
	t.Helper()
	count := func() int {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return bytes.Count(b, []byte("msg=process "))
	}
	from := count()
	deadline := time.Now().Add(time.Minute)
	for {
		if n := count(); n > from {
			f := logFields(t, path, "process")
			num := func(k string) int64 {
				v, err := strconv.ParseInt(f[k], 10, 64)
				if err != nil {
					t.Fatalf("process line: %s=%q", k, f[k])
				}
				return v
			}
			p := processLine{goroutines: int(num("goroutines")), sessions: int(num("sessions")),
				captures: int(num("screenCaptures") + num("videoCaptures") + num("soundCaptures")), heap: num("heapBytes")}
			if settled(p) || n >= from+3 {
				return p
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("mardp logs no process line in a minute")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// longSession is what the long session saw.
type longSession struct {
	perSecond []int         // H.264 frames each second
	rss       []int64       // mardp's memory every 10 s
	blocks    int           // sound blocks confirmed
	gap       time.Duration // the longest wait between two blocks of sound
	gapAt     time.Duration // when, from the start
}

// watchLong connects at 1920×1080 with H.264 and sound and watches for
// watch, acknowledging every frame and confirming every block.
func watchLong(t *testing.T, h *home, pid int, watch time.Duration) longSession {
	t.Helper()
	c, err := connectSized(t, h, testUser, testPassword, 1920, 1080)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), watch+time.Minute)
	defer cancel()
	if err := c.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	c.KeepChannel(c.ChannelID("rdpsnd"))
	c.KeepChannel(c.ChannelID("drdynvc"))
	g, err := c.StartGFX(ctx, rdptest.AVC420Caps())
	if err != nil {
		t.Fatal(err)
	}
	snd, err := c.NegotiateAudio(ctx, rdptest.AudioOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var (
		r        longSession
		total    uint32
		inSecond int
		start    = time.Now()
		second   = start.Add(time.Second)
		sample   = start
		end      = start.Add(watch)
		last     time.Time
	)
	for time.Now().Before(end) {
		f, err := c.ReadGFXFrame(ctx, g.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		total++
		if err := c.AckFrame(ctx, g.ChannelID, 0, f.ID, total); err != nil {
			t.Fatal(err)
		}
		if len(f.H264) > 0 {
			inSecond++
		}
		for snd.Pending() {
			b, err := snd.ReadBlock(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := snd.Confirm(ctx, b, 0); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			if !last.IsZero() && now.Sub(last) > r.gap {
				r.gap, r.gapAt = now.Sub(last), now.Sub(start)
			}
			last = now
			r.blocks++
		}
		now := time.Now()
		for ; !now.Before(second); second = second.Add(time.Second) {
			r.perSecond = append(r.perSecond, inSecond)
			inSecond = 0
		}
		if !now.Before(sample) {
			r.rss = append(r.rss, rss(t, pid))
			sample = sample.Add(10 * time.Second)
		}
	}
	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	return r
}

// cycle connects with H.264 and sound, waits for the first picture and
// disconnects.
func cycle(t *testing.T, h *home) {
	t.Helper()
	c, err := connectSized(t, h, testUser, testPassword, 1920, 1080)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	c.KeepChannel(c.ChannelID("rdpsnd"))
	c.KeepChannel(c.ChannelID("drdynvc"))
	g, err := c.StartGFX(ctx, rdptest.AVC420Caps())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.NegotiateAudio(ctx, rdptest.AudioOptions{}); err != nil {
		t.Fatal(err)
	}
	var total uint32
	for {
		f, err := c.ReadGFXFrame(ctx, g.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		total++
		if err := c.AckFrame(ctx, g.ChannelID, 0, f.ID, total); err != nil {
			t.Fatal(err)
		}
		if len(f.H264) > 0 {
			break
		}
	}
	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPerfLong(t *testing.T) {
	watch := perfSeconds(t, 1800*time.Second)
	const cycles = 100
	h := newHome(t, nil)
	p := startPerf(t, h)
	pid := p.cmd.Process.Pid
	idle := func(l processLine) bool { return l.sessions == 0 && l.captures == 0 }
	before := awaitProcess(t, p.log, idle)

	r := watchLong(t, h, pid, watch)
	afterLong := awaitProcess(t, p.log, func(l processLine) bool { return idle(l) && l.goroutines <= before.goroutines })
	rssAfterLong := rss(t, pid)

	var rssCycles []int64
	start := time.Now()
	for i := range cycles {
		cycle(t, h)
		if i == 9 || i == cycles-1 {
			rssCycles = append(rssCycles, rss(t, pid))
		}
	}
	cycled := time.Since(start)
	after := awaitProcess(t, p.log, func(l processLine) bool { return idle(l) && l.goroutines <= before.goroutines })
	rssEnd := rss(t, pid)
	_, maxRSS := p.stop(t)
	b, err := os.ReadFile(p.log)
	if err != nil {
		t.Fatal(err)
	}

	steady := r.perSecond[1:]
	var sum int
	under := 0
	for _, n := range steady {
		sum += n
		if n < 57 {
			under++
		}
	}
	mean := float64(sum) / float64(len(steady))
	fewest := slices.Min(steady)
	mb := func(b int64) float64 { return float64(b) / (1 << 20) }
	// The memory of the session's first five minutes after its first
	// minute, and of its last five.
	window := min(30, len(r.rss)/3)
	early, late := slices.Max(r.rss[min(6, len(r.rss)-window):][:window]), slices.Max(r.rss[len(r.rss)-window:])
	t.Logf("one session of %v at 1920×1080, 60 fps cap, with sound", watch)
	t.Logf("frames a second, after the first: mean %.1f, fewest %d, %d of %d seconds under 57", mean, fewest, under, len(steady))
	t.Logf("sound: %d blocks confirmed (%.1f a second), the longest wait between two %v (at %v)",
		r.blocks, float64(r.blocks)/watch.Seconds(), r.gap.Round(time.Millisecond), r.gapAt.Round(time.Second))
	t.Logf("memory of mardp: %.1f MB after 10 s, at most %.1f MB early on, %.1f MB in the last stretch, %.1f MB at the end",
		mb(r.rss[1]), mb(early), mb(late), mb(rssAfterLong))
	t.Logf("%d sessions in turn in %v (%v each): memory %.1f MB after 10, %.1f MB after %d, %.1f MB at the end; at most %.1f MB",
		cycles, cycled.Round(time.Second), (cycled / cycles).Round(time.Millisecond), mb(rssCycles[0]), mb(rssCycles[1]), cycles,
		mb(rssEnd), mb(maxRSS))
	t.Logf("goroutines: %d before any session, %d after the long one, %d after the %d; heap %.1f, %.1f, %.1f MB",
		before.goroutines, afterLong.goroutines, after.goroutines, cycles, mb(before.heap), mb(afterLong.heap), mb(after.heap))
	t.Logf("left after the sessions: %d sessions, %d captures", after.sessions, after.captures)

	if mean < 57 || fewest < 30 {
		t.Errorf("the video dropped: mean %.1f frames a second, fewest %d", mean, fewest)
	}
	if r.gap > 250*time.Millisecond {
		t.Errorf("the sound dropped: %v without a block, at %v", r.gap, r.gapAt)
	}
	if late > early+early/10 {
		t.Errorf("the memory grew: %.1f MB at most early on, %.1f MB in the last stretch", mb(early), mb(late))
	}
	if rssCycles[1] > rssCycles[0]+rssCycles[0]/10 {
		t.Errorf("the memory grew over the sessions: %.1f MB after 10, %.1f MB after %d", mb(rssCycles[0]), mb(rssCycles[1]), cycles)
	}
	if afterLong.sessions != 0 || afterLong.captures != 0 || after.sessions != 0 || after.captures != 0 {
		t.Errorf("left after the sessions ended: %+v after the long one, %+v after the %d", afterLong, after, cycles)
	}
	if after.goroutines > before.goroutines || afterLong.goroutines > before.goroutines {
		t.Errorf("goroutines left: %d before any session, %d after the long one, %d after the %d",
			before.goroutines, afterLong.goroutines, after.goroutines, cycles)
	}
	if n := bytes.Count(b, []byte("level=ERROR")); n > 0 {
		t.Errorf("%d errors in mardp's log", n)
	}
	for l := range bytes.Lines(b) {
		if bytes.Contains(l, []byte("level=WARN")) {
			t.Logf("warning in mardp's log: %s", bytes.TrimSpace(l))
		}
	}
}

// A slow client (1.0.1): the moving pattern at 1920×1080 for a client that
// takes 25 ms to show each frame, 40 frames a second at most, as a
// software decoder does. It reports the frames and the bitrate the client
// got in the watch's last third, and fails when the bitrate there is under
// half the start bitrate: a slow client costs frames, not the picture
// (SPEC §9.5). scripts/perf.sh slow runs it (default 45 s).
func TestPerfSlow(t *testing.T) {
	watch := perfSeconds(t, 45*time.Second)
	const show = 25 * time.Millisecond
	h := newHome(t, nil)
	p := startPerf(t, h)
	c, err := connectSized(t, h, testUser, testPassword, 1920, 1080)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), watch+time.Minute)
	defer cancel()
	if err := c.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	c.KeepChannel(c.ChannelID("drdynvc"))
	g, err := c.StartGFX(ctx, rdptest.AVC420Caps())
	if err != nil {
		t.Fatal(err)
	}
	var (
		total         uint32
		frames, bytes int
		start         = time.Now()
		last          = start.Add(watch * 2 / 3)
		end           = start.Add(watch)
	)
	for time.Now().Before(end) {
		f, err := c.ReadGFXFrame(ctx, g.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(show)
		total++
		if err := c.AckFrame(ctx, g.ChannelID, 0, f.ID, total); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(last) {
			frames++
			for _, u := range f.H264 {
				bytes += len(u)
			}
		}
	}
	span := time.Since(last).Seconds()
	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	p.stop(t)
	b, err := os.ReadFile(p.log)
	if err != nil {
		t.Fatal(err)
	}
	mbit := float64(bytes) * 8 / span / 1e6
	t.Logf("a client that shows a frame in %v, for %v: in the last %.0f s, %.1f frames a second at %.1f Mbit/s", show, watch, span, float64(frames)/span, mbit)
	for l := range bytes2Lines(b) {
		if strings.Contains(l, "the client sets the pace") {
			t.Logf("mardp: %s", l)
		}
	}
	if mbit < 4 {
		t.Errorf("%.1f Mbit/s for a slow client: the bitrate fell", mbit)
	}
}

// bytes2Lines yields the lines of b.
func bytes2Lines(b []byte) func(func(string) bool) {
	return func(yield func(string) bool) {
		for l := range bytes.Lines(b) {
			if !yield(strings.TrimSpace(string(l))) {
				return
			}
		}
	}
}

// The pictures of 1.0.2 (SPEC §9.3, §15.5): what a client shows of text.
// mardp -headless -test-pattern -text serves pages of text, a second of
// pages that each differ a little from the one before and then four
// seconds of the last. The test is the client: it decodes every frame
// with VideoToolbox, as Remmina does with OpenH264, and looks at what it
// shows a moment after the screen stopped and three and a half seconds
// later, against the page the screen shows. It fails when the still
// picture is not clean. MARDP_PICTURES names a folder for the pictures.
// scripts/perf.sh picture runs it:
//
//	scripts/perf.sh picture     # pictures in build/pictures
func TestPerfPicture(t *testing.T) {
	const (
		width, height = 1920, 1080
		fps           = 60
		cycle         = 5 * fps // the pattern's cycle, in ticks
		cycles        = 3
	)
	for _, tt := range []struct {
		name string
		cap  int           // the bitrate cap of the settings, 0 for the default
		show time.Duration // how long the client takes to show a frame
	}{
		{"fast-client", 0, 0},
		{"slow-client", 0, 25 * time.Millisecond},
		{"cap-2mbit", 2_000_000, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHome(t, func(s *config.Settings) {
				s.Sound = config.SoundOff
				if tt.cap != 0 {
					s.MaxBitrate = tt.cap
				}
			})
			p := startPerf(t, h, "-text")
			c, err := connectSized(t, h, testUser, testPassword, width, height)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := c.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			c.KeepChannel(c.ChannelID("drdynvc"))
			g, err := c.StartGFX(ctx, rdptest.AVC420Caps())
			if err != nil {
				t.Fatal(err)
			}
			type got struct {
				at   time.Time
				h264 []byte
			}
			frames := make(chan got, 4096)
			read := make(chan error, 1)
			rctx, stop := context.WithCancel(ctx)
			go func() {
				var total uint32
				for {
					f, err := c.ReadGFXFrame(rctx, g.ChannelID)
					if err != nil {
						read <- err
						return
					}
					time.Sleep(tt.show)
					total++
					if err := c.AckFrame(rctx, g.ChannelID, 0, f.ID, total); err != nil {
						read <- err
						return
					}
					for _, u := range f.H264 {
						frames <- got{at: time.Now(), h264: bytes.Clone(u)}
					}
				}
			}()
			dec := vtdec.New()
			defer dec.Close()
			// shown is what the client shows; the test notes it a moment
			// after each burst of frames ended (the screen stopped) and
			// again later.
			var (
				shown             *image.RGBA
				last              time.Time
				burst             int
				asStops, settled  []*image.RGBA
				noteStop, noteEnd time.Time
				count, size       int
			)
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			for len(settled) < cycles {
				select {
				case f := <-frames:
					img, err := dec.Decode(f.h264)
					if err != nil {
						t.Fatal(err)
					}
					shown = img
					count++
					size += len(f.h264)
					if f.at.Sub(last) < 60*time.Millisecond {
						burst++
					} else {
						burst = 0
					}
					last = f.at
				case now := <-tick.C:
					switch {
					case burst >= 10 && now.Sub(last) > 80*time.Millisecond:
						// A second of pages just ended.
						burst = 0
						asStops = append(asStops, shown)
						noteStop, noteEnd = now, now.Add(3500*time.Millisecond)
					case !noteEnd.IsZero() && now.After(noteEnd):
						settled = append(settled, shown)
						noteEnd = time.Time{}
					}
				case err := <-read:
					t.Fatalf("the client stopped reading: %v", err)
				case <-ctx.Done():
					t.Fatalf("%d bursts seen, %d settled, by %v", len(asStops), len(settled), noteStop)
				}
			}
			stop()
			t.Logf("the client stopped reading: %v", <-read)
			if err := c.Disconnect(ctx); err != nil {
				t.Log(err)
			}
			p.stop(t)

			// Which page the screen stopped on: the last of a cycle's
			// first second.
			page := func(img *image.RGBA) (best uint32, db float64) {
				for c := range uint32(12) {
					n := c*cycle + fps - 1
					if d := psnr(img, vtenc.TextPage(width, height, n)); d > db {
						best, db = n, d
					}
				}
				return best, db
			}
			part := image.Rect(0, 0, 960, 220)
			for i := range settled {
				n, after := page(settled[i])
				want := vtenc.TextPage(width, height, n)
				before := psnr(asStops[i], want)
				t.Logf("stop %d, page %d: %.1f dB as the screen stops, %.1f dB three and a half seconds later", i+1, n, before, after)
				if after < 38 {
					t.Errorf("stop %d: the still picture is %.1f dB against the screen, want at least 38", i+1, after)
				}
				if i == len(settled)-1 {
					savePicture(t, tt.name+"-0-the-screen", want, part)
					savePicture(t, tt.name+"-1-as-the-screen-stops", asStops[i], part)
					savePicture(t, tt.name+"-2-later", settled[i], part)
				}
			}
			t.Logf("%d frames, %d bytes", count, size)
		})
	}
}

// psnr is the peak signal-to-noise ratio of got against want, in dB, over
// red, green and blue: about 30 is a soft picture, 40 a clean one; 0 when
// either is missing.
func psnr(got, want *image.RGBA) float64 {
	if got == nil || want == nil || len(got.Pix) != len(want.Pix) {
		return 0
	}
	var sum float64
	for i := 0; i+3 < len(want.Pix); i += 4 {
		for c := range 3 {
			d := float64(got.Pix[i+c]) - float64(want.Pix[i+c])
			sum += d * d
		}
	}
	if sum == 0 {
		return 99
	}
	return 10 * math.Log10(255*255/(sum/float64(len(want.Pix)/4*3)))
}

// savePicture writes the part r of img as name.png into the folder
// MARDP_PICTURES names, when it is set.
func savePicture(t *testing.T, name string, img *image.RGBA, r image.Rectangle) {
	t.Helper()
	dir := os.Getenv("MARDP_PICTURES")
	if dir == "" || img == nil {
		return
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img.SubImage(r)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".png"), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
