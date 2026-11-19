package rdp

import (
	"image"
	"sync"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/bitmap"
	"github.com/rm4n0s/mardp/rdp/internal/gfx"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// TagGFXNotReady: a frame for the graphics pipeline before the client
// confirmed its capabilities and the surface was created, or after the
// channel closed.
const TagGFXNotReady = "VideoGFXNotReady"

// TagH264Unavailable: an H.264 frame for a session whose video path is not
// VideoAVC420.
const TagH264Unavailable = "VideoH264Unavailable"

// TagKeyFrameWanted: an H.264 frame that is not a key frame, for a surface
// that has had no key frame since it was created.
const TagKeyFrameWanted = "VideoKeyFrameWanted"

// TagFrameEmpty: an H.264 frame without data.
const TagFrameEmpty = "VideoFrameEmpty"

// The quantization and quality the AVC420 metablock states for a frame.
// The client SHOULD NOT use them (MS-RDPEGFX 2.2.4.4.1), and the encoder's
// own vary, so these are the values of a good picture.
const (
	avc420QP      = 22
	avc420Quality = 100
)

// gfxChannelName is the dynamic channel of the graphics pipeline
// (MS-RDPEGFX 2.1).
const gfxChannelName = "Microsoft::Windows::RDS::Graphics"

// gfxSurfaceID is the one surface the server creates: the desktop, mapped
// to the output at 0, 0 (SPEC §9.1).
const gfxSurfaceID uint16 = 1

// delayWeight is the weight of the average of the frames' delay: each
// acknowledgement moves it by 1/delayWeight of the difference.
const delayWeight = 8

// gfxSentFrame is a frame the client has not acknowledged yet.
type gfxSentFrame struct {
	id uint32
	at time.Time
}

// gfxPipeline is the session's end of the graphics pipeline (SPEC §9.1):
// the capability exchange, the surface, frames and their
// acknowledgements. Its methods are called on the session's reading
// goroutine, sendFrame and backlog from any.
type gfxPipeline struct {
	s *Session
	// now is the clock of the frames' timestamps and ages.
	now func() time.Time

	mu sync.Mutex
	d  *dvc
	// confirmed is the set the server confirmed; ready is set once the
	// surface is created, and cleared when the channel closes.
	confirmed gfx.CapSet
	ready     bool
	// avc420 says whether the client decodes H.264 in YUV420 mode;
	// keyed is set once a key frame went to the surface.
	avc420, keyed bool
	// next is the ID of the next frame.
	next uint32
	// unacked are the frames sent and not acknowledged, oldest first.
	// suspended is set while the client has suspended acknowledgements.
	unacked   []gfxSentFrame
	suspended bool
	// acked counts the frames acknowledged; delay is the time from a
	// frame's send to its acknowledgement, averaged over the last frames.
	acked uint64
	delay time.Duration
	// raw and wrapped build the messages, region and quant an H.264
	// frame's metablock; guarded by mu.
	raw, wrapped wire.Writer
	region       [1]gfx.Rect16
	quant        [1]gfx.QuantQuality
}

// startGFX opens the graphics pipeline's channel when the client said it
// supports the pipeline and joined drdynvc.
func (s *Session) startGFX() {
	if !s.client.GraphicsPipeline || s.dvc == nil {
		return
	}
	g := &gfxPipeline{s: s, now: time.Now, next: 1}
	d, err := s.openDVC(gfxChannelName, g)
	if err != nil {
		s.c.log.Debug("opening the graphics pipeline", "err", err)
		return
	}
	g.d = d
	s.gfx = g
}

func (g *gfxPipeline) dvcOpened(*dvc) {}

func (g *gfxPipeline) dvcRefused(_ *dvc, status int32) {
	g.s.c.log.Info("graphics pipeline unavailable", "status", uint32(status))
}

func (g *gfxPipeline) dvcClosed(*dvc) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ready = false
	g.unacked = nil
}

// dvcReceived handles a message of the client: one or more PDUs.
func (g *gfxPipeline) dvcReceived(_ *dvc, msg []byte) error {
	return gfx.ReadClient(msg, g.handle)
}

func (g *gfxPipeline) handle(p gfx.PDU) error {
	switch p.Cmd {
	case gfx.CmdCapsAdvertise:
		ready, err := g.caps(p.CapSets)
		if ready {
			// The new surface is black until the whole desktop is sent.
			g.s.wantKeyFrame()
		}
		return err
	case gfx.CmdFrameAcknowledge:
		g.ack(p)
		return nil
	case gfx.CmdCacheImportOffer:
		// The server keeps no cache, so it imports none of the entries.
		return g.send(queueControl, func(w *wire.Writer) error { gfx.AppendCacheImportReply(w, nil); return nil })
	case gfx.CmdQoEFrameAcknowledge:
		return nil
	}
	// An unexpected command SHOULD be ignored (MS-RDPEGFX 3.1.5.1).
	g.s.c.log.Debug("graphics PDU ignored", "cmd", p.Cmd, "length", len(p.Body))
	return nil
}

