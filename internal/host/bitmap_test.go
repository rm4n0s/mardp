package host

import (
	"image"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp"
)

// fakeVideo records what a bitmapPath sends, and fails as told.
type fakeVideo struct {
	mu    sync.Mutex
	sent  [][]image.Rectangle
	pix   [][]byte
	fail  func() error
	calls int
	tick  chan struct{}
}

func newFakeVideo() *fakeVideo { return &fakeVideo{tick: make(chan struct{}, 100)} }

func (v *fakeVideo) send(img *rdp.Image, rects []image.Rectangle) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls++
	defer func() { v.tick <- struct{}{} }()
	if v.fail != nil {
		if err := v.fail(); err != nil {
			return err
		}
	}
	v.sent = append(v.sent, slices.Clone(rects))
	v.pix = append(v.pix, slices.Clone(img.Pix))
	return nil
}

func (v *fakeVideo) setFail(f func() error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.fail = f
}

func (v *fakeVideo) last() ([]image.Rectangle, []byte, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.sent) == 0 {
		return nil, nil, len(v.sent)
	}
	return v.sent[len(v.sent)-1], v.pix[len(v.pix)-1], len(v.sent)
}

// wait waits for the next send call.
func (v *fakeVideo) wait(t *testing.T) {
	t.Helper()
	select {
	case <-v.tick:
	case <-time.After(5 * time.Second):
		t.Fatal("nothing sent")
	}
}

// solid is an image of w×h whose pixels are all v.
func solid(w, h int, v byte) *rdp.Image {
	img := &rdp.Image{Pix: make([]byte, 4*w*h), Stride: 4 * w, Width: w, Height: h}
	for i := range img.Pix {
		img.Pix[i] = v
	}
	return img
}

func queueFull() error { return errors.New(rdp.TagQueueFull, "the video queue is full") }

func TestBitmapPathFrames(t *testing.T) {
	v := newFakeVideo()
	b := newBitmapPath(v.send, slog.New(slog.DiscardHandler), 100, 50, time.Hour)
	defer b.stop()

	// The first frame is all of it.
	b.frame(&Frame{Image: solid(100, 50, 1)})
	rects, pix, n := v.last()
	if n != 1 || !slices.Equal(rects, []image.Rectangle{image.Rect(0, 0, 100, 50)}) || pix[0] != 1 {
		t.Fatalf("first frame sent %v (%d sends)", rects, n)
	}

	// Then only what changed is copied and sent; a rectangle partly
	// outside is clipped, one wholly outside dropped.
	img := solid(100, 50, 2)
	b.frame(&Frame{Image: img, Dirty: []image.Rectangle{image.Rect(10, 10, 20, 20), image.Rect(90, 40, 120, 60), image.Rect(200, 200, 210, 210)}})
	rects, pix, _ = v.last()
	want := []image.Rectangle{image.Rect(10, 10, 20, 20), image.Rect(90, 40, 100, 50)}
	if !slices.Equal(rects, want) {
		t.Errorf("sent %v, want %v", rects, want)
	}
	if pix[0] != 1 || pix[4*(10*100+10)] != 2 || pix[4*(9*100+10)] != 1 || pix[4*(49*100+99)] != 2 {
		t.Error("the copy of the desktop did not take only the changed rectangles")
	}

	// A frame with no change sends nothing.
	b.frame(&Frame{Image: img, Dirty: []image.Rectangle{}})
	if _, _, n := v.last(); n != 2 {
		t.Errorf("%d sends after an empty frame, want 2", n)
	}
}

func TestBitmapPathQueueFull(t *testing.T) {
	v := newFakeVideo()
	b := newBitmapPath(v.send, slog.New(slog.DiscardHandler), 100, 50, 20*time.Millisecond)
	defer b.stop()
	b.frame(&Frame{Image: solid(100, 50, 1)})
	v.wait(t)

	v.setFail(queueFull)
	b.frame(&Frame{Image: solid(100, 50, 2), Dirty: []image.Rectangle{image.Rect(0, 0, 10, 10)}})
	v.wait(t)
	b.frame(&Frame{Image: solid(100, 50, 3), Dirty: []image.Rectangle{image.Rect(50, 0, 60, 10)}})
	v.wait(t)
	// The retry after the interval fails too; then the queue has room.
	v.wait(t)
	v.setFail(nil)
	v.wait(t)
	rects, pix, n := v.last()
	want := []image.Rectangle{image.Rect(0, 0, 10, 10), image.Rect(50, 0, 60, 10)}
	if n != 2 || !slices.Equal(rects, want) {
		t.Fatalf("after the queue had room, sent %v (%d sends), want %v", rects, n, want)
	}
	if pix[0] != 2 || pix[4*50] != 3 {
		t.Error("the retry did not send the newest picture")
	}
}

