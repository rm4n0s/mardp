package main

import (
	"context"
	"log/slog"
	"runtime"
	"time"

	"github.com/rm4n0s/mardp/internal/host"
)

// measureEvery is how often `mardp -headless -measure` logs the H.264
// frames' timing.
const measureEvery = 10 * time.Second

// measurer is `mardp -headless -measure` (SPEC §14, §15.5): it gathers
// each H.264 frame's timing and logs its spread every period, and each
// session's when the session ends; and every period it logs the process:
// its goroutines and memory, the sessions and the captures running.
type measurer struct {
	log     *slog.Logger
	every   time.Duration
	period  host.Timings
	session host.Timings
}

func newMeasurer(log *slog.Logger, every time.Duration) *measurer {
	return &measurer{log: log, every: every}
}

// frame is host.Options.OnFrameTiming.
func (m *measurer) frame(f host.FrameTiming) {
	m.period.Add(f)
	m.session.Add(f)
}

// event is host.Options.OnEvent: the end of a session logs its timing.
func (m *measurer) event(e host.Event) {
	if e.Ended == 0 {
		return
	}
	if s := m.session.Take(); s.Frames > 0 {
		m.log.Info("session video timing", "remote", e.Remote, "timing", s)
	}
}

// hostCounts is what the measurer reads of the host: *host.Host.
type hostCounts interface {
	Sessions() int
	Captures() host.Captures
}

// run logs the timing of every period with frames, and the process every
// period, until ctx ends.
func (m *measurer) run(ctx context.Context, h hostCounts) {
	t := time.NewTicker(m.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if s := m.period.Take(); s.Frames > 0 {
				m.log.Info("video timing", "timing", s)
			}
			m.process(h)
		}
	}
}

// process logs the process's goroutines and memory, and the sessions and
// captures h runs.
func (m *measurer) process(h hostCounts) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	c := h.Captures()
	m.log.Info("process", "goroutines", runtime.NumGoroutine(), "heapBytes", ms.HeapInuse, "sysBytes", ms.Sys,
		"sessions", h.Sessions(), "screenCaptures", c.Screen, "videoCaptures", c.Video, "soundCaptures", c.Sound)
}
