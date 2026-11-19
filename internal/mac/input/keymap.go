package input

import "github.com/rm4n0s/mardp/internal/host"

// noKey marks a scancode that posts nothing.
const noKey = 0xFFFF

// The macOS virtual key codes the keymap treats apart (HIToolbox
// Events.h, kVK_*).
const (
	vkA            = 0x00
	vkReturn       = 0x24
	vkTab          = 0x30
	vkSpace        = 0x31
	vkDelete       = 0x33 // backspace
	vkEscape       = 0x35
	vkRightCommand = 0x36
	vkCommand      = 0x37
	vkShift        = 0x38
	vkCapsLock     = 0x39
	vkOption       = 0x3A
	vkControl      = 0x3B
	vkRightShift   = 0x3C
	vkRightOption  = 0x3D
	vkRightControl = 0x3E
	vkF13          = 0x69
	vkF14          = 0x6B
	vkF15          = 0x71
	vkHelp         = 0x72
	vkHome         = 0x73
	vkPageUp       = 0x74
	vkForwardDel   = 0x75
	vkEnd          = 0x77
	vkPageDown     = 0x79
	vkLeftArrow    = 0x7B
	vkRightArrow   = 0x7C
	vkDownArrow    = 0x7D
	vkUpArrow      = 0x7E
)

// The PC scancodes (set 1) the keymap treats apart.
const (
	scLeftCtrl   = 0x1D
	scNumLock    = 0x45
	scLeftWin    = 0x5B
	scRightWin   = 0x5C
	scPauseFirst = 0x1D // with ExtendedE1; 0x45 follows
)

// plain maps the scancodes without a prefix to virtual key codes: the 105
// keys of an ISO keyboard, the F13 to F20 row and the Japanese and Brazilian
// keys. Num Lock (0x45) is a lock, not a key, on the Mac.
var plain = [128]uint16{
	0x00: noKey,
	0x01: vkEscape,
	0x02: 0x12, 0x03: 0x13, 0x04: 0x14, 0x05: 0x15, 0x06: 0x17, // 1 to 5
	0x07: 0x16, 0x08: 0x1A, 0x09: 0x1C, 0x0A: 0x19, 0x0B: 0x1D, // 6 to 0
	0x0C: 0x1B, 0x0D: 0x18, // - =
	0x0E: vkDelete, 0x0F: vkTab,
	0x10: 0x0C, 0x11: 0x0D, 0x12: 0x0E, 0x13: 0x0F, 0x14: 0x11, // Q W E R T
	0x15: 0x10, 0x16: 0x20, 0x17: 0x22, 0x18: 0x1F, 0x19: 0x23, // Y U I O P
	0x1A: 0x21, 0x1B: 0x1E, // [ ]
	0x1C: vkReturn,
	0x1D: vkControl,
	0x1E: vkA, 0x1F: 0x01, 0x20: 0x02, 0x21: 0x03, 0x22: 0x05, // A S D F G
	0x23: 0x04, 0x24: 0x26, 0x25: 0x28, 0x26: 0x25, // H J K L
	0x27: 0x29, 0x28: 0x27, 0x29: 0x32, // ; ' `
	0x2A: vkShift, 0x2B: 0x2A, // left Shift, backslash
	0x2C: 0x06, 0x2D: 0x07, 0x2E: 0x08, 0x2F: 0x09, 0x30: 0x0B, // Z X C V B
	0x31: 0x2D, 0x32: 0x2E, // N M
	0x33: 0x2B, 0x34: 0x2F, 0x35: 0x2C, // , . /
	0x36: vkRightShift,
	0x37: 0x43, // keypad *
	0x38: vkOption,
	0x39: vkSpace,
	0x3A: vkCapsLock,
	0x3B: 0x7A, 0x3C: 0x78, 0x3D: 0x63, 0x3E: 0x76, 0x3F: 0x60, // F1 to F5
	0x40: 0x61, 0x41: 0x62, 0x42: 0x64, 0x43: 0x65, 0x44: 0x6D, // F6 to F10
	0x45: noKey,                                    // Num Lock
	0x46: vkF14,                                    // Scroll Lock
	0x47: 0x59, 0x48: 0x5B, 0x49: 0x5C, 0x4A: 0x4E, // keypad 7 8 9 -
	0x4B: 0x56, 0x4C: 0x57, 0x4D: 0x58, 0x4E: 0x45, // keypad 4 5 6 +
	0x4F: 0x53, 0x50: 0x54, 0x51: 0x55, // keypad 1 2 3
	0x52: 0x52, 0x53: 0x41, // keypad 0 .
	0x54: vkF13, // SysRq (Alt+Print Screen)
	0x55: noKey,
	0x56: 0x0A,             // the ISO key between left Shift and Z
	0x57: 0x67, 0x58: 0x6F, // F11 F12
	0x59: 0x51, // keypad =
	0x5A: noKey, 0x5B: noKey, 0x5C: noKey, 0x5D: noKey, 0x5E: noKey, 0x5F: noKey,
	0x60: noKey, 0x61: noKey, 0x62: noKey, 0x63: noKey,
	0x64: vkF13, 0x65: vkF14, 0x66: vkF15, 0x67: 0x6A, // F13 to F16
	0x68: 0x40, 0x69: 0x4F, 0x6A: 0x50, 0x6B: 0x5A, // F17 to F20
	0x6C: noKey, 0x6D: noKey, 0x6E: noKey, 0x6F: noKey,
	0x70: 0x68, // Katakana/Hiragana: Kana
	0x71: noKey, 0x72: noKey,
	0x73: 0x5E, // JIS _ (Ro)
	0x74: noKey, 0x75: noKey, 0x76: noKey, 0x77: noKey, 0x78: noKey,
	0x79: 0x68, // Henkan: Kana
	0x7A: noKey,
	0x7B: 0x66, // Muhenkan: Eisu
	0x7C: noKey,
	0x7D: 0x5D, // JIS Yen
	0x7E: 0x5F, // Brazilian keypad , : JIS keypad ,
	0x7F: noKey,
}

