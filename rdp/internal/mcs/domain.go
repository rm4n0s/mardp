package mcs

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// DomainMCSPDU choices (T.125 section 7, part 10); a domain PDU's first byte
// holds its choice in the high six bits.
const (
	ChoiceErectDomainRequest          uint8 = 1
	ChoiceDisconnectProviderUltimatum uint8 = 8
	ChoiceAttachUserRequest           uint8 = 10
	ChoiceAttachUserConfirm           uint8 = 11
	ChoiceDetachUserRequest           uint8 = 12
	ChoiceChannelJoinRequest          uint8 = 14
	ChoiceChannelJoinConfirm          uint8 = 15
	ChoiceSendDataRequest             uint8 = 25
	ChoiceSendDataIndication          uint8 = 26
)

// UserIDBase is the lowest UserId (T.125's UserId is DynamicChannelId,
// 1001..65535); PER writes a UserId as its distance from it.
const UserIDBase = 1001

// Disconnect Provider Ultimatum reasons (T.125 Reason).
const (
	ReasonDomainDisconnected uint8 = 0
	ReasonProviderInitiated  uint8 = 1
	ReasonTokenPurged        uint8 = 2
	ReasonUserRequested      uint8 = 3
	ReasonChannelPurged      uint8 = 4
)

// Data priorities (T.125 DataPriority).
const (
	PriorityTop    uint8 = 0
	PriorityHigh   uint8 = 1
	PriorityMedium uint8 = 2
	PriorityLow    uint8 = 3
)

// Segmentation bits (T.125 Segmentation); RDP sends every PDU whole, with
// both.
const (
	SegmentationBegin uint8 = 0x02
	SegmentationEnd   uint8 = 0x01
	SegmentationWhole       = SegmentationBegin | SegmentationEnd
)

// Choice returns the DomainMCSPDU choice of a PDU from its first byte.
func Choice(b0 byte) uint8 { return b0 >> 2 }

// ErectDomainRequest is T.125's ErectDomainRequest (MS-RDPBCGR 2.2.1.5).
type ErectDomainRequest struct {
	SubHeight, SubInterval uint32
}

// ReadErectDomainRequest reads an Erect Domain Request.
func ReadErectDomainRequest(r *wire.Reader) (ErectDomainRequest, error) {
	var e ErectDomainRequest
	b0, err := r.U8()
	if err != nil {
		return e, err
	}
	if Choice(b0) != ChoiceErectDomainRequest {
		return e, errors.New(TagUnexpectedPDU, "not an Erect Domain Request", "choice", Choice(b0))
	}
	if e.SubHeight, err = r.PERUint(); err != nil {
		return e, err
	}
	if e.SubInterval, err = r.PERUint(); err != nil {
		return e, err
	}
	if r.Remaining() != 0 {
		return e, errors.New(TagTrailingBytes, "bytes after the Erect Domain Request", "left", r.Remaining())
	}
	return e, nil
}

// WriteErectDomainRequest appends e.
func WriteErectDomainRequest(w *wire.Writer, e *ErectDomainRequest) {
	w.U8(ChoiceErectDomainRequest << 2)
	w.PERUint(e.SubHeight)
	w.PERUint(e.SubInterval)
}

// ReadAttachUserRequest reads an Attach User Request (MS-RDPBCGR 2.2.1.6),
// which has no fields.
func ReadAttachUserRequest(r *wire.Reader) error {
	b0, err := r.U8()
	if err != nil {
		return err
	}
	if Choice(b0) != ChoiceAttachUserRequest {
		return errors.New(TagUnexpectedPDU, "not an Attach User Request", "choice", Choice(b0))
	}
	if r.Remaining() != 0 {
		return errors.New(TagTrailingBytes, "bytes after the Attach User Request", "left", r.Remaining())
	}
	return nil
}

// WriteAttachUserRequest appends an Attach User Request.
func WriteAttachUserRequest(w *wire.Writer) {
	w.U8(ChoiceAttachUserRequest << 2)
}

// AttachUserConfirm is T.125's AttachUserConfirm (MS-RDPBCGR 2.2.1.7).
type AttachUserConfirm struct {
	Result uint8
	// HasInitiator tells whether Initiator, the user's id, is present; it
	// is when the result is successful.
	HasInitiator bool
	Initiator    uint16
}

// WriteAttachUserConfirm appends a.
func WriteAttachUserConfirm(w *wire.Writer, a *AttachUserConfirm) error {
	if a.Result > ResultUserRejected {
		return errors.New(TagResultOutOfRange, "result above rt-user-rejected", "result", a.Result)
	}
	writeResultHead(w, ChoiceAttachUserConfirm, a.HasInitiator, a.Result)
	if !a.HasInitiator {
		return nil
	}
	return w.PERInteger16(a.Initiator, UserIDBase)
}

