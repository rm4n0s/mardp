package bitmap

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// planeImage makes an image whose red channel holds lines, given in the
// order of a Planar stream: the first line is the image's bottom row.
func planeImage(lines [][]byte) *Image {
	h, w := len(lines), len(lines[0])
	img := newImage(w, h, 0)
	for i, line := range lines {
		for col, v := range line {
			img.Pix[(h-1-i)*img.Stride+4*col+2] = v
		}
	}
	return img
}

// newImage makes a w×h image with stride pad bytes wider than its pixels.
func newImage(w, h, pad int) *Image {
	stride := 4*w + pad
	return &Image{Pix: make([]byte, stride*h), Stride: stride, Width: w, Height: h}
}

// TestMicrosoftRLE encodes and decodes the planes of MS-RDPEGDI's examples
// of RDP 6.0 RLE, byte for byte.
func TestMicrosoftRLE(t *testing.T) {
	tests := []struct {
		name    string // where the example is
		lines   [][]byte
		encoded []byte
	}{
		{"3.1.9.2 segments", [][]byte{[]byte("AAAABBCCCCCD")},
			[]byte{0x13, 'A', 0x34, 'B', 'B', 'C', 0x10, 'D'}},
		{"3.1.9.2 delta values", [][]byte{{10, 20, 30, 40, 50, 60}, {5, 15, 25, 35, 45, 55}, {5, 15, 25, 35, 45, 55}},
			// RAW [10..60]; RUN [0], RAW [-5]; RUN [5], RAW [<none>]; RUN [6].
			[]byte{0x60, 10, 20, 30, 40, 50, 60, 0x15, 0x09, 0x06}},
		{"3.1.9.2.1 and 3.1.9.2.3", [][]byte{{255, 255, 255, 255, 254, 253}, {254, 192, 132, 96, 75, 25}, {253, 140, 62, 14, 135, 193}},
			[]byte{0x13, 0xFF, 0x20, 0xFE, 0xFD, 0x60, 0x01, 0x7D, 0xF5, 0xC2, 0x9A, 0x38, 0x60, 0x01, 0x67, 0x8B, 0xA3, 0x78, 0xAF}},
		{"3.1.9.2.2", [][]byte{bytes.Repeat([]byte("A"), 100)},
			[]byte{0x1F, 0x41, 0xF2, 0x52}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img := planeImage(tt.lines)
			p := plane{img: img, r: Rect{Width: img.Width, Height: img.Height}, c: 2}
			var w wire.Writer
			p.appendRLE(&w)
			if !bytes.Equal(w.Bytes(), tt.encoded) {
				t.Errorf("encoded % x\nwant    % x", w.Bytes(), tt.encoded)
			}
			got := newImage(img.Width, img.Height, 0)
			r := wire.NewReader(tt.encoded)
			tg := target{screen: got, w: img.Width, h: img.Height, height: img.Height}
			if err := decodeRLE(&r, &tg, img.Width, 2); err != nil || r.Remaining() != 0 {
				t.Fatalf("decode: %v, %d bytes left", err, r.Remaining())
			}
			if !bytes.Equal(got.Pix, img.Pix) {
				t.Errorf("decoded %v\nwant    %v", got.Pix, img.Pix)
			}
		})
	}
}

// TestRuns encodes every run length from 1 to 200, after raw values and
// at the start of a scan line, and checks the segments' rules.
func TestRuns(t *testing.T) {
	for n := 1; n <= 200; n++ {
		for _, prefix := range [][]byte{nil, {7}, {7, 8, 9}, bytes.Repeat([]byte{1, 2}, 10)} {
			for _, v := range []byte{0, 5} {
				line := append(append([]byte{}, prefix...), bytes.Repeat([]byte{v}, n)...)
				img := planeImage([][]byte{line})
				p := plane{img: img, r: Rect{Width: img.Width, Height: 1}, c: 2}
				var w wire.Writer
				p.appendRLE(&w)
				checkSegments(t, w.Bytes())
				got := newImage(img.Width, 1, 0)
				r := wire.NewReader(w.Bytes())
				if err := decodeRLE(&r, &target{screen: got, w: img.Width, h: 1, height: 1}, img.Width, 2); err != nil || r.Remaining() != 0 {
					t.Fatalf("run %d after % x: %v", n, prefix, err)
				}
				if !bytes.Equal(got.Pix, img.Pix) {
					t.Fatalf("run %d of %d after % x: decoded wrong", n, v, prefix)
				}
			}
		}
	}
}

