package rdptest

import (
	"context"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/fastpath"
	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/tpkt"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/internal/x224"
)

// ReadUpdate reads the next fast-path update the server sends, whole: the
// fragments of one are joined as a client joins them (MS-RDPBCGR
// 3.2.5.9.3.1), into a buffer of the MaxRequestSize the client confirmed
// (one PDU's worth when it confirmed none). Slow-path PDUs before it are
// skipped; a Disconnect Provider Ultimatum fails it with TagPDUUnexpected.
// The update's data is valid until the next read.
func (c *Client) ReadUpdate(ctx context.Context) (fastpath.Update, error) {
	stop, err := c.watch(ctx)
	if err != nil {
		return fastpath.Update{}, err
	}
	defer stop()
	c.reassembly.Max = fastpath.MaxUpdate(int(c.Caps.MaxRequestSize))
	for {
		k, f, err := tpkt.ReadFrame(c.rw, c.buf)
		if err != nil {
			return fastpath.Update{}, err
		}
		if k == tpkt.TPKT {
			r := wire.NewReader(f[tpkt.HeaderLen:])
			if err := x224.ReadDataHeader(&r); err != nil {
				return fastpath.Update{}, err
			}
			if b := r.Rest(); len(b) > 0 && mcs.Choice(b[0]) == mcs.ChoiceDisconnectProviderUltimatum {
				reason, err := mcs.ReadDisconnectProviderUltimatum(&r)
				if err != nil {
					return fastpath.Update{}, err
				}
				return fastpath.Update{}, errors.New(TagPDUUnexpected, "the server disconnected", "reason", reason)
			}
			continue
		}
		r, err := fastpath.ParsePDU(f)
		if err != nil {
			return fastpath.Update{}, err
		}
		// A PDU may carry several updates; the server sends one per PDU,
		// so the update returned is the last of its PDU.
		for r.Remaining() > 0 {
			u, err := fastpath.ReadUpdate(&r)
			if err != nil {
				return fastpath.Update{}, err
			}
			whole, ok, err := c.reassembly.Add(u)
			if err != nil {
				return fastpath.Update{}, err
			}
			if ok && r.Remaining() == 0 {
				return whole, nil
			}
			if ok {
				return fastpath.Update{}, errors.New(TagPDUUnexpected, "more than one update in a fast-path PDU", "code", whole.Code)
			}
		}
	}
}
