package pointer

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/fastpath"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// randomShape is a w×h shape with transparent, half transparent and opaque
// pixels, and padding after each row.
func randomShape(rng *rand.Rand, w, h int) *Shape {
	s := &Shape{Stride: 4*w + 8, Width: w, Height: h, HotX: rng.IntN(w), HotY: rng.IntN(h)}
	s.Pix = make([]byte, s.Stride*h)
	for y := range h {
		for x := range w {
			p := s.pixel(x, y)
			for c := range 3 {
				p[c] = byte(rng.IntN(256))
			}
			switch rng.IntN(3) {
			case 0:
				p[3] = 0
			case 1:
				p[3] = byte(rng.IntN(256))
			default:
				p[3] = 0xff
			}
		}
	}
	return s
}

// want is the shape a client reads back from s sent at bpp.
func want(s *Shape, bpp int) []byte {
	out := make([]byte, 4*s.Width*s.Height)
	for y := range s.Height {
		for x := range s.Width {
			p, o := s.pixel(x, y), out[4*(y*s.Width+x):]
			switch {
			case bpp == 32 && p[3] != 0:
				copy(o, p)
			case bpp == 24 && p[3] >= 0x80:
				copy(o, p[:3])
				o[3] = 0xff
			}
		}
	}
	return out
}

func TestBytes(t *testing.T) {
	// A 2×2 shape: top row opaque blue and transparent, bottom row half
	// transparent red and opaque white.
	s := &Shape{Pix: []byte{
		0xff, 0, 0, 0xff, 1, 2, 3, 0,
		0, 0, 0xff, 0x40, 0xff, 0xff, 0xff, 0xff,
	}, Stride: 8, Width: 2, Height: 2, HotX: 1, HotY: 0}
	tests := []struct {
		name   string
		append func(w *wire.Writer)
		want   []byte
	}{
		{"position", func(w *wire.Writer) { AppendPosition(w, 0x123, 0x45) }, []byte{0x23, 0x01, 0x45, 0x00}},
		{"cached", func(w *wire.Writer) { AppendCached(w, 7) }, []byte{7, 0}},
		{"new", func(w *wire.Writer) { AppendNew(w, s, 3) }, []byte{
			0x20, 0, // xorBpp
			3, 0, 1, 0, 0, 0, 2, 0, 2, 0, // cacheIndex, hotSpot, width, height
			4, 0, 16, 0, // lengthAndMask, lengthXorMask
			0, 0, 0xff, 0x40, 0xff, 0xff, 0xff, 0xff, // bottom row first
			0xff, 0, 0, 0xff, 0, 0, 0, 0, // the transparent pixel is 0
			0x00, 0, 0x40, 0, // AND rows, 2 bytes each: the top right pixel
		}},
		{"large", func(w *wire.Writer) { AppendLarge(w, s, 3) }, []byte{
			0x20, 0, 3, 0, 1, 0, 0, 0, 2, 0, 2, 0,
			4, 0, 0, 0, 16, 0, 0, 0,
			0, 0, 0xff, 0x40, 0xff, 0xff, 0xff, 0xff,
			0xff, 0, 0, 0xff, 0, 0, 0, 0,
			0x00, 0, 0x40, 0,
		}},
		{"color", func(w *wire.Writer) { AppendColor(w, s, 3) }, []byte{
			3, 0, 1, 0, 0, 0, 2, 0, 2, 0,
			4, 0, 12, 0, // rows of 6 bytes
			0, 0, 0, 0xff, 0xff, 0xff, // alpha 0x40 is transparent at 24 bpp
			0xff, 0, 0, 0, 0, 0,
			0x80, 0, 0x40, 0,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w wire.Writer
			tt.append(&w)
			if !bytes.Equal(w.Bytes(), tt.want) {
				t.Errorf("got  % x\nwant % x", w.Bytes(), tt.want)
			}
		})
	}
	if n := UpdateSize(2, 2, false); n != 36 {
		t.Errorf("UpdateSize %d, want 36", n)
	}
	if n := UpdateSize(2, 2, true); n != 40 {
		t.Errorf("UpdateSize large %d, want 40", n)
	}
}

// TestMaskPadding uses the examples of 2.2.9.1.1.4.4: a 3×3 cursor takes
// 10 bytes per XOR scan line at 24 bpp, and a 7×7 one 2 bytes per AND scan
// line.
func TestMaskPadding(t *testing.T) {
	if n := xorLen(3, 3, 24); n != 30 {
		t.Errorf("3×3 XOR mask %d bytes, want 30", n)
	}
	if n := andLen(7, 7); n != 14 {
		t.Errorf("7×7 AND mask %d bytes, want 14", n)
	}
	if n := andLen(17, 1); n != 4 {
		t.Errorf("17 pixels of AND mask %d bytes, want 4", n)
	}
}

