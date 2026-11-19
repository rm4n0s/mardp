package input

import (
	"testing"

	"github.com/rm4n0s/mardp/internal/host"
)

// iso105 is every key of a 105-key ISO keyboard, with its scancode and the
// macOS key code it posts by default with Num Lock on.
var iso105 = []struct {
	name string
	sc   uint8
	ext  host.Extended
	code uint16
}{
	{"Esc", 0x01, 0, 0x35},
	{"F1", 0x3B, 0, 0x7A}, {"F2", 0x3C, 0, 0x78}, {"F3", 0x3D, 0, 0x63}, {"F4", 0x3E, 0, 0x76},
	{"F5", 0x3F, 0, 0x60}, {"F6", 0x40, 0, 0x61}, {"F7", 0x41, 0, 0x62}, {"F8", 0x42, 0, 0x64},
	{"F9", 0x43, 0, 0x65}, {"F10", 0x44, 0, 0x6D}, {"F11", 0x57, 0, 0x67}, {"F12", 0x58, 0, 0x6F},
	{"PrintScreen", 0x37, host.ExtendedE0, 0x69}, {"ScrollLock", 0x46, 0, 0x6B}, {"Pause", 0x1D, host.ExtendedE1, 0x71},
	{"`", 0x29, 0, 0x32}, {"1", 0x02, 0, 0x12}, {"2", 0x03, 0, 0x13}, {"3", 0x04, 0, 0x14},
	{"4", 0x05, 0, 0x15}, {"5", 0x06, 0, 0x17}, {"6", 0x07, 0, 0x16}, {"7", 0x08, 0, 0x1A},
	{"8", 0x09, 0, 0x1C}, {"9", 0x0A, 0, 0x19}, {"0", 0x0B, 0, 0x1D}, {"-", 0x0C, 0, 0x1B},
	{"=", 0x0D, 0, 0x18}, {"Backspace", 0x0E, 0, 0x33},
	{"Tab", 0x0F, 0, 0x30}, {"Q", 0x10, 0, 0x0C}, {"W", 0x11, 0, 0x0D}, {"E", 0x12, 0, 0x0E},
	{"R", 0x13, 0, 0x0F}, {"T", 0x14, 0, 0x11}, {"Y", 0x15, 0, 0x10}, {"U", 0x16, 0, 0x20},
	{"I", 0x17, 0, 0x22}, {"O", 0x18, 0, 0x1F}, {"P", 0x19, 0, 0x23}, {"[", 0x1A, 0, 0x21},
	{"]", 0x1B, 0, 0x1E}, {"Enter", 0x1C, 0, 0x24},
	{"CapsLock", 0x3A, 0, 0x39}, {"A", 0x1E, 0, 0x00}, {"S", 0x1F, 0, 0x01}, {"D", 0x20, 0, 0x02},
	{"F", 0x21, 0, 0x03}, {"G", 0x22, 0, 0x05}, {"H", 0x23, 0, 0x04}, {"J", 0x24, 0, 0x26},
	{"K", 0x25, 0, 0x28}, {"L", 0x26, 0, 0x25}, {";", 0x27, 0, 0x29}, {"'", 0x28, 0, 0x27},
	{"#", 0x2B, 0, 0x2A},
	{"LShift", 0x2A, 0, 0x38}, {"ISO \\", 0x56, 0, 0x0A}, {"Z", 0x2C, 0, 0x06}, {"X", 0x2D, 0, 0x07},
	{"C", 0x2E, 0, 0x08}, {"V", 0x2F, 0, 0x09}, {"B", 0x30, 0, 0x0B}, {"N", 0x31, 0, 0x2D},
	{"M", 0x32, 0, 0x2E}, {",", 0x33, 0, 0x2B}, {".", 0x34, 0, 0x2F}, {"/", 0x35, 0, 0x2C},
	{"RShift", 0x36, 0, 0x3C},
	{"LCtrl", 0x1D, 0, 0x3B}, {"LWin", 0x5B, host.ExtendedE0, 0x37}, {"LAlt", 0x38, 0, 0x3A},
	{"Space", 0x39, 0, 0x31}, {"AltGr", 0x38, host.ExtendedE0, 0x3D}, {"RWin", 0x5C, host.ExtendedE0, 0x36},
	{"Menu", 0x5D, host.ExtendedE0, 0x6E}, {"RCtrl", 0x1D, host.ExtendedE0, 0x3E},
	{"Insert", 0x52, host.ExtendedE0, 0x72}, {"Home", 0x47, host.ExtendedE0, 0x73}, {"PageUp", 0x49, host.ExtendedE0, 0x74},
	{"Delete", 0x53, host.ExtendedE0, 0x75}, {"End", 0x4F, host.ExtendedE0, 0x77}, {"PageDown", 0x51, host.ExtendedE0, 0x79},
	{"Up", 0x48, host.ExtendedE0, 0x7E}, {"Left", 0x4B, host.ExtendedE0, 0x7B}, {"Down", 0x50, host.ExtendedE0, 0x7D},
	{"Right", 0x4D, host.ExtendedE0, 0x7C},
	{"NumLock", 0x45, 0, noKey}, {"KP/", 0x35, host.ExtendedE0, 0x4B}, {"KP*", 0x37, 0, 0x43}, {"KP-", 0x4A, 0, 0x4E},
	{"KP7", 0x47, 0, 0x59}, {"KP8", 0x48, 0, 0x5B}, {"KP9", 0x49, 0, 0x5C}, {"KP+", 0x4E, 0, 0x45},
	{"KP4", 0x4B, 0, 0x56}, {"KP5", 0x4C, 0, 0x57}, {"KP6", 0x4D, 0, 0x58},
	{"KP1", 0x4F, 0, 0x53}, {"KP2", 0x50, 0, 0x54}, {"KP3", 0x51, 0, 0x55}, {"KPEnter", 0x1C, host.ExtendedE0, 0x4C},
	{"KP0", 0x52, 0, 0x52}, {"KP.", 0x53, 0, 0x41},
}

