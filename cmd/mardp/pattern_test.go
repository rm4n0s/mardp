package main

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/internal/config"
	"github.com/rm4n0s/mardp/internal/host"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// TestServePattern: `mardp -headless -test-pattern -measure`, moving or
// -still, needs no permission of the Mac, serves the machine's test
// pattern over H.264 without making the screen's parts, and logs the
// session's timing when it ends.
func TestServePattern(t *testing.T) {
	for _, tt := range []struct {
		name   string
		args   []string
		log    string
		stills int
	}{
		{"moving", []string{"-headless", "-test-pattern", "-measure"}, "serving a test pattern and a tone", 0},
		{"still", []string{"-headless", "-test-pattern", "-still", "-measure"}, "serving a still test pattern and no sound", 1},
	} {
		t.Run(tt.name, func(t *testing.T) { servePattern(t, tt.args, tt.log, tt.stills) })
	}
}

// servePattern runs mardp with args, a session of 10 H.264 frames, and
// checks the log has want, and the machine made the pattern once, still
// stills times, and never the screen's parts.
func servePattern(t *testing.T, args []string, want string, stills int) {
	h := newHome(t, func(s *config.Settings) { s.Sound = config.SoundOff })
	m := newFakeMac() // nothing granted
	r := start(t, h, m, args...)
	c, err := connect(t, h, testUser, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	c.KeepChannel(c.ChannelID("drdynvc"))
	g, err := c.StartGFX(ctx, rdptest.AVC420Caps())
	if err != nil {
		t.Fatal(err)
	}
	var total uint32
	for frames := 0; frames < 10; {
		f, err := c.ReadGFXFrame(ctx, g.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		total++
		if err := c.AckFrame(ctx, g.ChannelID, 0, f.ID, total); err != nil {
			t.Fatal(err)
		}
		if len(f.H264) > 0 {
			frames++
		}
	}
	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	for !strings.Contains(r.log.String(), "session video timing") {
		if ctx.Err() != nil {
			t.Fatalf("no session timing in the log:\n%s", r.log)
		}
		time.Sleep(20 * time.Millisecond)
	}
	r.stop(t)
	log := r.log.String()
	for _, w := range []string{want, "sessions are view-only", "timing.total.p95="} {
		if !strings.Contains(log, w) {
			t.Errorf("the log has no %q:\n%s", w, log)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.patterns != 1 || m.stills != stills || len(m.posts) != 0 {
		t.Errorf("the pattern made %d times, %d still, the screen's parts %d times", m.patterns, m.stills, len(m.posts))
	}
}

// TestMeasurer: the timing of each period with frames is logged, and each
// session's when it ends.
func TestMeasurer(t *testing.T) {
	var buf syncBuffer
	me := newMeasurer(slog.New(slog.NewTextHandler(&buf, nil)), time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		me.run(ctx)
	}()
	now := time.Now()
	me.frame(host.FrameTiming{Captured: now, Encoded: now, Queued: now, Written: now.Add(3 * time.Millisecond), Bytes: 10})
	for !strings.Contains(buf.String(), "msg=\"video timing\"") {
		time.Sleep(time.Millisecond)
	}
	me.event(host.Event{Remote: "1.2.3.4"}) // opened: nothing
	me.event(host.Event{Ended: host.EndedByClient, Remote: "1.2.3.4"})
	me.event(host.Event{Ended: host.EndedByClient, Remote: "1.2.3.4"}) // no frames since
	cancel()
	<-done
	log := buf.String()
	if n := strings.Count(log, "session video timing"); n != 1 || !strings.Contains(log, "remote=1.2.3.4 timing.frames=1") {
		t.Errorf("%d session timings:\n%s", n, log)
	}
	if strings.Count(log, "msg=\"video timing\"") != 1 {
		t.Errorf("periods without frames logged:\n%s", log)
	}
}
