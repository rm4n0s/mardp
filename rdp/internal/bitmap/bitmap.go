// Package bitmap writes the screen as Bitmap Updates (MS-RDPBCGR
// 2.2.9.1.1.3.1.2): rectangles of 32-bit pixels, compressed with RDP 6.0
// Bitmap Compression, the Planar codec (MS-RDPEGDI 2.2.2.5.1, 3.1.9), or
// uncompressed when that is smaller. It reads them too, for rdptest.
//
// The encoder is lossless: the ARGB color space (color loss level 0), no
// chroma subsampling, and the planes run-length encoded when that makes
// them smaller. Writing allocates nothing.
package bitmap

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors this package returns.
const (
	// TagAreaInvalid: an area to write that is empty, larger than a
	// Bitmap Data carries, or not inside its image.
	TagAreaInvalid = "BitmapAreaInvalid"
	// TagUpdateInvalid: a Bitmap Update Data whose updateType is not
	// UPDATETYPE_BITMAP.
	TagUpdateInvalid = "BitmapUpdateInvalid"
	// TagRectInvalid: a Bitmap Data whose destination is not inside the
	// screen or larger than its bitmap, or whose bitmap is larger than
	// MaxArea.
	TagRectInvalid = "BitmapRectInvalid"
	// TagFormatUnsupported: a Bitmap Data that is not 32 bits per pixel,
	// or whose compression is not the one this server writes.
	TagFormatUnsupported = "BitmapFormatUnsupported"
	// TagLengthInvalid: an uncompressed bitmap whose length is not that of
	// its pixels.
	TagLengthInvalid = "BitmapLengthInvalid"
	// TagPlanarHeaderInvalid: a Planar stream with a color loss level,
	// chroma subsampling or reserved bits, which this server never sends.
	TagPlanarHeaderInvalid = "BitmapPlanarHeaderInvalid"
	// TagRLEInvalid: a run-length segment whose control byte is zero, or
	// which runs past the end of its scan line.
	TagRLEInvalid = "BitmapRLEInvalid"
	// TagTrailingData: bytes after the last plane, or after the last
	// rectangle of an update.
	TagTrailingData = "BitmapTrailingData"
)

// Image is 32-bit pixels in the byte order blue, green, red, alpha (how
// macOS captures the screen), rows top-down, Stride bytes apart.
type Image struct {
	Pix           []byte
	Stride        int
	Width, Height int
}

// Rect is an area of an image, and the same area of the screen.
type Rect struct {
	X, Y, Width, Height int
}

// Bitmap Data flags (2.2.9.1.1.3.1.2.2).
const (
	FlagCompression         uint16 = 0x0001
	FlagNoCompressionHeader uint16 = 0x0400
)

// updateTypeBitmap is UPDATETYPE_BITMAP.
const updateTypeBitmap = 0x0001

// bitsPerPixel is the only color depth this server sends (SPEC §5.2).
const bitsPerPixel = 32

// The bits of the Planar FormatHeader (MS-RDPEGDI 2.2.2.5.1).
const (
	planarCLL      = 0x07
	planarCS       = 0x08
	planarRLE      = 0x10
	planarNoAlpha  = 0x20
	planarReserved = 0xc0
)

// MaxArea is the most pixels one rectangle holds, so that it fits in a
// Bitmap Data (bitmapLength is 16 bits) and in one fast-path PDU whatever
// its compression; RectSize(MaxArea) is the most bytes it takes.
const MaxArea = 4032

// MaxTile is the most pixels a tile of the graphics pipeline holds
// (AppendTile): 64×64, the tiles Windows sends.
const MaxTile = 64 * 64

// rectHeaderLen is a Bitmap Data without its bitmap.
const rectHeaderLen = 18

// UpdateHeaderLen is a Bitmap Update Data without its rectangles.
const UpdateHeaderLen = 4

// RectSize is the most bytes AppendRect writes for an area of n pixels:
// the uncompressed size, which it never exceeds.
func RectSize(n int) int { return rectHeaderLen + 4*n }

// BeginUpdate appends the header of a Bitmap Update Data (2.2.9.1.1.3.1.2.1),
// the data of a fast-path Bitmap Update, and returns its offset for
// EndUpdate.
func BeginUpdate(w *wire.Writer) int {
	off := w.Len()
	w.U16LE(updateTypeBitmap)
	w.U16LE(0)
	return off
}

