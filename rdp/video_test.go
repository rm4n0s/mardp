package rdp

import (
	"bytes"
	"context"
	"image"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/rdp/internal/caps"
	"github.com/rm4n0s/mardp/rdp/internal/fastpath"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// testImage is a w×h picture with flat areas, gradients, stripes and noise,
// so that every rectangle sent takes a different compression; alpha is
// 0xFF, as on a screen.
func testImage(rng *rand.Rand, w, h int) *Image {
	// Rows have padding after them, and the last row none.
	stride := 4*w + 12
	img := &Image{Pix: make([]byte, (h-1)*stride+4*w), Stride: stride, Width: w, Height: h}
	flat := [3]byte{byte(rng.IntN(256)), byte(rng.IntN(256)), byte(rng.IntN(256))}
	for y := range h {
		for x := range w {
			p := img.Pix[y*img.Stride+4*x:]
			switch (x/97 + y/61) % 4 {
			case 0:
				copy(p, flat[:])
			case 1:
				p[0], p[1], p[2] = byte(x), byte(y), byte(x+y)
			case 2:
				p[0], p[1], p[2] = byte(y/3*40), byte(y/3*40), 0
			default:
				p[0], p[1], p[2] = byte(rng.IntN(256)), byte(rng.IntN(256)), byte(rng.IntN(256))
			}
			p[3] = 0xff
		}
	}
	return img
}

// paintRect copies the area r of img onto sc.
func paintRect(sc *rdptest.Screen, img *Image, r image.Rectangle) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		copy(sc.Pix[y*sc.Stride+4*r.Min.X:y*sc.Stride+4*r.Max.X], img.Pix[y*img.Stride+4*r.Min.X:])
	}
}

