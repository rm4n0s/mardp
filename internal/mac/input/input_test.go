package input

import (
	"reflect"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/internal/host"
)

// assertRoute fails t unless err is an *errors.Error with route.
func assertRoute(t *testing.T, err error, route string) {
	t.Helper()
	e, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("expected *errors.Error, got %T: %v", err, err)
	}
	if !e.HasRoute(route) {
		t.Errorf("route %s, want %s", e.Route(), route)
	}
}

// fakeMac records the events instead of posting them.
type fakeMac struct {
	denied   bool
	missing  bool
	ox, oy   float64
	events   []event
	asked    int
	origins  int
	postFail bool
}

func (f *fakeMac) trusted() bool { f.asked++; return !f.denied }

func (f *fakeMac) origin(string) (x, y float64, ok bool) {
	f.origins++
	return f.ox, f.oy, !f.missing
}

func (f *fakeMac) post(e *event) error {
	if f.postFail {
		return errors.New(TagInputEventFailed, "test")
	}
	f.events = append(f.events, *e)
	return nil
}

// clock is a time the test moves.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// newTest makes an Input over a fake at origin (100, 50).
func newTest(opts Options) (*Input, *fakeMac, *clock) {
	f := &fakeMac{ox: 100, oy: 50}
	c := &clock{t: time.Unix(1000, 0)}
	return newInput(opts, f, c.now), f, c
}

// Short forms of the events the tests expect.
func key(code uint16, down bool, flags uint64) event {
	return event{kind: evKey, key: code, down: down, flags: flags | keyFlags(code)}
}

func flagsEv(code uint16, flags uint64) event {
	return event{kind: evFlags, key: code, flags: flags}
}

// call is one call of the Injector.
type call func(in *Input) error

func k(sc uint8, ext host.Extended, down bool) call {
	return func(in *Input) error { return in.Key(sc, ext, down) }
}

func locks(caps, num bool) call {
	return func(in *Input) error { return in.SetLocks(caps, num, false) }
}

func TestKeys(t *testing.T) {
	const (
		shift   = flagShift | devLeftShift
		command = flagCommand | devLeftCommand
		control = flagControl | devLeftControl
	)
	tests := []struct {
		name  string
		swap  bool
		calls []call
		want  []event
	}{
		{"letter", false, []call{k(0x1E, 0, true), k(0x1E, 0, false)},
			[]event{key(0x00, true, 0), key(0x00, false, 0)}},
		{"shift A", false, []call{k(0x2A, 0, true), k(0x1E, 0, true), k(0x1E, 0, false), k(0x2A, 0, false)},
			[]event{flagsEv(0x38, shift), key(0x00, true, shift), key(0x00, false, shift), flagsEv(0x38, 0)}},
		{"both shifts", false, []call{k(0x2A, 0, true), k(0x36, 0, true), k(0x2A, 0, false), k(0x36, 0, false)},
			[]event{flagsEv(0x38, shift), flagsEv(0x3C, shift|devRightShift), flagsEv(0x38, flagShift|devRightShift), flagsEv(0x3C, 0)}},
		{"ctrl C", false, []call{k(0x1D, 0, true), k(0x2E, 0, true)},
			[]event{flagsEv(0x3B, control), key(0x08, true, control)}},
		{"ctrl C swapped", true, []call{k(0x1D, 0, true), k(0x2E, 0, true)},
			[]event{flagsEv(0x37, command), key(0x08, true, command)}},
		{"win swapped is control", true, []call{k(0x5B, host.ExtendedE0, true)},
			[]event{flagsEv(0x3B, control)}},
		{"AltGr", false, []call{k(0x38, host.ExtendedE0, true)},
			[]event{flagsEv(0x3D, flagOption|devRightOption)}},
		{"repeat", false, []call{k(0x1E, 0, true), k(0x1E, 0, true), k(0x1E, 0, false)},
			[]event{key(0x00, true, 0), {kind: evKey, key: 0x00, down: true, repeat: true}, key(0x00, false, 0)}},
		{"modifier repeat", false, []call{k(0x2A, 0, true), k(0x2A, 0, true)},
			[]event{flagsEv(0x38, shift)}},
		{"release without press", false, []call{k(0x1E, 0, false)}, nil},
		{"media key", false, []call{k(0x22, host.ExtendedE0, true), k(0x22, host.ExtendedE0, false)}, nil},
		{"arrow", false, []call{k(0x48, host.ExtendedE0, true)},
			[]event{key(0x7E, true, 0)}},
		{"num lock off", false, []call{k(0x45, 0, true), k(0x45, 0, false), k(0x47, 0, true)},
			[]event{key(0x73, true, 0)}},
		{"num lock twice", false, []call{k(0x45, 0, true), k(0x45, 0, true), k(0x47, 0, true)},
			[]event{key(0x59, true, 0)}},
		{"code kept across num lock", false, []call{k(0x47, 0, true), locks(false, false), k(0x47, 0, false)},
			[]event{key(0x59, true, 0), key(0x59, false, 0)}},
		{"sync num off", false, []call{locks(false, false), k(0x50, 0, true)},
			[]event{key(0x7D, true, 0)}},
		{"pause", false, []call{k(0x1D, host.ExtendedE1, true), k(0x45, 0, true), k(0x1D, host.ExtendedE1, false), k(0x45, 0, false), k(0x47, 0, true)},
			[]event{key(0x71, true, 0), key(0x71, false, 0), key(0x59, true, 0)}},
		{"num lock after another key", false, []call{k(0x1D, host.ExtendedE1, true), k(0x1E, 0, true), k(0x45, 0, true), k(0x47, 0, true)},
			[]event{key(0x71, true, 0), key(0x00, true, 0), key(0x73, true, 0)}},
		{"caps lock", false, []call{k(0x3A, 0, true), k(0x3A, 0, false), k(0x1E, 0, true), k(0x3A, 0, true), k(0x3A, 0, false)},
			[]event{flagsEv(0x39, flagAlphaShift), key(0x00, true, flagAlphaShift), flagsEv(0x39, 0)}},
		{"caps held", false, []call{k(0x3A, 0, true), k(0x3A, 0, true)},
			[]event{flagsEv(0x39, flagAlphaShift)}},
		{"sync caps", false, []call{locks(true, true), locks(true, true), k(0x1E, 0, true), locks(false, true)},
			[]event{flagsEv(0x39, flagAlphaShift), key(0x00, true, flagAlphaShift), flagsEv(0x39, 0)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, f, _ := newTest(Options{SwapCtrlCommand: tt.swap})
			for i, c := range tt.calls {
				if err := c(in); err != nil {
					t.Fatalf("call %d: %v", i, err)
				}
			}
			if !reflect.DeepEqual(f.events, tt.want) {
				t.Errorf("events\n%+v\nwant\n%+v", f.events, tt.want)
			}
		})
	}
}

