package rdptest

import "github.com/rm4n0s/mardp/rdp/internal/bitmap"

// Screen is the desktop as the client draws it: 32-bit pixels in the byte
// order blue, green, red, alpha, rows top-down, Stride bytes apart, as in
// rdp.Image.
type Screen struct {
	Pix           []byte
	Stride        int
	Width, Height int
}

// NewScreen returns a screen of the size, all zeros.
func NewScreen(width, height int) *Screen {
	return &Screen{Pix: make([]byte, 4*width*height), Stride: 4 * width, Width: width, Height: height}
}

// DrawBitmaps draws the rectangles of the data of a fast-path Bitmap Update
// (MS-RDPBCGR 2.2.9.1.2.1.2), uncompressed or Planar, and returns how many
// there were. It fails with the bitmap package's tags on a rectangle the
// server should not have sent.
func (sc *Screen) DrawBitmaps(data []byte) (int, error) {
	u, err := bitmap.ParseUpdate(data)
	if err != nil {
		return 0, err
	}
	n := 0
	for {
		d, ok, err := u.Next()
		if err != nil || !ok {
			return n, err
		}
		if err := bitmap.Draw((*bitmap.Image)(sc), d); err != nil {
			return n, err
		}
		n++
	}
}
