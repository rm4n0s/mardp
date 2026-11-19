package rdptest

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/bitmap"
	"github.com/rm4n0s/mardp/rdp/internal/gfx"
)

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

// TagGFXUnsupported: a Wire To Surface 1 that the test client does not
// draw: another surface than the desktop's, another pixel format than
// XRGB_8888, or another codec than Uncompressed and Planar.
const TagGFXUnsupported = "ClientGFXUnsupported"

// GFXDrawn is what DrawGFX drew.
type GFXDrawn struct {
	// Tiles is the number of Wire To Surface 1 PDUs drawn; Planar is how
	// many of them were Planar.
	Tiles, Planar int
	// Ended are the IDs of the frames that ended.
	Ended []uint32
}

// DrawGFX draws the Wire To Surface 1 PDUs of ps on the screen, as the
// desktop's surface 1 mapped at 0, 0, and notes the frames that end; other
// PDUs are skipped. It fails with TagGFXUnsupported, or with the bitmap
// package's tags on a tile the server should not have sent.
func (sc *Screen) DrawGFX(ps []gfx.PDU) (GFXDrawn, error) {
	var d GFXDrawn
	for _, p := range ps {
		switch p.Cmd {
		case gfx.CmdEndFrame:
			d.Ended = append(d.Ended, p.FrameID)
		case gfx.CmdWireToSurface1:
			if p.SurfaceID != 1 || p.PixelFormat != gfx.PixelFormatXRGB ||
				(p.CodecID != gfx.CodecUncompressed && p.CodecID != gfx.CodecPlanar) {
				return d, errors.New(TagGFXUnsupported, "a Wire To Surface 1 the test client does not draw",
					"surface", p.SurfaceID, "format", p.PixelFormat, "codec", p.CodecID)
			}
			planar := p.CodecID == gfx.CodecPlanar
			x, y := int(p.Rect.Left), int(p.Rect.Top)
			err := bitmap.DrawTile((*bitmap.Image)(sc), x, y, int(p.Rect.Right)-x, int(p.Rect.Bottom)-y, planar, p.Body)
			if err != nil {
				return d, err
			}
			d.Tiles++
			if planar {
				d.Planar++
			}
		}
	}
	return d, nil
}
