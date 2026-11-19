package host

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// memMarker is a MuteMarker in memory; its calls can be made to fail.
type memMarker struct {
	mu                       sync.Mutex
	uid                      string
	ok                       bool
	markErr, readErr, clrErr error
}

func (m *memMarker) MarkMuted(_ context.Context, uid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.markErr != nil {
		return m.markErr
	}
	m.uid, m.ok = uid, true
	return nil
}

func (m *memMarker) MutedMark(context.Context) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.uid, m.ok, m.readErr
}

func (m *memMarker) ClearMuted(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.clrErr != nil {
		return m.clrErr
	}
	m.uid, m.ok = "", false
	return nil
}

// mark is the device marked, "" for none.
func (m *memMarker) mark() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.uid
}

func TestMuter(t *testing.T) {
	const spk, hp = "Speakers", "Headphones"
	failed := errors.New("FakeFailed", "it failed")
	tests := []struct {
		name string
		// setup prepares the output and the marker; during runs between
		// the hold and the release.
		setup  func(o *FakeOutput, m *memMarker)
		during func(o *FakeOutput)
		// muted is whether the speakers are muted while held, and after
		// the release; mark the marker while held, and after.
		muted, after    bool
		mark, markAfter string
		log             string
	}{
		{"mutes and unmutes", nil, nil, true, false, spk, "", "the Mac's output is muted while the client plays the sound"},
		{"muted already", func(o *FakeOutput, _ *memMarker) { o.Mute(spk, true) }, nil, true, true, "", "",
			"the Mac's output is muted already"},
		{"no default output", func(o *FakeOutput, _ *memMarker) { o.Fail("default", failed) }, nil, false, false, "", "",
			"the sound plays on the Mac too: no default output"},
		{"no mute to read", func(o *FakeOutput, _ *memMarker) { o.Fail("muted", failed) }, nil, false, false, "", "",
			"the sound plays on the Mac too: the output cannot be muted"},
		{"no mute to set", func(o *FakeOutput, _ *memMarker) { o.Fail("set", failed) }, nil, false, false, "", "",
			"the sound plays on the Mac too: the output cannot be muted"},
		{"no marker", func(_ *FakeOutput, m *memMarker) { m.markErr = failed }, nil, false, false, "", "",
			"the sound plays on the Mac too: the mute could not be recorded"},
		{"the default changes", nil, func(o *FakeOutput) { o.SetDefault(hp) }, true, false, spk, "",
			"the Mac's output is unmuted"},
		{"the owner unmutes", nil, func(o *FakeOutput) { o.Mute(spk, false) }, false, false, spk, "",
			"the Mac's output is unmuted"},
		{"unplugged", nil, func(o *FakeOutput) { o.Remove(spk) }, false, false, spk, spk,
			"the Mac's output could not be unmuted"},
		{"the marker stays", func(_ *FakeOutput, m *memMarker) { m.clrErr = failed }, nil, true, false, spk, spk,
			"the record of the muted output could not be removed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, mk, rec := NewFakeOutput(spk, hp), &memMarker{}, &recorder{}
			if tt.setup != nil {
				tt.setup(o, mk)
			}
			m := &muter{out: o, marker: mk, log: slog.New(rec)}
			release := m.hold(t.Context())
			if tt.during != nil {
				tt.during(o)
			}
			if o.Muted(spk) != tt.muted || mk.mark() != tt.mark {
				t.Errorf("held: muted %v, marked %q; want %v, %q", o.Muted(spk), mk.mark(), tt.muted, tt.mark)
			}
			release()
			release()
			if o.Muted(spk) != tt.after || mk.mark() != tt.markAfter {
				t.Errorf("released: muted %v, marked %q; want %v, %q", o.Muted(spk), mk.mark(), tt.after, tt.markAfter)
			}
			if o.Muted(hp) {
				t.Error("the headphones were muted")
			}
			if len(rec.find(tt.log)) != 1 {
				t.Errorf("no log %q", tt.log)
				rec.dump(t)
			}
		})
	}
}

