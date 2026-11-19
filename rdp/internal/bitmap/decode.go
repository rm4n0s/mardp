package bitmap

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Data is one Bitmap Data (2.2.9.1.1.3.1.2.2) as read: where it goes on the
// screen (Right and Bottom inclusive), its bitmap's size and format, and the
// bitmap itself, which is valid as long as the update it was read from.
type Data struct {
	Left, Top, Right, Bottom uint16
	Width, Height            uint16
	BitsPerPixel             uint16
	Flags                    uint16
	Bitmap                   []byte
}

// Update reads the rectangles of a Bitmap Update Data one at a time.
type Update struct {
	r    wire.Reader
	left int
}

// ParseUpdate starts reading a Bitmap Update Data (2.2.9.1.1.3.1.2.1), the
// data of a fast-path Bitmap Update.
func ParseUpdate(data []byte) (Update, error) {
	r := wire.NewReader(data)
	t, err := r.U16LE()
	if err != nil {
		return Update{}, err
	}
	if t != updateTypeBitmap {
		return Update{}, errors.New(TagUpdateInvalid, "the update is not a bitmap update", "updateType", t)
	}
	n, err := r.U16LE()
	if err != nil {
		return Update{}, err
	}
	return Update{r: r, left: int(n)}, nil
}

// Next returns the next rectangle; ok is false after the last, when the
// update must have nothing left.
func (u *Update) Next() (d Data, ok bool, err error) {
	if u.left == 0 {
		if n := u.r.Remaining(); n != 0 {
			return Data{}, false, errors.New(TagTrailingData, "bytes after the last rectangle", "bytes", n)
		}
		return Data{}, false, nil
	}
	u.left--
	for _, f := range []*uint16{&d.Left, &d.Top, &d.Right, &d.Bottom, &d.Width, &d.Height, &d.BitsPerPixel, &d.Flags} {
		if *f, err = u.r.U16LE(); err != nil {
			return Data{}, false, err
		}
	}
	n, err := u.r.U16LE()
	if err != nil {
		return Data{}, false, err
	}
	if d.Bitmap, err = u.r.Bytes(int(n)); err != nil {
		return Data{}, false, err
	}
	return d, true, nil
}

// Draw draws d into screen: the bitmap, uncompressed or Planar, clipped to
// d's destination. A bitmap larger than MaxArea, which this server never
// sends, is refused. Alpha is 0xFF where the Planar stream has no alpha
// plane.
func Draw(screen *Image, d Data) error {
	w, h := int(d.Width), int(d.Height)
	dw, dh := int(d.Right)-int(d.Left)+1, int(d.Bottom)-int(d.Top)+1
	if w == 0 || h == 0 || w*h > MaxArea || dw <= 0 || dh <= 0 || dw > w || dh > h ||
		int(d.Right) >= screen.Width || int(d.Bottom) >= screen.Height {
		return errors.New(TagRectInvalid, "the rectangle is not inside the screen and its bitmap",
			"left", d.Left, "top", d.Top, "right", d.Right, "bottom", d.Bottom, "width", w, "height", h)
	}
	if d.BitsPerPixel != bitsPerPixel || (d.Flags != 0 && d.Flags != FlagCompression|FlagNoCompressionHeader) {
		return errors.New(TagFormatUnsupported, "the bitmap is not one this server sends",
			"bitsPerPixel", d.BitsPerPixel, "flags", d.Flags)
	}
	t := target{screen: screen, x: int(d.Left), y: int(d.Top), w: dw, h: dh, height: h}
	if d.Flags == 0 {
		if len(d.Bitmap) != 4*w*h {
			return errors.New(TagLengthInvalid, "the bitmap's length is not that of its pixels",
				"length", len(d.Bitmap), "width", w, "height", h)
		}
		for row := range h {
			for col := range w {
				for c := range 4 {
					t.set(row, col, c, d.Bitmap[(row*w+col)*4+c])
				}
			}
		}
		return nil
	}
	return decodePlanar(&t, w, d.Bitmap)
}

