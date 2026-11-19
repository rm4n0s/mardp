package bitmap

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// TestTileRoundTrip appends random tiles of every kind of picture and draws
// them back, pixel for pixel.
func TestTileRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 12))
	kinds := []string{"flat", "noise", "gradient", "stripes", "mixed"}
	for i := range 400 {
		kind := kinds[i%len(kinds)]
		img := newImage(1+rng.IntN(150), 1+rng.IntN(100), rng.IntN(3)*4)
		paint(rng, img, kind)
		w := 1 + rng.IntN(min(img.Width, 64))
		h := 1 + rng.IntN(min(img.Height, MaxTile/w, 64))
		r := Rect{X: rng.IntN(img.Width - w + 1), Y: rng.IntN(img.Height - h + 1), Width: w, Height: h}
		var wr wire.Writer
		planar, err := AppendTile(&wr, img, r)
		if err != nil {
			t.Fatal(err)
		}
		if wr.Len() > 4*w*h {
			t.Fatalf("%s %dx%d: %d bytes, more than its pixels", kind, w, h, wr.Len())
		}
		if !planar && wr.Len() != 4*w*h {
			t.Fatalf("%s %dx%d: uncompressed %d bytes", kind, w, h, wr.Len())
		}
		if kind == "flat" && w*h > 4 && !planar {
			t.Errorf("flat %dx%d not Planar", w, h)
		}
		// Without an alpha plane, plain planes take 3 bytes a pixel and 2
		// more, so only a tile of one or two pixels goes uncompressed.
		if planar != (w*h > 2) {
			t.Errorf("%s %dx%d: planar %v", kind, w, h, planar)
		}
		screen := newImage(img.Width, img.Height, 4)
		if err := DrawTile(screen, r.X, r.Y, w, h, planar, wr.Bytes()); err != nil {
			t.Fatal(err)
		}
		for y := range img.Height {
			for x := range img.Width {
				got := screen.Pix[y*screen.Stride+4*x : y*screen.Stride+4*x+4]
				want := []byte{0, 0, 0, 0}
				if x >= r.X && x < r.X+w && y >= r.Y && y < r.Y+h {
					want = img.Pix[y*img.Stride+4*x : y*img.Stride+4*x+4]
				}
				if !bytes.Equal(got[:3], want[:3]) {
					t.Fatalf("%s %+v planar %v pixel %d,%d: % x, want % x", kind, r, planar, x, y, got, want)
				}
			}
		}
	}
}

// TestTileBytes checks that a tile's scan lines go top-down, uncompressed
// and in Planar, without an alpha plane.
func TestTileBytes(t *testing.T) {
	img := newImage(3, 3, 0)
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	var w wire.Writer
	planar, err := AppendTile(&w, img, Rect{X: 1, Y: 1, Width: 1, Height: 2})
	if err != nil || planar {
		t.Fatalf("planar %v, err %v", planar, err)
	}
	want := []byte{16, 17, 18, 19, 28, 29, 30, 31}
	if !bytes.Equal(w.Bytes(), want) {
		t.Errorf("uncompressed % x\nwant         % x", w.Bytes(), want)
	}

	// Noise of 2×2 goes as plain planes, top-down: red, green, blue.
	w.Reset()
	planar, err = AppendTile(&w, img, Rect{X: 1, Y: 1, Width: 2, Height: 2})
	if err != nil || !planar {
		t.Fatalf("planar %v, err %v", planar, err)
	}
	want = []byte{planarNoAlpha, 18, 22, 30, 34, 17, 21, 29, 33, 16, 20, 28, 32, 0}
	if !bytes.Equal(w.Bytes(), want) {
		t.Errorf("plain planes % x\nwant         % x", w.Bytes(), want)
	}

	// A 4×2 tile, its top row one color and its bottom row another: each
	// plane is two scan lines, the top one first, and the second is the
	// delta of the first.
	two := newImage(4, 2, 0)
	for x := range 4 {
		copy(two.Pix[4*x:], []byte{0x30, 0x20, 0x10, 0xff})
		copy(two.Pix[two.Stride+4*x:], []byte{0x31, 0x22, 0x13, 0xff})
	}
	w.Reset()
	planar, err = AppendTile(&w, two, Rect{Width: 4, Height: 2})
	if err != nil || !planar {
		t.Fatalf("planar %v, err %v", planar, err)
	}
	// Deltas are sign-magnitude doubled: +3 → 6, +2 → 4, +1 → 2.
	want = []byte{
		planarRLE | planarNoAlpha,
		0x13, 0x10, 0x13, 0x06,
		0x13, 0x20, 0x13, 0x04,
		0x13, 0x30, 0x13, 0x02,
	}
	if !bytes.Equal(w.Bytes(), want) {
		t.Errorf("planar % x\nwant   % x", w.Bytes(), want)
	}
	screen := newImage(4, 2, 0)
	if err := DrawTile(screen, 0, 0, 4, 2, true, w.Bytes()); err != nil {
		t.Fatal(err)
	}
	for i := range screen.Pix {
		if i%4 != 3 && screen.Pix[i] != two.Pix[i] {
			t.Fatalf("drawn % x\nwant  % x", screen.Pix, two.Pix)
		}
	}
}

