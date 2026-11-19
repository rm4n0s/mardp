package rdp

import (
	"image"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/bitmap"
	"github.com/rm4n0s/mardp/rdp/internal/fastpath"
	"github.com/rm4n0s/mardp/rdp/internal/gfx"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// TagImageInvalid: an image whose pixels do not hold its size and stride.
const TagImageInvalid = "VideoImageInvalid"

// Image is a picture of the desktop: 32-bit pixels in the byte order blue,
// green, red, alpha (how macOS captures the screen), rows top-down, Stride
// bytes apart. Its top left corner is the desktop's.
type Image struct {
	Pix           []byte
	Stride        int
	Width, Height int
}

// VideoPath is how a session sends the desktop (SPEC §9.1).
type VideoPath int

const (
	// VideoBitmaps is fast-path Bitmap Updates of the changed rectangles,
	// for a client without the graphics pipeline.
	VideoBitmaps VideoPath = iota + 1
	// VideoPlanar is Planar of the changed rectangles through the graphics
	// pipeline, for a client that does not take H.264.
	VideoPlanar
	// VideoAVC420 is H.264 frames through the graphics pipeline.
	VideoAVC420
)

func (p VideoPath) String() string {
	switch p {
	case VideoBitmaps:
		return "bitmaps"
	case VideoPlanar:
		return "planar"
	case VideoAVC420:
		return "avc420"
	}
	return "unknown"
}

// Video is a session's video output (SPEC §6). Its methods may be called
// from any goroutine.
type Video struct {
	s *Session
}

// Video returns the session's video output.
func (s *Session) Video() *Video { return &s.video }

// Path returns how the session sends the desktop (SPEC §9.1): once the
// graphics pipeline's surface is created, H.264 when the client takes it
// and Planar when not, otherwise bitmaps. SendFrame works on VideoAVC420
// only; SendRects on every path.
func (v *Video) Path() VideoPath {
	if g := v.s.gfx; g != nil {
		return g.path()
	}
	return VideoBitmaps
}

// The tiles a rectangle is cut into for Bitmap Updates: each fits in one
// Bitmap Data and in one fast-path PDU whatever its compression.
const (
	tileWidth  = 64
	tileHeight = bitmap.MaxArea / tileWidth
)

// gfxTile is the side of the tiles a rectangle is cut into for the
// graphics pipeline.
const gfxTile = 64

// SendRects sends the rectangles rects of img, which have changed, without
// waiting for the network (SPEC §9.6). Once the graphics pipeline is ready
// they go as one frame of Wire To Surface 1 PDUs, tiles of 64×64 in Planar
// or uncompressed when that is not smaller; otherwise as tiles of 64×63
// compressed the same way, in Bitmap Updates as large as the client takes.
// Rectangles are clipped to the image and the desktop. All of them go in
// one packet of the video queue, so that the client gets the whole change
// or, when the queue is full (TagQueueFull), none of it. img is not used
// after SendRects returns.
func (v *Video) SendRects(img *Image, rects []image.Rectangle) error {
	s := v.s
	if img == nil || img.Width <= 0 || img.Height <= 0 || img.Stride < 4*img.Width ||
		len(img.Pix) < (img.Height-1)*img.Stride+4*img.Width {
		var w, h, stride, n int
		if img != nil {
			w, h, stride, n = img.Width, img.Height, img.Stride, len(img.Pix)
		}
		return errors.New(TagImageInvalid, "the image's pixels do not hold its size",
			"width", w, "height", h, "stride", stride, "len", n)
	}
	bounds := image.Rect(0, 0, min(img.Width, s.width), min(img.Height, s.height))
	some := false
	for _, r := range rects {
		if !r.Intersect(bounds).Empty() {
			some = true
			break
		}
	}
	if !some {
		return nil
	}
	b := (*bitmap.Image)(img)
	if g := s.gfx; g != nil {
		if sent, err := g.sendTiles(b, rects, bounds); sent {
			return err
		}
	}
	if !s.client.FastPathOutput {
		return errors.New(TagFastPathUnsupported, "the client did not confirm fast-path output", "code", fastpath.UpdateBitmap)
	}
	noAlpha := s.client.SkipAlpha
	limit := fastpath.MaxUpdate(s.client.MaxRequestSize)
	return s.wr.send(queueVideo, func(p *packet) error {
		return appendBitmaps(&p.w, &p.tmp, b, rects, bounds, noAlpha, limit)
	})
}

// appendTiles appends to w a Wire To Surface 1 for each tile of rects,
// clipped to bounds.
func appendTiles(w *wire.Writer, img *bitmap.Image, rects []image.Rectangle, bounds image.Rectangle) error {
	for _, r := range rects {
		r = r.Intersect(bounds)
		for y := r.Min.Y; y < r.Max.Y; y += gfxTile {
			for x := r.Min.X; x < r.Max.X; x += gfxTile {
				t := bitmap.Rect{X: x, Y: y, Width: min(gfxTile, r.Max.X-x), Height: min(gfxTile, r.Max.Y-y)}
				off := gfx.BeginWireToSurface1(w, gfxSurfaceID, gfx.PixelFormatXRGB,
					gfx.Rect16{Left: uint16(t.X), Top: uint16(t.Y), Right: uint16(t.X + t.Width), Bottom: uint16(t.Y + t.Height)})
				planar, err := bitmap.AppendTile(w, img, t)
				if err != nil {
					return err
				}
				codec := gfx.CodecUncompressed
				if planar {
					codec = gfx.CodecPlanar
				}
				if err := gfx.EndWireToSurface1(w, off, codec); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// appendBitmaps appends to w the Bitmap Updates of rects, clipped to
// bounds, each update built in tmp and at most limit bytes.
func appendBitmaps(w, tmp *wire.Writer, img *bitmap.Image, rects []image.Rectangle, bounds image.Rectangle, noAlpha bool, limit int) error {
	off := bitmap.BeginUpdate(tmp)
	n := 0
	for _, r := range rects {
		r = r.Intersect(bounds)
		for y := r.Min.Y; y < r.Max.Y; y += tileHeight {
			for x := r.Min.X; x < r.Max.X; x += tileWidth {
				t := bitmap.Rect{X: x, Y: y, Width: min(tileWidth, r.Max.X-x), Height: min(tileHeight, r.Max.Y-y)}
				mark := tmp.Len()
				if err := bitmap.AppendRect(tmp, img, t, noAlpha); err != nil {
					return err
				}
				if tmp.Len() <= limit && n < 0xffff {
					n++
					continue
				}
				// The tile goes in the next update, where it fits alone.
				tmp.Truncate(mark)
				if err := flushBitmaps(w, tmp, off, n, limit); err != nil {
					return err
				}
				off = bitmap.BeginUpdate(tmp)
				if err := bitmap.AppendRect(tmp, img, t, noAlpha); err != nil {
					return err
				}
				n = 1
			}
		}
	}
	return flushBitmaps(w, tmp, off, n, limit)
}

// flushBitmaps ends the update of n rectangles in tmp, appends it to w as a
// fast-path Bitmap Update, and empties tmp.
func flushBitmaps(w, tmp *wire.Writer, off, n, limit int) error {
	if err := bitmap.EndUpdate(tmp, off, uint16(n)); err != nil {
		return err
	}
	if err := fastpath.AppendUpdate(w, fastpath.UpdateBitmap, tmp.Bytes(), limit); err != nil {
		return err
	}
	tmp.Reset()
	return nil
}
