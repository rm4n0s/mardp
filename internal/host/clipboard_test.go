package host

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// offers records what a relay offers to the client.
type offers struct {
	mu   sync.Mutex
	got  []bool
	fail error
}

func (o *offers) Offer(hasText bool) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.got = append(o.got, hasText)
	return o.fail
}

func TestClipboardRelay(t *testing.T) {
	mac := &FakeClipboard{}
	mac.Put("before the session", true)
	dst := &offers{}
	rec := &recorder{}
	r := &clipboardRelay{mac: mac, dst: dst, log: slog.New(rec)}
	r.start()
	refused := errors.New("TestSetFailed", "no")
	steps := []struct {
		name string
		do   func()
		fail error
		want []bool
	}{
		{"what the Mac held before is not offered", nil, nil, nil},
		{"the Mac copies text", func() { mac.Put("one", true) }, nil, []bool{true}},
		{"nothing changed", nil, nil, nil},
		{"the Mac copies a picture", func() { mac.Put("", false) }, nil, []bool{false}},
		{"the client's text is not offered back", func() { r.ClientText("from the client") }, nil, nil},
		{"the queue is full", func() { mac.Put("two", true) }, errors.New(rdp.TagQueueFull, "full"), []bool{true}},
		{"tried again", nil, nil, []bool{true}},
		{"another failure", func() { mac.Put("three", true) }, errors.New("TestOfferFailed", "no"), []bool{true}},
		{"not tried again", nil, nil, nil},
		{"the Mac refuses the client's text", func() {
			mac.Fail(refused)
			r.ClientText("lost")
			r.ClientText("lost too")
			mac.Fail(nil)
		}, nil, nil},
	}
	for _, s := range steps {
		if s.do != nil {
			s.do()
		}
		dst.mu.Lock()
		dst.got, dst.fail = nil, s.fail
		dst.mu.Unlock()
		r.check()
		dst.mu.Lock()
		got := slices.Clone(dst.got)
		dst.mu.Unlock()
		if !slices.Equal(got, s.want) {
			t.Fatalf("%s: offered %v, want %v", s.name, got, s.want)
		}
	}
	if text, ok := r.ServerText(); !ok || text != "three" {
		t.Errorf("the Mac's text for the client: %q, %v", text, ok)
	}
	warns := rec.find("the client's text was not put on the Mac's clipboard")
	if len(warns) != 1 {
		t.Fatalf("logged %d times, want once", len(warns))
	}
	err, _ := attr(warns[0], "err").Any().(error)
	assertRoute(t, err, "TestClipboardRelay.TestSetFailed")
}

// TestSessionClipboard is a whole session: the text the client copies is
// on the Mac's clipboard, and what the Mac copies afterwards is offered to
// the client and pasted there.
func TestSessionClipboard(t *testing.T) {
	mac := &FakeClipboard{}
	mac.Put("held before the session", true)
	parts := Parts{Screen: &PatternScreen{Frames: 1}, Input: &RecordingInjector{}, Clipboard: mac}
	s := serve(t, parts, Options{}, 640, 480)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cb, err := s.c.OpenClipboard(ctx, rdptest.ClipboardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cb.Copy(ctx, "from Linux\r\nline 2"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if text, _ := mac.Text(); text == "from Linux\nline 2" {
			break
		}
		if time.Now().After(deadline) {
			text, ok := mac.Text()
			t.Fatalf("the Mac's clipboard holds %q (%v)", text, ok)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The Mac copies: within a look or two the client is told, and pastes.
	mac.Put("from the Mac", true)
	has, err := cb.WaitOffer(ctx)
	if err != nil || !has {
		t.Fatalf("the Mac's text was offered as %v, %v", has, err)
	}
	if text, ok, err := cb.PasteText(ctx); err != nil || !ok || text != "from the Mac" {
		t.Errorf("pasted %q, %v, %v", text, ok, err)
	}
	mac.Put("", false)
	if has, err := cb.WaitOffer(ctx); err != nil || has {
		t.Errorf("a clipboard without text was offered as %v, %v", has, err)
	}
	if len(s.rec.find("the clipboard's text is shared with the client")) != 1 {
		t.Error("the session did not say it shares the clipboard")
	}
}

// TestSessionNoClipboard: a host without the client's input (view-only),
// or without a clipboard, shares none.
func TestSessionNoClipboard(t *testing.T) {
	tests := []struct {
		name  string
		parts Parts
	}{
		{"view-only", Parts{Screen: &PatternScreen{Frames: 1}, Clipboard: &FakeClipboard{}}},
		{"no clipboard", Parts{Screen: &PatternScreen{Frames: 1}, Input: &RecordingInjector{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := serve(t, tt.parts, Options{}, 640, 480)
			if s.h.Clipboard() {
				t.Fatal("the host shares a clipboard")
			}
			// The server says nothing on cliprdr: a client that waits for
			// its capabilities waits in vain.
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			if cb, err := s.c.OpenClipboard(ctx, rdptest.ClipboardOptions{}); err == nil {
				t.Errorf("the server opened the clipboard: flags %#x", cb.ServerFlags)
			}
			if len(s.rec.find("the clipboard's text is shared with the client")) != 0 {
				t.Error("the session says it shares the clipboard")
			}
		})
	}
}
