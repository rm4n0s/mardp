package rdp

import (
	"bytes"
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/rdp/internal/caps"
	"github.com/rm4n0s/mardp/rdp/internal/fastpath"
	"github.com/rm4n0s/mardp/rdp/internal/pointer"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// pointerImage is a w×h pointer with transparent, half transparent and
// opaque pixels.
func pointerImage(rng *rand.Rand, w, h int) *Image {
	img := &Image{Pix: make([]byte, 4*w*h), Stride: 4 * w, Width: w, Height: h}
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2] = byte(rng.IntN(256)), byte(rng.IntN(256)), byte(rng.IntN(256))
		img.Pix[i+3] = [4]byte{0, 0x30, 0xc0, 0xff}[rng.IntN(4)]
	}
	return img
}

// shown is how a client shows img sent at most side pixels a side, at bpp:
// shrunk as the server shrinks it, transparent pixels 0.
func shown(img *Image, hotX, hotY, side, bpp int) pointer.Shape {
	s := pointer.Shape{Pix: img.Pix, Stride: img.Stride, Width: img.Width, Height: img.Height, HotX: hotX, HotY: hotY}
	s = pointer.Shrink(make([]byte, 4*side*side), &s, side)
	out := s
	out.Stride = 4 * s.Width
	out.Pix = make([]byte, out.Stride*s.Height)
	for y := range s.Height {
		for x := range s.Width {
			p, o := s.Pix[y*s.Stride+4*x:][:4], out.Pix[y*out.Stride+4*x:][:4]
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

// readPointer reads the next update, which has to be about the pointer,
// into cur.
func readPointer(t *testing.T, c *rdptest.Client, cur *rdptest.Cursor) pointer.Update {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u, err := c.ReadUpdate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pu, ok, err := cur.Apply(u)
	if err != nil || !ok {
		t.Fatalf("update %d: %v, %v", u.Code, ok, err)
	}
	return pu
}

func sameShape(t *testing.T, got, want pointer.Shape) {
	t.Helper()
	if got.Width != want.Width || got.Height != want.Height || got.HotX != want.HotX || got.HotY != want.HotY ||
		!bytes.Equal(got.Pix, want.Pix) {
		t.Errorf("shape %d×%d hotspot %d,%d, want %d×%d hotspot %d,%d (or other pixels)",
			got.Width, got.Height, got.HotX, got.HotY, want.Width, want.Height, want.HotX, want.HotY)
	}
}

func TestPointer(t *testing.T) {
	noMultifragment := func(cc *caps.Caps) {
		cc.Sets &^= 1 << caps.SetMultifragmentUpdate
		cc.MaxRequestSize = 0
	}
	tests := []struct {
		name  string
		edit  func(*caps.Caps)
		side  int   // the largest side the client gets
		bpp   int   // of its shapes
		large uint8 // the code of a shape larger than 96
	}{
		{"default", nil, 384, 32, fastpath.UpdateLargePointer},
		{"96 only", func(cc *caps.Caps) { cc.LargePointer = caps.LargePointer96 }, 96, 32, fastpath.UpdateNewPointer},
		{"no large pointer", func(cc *caps.Caps) { cc.Sets &^= 1 << caps.SetLargePointer }, 32, 32, fastpath.UpdateNewPointer},
		{"no multifragment", noMultifragment, 32, 32, fastpath.UpdateNewPointer},
		{"color pointers only", func(cc *caps.Caps) { cc.Pointer.CacheSize = 0 }, 384, 24, fastpath.UpdateLargePointer},
		{"color pointers, 96", func(cc *caps.Caps) {
			cc.Pointer.HasCacheSize = false
			cc.LargePointer = caps.LargePointer96
		}, 96, 24, fastpath.UpdateColorPointer},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newSessions()
			ts := start(t, Config{Handler: h})
			c, _ := ts.open(t, rdptest.Options{EditCaps: tt.edit})
			s := h.next(t)
			cur := c.NewCursor()
			ptr := s.Pointer()
			if s.Client().AlphaPointer != (tt.bpp == 32) || ptr.maxSide() != tt.side {
				t.Fatalf("alpha pointer %v, side %d", s.Client().AlphaPointer, ptr.maxSide())
			}
			small := fastpath.UpdateNewPointer
			if tt.bpp == 24 {
				small = fastpath.UpdateColorPointer
			}
			rng := rand.New(rand.NewPCG(uint64(i), 1))
			a, b := pointerImage(rng, 20, 24), pointerImage(rng, 17, 5)
			set := func(img *Image, x, y int) {
				t.Helper()
				if err := ptr.SetShape(img, x, y); err != nil {
					t.Fatal(err)
				}
			}

			set(a, 3, 4)
			if u := readPointer(t, c, cur); u.Kind != pointer.KindShape || u.BitsPerPixel != tt.bpp {
				t.Errorf("first shape: kind %d, %d bpp", u.Kind, u.BitsPerPixel)
			}
			sameShape(t, cur.Shape, shown(a, 3, 4, tt.side, tt.bpp))
			set(b, 16, 0)
			readPointer(t, c, cur)
			sameShape(t, cur.Shape, shown(b, 16, 0, tt.side, tt.bpp))

			// A shape seen before is a cache index; the same pixels with
			// another hotspot are another shape.
			set(a, 3, 4)
			if u := readPointer(t, c, cur); u.Kind != pointer.KindCached || cur.Cached != 1 {
				t.Errorf("a repeated shape: kind %d", u.Kind)
			}
			sameShape(t, cur.Shape, shown(a, 3, 4, tt.side, tt.bpp))
			set(a, 0, 0)
			if u := readPointer(t, c, cur); u.Kind != pointer.KindShape {
				t.Errorf("another hotspot: kind %d", u.Kind)
			}

			// Hidden, default, and back.
			if err := ptr.Hide(); err != nil {
				t.Fatal(err)
			}
			if readPointer(t, c, cur); !cur.Hidden {
				t.Error("not hidden")
			}
			if err := ptr.Default(); err != nil {
				t.Fatal(err)
			}
			if readPointer(t, c, cur); !cur.Default || cur.Hidden {
				t.Error("not the default pointer")
			}
			set(b, 16, 0)
			if readPointer(t, c, cur); cur.Hidden || cur.Default || cur.Cached != 2 {
				t.Error("the shape did not come back from the cache")
			}

			// Positions, clamped to the desktop.
			w, ht := s.DesktopSize()
			for _, p := range [][4]int{{10, 20, 10, 20}, {-5, 99999, 0, ht - 1}, {w, -1, w - 1, 0}} {
				if err := ptr.Move(p[0], p[1]); err != nil {
					t.Fatal(err)
				}
				if readPointer(t, c, cur); cur.X != p[2] || cur.Y != p[3] {
					t.Errorf("move to %d,%d: at %d,%d, want %d,%d", p[0], p[1], cur.X, cur.Y, p[2], p[3])
				}
			}

			// Large shapes come as large as the client shows them.
			for _, sz := range [][2]int{{96, 40}, {97, 30}, {200, 150}, {500, 384}} {
				img := pointerImage(rng, sz[0], sz[1])
				set(img, sz[0]-1, sz[1]-1)
				want := shown(img, sz[0]-1, sz[1]-1, tt.side, tt.bpp)
				code := small
				if max(want.Width, want.Height) > 96 {
					// A Large Pointer Update is 32 bpp for every client.
					code = tt.large
					want = shown(img, sz[0]-1, sz[1]-1, tt.side, 32)
				}
				u := readPointer(t, c, cur)
				if gotCode := map[bool]uint8{true: fastpath.UpdateLargePointer, false: small}[u.Large]; gotCode != code {
					t.Errorf("%d×%d sent with code %d, want %d", sz[0], sz[1], gotCode, code)
				}
				sameShape(t, cur.Shape, want)
			}
			if cur.Shapes != 7 {
				t.Errorf("%d shapes sent, want 7", cur.Shapes)
			}
		})
	}
}

func TestPointerCache(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h})
	c, _ := ts.open(t, rdptest.Options{EditCaps: func(cc *caps.Caps) { cc.Pointer.CacheSize = 3 }})
	s := h.next(t)
	cur := c.NewCursor()
	rng := rand.New(rand.NewPCG(1, 2))
	var imgs []*Image
	for range 5 {
		imgs = append(imgs, pointerImage(rng, 8, 8))
	}
	// 0 1 2 fill the cache; 0 is used again, so 3 takes the place of 1, the
	// least recently used; then 1 is a new shape again (in place of 2), and
	// 0, 3 and 1 are cached.
	for _, step := range []struct {
		img    int
		cached bool
		index  int
	}{
		{0, false, 0}, {1, false, 1}, {2, false, 2}, {0, true, 0}, {3, false, 1}, {1, false, 2},
		{0, true, 0}, {3, true, 1}, {1, true, 2}, {2, false, 0},
	} {
		if err := s.Pointer().SetShape(imgs[step.img], 0, 0); err != nil {
			t.Fatal(err)
		}
		u := readPointer(t, c, cur)
		if (u.Kind == pointer.KindCached) != step.cached || u.Index != step.index {
			t.Errorf("shape %d: kind %d at %d, want cached %v at %d", step.img, u.Kind, u.Index, step.cached, step.index)
		}
		sameShape(t, cur.Shape, shown(imgs[step.img], 0, 0, 384, 32))
	}

	// A client without a pointer cache gets every shape whole, at index 0.
	h = newSessions()
	ts = start(t, Config{Handler: h})
	c, _ = ts.open(t, rdptest.Options{EditCaps: func(cc *caps.Caps) {
		cc.Pointer.HasCacheSize = false
		cc.Pointer.ColorCacheSize = 0
	}})
	s = h.next(t)
	cur = c.NewCursor()
	for range 3 {
		if err := s.Pointer().SetShape(imgs[0], 1, 1); err != nil {
			t.Fatal(err)
		}
		if u := readPointer(t, c, cur); u.Kind != pointer.KindShape || u.Index != 0 || u.BitsPerPixel != 24 {
			t.Errorf("without a cache: kind %d at %d, %d bpp", u.Kind, u.Index, u.BitsPerPixel)
		}
	}
}