func TestKeyCode105(t *testing.T) {
	if len(iso105) != 105 {
		t.Fatalf("%d keys, want 105", len(iso105))
	}
	seen := map[keyID]string{}
	codes := map[uint16]string{}
	for _, k := range iso105 {
		id := keyID{k.sc, k.ext}
		if other, ok := seen[id]; ok {
			t.Errorf("%s and %s have the same scancode", k.name, other)
		}
		seen[id] = k.name
		got := keyCode(k.sc, k.ext, true, false)
		if got != k.code {
			t.Errorf("%s (%#x, %d): %#x, want %#x", k.name, k.sc, k.ext, got, k.code)
		}
		// No two keys post the same code, except Print Screen and F13
		// (none here) and the keys that post nothing.
		if got != noKey {
			if other, ok := codes[got]; ok {
				t.Errorf("%s and %s both post %#x", k.name, other, got)
			}
			codes[got] = k.name
		}
	}
}

func TestKeyCodeOthers(t *testing.T) {
	tests := []struct {
		name string
		sc   uint8
		ext  host.Extended
		num  bool
		swap bool
		code uint16
	}{
		{"SysRq", 0x54, 0, true, false, 0x69},
		{"F13", 0x64, 0, true, false, 0x69},
		{"F20", 0x6B, 0, true, false, 0x5A},
		{"KP=", 0x59, 0, true, false, 0x51},
		{"JIS Yen", 0x7D, 0, true, false, 0x5D},
		{"JIS Ro", 0x73, 0, true, false, 0x5E},
		{"Kana", 0x70, 0, true, false, 0x68},
		{"Eisu", 0x7B, 0, true, false, 0x66},
		{"media key", 0x22, host.ExtendedE0, true, false, noKey},
		{"beyond the table", 0x80, 0, true, false, noKey},
		{"zero", 0x00, 0, true, false, noKey},
		{"E1 not Pause", 0x45, host.ExtendedE1, true, false, noKey},
		{"unknown prefix", 0x1E, host.Extended(9), true, false, noKey},
		{"swap LCtrl", 0x1D, 0, true, true, 0x37},
		{"swap RCtrl", 0x1D, host.ExtendedE0, true, true, 0x36},
		{"swap LWin", 0x5B, host.ExtendedE0, true, true, 0x3B},
		{"swap RWin", 0x5C, host.ExtendedE0, true, true, 0x3E},
		{"swap leaves Alt", 0x38, 0, true, true, 0x3A},
		{"swap leaves A", 0x1E, 0, true, true, 0x00},
		{"KP7 no num", 0x47, 0, false, false, 0x73},
		{"KP8 no num", 0x48, 0, false, false, 0x7E},
		{"KP9 no num", 0x49, 0, false, false, 0x74},
		{"KP4 no num", 0x4B, 0, false, false, 0x7B},
		{"KP5 no num", 0x4C, 0, false, false, noKey},
		{"KP6 no num", 0x4D, 0, false, false, 0x7C},
		{"KP1 no num", 0x4F, 0, false, false, 0x77},
		{"KP2 no num", 0x50, 0, false, false, 0x7D},
		{"KP3 no num", 0x51, 0, false, false, 0x79},
		{"KP0 no num", 0x52, 0, false, false, 0x72},
		{"KP. no num", 0x53, 0, false, false, 0x75},
		{"KP+ no num", 0x4E, 0, false, false, 0x45},
		{"Home no num", 0x47, host.ExtendedE0, false, false, 0x73},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := keyCode(tt.sc, tt.ext, tt.num, tt.swap); got != tt.code {
				t.Errorf("%#x, want %#x", got, tt.code)
			}
		})
	}
}

func TestModifier(t *testing.T) {
	tests := []struct {
		code           uint16
		family, device uint64
		ok             bool
	}{
		{0x38, flagShift, devLeftShift, true},
		{0x3C, flagShift, devRightShift, true},
		{0x3B, flagControl, devLeftControl, true},
		{0x3E, flagControl, devRightControl, true},
		{0x3A, flagOption, devLeftOption, true},
		{0x3D, flagOption, devRightOption, true},
		{0x37, flagCommand, devLeftCommand, true},
		{0x36, flagCommand, devRightCommand, true},
		{0x39, 0, 0, false},
		{0x00, 0, 0, false},
	}
	for _, tt := range tests {
		f, d, ok := modifier(tt.code)
		if f != tt.family || d != tt.device || ok != tt.ok {
			t.Errorf("%#x: %#x %#x %v, want %#x %#x %v", tt.code, f, d, ok, tt.family, tt.device, tt.ok)
		}
	}
}

func TestKeyFlags(t *testing.T) {
	tests := []struct {
		name  string
		code  uint16
		flags uint64
	}{
		{"Up", 0x7E, flagNumericPad | flagSecondaryFn},
		{"Home", 0x73, flagSecondaryFn},
		{"F1", 0x7A, flagSecondaryFn},
		{"F20", 0x5A, flagSecondaryFn},
		{"KP7", 0x59, flagNumericPad},
		{"KPEnter", 0x4C, flagNumericPad},
		{"A", 0x00, 0},
		{"Return", 0x24, 0},
	}
	for _, tt := range tests {
		if got := keyFlags(tt.code); got != tt.flags {
			t.Errorf("%s: %#x, want %#x", tt.name, got, tt.flags)
		}
	}
}
