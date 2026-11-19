package rdp

import (
	"sync"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/gfx"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// TagGFXNotReady: a frame for the graphics pipeline before the client
// confirmed its capabilities and the surface was created, or after the
// channel closed.
const TagGFXNotReady = "VideoGFXNotReady"

// gfxChannelName is the dynamic channel of the graphics pipeline
// (MS-RDPEGFX 2.1).
const gfxChannelName = "Microsoft::Windows::RDS::Graphics"

// gfxSurfaceID is the one surface the server creates: the desktop, mapped
// to the output at 0, 0 (SPEC §9.1).
const gfxSurfaceID uint16 = 1

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
	// avc420 says whether the client decodes H.264 in YUV420 mode.
	avc420 bool
	// next is the ID of the next frame.
	next uint32
	// unacked are the frames sent and not acknowledged, oldest first.
	// suspended is set while the client has suspended acknowledgements.
	unacked   []gfxSentFrame
	suspended bool
	// raw and wrapped build the messages; guarded by mu.
	raw, wrapped wire.Writer
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
		return g.caps(p.CapSets)
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
// desktop. With none in common it closes the channel. A second advertise
// starts over after a confirmed 10.3 or newer (MS-RDPEGFX 3.2.5.19), and
// is ignored after an older one.
func (g *gfxPipeline) caps(sets []gfx.CapSet) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.confirmed.Version != 0 {
		if g.confirmed.Version < gfx.Version103 {
			g.s.c.log.Debug("a second graphics Caps Advertise ignored", "confirmed", g.confirmed.Version)
			return nil
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
		return g.d.close()
	}
	c.Data = append([]byte(nil), c.Data...)
	err := g.sendLocked(queueControl, func(w *wire.Writer) error { gfx.AppendCapsConfirm(w, c); return nil })
	if err != nil {
		return err
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
		return err
	}
	g.ready = true
	s.c.log.Info("graphics pipeline ready", "version", c.Version, "flags", c.Flags(), "avc420", g.avc420)
	return nil
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

// Backlog is the video sent and not yet acknowledged by the client (SPEC
// §6, §9.5).
type Backlog struct {
	// Frames is the number of frames on their way.
	Frames int
	// Oldest is how long ago the oldest of them was sent.
	Oldest time.Duration
}

// Backlog returns the frames sent through the graphics pipeline that the
// client has not acknowledged. It is zero on the bitmap path, which has no
// acknowledgements, and while the client has suspended them.
func (v *Video) Backlog() Backlog {
	g := v.s.gfx
	if g == nil {
		return Backlog{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.unacked) == 0 {
		return Backlog{}
	}
	return Backlog{Frames: len(g.unacked), Oldest: g.now().Sub(g.unacked[0].at)}
}