func TestTileErrors(t *testing.T) {
	img := newImage(8, 8, 0)
	area := []struct {
		name string
		img  *Image
		r    Rect
	}{
		{"empty", img, Rect{Width: 1, Height: 0}},
		{"too large", newImage(100, 100, 0), Rect{Width: 65, Height: 64}},
		{"outside", img, Rect{Y: 4, Width: 1, Height: 5}},
		{"negative", img, Rect{X: -1, Width: 1, Height: 1}},
		{"short pixels", &Image{Pix: make([]byte, 10), Stride: 32, Width: 8, Height: 8}, Rect{Width: 1, Height: 1}},
		{"short stride", &Image{Pix: make([]byte, 256), Stride: 16, Width: 8, Height: 8}, Rect{Width: 1, Height: 1}},
	}
	for _, tt := range area {
		t.Run(tt.name, func(t *testing.T) {
			var w wire.Writer
			_, err := AppendTile(&w, tt.img, tt.r)
			assertRoute(t, err, "AppendTile."+TagAreaInvalid)
			if w.Len() != 0 {
				t.Errorf("%d bytes written", w.Len())
			}
		})
	}

	draw := []struct {
		name       string
		x, y, w, h int
		planar     bool
		data       []byte
		route      string
	}{
		{"outside", 7, 0, 2, 1, false, make([]byte, 8), "DrawTile." + TagRectInvalid},
		{"empty", 0, 0, 0, 1, false, nil, "DrawTile." + TagRectInvalid},
		{"negative", 0, -1, 1, 1, false, make([]byte, 4), "DrawTile." + TagRectInvalid},
		{"short pixels", 0, 0, 2, 1, false, make([]byte, 7), "DrawTile." + TagLengthInvalid},
		{"long pixels", 0, 0, 2, 1, false, make([]byte, 9), "DrawTile." + TagLengthInvalid},
		{"no planar header", 0, 0, 1, 1, true, nil, "DrawTile->decodePlanar->Reader.U8." + wire.TagTruncated},
		{"color loss", 0, 0, 1, 1, true, []byte{0x01 | planarNoAlpha, 0, 0, 0, 0}, "DrawTile->decodePlanar." + TagPlanarHeaderInvalid},
		{"bad RLE", 0, 0, 4, 1, true, []byte{planarRLE | planarNoAlpha, 0x00}, "DrawTile->decodePlanar->decodeRLE." + TagRLEInvalid},
	}
	for _, tt := range draw {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, DrawTile(newImage(8, 8, 0), tt.x, tt.y, tt.w, tt.h, tt.planar, tt.data), tt.route)
		})
	}
	big := newImage(200, 200, 0)
	assertRoute(t, DrawTile(big, 0, 0, 65, 64, false, make([]byte, 4*65*64)), "DrawTile."+TagRectInvalid)
}

// TestTileHostile draws every cut and many mutations of valid tiles
// without a panic.
func TestTileHostile(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 14))
	for _, kind := range []string{"flat", "mixed", "gradient", "stripes"} {
		img := newImage(64, 64, 0)
		paint(rng, img, kind)
		var w wire.Writer
		planar, err := AppendTile(&w, img, Rect{Width: 64, Height: 64})
		if err != nil {
			t.Fatal(err)
		}
		s := bytes.Clone(w.Bytes())
		screen := newImage(64, 64, 0)
		for n := range s {
			_ = DrawTile(screen, 0, 0, 64, 64, planar, s[:n])
		}
		for range 3000 {
			m := bytes.Clone(s)
			for range 1 + rng.IntN(4) {
				m[rng.IntN(len(m))] = byte(rng.Uint32())
			}
			_ = DrawTile(screen, 0, 0, 64, 64, true, m)
		}
	}
}

func TestTileAllocations(t *testing.T) {
	rng := rand.New(rand.NewPCG(15, 16))
	img := newImage(200, 100, 0)
	paint(rng, img, "mixed")
	w := wire.NewWriter(make([]byte, 0, 64<<10))
	allocs := testing.AllocsPerRun(20, func() {
		w.Reset()
		if _, err := AppendTile(&w, img, Rect{X: 3, Y: 5, Width: 64, Height: 64}); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Errorf("%v allocations", allocs)
	}
}
