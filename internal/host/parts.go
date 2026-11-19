package host

import (
	"context"
	"image"
	"time"

	"github.com/rm4n0s/mardp/rdp"
)

// The parts of a session (SPEC §4.1): the packages under internal/mac
// implement them on macOS, and fake.go has stand-ins for the tests and for
// `mardp -headless`.

// Display is one display of the Mac.
type Display struct {
	// ID is the display's UUID, which survives sleep, unlike the display
	// ID (SPEC §7.2).
	ID string
	// Name is what macOS calls it.
	Name string
	// Width and Height are its size in pixels.
	Width, Height int
	// PointWidth and PointHeight are its size in points, the unit of mouse
	// positions on macOS.
	PointWidth, PointHeight int
	// Main says it is the main display.
	Main bool
}

// Image is rdp.Image, so the packages under internal/mac, which import
// only internal/host of mardp (SPEC §4.1), can make frames.
type Image = rdp.Image

// MaxDesktopSide is rdp.MaxDesktopSide: the largest width or height of a
// desktop.
const MaxDesktopSide = rdp.MaxDesktopSide

// Frame is one captured picture for the bitmap paths (SPEC §9.6).
type Frame struct {
	// Image is the display's picture at the session's desktop size.
	Image *rdp.Image
	// Dirty are the rectangles that changed since the previous frame; nil
	// means all of it.
	Dirty []image.Rectangle
}

// ScreenConfig is what a capture is asked for.
type ScreenConfig struct {
	// Width and Height are the session's desktop size: the display is
	// scaled to it (SPEC §9.2).
	Width, Height int
	// MaxFPS is the most frames a second.
	MaxFPS int
}

// ScreenSource captures one display (SPEC §7.2).
type ScreenSource interface {
	// Display describes the display it captures now.
	Display(ctx context.Context) (Display, error)
	// Capture captures the display at cfg and calls frame with each frame
	// that changed, one at a time on a goroutine of the source. The frame
	// and its image are valid only during the call, which should return
	// quickly. Capture returns nil when ctx ends, or the error that stopped
	// the capture.
	Capture(ctx context.Context, cfg ScreenConfig, frame func(*Frame)) error
}

// VideoEncoder controls the H.264 encoder of the H.264 path (SPEC §9.3,
// §9.5), which M3 adds. Frames do not pass through Go (§4.2): the capture
// hands them to the encoder, and only the encoded frames come out.
type VideoEncoder interface {
	// SetBitrate sets the target bitrate, without a restart.
	SetBitrate(bitsPerSecond int) error
	// ForceKeyFrame makes the next frame a key frame.
	ForceKeyFrame()
	// SetSkip makes the encoder skip the frames it gets, or encode them
	// again.
	SetSkip(skip bool)
}

// SoundBuffer is system sound as it is captured (SPEC §10.1): 48 kHz, two
// channels, 32-bit float, not interleaved.
type SoundBuffer struct {
	Left, Right []float32
	// Time is when its first sample was captured, from the start of the
	// capture.
	Time time.Duration
}

// SoundRate is the sample rate of SoundBuffer.
const SoundRate = 48000

// SoundSource captures the system sound.
type SoundSource interface {
	// Capture calls buf with each buffer of sound, one at a time on a
	// goroutine of the source; the buffer is valid only during the call.
	// It returns nil when ctx ends, or the error that stopped it.
	Capture(ctx context.Context, buf func(*SoundBuffer)) error
}

// SoundEncoder turns captured sound into the packets of one format
// (SPEC §10.1).
type SoundEncoder interface {
	// Encode encodes buf and calls packet with each packet it completes,
	// with the time of its first sample; data is valid only during the
	// call.
	Encode(buf *SoundBuffer, packet func(data []byte, t time.Duration)) error
}

// Injector posts the client's input on the Mac (SPEC §10.4, §10.5).
// Positions are in the display's points. A failure, such as a missing
// Accessibility permission, is logged by the session and the input dropped.
type Injector interface {
	// Key presses or releases the key of a PC scancode (set 1).
	Key(scancode uint8, extended rdp.Extended, down bool) error
	// Unicode types or releases a character.
	Unicode(r rune, down bool) error
	// MouseMove moves the pointer.
	MouseMove(x, y float64) error
	// MouseButton presses or releases a button; clicks is the click count
	// macOS expects in the event: 2 for the second click of a double
	// click, both down and up.
	MouseButton(button rdp.MouseButton, down bool, x, y float64, clicks int) error
	// Wheel scrolls, in 120ths of a notch, dy positive away from the user,
	// dx positive to the right.
	Wheel(dx, dy int) error
	// SetLocks sets Caps Lock, Num Lock and Scroll Lock.
	SetLocks(caps, num, scroll bool) error
}

// PointerShape is the system pointer.
type PointerShape struct {
	// Visible says the pointer is shown; the rest is unset when not.
	Visible bool
	// Image is the pointer's picture; HotX and HotY its hotspot.
	Image      *rdp.Image
	HotX, HotY int
	// Hash identifies the picture and hotspot: equal hashes are the same
	// shape.
	Hash uint64
}

// PointerSource reads the system pointer (SPEC §10.3).
type PointerSource interface {
	// Current reads the pointer now; the image is valid until the next
	// call.
	Current() (PointerShape, error)
}

// Permission is one of the permissions of SPEC §7.1.
type Permission int

// The permissions mardp needs.
const (
	// ScreenRecording is needed for the picture and the sound.
	ScreenRecording Permission = iota + 1
	// Accessibility is needed to post input.
	Accessibility
)

// System is what mardp needs of macOS around a session (SPEC §7.1, §7.2).
type System interface {
	// Granted says whether p is granted.
	Granted(p Permission) bool
	// Request asks macOS for p, which shows its question the first time.
	Request(p Permission) error
	// OpenSettings opens the pane of System Settings for p.
	OpenSettings(ctx context.Context, p Permission) error
	// Displays lists the displays.
	Displays() ([]Display, error)
	// KeepAwake wakes the display and keeps it awake until release is
	// called.
	KeepAwake() (release func(), err error)
}
