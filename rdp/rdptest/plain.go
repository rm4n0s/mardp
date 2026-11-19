package rdptest

import (
	"context"

	"github.com/rm4n0s/mardp/rdp/internal/fastpath"
	"github.com/rm4n0s/mardp/rdp/internal/input"
)

// Helpers for tests outside rdp/, which cannot import the library's internal
// packages: they build the events and PDUs from plain values.

// Key is a scancode event: extended sets the E0 prefix.
func Key(scancode uint8, extended, down bool) input.Event {
	var f uint16
	if extended {
		f |= input.KeyExtended
	}
	if !down {
		f |= input.KeyRelease
	}
	return input.Event{Kind: input.KindScancode, Flags: f, Code: uint16(scancode)}
}

// Unicode is a Unicode event for one UTF-16 code unit.
func Unicode(unit uint16, down bool) input.Event {
	var f uint16
	if !down {
		f |= input.KeyRelease
	}
	return input.Event{Kind: input.KindUnicode, Flags: f, Code: unit}
}

// Move is a mouse move to x, y.
func Move(x, y int) input.Event {
	return input.Event{Kind: input.KindMouse, Flags: input.PtrMove, X: uint16(x), Y: uint16(y)}
}

// Button is a button of the mouse event, 1 left, 2 right, 3 middle, going
// down or up at x, y.
func Button(button int, down bool, x, y int) input.Event {
	f := [...]uint16{1: input.PtrButton1, 2: input.PtrButton2, 3: input.PtrButton3}[button]
	if down {
		f |= input.PtrDown
	}
	return input.Event{Kind: input.KindMouse, Flags: f, X: uint16(x), Y: uint16(y)}
}

// Sync is a Synchronize event with the lock keys' states.
func Sync(caps, num, scroll bool) input.Event {
	var f uint16
	if caps {
		f |= input.SyncCapsLock
	}
	if num {
		f |= input.SyncNumLock
	}
	if scroll {
		f |= input.SyncScrollLock
	}
	return input.Event{Kind: input.KindSync, Flags: f}
}

// SendRefresh sends a Refresh Rect PDU for the area at x, y of the size.
func (c *Client) SendRefresh(ctx context.Context, x, y, width, height int) error {
	return c.SendRefreshRect(ctx, input.Rect{Left: uint16(x), Top: uint16(y), Right: uint16(x + width - 1), Bottom: uint16(y + height - 1)})
}

// SendSuppress sends a Suppress Output PDU: suppressed stops the updates,
// otherwise it asks for them again for a desktop of the size.
func (c *Client) SendSuppress(ctx context.Context, suppressed bool, width, height int) error {
	s := input.SuppressOutput{Allow: !suppressed}
	if !suppressed {
		s.Area = input.Rect{Right: uint16(width - 1), Bottom: uint16(height - 1)}
	}
	return c.SendSuppressOutput(ctx, s)
}

// Draw draws u on sc when it is a Bitmap Update, and says whether it was.
func (sc *Screen) Draw(u fastpath.Update) (bool, error) {
	if u.Code != fastpath.UpdateBitmap {
		return false, nil
	}
	_, err := sc.DrawBitmaps(u.Data)
	return err == nil, err
}
