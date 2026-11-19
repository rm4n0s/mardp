package rdp

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/rdp/internal/dynvc"
	"github.com/rm4n0s/mardp/rdp/internal/gfx"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// fakeClock is a clock the test moves.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// gfxOpen opens a session whose client supports the graphics pipeline;
// the pipeline's clock is the returned one.
func gfxOpen(t *testing.T) (*testServer, *rdptest.Client, *Session, *fakeClock) {
	t.Helper()
	h := newSessions()
	ts := start(t, Config{Handler: h})
	c, _ := ts.open(t, rdptest.Options{})
	s := h.next(t)
	if s.gfx == nil {
		t.Fatal("the session has no graphics pipeline")
	}
	clock := &fakeClock{t: time.Date(2026, 11, 19, 13, 45, 30, 250_000_000, time.UTC)}
	s.gfx.mu.Lock()
	s.gfx.now = clock.now
	s.gfx.mu.Unlock()
	return ts, c, s, clock
}

func gfxContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// TestGFX goes through the capability exchange with the sets of each
// version: the server confirms the newest it supports, as the client sent
// it, and creates the surface of the desktop.
func TestGFX(t *testing.T) {
	tests := []struct {
		name   string
		sets   []gfx.CapSet
		want   gfx.CapSet
		avc420 bool
	}{
		{"8.1 with AVC420", []gfx.CapSet{gfx.NewCapSet(gfx.Version8, 0), gfx.NewCapSet(gfx.Version81, gfx.FlagAVC420Enabled)},
			gfx.NewCapSet(gfx.Version81, gfx.FlagAVC420Enabled), true},
		{"8.1", []gfx.CapSet{gfx.NewCapSet(gfx.Version81, gfx.FlagThinClient)}, gfx.NewCapSet(gfx.Version81, gfx.FlagThinClient), false},
		{"10", []gfx.CapSet{gfx.NewCapSet(gfx.Version10, 0)}, gfx.NewCapSet(gfx.Version10, 0), true},
		{"10 without AVC", []gfx.CapSet{gfx.NewCapSet(gfx.Version10, gfx.FlagAVCDisabled)}, gfx.NewCapSet(gfx.Version10, gfx.FlagAVCDisabled), false},
		{"10.1", []gfx.CapSet{gfx.NewCapSet(gfx.Version101, 0)}, gfx.NewCapSet(gfx.Version101, 0), true},
		{"10.2", []gfx.CapSet{gfx.NewCapSet(gfx.Version102, gfx.FlagSmallCache)}, gfx.NewCapSet(gfx.Version102, gfx.FlagSmallCache), true},
		{"10.3", []gfx.CapSet{gfx.NewCapSet(gfx.Version103, gfx.FlagAVCThinClient)}, gfx.NewCapSet(gfx.Version103, gfx.FlagAVCThinClient), true},
		{"10.4", []gfx.CapSet{gfx.NewCapSet(gfx.Version104, 0)}, gfx.NewCapSet(gfx.Version104, 0), true},
		{"10.5", []gfx.CapSet{gfx.NewCapSet(gfx.Version105, gfx.FlagAVCDisabled)}, gfx.NewCapSet(gfx.Version105, gfx.FlagAVCDisabled), false},
		{"10.6", []gfx.CapSet{gfx.NewCapSet(gfx.Version106, 0)}, gfx.NewCapSet(gfx.Version106, 0), true},
		{"10.7", []gfx.CapSet{gfx.NewCapSet(gfx.Version107, gfx.FlagScaledMapDisable)}, gfx.NewCapSet(gfx.Version107, gfx.FlagScaledMapDisable), true},
		// FreeRDP's sets, newest last; a 10.1 with the wrong length and an
		// unknown version are skipped.
		{"newest of several", []gfx.CapSet{
			gfx.NewCapSet(gfx.Version8, 0), gfx.NewCapSet(gfx.Version81, gfx.FlagAVC420Enabled), gfx.NewCapSet(gfx.Version107, 0),
			gfx.NewCapSet(gfx.Version105, 0), {Version: 0x000B0000, Data: make([]byte, 4)},
		}, gfx.NewCapSet(gfx.Version107, 0), true},
		{"invalid newest", []gfx.CapSet{gfx.NewCapSet(gfx.Version10, 0), {Version: gfx.Version101, Data: make([]byte, 4)}},
			gfx.NewCapSet(gfx.Version10, 0), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts, c, s, _ := gfxOpen(t)
			ctx := gfxContext(t)
			g, err := c.StartGFX(ctx, tc.sets)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(g.Confirm, tc.want) {
				t.Errorf("confirmed %#x % x, want %#x % x", g.Confirm.Version, g.Confirm.Data, tc.want.Version, tc.want.Data)
			}
			w, h := uint32(rdptest.DefaultWidth), uint32(rdptest.DefaultHeight)
			wantReset := gfx.PDU{Cmd: gfx.CmdResetGraphics, Width: w, Height: h,
				Monitors: []gfx.Monitor{{Right: int32(w - 1), Bottom: int32(h - 1), Flags: gfx.MonitorPrimary}}}
			if !reflect.DeepEqual(g.Reset, wantReset) {
				t.Errorf("reset %+v", g.Reset)
			}
			wantSurface := gfx.PDU{Cmd: gfx.CmdCreateSurface, SurfaceID: 1, Width: w, Height: h, PixelFormat: gfx.PixelFormatXRGB}
			if !reflect.DeepEqual(g.Surface, wantSurface) {
				t.Errorf("surface %+v", g.Surface)
			}
			if wantMap := (gfx.PDU{Cmd: gfx.CmdMapSurfaceToOutput, SurfaceID: 1}); !reflect.DeepEqual(g.Map, wantMap) {
				t.Errorf("map %+v", g.Map)
			}
			rec := ts.log.find(t, "graphics pipeline ready")
			if v := attr(rec, "avc420").Bool(); v != tc.avc420 {
				t.Errorf("logged avc420 %v", v)
			}
			s.gfx.mu.Lock()
			ready, avc420 := s.gfx.ready, s.gfx.avc420
			s.gfx.mu.Unlock()
			if !ready || avc420 != tc.avc420 {
				t.Errorf("ready %v, avc420 %v", ready, avc420)
			}
		})
	}
}

