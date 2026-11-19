package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/rm4n0s/mardp/internal/host"
)

// measureEvery is how often `mardp -headless -measure` logs the H.264
// frames' timing.
const measureEvery = 10 * time.Second

// measurer is `mardp -headless -measure` (SPEC §14, §15.5): it gathers
// each H.264 frame's timing and logs its spread every period, and each
// session's when the session ends.
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

// run logs the timing of every period with frames until ctx ends.
func (m *measurer) run(ctx context.Context) {
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
		}
	}
}
