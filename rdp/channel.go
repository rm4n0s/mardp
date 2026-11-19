package rdp

import (
	"sync/atomic"

	"github.com/rm4n0s/mardp/rdp/internal/svc"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// openedChannels are the static channels the server reads (SPEC §5.1 step
// 3); the data of the others the client joins is ignored unread.
var openedChannels = [...]string{"drdynvc", "rdpsnd"}

// channelMaxMessage is the longest message the server joins on a static
// channel. What the client sends on drdynvc and rdpsnd is short: drdynvc's
// own PDUs are chunked to fit in one static chunk, and rdpsnd's from the
// client are confirmations.
const channelMaxMessage = 1 << 20

// channelHandler gets each whole message the client sends on a channel, on
// the session's reading goroutine; data is valid until it returns. An error
// ends the session.
type channelHandler func(data []byte) error

// staticChannel is a static channel the server reads (SPEC §5.1 step 3).
type staticChannel struct {
	name string
	id   uint16
	// join is used by the reading goroutine alone.
	join    svc.Joiner
	handler atomic.Pointer[channelHandler]
}

// openChannels makes the static channels the server reads, among those the
// client joined.
func (s *Session) openChannels() {
	for i, name := range s.client.Channels {
		for _, o := range openedChannels {
			if name == o {
				ch := &staticChannel{name: name, id: s.c.channels[2+i]}
				ch.join.Max = channelMaxMessage
				s.channels = append(s.channels, ch)
			}
		}
	}
}

// channel returns the static channel name when the server reads it and the
// client joined it, nil otherwise.
func (s *Session) channel(name string) *staticChannel {
	for _, ch := range s.channels {
		if ch.name == name {
			return ch
		}
	}
	return nil
}

// channelByID returns the static channel with the MCS channel ID id when
// the server reads it, nil otherwise.
func (s *Session) channelByID(id uint16) *staticChannel {
	for _, ch := range s.channels {
		if ch.id == id {
			return ch
		}
	}
	return nil
}

// setHandler sets the function that gets the channel's messages; nil drops
// them. Messages that arrived before it are dropped.
func (ch *staticChannel) setHandler(h channelHandler) {
	if h == nil {
		ch.handler.Store(nil)
		return
	}
	ch.handler.Store(&h)
}

// static handles what the client sends on a static channel other than the
// I/O channel: one chunk, joined with the others of its message.
func (sr *sessionReader) static(id uint16, data []byte) error {
	c := sr.c
	ch := sr.s.channelByID(id)
	if ch == nil {
		c.log.Debug("static channel data ignored", "channel", id, "length", len(data))
		return nil
	}
	r := wire.NewReader(data)
	h, err := svc.ReadHeader(&r)
	if err != nil {
		return err
	}
	msg, ok, err := ch.join.Add(h, r.Rest())
	if err != nil || !ok {
		return err
	}
	if fn := ch.handler.Load(); fn != nil {
		return (*fn)(msg)
	}
	c.log.Debug("static channel message dropped", "channel", ch.name, "length", len(msg))
	return nil
}

// sendChannel queues on q the message msg on the static channel ch, in
// chunks of at most svc.ChunkLength bytes (Demand Active states no
// VCChunkSize), one Send Data Indication each, all in one packet.
func (s *Session) sendChannel(q queue, ch *staticChannel, msg []byte) error {
	return s.wr.send(q, func(p *packet) error { return appendChannelMessage(p, ch, msg) })
}
