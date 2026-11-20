//go:build darwin

package clipboard

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/rm4n0s/errors"
)

// TestClipboard reads and writes a pasteboard of the test's own: the
// owner's clipboard is not touched.
func TestClipboard(t *testing.T) {
	c, free := named(fmt.Sprintf("com.github.rm4n0s.mardp.test.%d", os.Getpid()))
	defer free()
	if text, ok := c.Text(); ok {
		t.Fatalf("a new pasteboard holds %q", text)
	}
	before := c.Changes()
	tests := []string{"hello", "", "two\nlines\twith a tab", "μ 😀 日本語", strings.Repeat("long ", 100_000)}
	for _, text := range tests {
		if err := c.SetText(text); err != nil {
			t.Fatal(err)
		}
		after := c.Changes()
		if after == before {
			t.Errorf("%.20q: the change count stayed %d", text, after)
		}
		before = after
		if got, ok := c.Text(); !ok || got != text {
			t.Errorf("%.20q: read back %.20q (%d bytes), %v", text, got, len(got), ok)
		}
		if again := c.Changes(); again != after {
			t.Errorf("reading changed the count from %d to %d", after, again)
		}
	}
	// Text that is not UTF-8 is not taken, and what the pasteboard held
	// stays.
	if err := c.SetText("ok"); err != nil {
		t.Fatal(err)
	}
	err := c.SetText("bad \xff\xfe")
	if err == nil {
		t.Fatal("text that is not UTF-8 was taken")
	}
	appErr, ok := errors.FromError(err)
	if !ok || !appErr.HasRoute("Clipboard.SetText."+TagClipboardWriteFailed) {
		t.Errorf("route of %v", err)
	}
	if got, ok := c.Text(); !ok || got != "ok" {
		t.Errorf("after the refusal the pasteboard holds %q, %v", got, ok)
	}
}

// TestTextFormats: an app that writes its text in one format alone, other
// than UTF-8, is read too. Screen Sharing writes "traditional Mac plain
// text" only, and asking for UTF-8 then gives nothing.
func TestTextFormats(t *testing.T) {
	c, free := named(fmt.Sprintf("com.github.rm4n0s.mardp.test.formats.%d", os.Getpid()))
	defer free()
	tests := []struct {
		name, typ string
		data      []byte
		want      string
		ok        bool
	}{
		{"UTF-8", "public.utf8-plain-text", []byte("héllo ✓"), "héllo ✓", true},
		{"traditional Mac text", "com.apple.traditional-mac-plain-text", []byte("caf\x8e, 75 bytes of it"), "café, 75 bytes of it", true},
		{"UTF-16 with a mark", "public.utf16-external-plain-text", []byte{0xff, 0xfe, 'h', 0, 0xbc, 0x03}, "hμ", true},
		{"UTF-16", "public.utf16-plain-text", []byte{'h', 0, 0xbc, 0x03}, "hμ", true},
		{"RTF", "public.rtf", []byte(`{\rtf1\ansi rich \b text\b0}`), "rich text", true},
		{"a picture", "public.png", []byte{0x89, 'P', 'N', 'G'}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !c.setData(tt.typ, tt.data) {
				t.Fatal("the pasteboard did not take the data")
			}
			if got, ok := c.Text(); ok != tt.ok || got != tt.want {
				t.Errorf("text %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestGeneral only looks at the general pasteboard's count: it reads no
// contents and changes nothing.
func TestGeneral(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("macOS's answer to this program reading the clipboard: %s", c.Access())
	if a := c.Access(); a != AccessDefault && a != AccessAllowed && a != AccessAsks && a != AccessRefused {
		t.Errorf("Access = %q", a)
	}
	if a, b := c.Changes(), c.Changes(); a != b && b != a+1 {
		t.Errorf("the count went from %d to %d between two looks", a, b)
	}
}
