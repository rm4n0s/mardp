package rdp

import (
	"sync"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/cliprdr"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// TagClipboardUnavailable: the session has no clipboard: the server's
// Config.Clipboard is off, or the client did not ask for the channel.
const TagClipboardUnavailable = "ClipboardUnavailable"

// ClipboardMaxText is the longest text the clipboard carries, in bytes of
// UTF-8: longer text is not offered to the client, and a longer text of
// the client's is cut by the limit of a channel message.
const ClipboardMaxText = channelMaxMessage / 4

// ClipboardHandler is the app's end of the text clipboard (SPEC §10.6).
// Its methods are called on the session's reading goroutine, one at a
// time, and return soon.
type ClipboardHandler interface {
	// ServerText is the text of the server's clipboard now: the client
	// pastes it. ok is false when the clipboard has no text.
	ServerText() (text string, ok bool)
	// ClientText gets the text the client copied, lines ended by "\n".
	ClientText(text string)
}

// Clipboard is a session's text clipboard (SPEC §10.6, MS-RDPECLIP): the
// app says when the server's clipboard has new text (Offer), the client
// asks for it when its user pastes; and the text the client's user copies
// is fetched at once and handed to the app. Only text travels. Its methods
// may be called from any goroutine.
type Clipboard struct {
	s  *Session
	ch *staticChannel

	mu      sync.Mutex
	handler ClipboardHandler
	// long says both sides use Long Format Names.
	long bool
	// ready says the client's first Format List came, which ends the
	// channel's setup; offer is an Offer made before, to send then.
	ready bool
	offer *bool
	// asked says a Format Data Request of the server's waits for its
	// response; again, that the client announced new text meanwhile.
	asked, again bool
	buf          wire.Writer
}

// Clipboard returns the session's clipboard.
func (s *Session) Clipboard() *Clipboard { return &s.clipboard }

// Available says whether the session has a clipboard: the server's
// Config.Clipboard is on and the client asked for the channel.
func (c *Clipboard) Available() bool { return c.ch != nil }

// SetHandler sets the app's end; nil takes it away: the client's text is
// then not fetched, and its requests for the server's are refused.
func (c *Clipboard) SetHandler(h ClipboardHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handler = h
}

// startClipboard opens the clipboard when the server has it and the client
// joined cliprdr: the server's capabilities and Monitor Ready.
func (s *Session) startClipboard() {
	c := &s.clipboard
	c.mu.Lock()
	defer c.mu.Unlock()
	if !s.clipboardOn {
		return
	}
	for i, name := range s.client.Channels {
		if name == "cliprdr" && s.channel(name) == nil {
			s.openChannel(i)
		}
	}
	ch := s.channel("cliprdr")
	if ch == nil {
		return
	}
	ch.setHandler(c.receive)
	c.buf.Reset()
	cliprdr.AppendCaps(&c.buf, cliprdr.CapsLongFormatNames)
	if err := s.sendChannel(queueControl, ch, c.buf.Bytes()); err != nil {
		s.c.log.Debug("sending the clipboard's capabilities", "err", err)
		return
	}
	c.buf.Reset()
	cliprdr.AppendMonitorReady(&c.buf)
	if err := s.sendChannel(queueControl, ch, c.buf.Bytes()); err != nil {
		s.c.log.Debug("sending the clipboard's Monitor Ready", "err", err)
		return
	}
	c.ch = ch
}

// Offer tells the client that the server's clipboard changed: it has text
// (the client asks the handler for it when its user pastes), or it has
// none. An Offer made before the client finished setting the channel up is
// sent when it has.
func (c *Clipboard) Offer(hasText bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ch == nil {
		return errors.New(TagClipboardUnavailable, "the session has no clipboard")
	}
	if !c.ready {
		c.offer = &hasText
		return nil
	}
	return c.sendOffer(hasText)
}

// sendOffer sends the server's Format List; c.mu is held.
func (c *Clipboard) sendOffer(hasText bool) error {
	var formats []cliprdr.Format
	if hasText {
		formats = []cliprdr.Format{{ID: cliprdr.FormatUnicodeText}, {ID: cliprdr.FormatText}}
	}
	c.buf.Reset()
	cliprdr.AppendFormatList(&c.buf, formats, c.long)
	return c.s.sendChannel(queueControl, c.ch, c.buf.Bytes())
}

// receive handles what the client sends on cliprdr, on the session's
// reading goroutine. A PDU that is malformed, out of turn or about what
// the server does not do (files, locking) is ignored and logged.
func (c *Clipboard) receive(msg []byte) error {
	log := c.s.c.log
	p, err := cliprdr.ReadPDU(msg)
	if err != nil {
		log.Debug("a clipboard PDU ignored", "err", err)
		return nil
	}
	switch p.Type {
	case cliprdr.MsgCaps:
		flags, err := cliprdr.ReadCaps(p.Data)
		if err != nil {
			log.Debug("the client's clipboard capabilities ignored", "err", err)
			return nil
		}
		c.mu.Lock()
		c.long = flags&cliprdr.CapsLongFormatNames != 0
		c.mu.Unlock()
	case cliprdr.MsgFormatList:
		c.clientFormats(p)
	case cliprdr.MsgFormatListResponse:
		if p.Flags&cliprdr.FlagResponseOK == 0 {
			log.Debug("the client refused the server's clipboard formats", "flags", p.Flags)
		}
	case cliprdr.MsgFormatDataRequest:
		c.clientAsks(p)
	case cliprdr.MsgFormatDataResponse:
		c.clientText(p)
	default:
		log.Debug("a clipboard PDU the server does not act on", "type", p.Type, "length", len(p.Data))
	}
	return nil
}

// reply sends the PDU in c.buf; c.mu is held.
func (c *Clipboard) reply(what string) {
	if err := c.s.sendChannel(queueControl, c.ch, c.buf.Bytes()); err != nil {
		c.s.c.log.Debug("a clipboard PDU was not sent", "pdu", what, "err", err)
	}
}

// clientFormats takes the client's Format List: the client's user copied
// something. The list is accepted, and when it has text and the app
// listens, the text is asked for at once.
func (c *Clipboard) clientFormats(p cliprdr.PDU) {
	c.mu.Lock()
	defer c.mu.Unlock()
	formats, err := cliprdr.ReadFormatList(p, c.long)
	c.buf.Reset()
	cliprdr.AppendFormatListResponse(&c.buf, err == nil)
	c.reply("Format List Response")
	if !c.ready {
		c.ready = true
		if c.offer != nil {
			if oerr := c.sendOffer(*c.offer); oerr != nil {
				c.s.c.log.Debug("the server's clipboard formats were not sent", "err", oerr)
			}
			c.offer = nil
		}
	}
	if err != nil {
		c.s.c.log.Debug("the client's clipboard formats ignored", "err", err)
		return
	}
	text := false
	for _, f := range formats {
		text = text || f.ID == cliprdr.FormatUnicodeText
	}
	if !text || c.handler == nil {
		return
	}
	if c.asked {
		c.again = true // the answer on its way is of the text before
		return
	}
	c.ask()
}

// ask sends the Format Data Request for the client's text; c.mu is held.
func (c *Clipboard) ask() {
	c.buf.Reset()
	cliprdr.AppendFormatDataRequest(&c.buf, cliprdr.FormatUnicodeText)
	c.reply("Format Data Request")
	c.asked, c.again = true, false
}

// clientText takes the client's Format Data Response: the text it copied.
func (c *Clipboard) clientText(p cliprdr.PDU) {
	c.mu.Lock()
	if !c.asked {
		c.mu.Unlock()
		c.s.c.log.Debug("a clipboard Format Data Response nobody asked for ignored", "length", len(p.Data))
		return
	}
	c.asked = false
	h := c.handler
	if c.again {
		// The client copied again since: this text is old.
		c.ask()
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	if p.Flags&cliprdr.FlagResponseOK == 0 {
		c.s.c.log.Debug("the client did not give its clipboard's text", "flags", p.Flags)
		return
	}
	if h != nil {
		h.ClientText(cliprdr.DecodeText(p.Data))
	}
}

// clientAsks answers the client's Format Data Request: its user pastes the
// server's text.
func (c *Clipboard) clientAsks(p cliprdr.PDU) {
	id, err := cliprdr.ReadFormatDataRequest(p.Data)
	c.mu.Lock()
	h := c.handler
	c.mu.Unlock()
	var text string
	ok := err == nil && h != nil && (id == cliprdr.FormatUnicodeText || id == cliprdr.FormatText)
	if ok {
		text, ok = h.ServerText()
		ok = ok && len(text) <= ClipboardMaxText
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf.Reset()
	switch {
	case !ok:
		c.s.c.log.Debug("the client's request for the clipboard refused", "format", id, "err", err, "length", len(text))
		cliprdr.AppendFormatDataResponse(&c.buf, false, nil)
	case id == cliprdr.FormatText:
		cliprdr.AppendFormatDataResponse(&c.buf, true, cliprdr.EncodeText8(nil, text))
	default:
		cliprdr.AppendFormatDataResponse(&c.buf, true, cliprdr.EncodeText(nil, text))
	}
	c.reply("Format Data Response")
}