func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	kinds := []struct {
		code   uint8
		bpp    int
		side   int
		append func(w *wire.Writer, s *Shape, i uint16)
	}{
		{fastpath.UpdateNewPointer, 32, MaxSide96, AppendNew},
		{fastpath.UpdateColorPointer, 24, MaxSide96, AppendColor},
		{fastpath.UpdateLargePointer, 32, MaxSideLarge, AppendLarge},
	}
	for i := range 300 {
		k := kinds[i%len(kinds)]
		w, h := rng.IntN(k.side)+1, rng.IntN(k.side)+1
		if i < 30 {
			w, h = rng.IntN(17)+1, rng.IntN(17)+1
		}
		s := randomShape(rng, w, h)
		var wr wire.Writer
		k.append(&wr, s, uint16(i))
		if k.code != fastpath.UpdateLargePointer && wr.Len() != UpdateSize(w, h, false) && k.bpp == 32 {
			t.Fatalf("%d bytes, UpdateSize says %d", wr.Len(), UpdateSize(w, h, false))
		}
		u, err := Parse(k.code, wr.Bytes())
		if err != nil {
			t.Fatalf("%d×%d at %d: %v", w, h, k.bpp, err)
		}
		if u.Kind != KindShape || u.Index != i || u.BitsPerPixel != k.bpp || u.Large != (k.code == fastpath.UpdateLargePointer) ||
			u.Shape.Width != w || u.Shape.Height != h || u.Shape.HotX != s.HotX || u.Shape.HotY != s.HotY {
			t.Fatalf("read %+v", u)
		}
		if !bytes.Equal(u.Shape.Pix, want(s, k.bpp)) {
			t.Fatalf("%d×%d at %d: the pixels differ", w, h, k.bpp)
		}
	}
	for _, tt := range []struct {
		code uint8
		data []byte
		want Update
	}{
		{fastpath.UpdatePointerHidden, nil, Update{Kind: KindHidden}},
		{fastpath.UpdatePointerDefault, nil, Update{Kind: KindDefault}},
		{fastpath.UpdatePointerPosition, []byte{1, 2, 3, 4}, Update{Kind: KindPosition, X: 0x201, Y: 0x403}},
		{fastpath.UpdateCachedPointer, []byte{9, 0}, Update{Kind: KindCached, Index: 9}},
	} {
		u, err := Parse(tt.code, tt.data)
		if err != nil || u.Kind != tt.want.Kind || u.X != tt.want.X || u.Y != tt.want.Y || u.Index != tt.want.Index {
			t.Errorf("code %d: %+v, %v", tt.code, u, err)
		}
	}
}

func TestShrink(t *testing.T) {
	// Fits: returned as it is.
	s := randomShape(rand.New(rand.NewPCG(3, 4)), 32, 20)
	if got := Shrink(nil, s, MaxSide); &got.Pix[0] != &s.Pix[0] || got.Width != 32 {
		t.Error("a shape that fits was changed")
	}

	// 4×2 into 2: blocks of 2×2. The left block averages an opaque white
	// and an opaque black pixel with two transparent ones; the right one is
	// all transparent.
	s = &Shape{Pix: []byte{
		0xff, 0xff, 0xff, 0xff, 9, 9, 9, 0, 1, 1, 1, 0, 2, 2, 2, 0,
		0, 0, 0, 0xff, 9, 9, 9, 0, 3, 3, 3, 0, 4, 4, 4, 0,
	}, Stride: 16, Width: 4, Height: 2, HotX: 3, HotY: 1}
	got := Shrink(make([]byte, 64), s, 2)
	wantPix := []byte{0x7f, 0x7f, 0x7f, 0x7f, 0, 0, 0, 0}
	if got.Width != 2 || got.Height != 1 || got.HotX != 1 || got.HotY != 0 || got.Stride != 8 || !bytes.Equal(got.Pix, wantPix) {
		t.Errorf("got %+v, want pixels % x", got, wantPix)
	}

	// Large shapes come within every limit, odd sizes included.
	rng := rand.New(rand.NewPCG(5, 6))
	buf := make([]byte, 4*MaxSideLarge*MaxSideLarge)
	for _, tt := range []struct{ w, h, side, ww, wh int }{
		{64, 64, MaxSide, 32, 32},
		{65, 30, MaxSide, 22, 10},
		{200, 400, MaxSide96, 40, 80},
		{1000, 10, MaxSideLarge, 334, 4},
	} {
		s := randomShape(rng, tt.w, tt.h)
		got := Shrink(buf, s, tt.side)
		if got.Width != tt.ww || got.Height != tt.wh || got.HotX >= got.Width || got.HotY >= got.Height {
			t.Errorf("%d×%d into %d: %d×%d hotspot %d,%d", tt.w, tt.h, tt.side, got.Width, got.Height, got.HotX, got.HotY)
		}
	}
}

