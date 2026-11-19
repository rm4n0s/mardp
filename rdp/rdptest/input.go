package rdptest

import (
	"context"

	"github.com/rm4n0s/mardp/rdp/internal/caps"
	"github.com/rm4n0s/mardp/rdp/internal/input"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// SendInput sends events in one fast-path input PDU (MS-RDPBCGR 2.2.8.1.2).
func (c *Client) SendInput(ctx context.Context, events ...input.Event) error {
	var w wire.Writer
	if err := input.AppendFastPath(&w, events); err != nil {
		return err
	}
	return c.WriteFrame(ctx, w.Bytes())
}

// SendSlowInput sends events in one Input Event PDU, the slow-path form
// (2.2.8.1.1.3).
func (c *Client) SendSlowInput(ctx context.Context, events ...input.Event) error {
	return c.SendShare(ctx, func(w *wire.Writer) error {
		off := caps.BeginData(w, c.ShareID, c.UserID, caps.Type2Input)
		if err := input.AppendSlowPath(w, events); err != nil {
			return err
		}
		return caps.EndData(w, off)
	})
}

// SendRefreshRect sends a Refresh Rect PDU for areas (2.2.11.2).
func (c *Client) SendRefreshRect(ctx context.Context, areas ...input.Rect) error {
	return c.SendShare(ctx, func(w *wire.Writer) error {
		off := caps.BeginData(w, c.ShareID, c.UserID, caps.Type2RefreshRect)
		if err := input.AppendRefreshRect(w, areas); err != nil {
			return err
		}
		return caps.EndData(w, off)
	})
}

// SendSuppressOutput sends a Suppress Output PDU (2.2.11.3).
func (c *Client) SendSuppressOutput(ctx context.Context, s input.SuppressOutput) error {
	return c.SendShare(ctx, func(w *wire.Writer) error {
		off := caps.BeginData(w, c.ShareID, c.UserID, caps.Type2SuppressOutput)
		input.AppendSuppressOutput(w, s)
		return caps.EndData(w, off)
	})
}

// SendShutdownRequest sends a Shutdown Request PDU (2.2.2.1), which a
// server answers with Shutdown Request Denied while a user is logged on.
func (c *Client) SendShutdownRequest(ctx context.Context) error {
	return c.SendShare(ctx, func(w *wire.Writer) error { return caps.WriteShutdownRequest(w, c.ShareID, c.UserID) })
}

// ReadShareData reads the next Data PDU the server sends, skipping FlowPDUs,
// Set Error Info and what comes on the static channels. A Disconnect Provider Ultimatum fails it with
// TagPDUUnexpected, and so does a share PDU of another type. The body is
// valid until the next read.
func (c *Client) ReadShareData(ctx context.Context) (caps.Data, error) {
	stop, err := c.watch(ctx)
	if err != nil {
		return caps.Data{}, err
	}
	defer stop()
	return c.readShareData()
}