// gfxChoose returns the newest of sets that the server supports: 8.1 and
// the 10.x versions, with the data their version states.
func gfxChoose(sets []gfx.CapSet) (gfx.CapSet, bool) {
	var best gfx.CapSet
	found := false
	for _, c := range sets {
		if c.Version == gfx.Version8 || !c.Valid() {
			continue
		}
		if !found || c.Version > best.Version {
			best, found = c, true
		}
	}
	return best, found
}

// caps handles the client's Caps Advertise: the server confirms the newest
// set both have, echoing the client's flags, and creates the surface of the
// desktop, and reports whether it did. With none in common it closes the
// channel. A second advertise starts over after a confirmed 10.3 or newer
// (MS-RDPEGFX 3.2.5.19), and is ignored after an older one.
func (g *gfxPipeline) caps(sets []gfx.CapSet) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.confirmed.Version != 0 {
		if g.confirmed.Version < gfx.Version103 {
			g.s.c.log.Debug("a second graphics Caps Advertise ignored", "confirmed", g.confirmed.Version)
			return false, nil
		}
		g.ready, g.unacked, g.suspended = false, nil, false
	}
	c, ok := gfxChoose(sets)
	if !ok {
		versions := make([]uint32, len(sets))
		for i, c := range sets {
			versions[i] = c.Version
		}
		g.s.c.log.Warn("no graphics pipeline version in common; closing it", "versions", versions)
		return false, g.d.close()
	}
	c.Data = append([]byte(nil), c.Data...)
	err := g.sendLocked(queueControl, func(w *wire.Writer) error { gfx.AppendCapsConfirm(w, c); return nil })
	if err != nil {
		return false, err
	}
	g.confirmed, g.avc420 = c, c.AVC420()
	s := g.s
	err = g.sendLocked(queueControl, func(w *wire.Writer) error {
		m := gfx.Monitor{Right: int32(s.width - 1), Bottom: int32(s.height - 1), Flags: gfx.MonitorPrimary}
		if err := gfx.AppendResetGraphics(w, uint32(s.width), uint32(s.height), []gfx.Monitor{m}); err != nil {
			return err
		}
		if err := gfx.AppendCreateSurface(w, gfxSurfaceID, s.width, s.height, gfx.PixelFormatXRGB); err != nil {
			return err
		}
		gfx.AppendMapSurfaceToOutput(w, gfxSurfaceID, 0, 0)
		return nil
	})
	if err != nil {
		return false, err
	}
	g.ready, g.keyed = true, false
	s.c.log.Info("graphics pipeline ready", "version", c.Version, "flags", c.Flags(), "avc420", g.avc420)
	return true, nil
}

// ack handles a Frame Acknowledge: the frame and those sent before it are
// acknowledged. A queue depth of SUSPEND forgets the frames on their way
// and expects no acknowledgements until the next one (MS-RDPEGFX
// 3.2.5.13).
func (g *gfxPipeline) ack(p gfx.PDU) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if p.QueueDepth == gfx.SuspendFrameAcknowledgement {
		g.suspended, g.unacked = true, nil
		return
	}
	g.suspended = false
	for i, f := range g.unacked {
		if f.id == p.FrameID {
			g.acked += uint64(i + 1)
			d := g.now().Sub(f.at)
			if g.delay == 0 {
				g.delay = d
			} else {
				g.delay += (d - g.delay) / delayWeight
			}
			g.unacked = append(g.unacked[:0], g.unacked[i+1:]...)
			return
		}
	}
	g.s.c.log.Debug("an acknowledgement of a frame not on its way", "frame", p.FrameID)
}

// send sends on q the PDUs that build appends, as one message.
func (g *gfxPipeline) send(q queue, build func(w *wire.Writer) error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sendLocked(q, build)
}

// sendLocked is send with g.mu held.
func (g *gfxPipeline) sendLocked(q queue, build func(w *wire.Writer) error) error {
	g.raw.Reset()
	if err := build(&g.raw); err != nil {
		return err
	}
	g.wrapped.Reset()
	gfx.Wrap(&g.wrapped, g.raw.Bytes())
	return g.d.send(q, g.wrapped.Bytes())
}

// sendFrame sends one frame on the video queue: Start Frame, what build
// appends, End Frame. The frame is on its way until the client
// acknowledges it.
func (g *gfxPipeline) sendFrame(build func(w *wire.Writer) error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sendFrameLocked(build)
}

