//go:build darwin && mardp_mac

package input

import "testing"

// These read the system; none posts an event (CLAUDE.md, rule 11):
// posting is a manual check.

func TestMacTrusted(t *testing.T) {
	t.Logf("Accessibility granted: %v", macOS{}.trusted())
}

func TestMacOrigin(t *testing.T) {
	x, y, ok := macOS{}.origin("")
	if !ok || x != 0 || y != 0 {
		t.Errorf("main display at %v,%v (%v), want 0,0", x, y, ok)
	}
	if _, _, ok := (macOS{}).origin("00000000-0000-0000-0000-000000000000"); ok {
		t.Error("found a display that is not there")
	}
}

// TestMacPointer reads the system pointer from a goroutine that is not the
// main thread, as the host does.
func TestMacPointer(t *testing.T) {
	p, err := NewPointer(PointerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sh, err := p.Current()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("visible %v, on the display %v at %.1f,%.1f", sh.Visible, sh.OnDisplay, sh.X, sh.Y)
	if !sh.OnDisplay {
		t.Error("the pointer is not on the main display")
	}
	if !sh.Visible {
		t.Skip("the pointer is hidden: no image to check")
	}
	img := sh.Image
	opaque := 0
	for y := range img.Height {
		for x := range img.Width {
			if img.Pix[y*img.Stride+4*x+3] != 0 {
				opaque++
			}
		}
	}
	t.Logf("image %d×%d, hotspot %d,%d, %d pixels not transparent, hash %#x", img.Width, img.Height, sh.HotX, sh.HotY, opaque, sh.Hash)
	if img.Width < 8 || img.Height < 8 || sh.HotX >= img.Width || sh.HotY >= img.Height || opaque == 0 {
		t.Error("not a pointer")
	}
	again, err := p.Current()
	if err != nil {
		t.Fatal(err)
	}
	if again.Hash != sh.Hash {
		t.Log("the pointer changed between two reads")
	}
	if _, err := (macCursor{}).read("", make([]byte, 4)); err != nil {
		assertRoute(t, err, "macCursor.read->checkRead.PointerTooLarge")
	} else {
		t.Error("a pointer fits in one pixel")
	}
}

func BenchmarkMacPointer(b *testing.B) {
	p, err := NewPointer(PointerOptions{})
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := p.Current(); err != nil {
			b.Fatal(err)
		}
	}
}
