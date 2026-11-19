package rdp

import (
	"bytes"
	"context"
	"image"
	"math/rand/v2"
	"testing"

	"github.com/rm4n0s/mardp/rdp/internal/dynvc"
	"github.com/rm4n0s/mardp/rdp/internal/gfx"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// drawGFXFrame reads one graphics frame into screen, acknowledges it,
// adds up the tiles drawn, and checks that the screen is then want.
func drawGFXFrame(t *testing.T, ctx context.Context, c *rdptest.Client, ch uint32, screen, want *rdptest.Screen, drawn *rdptest.GFXDrawn) {
	t.Helper()
	ps, err := c.ReadGFX(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	d, err := screen.DrawGFX(ps)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Ended) != 1 || ps[0].Cmd != gfx.CmdStartFrame {
		t.Fatalf("a message of %d PDUs that is not one frame", len(ps))
	}
	if err := c.AckFrame(ctx, ch, 0, d.Ended[0], d.Ended[0]); err != nil {
		t.Fatal(err)
	}
	drawn.Tiles += d.Tiles
	drawn.Planar += d.Planar
	drawn.Ended = append(drawn.Ended, d.Ended...)
	if !bytes.Equal(screen.Pix, want.Pix) {
		t.Fatalf("after frame %d the screen is not the picture", len(drawn.Ended))
	}
}

// TestGFXBitmaps is TestBitmaps through the graphics pipeline: random
// changed rectangles of random pictures arrive pixel for pixel, as frames
// of Planar and uncompressed tiles. The client is asked for the whole
// desktop once the surface exists, and after the channel closes the
// rectangles go as bitmaps again.
func TestGFXBitmaps(t *testing.T) {
	tests := []struct {
		name          string
		opts          rdptest.Options
		width, height int // of the picture; 0 is the desktop's
	}{
		{name: "default"},
		{name: "picture larger than the desktop", width: 1100, height: 800},
		{name: "picture smaller than the desktop", width: 500, height: 300},
		{name: "small desktop", opts: rdptest.Options{Width: 300, Height: 200}, width: 1024, height: 768},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newSessions()
			ts := start(t, Config{Handler: h})
			c, _ := ts.open(t, tt.opts)
			s := h.next(t)
			sink := newRecordSink()
			s.SetFlowSink(sink)
			ctx := gfxContext(t)
			g, err := c.StartGFX(ctx, []gfx.CapSet{gfx.NewCapSet(gfx.Version107, 0)})
			if err != nil {
				t.Fatal(err)
			}
			if calls := sink.wait(t, 1); calls[0] != "key frame" {
				t.Errorf("calls %q", calls)
			}
			waitFor(t, func() bool { return s.Video().Path() == VideoPlanar })
			if p := s.Video().Path(); p.String() != "planar" {
				t.Errorf("path %v", p)
			}

			dw, dh := s.DesktopSize()
			w, ht := tt.width, tt.height
			if w == 0 {
				w, ht = dw, dh
			}
			rng := rand.New(rand.NewPCG(uint64(i), 20))
			bounds := image.Rect(0, 0, min(w, dw), min(ht, dh))
			screen, want := rdptest.NewScreen(dw, dh), rdptest.NewScreen(dw, dh)
			var drawn rdptest.GFXDrawn
			for round := range 6 {
				img := testImage(rng, w, ht)
				var rects []image.Rectangle
				if round == 0 {
					rects = []image.Rectangle{image.Rect(-10, -10, w+10, ht+10)}
				}
				// A pixel goes uncompressed.
				x, y := rng.IntN(bounds.Dx()), rng.IntN(bounds.Dy())
				rects = append(rects, image.Rect(x, y, x+1, y+1))
				for range rng.IntN(8) + 1 {
					x, y := rng.IntN(w+40)-20, rng.IntN(ht+40)-20
					rects = append(rects, image.Rect(x, y, x+rng.IntN(300)+1, y+rng.IntN(200)+1))
				}
				if err := s.Video().SendRects(img, rects); err != nil {
					t.Fatal(err)
				}
				for _, r := range rects {
					paintRect(want, img, r.Intersect(bounds))
				}
				drawGFXFrame(t, ctx, c, g.ChannelID, screen, want, &drawn)
			}
			if drawn.Planar == 0 || drawn.Planar == drawn.Tiles {
				t.Errorf("%d tiles, %d of them Planar", drawn.Tiles, drawn.Planar)
			}
			for i, id := range drawn.Ended {
				if id != uint32(i+1) {
					t.Fatalf("frames %v", drawn.Ended)
				}
			}
			waitBacklog(t, s.Video(), Backlog{})

			// After the channel closes, the rectangles go as bitmaps.
			if err := c.SendDVC(ctx, func(w *wire.Writer) { dynvc.AppendClose(w, g.ChannelID) }); err != nil {
				t.Fatal(err)
			}
			ts.log.find(t, "dynamic channel closed by the client")
			waitFor(t, func() bool { return s.Video().Path() == VideoBitmaps })
			marker := &Image{Pix: []byte{^screen.Pix[0], 0, 0, 0xff}, Stride: 4, Width: 1, Height: 1}
			if err := s.Video().SendRects(marker, []image.Rectangle{image.Rect(0, 0, 1, 1)}); err != nil {
				t.Fatal(err)
			}
			paintRect(want, marker, image.Rect(0, 0, 1, 1))
			drawUntil(t, c, screen, want)
		})
	}
}
