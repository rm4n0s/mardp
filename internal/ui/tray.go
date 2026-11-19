package ui

import (
	"image"
	"image/color"
	"image/draw"

	"github.com/rm4n0s/glutter/desktop"
	m "github.com/rm4n0s/glutter/material"
	gui "github.com/rm4n0s/glutter/ui"
)

// The texts of the menu-bar icon's menu (SPEC §12.3).
const (
	TextShowWindow = "Show window"
	TextQuit       = "Quit"
	// TextTrayTitle names the icon in the places that list the icons.
	TextTrayTitle = "mardp"
	// TextIconFailed is said when the menu-bar icon could not be drawn.
	TextIconFailed = "The menu-bar icon could not be drawn: "
)

// TrayAction is what an item of the menu-bar icon's menu does.
type TrayAction int

// The actions of the menu's items.
const (
	// TrayNone: the item does nothing, as the line of the state.
	TrayNone TrayAction = iota
	// TrayStart starts the server.
	TrayStart
	// TrayStop stops the server.
	TrayStop
	// TrayDisconnect ends the connected session.
	TrayDisconnect
	// TrayShowWindow brings the window back.
	TrayShowWindow
	// TrayQuit stops the server and ends the app.
	TrayQuit
)

// TrayItem is an item of the menu-bar icon's menu.
type TrayItem struct {
	// Separator says that the item is a line between groups; it has no
	// label then.
	Separator bool
	// Label is the item's text.
	Label string
	// Action is what it does.
	Action TrayAction
	// Disabled says that it is shown greyed out.
	Disabled bool
}

// NewTrayMenu is the menu of the menu-bar icon (SPEC §12.3): the state in
// words, Start or Stop, Disconnect while a client is connected, Show window
// and Quit. busy says that an action of the window is running, which greys
// out Start, Stop and Disconnect as the window's buttons are.
func NewTrayMenu(vm ViewModel, busy bool) []TrayItem {
	items := []TrayItem{{Label: vm.State, Disabled: true}, {Separator: true}}
	if vm.CanStop {
		items = append(items, TrayItem{Label: TextStop, Action: TrayStop, Disabled: busy})
	} else {
		items = append(items, TrayItem{Label: TextStart, Action: TrayStart, Disabled: busy || !vm.CanStart})
	}
	if vm.Session != nil {
		items = append(items, TrayItem{Label: TextDisconnect, Action: TrayDisconnect, Disabled: busy})
	}
	return append(items, TrayItem{Separator: true},
		TrayItem{Label: TextShowWindow, Action: TrayShowWindow},
		TrayItem{Label: TextQuit, Action: TrayQuit})
}

// trayIconSize is the size the icons are drawn at, which the tray scales
// to its sizes.
const trayIconSize = 64

// TrayIcon draws the menu-bar icon of the state st: a screen on a stand,
// empty while the server is stopped, with a dot in the middle while it
// listens, and filled while a client is connected. macOS uses it as a
// template image, of which only the alpha counts, so it is black.
func TrayIcon(st ServerState) (gui.WindowIcon, error) {
	const (
		left, right = 6, 58 // the screen
		top, bottom = 10, 46
		line        = 5
	)
	img := image.NewNRGBA(image.Rect(0, 0, trayIconSize, trayIconSize))
	black := image.NewUniform(color.NRGBA{A: 0xFF})
	fill := func(r image.Rectangle) { draw.Draw(img, r, black, image.Point{}, draw.Src) }
	// The frame, or the whole screen while connected.
	if st == ServerConnected {
		fill(image.Rect(left, top, right, bottom))
	} else {
		fill(image.Rect(left, top, right, top+line))
		fill(image.Rect(left, bottom-line, right, bottom))
		fill(image.Rect(left, top, left+line, bottom))
		fill(image.Rect(right-line, top, right, bottom))
	}
	if st == ServerListening {
		const cx, cy, r = (left + right) / 2, (top + bottom) / 2, 7
		for y := cy - r; y <= cy+r; y++ {
			for x := cx - r; x <= cx+r; x++ {
				if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r {
					img.SetNRGBA(x, y, color.NRGBA{A: 0xFF})
				}
			}
		}
	}
	// The stand and its foot.
	fill(image.Rect(29, bottom, 35, 54))
	fill(image.Rect(18, 54, 46, 58))
	return desktop.TrayIconFromRGBA(trayIconSize, img.Pix)
}

// trayEntries are the Glutter menu entries of items; act runs an action.
func trayEntries(items []TrayItem, act func(TrayAction)) []desktop.TrayMenuEntry {
	entries := make([]desktop.TrayMenuEntry, 0, len(items))
	for _, it := range items {
		if it.Separator {
			entries = append(entries, desktop.NewTrayMenuSeparator())
			continue
		}
		var fn func()
		if a := it.Action; a != TrayNone {
			fn = func() { act(a) }
		}
		entries = append(entries, desktop.NewTrayMenuItem(it.Label, fn, desktop.TrayMenuItemOpts{Disabled: it.Disabled}))
	}
	return entries
}

// windowController is the window of the app, nil in a test without one.
func (s *windowState) windowController() *m.RegularWindowController {
	c, _ := m.WidgetsBindingOf(s.Context()).WindowingOwner().ImplicitWindowController()
	return c
}
