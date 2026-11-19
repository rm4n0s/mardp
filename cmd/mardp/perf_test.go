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
	"syscall"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/internal/config"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// perfSeconds is how long the client watches, from MARDP_PERF_SECONDS.
func perfSeconds(t *testing.T) time.Duration {
	t.Helper()
	v := os.Getenv("MARDP_PERF_SECONDS")
	if v == "" {
		return 60 * time.Second
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

// startPerf builds mardp and runs it on h with the test pattern and
// -measure, until it listens.
func startPerf(t *testing.T, h *home) *perfMardp {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "mardp")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	p := &perfMardp{log: filepath.Join(dir, "mardp.log")}
	p.cmd = exec.Command(exe, "-headless", "-test-pattern", "-measure", "-log-file", p.log)
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
		if strings.Contains(sc.Text(), "msg=\""+msg+"\"") {
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
	watch := perfSeconds(t)
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