// checkSegments checks that segments follow 2.2.2.5.1.2: no zero control
// byte, and no raw values with a run length of 1 or 2.
func checkSegments(t *testing.T, b []byte) {
	t.Helper()
	for i := 0; i < len(b); {
		ctrl := b[i]
		raw, run := int(ctrl>>4), int(ctrl&0xf)
		if ctrl == 0 {
			t.Fatalf("zero control byte at %d in % x", i, b)
		}
		if run == 1 || run == 2 {
			raw = 0
		}
		i += 1 + raw
	}
}

// TestRoundTrip writes random areas of random images, each kind of picture
// (flat, noise, gradients, stripes), and draws them back on a screen.
func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	kinds := []string{"flat", "noise", "gradient", "stripes", "mixed"}
	for i := range 400 {
		kind := kinds[i%len(kinds)]
		img := newImage(1+rng.IntN(150), 1+rng.IntN(100), rng.IntN(3)*4)
		paint(rng, img, kind)
		w := 1 + rng.IntN(min(img.Width, 64))
		h := 1 + rng.IntN(min(img.Height, MaxArea/w))
		r := Rect{X: rng.IntN(img.Width - w + 1), Y: rng.IntN(img.Height - h + 1), Width: w, Height: h}
		noAlpha := rng.IntN(2) == 0
		var wr wire.Writer
		off := BeginUpdate(&wr)
		if err := AppendRect(&wr, img, r, noAlpha); err != nil {
			t.Fatal(err)
		}
		if err := EndUpdate(&wr, off, 1); err != nil {
			t.Fatal(err)
		}
		if n := wr.Len() - UpdateHeaderLen; n > RectSize(w*h) {
			t.Fatalf("%s %dx%d: %d bytes, more than %d", kind, w, h, n, RectSize(w*h))
		}
		screen := newImage(img.Width, img.Height, 4)
		drawAll(t, screen, wr.Bytes(), 1)
		for y := range img.Height {
			for x := range img.Width {
				got := screen.Pix[y*screen.Stride+4*x : y*screen.Stride+4*x+4]
				want := []byte{0, 0, 0, 0}
				if x >= r.X && x < r.X+w && y >= r.Y && y < r.Y+h {
					want = img.Pix[y*img.Stride+4*x : y*img.Stride+4*x+4]
				}
				if !bytes.Equal(got[:3], want[:3]) {
					t.Fatalf("%s %+v pixel %d,%d: % x, want % x", kind, r, x, y, got, want)
				}
			}
		}
	}
}

// paint fills img with one kind of picture; alpha is 0xFF, as on a screen.
func paint(rng *rand.Rand, img *Image, kind string) {
	base := [3]byte{byte(rng.Uint32()), byte(rng.Uint32()), byte(rng.Uint32())}
	for y := range img.Height {
		for x := range img.Width {
			px := img.Pix[y*img.Stride+4*x:]
			for c := range 3 {
				switch kind {
				case "flat":
					px[c] = base[c]
				case "noise":
					px[c] = byte(rng.Uint32())
				case "gradient":
					px[c] = base[c] + byte(x*(c+1)) - byte(y*(3-c))
				case "stripes":
					px[c] = base[c] * byte((x/3+y/5)%2)
				default:
					if rng.IntN(8) == 0 {
						px[c] = byte(rng.Uint32())
					} else {
						px[c] = base[c] + byte(x/7)
					}
				}
			}
			px[3] = 0xff
		}
	}
}

