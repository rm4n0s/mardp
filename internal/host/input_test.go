package host

import (
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp"
)

// testRelay is a relay over a display of 2000×1000 points, for a desktop of
// 1000×500, with a clock the test moves.
func testRelay(in Injector, log *slog.Logger) (*relay, *time.Time) {
	clock := time.Unix(1000, 0)
	d := Display{Width: 4000, Height: 2000, PointWidth: 2000, PointHeight: 1000}
	return newRelay(in, log, func() time.Time { return clock }, d, 1000, 500, DefaultDoubleClickTime, DefaultDoubleClickDistance), &clock
}

func TestRelayClicks(t *testing.T) {
	type press struct {
		after time.Duration
		b     rdp.MouseButton
		x, y  int
	}
	tests := []struct {
		name    string
		presses []press
		want    []int // the click count of each press, down and up
	}{
		{"single", []press{{0, rdp.ButtonLeft, 10, 10}}, []int{1}},
		{"double", []press{{0, rdp.ButtonLeft, 10, 10}, {200 * time.Millisecond, rdp.ButtonLeft, 11, 11}}, []int{1, 2}},
		{"triple", []press{{0, rdp.ButtonLeft, 10, 10}, {100 * time.Millisecond, rdp.ButtonLeft, 10, 10}, {100 * time.Millisecond, rdp.ButtonLeft, 10, 10}}, []int{1, 2, 3}},
		{"too late", []press{{0, rdp.ButtonLeft, 10, 10}, {501 * time.Millisecond, rdp.ButtonLeft, 10, 10}}, []int{1, 1}},
		{"just in time", []press{{0, rdp.ButtonLeft, 10, 10}, {500 * time.Millisecond, rdp.ButtonLeft, 10, 10}}, []int{1, 2}},
		// 3 desktop pixels are 6 points, more than 4.
		{"too far", []press{{0, rdp.ButtonLeft, 10, 10}, {100 * time.Millisecond, rdp.ButtonLeft, 13, 10}}, []int{1, 1}},
		{"other button", []press{{0, rdp.ButtonLeft, 10, 10}, {100 * time.Millisecond, rdp.ButtonRight, 10, 10}}, []int{1, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := &RecordingInjector{}
			r, clock := testRelay(in, slog.New(slog.DiscardHandler))
			for _, p := range tt.presses {
				*clock = clock.Add(p.after)
				r.MouseButton(p.b, true, p.x, p.y)
				r.MouseButton(p.b, false, p.x, p.y)
			}
			var got []int
			for i, e := range in.Events() {
				if i%2 == 1 {
					if e.Clicks != got[len(got)-1] || e.Down {
						t.Errorf("up %+v does not match its down", e)
					}
					continue
				}
				got = append(got, e.Clicks)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("clicks %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRelayScaling(t *testing.T) {
	tests := []struct {
		x, y   int
		px, py float64
	}{
		{0, 0, 0, 0},
		{500, 250, 1000, 500},
		{999, 499, 1998, 998},
		{1000, 500, 1999, 999},
		{-5, -5, 0, 0},
		{65535, 65535, 1999, 999},
	}
	for _, tt := range tests {
		in := &RecordingInjector{}
		r, _ := testRelay(in, slog.New(slog.DiscardHandler))
		r.MouseMove(tt.x, tt.y)
		e := in.Events()[0]
		if e.X != tt.px || e.Y != tt.py {
			t.Errorf("move to %d,%d posted at %v,%v, want %v,%v", tt.x, tt.y, e.X, e.Y, tt.px, tt.py)
		}
	}
}

func TestRelayRelease(t *testing.T) {
	in := &RecordingInjector{}
	r, _ := testRelay(in, slog.New(slog.DiscardHandler))
	mice := 0
	r.onMouse = func() { mice++ }
	r.Key(0x2a, rdp.NotExtended, true)
	r.Key(0x2a, rdp.NotExtended, true) // repeat
	r.Key(0x1e, rdp.NotExtended, true)
	r.Key(0x1e, rdp.NotExtended, false)
	r.Unicode('ж', true)
	r.MouseButton(rdp.ButtonMiddle, true, 50, 60)
	r.MouseMove(70, 80)
	r.Wheel(0, 120)

	// A synchronize event releases the keys but not the buttons.
	n := len(in.Events())
	r.SyncLocks(true, false, true)
	got := in.Events()[n:]
	want := []InputEvent{
		{Kind: InputKey, Scancode: 0x2a},
		{Kind: InputUnicode, Rune: 'ж'},
		{Kind: InputLocks, Caps: true, Scroll: true},
	}
	if !slices.Equal(got, want) {
		t.Errorf("sync posted\n%+v\nwant\n%+v", got, want)
	}

	r.Key(0x38, rdp.ExtendedE0, true)
	n = len(in.Events())
	r.release()
	got = in.Events()[n:]
	want = []InputEvent{
		{Kind: InputKey, Scancode: 0x38, Extended: rdp.ExtendedE0},
		{Kind: InputButton, Button: rdp.ButtonMiddle, X: 140, Y: 160, Clicks: 1},
	}
	if !slices.Equal(got, want) {
		t.Errorf("release posted\n%+v\nwant\n%+v", got, want)
	}

	// Nothing after the release, and a second release does nothing.
	n = len(in.Events())
	r.Key(0x1e, rdp.NotExtended, true)
	r.Unicode('a', true)
	r.MouseMove(1, 1)
	r.MouseButton(rdp.ButtonLeft, true, 1, 1)
	r.Wheel(1, 1)
	r.SyncLocks(false, false, false)
	r.release()
	if extra := in.Events()[n:]; len(extra) != 0 {
		t.Errorf("posted after the release: %+v", extra)
	}
	if mice != 3 {
		t.Errorf("onMouse called %d times, want 3", mice)
	}
}

func TestRelayUpWithoutDown(t *testing.T) {
	in := &RecordingInjector{}
	r, _ := testRelay(in, slog.New(slog.DiscardHandler))
	r.MouseButton(rdp.ButtonBack, false, 1, 1)
	if e := in.Events()[0]; e.Clicks != 1 || e.Down {
		t.Errorf("up without a down posted %+v", e)
	}
}

func TestRelayFailures(t *testing.T) {
	rec := &recorder{}
	in := &RecordingInjector{Err: errors.New("TestInjectFailed", "no Accessibility")}
	r, _ := testRelay(in, slog.New(rec))
	r.Key(0x1e, rdp.NotExtended, true)
	r.MouseMove(1, 1)
	r.Wheel(0, 1)
	warns := rec.find("input not posted")
	if len(warns) != 3 {
		t.Fatalf("%d records, want 3", len(warns))
	}
	if warns[0].Level != slog.LevelWarn || warns[1].Level != slog.LevelDebug || warns[2].Level != slog.LevelDebug {
		t.Errorf("levels %v %v %v, want Warn then Debug", warns[0].Level, warns[1].Level, warns[2].Level)
	}
	err, _ := attr(warns[0], "err").Any().(error)
	assertRoute(t, err, "TestRelayFailures.TestInjectFailed")
	if n := attr(warns[2], "failures").Int64(); n != 3 {
		t.Errorf("failures = %d, want 3", n)
	}
}
