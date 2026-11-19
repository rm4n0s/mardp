package input

import "testing"

func TestNew(t *testing.T) {
	in, err := New(Options{Display: "x", SwapCtrlCommand: true})
	if err != nil || in == nil {
		t.Fatalf("%v %v", in, err)
	}
	if in.opts.Display != "x" || !in.opts.SwapCtrlCommand || !in.num {
		t.Errorf("options %+v num %v", in.opts, in.num)
	}
}

// TestPostInvalid gives Core Graphics an event of no kind: nothing is made
// and nothing is posted.
func TestPostInvalid(t *testing.T) {
	assertRoute(t, macOS{}.post(&event{}), "macOS.post.InputEventFailed")
}

func TestNewPointer(t *testing.T) {
	p, err := NewPointer(PointerOptions{Display: "x"})
	if err != nil || p == nil || p.opts.Display != "x" || len(p.buf) != 4*maxPointerSide*maxPointerSide {
		t.Fatalf("%v %v", p, err)
	}
}
