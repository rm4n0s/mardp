// Command icon draws mardp's app icon (SPEC §16) into a PNG file, for
// scripts/build-app.sh: a screen on a stand on a blue rounded square, in
// the size and margins of macOS's app icons.
//
//	go run ./scripts/icon <file.png>
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
)

// size is the icon's size; macOS scales it down to its other sizes.
const size = 1024

// samples is how many samples a pixel's side has, for smooth edges.
const samples = 4

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/icon <file.png>")
		os.Exit(2)
	}
	if err := write(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "icon:", err)
		os.Exit(1)
	}
}

// write draws the icon into the file at path.
func write(path string) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	return encode(f)
}

// encode writes the icon as a PNG to w.
func encode(w io.Writer) error {
	return png.Encode(w, draw())
}

// roundRect says whether (x, y) is inside the rectangle from (x0, y0) to
// (x1, y1) with corners of radius r.
func roundRect(x, y, x0, y0, x1, y1, r float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	cx := math.Max(x0+r, math.Min(x, x1-r))
	cy := math.Max(y0+r, math.Min(y, y1-r))
	return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r
}

// layer is a shape and its color at a point.
type layer struct {
	in  func(x, y float64) bool
	col func(x, y float64) color.NRGBA
}

func solid(c color.NRGBA) func(x, y float64) color.NRGBA {
	return func(float64, float64) color.NRGBA { return c }
}

// vertical is a gradient from top, at y0, to bottom, at y1.
func vertical(top, bottom color.NRGBA, y0, y1 float64) func(x, y float64) color.NRGBA {
	return func(_, y float64) color.NRGBA {
		t := math.Max(0, math.Min(1, (y-y0)/(y1-y0)))
		mix := func(a, b uint8) uint8 { return uint8(math.Round(float64(a) + t*(float64(b)-float64(a)))) }
		return color.NRGBA{mix(top.R, bottom.R), mix(top.G, bottom.G), mix(top.B, bottom.B), 0xFF}
	}
}

// layers are the icon's shapes, the lowest first.
func layers() []layer {
	white := color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}
	// The body of a macOS icon is 824 of 1024 with corners of 185.
	const b0, b1 = 100, 924
	// The screen and its frame.
	const sx0, sy0, sx1, sy1, frame = 232, 280, 792, 640, 30
	return []layer{
		{func(x, y float64) bool { return roundRect(x, y, b0, b0, b1, b1, 185) },
			vertical(color.NRGBA{0x4A, 0x90, 0xE2, 0xFF}, color.NRGBA{0x1C, 0x3F, 0x8C, 0xFF}, b0, b1)},
		{func(x, y float64) bool { return roundRect(x, y, sx0, sy0, sx1, sy1, 40) }, solid(white)},
		{func(x, y float64) bool { return roundRect(x, y, sx0+frame, sy0+frame, sx1-frame, sy1-frame, 14) },
			vertical(color.NRGBA{0x12, 0x2A, 0x55, 0xFF}, color.NRGBA{0x0A, 0x17, 0x30, 0xFF}, sy0, sy1)},
		// A pointer on the screen: the remote hand.
		{pointer(455, 370, 1.6), solid(white)},
		// The stand and its foot.
		{func(x, y float64) bool { return x >= 482 && x <= 542 && y >= sy1 && y <= 720 }, solid(white)},
		{func(x, y float64) bool { return roundRect(x, y, 382, 712, 642, 750, 19) }, solid(white)},
	}
}

// pointer is a mouse pointer with its tip at (tx, ty), scale times its
// 100-pixel shape.
func pointer(tx, ty, scale float64) func(x, y float64) bool {
	// The outline, from the tip, clockwise.
	pts := [][2]float64{{0, 0}, {0, 100}, {24, 78}, {40, 114}, {58, 106}, {42, 72}, {74, 72}}
	return func(x, y float64) bool {
		px, py := (x-tx)/scale, (y-ty)/scale
		in := false
		for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
			a, b := pts[i], pts[j]
			if (a[1] > py) != (b[1] > py) && px < (b[0]-a[0])*(py-a[1])/(b[1]-a[1])+a[0] {
				in = !in
			}
		}
		return in
	}
}

// draw renders the layers, each pixel the mean of samples×samples points.
func draw() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	ls := layers()
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					x := float64(px) + (float64(sx)+0.5)/samples
					y := float64(py) + (float64(sy)+0.5)/samples
					var c color.NRGBA
					for _, l := range ls {
						if l.in(x, y) {
							c = l.col(x, y)
						}
					}
					al := float64(c.A) / 255
					r, g, b, a = r+float64(c.R)*al, g+float64(c.G)*al, b+float64(c.B)*al, a+al
				}
			}
			if a == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{uint8(math.Round(r / a)), uint8(math.Round(g / a)), uint8(math.Round(b / a)),
				uint8(math.Round(a / (samples * samples) * 255))})
		}
	}
	return img
}
