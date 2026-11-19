package host

import (
	"image"
	"log/slog"
	"sync"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp"
)

// maxPending is how many changed rectangles wait to be sent before they
// are merged into the one rectangle around them all.
const maxPending = 16

// bitmapPath sends a session's picture on the bitmap paths (SPEC §9.6). It
// keeps a copy of the desktop, into which each frame's changes are copied,
// and the rectangles not yet sent: when the video queue is full
// (rdp.TagQueueFull), they wait and are sent again from the copy, with the
// next frame or after one frame's time, so a skipped change is never lost
// and the client always ends up with the newest picture. It is the
// session's rdp.FlowSink: a Refresh Rect sends the whole desktop, and
// nothing is sent while the client suppresses output.
type bitmapPath struct {
	send     func(img *rdp.Image, rects []image.Rectangle) error
	log      *slog.Logger
	interval time.Duration
	desk     image.Rectangle
	onFrame  func()

	mu         sync.Mutex
	shadow     rdp.Image
	have       bool // a frame has come
	pending    []image.Rectangle
	whole      [1]image.Rectangle
	suppressed bool
	closed     bool
	timer      *time.Timer
}

// newBitmapPath makes the path of a desktop of the size, which sends with
// send and tries again after interval when the queue is full.
func newBitmapPath(send func(*rdp.Image, []image.Rectangle) error, log *slog.Logger, width, height int, interval time.Duration) *bitmapPath {
	b := &bitmapPath{
		send: send, log: log, interval: interval, desk: image.Rect(0, 0, width, height),
		shadow:  rdp.Image{Pix: make([]byte, 4*width*height), Stride: 4 * width, Width: width, Height: height},
		pending: make([]image.Rectangle, 0, maxPending),
	}
	b.timer = time.AfterFunc(time.Hour, b.retry)
	b.timer.Stop()
	return b
}

// frame takes a captured frame: the capture's callback.
func (b *bitmapPath) frame(f *Frame) {
	b.mu.Lock()
	if !b.closed {
		b.have = true
		dirty := f.Dirty
		full := image.Rect(0, 0, f.Image.Width, f.Image.Height).Intersect(b.desk)
		if dirty == nil {
			b.whole[0] = full
			dirty = b.whole[:]
		}
		for _, r := range dirty {
			if r = r.Intersect(full); !r.Empty() {
				copyRect(&b.shadow, f.Image, r)
				b.add(r)
			}
		}
		if !b.suppressed {
			b.flush()
		}
	}
	b.mu.Unlock()
	if b.onFrame != nil {
		b.onFrame()
	}
}

// copyRect copies the rectangle r of src into dst.
func copyRect(dst, src *rdp.Image, r image.Rectangle) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		copy(dst.Pix[y*dst.Stride+4*r.Min.X:y*dst.Stride+4*r.Max.X], src.Pix[y*src.Stride+4*r.Min.X:])
	}
}

// add adds r to the rectangles to send.
func (b *bitmapPath) add(r image.Rectangle) {
	for i, p := range b.pending {
		if r.In(p) {
			return
		}
		if p.In(r) {
			b.pending[i] = r
			return
		}
	}
	if len(b.pending) < maxPending {
		b.pending = append(b.pending, r)
		return
	}
	for _, p := range b.pending {
		r = r.Union(p)
	}
	b.pending = append(b.pending[:0], r)
}

// flush sends the waiting rectangles from the copy of the desktop. The
// caller holds mu.
func (b *bitmapPath) flush() {
	if len(b.pending) == 0 || !b.have {
		return
	}
	err := b.send(&b.shadow, b.pending)
	switch {
	case err == nil:
		b.pending = b.pending[:0]
	case hasTag(err, rdp.TagQueueFull):
		b.timer.Reset(b.interval)
	case hasTag(err, rdp.TagSessionOver):
		b.closed = true
	default:
		b.log.Warn("the picture was not sent", "err", err)
		b.pending = b.pending[:0]
	}
}

// retry sends what is waiting, after the queue was full or a Refresh Rect.
func (b *bitmapPath) retry() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed && !b.suppressed {
		b.flush()
	}
}

// KeyFrameWanted implements rdp.FlowSink: the whole desktop is sent again,
// from the capture's goroutine's side (a timer), not the reading one.
func (b *bitmapPath) KeyFrameWanted() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending = append(b.pending[:0], b.desk)
	b.timer.Reset(0)
}

// OutputSuppressed implements rdp.FlowSink. Frames still update the copy
// while output is suppressed; the resume is followed by KeyFrameWanted.
func (b *bitmapPath) OutputSuppressed(suppressed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.suppressed = suppressed
}

// stop ends the path: nothing more is sent.
func (b *bitmapPath) stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.timer.Stop()
}

// hasTag says err is an *errors.Error of tag.
func hasTag(err error, tag string) bool {
	e, ok := errors.FromError(err)
	return ok && e.Tag == tag
}