// drawAll draws every rectangle of an update and checks their number.
func drawAll(t *testing.T, screen *Image, data []byte, want int) {
	t.Helper()
	u, err := ParseUpdate(data)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for {
		d, ok, err := u.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		if err := Draw(screen, d); err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n != want {
		t.Fatalf("%d rectangles, want %d", n, want)
	}
}

// TestFormats checks which form AppendRect picks: RLE for a flat area,
// plain planes for noise without alpha, and RLE for noise with an alpha
// plane, which RLE makes small.
func TestFormats(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	flat, noise := newImage(64, 63, 0), newImage(64, 63, 0)
	paint(rng, flat, "flat")
	paint(rng, noise, "noise")
	r := Rect{Width: 64, Height: 63}
	tests := []struct {
		name    string
		img     *Image
		noAlpha bool
		flags   uint16
		header  int // the Planar header, -1 for none
		size    int // the bitmap's length, 0 for any
	}{
		{"flat, no alpha", flat, true, 0x0401, planarRLE | planarNoAlpha, 0},
		{"flat, alpha", flat, false, 0x0401, planarRLE, 0},
		{"noise, no alpha", noise, true, 0x0401, planarNoAlpha, 2 + 3*64*63},
		{"noise, alpha", noise, false, 0x0401, planarRLE, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w wire.Writer
			if err := AppendRect(&w, tt.img, r, tt.noAlpha); err != nil {
				t.Fatal(err)
			}
			u := Update{r: wire.NewReader(w.Bytes()), left: 1}
			d, ok, err := u.Next()
			if err != nil || !ok {
				t.Fatal(ok, err)
			}
			if d.Flags != tt.flags || (tt.header >= 0 && int(d.Bitmap[0]) != tt.header) || (tt.size > 0 && len(d.Bitmap) != tt.size) {
				t.Errorf("flags %#x header %#x size %d", d.Flags, d.Bitmap[0], len(d.Bitmap))
			}
			if d.Left != 0 || d.Top != 0 || d.Right != 63 || d.Bottom != 62 || d.Width != 64 || d.Height != 63 || d.BitsPerPixel != 32 {
				t.Errorf("%+v", d)
			}
			if tt.img == flat && len(d.Bitmap) > 600 {
				t.Errorf("a flat area takes %d bytes", len(d.Bitmap))
			}
		})
	}
}