// extended maps the scancodes with the E0 prefix that a 105-key keyboard
// has; the others (media keys) post nothing.
func extended(sc uint8) uint16 {
	switch sc {
	case 0x1C:
		return 0x4C // keypad Enter
	case 0x1D:
		return vkRightControl
	case 0x35:
		return 0x4B // keypad /
	case 0x37:
		return vkF13 // Print Screen
	case 0x38:
		return vkRightOption // AltGr
	case 0x47:
		return vkHome
	case 0x48:
		return vkUpArrow
	case 0x49:
		return vkPageUp
	case 0x4B:
		return vkLeftArrow
	case 0x4D:
		return vkRightArrow
	case 0x4F:
		return vkEnd
	case 0x50:
		return vkDownArrow
	case 0x51:
		return vkPageDown
	case 0x52:
		return vkHelp // Insert: the Mac's Help key sits there
	case 0x53:
		return vkForwardDel
	case scLeftWin:
		return vkCommand
	case scRightWin:
		return vkRightCommand
	case 0x5D:
		return 0x6E // Menu: the Context Menu key
	}
	return noKey
}

// keypadNav maps the keypad's digits and point to what they are with Num
// Lock off; keypad 5 is then nothing.
func keypadNav(sc uint8) (uint16, bool) {
	switch sc {
	case 0x47:
		return vkHome, true
	case 0x48:
		return vkUpArrow, true
	case 0x49:
		return vkPageUp, true
	case 0x4B:
		return vkLeftArrow, true
	case 0x4C:
		return noKey, true
	case 0x4D:
		return vkRightArrow, true
	case 0x4F:
		return vkEnd, true
	case 0x50:
		return vkDownArrow, true
	case 0x51:
		return vkPageDown, true
	case 0x52:
		return vkHelp, true
	case 0x53:
		return vkForwardDel, true
	}
	return 0, false
}

