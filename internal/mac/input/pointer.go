package input

import (
	"encoding/binary"
	"hash/maphash"
	"sync"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/internal/host"
)

// Tags of the errors of the pointer.
const (
	// TagPointerUnreadable: macOS gave no image of the pointer.
	TagPointerUnreadable = "PointerUnreadable"
	// TagPointerTooLarge: the pointer's image is larger than
	// maxPointerSide a side.
	TagPointerTooLarge = "PointerTooLarge"
)

// maxPointerSide is the largest pointer read, in pixels a side; the
// library shrinks what the client cannot show.
const maxPointerSide = 256

// PointerOptions configure a Pointer.
type PointerOptions struct {
	// Display is the UUID of the display the session shows; empty is the
	// main display.
	Display string
}

// pointerInfo is what one read of the system pointer gives.
type pointerInfo struct {
	visible       bool
	width, height int
	hotX, hotY    int
	onDisplay     bool
	x, y          float64
}

// The answers of the C reader (mardpPtrRead).
const (
	readOK       = 0
	readNoImage  = 1
	readTooLarge = 2
	readNotDrawn = 3
)

// checkRead turns the C reader's answer into the info or its error.
func checkRead(status int, info pointerInfo) (pointerInfo, error) {
	switch status {
	case readOK:
		return info, nil
	case readTooLarge:
		return pointerInfo{}, errors.New(TagPointerTooLarge, "the pointer's image is too large",
			"width", info.width, "height", info.height, "max", maxPointerSide)
	}
	return pointerInfo{}, errors.New(TagPointerUnreadable, "macOS gave no image of the pointer", "status", status)
}

// pointerReader reads the system pointer's image into buf; the tests fake
// it.
type pointerReader interface {
	read(uuid string, buf []byte) (pointerInfo, error)
}

// Pointer reads the system pointer (SPEC §10.3): its image at the display's
// scale with straight alpha, its hotspot, whether it is shown, and where
// it is on the display. It implements host.PointerSource; the host decides
// when to read it. Its image is valid until the next Current.
type Pointer struct {
	opts PointerOptions
	mac  pointerReader

	mu  sync.Mutex
	buf []byte
	img host.Image
	h   maphash.Hash
}

// Pointer implements host.PointerSource.
var _ host.PointerSource = (*Pointer)(nil)

// newPointer makes a Pointer over mac.
func newPointer(opts PointerOptions, mac pointerReader) *Pointer {
	return &Pointer{opts: opts, mac: mac, buf: make([]byte, 4*maxPointerSide*maxPointerSide)}
}

// Current reads the pointer now.
func (p *Pointer) Current() (host.PointerShape, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	info, err := p.mac.read(p.opts.Display, p.buf)
	if err != nil {
		return host.PointerShape{}, err
	}
	sh := host.PointerShape{Visible: info.visible, OnDisplay: info.onDisplay, X: info.x, Y: info.y}
	if !info.visible {
		return sh, nil
	}
	w, h := info.width, info.height
	p.img = host.Image{Pix: p.buf[:4*w*h], Stride: 4 * w, Width: w, Height: h}
	sh.Image, sh.HotX, sh.HotY = &p.img, info.hotX, info.hotY
	var dims [16]byte
	binary.LittleEndian.PutUint32(dims[0:], uint32(w))
	binary.LittleEndian.PutUint32(dims[4:], uint32(h))
	binary.LittleEndian.PutUint32(dims[8:], uint32(info.hotX))
	binary.LittleEndian.PutUint32(dims[12:], uint32(info.hotY))
	p.h.Reset()
	_, _ = p.h.Write(dims[:])
	_, _ = p.h.Write(p.img.Pix)
	sh.Hash = p.h.Sum64()
	return sh, nil
}
