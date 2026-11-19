package rdptest

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/fastpath"
	"github.com/rm4n0s/mardp/rdp/internal/pointer"
)

// TagCacheIndexInvalid: a pointer update with a cache index outside the
// client's pointer cache, or a Cached Pointer Update of an empty entry.
const TagCacheIndexInvalid = "ClientCacheIndexInvalid"

// Cursor is the pointer as the client shows it, kept from the pointer
// updates the server sends.
type Cursor struct {
	// Hidden and Default say the pointer is hidden, or the client's own;
	// otherwise it is Shape.
	Hidden, Default bool
	Shape           pointer.Shape
	// X and Y are the last position the server sent.
	X, Y int
	// Cached counts Cached Pointer Updates, Shapes the updates with a
	// shape.
	Cached, Shapes int

	cache []*pointer.Shape
}

// NewCursor returns the pointer of a client whose pointer cache has size
// entries, at least one.
func NewCursor(size int) *Cursor {
	return &Cursor{Default: true, cache: make([]*pointer.Shape, max(size, 1))}
}

// NewCursor returns the pointer of this client, its cache as large as the
// one its Confirm Active offers: pointerCacheSize, or without it
// colorPointerCacheSize.
func (c *Client) NewCursor() *Cursor {
	n := int(c.Caps.Pointer.ColorCacheSize)
	if c.Caps.Pointer.HasCacheSize && c.Caps.Pointer.CacheSize != 0 {
		n = int(c.Caps.Pointer.CacheSize)
	}
	return NewCursor(n)
}

// Apply applies a fast-path update and returns it as a pointer update; ok
// is false for an update that is not about the pointer, which it ignores.
func (cur *Cursor) Apply(u fastpath.Update) (pu pointer.Update, ok bool, err error) {
	switch u.Code {
	case fastpath.UpdatePointerHidden, fastpath.UpdatePointerDefault, fastpath.UpdatePointerPosition,
		fastpath.UpdateCachedPointer, fastpath.UpdateColorPointer, fastpath.UpdateNewPointer, fastpath.UpdateLargePointer:
	default:
		return pointer.Update{}, false, nil
	}
	pu, err = pointer.Parse(u.Code, u.Data)
	if err != nil {
		return pointer.Update{}, false, err
	}
	switch pu.Kind {
	case pointer.KindHidden:
		cur.Hidden, cur.Default = true, false
	case pointer.KindDefault:
		cur.Hidden, cur.Default = false, true
	case pointer.KindPosition:
		cur.X, cur.Y = pu.X, pu.Y
	case pointer.KindCached:
		if pu.Index >= len(cur.cache) || cur.cache[pu.Index] == nil {
			return pointer.Update{}, false, errors.New(TagCacheIndexInvalid, "a cached pointer the client does not have", "index", pu.Index)
		}
		cur.Hidden, cur.Default, cur.Shape = false, false, *cur.cache[pu.Index]
		cur.Cached++
	case pointer.KindShape:
		if pu.Index >= len(cur.cache) {
			return pointer.Update{}, false, errors.New(TagCacheIndexInvalid, "a cache index outside the client's cache",
				"index", pu.Index, "size", len(cur.cache))
		}
		s := pu.Shape
		cur.cache[pu.Index] = &s
		cur.Hidden, cur.Default, cur.Shape = false, false, s
		cur.Shapes++
	}
	return pu, true, nil
}
