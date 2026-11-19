package host

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// events records a host's events.
type events struct {
	mu sync.Mutex
	es []Event
}

func (e *events) add(ev Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.es = append(e.es, ev)
}

func (e *events) get() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.es)
}

// TestSessionInfo: the running session's details and numbers, and the
// events of its opening and its end, for each way a session ends.
func TestSessionInfo(t *testing.T) {
	tests := []struct {
		name   string
		screen ScreenSource
		end    func(t *testing.T, s *session)
		want   Ended
	}{
		{"the client disconnects", &PatternScreen{}, func(t *testing.T, s *session) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.c.Disconnect(ctx); err != nil {
				t.Fatal(err)
			}
		}, EndedByClient},
		{"the owner disconnects", &PatternScreen{}, func(t *testing.T, s *session) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.h.Disconnect(ctx); err != nil {
				t.Fatal(err)
			}
			_, info, err := s.c.AwaitDisconnect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if info == 0 {
				t.Error("no Set Error Info")
			}
			if err := s.c.Close(); err != nil {
				t.Fatal(err)
			}
		}, EndedByOwner},
		{"the capture fails", &errScreen{d: Display{Width: 640, Height: 480, PointWidth: 640, PointHeight: 480},
			capture: errors.New("TestCaptureFailed", "no capture")}, nil, EndedByHost},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := &events{}
			since, start := time.Unix(5000, 0), time.Now()
			now := func() time.Time { return since.Add(time.Since(start)) }
			s := serve(t, Parts{Screen: tt.screen}, Options{OnEvent: ev.add, statsEvery: 20 * time.Millisecond, now: now}, 640, 360)
			want := []Event{{Client: rdptest.DefaultName, User: testUser}}
			if tt.end != nil {
				s.sc = rdptest.NewScreen(640, 360)
				var info SessionInfo
				s.readUntil(t, "the session's numbers", func() bool {
					var ok bool
					info, ok = s.h.Session()
					return ok && info.FPS > 0 && info.Bitrate > 0
				})
				if info.Client != rdptest.DefaultName || info.Remote == "" || info.Since.Sub(since) > time.Second ||
					info.Width != 640 || info.Height != 360 || info.Video != rdp.VideoBitmaps || info.Sound != "" || info.Delay != 0 {
					t.Errorf("session %+v", info)
				}
				want[0].Remote = info.Remote
				tt.end(t, s)
			}
			waitFor(t, func() bool { return len(ev.get()) == 2 })
			got := ev.get()
			if tt.end == nil {
				want[0].Remote = got[0].Remote
			}
			want = append(want, want[0])
			want[1].Ended = tt.want
			if !slices.Equal(got, want) {
				t.Errorf("events\n%+v\nwant\n%+v", got, want)
			}
			if _, ok := s.h.Session(); ok {
				t.Error("a session after the end")
			}
		})
	}
}

// TestDisconnectWithoutSession does nothing.
func TestDisconnectWithoutSession(t *testing.T) {
	h, err := New(Parts{Screen: &PatternScreen{}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Disconnect(context.Background()); err != nil {
		t.Error(err)
	}
	if _, ok := h.Session(); ok {
		t.Error("a session")
	}
}