func TestUnicode(t *testing.T) {
	tests := []struct {
		name string
		r    rune
		text [2]uint16
		n    int
	}{
		{"ascii", 'a', [2]uint16{'a'}, 1},
		{"accent", 'é', [2]uint16{0xE9}, 1},
		{"emoji", 0x1F600, [2]uint16{0xD83D, 0xDE00}, 2},
		{"last", 0x10FFFF, [2]uint16{0xDBFF, 0xDFFF}, 2},
		{"surrogate", 0xD800, [2]uint16{}, 0},
		{"too large", 0x110000, [2]uint16{}, 0},
		{"negative", -1, [2]uint16{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, f, _ := newTest(Options{})
			if err := in.Unicode(tt.r, true); err != nil {
				t.Fatal(err)
			}
			if tt.n == 0 {
				if len(f.events) != 0 {
					t.Fatalf("posted %+v", f.events)
				}
				return
			}
			want := []event{{kind: evText, down: true, text: tt.text, n: tt.n}}
			if !reflect.DeepEqual(f.events, want) {
				t.Errorf("%+v, want %+v", f.events, want)
			}
		})
	}
}

func TestMouse(t *testing.T) {
	in, f, _ := newTest(Options{})
	mv := func(x, y float64) {
		if err := in.MouseMove(x, y); err != nil {
			t.Fatal(err)
		}
	}
	btn := func(b host.MouseButton, down bool, x, y float64, clicks int) {
		if err := in.MouseButton(b, down, x, y, clicks); err != nil {
			t.Fatal(err)
		}
	}
	mv(10, 20)
	btn(host.ButtonLeft, true, 10, 20, 1)
	btn(host.ButtonLeft, false, 10, 20, 1)
	btn(host.ButtonLeft, true, 11, 20, 2)
	mv(12, 21)
	btn(host.ButtonLeft, false, 12, 21, 2)
	btn(host.ButtonRight, true, 12, 21, 1)
	mv(13, 21)
	btn(host.ButtonRight, false, 13, 21, 1)
	btn(host.ButtonMiddle, true, 13, 21, 1)
	mv(14, 21)
	btn(host.ButtonBack, true, 14, 21, 3)
	btn(host.ButtonMiddle, false, 14, 21, 1)
	mv(15, 21)
	btn(host.ButtonBack, false, 15, 21, 3)
	btn(host.ButtonForward, true, 15, 21, 0)
	btn(host.ButtonForward, false, 15, 21, 1)
	want := []event{
		{kind: evMouse, mouse: cgMoved, x: 110, y: 70},
		{kind: evMouse, mouse: cgLeftDown, x: 110, y: 70, clicks: 1},
		{kind: evMouse, mouse: cgLeftUp, x: 110, y: 70, clicks: 1},
		{kind: evMouse, mouse: cgLeftDown, x: 111, y: 70, clicks: 2},
		{kind: evMouse, mouse: cgLeftDragged, x: 112, y: 71},
		{kind: evMouse, mouse: cgLeftUp, x: 112, y: 71, clicks: 2},
		{kind: evMouse, mouse: cgRightDown, button: 1, x: 112, y: 71, clicks: 1},
		{kind: evMouse, mouse: cgRightDragged, button: 1, x: 113, y: 71},
		{kind: evMouse, mouse: cgRightUp, button: 1, x: 113, y: 71, clicks: 1},
		{kind: evMouse, mouse: cgOtherDown, button: 2, x: 113, y: 71, clicks: 1},
		{kind: evMouse, mouse: cgOtherDragged, button: 2, x: 114, y: 71},
		{kind: evMouse, mouse: cgOtherDown, button: 3, x: 114, y: 71, clicks: 3},
		{kind: evMouse, mouse: cgOtherUp, button: 2, x: 114, y: 71, clicks: 1},
		{kind: evMouse, mouse: cgOtherDragged, button: 3, x: 115, y: 71},
		{kind: evMouse, mouse: cgOtherUp, button: 3, x: 115, y: 71, clicks: 3},
		{kind: evMouse, mouse: cgOtherDown, button: 4, x: 115, y: 71, clicks: 1},
		{kind: evMouse, mouse: cgOtherUp, button: 4, x: 115, y: 71, clicks: 1},
	}
	if len(f.events) != len(want) {
		t.Fatalf("%d events, want %d: %+v", len(f.events), len(want), f.events)
	}
	for i := range want {
		if f.events[i] != want[i] {
			t.Errorf("event %d: %+v, want %+v", i, f.events[i], want[i])
		}
	}
}