// TestMuterSessions: the output is muted once for two sessions, and
// unmuted when the last ends; a muter without an output does nothing.
func TestMuterSessions(t *testing.T) {
	o, mk := NewFakeOutput("Speakers"), &memMarker{}
	m := &muter{out: o, marker: mk, log: slog.New(slog.DiscardHandler)}
	a := m.hold(t.Context())
	b := m.hold(t.Context())
	a()
	a()
	if !o.Muted("Speakers") {
		t.Fatal("unmuted while a session holds it")
	}
	b()
	if o.Muted("Speakers") || mk.mark() != "" {
		t.Fatal("still muted")
	}
	if o.Writes() != 2 {
		t.Errorf("%d writes, want 2", o.Writes())
	}
	none := &muter{log: slog.New(slog.DiscardHandler)}
	none.hold(t.Context())()
}

func TestRecoverOutput(t *testing.T) {
	failed := errors.New("FakeFailed", "it failed")
	tests := []struct {
		name  string
		setup func(o *FakeOutput, m *memMarker)
		muted bool
		mark  string
		log   string
	}{
		{"no marker", func(o *FakeOutput, _ *memMarker) { o.Mute("Speakers", true) }, true, "", ""},
		{"unmuted", func(o *FakeOutput, m *memMarker) {
			o.Mute("Speakers", true)
			m.uid, m.ok = "Speakers", true
		}, false, "", "unmuted the output mardp left muted"},
		{"unplugged", func(o *FakeOutput, m *memMarker) { m.uid, m.ok = "USB", true }, false, "USB",
			"the output mardp muted before could not be unmuted"},
		{"unreadable", func(_ *FakeOutput, m *memMarker) { m.readErr = failed }, false, "",
			"the record of the muted output could not be read"},
		{"stuck", func(o *FakeOutput, m *memMarker) {
			o.Mute("Speakers", true)
			m.uid, m.ok, m.clrErr = "Speakers", true, failed
		}, false, "Speakers", "the record of the muted output could not be removed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, mk, rec := NewFakeOutput("Speakers"), &memMarker{}, &recorder{}
			tt.setup(o, mk)
			RecoverOutput(t.Context(), o, mk, slog.New(rec))
			if o.Muted("Speakers") != tt.muted || mk.mark() != tt.mark {
				t.Errorf("muted %v, marked %q; want %v, %q", o.Muted("Speakers"), mk.mark(), tt.muted, tt.mark)
			}
			if tt.log != "" && len(rec.find(tt.log)) != 1 {
				t.Errorf("no log %q", tt.log)
				rec.dump(t)
			}
		})
	}
	RecoverOutput(t.Context(), NewFakeOutput(), &memMarker{}, nil)
}

func TestFakeOutputUnknown(t *testing.T) {
	o := NewFakeOutput()
	_, err := o.OutputMuted("x")
	assertRoute(t, err, "FakeOutput.OutputMuted.FakeOutputUnknown")
	assertRoute(t, o.SetOutputMuted("x", true), "FakeOutput.SetOutputMuted.FakeOutputUnknown")
}

// TestSessionMute: the Mac's output is muted while a client gets the
// sound, and unmuted when it leaves; a client that plays nothing leaves it
// alone.
func TestSessionMute(t *testing.T) {
	tests := []struct {
		name  string
		opts  rdptest.AudioOptions
		muted bool
		log   string
	}{
		{"plays the sound", rdptest.AudioOptions{}, true, "sending sound"},
		{"plays nothing", rdptest.AudioOptions{Flags: 2}, false, "no sound for this session"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, mk := NewFakeOutput("Speakers"), &memMarker{}
			s := serve(t, Parts{Screen: &PatternScreen{Frames: 1}, Sound: &ToneSource{}, Output: o},
				Options{MuteMarker: mk}, 640, 480)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := s.c.NegotiateAudio(ctx, tt.opts); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return len(s.rec.find(tt.log)) > 0 })
			if tt.muted {
				waitFor(t, func() bool { return o.Muted("Speakers") })
				if mk.mark() != "Speakers" {
					t.Errorf("marked %q", mk.mark())
				}
			}
			if err := s.c.Close(); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return s.h.Sessions() == 0 })
			if o.Muted("Speakers") || mk.mark() != "" {
				t.Errorf("after the session: muted %v, marked %q", o.Muted("Speakers"), mk.mark())
			}
			if want := map[bool]int{true: 2, false: 0}[tt.muted]; o.Writes() != want {
				t.Errorf("%d writes, want %d", o.Writes(), want)
			}
		})
	}
}

// waitFor waits up to 5 s for cond.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