func TestBitmapPathMerge(t *testing.T) {
	v := newFakeVideo()
	b := newBitmapPath(v.send, slog.New(slog.DiscardHandler), 1000, 100, time.Hour)
	defer b.stop()
	b.frame(&Frame{Image: solid(1000, 100, 0)})
	v.setFail(queueFull)
	img := solid(1000, 100, 1)
	var dirty []image.Rectangle
	for i := range maxPending {
		dirty = append(dirty, image.Rect(10*i, 0, 10*i+5, 5))
	}
	// A rectangle inside a waiting one adds nothing; one around it
	// replaces it.
	dirty = append(dirty, image.Rect(1, 1, 2, 2), image.Rect(0, 0, 6, 6))
	b.frame(&Frame{Image: img, Dirty: dirty})
	b.mu.Lock()
	if len(b.pending) != maxPending || b.pending[0] != image.Rect(0, 0, 6, 6) {
		t.Errorf("pending %v", b.pending)
	}
	b.mu.Unlock()
	// One more is merged with them all.
	v.setFail(nil)
	b.frame(&Frame{Image: img, Dirty: []image.Rectangle{image.Rect(500, 50, 510, 60)}})
	rects, _, _ := v.last()
	if want := []image.Rectangle{image.Rect(0, 0, 510, 60)}; !slices.Equal(rects, want) {
		t.Errorf("sent %v, want %v", rects, want)
	}
}

func TestBitmapPathFlow(t *testing.T) {
	v := newFakeVideo()
	b := newBitmapPath(v.send, slog.New(slog.DiscardHandler), 100, 50, time.Hour)
	defer b.stop()

	// A Refresh Rect before the first frame waits for it.
	b.KeyFrameWanted()
	time.Sleep(20 * time.Millisecond)
	if _, _, n := v.last(); n != 0 {
		t.Fatalf("%d sends before any frame", n)
	}
	b.frame(&Frame{Image: solid(100, 50, 1), Dirty: []image.Rectangle{image.Rect(0, 0, 1, 1)}})
	if rects, _, _ := v.last(); !slices.Equal(rects, []image.Rectangle{image.Rect(0, 0, 100, 50)}) {
		t.Errorf("sent %v, want the whole desktop", rects)
	}

	// Suppressed: frames update the copy and send nothing.
	b.OutputSuppressed(true)
	b.frame(&Frame{Image: solid(100, 50, 2), Dirty: []image.Rectangle{image.Rect(0, 0, 10, 10)}})
	b.KeyFrameWanted()
	time.Sleep(20 * time.Millisecond)
	if _, _, n := v.last(); n != 1 {
		t.Errorf("%d sends while suppressed, want 1", n)
	}
	// Resumed: the client asks for the desktop, and gets the newest.
	b.OutputSuppressed(false)
	b.KeyFrameWanted()
	v.wait(t) // the first frame
	v.wait(t)
	rects, pix, _ := v.last()
	if !slices.Equal(rects, []image.Rectangle{image.Rect(0, 0, 100, 50)}) || pix[0] != 2 || pix[4*50] != 0 {
		t.Errorf("after the resume sent %v", rects)
	}
}

func TestBitmapPathEnd(t *testing.T) {
	tests := []struct {
		name    string
		err     func() error
		pending int
		warned  bool
		closed  bool
	}{
		{"session over", func() error { return errors.New(rdp.TagSessionOver, "over") }, 1, false, true},
		{"other failure", func() error { return errors.New("TestSendFailed", "no") }, 0, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{}
			v := newFakeVideo()
			v.setFail(tt.err)
			b := newBitmapPath(v.send, slog.New(rec), 10, 10, time.Hour)
			defer b.stop()
			b.frame(&Frame{Image: solid(10, 10, 1)})
			b.mu.Lock()
			pending, closed := len(b.pending), b.closed
			b.mu.Unlock()
			if pending != tt.pending || closed != tt.closed {
				t.Errorf("pending %d closed %v, want %d %v", pending, closed, tt.pending, tt.closed)
			}
			warns := rec.find("the picture was not sent")
			if (len(warns) == 1) != tt.warned {
				t.Fatalf("warned %d times", len(warns))
			}
			if tt.warned {
				err, _ := attr(warns[0], "err").Any().(error)
				assertRoute(t, err, "TestSendFailed")
			}
			// Nothing is sent after the end.
			b.stop()
			calls := v.calls
			b.frame(&Frame{Image: solid(10, 10, 2)})
			b.KeyFrameWanted()
			time.Sleep(10 * time.Millisecond)
			v.mu.Lock()
			defer v.mu.Unlock()
			if v.calls != calls {
				t.Errorf("sent after stop")
			}
		})
	}
}

func TestBitmapPathAllocs(t *testing.T) {
	b := newBitmapPath(func(*rdp.Image, []image.Rectangle) error { return nil }, slog.New(slog.DiscardHandler), 640, 480, time.Hour)
	defer b.stop()
	img := solid(640, 480, 1)
	f := &Frame{Image: img, Dirty: []image.Rectangle{image.Rect(0, 0, 64, 64), image.Rect(100, 100, 200, 200)}}
	full := &Frame{Image: img}
	if n := testing.AllocsPerRun(100, func() { b.frame(f); b.frame(full) }); n != 0 {
		t.Errorf("%v allocations per frame, want 0", n)
	}
}
