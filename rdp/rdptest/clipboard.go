package rdptest

import (
	"context"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/cliprdr"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// TagClipboardUnavailable: a clipboard helper on a client that did not ask
// for cliprdr.
const TagClipboardUnavailable = "ClientClipboardUnavailable"

// ClipboardOptions is how the client sets the clipboard channel up.
type ClipboardOptions struct {
	// NoCaps sends no Clipboard Capabilities PDU, as a client of the
	// first version does: format names are short.
	NoCaps bool
	// ShortNames sends capabilities without Long Format Names.
	ShortNames bool
	// Formats are the formats of the client's first Format List; none
	// sends an empty list.
	Formats []cliprdr.Format
}

// Clipboard is the client's end of the clipboard channel, set up by
// OpenClipboard.
type Clipboard struct {
	c  *Client
	id uint16
	// ServerFlags are the general flags of the server's capabilities.
	ServerFlags uint32
	// Long says both sides use Long Format Names.
	Long bool
}

// OpenClipboard reads the server's capabilities and Monitor Ready on
// cliprdr, and answers with the client's capabilities and its first Format
// List, whose response it reads. Another PDU in their place fails it with
// TagPDUUnexpected.
func (c *Client) OpenClipboard(ctx context.Context, opts ClipboardOptions) (*Clipboard, error) {
	id := c.ChannelID("cliprdr")
	if id == 0 {
		return nil, errors.New(TagClipboardUnavailable, "the client did not ask for cliprdr")
	}
	cb := &Clipboard{c: c, id: id}
	p, err := cb.Read(ctx)
	if err != nil {
		return nil, err
	}
	if p.Type != cliprdr.MsgCaps {
		return nil, errors.New(TagPDUUnexpected, "another clipboard PDU than the server's capabilities", "type", p.Type)
	}
	if cb.ServerFlags, err = cliprdr.ReadCaps(p.Data); err != nil {
		return nil, err
	}
	if p, err = cb.Read(ctx); err != nil {
		return nil, err
	}
	if p.Type != cliprdr.MsgMonitorReady {
		return nil, errors.New(TagPDUUnexpected, "another clipboard PDU than Monitor Ready", "type", p.Type)
	}
	if !opts.NoCaps {
		flags := cliprdr.CapsLongFormatNames
		if opts.ShortNames {
			flags = 0
		}
		cb.Long = flags&cb.ServerFlags&cliprdr.CapsLongFormatNames != 0
		if err := cb.Send(ctx, func(w *wire.Writer) { cliprdr.AppendCaps(w, flags) }); err != nil {
			return nil, err
		}
	}
	if err := cb.SendFormats(ctx, opts.Formats); err != nil {
		return nil, err
	}
	if p, err = cb.Read(ctx); err != nil {
		return nil, err
	}
	if p.Type != cliprdr.MsgFormatListResponse || p.Flags&cliprdr.FlagResponseOK == 0 {
		return nil, errors.New(TagPDUUnexpected, "the client's first Format List was not accepted", "type", p.Type, "flags", p.Flags)
	}
	return cb, nil
}

// Read reads the server's next PDU on cliprdr.
func (cb *Clipboard) Read(ctx context.Context) (cliprdr.PDU, error) {
	m, err := cb.c.ReadChannel(ctx, cb.id)
	if err != nil {
		return cliprdr.PDU{}, err
	}
	return cliprdr.ReadPDU(m.Data)
}

// Send sends the PDU that write appends on cliprdr.
func (cb *Clipboard) Send(ctx context.Context, write func(w *wire.Writer)) error {
	w := wire.NewWriter(nil)
	write(&w)
	return cb.c.SendChannel(ctx, cb.id, w.Bytes())
}

// SendFormats sends a Format List with formats, as when the client's user
// copies something.
func (cb *Clipboard) SendFormats(ctx context.Context, formats []cliprdr.Format) error {
	return cb.Send(ctx, func(w *wire.Writer) { cliprdr.AppendFormatList(w, formats, cb.Long) })
}

// SendText answers the server's Format Data Request with text, as
// cliprdr.FormatUnicodeText.
func (cb *Clipboard) SendText(ctx context.Context, text string) error {
	return cb.Send(ctx, func(w *wire.Writer) { cliprdr.AppendFormatDataResponse(w, true, cliprdr.EncodeText(nil, text)) })
}

// Paste asks the server for its clipboard in the format id and returns
// the Format Data Response's body; ok is false when the server refused.
func (cb *Clipboard) Paste(ctx context.Context, id uint32) (data []byte, ok bool, err error) {
	if err := cb.Send(ctx, func(w *wire.Writer) { cliprdr.AppendFormatDataRequest(w, id) }); err != nil {
		return nil, false, err
	}
	p, err := cb.Read(ctx)
	if err != nil {
		return nil, false, err
	}
	if p.Type != cliprdr.MsgFormatDataResponse {
		return nil, false, errors.New(TagPDUUnexpected, "another clipboard PDU than the Format Data Response", "type", p.Type)
	}
	return p.Data, p.Flags&cliprdr.FlagResponseOK != 0, nil
}

// Copy does what a client does when its user copies text: it sends a
// Format List with the text format, reads the server's response and its
// request for the text, and answers with text. A server that does not ask
// (no app listens) fails it with TagPDUUnexpected.
func (cb *Clipboard) Copy(ctx context.Context, text string) error {
	if err := cb.SendFormats(ctx, []cliprdr.Format{{ID: cliprdr.FormatUnicodeText}}); err != nil {
		return err
	}
	for _, want := range []uint16{cliprdr.MsgFormatListResponse, cliprdr.MsgFormatDataRequest} {
		p, err := cb.Read(ctx)
		if err != nil {
			return err
		}
		if p.Type != want {
			return errors.New(TagPDUUnexpected, "another clipboard PDU than the answer to a copy", "type", p.Type, "want", want)
		}
	}
	return cb.SendText(ctx, text)
}

// WaitOffer reads the server's Format List, accepts it, and says whether
// it has the text format: the server's clipboard changed.
func (cb *Clipboard) WaitOffer(ctx context.Context) (hasText bool, err error) {
	p, err := cb.Read(ctx)
	if err != nil {
		return false, err
	}
	if p.Type != cliprdr.MsgFormatList {
		return false, errors.New(TagPDUUnexpected, "another clipboard PDU than the server's Format List", "type", p.Type)
	}
	formats, err := cliprdr.ReadFormatList(p, cb.Long)
	if err != nil {
		return false, err
	}
	for _, f := range formats {
		hasText = hasText || f.ID == cliprdr.FormatUnicodeText
	}
	return hasText, cb.Send(ctx, func(w *wire.Writer) { cliprdr.AppendFormatListResponse(w, true) })
}

// PasteText asks the server for its clipboard's text, as when the client's
// user pastes; ok is false when the server has none.
func (cb *Clipboard) PasteText(ctx context.Context) (text string, ok bool, err error) {
	data, ok, err := cb.Paste(ctx, cliprdr.FormatUnicodeText)
	if err != nil || !ok {
		return "", false, err
	}
	return cliprdr.DecodeText(data), true, nil
}
