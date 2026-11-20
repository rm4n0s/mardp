package rdp

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/rdp/internal/cliprdr"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// clip is a ClipboardHandler that keeps what the client copied and gives
// the text the test sets.
type clip struct {
	mu     sync.Mutex
	text   string
	has    bool
	copied []string
	got    chan string
}

func newClip() *clip { return &clip{got: make(chan string, 8)} }

func (c *clip) ServerText() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text, c.has
}

func (c *clip) ClientText(text string) {
	c.mu.Lock()
	c.copied = append(c.copied, text)
	c.mu.Unlock()
	c.got <- text
}

func (c *clip) set(text string, has bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.text, c.has = text, has
}

// next waits for the next text the client copied.
func (c *clip) next(t *testing.T) string {
	t.Helper()
	select {
	case s := <-c.got:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("no text from the client")
	}
	return ""
}

// clipSession opens a session with the clipboard on both sides.
func clipSession(t *testing.T, opts rdptest.ClipboardOptions) (*testServer, *Session, *rdptest.Clipboard, context.Context) {
	t.Helper()
	h := newSessions()
	ts := start(t, Config{Handler: h, Clipboard: true})
	c, _ := ts.open(t, rdptest.Options{Channels: channelTestNames})
	s := h.next(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	cb, err := c.OpenClipboard(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Clipboard().Available() {
		t.Fatal("the session has no clipboard")
	}
	return ts, s, cb, ctx
}

// read reads the server's next clipboard PDU, which must be of type want.
func readClip(t *testing.T, ctx context.Context, cb *rdptest.Clipboard, want uint16) cliprdr.PDU {
	t.Helper()
	p, err := cb.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != want {
		t.Fatalf("clipboard PDU of type %#x (flags %#x, %d bytes), want %#x", p.Type, p.Flags, len(p.Data), want)
	}
	return p
}

var textFormats = []cliprdr.Format{{ID: cliprdr.FormatUnicodeText}}

// TestClipboard: the text the client's user copies reaches the app, and
// the app's text reaches the client when its user pastes, with long and
// short format names and with a client that sends no capabilities
// (MS-RDPECLIP 1.3.2).
func TestClipboard(t *testing.T) {
	tests := []struct {
		name string
		opts rdptest.ClipboardOptions
		long bool
	}{
		{"long names", rdptest.ClipboardOptions{}, true},
		{"short names", rdptest.ClipboardOptions{ShortNames: true}, false},
		{"no capabilities", rdptest.ClipboardOptions{NoCaps: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, s, cb, ctx := clipSession(t, tt.opts)
			if cb.ServerFlags != cliprdr.CapsLongFormatNames || cb.Long != tt.long {
				t.Fatalf("server flags %#x, long names %v", cb.ServerFlags, cb.Long)
			}
			h := newClip()
			s.Clipboard().SetHandler(h)

			// The client's user copies: the server accepts the list and asks
			// for the text at once.
			formats := append([]cliprdr.Format{{ID: 0xc0a1, Name: "HTML Format"}}, textFormats...)
			if err := cb.SendFormats(ctx, formats); err != nil {
				t.Fatal(err)
			}
			if p := readClip(t, ctx, cb, cliprdr.MsgFormatListResponse); p.Flags != cliprdr.FlagResponseOK {
				t.Errorf("the list's response has flags %#x", p.Flags)
			}
			p := readClip(t, ctx, cb, cliprdr.MsgFormatDataRequest)
			if id, err := cliprdr.ReadFormatDataRequest(p.Data); err != nil || id != cliprdr.FormatUnicodeText {
				t.Errorf("the server asks for format %d, %v", id, err)
			}
			if err := cb.SendText(ctx, "héllo\r\nwörld 😀"); err != nil {
				t.Fatal(err)
			}
			if got := h.next(t); got != "héllo\nwörld 😀" {
				t.Errorf("the app got %q", got)
			}

			// The app's clipboard has text: the client learns of it, and
			// gets it when it asks, in either text format.
			h.set("from the Mac\nline 2 μ", true)
			if err := s.Clipboard().Offer(true); err != nil {
				t.Fatal(err)
			}
			p = readClip(t, ctx, cb, cliprdr.MsgFormatList)
			got, err := cliprdr.ReadFormatList(p, tt.long)
			if want := []cliprdr.Format{{ID: cliprdr.FormatUnicodeText}, {ID: cliprdr.FormatText}}; err != nil || !slices.Equal(got, want) {
				t.Errorf("the server's formats: %+v, %v", got, err)
			}
			if err := cb.Send(ctx, func(w *wire.Writer) { cliprdr.AppendFormatListResponse(w, true) }); err != nil {
				t.Fatal(err)
			}
			data, ok, err := cb.Paste(ctx, cliprdr.FormatUnicodeText)
			if err != nil || !ok || cliprdr.DecodeText(data) != "from the Mac\nline 2 μ" || !bytes.Contains(data, []byte{'\r', 0, '\n', 0}) {
				t.Errorf("pasted %q (%x), ok %v, %v", cliprdr.DecodeText(data), data, ok, err)
			}
			data, ok, err = cb.Paste(ctx, cliprdr.FormatText)
			if err != nil || !ok || string(data) != "from the Mac\r\nline 2 ?\x00" {
				t.Errorf("pasted as 8-bit text %q, ok %v, %v", data, ok, err)
			}

			// A format the server does not have, and a clipboard without
			// text, are refused.
			if _, ok, err := cb.Paste(ctx, 0xc0a1); err != nil || ok {
				t.Errorf("another format: ok %v, %v", ok, err)
			}
			h.set("", false)
			if _, ok, err := cb.Paste(ctx, cliprdr.FormatUnicodeText); err != nil || ok {
				t.Errorf("no text: ok %v, %v", ok, err)
			}
			if err := s.Clipboard().Offer(false); err != nil {
				t.Fatal(err)
			}
			if p := readClip(t, ctx, cb, cliprdr.MsgFormatList); len(p.Data) != 0 {
				t.Errorf("a clipboard without text is offered as %x", p.Data)
			}
			// A text longer than the clipboard carries is refused too.
			h.set(strings.Repeat("x", ClipboardMaxText+1), true)
			if _, ok, err := cb.Paste(ctx, cliprdr.FormatUnicodeText); err != nil || ok {
				t.Errorf("too long: ok %v, %v", ok, err)
			}
			h.set(strings.Repeat("y", ClipboardMaxText), true)
			if data, ok, err := cb.Paste(ctx, cliprdr.FormatUnicodeText); err != nil || !ok || len(data) != 2*ClipboardMaxText+2 {
				t.Errorf("the longest text: %d bytes, ok %v, %v", len(data), ok, err)
			}
		})
	}
}

// TestClipboardCopiesTwice: the client's user copies again before the
// client has answered the server's request: the answer on its way is of
// the first text, which is dropped, and the server asks again.
func TestClipboardCopiesTwice(t *testing.T) {
	_, s, cb, ctx := clipSession(t, rdptest.ClipboardOptions{})
	h := newClip()
	s.Clipboard().SetHandler(h)
	for range 2 {
		if err := cb.SendFormats(ctx, textFormats); err != nil {
			t.Fatal(err)
		}
	}
	readClip(t, ctx, cb, cliprdr.MsgFormatListResponse)
	readClip(t, ctx, cb, cliprdr.MsgFormatDataRequest)
	readClip(t, ctx, cb, cliprdr.MsgFormatListResponse)
	if err := cb.SendText(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	readClip(t, ctx, cb, cliprdr.MsgFormatDataRequest)
	if err := cb.SendText(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	if got := h.next(t); got != "second" {
		t.Errorf("the app got %q, want the second text alone", got)
	}
}

// TestClipboardOfferEarly: an Offer made before the client's first Format
// List goes out after the response to that list.
func TestClipboardOfferEarly(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h, Clipboard: true})
	c, _ := ts.open(t, rdptest.Options{Channels: channelTestNames})
	s := h.next(t)
	if err := s.Clipboard().Offer(false); err != nil {
		t.Fatal(err)
	}
	if err := s.Clipboard().Offer(true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cb, err := c.OpenClipboard(ctx, rdptest.ClipboardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p := readClip(t, ctx, cb, cliprdr.MsgFormatList)
	if got, err := cliprdr.ReadFormatList(p, true); err != nil || len(got) != 2 {
		t.Errorf("the early offer arrived as %+v, %v", got, err)
	}
}

// TestClipboardUnavailable: a server without Config.Clipboard, and a
// client that did not ask for the channel, have no clipboard.
func TestClipboardUnavailable(t *testing.T) {
	tests := []struct {
		name     string
		on       bool
		channels []string
	}{
		{"the server has none", false, channelTestNames},
		{"the client has none", true, []string{"rdpsnd", "drdynvc"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newSessions()
			ts := start(t, Config{Handler: h, Clipboard: tt.on})
			ts.open(t, rdptest.Options{Channels: tt.channels})
			s := h.next(t)
			if s.Clipboard().Available() || s.channel("cliprdr") != nil {
				t.Error("the session has a clipboard")
			}
			s.Clipboard().SetHandler(newClip())
			assertRoute(t, s.Clipboard().Offer(true), "Clipboard.Offer."+TagClipboardUnavailable)
		})
	}
}

// TestClipboardIgnores: what is malformed, out of turn or not the
// server's business is ignored, and the session goes on; without a handler
// the client's text is not asked for and its requests are refused.
func TestClipboardIgnores(t *testing.T) {
	ts, s, cb, ctx := clipSession(t, rdptest.ClipboardOptions{})
	send := func(b ...byte) {
		t.Helper()
		if err := cb.Send(ctx, func(w *wire.Writer) { w.Write(b) }); err != nil {
			t.Fatal(err)
		}
	}
	send(1, 2, 3)                                          // shorter than a header
	send(0x07, 0, 0, 0, 1, 0, 0, 0, 9)                     // capabilities cut short
	send(0x0a, 0, 0, 0, 4, 0, 0, 0, 1, 0, 0, 0)            // Lock Clipboard Data
	send(0x05, 0, 1, 0, 2, 0, 0, 0, 'x', 0)                // a response nobody asked for
	send(0x03, 0, 2, 0, 0, 0, 0, 0)                        // the client refuses a list
	send(0x04, 0, 0, 0, 2, 0, 0, 0, 0x0d, 0)               // a request without its format
	readClip(t, ctx, cb, cliprdr.MsgFormatDataResponse)    // refused
	send(0x02, 0, 0, 0, 6, 0, 0, 0, 0x0d, 0, 0, 0, 'A', 0) // a list whose name does not end
	if p := readClip(t, ctx, cb, cliprdr.MsgFormatListResponse); p.Flags != cliprdr.FlagResponseFail {
		t.Errorf("a malformed list was answered with flags %#x", p.Flags)
	}
	// No handler: the text is not asked for, a request is refused.
	if err := cb.SendFormats(ctx, textFormats); err != nil {
		t.Fatal(err)
	}
	readClip(t, ctx, cb, cliprdr.MsgFormatListResponse)
	if _, ok, err := cb.Paste(ctx, cliprdr.FormatUnicodeText); err != nil || ok {
		t.Errorf("without a handler: ok %v, %v", ok, err)
	}
	// A list without text is accepted and nothing is asked for; the
	// client's refusal to give its text reaches nobody.
	h := newClip()
	s.Clipboard().SetHandler(h)
	if err := cb.SendFormats(ctx, []cliprdr.Format{{ID: 2}}); err != nil {
		t.Fatal(err)
	}
	readClip(t, ctx, cb, cliprdr.MsgFormatListResponse)
	if err := cb.SendFormats(ctx, textFormats); err != nil {
		t.Fatal(err)
	}
	readClip(t, ctx, cb, cliprdr.MsgFormatListResponse)
	readClip(t, ctx, cb, cliprdr.MsgFormatDataRequest)
	if err := cb.Send(ctx, func(w *wire.Writer) { cliprdr.AppendFormatDataResponse(w, false, nil) }); err != nil {
		t.Fatal(err)
	}
	// The session is still there: a paste is answered.
	h.set("still here", true)
	if data, ok, err := cb.Paste(ctx, cliprdr.FormatUnicodeText); err != nil || !ok || cliprdr.DecodeText(data) != "still here" {
		t.Errorf("after all that: %q, ok %v, %v", cliprdr.DecodeText(data), ok, err)
	}
	if len(h.copied) != 0 {
		t.Errorf("the app got %q", h.copied)
	}
	for _, msg := range []string{"a clipboard PDU ignored", "the client's clipboard capabilities ignored",
		"a clipboard PDU the server does not act on", "a clipboard Format Data Response nobody asked for ignored",
		"the client refused the server's clipboard formats", "the client's request for the clipboard refused",
		"the client's clipboard formats ignored", "the client did not give its clipboard's text"} {
		ts.log.find(t, msg)
	}
}

// TestClipboardClientUnavailable: the test client's helper says so when
// the client did not ask for the channel.
func TestClipboardClientUnavailable(t *testing.T) {
	ts := start(t, Config{Clipboard: true})
	c, _ := ts.open(t, rdptest.Options{Channels: []string{"rdpsnd"}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.OpenClipboard(ctx, rdptest.ClipboardOptions{})
	assertRoute(t, err, "Client.OpenClipboard."+rdptest.TagClipboardUnavailable)
}