func TestCommandClick(t *testing.T) {
	in, f, _ := newTest(Options{})
	if err := in.Key(0x5B, host.ExtendedE0, true); err != nil {
		t.Fatal(err)
	}
	if err := in.MouseButton(host.ButtonLeft, true, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if got, want := f.events[1].flags, flagCommand|devLeftCommand; got != want {
		t.Errorf("flags %#x, want %#x", got, want)
	}
}

func TestWheel(t *testing.T) {
	type turn struct {
		dx, dy int
		posted bool
		lx, ly int32
	}
	tests := []struct {
		name  string
		turns []turn
	}{
		{"notch up", []turn{{0, 120, true, 0, 1}}},
		{"notch down", []turn{{0, -120, true, 0, -1}}},
		{"two notches", []turn{{0, 240, true, 0, 2}}},
		{"halves", []turn{{0, 60, false, 0, 0}, {0, 60, true, 0, 1}}},
		{"rest kept", []turn{{0, 200, true, 0, 1}, {0, 40, true, 0, 1}}},
		{"reverse drops the rest", []turn{{0, 100, false, 0, 0}, {0, -100, false, 0, 0}, {0, -20, true, 0, -1}}},
		{"right is negative", []turn{{120, 0, true, -1, 0}}},
		{"left", []turn{{-120, 0, true, 1, 0}}},
		{"both", []turn{{-240, 120, true, 2, 1}}},
		{"horizontal rest", []turn{{90, 0, false, 0, 0}, {-30, 0, false, 0, 0}, {-60, 0, false, 0, 0}, {-30, 0, true, 1, 0}}},
		{"zero", []turn{{0, 0, false, 0, 0}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, f, _ := newTest(Options{})
			for i, tu := range tt.turns {
				before := len(f.events)
				if err := in.Wheel(tu.dx, tu.dy); err != nil {
					t.Fatal(err)
				}
				posted := len(f.events) > before
				if posted != tu.posted {
					t.Fatalf("turn %d: posted %v, want %v", i, posted, tu.posted)
				}
				if posted {
					e := f.events[len(f.events)-1]
					if e.kind != evScroll || e.dx != tu.lx || e.dy != tu.ly {
						t.Errorf("turn %d: %+v, want dx %d dy %d", i, e, tu.lx, tu.ly)
					}
				}
			}
		})
	}
}

func TestReleaseAll(t *testing.T) {
	in, f, _ := newTest(Options{})
	if err := in.ReleaseAll(); err != nil || len(f.events) != 0 {
		t.Fatalf("nothing held: %v %+v", err, f.events)
	}
	for _, c := range []call{k(0x2A, 0, true), k(0x3A, 0, true)} {
		if err := c(in); err != nil {
			t.Fatal(err)
		}
	}
	if err := in.MouseButton(host.ButtonRight, true, 5, 5, 1); err != nil {
		t.Fatal(err)
	}
	if err := in.MouseButton(host.ButtonForward, true, 5, 5, 1); err != nil {
		t.Fatal(err)
	}
	f.events = nil
	if err := in.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	want := []event{
		flagsEv(0x38, flagAlphaShift),
		{kind: evMouse, mouse: cgRightUp, button: 1, x: 105, y: 55, clicks: 1, flags: flagAlphaShift},
		{kind: evMouse, mouse: cgOtherUp, button: 4, x: 105, y: 55, clicks: 1, flags: flagAlphaShift},
	}
	if !reflect.DeepEqual(f.events, want) {
		t.Errorf("events\n%+v\nwant\n%+v", f.events, want)
	}
	f.events = nil
	if err := in.ReleaseAll(); err != nil || len(f.events) != 0 {
		t.Errorf("released twice: %v %+v", err, f.events)
	}
	if err := in.MouseMove(1, 1); err != nil || f.events[0].mouse != cgMoved {
		t.Errorf("a move after the release drags: %v %+v", err, f.events)
	}
}

func TestReleaseAllErrors(t *testing.T) {
	in, f, _ := newTest(Options{})
	if err := in.Key(0x1E, 0, true); err != nil {
		t.Fatal(err)
	}
	if err := in.MouseButton(host.ButtonLeft, true, 0, 0, 1); err != nil {
		t.Fatal(err)
	}
	f.postFail = true
	assertRoute(t, in.ReleaseAll(), "Input.ReleaseAll->Input.postKey->fakeMac.post.InputEventFailed")
	if len(in.held) != 0 || in.buttons != 0 {
		t.Errorf("still held: %v %b", in.held, in.buttons)
	}
	f.postFail = false
	if err := in.Key(0x1E, 0, true); err != nil {
		t.Fatal(err)
	}
	f.denied = true
	in.trustedAt = time.Time{}
	assertRoute(t, in.ReleaseAll(), "Input.ReleaseAll->Input.ready.InputNotPermitted")
	if len(in.held) != 0 {
		t.Errorf("still held: %v", in.held)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name    string
		denied  bool
		missing bool
		call    call
		route   string
	}{
		{"key denied", true, false, k(0x1E, 0, true), "Input.Key->Input.ready.InputNotPermitted"},
		{"unicode denied", true, false, func(in *Input) error { return in.Unicode('a', true) }, "Input.Unicode->Input.ready.InputNotPermitted"},
		{"move denied", true, false, func(in *Input) error { return in.MouseMove(1, 1) }, "Input.MouseMove->Input.ready.InputNotPermitted"},
		{"wheel denied", true, false, func(in *Input) error { return in.Wheel(0, 120) }, "Input.Wheel->Input.ready.InputNotPermitted"},
		{"caps denied", true, false, locks(true, true), "Input.SetLocks->Input.ready.InputNotPermitted"},
		{"display missing", false, true, func(in *Input) error { return in.MouseMove(1, 1) }, "Input.MouseMove->Input.ready.InputDisplayMissing"},
		{"button unknown", false, false, func(in *Input) error { return in.MouseButton(9, true, 1, 1, 1) }, "Input.MouseButton.InputButtonUnknown"},
		{"button denied", true, false, func(in *Input) error { return in.MouseButton(host.ButtonLeft, true, 1, 1, 1) }, "Input.MouseButton->Input.ready.InputNotPermitted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, f, _ := newTest(Options{})
			f.denied, f.missing = tt.denied, tt.missing
			assertRoute(t, tt.call(in), tt.route)
			if len(f.events) != 0 {
				t.Errorf("posted %+v", f.events)
			}
		})
	}
}

func TestRecheck(t *testing.T) {
	in, f, c := newTest(Options{})
	f.denied = true
	for range 3 {
		if err := in.Key(0x1E, 0, true); err == nil {
			t.Fatal("posted without the permission")
		}
	}
	if f.asked != 1 {
		t.Errorf("asked %d times in a second, want 1", f.asked)
	}
	f.denied = false
	if err := in.Key(0x1E, 0, true); err == nil {
		t.Fatal("the answer was not kept for a second")
	}
	c.t = c.t.Add(recheck)
	if err := in.Key(0x1E, 0, true); err != nil {
		t.Fatalf("after a second: %v", err)
	}
	f.ox = 2560
	if err := in.MouseMove(1, 1); err != nil {
		t.Fatal(err)
	}
	if f.events[len(f.events)-1].x != 101 {
		t.Errorf("the origin was read again within a second")
	}
	c.t = c.t.Add(recheck)
	if err := in.MouseMove(1, 1); err != nil {
		t.Fatal(err)
	}
	if got := f.events[len(f.events)-1].x; got != 2561 {
		t.Errorf("x %v after the display moved, want 2561", got)
	}
}
