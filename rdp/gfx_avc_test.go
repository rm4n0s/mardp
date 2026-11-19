package rdp

import (
	"bytes"
	"context"
	"image"
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/rm4n0s/mardp/rdp/internal/gfx"
	"github.com/rm4n0s/mardp/rdp/internal/input"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// annexB is an H.264 frame of n bytes for the tests: a start code, then
// random bytes.
func annexB(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.Uint32())
	}
	copy(b, []byte{0, 0, 0, 1, 0x65})
	return b
}

// readAVC420 reads a frame with one H.264 frame of the whole desktop and
// returns its ID and the Annex B data.
func readAVC420(t *testing.T, ctx context.Context, c *rdptest.Client, ch uint32) (uint32, []byte) {
	t.Helper()
	ps, err := c.ReadGFX(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 3 || ps[0].Cmd != gfx.CmdStartFrame || ps[1].Cmd != gfx.CmdWireToSurface1 ||
		ps[2].Cmd != gfx.CmdEndFrame || ps[2].FrameID != ps[0].FrameID {
		t.Fatalf("not a frame of one Wire To Surface 1: %+v", ps)
	}
	desktop := gfx.Rect16{Right: rdptest.DefaultWidth, Bottom: rdptest.DefaultHeight}
	p := ps[1]
	if p.SurfaceID != 1 || p.CodecID != gfx.CodecAVC420 || p.PixelFormat != gfx.PixelFormatXRGB || p.Rect != desktop {
		t.Errorf("wire to surface %+v", p)
	}
	a, err := gfx.ReadAVC420(p.Body)
	if err != nil {
		t.Fatal(err)
	}
	if want := []gfx.Rect16{desktop}; !reflect.DeepEqual(a.Regions, want) {
		t.Errorf("regions %+v", a.Regions)
	}
	if want := []gfx.QuantQuality{{QP: 22, Quality: 100}}; !reflect.DeepEqual(a.Quant, want) {
		t.Errorf("quantization %+v", a.Quant)
	}
	return ps[0].FrameID, a.Bitstream
}

// TestH264 sends H.264 frames: rdptest gets back the Annex B data it was
// sent, one frame each, after the key frame the surface needs.
func TestH264(t *testing.T) {
	_, c, s, _ := gfxOpen(t)
	ctx := gfxContext(t)
	sink := newRecordSink()
	s.SetFlowSink(sink)
	v := s.Video()
	rng := rand.New(rand.NewPCG(30, 31))
	assertRoute(t, v.SendFrame(annexB(rng, 100), true), "Video.SendFrame->gfxPipeline.sendAVC420."+TagH264Unavailable)
	g, err := c.StartGFX(ctx, []gfx.CapSet{gfx.NewCapSet(gfx.Version81, gfx.FlagAVC420Enabled), gfx.NewCapSet(gfx.Version107, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if calls := sink.wait(t, 1); calls[0] != "key frame" {
		t.Errorf("calls %q", calls)
	}
	if p := v.Path(); p != VideoAVC420 || p.String() != "avc420" {
		t.Errorf("path %v", p)
	}
	assertRoute(t, v.SendFrame(annexB(rng, 100), false), "Video.SendFrame->gfxPipeline.sendAVC420."+TagKeyFrameWanted)

	// A key frame, a small P-frame, and one large enough for multipart
	// segments and many DVC PDUs.
	sizes := []int{40_000, 300, 200_000}
	for i, n := range sizes {
		frame := annexB(rng, n)
		if err := v.SendFrame(frame, i == 0); err != nil {
			t.Fatal(err)
		}
		id, got := readAVC420(t, ctx, c, g.ChannelID)
		if id != uint32(i+1) || !bytes.Equal(got, frame) {
			t.Fatalf("frame %d: ID %d, %d bytes, want %d", i, id, len(got), n)
		}
	}
	waitBacklog(t, v, Backlog{Frames: 3})
	if err := c.AckFrame(ctx, g.ChannelID, 0, 3, 3); err != nil {
		t.Fatal(err)
	}
	waitBacklog(t, v, Backlog{})

	// Planar rectangles still go through the pipeline on this path.
	if err := v.SendRects(testImage(rng, 64, 64), []image.Rectangle{image.Rect(0, 0, 64, 64)}); err != nil {
		t.Fatal(err)
	}
	ps, err := c.ReadGFX(ctx, g.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if d, err := rdptest.NewScreen(rdptest.DefaultWidth, rdptest.DefaultHeight).DrawGFX(ps); err != nil || d.Tiles != 1 || len(d.Ended) != 1 {
		t.Errorf("drawn %+v, %v", d, err)
	}
}

// TestH264Readvertise starts over with a second Caps Advertise: the new
// surface needs a key frame again, and a client that no longer takes
// H.264 gets none.
func TestH264Readvertise(t *testing.T) {
	_, c, s, _ := gfxOpen(t)
	ctx := gfxContext(t)
	v := s.Video()
	rng := rand.New(rand.NewPCG(32, 33))
	g, err := c.StartGFX(ctx, []gfx.CapSet{gfx.NewCapSet(gfx.Version104, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.SendFrame(annexB(rng, 50), true); err != nil {
		t.Fatal(err)
	}
	readAVC420(t, ctx, c, g.ChannelID)
	if err := v.SendFrame(annexB(rng, 50), false); err != nil {
		t.Fatal(err)
	}
	readAVC420(t, ctx, c, g.ChannelID)
	if _, err := c.AdvertiseGFX(ctx, g.ChannelID, []gfx.CapSet{gfx.NewCapSet(gfx.Version106, 0)}); err != nil {
		t.Fatal(err)
	}
	assertRoute(t, v.SendFrame(annexB(rng, 50), false), "Video.SendFrame->gfxPipeline.sendAVC420."+TagKeyFrameWanted)
	if err := v.SendFrame(annexB(rng, 50), true); err != nil {
		t.Fatal(err)
	}
	readAVC420(t, ctx, c, g.ChannelID)

	if _, err := c.AdvertiseGFX(ctx, g.ChannelID, []gfx.CapSet{gfx.NewCapSet(gfx.Version107, gfx.FlagAVCDisabled)}); err != nil {
		t.Fatal(err)
	}
	if p := v.Path(); p != VideoPlanar {
		t.Errorf("path %v", p)
	}
	assertRoute(t, v.SendFrame(annexB(rng, 50), true), "Video.SendFrame->gfxPipeline.sendAVC420."+TagH264Unavailable)
}

func TestH264Errors(t *testing.T) {
	_, _, s, _ := gfxOpen(t)
	assertRoute(t, s.Video().SendFrame(nil, true), "Video.SendFrame."+TagFrameEmpty)

	h := newSessions()
	ts := start(t, Config{Handler: h})
	ts.open(t, rdptest.Options{EditClientData: noGFX})
	s = h.next(t)
	if p := s.Video().Path(); p != VideoBitmaps {
		t.Errorf("path %v", p)
	}
	assertRoute(t, s.Video().SendFrame([]byte{0, 0, 0, 1}, true), "Video.SendFrame."+TagH264Unavailable)
}

// TestGFXSuppressed creates the surface while output is suppressed: the
// key frame is asked for when output resumes, not before.
func TestGFXSuppressed(t *testing.T) {
	_, c, s, _ := gfxOpen(t)
	ctx := gfxContext(t)
	sink := newRecordSink()
	s.SetFlowSink(sink)
	if err := c.SendSuppressOutput(ctx, input.SuppressOutput{}); err != nil {
		t.Fatal(err)
	}
	sink.wait(t, 1)
	if _, err := c.StartGFX(ctx, []gfx.CapSet{gfx.NewCapSet(gfx.Version107, 0)}); err != nil {
		t.Fatal(err)
	}
	if err := c.SendSuppressOutput(ctx, input.SuppressOutput{Allow: true, Area: input.Rect{Right: 1023, Bottom: 767}}); err != nil {
		t.Fatal(err)
	}
	if calls, want := sink.wait(t, 3), []string{"suppressed true", "suppressed false", "key frame"}; !reflect.DeepEqual(calls, want) {
		t.Errorf("calls %q, want %q", calls, want)
	}
}