// waitBacklog waits until the backlog is want.
func waitBacklog(t *testing.T, v *Video, want Backlog) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for v.Backlog() != want {
		if time.Now().After(deadline) {
			t.Fatalf("backlog %+v, want %+v", v.Backlog(), want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// readFrame reads a frame and checks its ID, timestamp and content.
func readFrame(t *testing.T, ctx context.Context, c *rdptest.Client, ch, id uint32, at time.Time, content ...gfx.PDU) {
	t.Helper()
	ps, err := c.ReadGFX(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]gfx.PDU{{Cmd: gfx.CmdStartFrame, Timestamp: gfx.Timestamp(at), FrameID: id}}, content...)
	want = append(want, gfx.PDU{Cmd: gfx.CmdEndFrame, FrameID: id})
	if !reflect.DeepEqual(ps, want) {
		t.Errorf("frame %+v, want %+v", ps, want)
	}
}

// emptyFrame is a frame with nothing between Start Frame and End Frame.
func emptyFrame(*wire.Writer) error { return nil }

// TestGFXBacklog checks that the backlog follows the acknowledgements: an
// acknowledgement takes its frame and the frames before it off the
// backlog, a suspension empties it and keeps it empty until the client
// acknowledges again.
func TestGFXBacklog(t *testing.T) {
	ts, c, s, clock := gfxOpen(t)
	ctx := gfxContext(t)
	v := s.Video()
	assertRoute(t, s.gfx.sendFrame(emptyFrame), "gfxPipeline.sendFrame->gfxPipeline.sendFrameLocked."+TagGFXNotReady)
	g, err := c.StartGFX(ctx, []gfx.CapSet{gfx.NewCapSet(gfx.Version107, 0)})
	if err != nil {
		t.Fatal(err)
	}
	ch := g.ChannelID
	waitBacklog(t, v, Backlog{})

	// Three frames: the first with a PDU in it, sent 10 ms apart.
	start := clock.now()
	fill := gfx.PDU{Cmd: gfx.CmdSolidFill, Body: []byte{1, 0, 0, 0, 0, 0}}
	if err := s.gfx.sendFrame(func(w *wire.Writer) error {
		off := gfx.Begin(w, gfx.CmdSolidFill)
		w.Write(fill.Body)
		return gfx.End(w, off)
	}); err != nil {
		t.Fatal(err)
	}
	readFrame(t, ctx, c, ch, 1, start, fill)
	for id := uint32(2); id <= 3; id++ {
		clock.add(10 * time.Millisecond)
		if err := s.gfx.sendFrame(emptyFrame); err != nil {
			t.Fatal(err)
		}
		readFrame(t, ctx, c, ch, id, clock.now())
	}
	waitBacklog(t, v, Backlog{Frames: 3, Oldest: 20 * time.Millisecond})
	clock.add(30 * time.Millisecond)
	waitBacklog(t, v, Backlog{Frames: 3, Oldest: 50 * time.Millisecond})

	if err := c.AckFrame(ctx, ch, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	waitBacklog(t, v, Backlog{Frames: 2, Oldest: 40 * time.Millisecond})
	// Frame 3's acknowledgement covers frame 2.
	if err := c.AckFrame(ctx, ch, 1, 3, 3); err != nil {
		t.Fatal(err)
	}
	waitBacklog(t, v, Backlog{})
	if err := c.AckFrame(ctx, ch, 1, 9, 3); err != nil {
		t.Fatal(err)
	}
	ts.log.find(t, "an acknowledgement of a frame not on its way")

	// Suspended: the frames on their way are forgotten, and new ones are
	// not counted.
	for id := uint32(4); id <= 5; id++ {
		if err := s.gfx.sendFrame(emptyFrame); err != nil {
			t.Fatal(err)
		}
		readFrame(t, ctx, c, ch, id, clock.now())
	}
	waitBacklog(t, v, Backlog{Frames: 2})
	if err := c.AckFrame(ctx, ch, gfx.SuspendFrameAcknowledgement, 4, 4); err != nil {
		t.Fatal(err)
	}
	waitBacklog(t, v, Backlog{})
	if err := s.gfx.sendFrame(emptyFrame); err != nil {
		t.Fatal(err)
	}
	readFrame(t, ctx, c, ch, 6, clock.now())
	if v.Backlog() != (Backlog{}) {
		t.Errorf("backlog while suspended %+v", v.Backlog())
	}
	// An acknowledgement resumes them; queue depth 0 means unknown.
	if err := c.AckFrame(ctx, ch, 0, 6, 6); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		s.gfx.mu.Lock()
		defer s.gfx.mu.Unlock()
		return !s.gfx.suspended
	})
	if err := s.gfx.sendFrame(emptyFrame); err != nil {
		t.Fatal(err)
	}
	readFrame(t, ctx, c, ch, 7, clock.now())
	waitBacklog(t, v, Backlog{Frames: 1})

	// The server imports no cache entries; QoE acknowledgements and
	// unknown commands are ignored.
	if err := c.SendGFX(ctx, ch, func(w *wire.Writer) {
		gfx.AppendQoEFrameAcknowledge(w, 7, 1, 2, 3)
		off := gfx.Begin(w, gfx.CmdSolidFill)
		if err := gfx.End(w, off); err != nil {
			t.Error(err)
		}
		gfx.AppendCacheImportOffer(w, []uint64{1, 2, 3})
	}); err != nil {
		t.Fatal(err)
	}
	ps, err := c.ReadGFX(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if want := []gfx.PDU{{Cmd: gfx.CmdCacheImportReply, CacheSlots: []uint16{}}}; !reflect.DeepEqual(ps, want) {
		t.Errorf("cache import reply %+v", ps)
	}
	if ts.log.count("graphics PDU ignored") != 1 {
		t.Errorf("ignored %d PDUs", ts.log.count("graphics PDU ignored"))
	}
	if p := v.Path(); p != VideoPlanar {
		t.Errorf("path %v", p)
	}
}

// waitFor waits until cond holds.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("the condition did not come true")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestGFXReadvertise sends a second Caps Advertise: after 10.3 or newer
// the server starts over, after an older version it ignores it.
func TestGFXReadvertise(t *testing.T) {
	t.Run("10.3", func(t *testing.T) {
		_, c, s, _ := gfxOpen(t)
		ctx := gfxContext(t)
		g, err := c.StartGFX(ctx, []gfx.CapSet{gfx.NewCapSet(gfx.Version103, 0)})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.gfx.sendFrame(emptyFrame); err != nil {
			t.Fatal(err)
		}
		if _, err := c.ReadGFX(ctx, g.ChannelID); err != nil {
			t.Fatal(err)
		}
		waitBacklog(t, s.Video(), Backlog{Frames: 1})
		g2, err := c.AdvertiseGFX(ctx, g.ChannelID, []gfx.CapSet{gfx.NewCapSet(gfx.Version105, gfx.FlagAVCDisabled)})
		if err != nil {
			t.Fatal(err)
		}
		if g2.Confirm.Version != gfx.Version105 || g2.Surface.SurfaceID != 1 {
			t.Errorf("second start %+v", g2)
		}
		waitBacklog(t, s.Video(), Backlog{})
		s.gfx.mu.Lock()
		avc420 := s.gfx.avc420
		s.gfx.mu.Unlock()
		if avc420 {
			t.Error("H.264 still allowed")
		}
		// Frames go on from the next ID.
		if err := s.gfx.sendFrame(emptyFrame); err != nil {
			t.Fatal(err)
		}
		ps, err := c.ReadGFX(ctx, g.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		if ps[0].Cmd != gfx.CmdStartFrame || ps[0].FrameID != 2 {
			t.Errorf("frame %+v", ps[0])
		}
	})
	t.Run("10.2", func(t *testing.T) {
		ts, c, s, _ := gfxOpen(t)
		ctx := gfxContext(t)
		g, err := c.StartGFX(ctx, []gfx.CapSet{gfx.NewCapSet(gfx.Version102, 0)})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.SendGFX(ctx, g.ChannelID, func(w *wire.Writer) {
			gfx.AppendCapsAdvertise(w, []gfx.CapSet{gfx.NewCapSet(gfx.Version107, 0)})
		}); err != nil {
			t.Fatal(err)
		}
		ts.log.find(t, "a second graphics Caps Advertise ignored")
		if err := s.gfx.sendFrame(emptyFrame); err != nil {
			t.Fatal(err)
		}
		ps, err := c.ReadGFX(ctx, g.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		if ps[0].Cmd != gfx.CmdStartFrame {
			t.Errorf("after the ignored advertise %+v", ps)
		}
	})
}

// TestGFXNoCommonVersion advertises only sets the server does not take:
// it closes the channel.
func TestGFXNoCommonVersion(t *testing.T) {
	ts, c, s, _ := gfxOpen(t)
	ctx := gfxContext(t)
	if _, err := c.AnswerDVCCaps(ctx, dynvc.Version2); err != nil {
		t.Fatal(err)
	}
	ch, err := c.AcceptGFX(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sets := []gfx.CapSet{gfx.NewCapSet(gfx.Version8, 0), {Version: gfx.Version101, Data: make([]byte, 4)}, {Version: 0x000B0000, Data: make([]byte, 4)}}
	if err := c.SendGFX(ctx, ch, func(w *wire.Writer) { gfx.AppendCapsAdvertise(w, sets) }); err != nil {
		t.Fatal(err)
	}
	p, err := c.ReadDVC(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p.Cmd != dynvc.CmdClose || p.ChannelID != ch {
		t.Errorf("not a close: %+v", p)
	}
	rec := ts.log.find(t, "no graphics pipeline version in common; closing it")
	if v, _ := attr(rec, "versions").Any().([]uint32); !reflect.DeepEqual(v, []uint32{gfx.Version8, gfx.Version101, 0x000B0000}) {
		t.Errorf("logged versions %v", attr(rec, "versions"))
	}
	assertRoute(t, s.gfx.sendFrame(emptyFrame), "gfxPipeline.sendFrame->gfxPipeline.sendFrameLocked."+TagGFXNotReady)
}

// TestGFXUnavailable covers the sessions without the graphics pipeline: a
// client that does not support it, one that refuses the channel, and one
// that closes it.
func TestGFXUnavailable(t *testing.T) {
	t.Run("not supported", func(t *testing.T) {
		h := newSessions()
		ts := start(t, Config{Handler: h})
		ts.open(t, rdptest.Options{EditClientData: noGFX})
		s := h.next(t)
		if s.gfx != nil || s.Video().Backlog() != (Backlog{}) {
			t.Errorf("gfx %v, backlog %+v", s.gfx, s.Video().Backlog())
		}
	})
	t.Run("no drdynvc", func(t *testing.T) {
		h := newSessions()
		ts := start(t, Config{Handler: h})
		ts.open(t, rdptest.Options{Channels: []string{"rdpsnd"}})
		if s := h.next(t); s.gfx != nil {
			t.Error("a graphics pipeline without drdynvc")
		}
	})
	t.Run("refused", func(t *testing.T) {
		ts, c, s, _ := gfxOpen(t)
		ctx := gfxContext(t)
		if _, err := c.AnswerDVCCaps(ctx, dynvc.Version2); err != nil {
			t.Fatal(err)
		}
		if _, err := c.AnswerDVCCreate(ctx, -0x7fffbffb); err != nil {
			t.Fatal(err)
		}
		ts.log.find(t, "graphics pipeline unavailable")
		assertRoute(t, s.gfx.sendFrame(emptyFrame), "gfxPipeline.sendFrame->gfxPipeline.sendFrameLocked."+TagGFXNotReady)
	})
	t.Run("closed", func(t *testing.T) {
		ts, c, s, _ := gfxOpen(t)
		ctx := gfxContext(t)
		g, err := c.StartGFX(ctx, []gfx.CapSet{gfx.NewCapSet(gfx.Version107, 0)})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.gfx.sendFrame(emptyFrame); err != nil {
			t.Fatal(err)
		}
		waitBacklog(t, s.Video(), Backlog{Frames: 1})
		if err := c.SendDVC(ctx, func(w *wire.Writer) { dynvc.AppendClose(w, g.ChannelID) }); err != nil {
			t.Fatal(err)
		}
		ts.log.find(t, "dynamic channel closed by the client")
		waitBacklog(t, s.Video(), Backlog{})
		assertRoute(t, s.gfx.sendFrame(emptyFrame), "gfxPipeline.sendFrame->gfxPipeline.sendFrameLocked."+TagGFXNotReady)
	})
}

// TestGFXHostile sends graphics PDUs that do not parse: each ends the
// session with its tagged error.
func TestGFXHostile(t *testing.T) {
	const prefix = "sessionReader.static->dvcManager.receive->dvcManager.data->gfxPipeline.dvcReceived->ReadClient->readPDUs"
	tests := []struct {
		name  string
		msg   []byte
		route string
	}{
		{"length too long", []byte{0x0d, 0, 0, 0, 0x20, 0, 0, 0}, prefix + "." + gfx.TagLengthInvalid},
		{"capability set too long", []byte{0x12, 0, 0, 0, 0x12, 0, 0, 0, 1, 0, 4, 0, 8, 0, 9, 0, 0, 0},
			prefix + "->parseClient->readCapSet." + gfx.TagCapsInvalid},
		{"too many cache entries", []byte{0x10, 0, 0, 0, 0x0a, 0, 0, 0, 0x56, 0x15}, prefix + "->parseClient." + gfx.TagCountInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newSessions()
			ts := start(t, Config{Handler: h})
			c, _ := ts.open(t, rdptest.Options{})
			h.next(t)
			ctx := gfxContext(t)
			if _, err := c.AnswerDVCCaps(ctx, dynvc.Version2); err != nil {
				t.Fatal(err)
			}
			ch, err := c.AcceptGFX(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.SendDVCMessage(ctx, ch, tc.msg); err != nil {
				t.Fatal(err)
			}
			h.waitEnded(t)
			rec := ts.log.find(t, "connection ended")
			err, _ = attr(rec, "err").Any().(error)
			assertRoute(t, err, tc.route)
		})
	}
}