func TestErrors(t *testing.T) {
	var w wire.Writer
	AppendNew(&w, &Shape{Pix: make([]byte, 16), Stride: 8, Width: 2, Height: 2}, 0)
	good := w.Bytes()
	edit := func(off int, b ...byte) []byte {
		d := bytes.Clone(good)
		copy(d[off:], b)
		return d
	}
	// The shape is all transparent; a transparent pixel whose XOR is not 0
	// inverts the screen.
	inverting := edit(16, 1)
	tests := []struct {
		name  string
		code  uint8
		data  []byte
		route string
	}{
		{"not a pointer update", fastpath.UpdateBitmap, nil, "Parse." + TagCodeUnknown},
		{"trailing data", fastpath.UpdatePointerHidden, []byte{0}, "Parse." + TagTrailingData},
		{"cut position", fastpath.UpdatePointerPosition, []byte{1, 2, 3}, "Parse->Reader.U16LE." + wire.TagTruncated},
		{"cut position x", fastpath.UpdatePointerPosition, []byte{1}, "Parse->Reader.U16LE." + wire.TagTruncated},
		{"cut cached", fastpath.UpdateCachedPointer, []byte{1}, "Parse->Reader.U16LE." + wire.TagTruncated},
		{"cut xorBpp", fastpath.UpdateNewPointer, []byte{32}, "Parse->Reader.U16LE." + wire.TagTruncated},
		{"cut fields", fastpath.UpdateNewPointer, good[:8], "Parse->readShape->Reader.U16LE." + wire.TagTruncated},
		{"cut lengths", fastpath.UpdateNewPointer, good[:13], "Parse->readShape->Reader.U16LE." + wire.TagTruncated},
		{"cut and length", fastpath.UpdateNewPointer, good[:12], "Parse->readShape->Reader.U16LE." + wire.TagTruncated},
		{"cut large lengths", fastpath.UpdateLargePointer, good[:15], "Parse->readShape->Reader.U32LE." + wire.TagTruncated},
		{"cut large and length", fastpath.UpdateLargePointer, good[:13], "Parse->readShape->Reader.U32LE." + wire.TagTruncated},
		{"cut XOR mask", fastpath.UpdateNewPointer, good[:20], "Parse->readShape->Reader.Bytes." + wire.TagTruncated},
		{"cut AND mask", fastpath.UpdateNewPointer, good[:len(good)-1], "Parse->readShape->Reader.Bytes." + wire.TagTruncated},
		{"no width", fastpath.UpdateNewPointer, edit(8, 0, 0), "Parse->readShape." + TagShapeInvalid},
		{"too high", fastpath.UpdateNewPointer, edit(10, 97, 0), "Parse->readShape." + TagShapeInvalid},
		{"8 bpp", fastpath.UpdateNewPointer, edit(0, 8, 0), "Parse->readShape." + TagShapeInvalid},
		{"wrong XOR length", fastpath.UpdateNewPointer, edit(14, 15, 0), "Parse->readShape." + TagShapeInvalid},
		{"wrong AND length", fastpath.UpdateNewPointer, edit(12, 2, 0), "Parse->readShape." + TagShapeInvalid},
		{"hotspot outside", fastpath.UpdateNewPointer, edit(4, 2, 0), "Parse->readShape." + TagShapeInvalid},
		{"inverting pixel", fastpath.UpdateNewPointer, inverting, "Parse->readShape." + TagShapeInvalid},
		{"trailing after the shape", fastpath.UpdateNewPointer, append(bytes.Clone(good), 0), "Parse." + TagTrailingData},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.code, tt.data)
			appErr, ok := errors.FromError(err)
			if !ok {
				t.Fatalf("expected *errors.Error, got %T", err)
			}
			if !appErr.HasRoute(tt.route) {
				t.Errorf("unexpected failure path: %s, want %s", appErr.Route(), tt.route)
			}
		})
	}
}

func TestHostile(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	codes := []uint8{fastpath.UpdateColorPointer, fastpath.UpdateNewPointer, fastpath.UpdateLargePointer}
	for _, code := range codes {
		var w wire.Writer
		s := randomShape(rng, 13, 9)
		switch code {
		case fastpath.UpdateColorPointer:
			AppendColor(&w, s, 1)
		case fastpath.UpdateNewPointer:
			AppendNew(&w, s, 1)
		default:
			AppendLarge(&w, s, 1)
		}
		seed := w.Bytes()
		for n := range len(seed) {
			_, _ = Parse(code, seed[:n])
		}
		for range 5000 {
			d := bytes.Clone(seed)
			for range rng.IntN(4) + 1 {
				d[rng.IntN(len(d))] = byte(rng.IntN(256))
			}
			_, _ = Parse(code, d)
		}
	}
}

func TestAllocations(t *testing.T) {
	s := randomShape(rand.New(rand.NewPCG(9, 10)), 96, 96)
	w := wire.NewWriter(make([]byte, 0, 2*UpdateSize(96, 96, true)))
	buf := make([]byte, 4*32*32)
	n := testing.AllocsPerRun(50, func() {
		w.Reset()
		AppendNew(&w, s, 1)
		AppendColor(&w, s, 1)
		small := Shrink(buf, s, MaxSide)
		AppendNew(&w, &small, 2)
	})
	if n != 0 {
		t.Errorf("%v allocations, want 0", n)
	}
}