// keyCode maps a scancode to the virtual key code it posts, or noKey. num
// is the Num Lock state; swap swaps Control and Command (SPEC D19).
func keyCode(sc uint8, ext host.Extended, num, swap bool) uint16 {
	var code uint16
	switch ext {
	case host.NotExtended:
		if sc >= uint8(len(plain)) {
			return noKey
		}
		if !num {
			if c, ok := keypadNav(sc); ok {
				return c
			}
		}
		code = plain[sc]
	case host.ExtendedE0:
		code = extended(sc)
	case host.ExtendedE1:
		if sc != scPauseFirst {
			return noKey
		}
		code = vkF15 // Pause
	default:
		return noKey
	}
	if swap {
		switch code {
		case vkControl:
			code = vkCommand
		case vkRightControl:
			code = vkRightCommand
		case vkCommand:
			code = vkControl
		case vkRightCommand:
			code = vkRightControl
		}
	}
	return code
}

// The event flags of macOS (CGEventFlags): a family bit for each modifier,
// and the device bits that say which of the two keys it was (IOLLEvent.h,
// NX_DEVICE*KEYMASK).
const (
	flagAlphaShift  uint64 = 0x10000
	flagShift       uint64 = 0x20000
	flagControl     uint64 = 0x40000
	flagOption      uint64 = 0x80000
	flagCommand     uint64 = 0x100000
	flagNumericPad  uint64 = 0x200000
	flagSecondaryFn uint64 = 0x800000

	devLeftControl  uint64 = 0x1
	devLeftShift    uint64 = 0x2
	devRightShift   uint64 = 0x4
	devLeftCommand  uint64 = 0x8
	devRightCommand uint64 = 0x10
	devLeftOption   uint64 = 0x20
	devRightOption  uint64 = 0x40
	devRightControl uint64 = 0x2000
)

// modifier gives a modifier key's family and device bits; ok is false for
// a key that is not a modifier.
func modifier(code uint16) (family, device uint64, ok bool) {
	switch code {
	case vkShift:
		return flagShift, devLeftShift, true
	case vkRightShift:
		return flagShift, devRightShift, true
	case vkControl:
		return flagControl, devLeftControl, true
	case vkRightControl:
		return flagControl, devRightControl, true
	case vkOption:
		return flagOption, devLeftOption, true
	case vkRightOption:
		return flagOption, devRightOption, true
	case vkCommand:
		return flagCommand, devLeftCommand, true
	case vkRightCommand:
		return flagCommand, devRightCommand, true
	}
	return 0, 0, false
}

// keyFlags gives the flags a key carries itself, as a Mac keyboard sets
// them: the arrows are on the numeric pad and need Fn on a laptop, the
// navigation and function keys need Fn, the keypad's keys are on it.
func keyFlags(code uint16) uint64 {
	switch code {
	case vkLeftArrow, vkRightArrow, vkDownArrow, vkUpArrow:
		return flagNumericPad | flagSecondaryFn
	case vkHome, vkEnd, vkPageUp, vkPageDown, vkForwardDel, vkHelp,
		0x7A, 0x78, 0x63, 0x76, 0x60, 0x61, 0x62, 0x64, 0x65, 0x6D, 0x67, 0x6F, // F1 to F12
		vkF13, vkF14, vkF15, 0x6A, 0x40, 0x4F, 0x50, 0x5A: // F13 to F20
		return flagSecondaryFn
	case 0x41, 0x43, 0x45, 0x4B, 0x4C, 0x4E, 0x51, 0x52, 0x53, 0x54, 0x55,
		0x56, 0x57, 0x58, 0x59, 0x5B, 0x5C, 0x5F:
		return flagNumericPad
	}
	return 0
}