// EndUpdate sets the number of rectangles of the update that BeginUpdate
// started at off.
func EndUpdate(w *wire.Writer, off int, rects uint16) error {
	return w.PatchU16LE(off+2, rects)
}

// AppendRect appends one Bitmap Data for the area r of img, placed at r on
// the screen: Planar when that is smaller than the pixels, otherwise the
// pixels themselves. With noAlpha (the client's DRAW_ALLOW_SKIP_ALPHA) the
// Planar stream has no alpha plane; without it, the alpha plane says 0xFF,
// since the screen is opaque.
func AppendRect(w *wire.Writer, img *Image, r Rect, noAlpha bool) error {
	if r.Width <= 0 || r.Height <= 0 || r.Width*r.Height > MaxArea || r.X < 0 || r.Y < 0 ||
		r.X+r.Width > img.Width || r.Y+r.Height > img.Height || img.Stride < 4*img.Width ||
		len(img.Pix) < (img.Height-1)*img.Stride+4*img.Width {
		return errors.New(TagAreaInvalid, "the area is not one rectangle of the image",
			"x", r.X, "y", r.Y, "width", r.Width, "height", r.Height,
			"imageWidth", img.Width, "imageHeight", img.Height, "stride", img.Stride, "len", len(img.Pix))
	}
	w.U16LE(uint16(r.X))
	w.U16LE(uint16(r.Y))
	w.U16LE(uint16(r.X + r.Width - 1))
	w.U16LE(uint16(r.Y + r.Height - 1))
	w.U16LE(uint16(r.Width))
	w.U16LE(uint16(r.Height))
	w.U16LE(bitsPerPixel)
	flags := w.Reserve(2)
	length := w.Reserve(2)
	start := w.Len()
	raw := 4 * r.Width * r.Height
	f := FlagCompression | FlagNoCompressionHeader
	if !appendPlanar(w, img, r, noAlpha, false, raw) {
		w.Truncate(start)
		appendRaw(w, img, r)
		f = 0
	}
	if err := w.PatchU16LE(flags, f); err != nil {
		return err
	}
	return w.PatchU16LE(length, uint16(w.Len()-start))
}

// appendRaw appends the pixels of r uncompressed: bottom-up, left to right
// (2.2.9.1.1.3.1.2.2); a row of 32-bit pixels needs no padding.
func appendRaw(w *wire.Writer, img *Image, r Rect) {
	for row := r.Y + r.Height - 1; row >= r.Y; row-- {
		off := row*img.Stride + 4*r.X
		w.Write(img.Pix[off : off+4*r.Width])
	}
}

// AppendTile appends the area r of img for a Wire To Surface of the
// graphics pipeline (MS-RDPEGFX 2.2.2.1), its scan lines top-down: a
// Planar stream without an alpha plane when that is smaller than the
// pixels, otherwise the pixels themselves, whose alpha the client ignores.
// It reports whether it wrote Planar.
func AppendTile(w *wire.Writer, img *Image, r Rect) (bool, error) {
	if r.Width <= 0 || r.Height <= 0 || r.Width*r.Height > MaxTile || r.X < 0 || r.Y < 0 ||
		r.X+r.Width > img.Width || r.Y+r.Height > img.Height || img.Stride < 4*img.Width ||
		len(img.Pix) < (img.Height-1)*img.Stride+4*img.Width {
		return false, errors.New(TagAreaInvalid, "the area is not one tile of the image",
			"x", r.X, "y", r.Y, "width", r.Width, "height", r.Height,
			"imageWidth", img.Width, "imageHeight", img.Height, "stride", img.Stride, "len", len(img.Pix))
	}
	start := w.Len()
	if appendPlanar(w, img, r, true, true, 4*r.Width*r.Height) {
		return true, nil
	}
	w.Truncate(start)
	for row := r.Y; row < r.Y+r.Height; row++ {
		off := row*img.Stride + 4*r.X
		w.Write(img.Pix[off : off+4*r.Width])
	}
	return false, nil
}