// DrawTile draws a tile of the graphics pipeline into screen at x, y: the
// width × height pixels, or a Planar stream, its scan lines top-down
// (AppendTile). The pixels are XRGB_8888, whose fourth byte means
// nothing: alpha is drawn as 0xFF, unless a Planar stream has an alpha
// plane.
func DrawTile(screen *Image, x, y, width, height int, planar bool, data []byte) error {
	if width <= 0 || height <= 0 || width*height > MaxTile || x < 0 || y < 0 || x+width > screen.Width || y+height > screen.Height {
		return errors.New(TagRectInvalid, "the tile is not inside the screen",
			"x", x, "y", y, "width", width, "height", height)
	}
	t := target{screen: screen, x: x, y: y, w: width, h: height, height: height, topDown: true}
	if planar {
		return decodePlanar(&t, width, data)
	}
	if len(data) != 4*width*height {
		return errors.New(TagLengthInvalid, "the tile's length is not that of its pixels",
			"length", len(data), "width", width, "height", height)
	}
	for row := range height {
		line := screen.Pix[(y+row)*screen.Stride+4*x : (y+row)*screen.Stride+4*(x+width)]
		copy(line, data[4*width*row:4*width*(row+1)])
		for i := 3; i < len(line); i += 4 {
			line[i] = 0xff
		}
	}
	return nil
}

// target is where a bitmap of height rows, its scan lines bottom-up or
// top-down, is drawn on the screen: the w×h area at x, y.
type target struct {
	screen     *Image
	x, y, w, h int
	height     int
	topDown    bool
}

// set sets byte c of the pixel at scan line row, column col, when it is
// inside the area.
func (t *target) set(row, col, c int, v byte) {
	y := t.height - 1 - row
	if t.topDown {
		y = row
	}
	if col < t.w && y < t.h {
		t.screen.Pix[(t.y+y)*t.screen.Stride+4*(t.x+col)+c] = v
	}
}

// decodePlanar draws a Planar stream (MS-RDPEGDI 2.2.2.5.1, 3.1.9.4) of
// width pixels per scan line.
func decodePlanar(t *target, width int, b []byte) error {
	r := wire.NewReader(b)
	header, err := r.U8()
	if err != nil {
		return err
	}
	if header&(planarCLL|planarCS|planarReserved) != 0 {
		return errors.New(TagPlanarHeaderInvalid, "a Planar stream this server never sends", "header", header)
	}
	first := 0
	if header&planarNoAlpha != 0 {
		first = 1
		for row := range t.height {
			for col := range width {
				t.set(row, col, 3, 0xff)
			}
		}
	}
	for i := first; i < 4; i++ {
		c := planeChannel[i]
		if c < 0 {
			c = 3
		}
		if header&planarRLE == 0 {
			for row := range t.height {
				for col := range width {
					v, err := r.U8()
					if err != nil {
						return err
					}
					t.set(row, col, c, v)
				}
			}
			continue
		}
		if err := decodeRLE(&r, t, width, c); err != nil {
			return err
		}
	}
	n := r.Remaining()
	if header&planarRLE == 0 && n == 1 {
		n = 0 // the pad byte
	}
	if n != 0 {
		return errors.New(TagTrailingData, "bytes after the last plane", "bytes", n)
	}
	return nil
}

// decodeRLE decodes one run-length encoded plane into byte c of the
// target's pixels (3.1.9.2.3). Values after the first scan line are
// differences from the scan line before, read back from the target; outside
// the target's area they are taken as 0 on both sides, which keeps the
// decoding of what is inside right.
func decodeRLE(r *wire.Reader, t *target, width, c int) error {
	prev := make([]byte, width)
	cur := make([]byte, width)
	for row := range t.height {
		col := 0
		last := byte(0)
		for col < width {
			ctrl, err := r.U8()
			if err != nil {
				return err
			}
			raw, run := int(ctrl>>4), int(ctrl&0x0f)
			switch run {
			case 1:
				run, raw = 16+raw, 0
			case 2:
				run, raw = 32+raw, 0
			}
			if ctrl == 0 || col+raw+run > width {
				return errors.New(TagRLEInvalid, "a segment that is empty or runs past its scan line",
					"control", ctrl, "row", row, "column", col, "width", width)
			}
			for range raw {
				if last, err = r.U8(); err != nil {
					return err
				}
				cur[col] = absolute(row, last, prev[col])
				col++
			}
			for range run {
				cur[col] = absolute(row, last, prev[col])
				col++
			}
		}
		for col, v := range cur {
			t.set(row, col, c, v)
		}
		prev, cur = cur, prev
	}
	return nil
}

// absolute turns a value of scan line row back into the plane's value: on
// the first scan line it is the value, then a difference from prev, the
// low bit giving its sign.
func absolute(row int, v, prev byte) byte {
	if row == 0 {
		return v
	}
	if v&1 != 0 {
		return prev + 0xff - (v-1)>>1
	}
	return prev + v>>1
}
