package rdptest

import (
	"context"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/dynvc"
	"github.com/rm4n0s/mardp/rdp/internal/svc"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// TagDVCUnavailable: a dynamic channel helper on a client that did not ask
// for drdynvc.
const TagDVCUnavailable = "ClientDVCUnavailable"

// DVCMaxMessage is the longest message ReadDVCMessage joins.
const DVCMaxMessage = 64 << 20

// drdynvc returns the MCS channel ID of drdynvc.
func (c *Client) drdynvc() (uint16, error) {
	id := c.ChannelID("drdynvc")
	if id == 0 {
		return 0, errors.New(TagDVCUnavailable, "the client did not ask for drdynvc")
	}
	return id, nil
}

// ReadDVC reads the next PDU the server sends on drdynvc (MS-RDPEDYC
// 2.2), skipping what it sends elsewhere. Its data is the caller's.
func (c *Client) ReadDVC(ctx context.Context) (dynvc.PDU, error) {
	id, err := c.drdynvc()
	if err != nil {
		return dynvc.PDU{}, err
	}
	m, err := c.ReadChannel(ctx, id)
	if err != nil {
		return dynvc.PDU{}, err
	}
	return dynvc.ParseServer(m.Data)
}

// SendDVC sends the DVC PDU that build appends on drdynvc, in one chunk as
// a client does.
func (c *Client) SendDVC(ctx context.Context, build func(w *wire.Writer)) error {
	id, err := c.drdynvc()
	if err != nil {
		return err
	}
	w := wire.NewWriter(make([]byte, 0, dynvc.MaxPDU))
	build(&w)
	return c.SendChannelChunk(ctx, id, svc.Header{Length: uint32(w.Len()), Flags: svc.FlagFirst | svc.FlagLast}, w.Bytes())
}

// AnswerDVCCaps reads the server's capabilities and answers them with
// version; it returns them. Another PDU first fails it with
// TagPDUUnexpected.
func (c *Client) AnswerDVCCaps(ctx context.Context, version uint16) (dynvc.PDU, error) {
	p, err := c.ReadDVC(ctx)
	if err != nil {
		return dynvc.PDU{}, err
	}
	if p.Cmd != dynvc.CmdCapabilities {
		return dynvc.PDU{}, errors.New(TagPDUUnexpected, "another DVC PDU than the capabilities", "cmd", p.Cmd)
	}
	var werr error
	err = c.SendDVC(ctx, func(w *wire.Writer) { werr = dynvc.AppendCapsResponse(w, version) })
	if werr != nil {
		return dynvc.PDU{}, werr
	}
	return p, err
}

// AnswerDVCCreate reads the server's next Create Request and answers it
// with the HRESULT status (0 accepts, a negative one refuses); it returns
// the request. Another PDU first fails it with TagPDUUnexpected.
func (c *Client) AnswerDVCCreate(ctx context.Context, status int32) (dynvc.PDU, error) {
	p, err := c.ReadDVC(ctx)
	if err != nil {
		return dynvc.PDU{}, err
	}
	if p.Cmd != dynvc.CmdCreate {
		return dynvc.PDU{}, errors.New(TagPDUUnexpected, "another DVC PDU than a Create Request", "cmd", p.Cmd)
	}
	return p, c.SendDVC(ctx, func(w *wire.Writer) { dynvc.AppendCreateResponse(w, p.ChannelID, status) })
}

// SendDVCMessage sends msg on the dynamic channel id, in a Data, or a Data
// First and Data PDUs when it does not fit in one.
func (c *Client) SendDVCMessage(ctx context.Context, id uint32, msg []byte) error {
	ch, err := c.drdynvc()
	if err != nil {
		return err
	}
	w := wire.NewWriter(make([]byte, 0, dynvc.MaxPDU))
	return dynvc.Split(&w, id, msg, func(b []byte) error {
		return c.SendChannelChunk(ctx, ch, svc.Header{Length: uint32(len(b)), Flags: svc.FlagFirst | svc.FlagLast}, b)
	})
}

// ReadDVCMessage reads the server's PDUs on drdynvc until a whole message
// on the dynamic channel id, and returns it with the number of PDUs it came
// in. A PDU other than Data First or Data on id fails it with
// TagPDUUnexpected.
func (c *Client) ReadDVCMessage(ctx context.Context, id uint32) ([]byte, int, error) {
	if c.dvcJoiners == nil {
		c.dvcJoiners = make(map[uint32]*dynvc.Joiner)
	}
	j := c.dvcJoiners[id]
	if j == nil {
		j = &dynvc.Joiner{Max: DVCMaxMessage}
		c.dvcJoiners[id] = j
	}
	for n := 1; ; n++ {
		p, err := c.ReadDVC(ctx)
		if err != nil {
			return nil, n, err
		}
		if (p.Cmd != dynvc.CmdDataFirst && p.Cmd != dynvc.CmdData) || p.ChannelID != id {
			return nil, n, errors.New(TagPDUUnexpected, "another DVC PDU than data on the channel",
				"cmd", p.Cmd, "channel", p.ChannelID, "want", id)
		}
		msg, ok, err := j.Add(p)
		if err != nil {
			return nil, n, err
		}
		if ok {
			return append([]byte(nil), msg...), n, nil
		}
	}
}
