// Package pointer writes the pointer updates of MS-RDPBCGR 2.2.9.1.1.4 and
// 2.2.9.1.2.1 (position, hidden, default, cached, color, new and large
// pointers), shrinks shapes that are too large for the client, and reads
// the updates back for rdptest.
//
// Shapes are 32-bit pixels with alpha. A New or Large Pointer Update
// carries them as they are, as a 32 bpp XOR mask; a Color Pointer Update,
// for a client without the New Pointer Update, carries 24 bpp, opaque where
// alpha is at least half. Where a pixel is transparent the XOR pixel is 0
// and the AND bit is 1, so that the screen shows through whether the client
// reads the alpha or the AND mask. Writing allocates nothing.
package pointer

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/fastpath"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors this package returns.
const (
	// TagCodeUnknown: a fast-path update that is not a pointer update.
	TagCodeUnknown = "PointerCodeUnknown"
	// TagShapeInvalid: a shape whose size, color depth or mask lengths are
	// not what the update allows, or with a pixel that inverts the screen,
	// which this server never sends.
	TagShapeInvalid = "PointerShapeInvalid"
	// TagTrailingData: bytes after the end of a pointer update.
	TagTrailingData = "PointerTrailingData"
)

// The largest pointer sides: without the Large Pointer Capability Set, with
// LARGE_POINTER_FLAG_96x96, and in a Large Pointer Update
// (2.2.7.2.7, 2.2.9.1.1.4.4, 2.2.9.1.2.1.11).
const (
	MaxSide      = 32
	MaxSide96    = 96
	MaxSideLarge = 384
)

// Shape is a pointer image: 32-bit pixels in the byte order blue, green,
// red, alpha, rows top-down, Stride bytes apart, and its hotspot.
type Shape struct {
	Pix           []byte
	Stride        int
	Width, Height int
	HotX, HotY    int
}

// pixel is the pixel at x, y.
func (s *Shape) pixel(x, y int) []byte {
	off := y*s.Stride + 4*x
	return s.Pix[off : off+4]
}

// The lengths of a shape's masks: scan lines padded to 2 bytes
// (2.2.9.1.1.4.4).
func xorLen(width, height, bpp int) int { return (width*bpp/8 + 1) &^ 1 * height }
func andLen(width, height int) int      { return ((width+7)/8 + 1) &^ 1 * height }

// UpdateSize is the length of the update AppendNew or AppendLarge writes for
// a w×h shape.
func UpdateSize(w, h int, large bool) int {
	n := 2 + 2 + 4 + 2 + 2 + 2 + 2 + xorLen(w, h, 32) + andLen(w, h)
	if large {
		n += 4
	}
	return n
}

// AppendPosition appends a Pointer Position Update (2.2.9.1.1.4.2), the data
// of FASTPATH_UPDATETYPE_PTR_POSITION.
func AppendPosition(w *wire.Writer, x, y uint16) {
	w.U16LE(x)
	w.U16LE(y)
}

// AppendCached appends a Cached Pointer Update (2.2.9.1.1.4.6), the data of
// FASTPATH_UPDATETYPE_CACHED.
func AppendCached(w *wire.Writer, index uint16) { w.U16LE(index) }

// AppendNew appends a New Pointer Update (2.2.9.1.1.4.5) of s at 32 bpp, to
// be cached at index: the data of FASTPATH_UPDATETYPE_POINTER. s is at most
// MaxSide96 on each side.
func AppendNew(w *wire.Writer, s *Shape, index uint16) {
	w.U16LE(32)
	appendColor(w, s, index, 32, false)
}

// AppendLarge appends a Large Pointer Update (2.2.9.1.2.1.11) of s at
// 32 bpp, to be cached at index, without its fast-path header. s is at
// most MaxSideLarge on each side.
func AppendLarge(w *wire.Writer, s *Shape, index uint16) {
	w.U16LE(32)
	appendColor(w, s, index, 32, true)
}