// sendFrameLocked is sendFrame with g.mu held.
func (g *gfxPipeline) sendFrameLocked(build func(w *wire.Writer) error) error {
	if !g.ready {
		return errors.New(TagGFXNotReady, "a frame before the graphics pipeline is ready", "confirmed", g.confirmed.Version)
	}
	id, at := g.next, g.now()
	err := g.sendLocked(queueVideo, func(w *wire.Writer) error {
		gfx.AppendStartFrame(w, gfx.Timestamp(at), id)
		if err := build(w); err != nil {
			return err
		}
		gfx.AppendEndFrame(w, id)
		return nil
	})
	if err != nil {
		return err
	}
	g.next++
	if !g.suspended {
		g.unacked = append(g.unacked, gfxSentFrame{id: id, at: at})
	}
	return nil
}

// path returns the video path of the pipeline's state.
func (g *gfxPipeline) path() VideoPath {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case !g.ready:
		return VideoBitmaps
	case g.avc420:
		return VideoAVC420
	}
	return VideoPlanar
}

// sendAVC420 sends an H.264 frame of the whole surface.
func (g *gfxPipeline) sendAVC420(annexB []byte, keyFrame bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.ready || !g.avc420 {
		return errors.New(TagH264Unavailable, "an H.264 frame for a client that does not take it now",
			"ready", g.ready, "avc420", g.avc420)
	}
	if !g.keyed && !keyFrame {
		return errors.New(TagKeyFrameWanted, "an H.264 frame that depends on frames the client does not have",
			"length", len(annexB))
	}
	s := g.s
	err := g.sendFrameLocked(func(w *wire.Writer) error {
		r := gfx.Rect16{Right: uint16(s.width), Bottom: uint16(s.height)}
		off := gfx.BeginWireToSurface1(w, gfxSurfaceID, gfx.PixelFormatXRGB, r)
		g.region[0], g.quant[0] = r, gfx.QuantQuality{QP: avc420QP, Quality: avc420Quality}
		err := gfx.AppendAVC420(w, gfx.AVC420{Regions: g.region[:], Quant: g.quant[:], Bitstream: annexB})
		if err != nil {
			return err
		}
		return gfx.EndWireToSurface1(w, off, gfx.CodecAVC420)
	})
	if err != nil {
		return err
	}
	g.keyed = true
	return nil
}

// sendTiles sends rects of img, clipped to bounds, as one frame of tiles
// when the pipeline is ready, and reports whether it was.
func (g *gfxPipeline) sendTiles(img *bitmap.Image, rects []image.Rectangle, bounds image.Rectangle) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.ready {
		return false, nil
	}
	return true, g.sendFrameLocked(func(w *wire.Writer) error { return appendTiles(w, img, rects, bounds) })
}

// wantKeyFrame tells the flow sink that the client needs the whole desktop,
// unless output is suppressed: its resume asks for it then.
func (s *Session) wantKeyFrame() {
	if s.suppressed.Load() {
		return
	}
	s.mu.Lock()
	f := s.flow
	s.mu.Unlock()
	if f != nil {
		f.KeyFrameWanted()
	}
}

// SendFrame sends an H.264 frame of the whole desktop, Annex B byte
// stream, without waiting for the network (SPEC §6, §9.3): one graphics
// frame with a Wire To Surface 1 of the AVC420 stream, in one packet of the
// video queue (TagQueueFull when it is full). It works while Path is
// VideoAVC420, and fails with TagH264Unavailable otherwise. The first frame
// on a surface must be a key frame (TagKeyFrameWanted): the flow sink's
// KeyFrameWanted says when one is due. annexB is not used after SendFrame
// returns.
func (v *Video) SendFrame(annexB []byte, keyFrame bool) error {
	if len(annexB) == 0 {
		return errors.New(TagFrameEmpty, "an H.264 frame without data", "keyFrame", keyFrame)
	}
	g := v.s.gfx
	if g == nil {
		return errors.New(TagH264Unavailable, "an H.264 frame for a session without the graphics pipeline",
			"graphicsPipeline", v.s.client.GraphicsPipeline)
	}
	return g.sendAVC420(annexB, keyFrame)
}

// Backlog is the video sent and not yet acknowledged by the client (SPEC
// §6, §9.5).
type Backlog struct {
	// Frames is the number of frames on their way.
	Frames int
	// Oldest is how long ago the oldest of them was sent.
	Oldest time.Duration
	// Queued is the number of packets of the session's video queue not yet
	// written to the connection, on every path.
	Queued int
}

// Backlog returns the frames sent through the graphics pipeline that the
// client has not acknowledged, and the video still in the session's queue.
// Frames and Oldest are zero on the bitmap path, which has no
// acknowledgements, and while the client has suspended them.
func (v *Video) Backlog() Backlog {
	b := Backlog{Queued: v.s.wr.pending(queueVideo)}
	g := v.s.gfx
	if g == nil {
		return b
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.unacked) > 0 {
		b.Frames, b.Oldest = len(g.unacked), g.now().Sub(g.unacked[0].at)
	}
	return b
}