// ReadAttachUserConfirm reads an Attach User Confirm.
func ReadAttachUserConfirm(r *wire.Reader) (AttachUserConfirm, error) {
	var a AttachUserConfirm
	b0, err := r.U8()
	if err != nil {
		return a, err
	}
	if Choice(b0) != ChoiceAttachUserConfirm {
		return a, errors.New(TagUnexpectedPDU, "not an Attach User Confirm", "choice", Choice(b0))
	}
	b1, err := r.U8()
	if err != nil {
		return a, err
	}
	a.HasInitiator = b0&0x02 != 0
	a.Result = (b0&0x01)<<3 | b1>>5
	if a.HasInitiator {
		if a.Initiator, err = r.PERInteger16(UserIDBase); err != nil {
			return a, err
		}
	}
	if r.Remaining() != 0 {
		return a, errors.New(TagTrailingBytes, "bytes after the Attach User Confirm", "left", r.Remaining())
	}
	return a, nil
}

// writeResultHead writes the first two bytes of a confirm: the six-bit
// choice, the bit of its optional field, and the four-bit Result across the
// byte boundary, padded to the octet.
func writeResultHead(w *wire.Writer, choice uint8, optional bool, result uint8) {
	b0 := choice<<2 | result>>3
	if optional {
		b0 |= 0x02
	}
	w.U8(b0)
	w.U8((result & 0x07) << 5)
}

// ChannelJoinRequest is T.125's ChannelJoinRequest (MS-RDPBCGR 2.2.1.8).
type ChannelJoinRequest struct {
	Initiator, ChannelID uint16
}

// ReadChannelJoinRequest reads a Channel Join Request.
func ReadChannelJoinRequest(r *wire.Reader) (ChannelJoinRequest, error) {
	var c ChannelJoinRequest
	b0, err := r.U8()
	if err != nil {
		return c, err
	}
	if Choice(b0) != ChoiceChannelJoinRequest {
		return c, errors.New(TagUnexpectedPDU, "not a Channel Join Request", "choice", Choice(b0))
	}
	if c.Initiator, err = r.PERInteger16(UserIDBase); err != nil {
		return c, err
	}
	if c.ChannelID, err = r.PERInteger16(0); err != nil {
		return c, err
	}
	if r.Remaining() != 0 {
		return c, errors.New(TagTrailingBytes, "bytes after the Channel Join Request", "left", r.Remaining())
	}
	return c, nil
}

// WriteChannelJoinRequest appends c.
func WriteChannelJoinRequest(w *wire.Writer, c *ChannelJoinRequest) error {
	w.U8(ChoiceChannelJoinRequest << 2)
	if err := w.PERInteger16(c.Initiator, UserIDBase); err != nil {
		return err
	}
	return w.PERInteger16(c.ChannelID, 0)
}

// ChannelJoinConfirm is T.125's ChannelJoinConfirm (MS-RDPBCGR 2.2.1.9).
type ChannelJoinConfirm struct {
	Result    uint8
	Initiator uint16
	Requested uint16
	// HasChannelID tells whether ChannelID, the channel joined, is present;
	// it is when the result is successful.
	HasChannelID bool
	ChannelID    uint16
}

// WriteChannelJoinConfirm appends c.
func WriteChannelJoinConfirm(w *wire.Writer, c *ChannelJoinConfirm) error {
	if c.Result > ResultUserRejected {
		return errors.New(TagResultOutOfRange, "result above rt-user-rejected", "result", c.Result)
	}
	writeResultHead(w, ChoiceChannelJoinConfirm, c.HasChannelID, c.Result)
	if err := w.PERInteger16(c.Initiator, UserIDBase); err != nil {
		return err
	}
	if err := w.PERInteger16(c.Requested, 0); err != nil {
		return err
	}
	if !c.HasChannelID {
		return nil
	}
	return w.PERInteger16(c.ChannelID, 0)
}

// ReadChannelJoinConfirm reads a Channel Join Confirm.
func ReadChannelJoinConfirm(r *wire.Reader) (ChannelJoinConfirm, error) {
	var c ChannelJoinConfirm
	b0, err := r.U8()
	if err != nil {
		return c, err
	}
	if Choice(b0) != ChoiceChannelJoinConfirm {
		return c, errors.New(TagUnexpectedPDU, "not a Channel Join Confirm", "choice", Choice(b0))
	}
	b1, err := r.U8()
	if err != nil {
		return c, err
	}
	c.HasChannelID = b0&0x02 != 0
	c.Result = (b0&0x01)<<3 | b1>>5
	if c.Initiator, err = r.PERInteger16(UserIDBase); err != nil {
		return c, err
	}
	if c.Requested, err = r.PERInteger16(0); err != nil {
		return c, err
	}
	if c.HasChannelID {
		if c.ChannelID, err = r.PERInteger16(0); err != nil {
			return c, err
		}
	}
	if r.Remaining() != 0 {
		return c, errors.New(TagTrailingBytes, "bytes after the Channel Join Confirm", "left", r.Remaining())
	}
	return c, nil
}