// drawUntil reads Bitmap Updates into screen until it is want.
func drawUntil(t *testing.T, c *rdptest.Client, screen, want *rdptest.Screen) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for !bytes.Equal(screen.Pix, want.Pix) {
		u, err := c.ReadUpdate(ctx)
		if err != nil {
			t.Fatalf("the screen is not the picture: %v", err)
		}
		if u.Code != fastpath.UpdateBitmap {
			t.Fatalf("update %d, want a bitmap update", u.Code)
		}
		if _, err := screen.DrawBitmaps(u.Data); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBitmaps(t *testing.T) {
	tests := []struct {
		name          string
		opts          rdptest.Options
		width, height int // of the picture; 0 is the desktop's
		skipAlpha     bool
	}{
		{name: "default", skipAlpha: true},
		{name: "no multifragment", opts: rdptest.Options{EditCaps: func(cc *caps.Caps) {
			cc.Sets &^= 1 << caps.SetMultifragmentUpdate
			cc.MaxRequestSize = 0
		}}, skipAlpha: true},
		{name: "alpha plane", opts: rdptest.Options{EditCaps: func(cc *caps.Caps) { cc.Bitmap.DrawingFlags = 0 }}},
		{name: "picture larger than the desktop", width: 1100, height: 800, skipAlpha: true},
		{name: "picture smaller than the desktop", width: 500, height: 300, skipAlpha: true},
		{name: "small desktop", opts: rdptest.Options{Width: 300, Height: 200}, width: 1024, height: 768, skipAlpha: true},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newSessions()
			ts := start(t, Config{Handler: h})
			c, _ := ts.open(t, tt.opts)
			s := h.next(t)
			if s.Client().SkipAlpha != tt.skipAlpha {
				t.Errorf("SkipAlpha %v, want %v", s.Client().SkipAlpha, tt.skipAlpha)
			}
			if p := s.Video().Path(); p != VideoBitmaps || p.String() != "bitmaps" {
				t.Errorf("path %v", p)
			}
			dw, dh := s.DesktopSize()
			w, ht := tt.width, tt.height
			if w == 0 {
				w, ht = dw, dh
			}
			rng := rand.New(rand.NewPCG(uint64(i), 10))
			bounds := image.Rect(0, 0, min(w, dw), min(ht, dh))
			screen, want := rdptest.NewScreen(dw, dh), rdptest.NewScreen(dw, dh)
			for round := range 6 {
				img := testImage(rng, w, ht)
				var rects []image.Rectangle
				if round == 0 {
					rects = []image.Rectangle{image.Rect(-10, -10, w+10, ht+10)}
				}
				for range rng.IntN(8) + 1 {
					x, y := rng.IntN(w+40)-20, rng.IntN(ht+40)-20
					rects = append(rects, image.Rect(x, y, x+rng.IntN(300)+1, y+rng.IntN(200)+1))
				}
				if err := s.Video().SendRects(img, rects); err != nil {
					t.Fatal(err)
				}
				for _, r := range rects {
					paintRect(want, img, r.Intersect(bounds))
				}
				drawUntil(t, c, screen, want)
			}

			// The screen can match before the last update of a round has
			// arrived; a pixel unlike the screen's, sent last, drains them.
			marker := &Image{Pix: []byte{^screen.Pix[0], 0, 0, 0xff}, Stride: 4, Width: 1, Height: 1}
			if err := s.Video().SendRects(marker, []image.Rectangle{image.Rect(0, 0, 1, 1)}); err != nil {
				t.Fatal(err)
			}
			paintRect(want, marker, image.Rect(0, 0, 1, 1))
			drawUntil(t, c, screen, want)

			// Nothing inside the desktop sends nothing: the next update is
			// the rectangle after it.
			img := testImage(rng, w, ht)
			if err := s.Video().SendRects(img, []image.Rectangle{image.Rect(-5, -5, 0, 0), image.Rect(dw, 0, dw+5, 5), {}}); err != nil {
				t.Fatal(err)
			}
			if err := s.Video().SendRects(img, nil); err != nil {
				t.Fatal(err)
			}
			if err := s.Video().SendRects(img, []image.Rectangle{image.Rect(1, 2, 4, 6)}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			u, err := c.ReadUpdate(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if n, err := screen.DrawBitmaps(u.Data); err != nil || n != 1 {
				t.Fatalf("%d rectangles, %v; want the one sent", n, err)
			}
			paintRect(want, img, image.Rect(1, 2, 4, 6))
			if !bytes.Equal(screen.Pix, want.Pix) {
				t.Error("the rectangle was not drawn")
			}
		})
	}
}

func TestVideoErrors(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h})
	ts.open(t, rdptest.Options{})
	s := h.next(t)
	all := []image.Rectangle{image.Rect(0, 0, 10, 10)}
	tests := []struct {
		name string
		img  *Image
	}{
		{"no image", nil},
		{"empty", &Image{}},
		{"short stride", &Image{Pix: make([]byte, 400), Stride: 36, Width: 10, Height: 10}},
		{"short pixels", &Image{Pix: make([]byte, 399), Stride: 40, Width: 10, Height: 10}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, s.Video().SendRects(tt.img, all), "Video.SendRects."+TagImageInvalid)
		})
	}

	t.Run("no fast-path output", func(t *testing.T) {
		h := newSessions()
		ts := start(t, Config{Handler: h})
		ts.open(t, rdptest.Options{EditCaps: func(cc *caps.Caps) { cc.General.ExtraFlags &^= caps.ExtraFastPathOutput }})
		s := h.next(t)
		img := testImage(rand.New(rand.NewPCG(1, 2)), 10, 10)
		assertRoute(t, s.Video().SendRects(img, all), "Video.SendRects."+TagFastPathUnsupported)
	})

	t.Run("queue full", func(t *testing.T) {
		h := newSessions()
		ts := start(t, Config{Handler: h})
		ts.open(t, rdptest.Options{})
		s := h.next(t)
		dw, dh := s.DesktopSize()
		img := testImage(rand.New(rand.NewPCG(3, 4)), dw, dh)
		// The client reads nothing, so the connection fills and then the
		// queue does.
		var err error
		for range 1000 {
			if err = s.Video().SendRects(img, []image.Rectangle{image.Rect(0, 0, dw, dh)}); err != nil {
				break
			}
		}
		assertRoute(t, err, "Video.SendRects->writer.send->writer.get."+TagQueueFull)
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
		img := testImage(rand.New(rand.NewPCG(5, 6)), 10, 10)
		assertRoute(t, s.Video().SendRects(img, all), "Video.SendRects->writer.send->writer.get."+TagSessionOver)
	})
}

func TestVideoAllocs(t *testing.T) {
	sig := make(signal)
	wr := newWriter(sig, func(error) {})
	go wr.run()
	defer wr.halt()
	s := &Session{wr: wr, width: 256, height: 256, client: Client{FastPathOutput: true, MaxRequestSize: 0x3f0000, SkipAlpha: true}}
	s.video.s = s
	img := testImage(rand.New(rand.NewPCG(7, 8)), 256, 256)
	rects := []image.Rectangle{image.Rect(0, 0, 256, 256), image.Rect(10, 20, 30, 40)}
	var err error
	for range videoDepth {
		if err = s.Video().SendRects(img, rects); err != nil {
			t.Fatal(err)
		}
		<-sig
	}
	n := testing.AllocsPerRun(20, func() {
		if err = s.Video().SendRects(img, rects); err == nil {
			<-sig
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%v allocations per SendRects, want 0", n)
	}
}

func BenchmarkSendRects(b *testing.B) {
	sig := make(signal)
	wr := newWriter(sig, func(error) {})
	go wr.run()
	defer wr.halt()
	s := &Session{wr: wr, width: 1024, height: 768, client: Client{FastPathOutput: true, MaxRequestSize: 0x3f0000, SkipAlpha: true}}
	s.video.s = s
	img := testImage(rand.New(rand.NewPCG(9, 10)), 1024, 768)
	rects := []image.Rectangle{image.Rect(0, 0, 1024, 768)}
	b.SetBytes(4 * 1024 * 768)
	for b.Loop() {
		if err := s.Video().SendRects(img, rects); err != nil {
			b.Fatal(err)
		}
		<-sig
	}
}
