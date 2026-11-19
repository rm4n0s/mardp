package rdptest

import (
	"bytes"
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/bitmap"
	"github.com/rm4n0s/mardp/rdp/internal/gfx"
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

func TestDrawGFX(t *testing.T) {
	img := &bitmap.Image{Pix: make([]byte, 4*8*4), Stride: 32, Width: 8, Height: 4}
	for i := range img.Pix {
		img.Pix[i] = byte(i / 4 * 3)
		if i%4 == 3 {
			img.Pix[i] = 0
		}
	}
	tile := func(r bitmap.Rect) gfx.PDU {
		var w wire.Writer
		planar, err := bitmap.AppendTile(&w, img, r)
		if err != nil {
			t.Fatal(err)
		}
		codec := gfx.CodecUncompressed
		if planar {
			codec = gfx.CodecPlanar
		}
		return gfx.PDU{Cmd: gfx.CmdWireToSurface1, SurfaceID: 1, CodecID: codec, PixelFormat: gfx.PixelFormatXRGB,
			Left: uint16(r.X), Top: uint16(r.Y), Right: uint16(r.X + r.Width), Bottom: uint16(r.Y + r.Height), Body: w.Bytes()}
	}
	ps := []gfx.PDU{
		{Cmd: gfx.CmdStartFrame, FrameID: 3},
		tile(bitmap.Rect{Width: 8, Height: 3}),
		tile(bitmap.Rect{Y: 3, Width: 1, Height: 1}),
		tile(bitmap.Rect{X: 1, Y: 3, Width: 7, Height: 1}),
		{Cmd: gfx.CmdEndFrame, FrameID: 3},
	}
	sc := NewScreen(8, 4)
	d, err := sc.DrawGFX(ps)
	if err != nil {
		t.Fatal(err)
	}
	if d.Tiles != 3 || d.Planar != 2 || len(d.Ended) != 1 || d.Ended[0] != 3 {
		t.Errorf("drawn %+v", d)
	}
	// Alpha means nothing in XRGB_8888, and is drawn as 0xFF.
	want := bytes.Clone(img.Pix)
	for i := 3; i < len(want); i += 4 {
		want[i] = 0xff
	}
	if !bytes.Equal(sc.Pix, want) {
		t.Errorf("drawn % x\nwant  % x", sc.Pix, want)
	}

	edit := func(f func(p *gfx.PDU)) []gfx.PDU {
		p := tile(bitmap.Rect{Width: 2, Height: 2})
		f(&p)
		return []gfx.PDU{p}
	}
	tests := []struct {
		name  string
		ps    []gfx.PDU
		route string
	}{
		{"another surface", edit(func(p *gfx.PDU) { p.SurfaceID = 2 }), "Screen.DrawGFX." + TagGFXUnsupported},
		{"another format", edit(func(p *gfx.PDU) { p.PixelFormat = gfx.PixelFormatARGB }), "Screen.DrawGFX." + TagGFXUnsupported},
		{"another codec", edit(func(p *gfx.PDU) { p.CodecID = gfx.CodecAVC420 }), "Screen.DrawGFX." + TagGFXUnsupported},
		{"outside the screen", edit(func(p *gfx.PDU) { p.Left, p.Right = 7, 9 }), "Screen.DrawGFX->DrawTile." + bitmap.TagRectInvalid},
		{"inverted", edit(func(p *gfx.PDU) { p.Left, p.Right = 2, 0 }), "Screen.DrawGFX->DrawTile." + bitmap.TagRectInvalid},
		{"cut planar", edit(func(p *gfx.PDU) { p.Body = p.Body[:1] }), "Screen.DrawGFX->DrawTile->decodePlanar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewScreen(8, 4).DrawGFX(tt.ps)
			assertRoute(t, err, tt.route)
		})
	}
}
