package rdptest

import (
	"bytes"
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/bitmap"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

func TestDrawBitmaps(t *testing.T) {
	img := &bitmap.Image{Pix: make([]byte, 4*8*4), Stride: 32, Width: 8, Height: 4}
	for i := range img.Pix {
		img.Pix[i] = byte(i / 4 * 3)
		if i%4 == 3 {
			img.Pix[i] = 0xff
		}
	}
	var w wire.Writer
	off := bitmap.BeginUpdate(&w)
	for _, r := range []bitmap.Rect{{Width: 4, Height: 4}, {X: 4, Width: 4, Height: 4}} {
		if err := bitmap.AppendRect(&w, img, r, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := bitmap.EndUpdate(&w, off, 2); err != nil {
		t.Fatal(err)
	}
	sc := NewScreen(8, 4)
	n, err := sc.DrawBitmaps(w.Bytes())
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if !bytes.Equal(sc.Pix, img.Pix) {
		t.Errorf("drawn % x\nwant  % x", sc.Pix, img.Pix)
	}

	tests := []struct {
		name  string
		data  []byte
		route string
	}{
		{"not a bitmap update", []byte{2, 0, 0, 0}, "Screen.DrawBitmaps->ParseUpdate." + bitmap.TagUpdateInvalid},
		{"cut rectangle", []byte{1, 0, 1, 0, 0}, "Screen.DrawBitmaps->Update.Next->Reader.U16LE." + wire.TagTruncated},
		{"outside the screen", w.Bytes(), "Screen.DrawBitmaps->Draw." + bitmap.TagRectInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewScreen(6, 4).DrawBitmaps(tt.data)
			appErr, ok := errors.FromError(err)
			if !ok {
				t.Fatalf("expected *errors.Error, got %T", err)
			}
			if !appErr.HasRoute(tt.route) {
				t.Errorf("route %s, want %s", appErr.Route(), tt.route)
			}
		})
	}
}
