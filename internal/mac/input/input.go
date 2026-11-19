// Package input posts the client's keyboard and mouse to macOS with Core
// Graphics events (SPEC §10.4, §10.5, D19). It implements host.Injector.
//
// # Keys
//
// The client sends PC scancodes (set 1). A table maps each to a macOS
// virtual key code; the code chosen when a key goes down is the one its
// release posts, so a change of Num Lock or of the swap in between leaves no
// key stuck. Ctrl is Control, Alt is Option and the Windows key is Command;
// Options.SwapCtrlCommand swaps Ctrl and Command. Print Screen, Scroll Lock
// and Pause are F13, F14 and F15, where a Mac's extended keyboard has them;
// Insert is Help. Media keys post nothing.
//
// Modifier keys post flags-changed events, as a Mac keyboard does; every
// event carries the flags of the modifiers held and of Caps Lock, so
// Command-click and Shift-arrow work.
//
// # Locks
//
// Caps Lock toggles on each press and its state is in every event's flags;
// a Synchronize event sets it. Num Lock is not a key on the Mac: it only
// decides whether the keypad's digits are digits or the navigation keys.
// Scroll Lock's state is not kept.
//
// # Mouse
//
// Positions come in the display's points (internal/host scales them) and
// get the display's origin in the global space added. Moves with a button
// down are drags, and each button event carries the click count the host
// counted. The wheel turns 120ths of a notch into lines, keeping the rest
// for the next event.
//
// # Permission
//
// Without Accessibility, macOS drops posted events silently; the package
// checks it at most once a second and fails each event without it.
package input

import (
	"log/slog"
	"math/bits"
	"sync"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/internal/host"
)

// Tags of the errors of the input.
const (
	// TagInputUnsupported: the package runs on a system that is not
	// macOS.
	TagInputUnsupported = "InputUnsupported"
	// TagInputNotPermitted: Accessibility is not granted, so macOS would
	// drop the event.
	TagInputNotPermitted = "InputNotPermitted"
	// TagInputDisplayMissing: the display the session shows is not
	// connected.
	TagInputDisplayMissing = "InputDisplayMissing"
	// TagInputEventFailed: Core Graphics did not make the event.
	TagInputEventFailed = "InputEventFailed"
	// TagInputButtonUnknown: a mouse button that is none of the five.
	TagInputButtonUnknown = "InputButtonUnknown"
)

// The timing of the input.
const (
	// recheck is how long a permission answer and a display's origin are
	// trusted.
	recheck = time.Second
	// notch is a wheel notch in the client's units.
	notch = 120
)

// Options configure an Input.
type Options struct {
	// Display is the UUID of the display the session shows; empty is the
	// main display.
	Display string
	// SwapCtrlCommand makes Ctrl Command and the Windows key Control
	// (SPEC D19).
	SwapCtrlCommand bool
	// Logger receives the log; nil discards it.
	Logger *slog.Logger
}

// The kinds of event.
type eventKind uint8

const (
	evKey eventKind = iota + 1
	evFlags
	evMouse
	evScroll
	evText
)

// The mouse event types of Core Graphics (CGEventType).
const (
	cgLeftDown     = 1
	cgLeftUp       = 2
	cgRightDown    = 3
	cgRightUp      = 4
	cgMoved        = 5
	cgLeftDragged  = 6
	cgRightDragged = 7
	cgOtherDown    = 25
	cgOtherUp      = 26
	cgOtherDragged = 27
)

// event is one Core Graphics event to post; the fields of its kind are set.
type event struct {
	kind  eventKind
	flags uint64
	// evKey and evFlags: the key; evKey: down and repeat.
	key    uint16
	down   bool
	repeat bool
	// evMouse: the type, the button number, the global position and the
	// click count.
	mouse  uint32
	button uint32
	x, y   float64
	clicks int
	// evScroll: lines, vertical (positive up) and horizontal (positive
	// left).
	dy, dx int32
	// evText: the character as UTF-16, and down.
	text [2]uint16
	n    int
}

// poster is what the package needs of macOS; the tests fake it.
type poster interface {
	// trusted says whether Accessibility is granted.
	trusted() bool
	// origin finds the display with the UUID (empty: the main display) and
	// gives its top-left corner in the global space.
	origin(uuid string) (x, y float64, ok bool)
	// post posts an event.
	post(e *event) error
}

// keyID is a key as the client names it.
type keyID struct {
	sc  uint8
	ext host.Extended
}

// Input implements host.Injector.
var _ host.Injector = (*Input)(nil)