// AppendColor appends a Color Pointer Update (2.2.9.1.1.4.4) of s at 24 bpp,
// to be cached at index: the data of FASTPATH_UPDATETYPE_COLOR. s is at most
// MaxSide96 on each side.
func AppendColor(w *wire.Writer, s *Shape, index uint16) { appendColor(w, s, index, 24, false) }

// appendColor appends the fields of a Color Pointer Update with the XOR
// mask at bpp, or those of a Large Pointer Update after its xorBpp.
func appendColor(w *wire.Writer, s *Shape, index uint16, bpp int, large bool) {
	w.U16LE(index)
	w.U16LE(uint16(s.HotX))
	w.U16LE(uint16(s.HotY))
	w.U16LE(uint16(s.Width))
	w.U16LE(uint16(s.Height))
	xl, al := xorLen(s.Width, s.Height, bpp), andLen(s.Width, s.Height)
	if large {
		w.U32LE(uint32(al))
		w.U32LE(uint32(xl))
	} else {
		w.U16LE(uint16(al))
		w.U16LE(uint16(xl))
	}
	row := xl / s.Height
	for y := s.Height - 1; y >= 0; y-- {
		n := 0
		for x := range s.Width {
			p := s.pixel(x, y)
			switch {
			case bpp == 32 && p[3] != 0:
				w.Write(p)
			case bpp == 24 && p[3] >= 0x80:
				w.Write(p[:3])
			default:
				w.Zeros(bpp / 8)
			}
			n += bpp / 8
		}
		w.Zeros(row - n)
	}
	row = al / s.Height
	for y := s.Height - 1; y >= 0; y-- {
		var b byte
		n := 0
		for x := range s.Width {
			p := s.pixel(x, y)
			if (bpp == 32 && p[3] == 0) || (bpp == 24 && p[3] < 0x80) {
				b |= 0x80 >> (x % 8)
			}
			if x%8 == 7 || x == s.Width-1 {
				w.U8(b)
				b = 0
				n++
			}
		}
		w.Zeros(row - n)
	}
}

// Shrink returns s made small enough that neither side is more than side:
// each block of k×k pixels, k the smallest factor that does it, averaged
// with its alpha into one pixel of dst, which must hold the result. A shape
// that fits is returned as it is.
func Shrink(dst []byte, s *Shape, side int) Shape {
	k := (max(s.Width, s.Height) + side - 1) / side
	if k <= 1 {
		return *s
	}
	out := Shape{Width: (s.Width + k - 1) / k, Height: (s.Height + k - 1) / k, HotX: s.HotX / k, HotY: s.HotY / k}
	out.Stride = 4 * out.Width
	out.Pix = dst[:out.Stride*out.Height]
	for oy := range out.Height {
		for ox := range out.Width {
			var sum [4]int
			n := 0
			for y := oy * k; y < min((oy+1)*k, s.Height); y++ {
				for x := ox * k; x < min((ox+1)*k, s.Width); x++ {
					p := s.pixel(x, y)
					a := int(p[3])
					sum[0] += int(p[0]) * a
					sum[1] += int(p[1]) * a
					sum[2] += int(p[2]) * a
					sum[3] += a
					n++
				}
			}
			p := out.pixel(ox, oy)
			p[3] = byte(sum[3] / n)
			for c := range 3 {
				p[c] = 0
				if sum[3] > 0 {
					p[c] = byte(sum[c] / sum[3])
				}
			}
		}
	}
	return out
}

// Kind is what a pointer update does.
type Kind int

// The kinds of pointer update.
const (
	KindHidden Kind = iota + 1
	KindDefault
	KindPosition
	KindCached
	KindShape
)

// Update is a pointer update as read.
type Update struct {
	Kind Kind
	// X and Y are the position of KindPosition.
	X, Y int
	// Index is the cache entry of KindCached and KindShape.
	Index int
	// Shape is the shape of KindShape, its pixels made from the masks:
	// transparent pixels are 0, and a 24 bpp shape is opaque elsewhere.
	Shape Shape
	// BitsPerPixel is the XOR mask's color depth of KindShape, and Large
	// says it came in a Large Pointer Update.
	BitsPerPixel int
	Large        bool
}

