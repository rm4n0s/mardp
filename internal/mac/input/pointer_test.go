package input

import (
	"testing"

	"github.com/rm4n0s/errors"
)

// fakeCursor draws a pointer of the test's choosing.
type fakeCursor struct {
	info  pointerInfo
	color byte
	err   error
}

func (f *fakeCursor) read(_ string, buf []byte) (pointerInfo, error) {
	if f.err != nil {
		return pointerInfo{}, f.err
	}
	for i := range 4 * f.info.width * f.info.height {
		buf[i] = f.color
	}
	return f.info, nil
}

func TestPointerCurrent(t *testing.T) {
	f := &fakeCursor{info: pointerInfo{visible: true, width: 4, height: 3, hotX: 1, hotY: 2, onDisplay: true, x: 10, y: 20}, color: 7}
	p := newPointer(PointerOptions{}, f)
	read := func() (sh struct {
		hash uint64
		w, h int
	}) {
		t.Helper()
		s, err := p.Current()
		if err != nil {
			t.Fatal(err)
		}
		if !s.Visible || s.Image == nil || !s.OnDisplay || s.X != 10 || s.Y != 20 {
			t.Fatalf("shape %+v", s)
		}
		if s.Image.Width != f.info.width || s.Image.Height != f.info.height || s.Image.Stride != 4*f.info.width ||
			len(s.Image.Pix) != 4*f.info.width*f.info.height || s.HotX != f.info.hotX || s.HotY != f.info.hotY {
			t.Fatalf("image %d×%d stride %d len %d hot %d,%d", s.Image.Width, s.Image.Height, s.Image.Stride, len(s.Image.Pix), s.HotX, s.HotY)
		}
		sh.hash, sh.w, sh.h = s.Hash, s.Image.Width, s.Image.Height
		return sh
	}
	first := read()
	if again := read(); again.hash != first.hash {
		t.Error("the same pointer hashed differently")
	}
	f.color = 8
	pix := read()
	if pix.hash == first.hash {
		t.Error("other pixels, same hash")
	}
	f.info.hotX = 2
	hot := read()
	if hot.hash == pix.hash {
		t.Error("another hotspot, same hash")
	}
	f.info.width, f.info.height = 3, 4
	if size := read(); size.hash == hot.hash {
		t.Error("another size, same hash")
	}

	f.info = pointerInfo{onDisplay: true, x: 5, y: 6}
	s, err := p.Current()
	if err != nil || s.Visible || s.Image != nil || !s.OnDisplay || s.X != 5 {
		t.Errorf("hidden: %+v %v", s, err)
	}
	f.err = errors.New("TestReadFailed", "no")
	if _, err := p.Current(); err == nil {
		t.Error("the error was lost")
	} else {
		assertRoute(t, err, "TestPointerCurrent.TestReadFailed")
	}
}

func TestCheckRead(t *testing.T) {
	info := pointerInfo{visible: true, width: 300, height: 300}
	if got, err := checkRead(readOK, info); err != nil || got != info {
		t.Errorf("read: %+v %v", got, err)
	}
	tests := []struct {
		status int
		route  string
	}{
		{readTooLarge, "checkRead.PointerTooLarge"},
		{readNoImage, "checkRead.PointerUnreadable"},
		{readNotDrawn, "checkRead.PointerUnreadable"},
	}
	for _, tt := range tests {
		got, err := checkRead(tt.status, info)
		assertRoute(t, err, tt.route)
		if got != (pointerInfo{}) {
			t.Errorf("status %d: info %+v", tt.status, got)
		}
	}
}

func TestPointerAllocs(t *testing.T) {
	p := newPointer(PointerOptions{}, &fakeCursor{info: pointerInfo{visible: true, width: 32, height: 32}})
	if n := testing.AllocsPerRun(100, func() {
		if _, err := p.Current(); err != nil {
			t.Fatal(err)
		}
	}); n != 0 {
		t.Errorf("%v allocations a read, want 0", n)
	}
}