// SendData is the content of T.125's SendDataRequest (client to server) and
// SendDataIndication (server to client), which have the same fields
// (MS-RDPBCGR 2.2.2 and the PDUs of the later phases). Data points into the
// input.
type SendData struct {
	Initiator    uint16
	ChannelID    uint16
	Priority     uint8
	Segmentation uint8
	Data         []byte
}

// ReadSendDataRequest reads a Send Data Request; Data is the rest of the
// TPDU.
func ReadSendDataRequest(r *wire.Reader) (SendData, error) {
	return readSendData(r, ChoiceSendDataRequest)
}

// ReadSendDataIndication reads a Send Data Indication.
func ReadSendDataIndication(r *wire.Reader) (SendData, error) {
	return readSendData(r, ChoiceSendDataIndication)
}

func readSendData(r *wire.Reader, choice uint8) (SendData, error) {
	var d SendData
	b0, err := r.U8()
	if err != nil {
		return d, err
	}
	if Choice(b0) != choice {
		return d, errors.New(TagUnexpectedPDU, "not the Send Data PDU expected", "choice", Choice(b0), "want", choice)
	}
	if d.Initiator, err = r.PERInteger16(UserIDBase); err != nil {
		return d, err
	}
	if d.ChannelID, err = r.PERInteger16(0); err != nil {
		return d, err
	}
	b, err := r.U8()
	if err != nil {
		return d, err
	}
	d.Priority = b >> 6
	d.Segmentation = (b >> 4) & 0x03
	n, err := r.PERLength()
	if err != nil {
		return d, err
	}
	if d.Data, err = r.Bytes(n); err != nil {
		return d, err
	}
	if r.Remaining() != 0 {
		return d, errors.New(TagTrailingBytes, "bytes after the Send Data PDU's user data", "left", r.Remaining())
	}
	return d, nil
}

// WriteSendDataRequest appends d as a Send Data Request.
func WriteSendDataRequest(w *wire.Writer, d *SendData) error {
	return writeSendData(w, ChoiceSendDataRequest, d)
}

// WriteSendDataIndication appends d as a Send Data Indication.
func WriteSendDataIndication(w *wire.Writer, d *SendData) error {
	return writeSendData(w, ChoiceSendDataIndication, d)
}

func writeSendData(w *wire.Writer, choice uint8, d *SendData) error {
	if d.Priority > PriorityLow {
		return errors.New(TagPriorityInvalid, "data priority above low", "priority", d.Priority)
	}
	if d.Segmentation > SegmentationWhole {
		return errors.New(TagSegmentationInvalid, "segmentation other than begin and end", "segmentation", d.Segmentation)
	}
	w.U8(choice << 2)
	if err := w.PERInteger16(d.Initiator, UserIDBase); err != nil {
		return err
	}
	if err := w.PERInteger16(d.ChannelID, 0); err != nil {
		return err
	}
	w.U8(d.Priority<<6 | d.Segmentation<<4)
	if err := w.PERLength(len(d.Data)); err != nil {
		return err
	}
	w.Write(d.Data)
	return nil
}

// ReadDisconnectProviderUltimatum reads a Disconnect Provider Ultimatum
// (MS-RDPBCGR 2.2.2.3) and returns its reason.
func ReadDisconnectProviderUltimatum(r *wire.Reader) (uint8, error) {
	b0, err := r.U8()
	if err != nil {
		return 0, err
	}
	if Choice(b0) != ChoiceDisconnectProviderUltimatum {
		return 0, errors.New(TagUnexpectedPDU, "not a Disconnect Provider Ultimatum", "choice", Choice(b0))
	}
	b1, err := r.U8()
	if err != nil {
		return 0, err
	}
	reason := (b0&0x03)<<1 | b1>>7
	if reason > ReasonChannelPurged {
		return 0, errors.New(TagReasonInvalid, "reason outside the enumeration", "reason", reason)
	}
	if r.Remaining() != 0 {
		return 0, errors.New(TagTrailingBytes, "bytes after the Disconnect Provider Ultimatum", "left", r.Remaining())
	}
	return reason, nil
}

// WriteDisconnectProviderUltimatum appends a Disconnect Provider Ultimatum
// with reason: the six-bit choice, then the three-bit reason across the
// byte boundary.
func WriteDisconnectProviderUltimatum(w *wire.Writer, reason uint8) error {
	if reason > ReasonChannelPurged {
		return errors.New(TagReasonInvalid, "reason outside the enumeration", "reason", reason)
	}
	w.U8(ChoiceDisconnectProviderUltimatum<<2 | reason>>1)
	w.U8((reason & 0x01) << 7)
	return nil
}