// Input posts a session's keyboard and mouse. It implements host.Injector;
// its methods may be called from several goroutines.
type Input struct {
	opts Options
	log  *slog.Logger
	mac  poster
	now  func() time.Time

	mu sync.Mutex
	// The permission and the display's origin, and when they were read.
	trustedAt  time.Time
	isTrusted  bool
	originAt   time.Time
	ox, oy     float64
	haveOrigin bool
	// held keeps the code each key posted when it went down.
	held map[keyID]uint16
	// devices are the device bits of the modifiers held.
	devices uint64
	caps    bool
	num     bool
	// pause says an E1 0x1D came: the 0x45 that follows it is part of
	// Pause.
	pause bool
	// buttons are the Core Graphics buttons held, a bit each; x and y the
	// pointer's last global position.
	buttons uint32
	x, y    float64
	// The rest of the wheel, in 120ths of a notch.
	restX, restY int
}

// newInput makes an Input over mac.
func newInput(opts Options, mac poster, now func() time.Time) *Input {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Input{opts: opts, log: log, mac: mac, now: now, held: map[keyID]uint16{}, num: true}
}

// ready checks the permission and the display's origin, each at most once
// a second.
func (in *Input) ready() error {
	now := in.now()
	if in.trustedAt.IsZero() || now.Sub(in.trustedAt) >= recheck {
		in.isTrusted, in.trustedAt = in.mac.trusted(), now
	}
	if !in.isTrusted {
		return errors.New(TagInputNotPermitted, "Accessibility is not granted")
	}
	if in.originAt.IsZero() || now.Sub(in.originAt) >= recheck {
		in.ox, in.oy, in.haveOrigin = in.mac.origin(in.opts.Display)
		in.originAt = now
	}
	if !in.haveOrigin {
		return errors.New(TagInputDisplayMissing, "the session's display is not connected", "display", in.opts.Display)
	}
	return nil
}

// flags gives the flags of the modifiers held and of Caps Lock.
func (in *Input) flags() uint64 {
	f := in.devices
	for _, m := range [...]struct{ dev, family uint64 }{
		{devLeftShift | devRightShift, flagShift},
		{devLeftControl | devRightControl, flagControl},
		{devLeftOption | devRightOption, flagOption},
		{devLeftCommand | devRightCommand, flagCommand},
	} {
		if in.devices&m.dev != 0 {
			f |= m.family
		}
	}
	if in.caps {
		f |= flagAlphaShift
	}
	return f
}

// Key presses or releases the key of a PC scancode; it implements
// host.Injector.
func (in *Input) Key(sc uint8, ext host.Extended, down bool) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if err := in.ready(); err != nil {
		return err
	}
	if in.pause {
		in.pause = false
		if sc == scNumLock && ext == host.NotExtended {
			return nil // the second half of Pause
		}
	}
	if ext == host.ExtendedE1 && sc == scPauseFirst {
		in.pause = true
	}
	if sc == scNumLock && ext == host.NotExtended {
		if down {
			in.num = !in.num
		}
		return nil
	}
	k := keyID{sc, ext}
	code, wasHeld := in.held[k]
	if down && !wasHeld {
		code = keyCode(sc, ext, in.num, in.opts.SwapCtrlCommand)
		if code == noKey {
			in.log.Debug("a key macOS has no key for", "scancode", sc, "extended", int(ext))
			return nil
		}
		in.held[k] = code
	}
	if !down {
		if !wasHeld {
			return nil // a release of a key not pressed here
		}
		delete(in.held, k)
	}
	return in.postKey(code, down, down && wasHeld)
}

// postKey posts a key's event and keeps the modifiers and Caps Lock.
func (in *Input) postKey(code uint16, down, repeat bool) error {
	if code == vkCapsLock {
		if !down || repeat {
			return nil // Caps Lock changes on the press only
		}
		in.caps = !in.caps
		return in.mac.post(&event{kind: evFlags, key: code, flags: in.flags()})
	}
	if _, dev, ok := modifier(code); ok {
		if repeat {
			return nil
		}
		if down {
			in.devices |= dev
		} else {
			in.devices &^= dev
		}
		return in.mac.post(&event{kind: evFlags, key: code, flags: in.flags()})
	}
	return in.mac.post(&event{kind: evKey, key: code, down: down, repeat: repeat, flags: in.flags() | keyFlags(code)})
}

// Unicode types or releases a character; it implements host.Injector.
func (in *Input) Unicode(r rune, down bool) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if err := in.ready(); err != nil {
		return err
	}
	e := event{kind: evText, down: down, flags: in.flags()}
	switch {
	case r >= 0x10000 && r <= 0x10FFFF:
		r -= 0x10000
		e.text = [2]uint16{uint16(0xD800 + r>>10), uint16(0xDC00 + r&0x3FF)}
		e.n = 2
	case r >= 0 && r < 0x10000 && (r < 0xD800 || r > 0xDFFF):
		e.text[0] = uint16(r)
		e.n = 1
	default:
		in.log.Debug("not a character", "rune", int(r))
		return nil
	}
	return in.mac.post(&e)
}