// Parse reads the data of a fast-path pointer update of type code. The
// shape's pixels are allocated.
func Parse(code uint8, data []byte) (Update, error) {
	r := wire.NewReader(data)
	var u Update
	switch code {
	case fastpath.UpdatePointerHidden:
		u.Kind = KindHidden
	case fastpath.UpdatePointerDefault:
		u.Kind = KindDefault
	case fastpath.UpdatePointerPosition:
		u.Kind = KindPosition
		x, err := r.U16LE()
		if err != nil {
			return Update{}, err
		}
		y, err := r.U16LE()
		if err != nil {
			return Update{}, err
		}
		u.X, u.Y = int(x), int(y)
	case fastpath.UpdateCachedPointer:
		u.Kind = KindCached
		i, err := r.U16LE()
		if err != nil {
			return Update{}, err
		}
		u.Index = int(i)
	case fastpath.UpdateColorPointer, fastpath.UpdateNewPointer, fastpath.UpdateLargePointer:
		u.Kind = KindShape
		u.Large = code == fastpath.UpdateLargePointer
		u.BitsPerPixel = 24
		if code != fastpath.UpdateColorPointer {
			bpp, err := r.U16LE()
			if err != nil {
				return Update{}, err
			}
			u.BitsPerPixel = int(bpp)
		}
		if err := readShape(&r, &u); err != nil {
			return Update{}, err
		}
	default:
		return Update{}, errors.New(TagCodeUnknown, "not a pointer update", "code", code)
	}
	if n := r.Remaining(); n != 0 {
		return Update{}, errors.New(TagTrailingData, "bytes after the pointer update", "code", code, "bytes", n)
	}
	return u, nil
}

// readShape reads the fields of a Color Pointer Update, or of a Large
// Pointer Update after its xorBpp, into u.
func readShape(r *wire.Reader, u *Update) error {
	var f [5]uint16
	for i := range f {
		v, err := r.U16LE()
		if err != nil {
			return err
		}
		f[i] = v
	}
	var al, xl int
	if u.Large {
		a, err := r.U32LE()
		if err != nil {
			return err
		}
		x, err := r.U32LE()
		if err != nil {
			return err
		}
		al, xl = int(a), int(x)
	} else {
		a, err := r.U16LE()
		if err != nil {
			return err
		}
		x, err := r.U16LE()
		if err != nil {
			return err
		}
		al, xl = int(a), int(x)
	}
	s := Shape{HotX: int(f[1]), HotY: int(f[2]), Width: int(f[3]), Height: int(f[4])}
	u.Index = int(f[0])
	side := MaxSide96
	if u.Large {
		side = MaxSideLarge
	}
	bpp := u.BitsPerPixel
	if s.Width == 0 || s.Height == 0 || s.Width > side || s.Height > side || (bpp != 24 && bpp != 32) ||
		xl != xorLen(s.Width, s.Height, bpp) || al != andLen(s.Width, s.Height) || s.HotX >= s.Width || s.HotY >= s.Height {
		return errors.New(TagShapeInvalid, "a shape this server never sends",
			"width", s.Width, "height", s.Height, "bpp", bpp, "andLength", al, "xorLength", xl, "hotX", s.HotX, "hotY", s.HotY)
	}
	xor, err := r.Bytes(xl)
	if err != nil {
		return err
	}
	and, err := r.Bytes(al)
	if err != nil {
		return err
	}
	s.Stride = 4 * s.Width
	s.Pix = make([]byte, s.Stride*s.Height)
	xrow, arow := xl/s.Height, al/s.Height
	for row := range s.Height {
		y := s.Height - 1 - row
		for x := range s.Width {
			src := xor[row*xrow+x*bpp/8:][:bpp/8]
			transparent := and[row*arow+x/8]&(0x80>>(x%8)) != 0
			p := s.pixel(x, y)
			if transparent {
				for _, v := range src {
					if v != 0 {
						return errors.New(TagShapeInvalid, "a pixel that inverts the screen", "x", x, "y", y)
					}
				}
				continue
			}
			copy(p, src)
			if bpp == 24 {
				p[3] = 0xff
			}
		}
	}
	u.Shape = s
	return nil
}