func TestPointerErrors(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h})
	ts.open(t, rdptest.Options{})
	s := h.next(t)
	img := pointerImage(rand.New(rand.NewPCG(3, 4)), 10, 10)
	for _, tt := range []struct {
		name       string
		img        *Image
		hotX, hotY int
	}{
		{"no image", nil, 0, 0},
		{"empty", &Image{}, 0, 0},
		{"short pixels", &Image{Pix: make([]byte, 399), Stride: 40, Width: 10, Height: 10}, 0, 0},
		{"short stride", &Image{Pix: make([]byte, 400), Stride: 36, Width: 10, Height: 10}, 0, 0},
		{"hotspot right", img, 10, 0},
		{"hotspot below", img, 0, 10},
		{"hotspot left", img, -1, 0},
		{"hotspot above", img, 0, -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, s.Pointer().SetShape(tt.img, tt.hotX, tt.hotY), "Pointer.SetShape."+TagPointerShapeInvalid)
		})
	}

	t.Run("no fast-path output", func(t *testing.T) {
		h := newSessions()
		ts := start(t, Config{Handler: h})
		ts.open(t, rdptest.Options{EditCaps: func(cc *caps.Caps) { cc.General.ExtraFlags &^= caps.ExtraFastPathOutput }})
		s := h.next(t)
		assertRoute(t, s.Pointer().SetShape(img, 0, 0), "Pointer.SetShape."+TagFastPathUnsupported)
		assertRoute(t, s.Pointer().Move(1, 1), "Pointer.Move."+TagFastPathUnsupported)
		assertRoute(t, s.Pointer().Hide(), "Pointer.Hide->Session.sendUpdate."+TagFastPathUnsupported)
		assertRoute(t, s.Pointer().Default(), "Pointer.Default->Session.sendUpdate."+TagFastPathUnsupported)
	})

	t.Run("queue full", func(t *testing.T) {
		h := newSessions()
		ts := start(t, Config{Handler: h})
		ts.open(t, rdptest.Options{})
		s := h.next(t)
		ptr := s.Pointer()
		if err := ptr.SetShape(img, 0, 0); err != nil {
			t.Fatal(err)
		}
		// The client reads nothing, so the connection fills and then the
		// queue does: with large shapes, each another by one pixel.
		big := pointerImage(rand.New(rand.NewPCG(7, 8)), 384, 384)
		var err error
		for i := range 1000 {
			big.Pix[0] = byte(i)
			big.Pix[1] = byte(i >> 8)
			if err = ptr.SetShape(big, 0, 0); err != nil {
				break
			}
		}
		assertRoute(t, err, "Pointer.SetShape->writer.send->writer.get."+TagQueueFull)
		assertRoute(t, ptr.Move(1, 1), "Pointer.Move->writer.send->writer.get."+TagQueueFull)
		assertRoute(t, ptr.SetShape(img, 0, 0), "Pointer.SetShape->writer.send->writer.get."+TagQueueFull)
		other := pointerImage(rand.New(rand.NewPCG(5, 6)), 10, 10)
		assertRoute(t, ptr.SetShape(other, 0, 0), "Pointer.SetShape->writer.send->writer.get."+TagQueueFull)
		assertRoute(t, ptr.Hide(), "Pointer.Hide->Session.sendUpdate->writer.send->writer.get."+TagQueueFull)
		// A shape that was not sent is not taken as cached.
		for _, sl := range ptr.slots {
			if sl.used != 0 && sl.hash == ptr.hash(&pointer.Shape{Pix: other.Pix, Stride: other.Stride, Width: 10, Height: 10}) {
				t.Error("a shape that was not sent is in the cache")
			}
		}
	})

	t.Run("session over", func(t *testing.T) {
		h := newSessions()
		ts := start(t, Config{Handler: h})
		c, _ := ts.open(t, rdptest.Options{})
		s := h.next(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Disconnect(ctx); err != nil {
			t.Fatal(err)
		}
		h.waitEnded(t)
		<-s.wr.exited
		assertRoute(t, s.Pointer().SetShape(img, 0, 0), "Pointer.SetShape->writer.send->writer.get."+TagSessionOver)
		assertRoute(t, s.Pointer().Move(0, 0), "Pointer.Move->writer.send->writer.get."+TagSessionOver)
		assertRoute(t, s.Pointer().Default(), "Pointer.Default->Session.sendUpdate->writer.send->writer.get."+TagSessionOver)
	})
}

