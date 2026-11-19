package rdptest

import (
	"context"
	"slices"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/svc"
	"github.com/rm4n0s/mardp/rdp/internal/tpkt"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/internal/x224"
)

// ChannelMaxMessage is the longest message ReadChannel joins.
const ChannelMaxMessage = 64 << 20

// ChannelMessage is a whole message the server sent on a static channel.
type ChannelMessage struct {
	// Data is the message, joined from its chunks.
	Data []byte
	// Chunks are the Channel PDU Headers of its chunks, and Lengths the
	// length of each chunk's data, in the order they came.
	Chunks  []svc.Header
	Lengths []int
}

// ChannelID returns the MCS channel ID the server gave the static channel
// name, 0 when the client did not ask for it.
func (c *Client) ChannelID(name string) uint16 {
	i := slices.Index(c.opts.Channels, name)
	if i < 0 || i >= len(c.Server.Network.ChannelIDs) {
		return 0
	}
	return c.Server.Network.ChannelIDs[i]
}

// SendChannel sends msg on the static channel id in chunks of
// svc.ChunkLength bytes, one Send Data Request each, as a client does
// when the server's Virtual Channel Capability Set states no VCChunkSize.
func (c *Client) SendChannel(ctx context.Context, id uint16, msg []byte) error {
	stop, err := c.watch(ctx)
	if err != nil {
		return err
	}
	defer stop()
	return svc.Split(msg, svc.ChunkLength, func(h svc.Header, data []byte) error { return c.sendChunk(id, h, data) })
}

// SendChannelChunk sends one chunk, data with the header h, on the static
// channel id, to test what the server makes of chunks out of order.
func (c *Client) SendChannelChunk(ctx context.Context, id uint16, h svc.Header, data []byte) error {
	stop, err := c.watch(ctx)
	if err != nil {
		return err
	}
	defer stop()
	return c.sendChunk(id, h, data)
}

func (c *Client) sendChunk(id uint16, h svc.Header, data []byte) error {
	body := wire.NewWriter(make([]byte, 0, svc.HeaderLen+len(data)))
	svc.WriteHeader(&body, h)
	body.Write(data)
	return c.sendData(func(w *wire.Writer) error {
		return mcs.WriteSendDataRequest(w, &mcs.SendData{
			Initiator:    c.UserID,
			ChannelID:    id,
			Priority:     mcs.PriorityHigh,
			Segmentation: mcs.SegmentationWhole,
			Data:         body.Bytes(),
		})
	})
}

// ReadChannel reads until the server has sent a whole message on the static
// channel id, and returns it. Fast-path updates and what the server sends
// on other channels before it are skipped; a Disconnect Provider Ultimatum
// fails it with TagPDUUnexpected.
func (c *Client) ReadChannel(ctx context.Context, id uint16) (ChannelMessage, error) {
	stop, err := c.watch(ctx)
	if err != nil {
		return ChannelMessage{}, err
	}
	defer stop()
	if c.joiners == nil {
		c.joiners = make(map[uint16]*svc.Joiner)
	}
	j := c.joiners[id]
	if j == nil {
		j = &svc.Joiner{Max: ChannelMaxMessage}
		c.joiners[id] = j
	}
	var m ChannelMessage
	for {
		k, f, err := tpkt.ReadFrame(c.rw, c.buf)
		if err != nil {
			return ChannelMessage{}, err
		}
		if k != tpkt.TPKT {
			continue
		}
		r := wire.NewReader(f[tpkt.HeaderLen:])
		if err := x224.ReadDataHeader(&r); err != nil {
			return ChannelMessage{}, err
		}
		if b := r.Rest(); len(b) > 0 && mcs.Choice(b[0]) == mcs.ChoiceDisconnectProviderUltimatum {
			reason, err := mcs.ReadDisconnectProviderUltimatum(&r)
			if err != nil {
				return ChannelMessage{}, err
			}
			return ChannelMessage{}, errors.New(TagPDUUnexpected, "the server disconnected", "reason", reason)
		}
		d, err := mcs.ReadSendDataIndication(&r)
		if err != nil {
			return ChannelMessage{}, err
		}
		if d.ChannelID != id {
			continue
		}
		cr := wire.NewReader(d.Data)
		h, err := svc.ReadHeader(&cr)
		if err != nil {
			return ChannelMessage{}, err
		}
		m.Chunks = append(m.Chunks, h)
		m.Lengths = append(m.Lengths, cr.Remaining())
		msg, ok, err := j.Add(h, cr.Rest())
		if err != nil {
			return ChannelMessage{}, err
		}
		if ok {
			m.Data = slices.Clone(msg)
			return m, nil
		}
	}
}