// TestBytes checks a 2×2 area byte for byte in each form: the header of the
// Bitmap Data, and the scan lines bottom-up.
func TestBytes(t *testing.T) {
	img := newImage(3, 3, 0)
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	// Pixels 4 and 5 (row 1) and 7 and 8 (row 2), BGRA.
	var w wire.Writer
	appendRaw(&w, img, Rect{X: 1, Y: 1, Width: 2, Height: 2})
	want := []byte{28, 29, 30, 31, 32, 33, 34, 35, 16, 17, 18, 19, 20, 21, 22, 23}
	if !bytes.Equal(w.Bytes(), want) {
		t.Errorf("uncompressed % x\nwant         % x", w.Bytes(), want)
	}

	// A flat 4×1 area of one color, no alpha: each plane is a raw value
	// with a run of 3.
	flat := newImage(4, 1, 0)
	for x := range 4 {
		copy(flat.Pix[4*x:], []byte{0x30, 0x20, 0x10, 0xff})
	}
	w.Reset()
	if err := AppendRect(&w, flat, Rect{Width: 4, Height: 1}, true); err != nil {
		t.Fatal(err)
	}
	want = []byte{
		0, 0, 0, 0, 3, 0, 0, 0, 4, 0, 1, 0, 32, 0, 0x01, 0x04, 7, 0,
		planarRLE | planarNoAlpha, 0x13, 0x10, 0x13, 0x20, 0x13, 0x30,
	}
	if !bytes.Equal(w.Bytes(), want) {
		t.Errorf("planar % x\nwant   % x", w.Bytes(), want)
	}

	// The same with an alpha plane.
	w.Reset()
	if err := AppendRect(&w, flat, Rect{Width: 4, Height: 1}, false); err != nil {
		t.Fatal(err)
	}
	want = []byte{
		0, 0, 0, 0, 3, 0, 0, 0, 4, 0, 1, 0, 32, 0, 0x01, 0x04, 9, 0,
		planarRLE, 0x13, 0xff, 0x13, 0x10, 0x13, 0x20, 0x13, 0x30,
	}
	if !bytes.Equal(w.Bytes(), want) {
		t.Errorf("planar % x\nwant   % x", w.Bytes(), want)
	}

	// Two pixels: RLE takes 1+3×3 bytes, plain planes 1+3×2+1, as many as
	// the pixels, which are sent then.
	two := newImage(2, 1, 0)
	copy(two.Pix, []byte{1, 2, 3, 0xff, 4, 5, 6, 0xff})
	w.Reset()
	if err := AppendRect(&w, two, Rect{Width: 2, Height: 1}, true); err != nil {
		t.Fatal(err)
	}
	want = []byte{
		0, 0, 0, 0, 1, 0, 0, 0, 2, 0, 1, 0, 32, 0, 0, 0, 8, 0,
		1, 2, 3, 0xff, 4, 5, 6, 0xff,
	}
	if !bytes.Equal(w.Bytes(), want) {
		t.Errorf("uncompressed % x\nwant         % x", w.Bytes(), want)
	}

	// Three pixels: RLE takes 1+3×4 bytes, plain planes 1+3×3+1, the
	// pixels 12.
	three := newImage(3, 1, 0)
	copy(three.Pix, []byte{1, 2, 3, 0xff, 4, 5, 6, 0xff, 7, 8, 9, 0xff})
	w.Reset()
	if err := AppendRect(&w, three, Rect{Width: 3, Height: 1}, true); err != nil {
		t.Fatal(err)
	}
	want = []byte{
		0, 0, 0, 0, 2, 0, 0, 0, 3, 0, 1, 0, 32, 0, 0x01, 0x04, 11, 0,
		planarNoAlpha, 3, 6, 9, 2, 5, 8, 1, 4, 7, 0,
	}
	if !bytes.Equal(w.Bytes(), want) {
		t.Errorf("plain % x\nwant  % x", w.Bytes(), want)
	}
	screen := newImage(3, 1, 0)
	var u wire.Writer
	off := BeginUpdate(&u)
	u.Write(w.Bytes())
	if err := EndUpdate(&u, off, 1); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(u.Bytes()[:4], []byte{1, 0, 1, 0}) {
		t.Errorf("update header % x", u.Bytes()[:4])
	}
	drawAll(t, screen, u.Bytes(), 1)
	if !bytes.Equal(screen.Pix, three.Pix) {
		t.Errorf("drawn % x, want % x", screen.Pix, three.Pix)
	}
}

// update wraps rectangles in a Bitmap Update Data.
func update(n uint16, rects ...[]byte) []byte {
	var w wire.Writer
	w.U16LE(updateTypeBitmap)
	w.U16LE(n)
	for _, r := range rects {
		w.Write(r)
	}
	return w.Bytes()
}

// rect is a Bitmap Data at 0,0 of w×h with the given flags and bitmap.
func rect(w, h, bpp, flags uint16, bitmap ...byte) []byte {
	var b wire.Writer
	for _, v := range []uint16{0, 0, w - 1, h - 1, w, h, bpp, flags, uint16(len(bitmap))} {
		b.U16LE(v)
	}
	b.Write(bitmap)
	return b.Bytes()
}