// cgButton gives the Core Graphics number of a button.
func cgButton(b host.MouseButton) (uint32, bool) {
	switch b {
	case host.ButtonLeft:
		return 0, true
	case host.ButtonRight:
		return 1, true
	case host.ButtonMiddle:
		return 2, true
	case host.ButtonBack:
		return 3, true
	case host.ButtonForward:
		return 4, true
	}
	return 0, false
}

// MouseMove moves the pointer to a position in the display's points; it
// implements host.Injector. With a button down it drags.
func (in *Input) MouseMove(x, y float64) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if err := in.ready(); err != nil {
		return err
	}
	in.x, in.y = in.ox+x, in.oy+y
	e := event{kind: evMouse, mouse: cgMoved, x: in.x, y: in.y, flags: in.flags()}
	switch {
	case in.buttons&1 != 0:
		e.mouse = cgLeftDragged
	case in.buttons&2 != 0:
		e.mouse, e.button = cgRightDragged, 1
	case in.buttons != 0:
		e.mouse, e.button = cgOtherDragged, uint32(bits.TrailingZeros32(in.buttons))
	}
	return in.mac.post(&e)
}

// MouseButton presses or releases a button at a position in the display's
// points, with the click count the host counted; it implements
// host.Injector.
func (in *Input) MouseButton(b host.MouseButton, down bool, x, y float64, clicks int) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	n, ok := cgButton(b)
	if !ok {
		return errors.New(TagInputButtonUnknown, "a mouse button that is none of the five", "button", int(b))
	}
	if err := in.ready(); err != nil {
		return err
	}
	in.x, in.y = in.ox+x, in.oy+y
	return in.postButton(n, down, clicks)
}

// postButton posts a button's event at the pointer's position.
func (in *Input) postButton(n uint32, down bool, clicks int) error {
	if down {
		in.buttons |= 1 << n
	} else {
		in.buttons &^= 1 << n
	}
	e := event{kind: evMouse, button: n, x: in.x, y: in.y, clicks: max(clicks, 1), flags: in.flags()}
	switch {
	case n == 0 && down:
		e.mouse = cgLeftDown
	case n == 0:
		e.mouse = cgLeftUp
	case n == 1 && down:
		e.mouse = cgRightDown
	case n == 1:
		e.mouse = cgRightUp
	case down:
		e.mouse = cgOtherDown
	default:
		e.mouse = cgOtherUp
	}
	return in.mac.post(&e)
}

// Wheel scrolls, in 120ths of a notch, dy positive away from the user and
// dx positive to the right; it implements host.Injector. A notch is a line;
// what is less than a line waits for the next turn the same way.
func (in *Input) Wheel(dx, dy int) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if err := in.ready(); err != nil {
		return err
	}
	// A turn the other way drops what was left of the last one.
	if (dy > 0) != (in.restY > 0) && in.restY != 0 && dy != 0 {
		in.restY = 0
	}
	if (dx > 0) != (in.restX > 0) && in.restX != 0 && dx != 0 {
		in.restX = 0
	}
	in.restY += dy
	in.restX += dx
	lines, cols := in.restY/notch, in.restX/notch
	in.restY -= lines * notch
	in.restX -= cols * notch
	if lines == 0 && cols == 0 {
		return nil
	}
	// Core Graphics: vertical positive scrolls up (away), horizontal
	// positive scrolls left.
	return in.mac.post(&event{kind: evScroll, dy: int32(lines), dx: int32(-cols), flags: in.flags()})
}

// SetLocks sets Caps Lock and Num Lock; it implements host.Injector. macOS
// has no Scroll Lock.
func (in *Input) SetLocks(caps, num, _ bool) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.num = num
	if caps == in.caps {
		return nil
	}
	if err := in.ready(); err != nil {
		return err
	}
	in.caps = caps
	return in.mac.post(&event{kind: evFlags, key: vkCapsLock, flags: in.flags()})
}

// ReleaseAll releases every key and button this Input holds down, at the
// end of a session (SPEC §10.4). It returns the first error and goes on
// after it.
func (in *Input) ReleaseAll() error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if len(in.held) == 0 && in.buttons == 0 {
		return nil
	}
	if err := in.ready(); err != nil {
		clear(in.held)
		in.devices, in.buttons = 0, 0
		return err
	}
	var first error
	keep := func(err error) {
		if first == nil {
			first = err
		}
	}
	for k, code := range in.held {
		delete(in.held, k)
		keep(in.postKey(code, false, false))
	}
	for n := uint32(0); n < 5; n++ {
		if in.buttons&(1<<n) != 0 {
			keep(in.postButton(n, false, 1))
		}
	}
	in.pause = false
	return first
}