func TestPointerAllocs(t *testing.T) {
	sig := make(signal)
	wr := newWriter(sig, func(error) {})
	go wr.run()
	defer wr.halt()
	s := &Session{wr: wr, width: 800, height: 600, client: Client{FastPathOutput: true, MaxRequestSize: 0x3f0000,
		AlphaPointer: true, PointerCacheSize: 2, LargePointer: 384}}
	s.pointer.init(s)
	rng := rand.New(rand.NewPCG(7, 8))
	// Three shapes and two cache entries: every shape is sent whole.
	imgs := []*Image{pointerImage(rng, 32, 32), pointerImage(rng, 100, 120), pointerImage(rng, 500, 20)}
	i := 0
	tests := []struct {
		name string
		run  func() error
	}{
		{"new shapes", func() error { i++; return s.Pointer().SetShape(imgs[i%3], 0, 0) }},
		{"cached shape", func() error { return s.Pointer().SetShape(imgs[0], 0, 0) }},
		{"move", func() error { return s.Pointer().Move(5, 6) }},
		{"hide", func() error { return s.Pointer().Hide() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			// Each packet grows to the largest shape it carries.
			for range 4 * controlDepth {
				if err = tt.run(); err != nil {
					t.Fatal(err)
				}
				<-sig
			}
			n := testing.AllocsPerRun(30, func() {
				if err = tt.run(); err == nil {
					<-sig
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("%v allocations, want 0", n)
			}
		})
	}
}