func TestErrors(t *testing.T) {
	img := newImage(8, 8, 0)
	area := []struct {
		name string
		img  *Image
		r    Rect
	}{
		{"empty", img, Rect{Width: 0, Height: 1}},
		{"too large", newImage(100, 100, 0), Rect{Width: 64, Height: 64}},
		{"outside", img, Rect{X: 4, Width: 5, Height: 1}},
		{"negative", img, Rect{Y: -1, Width: 1, Height: 1}},
		{"short pixels", &Image{Pix: make([]byte, 10), Stride: 32, Width: 8, Height: 8}, Rect{Width: 1, Height: 1}},
		{"short stride", &Image{Pix: make([]byte, 256), Stride: 16, Width: 8, Height: 8}, Rect{Width: 1, Height: 1}},
	}
	for _, tt := range area {
		t.Run(tt.name, func(t *testing.T) {
			var w wire.Writer
			assertRoute(t, AppendRect(&w, tt.img, tt.r, true), "AppendRect."+TagAreaInvalid)
			if w.Len() != 0 {
				t.Errorf("%d bytes written", w.Len())
			}
		})
	}

	flat := rect(4, 1, 32, 0x0401, planarRLE|planarNoAlpha, 0x13, 0x10, 0x13, 0x20, 0x13, 0x30)
	read := []struct {
		name  string
		data  []byte
		route string
	}{
		{"cut header", []byte{1}, "ParseUpdate->Reader.U16LE." + wire.TagTruncated},
		{"cut count", []byte{1, 0, 1}, "ParseUpdate->Reader.U16LE." + wire.TagTruncated},
		{"not bitmap", []byte{2, 0, 0, 0}, "ParseUpdate." + TagUpdateInvalid},
		{"cut rectangle", update(1, flat[:10]), "Update.Next->Reader.U16LE." + wire.TagTruncated},
		{"cut length", update(1, flat[:17]), "Update.Next->Reader.U16LE." + wire.TagTruncated},
		{"cut bitmap", update(1, flat[:20]), "Update.Next->Reader.Bytes." + wire.TagTruncated},
		{"trailing", update(1, flat, []byte{0}), "Update.Next." + TagTrailingData},
		{"outside the screen", update(1, rect(9, 1, 32, 0, make([]byte, 36)...)), "Draw." + TagRectInvalid},
		{"empty bitmap", update(1, rect(0, 1, 32, 0)), "Draw." + TagRectInvalid},
		{"bitmap too large", update(1, rect(100, 100, 32, 0)), "Draw." + TagRectInvalid},
		{"16 bpp", update(1, rect(1, 1, 16, 0, 0, 0, 0, 0)), "Draw." + TagFormatUnsupported},
		{"compression header", update(1, rect(1, 1, 32, 0x0001, 0, 0, 0, 0)), "Draw." + TagFormatUnsupported},
		{"short pixels", update(1, rect(1, 1, 32, 0, 0, 0, 0)), "Draw." + TagLengthInvalid},
		{"no planar header", update(1, rect(1, 1, 32, 0x0401)), "Draw->decodePlanar->Reader.U8." + wire.TagTruncated},
		{"color loss", update(1, rect(1, 1, 32, 0x0401, 0x01|planarNoAlpha, 0, 0, 0, 0)), "Draw->decodePlanar." + TagPlanarHeaderInvalid},
		{"subsampling", update(1, rect(1, 1, 32, 0x0401, planarCS, 0, 0, 0, 0)), "Draw->decodePlanar." + TagPlanarHeaderInvalid},
		{"reserved", update(1, rect(1, 1, 32, 0x0401, 0x40|planarNoAlpha, 0, 0, 0, 0)), "Draw->decodePlanar." + TagPlanarHeaderInvalid},
		{"cut plain plane", update(1, rect(1, 1, 32, 0x0401, planarNoAlpha, 0, 0)), "Draw->decodePlanar->Reader.U8." + wire.TagTruncated},
		{"plain trailing", update(1, rect(1, 1, 32, 0x0401, planarNoAlpha, 0, 0, 0, 0, 0)), "Draw->decodePlanar." + TagTrailingData},
		{"zero control", update(1, rect(4, 1, 32, 0x0401, planarRLE|planarNoAlpha, 0x00)), "Draw->decodePlanar->decodeRLE." + TagRLEInvalid},
		{"segment too long", update(1, rect(4, 1, 32, 0x0401, planarRLE|planarNoAlpha, 0x14, 1)), "Draw->decodePlanar->decodeRLE." + TagRLEInvalid},
		{"long run too long", update(1, rect(4, 1, 32, 0x0401, planarRLE|planarNoAlpha, 0x01)), "Draw->decodePlanar->decodeRLE." + TagRLEInvalid},
		{"cut control", update(1, rect(4, 1, 32, 0x0401, planarRLE|planarNoAlpha, 0x13, 0x10)), "Draw->decodePlanar->decodeRLE->Reader.U8." + wire.TagTruncated},
		{"cut raw", update(1, rect(4, 1, 32, 0x0401, planarRLE|planarNoAlpha, 0x30, 0x10)), "Draw->decodePlanar->decodeRLE->Reader.U8." + wire.TagTruncated},
		{"RLE trailing", update(1, rect(4, 1, 32, 0x0401, planarRLE|planarNoAlpha, 0x13, 0x10, 0x13, 0x20, 0x13, 0x30, 0)), "Draw->decodePlanar." + TagTrailingData},
	}
	for _, tt := range read {
		t.Run(tt.name, func(t *testing.T) {
			screen := newImage(8, 8, 0)
			u, err := ParseUpdate(tt.data)
			for err == nil {
				var d Data
				var ok bool
				d, ok, err = u.Next()
				if err != nil || !ok {
					break
				}
				err = Draw(screen, d)
			}
			assertRoute(t, err, tt.route)
		})
	}
}

