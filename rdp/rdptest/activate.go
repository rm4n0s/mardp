package rdptest

import (
	"context"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/caps"
	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/sec"
	"github.com/rm4n0s/mardp/rdp/internal/tpkt"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/internal/x224"
)

// Tags of the errors Activate and AwaitDisconnect return.
const (
	// TagPDUUnexpected: a share PDU other than the one the sequence
	// expects, or a Disconnect Provider Ultimatum in the middle of it (its
	// reason and the Set Error Info code are in the metadata).
	TagPDUUnexpected = "ClientPDUUnexpected"
)

// ClientCaps returns the capability sets the client confirms, before
// Options.EditCaps: what FreeRDP sends, in the sets the caps package
// models, with the desktop size asked for.
func (c *Client) ClientCaps() caps.Caps {
	var cc caps.Caps
	for _, t := range []uint16{
		caps.SetGeneral, caps.SetBitmap, caps.SetOrder, caps.SetPointer, caps.SetInput,
		caps.SetVirtualChannel, caps.SetSound, caps.SetShare, caps.SetFont, caps.SetMultifragmentUpdate,
		caps.SetLargePointer, caps.SetSurfaceCommands, caps.SetFrameAcknowledge,
	} {
		cc.Add(t)
	}
	cc.General = caps.General{
		OSMajor:        caps.OSMajorUnix,
		OSMinor:        caps.OSMinorNativeX,
		ExtraFlags:     caps.ExtraFastPathOutput | caps.ExtraLongCredentials | caps.ExtraNoBitmapCompHdr,
		RefreshRect:    true,
		SuppressOutput: true,
	}
	cc.Bitmap = caps.Bitmap{BitsPerPixel: 32, DesktopWidth: c.opts.Width, DesktopHeight: c.opts.Height, DesktopResize: true,
		DrawingFlags: caps.DrawAllowDynamicColorFidelity | caps.DrawAllowSkipAlpha}
	cc.Pointer = caps.Pointer{Color: true, ColorCacheSize: 20, CacheSize: 25, HasCacheSize: true}
	cc.Input = caps.Input{
		Flags:                caps.InputScancodes | caps.InputMouseX | caps.InputUnicode | caps.InputFastPath2 | caps.InputMouseHWheel,
		KeyboardLayout:       0x409,
		KeyboardType:         4,
		KeyboardFunctionKeys: 12,
	}
	cc.SoundFlags = caps.SoundBeeps
	cc.FontFlags = caps.FontSupportList
	cc.MaxRequestSize = 0x3f0000
	cc.LargePointer = caps.LargePointer96 | caps.LargePointer384
	cc.SurfaceCommands = caps.SurfaceSetSurfaceBits | caps.SurfaceFrameMarker | caps.SurfaceStreamSurfaceBits
	cc.MaxUnacknowledgedFrames = 2
	return cc
}

// Activate runs the capability exchange and the connection finalization
// after Login: it reads Demand Active, sends Confirm Active, Synchronize,
// Control Cooperate, Control Request and Font List, and reads the server's
// Synchronize, Cooperate, Granted and Font Map. The session is then open.
func (c *Client) Activate(ctx context.Context) error {
	stop, err := c.watch(ctx)
	if err != nil {
		return err
	}
	defer stop()
	p, err := c.readShare()
	if err != nil {
		return err
	}
	if p.Type != caps.TypeDemandActive {
		return errors.New(TagPDUUnexpected, "another PDU than Demand Active after the license", "type", p.Type)
	}
	da, err := caps.ParseDemandActive(p)
	if err != nil {
		return err
	}
	c.ShareID, c.ServerSource, c.ServerCaps = da.ShareID, p.Source, da.Caps
	ca := caps.ConfirmActive{
		ShareID:          da.ShareID,
		Source:           c.UserID,
		Originator:       p.Source,
		SourceDescriptor: []byte("MSTSC\x00"),
		Caps:             c.ClientCaps(),
	}
	if c.opts.EditCaps != nil {
		c.opts.EditCaps(&ca.Caps)
	}
	c.Caps = ca.Caps
	if err := c.sendShare(func(w *wire.Writer) error { return caps.WriteConfirmActive(w, &ca) }); err != nil {
		return err
	}
	for _, body := range []func(w *wire.Writer) error{
		func(w *wire.Writer) error { return caps.WriteSynchronize(w, c.ShareID, c.UserID, c.ServerSource) },
		func(w *wire.Writer) error {
			return caps.WriteControl(w, c.ShareID, c.UserID, caps.Control{Action: caps.ActionCooperate})
		},
		func(w *wire.Writer) error {
			return caps.WriteControl(w, c.ShareID, c.UserID, caps.Control{Action: caps.ActionRequestControl})
		},
		func(w *wire.Writer) error { return caps.WriteFontList(w, c.ShareID, c.UserID) },
	} {
		if err := c.sendShare(body); err != nil {
			return err
		}
	}
	for _, want := range []struct {
		type2  uint8
		action uint16
	}{
		{caps.Type2Synchronize, 0},
		{caps.Type2Control, caps.ActionCooperate},
		{caps.Type2Control, caps.ActionGrantedControl},
		{caps.Type2FontMap, 0},
	} {
		d, err := c.readShareData()
		if err != nil {
			return err
		}
		if d.Type2 != want.type2 {
			return errors.New(TagPDUUnexpected, "another Data PDU than the finalization expects", "type2", d.Type2, "want", want.type2)
		}
		if want.action == 0 {
			continue
		}
		ctl, err := caps.ParseControl(d.Body)
		if err != nil {
			return err
		}
		if ctl.Action != want.action {
			return errors.New(TagPDUUnexpected, "another Control action than the finalization expects", "action", ctl.Action, "want", want.action)
		}
	}
	return nil
}

