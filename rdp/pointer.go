package rdp

import (
	"encoding/binary"
	"hash/maphash"
	"sync"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/fastpath"
	"github.com/rm4n0s/mardp/rdp/internal/pointer"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// TagPointerShapeInvalid: a pointer shape whose pixels do not hold its
// size, or whose hotspot is outside it.
const TagPointerShapeInvalid = "PointerShapeInvalid"

// maxPointerSlots is the most cache entries a session uses, whatever the
// client offers: pointers change among a few shapes.
const maxPointerSlots = 64

// Pointer is a session's pointer (SPEC §10.3). The client draws it, so a
// move of the client's mouse needs nothing from the server. Its methods
// may be called from any goroutine, and like SendRects they queue for the
// session's writer, on the control queue, without waiting for the network.
type Pointer struct {
	s *Session

	mu    sync.Mutex
	h     maphash.Hash
	slots []pointerSlot
	clock uint64
	buf   []byte // the shrunk shape
}

// pointerSlot is what the client's cache holds at one index: the hash of a
// shape, and when it was last used, 0 when never.
type pointerSlot struct {
	hash uint64
	used uint64
}

// Pointer returns the session's pointer.
func (s *Session) Pointer() *Pointer { return &s.pointer }

// init prepares the pointer for the client of s.
func (p *Pointer) init(s *Session) {
	p.s = s
	p.h.SetSeed(maphash.MakeSeed())
	p.slots = make([]pointerSlot, min(s.client.PointerCacheSize, maxPointerSlots))
}

// SetShape shows img as the pointer, its hotspot at hotX, hotY: alpha is
// kept when the client takes the New Pointer Update (Client.AlphaPointer),
// otherwise a pixel is opaque where its alpha is at least half. A shape
// larger than the client shows (32 pixels a side, 96 or 384 with Large
// Pointer support, and what fits in its largest fast-path update) is shrunk
// by averaging blocks of pixels. A shape the client has in its cache is
// sent as its cache index. img is not used after SetShape returns.
func (p *Pointer) SetShape(img *Image, hotX, hotY int) error {
	s := p.s
	if img == nil || img.Width <= 0 || img.Height <= 0 || img.Stride < 4*img.Width ||
		len(img.Pix) < (img.Height-1)*img.Stride+4*img.Width || hotX < 0 || hotY < 0 || hotX >= img.Width || hotY >= img.Height {
		var w, h, stride, n int
		if img != nil {
			w, h, stride, n = img.Width, img.Height, img.Stride, len(img.Pix)
		}
		return errors.New(TagPointerShapeInvalid, "the pointer's pixels do not hold its size, or its hotspot is outside",
			"width", w, "height", h, "stride", stride, "len", n, "hotX", hotX, "hotY", hotY)
	}
	if !s.client.FastPathOutput {
		return errors.New(TagFastPathUnsupported, "the client did not confirm fast-path output", "code", fastpath.UpdateNewPointer)
	}
	shape := pointer.Shape{Pix: img.Pix, Stride: img.Stride, Width: img.Width, Height: img.Height, HotX: hotX, HotY: hotY}

	p.mu.Lock()
	defer p.mu.Unlock()
	hash := p.hash(&shape)
	slot := -1
	for i, sl := range p.slots {
		if sl.used != 0 && sl.hash == hash {
			if err := s.wr.send(queueControl, func(pk *packet) error {
				pk.tmp.Reset()
				pointer.AppendCached(&pk.tmp, uint16(i))
				return fastpath.AppendUpdate(&pk.w, fastpath.UpdateCachedPointer, pk.tmp.Bytes(), s.client.MaxRequestSize)
			}); err != nil {
				return err
			}
			p.clock++
			p.slots[i].used = p.clock
			return nil
		}
		if slot < 0 || sl.used < p.slots[slot].used {
			slot = i
		}
	}

	side := p.maxSide()
	if max(shape.Width, shape.Height) > side {
		if n := 4 * side * side; len(p.buf) < n {
			p.buf = make([]byte, n)
		}
		shape = pointer.Shrink(p.buf, &shape, side)
	}
	code := fastpath.UpdateColorPointer
	switch {
	case max(shape.Width, shape.Height) > pointer.MaxSide96:
		code = fastpath.UpdateLargePointer
	case s.client.AlphaPointer:
		code = fastpath.UpdateNewPointer
	}
	index := uint16(max(slot, 0))
	if err := s.wr.send(queueControl, func(pk *packet) error {
		return appendShape(&pk.w, &pk.tmp, code, &shape, index, s.client.MaxRequestSize)
	}); err != nil {
		return err
	}
	if slot >= 0 {
		p.clock++
		p.slots[slot] = pointerSlot{hash: hash, used: p.clock}
	}
	return nil
}

// appendShape appends to w the fast-path update code of shape, cached at
// index, built in tmp.
func appendShape(w, tmp *wire.Writer, code uint8, shape *pointer.Shape, index uint16, maxRequestSize int) error {
	tmp.Reset()
	switch code {
	case fastpath.UpdateLargePointer:
		pointer.AppendLarge(tmp, shape, index)
	case fastpath.UpdateNewPointer:
		pointer.AppendNew(tmp, shape, index)
	default:
		pointer.AppendColor(tmp, shape, index)
	}
	return fastpath.AppendUpdate(w, code, tmp.Bytes(), maxRequestSize)
}

// maxSide is the largest pointer side the client shows: 32, or 96 or 384
// when it said so and such a pointer fits in its largest update.
func (p *Pointer) maxSide() int {
	cl := &p.s.client
	limit := fastpath.MaxUpdate(cl.MaxRequestSize)
	switch {
	case cl.LargePointer >= pointer.MaxSideLarge && pointer.UpdateSize(pointer.MaxSideLarge, pointer.MaxSideLarge, true) <= limit:
		return pointer.MaxSideLarge
	case cl.LargePointer >= pointer.MaxSide96 && pointer.UpdateSize(pointer.MaxSide96, pointer.MaxSide96, false) <= limit:
		return pointer.MaxSide96
	}
	return pointer.MaxSide
}

// hash is the hash of shape's size, hotspot and pixels.
func (p *Pointer) hash(shape *pointer.Shape) uint64 {
	p.h.Reset()
	var b [16]byte
	binary.LittleEndian.PutUint32(b[0:], uint32(shape.Width))
	binary.LittleEndian.PutUint32(b[4:], uint32(shape.Height))
	binary.LittleEndian.PutUint32(b[8:], uint32(shape.HotX))
	binary.LittleEndian.PutUint32(b[12:], uint32(shape.HotY))
	_, _ = p.h.Write(b[:]) // a maphash.Hash never fails to write
	for y := range shape.Height {
		_, _ = p.h.Write(shape.Pix[y*shape.Stride : y*shape.Stride+4*shape.Width])
	}
	return p.h.Sum64()
}

// Hide hides the pointer, until the next SetShape or Default.
func (p *Pointer) Hide() error {
	return p.s.sendUpdate(queueControl, fastpath.UpdatePointerHidden, nil)
}

// Default shows the client's own default pointer.
func (p *Pointer) Default() error {
	return p.s.sendUpdate(queueControl, fastpath.UpdatePointerDefault, nil)
}

// Move moves the pointer to x, y on the desktop, clamped to it: for when
// something on the server moves the pointer, never to echo the client's
// own moves.
func (p *Pointer) Move(x, y int) error {
	s := p.s
	x, y = min(max(x, 0), s.width-1), min(max(y, 0), s.height-1)
	if !s.client.FastPathOutput {
		return errors.New(TagFastPathUnsupported, "the client did not confirm fast-path output", "code", fastpath.UpdatePointerPosition)
	}
	return s.wr.send(queueControl, func(pk *packet) error {
		pk.tmp.Reset()
		pointer.AppendPosition(&pk.tmp, uint16(x), uint16(y))
		return fastpath.AppendUpdate(&pk.w, fastpath.UpdatePointerPosition, pk.tmp.Bytes(), s.client.MaxRequestSize)
	})
}