func assertRoute(t *testing.T, err error, route string) {
	t.Helper()
	appErr, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("expected *errors.Error, got %T (%v)", err, err)
	}
	if !appErr.HasRoute(route) {
		t.Errorf("route %s, want %s", appErr.Route(), route)
	}
}

// TestHostile draws every cut and many mutations of valid updates, and
// random bytes, without a panic.
func TestHostile(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	var seeds [][]byte
	for _, kind := range []string{"flat", "noise", "mixed", "gradient"} {
		for _, noAlpha := range []bool{true, false} {
			img := newImage(20, 10, 0)
			paint(rng, img, kind)
			var w wire.Writer
			off := BeginUpdate(&w)
			if err := AppendRect(&w, img, Rect{X: 2, Y: 1, Width: 17, Height: 9}, noAlpha); err != nil {
				t.Fatal(err)
			}
			if err := EndUpdate(&w, off, 1); err != nil {
				t.Fatal(err)
			}
			seeds = append(seeds, bytes.Clone(w.Bytes()))
		}
	}
	draw := func(b []byte) {
		screen := newImage(20, 10, 0)
		u, err := ParseUpdate(b)
		for err == nil {
			d, ok, err2 := u.Next()
			if err2 != nil || !ok {
				return
			}
			err = Draw(screen, d)
		}
	}
	for _, s := range seeds {
		for n := range s {
			draw(s[:n])
		}
		for range 5000 {
			m := bytes.Clone(s)
			for range 1 + rng.IntN(4) {
				m[rng.IntN(len(m))] = byte(rng.Uint32())
			}
			draw(m)
		}
	}
	for range 2000 {
		b := make([]byte, rng.IntN(64))
		for i := range b {
			b[i] = byte(rng.Uint32())
		}
		draw(b)
	}
}

func TestAllocations(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	img := newImage(200, 100, 0)
	paint(rng, img, "mixed")
	w := wire.NewWriter(make([]byte, 0, 64<<10))
	allocs := testing.AllocsPerRun(20, func() {
		w.Reset()
		off := BeginUpdate(&w)
		for _, noAlpha := range []bool{true, false} {
			if err := AppendRect(&w, img, Rect{X: 3, Y: 5, Width: 64, Height: 63}, noAlpha); err != nil {
				t.Fatal(err)
			}
		}
		if err := EndUpdate(&w, off, 2); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Errorf("%v allocations", allocs)
	}
}

func BenchmarkAppendRect(b *testing.B) {
	rng := rand.New(rand.NewPCG(9, 10))
	for _, kind := range []string{"flat", "mixed", "noise"} {
		b.Run(kind, func(b *testing.B) {
			img := newImage(64, 63, 0)
			paint(rng, img, kind)
			w := wire.NewWriter(make([]byte, 0, 64<<10))
			b.SetBytes(4 * 64 * 63)
			for b.Loop() {
				w.Reset()
				if err := AppendRect(&w, img, Rect{Width: 64, Height: 63}, true); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