// SendShare sends one share PDU, which body appends, on the I/O channel,
// to test what the server makes of PDUs the sequence does not expect.
func (c *Client) SendShare(ctx context.Context, body func(w *wire.Writer) error) error {
	stop, err := c.watch(ctx)
	if err != nil {
		return err
	}
	defer stop()
	return c.sendShare(body)
}

// sendShare sends one share PDU, which body appends, on the I/O channel.
func (c *Client) sendShare(body func(w *wire.Writer) error) error {
	var pw wire.Writer
	if err := body(&pw); err != nil {
		return err
	}
	return c.sendData(func(w *wire.Writer) error {
		return mcs.WriteSendDataRequest(w, &mcs.SendData{
			Initiator: c.UserID, ChannelID: c.Server.Network.MCSChannelID,
			Priority: mcs.PriorityHigh, Segmentation: mcs.SegmentationWhole, Data: pw.Bytes(),
		})
	})
}

// readShare reads the next share PDU the server sends, skipping FlowPDUs
// and what it sends on the static channels. A Disconnect Provider
// Ultimatum fails it.
func (c *Client) readShare() (caps.PDU, error) {
	var errorInfo uint32
	for {
		r, err := c.readData()
		if err != nil {
			return caps.PDU{}, err
		}
		if b := r.Rest(); len(b) > 0 && mcs.Choice(b[0]) == mcs.ChoiceDisconnectProviderUltimatum {
			reason, err := mcs.ReadDisconnectProviderUltimatum(&r)
			if err != nil {
				return caps.PDU{}, err
			}
			return caps.PDU{}, errors.New(TagPDUUnexpected, "the server disconnected", "reason", reason, "errorInfo", errorInfo)
		}
		d, err := mcs.ReadSendDataIndication(&r)
		if err != nil {
			return caps.PDU{}, err
		}
		if d.ChannelID != c.Server.Network.MCSChannelID {
			continue
		}
		sr := wire.NewReader(d.Data)
		p, err := caps.ReadPDU(&sr)
		if err != nil {
			return caps.PDU{}, err
		}
		if p.Flow {
			continue
		}
		if code, ok, err := errorInfoOf(p); err != nil {
			return caps.PDU{}, err
		} else if ok {
			errorInfo = code
			continue
		}
		return p, nil
	}
}

// readShareData reads the next Data PDU the server sends.
func (c *Client) readShareData() (caps.Data, error) {
	p, err := c.readShare()
	if err != nil {
		return caps.Data{}, err
	}
	if p.Type != caps.TypeData {
		return caps.Data{}, errors.New(TagPDUUnexpected, "another PDU than a Data PDU", "type", p.Type)
	}
	return caps.ParseData(p.Body)
}

// errorInfoOf returns the code of p when it is a Set Error Info PDU.
func errorInfoOf(p caps.PDU) (uint32, bool, error) {
	if p.Type != caps.TypeData {
		return 0, false, nil
	}
	d, err := caps.ParseData(p.Body)
	if err != nil {
		return 0, false, err
	}
	if d.Type2 != caps.Type2SetErrorInfo {
		return 0, false, nil
	}
	r := wire.NewReader(d.Body)
	code, err := r.U32LE()
	if err != nil {
		return 0, false, err
	}
	return code, true, nil
}

// AwaitDisconnect reads what the server sends until its Disconnect Provider
// Ultimatum and returns the ultimatum's reason and the code of the last Set
// Error Info before it (sec.ErrInfoNone when there was none). Everything
// else, fast-path output included, is skipped.
func (c *Client) AwaitDisconnect(ctx context.Context) (reason uint8, errorInfo uint32, err error) {
	stop, err := c.watch(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer stop()
	errorInfo = sec.ErrInfoNone
	for {
		k, f, err := tpkt.ReadFrame(c.rw, c.buf)
		if err != nil {
			return 0, errorInfo, err
		}
		if k != tpkt.TPKT {
			continue
		}
		r := wire.NewReader(f[tpkt.HeaderLen:])
		if err := x224.ReadDataHeader(&r); err != nil {
			return 0, errorInfo, err
		}
		if b := r.Rest(); len(b) > 0 && mcs.Choice(b[0]) == mcs.ChoiceDisconnectProviderUltimatum {
			reason, err := mcs.ReadDisconnectProviderUltimatum(&r)
			return reason, errorInfo, err
		}
		d, err := mcs.ReadSendDataIndication(&r)
		if err != nil {
			return 0, errorInfo, err
		}
		if d.ChannelID != c.Server.Network.MCSChannelID {
			continue
		}
		sr := wire.NewReader(d.Data)
		p, err := caps.ReadPDU(&sr)
		if err != nil {
			return 0, errorInfo, err
		}
		code, ok, err := errorInfoOf(p)
		if err != nil {
			return 0, errorInfo, err
		}
		if ok {
			errorInfo = code
		}
	}
}

// Disconnect ends the session from the client's side with a Disconnect
// Provider Ultimatum (rn-user-requested), as a client does when its user
// closes it.
func (c *Client) Disconnect(ctx context.Context) error {
	return c.SendData(ctx, func(w *wire.Writer) error {
		return mcs.WriteDisconnectProviderUltimatum(w, mcs.ReasonUserRequested)
	})
}
