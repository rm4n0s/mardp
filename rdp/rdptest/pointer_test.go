package rdptest

import (
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/caps"
	"github.com/rm4n0s/mardp/rdp/internal/fastpath"
	"github.com/rm4n0s/mardp/rdp/internal/pointer"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

func TestCursor(t *testing.T) {
	shape := func(index uint16) []byte {
		var w wire.Writer
		pointer.AppendNew(&w, &pointer.Shape{Pix: []byte{1, 2, 3, 0xff}, Stride: 4, Width: 1, Height: 1}, index)
		return w.Bytes()
	}
	cur := NewCursor(2)
	if !cur.Default {
		t.Error("a new cursor is not the default pointer")
	}
	steps := []struct {
		u     fastpath.Update
		check func() bool
	}{
		{fastpath.Update{Code: fastpath.UpdateNewPointer, Data: shape(1)}, func() bool { return cur.Shapes == 1 && !cur.Default && cur.Shape.Pix[0] == 1 }},
		{fastpath.Update{Code: fastpath.UpdatePointerHidden}, func() bool { return cur.Hidden }},
		{fastpath.Update{Code: fastpath.UpdateCachedPointer, Data: []byte{1, 0}}, func() bool { return cur.Cached == 1 && !cur.Hidden }},
		{fastpath.Update{Code: fastpath.UpdatePointerDefault}, func() bool { return cur.Default && !cur.Hidden }},
		{fastpath.Update{Code: fastpath.UpdatePointerPosition, Data: []byte{5, 0, 6, 0}}, func() bool { return cur.X == 5 && cur.Y == 6 }},
	}
	for i, st := range steps {
		if _, ok, err := cur.Apply(st.u); !ok || err != nil || !st.check() {
			t.Errorf("step %d: %v, %v, cursor %+v", i, ok, err, *cur)
		}
	}
	if _, ok, err := cur.Apply(fastpath.Update{Code: fastpath.UpdateBitmap}); ok || err != nil {
		t.Errorf("a bitmap update: %v, %v", ok, err)
	}

	tests := []struct {
		name  string
		u     fastpath.Update
		route string
	}{
		{"empty entry", fastpath.Update{Code: fastpath.UpdateCachedPointer, Data: []byte{0, 0}}, "Cursor.Apply." + TagCacheIndexInvalid},
		{"outside the cache", fastpath.Update{Code: fastpath.UpdateCachedPointer, Data: []byte{2, 0}}, "Cursor.Apply." + TagCacheIndexInvalid},
		{"shape outside the cache", fastpath.Update{Code: fastpath.UpdateNewPointer, Data: shape(2)}, "Cursor.Apply." + TagCacheIndexInvalid},
		{"malformed", fastpath.Update{Code: fastpath.UpdatePointerPosition, Data: []byte{1}}, "Cursor.Apply->Parse->Reader.U16LE." + wire.TagTruncated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := cur.Apply(tt.u)
			appErr, ok := errors.FromError(err)
			if !ok {
				t.Fatalf("expected *errors.Error, got %T", err)
			}
			if !appErr.HasRoute(tt.route) {
				t.Errorf("unexpected failure path: %s, want %s", appErr.Route(), tt.route)
			}
		})
	}

	// The cache of a client is the one its Confirm Active offers.
	c := &Client{}
	for _, tt := range []struct {
		p    caps.Pointer
		size int
	}{
		{caps.Pointer{ColorCacheSize: 20, CacheSize: 25, HasCacheSize: true}, 25},
		{caps.Pointer{ColorCacheSize: 20, HasCacheSize: true}, 20},
		{caps.Pointer{ColorCacheSize: 20}, 20},
		{caps.Pointer{}, 1},
	} {
		c.Caps.Pointer = tt.p
		if n := len(c.NewCursor().cache); n != tt.size {
			t.Errorf("caps %+v: cache of %d, want %d", tt.p, n, tt.size)
		}
	}
}
