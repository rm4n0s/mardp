package rdptest

import (
	"context"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/dynvc"
	"github.com/rm4n0s/mardp/rdp/internal/gfx"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// GFXChannelName is the dynamic channel of the graphics pipeline.
const GFXChannelName = "Microsoft::Windows::RDS::Graphics"

// GFX is what the server sent to start the graphics pipeline.
type GFX struct {
	// ChannelID is the dynamic channel's ID.
	ChannelID uint32
	// Confirm is the capability set the server confirmed.
	Confirm gfx.CapSet
	// Reset, Surface and Map are its Reset Graphics, Create Surface and
	// Map Surface To Output.
	Reset, Surface, Map gfx.PDU
}

// ReadGFX reads the server's next message on the graphics channel id and
// returns its PDUs, whose data is the caller's.
func (c *Client) ReadGFX(ctx context.Context, id uint32) ([]gfx.PDU, error) {
	msg, _, err := c.ReadDVCMessage(ctx, id)
	if err != nil {
		return nil, err
	}
	data, err := gfx.Unwrap(msg, nil)
	if err != nil {
		return nil, err
	}
	var ps []gfx.PDU
	err = gfx.ReadServer(data, func(p gfx.PDU) error { ps = append(ps, p); return nil })
	return ps, err
}

// SendGFX sends on the graphics channel id the PDUs that build appends,
// as one message; a client does not wrap them in segments.
func (c *Client) SendGFX(ctx context.Context, id uint32, build func(w *wire.Writer)) error {
	w := wire.NewWriter(nil)
	build(&w)
	return c.SendDVCMessage(ctx, id, w.Bytes())
}

// StartGFX answers the dynamic channels' capabilities and the Create
// Request of the graphics channel, advertises sets, and reads the server's
// Caps Confirm, Reset Graphics, Create Surface and Map Surface To Output.
// A request for another channel, or other PDUs, fail it with
// TagPDUUnexpected.
func (c *Client) StartGFX(ctx context.Context, sets []gfx.CapSet) (GFX, error) {
	if _, err := c.AnswerDVCCaps(ctx, dynvc.Version2); err != nil {
		return GFX{}, err
	}
	id, err := c.AcceptGFX(ctx)
	if err != nil {
		return GFX{}, err
	}
	return c.AdvertiseGFX(ctx, id, sets)
}

// AcceptGFX answers the server's Create Request for the graphics channel
// and returns the channel's ID. A request for another channel fails it
// with TagPDUUnexpected.
func (c *Client) AcceptGFX(ctx context.Context) (uint32, error) {
	p, err := c.AnswerDVCCreate(ctx, 0)
	if err != nil {
		return 0, err
	}
	if p.Name != GFXChannelName {
		return 0, errors.New(TagPDUUnexpected, "a Create Request for another channel than the graphics pipeline", "name", p.Name)
	}
	return p.ChannelID, nil
}

// AdvertiseGFX sends a Caps Advertise of sets on the graphics channel id
// and reads what the server answers: Caps Confirm, Reset Graphics, Create
// Surface and Map Surface To Output, in that order. Other PDUs fail it
// with TagPDUUnexpected.
func (c *Client) AdvertiseGFX(ctx context.Context, id uint32, sets []gfx.CapSet) (GFX, error) {
	g := GFX{ChannelID: id}
	if err := c.SendGFX(ctx, id, func(w *wire.Writer) { gfx.AppendCapsAdvertise(w, sets) }); err != nil {
		return g, err
	}
	want := []uint16{gfx.CmdCapsConfirm, gfx.CmdResetGraphics, gfx.CmdCreateSurface, gfx.CmdMapSurfaceToOutput}
	var got []gfx.PDU
	for len(got) < len(want) {
		ps, err := c.ReadGFX(ctx, id)
		if err != nil {
			return g, err
		}
		got = append(got, ps...)
	}
	for i, p := range got {
		if i >= len(want) || p.Cmd != want[i] {
			return g, errors.New(TagPDUUnexpected, "another graphics PDU than the start of the pipeline", "cmd", p.Cmd, "index", i)
		}
	}
	g.Confirm, g.Reset, g.Surface, g.Map = got[0].CapSet, got[1], got[2], got[3]
	return g, nil
}

// AckFrame sends a Frame Acknowledge on the graphics channel id.
func (c *Client) AckFrame(ctx context.Context, id, queueDepth, frameID, total uint32) error {
	return c.SendGFX(ctx, id, func(w *wire.Writer) { gfx.AppendFrameAcknowledge(w, queueDepth, frameID, total) })
}