// appendPlanar appends the Planar stream of r and reports whether it is
// smaller than limit bytes: the planes run-length encoded when that is
// smaller than the planes themselves. The scan lines go bottom-up, as in an
// uncompressed bitmap, or top-down for the graphics pipeline.
func appendPlanar(w *wire.Writer, img *Image, r Rect, noAlpha, topDown bool, limit int) bool {
	planes := 4
	header := byte(planarRLE)
	if noAlpha {
		planes = 3
		header |= planarNoAlpha
	}
	start := w.Len()
	w.U8(header)
	p := plane{img: img, r: r, topDown: topDown}
	for i := 4 - planes; i < 4; i++ {
		p.c = planeChannel[i]
		p.appendRLE(w)
	}
	// Without RLE the planes take their pixels and a pad byte.
	plain := 1 + planes*r.Width*r.Height + 1
	if w.Len()-start <= plain {
		return w.Len()-start < limit
	}
	if plain >= limit {
		return false
	}
	w.Truncate(start)
	w.U8(header &^ planarRLE)
	for i := 4 - planes; i < 4; i++ {
		p.c = planeChannel[i]
		for row := range r.Height {
			for col := range r.Width {
				w.U8(p.at(row, col))
			}
		}
	}
	w.U8(0)
	return true
}

// planeChannel is the byte of a pixel each plane takes, in the order of the
// stream: alpha, red, green, blue. Alpha, -1, is 0xFF everywhere.
var planeChannel = [4]int{-1, 2, 1, 0}

// plane is one color plane of an area of an image, its scan lines
// bottom-up or top-down.
type plane struct {
	img     *Image
	r       Rect
	c       int
	topDown bool
}

// at is the plane's value in scan line row, column col.
func (p *plane) at(row, col int) byte {
	if p.c < 0 {
		return 0xff
	}
	y := p.r.Y + p.r.Height - 1 - row
	if p.topDown {
		y = p.r.Y + row
	}
	return p.img.Pix[y*p.img.Stride+4*(p.r.X+col)+p.c]
}

// value is what the run-length encoder takes for scan line row, column col
// (MS-RDPEGDI 3.1.9.2.1): the value itself on the first scan line, then the
// difference from the scan line before, made positive and shifted left, the
// low bit set for a negative one.
func (p *plane) value(row, col int) byte {
	v := p.at(row, col)
	if row == 0 {
		return v
	}
	d := v - p.at(row-1, col)
	if d < 0x80 {
		return d << 1
	}
	return (-d)<<1 - 1
}

// appendRLE appends the plane run-length encoded (3.1.9.2), each scan line
// in segments of its own.
func (p *plane) appendRLE(w *wire.Writer) {
	var buf [MaxTile]byte
	line := buf[:p.r.Width]
	for row := range p.r.Height {
		for col := range line {
			line[col] = p.value(row, col)
		}
		appendLine(w, line)
	}
}

// appendLine appends the segments of one scan line of values. A run is a
// value repeated after a raw byte that has it, so a run of n values takes
// the first as raw and repeats it n-1 times, which a segment allows from 3
// on. At the start of the scan line, where the value before is 0, a run of
// 0s needs no raw byte.
func appendLine(w *wire.Writer, line []byte) {
	last := byte(0)
	raw := 0
	for i := 0; i < len(line); {
		v := line[i]
		n := 1
		for i+n < len(line) && line[i+n] == v {
			n++
		}
		switch {
		case v == last && n >= 3:
			appendSegments(w, line[raw:i], n)
			i += n
			raw = i
		case n >= 4:
			appendSegments(w, line[raw:i+1], n-1)
			i += n
			raw = i
			last = v
		default:
			i += n
			last = v
		}
	}
	if raw < len(line) {
		appendSegments(w, line[raw:], 0)
	}
}

// appendSegments appends raw values, then a run of n of the last one:
// segments of at most 15 raw values, and runs of more than 15 as segments
// with no raw values, which carry 16 to 47 (2.2.2.5.1.2, 3.1.9.2.2). No
// piece of a run is left at 1 or 2, which no segment holds.
func appendSegments(w *wire.Writer, raw []byte, n int) {
	for len(raw) > 15 {
		w.U8(15 << 4)
		w.Write(raw[:15])
		raw = raw[15:]
	}
	if len(raw) > 0 {
		c := 0
		if n > 0 {
			c = split(n, 15)
		}
		w.U8(byte(len(raw))<<4 | byte(c))
		w.Write(raw)
		n -= c
	}
	for n > 0 {
		c := split(n, 47)
		switch {
		case c >= 32:
			w.U8(byte(c-32)<<4 | 2)
		case c >= 16:
			w.U8(byte(c-16)<<4 | 1)
		default:
			w.U8(byte(c))
		}
		n -= c
	}
}

// split is how much of a run of n to take in a segment that holds at most
// limit, leaving no remainder of 1 or 2.
func split(n, limit int) int {
	c := min(n, limit)
	if n-c == 1 || n-c == 2 {
		c -= 3
	}
	return c
}
