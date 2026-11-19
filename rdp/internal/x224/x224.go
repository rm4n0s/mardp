// Package x224 reads and writes the X.224 (ISO 8073 class 0) TPDUs RDP uses
// inside TPKTs (MS-RDPBCGR 2.2.1.1, 2.2.1.2, T.123): the Connection Request
// with the client's RDP negotiation request, the Connection Confirm with the
// server's response or failure, the Data TPDU header that carries every later
// slow-path PDU, and the Disconnect Request.
//
// The functions work on the TPDU alone, after the TPKT header; package tpkt
// frames it. What they return points into the input instead of copying it.
package x224

import (
	"bytes"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors this package returns.
const (
	// TagLengthIndicatorMismatch: a length indicator that does not match the
	// TPDU's size.
	TagLengthIndicatorMismatch = "X224LengthIndicatorMismatch"
	// TagUnexpectedCode: a TPDU of another kind than the one expected.
	TagUnexpectedCode = "X224UnexpectedCode"
	// TagClassUnsupported: a Connection Request or Confirm for a class other
	// than 0, or with options.
	TagClassUnsupported = "X224ClassUnsupported"
	// TagCookieUnterminated: a cookie or routing token without its CR LF.
	TagCookieUnterminated = "X224CookieUnterminated"
	// TagNegotiationTypeUnknown: negotiation data of a type this message does
	// not carry.
	TagNegotiationTypeUnknown = "X224NegotiationTypeUnknown"
	// TagNegotiationLengthInvalid: negotiation data whose length field is not
	// the fixed size of its type.
	TagNegotiationLengthInvalid = "X224NegotiationLengthInvalid"
	// TagCorrelationFlagMismatch: correlation info without
	// CORRELATION_INFO_PRESENT, or the flag without the info.
	TagCorrelationFlagMismatch = "X224CorrelationFlagMismatch"
	// TagTrailingBytes: bytes after the last field of a TPDU.
	TagTrailingBytes = "X224TrailingBytes"
	// TagEOTMissing: a Data TPDU without the end-of-TSDU mark; RDP never
	// splits a PDU over several TPDUs.
	TagEOTMissing = "X224EOTMissing"
	// TagTooLong: a TPDU header that does not fit the 1-byte length
	// indicator.
	TagTooLong = "X224TooLong"
	// TagCookieInvalid: a cookie or routing token to write that contains CR
	// or LF.
	TagCookieInvalid = "X224CookieInvalid"
)

// TPDU codes (the high four bits of the second byte for CR and CC).
const (
	CodeConnectionRequest = 0xe0
	CodeConnectionConfirm = 0xd0
	CodeDisconnectRequest = 0x80
	CodeData              = 0xf0
)

// Negotiation message types.
const (
	TypeNegReq          = 0x01
	TypeNegRsp          = 0x02
	TypeNegFailure      = 0x03
	TypeCorrelationInfo = 0x06
)

// Requested and selected protocols (RDP_NEG_REQ requestedProtocols,
// RDP_NEG_RSP selectedProtocol).
const (
	ProtocolRDP      uint32 = 0x00
	ProtocolSSL      uint32 = 0x01
	ProtocolHybrid   uint32 = 0x02
	ProtocolRDSTLS   uint32 = 0x04
	ProtocolHybridEx uint32 = 0x08
	ProtocolRDSAAD   uint32 = 0x10
)

// RDP_NEG_REQ flags.
const (
	ReqRestrictedAdminModeRequired  uint8 = 0x01
	ReqRedirectedAuthenticationMode uint8 = 0x02
	ReqCorrelationInfoPresent       uint8 = 0x08
)

// RDP_NEG_RSP flags.
const (
	RspExtendedClientDataSupported       uint8 = 0x01
	RspDynvcGfxProtocolSupported         uint8 = 0x02
	RspReserved                          uint8 = 0x04
	RspRestrictedAdminModeSupported      uint8 = 0x08
	RspRedirectedAuthenticationSupported uint8 = 0x10
)

// RDP_NEG_FAILURE failure codes.
const (
	FailureSSLRequiredByServer     uint32 = 0x01
	FailureSSLNotAllowedByServer   uint32 = 0x02
	FailureSSLCertNotOnServer      uint32 = 0x03
	FailureInconsistentFlags       uint32 = 0x04
	FailureHybridRequiredByServer  uint32 = 0x05
	FailureSSLWithUserAuthRequired uint32 = 0x06
)

// Sizes of the fixed parts.
const (
	// DataHeaderLen is the size of a Data TPDU header: LI, code, EOT.
	DataHeaderLen = 3
	// negLen is the size of RDP_NEG_REQ, RDP_NEG_RSP and RDP_NEG_FAILURE.
	negLen = 8
	// correlationLen is the size of RDP_NEG_CORRELATION_INFO.
	correlationLen = 36
	// fixedLen is LI, code, DST-REF, SRC-REF and class of a CR or CC.
	fixedLen = 7
	// eot is the end-of-TSDU mark of a Data TPDU.
	eot = 0x80
)

const cookiePrefix = "Cookie: mstshash="

var crlf = []byte{'\r', '\n'}

// ConnectionRequest is a parsed X.224 Connection Request (MS-RDPBCGR
// 2.2.1.1). Cookie and RoutingToken point into the parsed bytes.
type ConnectionRequest struct {
	// SrcRef is the client's source reference.
	SrcRef uint16
	// Cookie is the IDENTIFIER of "Cookie: mstshash=IDENTIFIER", or nil.
	Cookie []byte
	// RoutingToken is a token line other than the mstshash cookie, without
	// its CR LF, or nil.
	RoutingToken []byte
	// HasNegReq tells whether an RDP_NEG_REQ followed; without it the client
	// knows only standard RDP security.
	HasNegReq bool
	// Flags and RequestedProtocols are the RDP_NEG_REQ's fields.
	Flags              uint8
	RequestedProtocols uint32
	// HasCorrelation tells whether RDP_NEG_CORRELATION_INFO followed.
	HasCorrelation bool
	// CorrelationID is the correlation info's correlationId.
	CorrelationID [16]byte
}

// ParseConnectionRequest reads a Connection Request TPDU.
func ParseConnectionRequest(tpdu []byte) (ConnectionRequest, error) {
	var cr ConnectionRequest
	r := wire.NewReader(tpdu)
	li, err := r.U8()
	if err != nil {
		return cr, err
	}
	if int(li) != len(tpdu)-1 {
		return cr, errors.New(TagLengthIndicatorMismatch, "length indicator does not match the TPDU", "li", li, "len", len(tpdu))
	}
	code, err := r.U8()
	if err != nil {
		return cr, err
	}
	if code&0xf0 != CodeConnectionRequest {
		return cr, errors.New(TagUnexpectedCode, "not a Connection Request", "code", code)
	}
	if _, err := r.U16BE(); err != nil {
		return cr, err
	}
	if cr.SrcRef, err = r.U16BE(); err != nil {
		return cr, err
	}
	class, err := r.U8()
	if err != nil {
		return cr, err
	}
	if class != 0 {
		return cr, errors.New(TagClassUnsupported, "class or options other than 0", "class", class)
	}
	rest := r.Rest()
	if len(rest) > 0 && rest[0] != TypeNegReq {
		i := bytes.Index(rest, crlf)
		if i < 0 {
			return cr, errors.New(TagCookieUnterminated, "cookie or routing token without CR LF", "len", len(rest))
		}
		line := rest[:i:i]
		if len(line) >= len(cookiePrefix) && string(line[:len(cookiePrefix)]) == cookiePrefix {
			cr.Cookie = line[len(cookiePrefix):]
		} else {
			cr.RoutingToken = line
		}
		if err := r.Skip(i + len(crlf)); err != nil {
			return cr, err
		}
	}
	if r.Remaining() == 0 {
		return cr, nil
	}
	typ, err := r.U8()
	if err != nil {
		return cr, err
	}
	if typ != TypeNegReq {
		return cr, errors.New(TagNegotiationTypeUnknown, "not an RDP_NEG_REQ", "type", typ)
	}
	cr.HasNegReq = true
	if cr.Flags, err = r.U8(); err != nil {
		return cr, err
	}
	n, err := r.U16LE()
	if err != nil {
		return cr, err
	}
	if n != negLen {
		return cr, errors.New(TagNegotiationLengthInvalid, "RDP_NEG_REQ length is not 8", "length", n)
	}
	if cr.RequestedProtocols, err = r.U32LE(); err != nil {
		return cr, err
	}
	present := cr.Flags&ReqCorrelationInfoPresent != 0
	if (r.Remaining() > 0) != present {
		return cr, errors.New(TagCorrelationFlagMismatch, "correlation info and its flag disagree", "flag", present, "left", r.Remaining())
	}
	if !present {
		return cr, nil
	}
	if typ, err = r.U8(); err != nil {
		return cr, err
	}
	if typ != TypeCorrelationInfo {
		return cr, errors.New(TagNegotiationTypeUnknown, "not an RDP_NEG_CORRELATION_INFO", "type", typ)
	}
	if _, err := r.U8(); err != nil {
		return cr, err
	}
	if n, err = r.U16LE(); err != nil {
		return cr, err
	}
	if n != correlationLen {
		return cr, errors.New(TagNegotiationLengthInvalid, "RDP_NEG_CORRELATION_INFO length is not 36", "length", n)
	}
	id, err := r.Bytes(len(cr.CorrelationID))
	if err != nil {
		return cr, err
	}
	copy(cr.CorrelationID[:], id)
	if err := r.Skip(16); err != nil {
		return cr, err
	}
	cr.HasCorrelation = true
	if r.Remaining() != 0 {
		return cr, errors.New(TagTrailingBytes, "bytes after the Connection Request", "left", r.Remaining())
	}
	return cr, nil
}

// WriteConnectionRequest appends cr as a Connection Request TPDU. A client
// writes it; the server's tests write it to read it back.
func WriteConnectionRequest(w *wire.Writer, cr *ConnectionRequest) error {
	line := 0
	for _, b := range [][]byte{cr.Cookie, cr.RoutingToken} {
		if bytes.IndexByte(b, '\r') >= 0 || bytes.IndexByte(b, '\n') >= 0 {
			return errors.New(TagCookieInvalid, "cookie or routing token holds CR or LF", "len", len(b))
		}
	}
	switch {
	case cr.Cookie != nil:
		line = len(cookiePrefix) + len(cr.Cookie) + len(crlf)
	case cr.RoutingToken != nil:
		line = len(cr.RoutingToken) + len(crlf)
	}
	n := fixedLen + line
	if cr.HasNegReq {
		n += negLen
		if cr.HasCorrelation {
			n += correlationLen
		}
	}
	if n-1 > 0xfe {
		return errors.New(TagTooLong, "Connection Request does not fit the length indicator", "len", n)
	}
	w.U8(uint8(n - 1))
	w.U8(CodeConnectionRequest)
	w.U16BE(0)
	w.U16BE(cr.SrcRef)
	w.U8(0)
	switch {
	case cr.Cookie != nil:
		for i := range len(cookiePrefix) {
			w.U8(cookiePrefix[i])
		}
		w.Write(cr.Cookie)
		w.Write(crlf)
	case cr.RoutingToken != nil:
		w.Write(cr.RoutingToken)
		w.Write(crlf)
	}
	if !cr.HasNegReq {
		return nil
	}
	flags := cr.Flags &^ ReqCorrelationInfoPresent
	if cr.HasCorrelation {
		flags |= ReqCorrelationInfoPresent
	}
	w.U8(TypeNegReq)
	w.U8(flags)
	w.U16LE(negLen)
	w.U32LE(cr.RequestedProtocols)
	if cr.HasCorrelation {
		w.U8(TypeCorrelationInfo)
		w.U8(0)
		w.U16LE(correlationLen)
		w.Write(cr.CorrelationID[:])
		w.Zeros(16)
	}
	return nil
}

// Negotiation is what a Connection Confirm carries after its fixed part.
type Negotiation int

// The kinds of negotiation data in a Connection Confirm.
const (
	// NegNone: no negotiation data, the answer to a request without
	// RDP_NEG_REQ.
	NegNone Negotiation = iota
	// NegResponse: RDP_NEG_RSP.
	NegResponse
	// NegFailure: RDP_NEG_FAILURE.
	NegFailure
)

// ConnectionConfirm is an X.224 Connection Confirm (MS-RDPBCGR 2.2.1.2).
type ConnectionConfirm struct {
	// SrcRef is the server's source reference.
	SrcRef uint16
	// Negotiation is the kind of data that follows.
	Negotiation Negotiation
	// Flags and SelectedProtocol are RDP_NEG_RSP's fields; FailureCode is
	// RDP_NEG_FAILURE's.
	Flags            uint8
	SelectedProtocol uint32
	FailureCode      uint32
}

// WriteConnectionConfirm appends cc as a Connection Confirm TPDU.
func WriteConnectionConfirm(w *wire.Writer, cc *ConnectionConfirm) {
	n := fixedLen
	if cc.Negotiation != NegNone {
		n += negLen
	}
	w.U8(uint8(n - 1))
	w.U8(CodeConnectionConfirm)
	w.U16BE(0)
	w.U16BE(cc.SrcRef)
	w.U8(0)
	switch cc.Negotiation {
	case NegResponse:
		w.U8(TypeNegRsp)
		w.U8(cc.Flags)
		w.U16LE(negLen)
		w.U32LE(cc.SelectedProtocol)
	case NegFailure:
		w.U8(TypeNegFailure)
		w.U8(0)
		w.U16LE(negLen)
		w.U32LE(cc.FailureCode)
	}
}

// ParseConnectionConfirm reads a Connection Confirm TPDU. A client reads
// it; the server's tests read what it wrote.
func ParseConnectionConfirm(tpdu []byte) (ConnectionConfirm, error) {
	var cc ConnectionConfirm
	r := wire.NewReader(tpdu)
	li, err := r.U8()
	if err != nil {
		return cc, err
	}
	if int(li) != len(tpdu)-1 {
		return cc, errors.New(TagLengthIndicatorMismatch, "length indicator does not match the TPDU", "li", li, "len", len(tpdu))
	}
	code, err := r.U8()
	if err != nil {
		return cc, err
	}
	if code&0xf0 != CodeConnectionConfirm {
		return cc, errors.New(TagUnexpectedCode, "not a Connection Confirm", "code", code)
	}
	if _, err := r.U16BE(); err != nil {
		return cc, err
	}
	if cc.SrcRef, err = r.U16BE(); err != nil {
		return cc, err
	}
	class, err := r.U8()
	if err != nil {
		return cc, err
	}
	if class != 0 {
		return cc, errors.New(TagClassUnsupported, "class or options other than 0", "class", class)
	}
	if r.Remaining() == 0 {
		return cc, nil
	}
	typ, err := r.U8()
	if err != nil {
		return cc, err
	}
	flags, err := r.U8()
	if err != nil {
		return cc, err
	}
	n, err := r.U16LE()
	if err != nil {
		return cc, err
	}
	if n != negLen {
		return cc, errors.New(TagNegotiationLengthInvalid, "negotiation length is not 8", "length", n)
	}
	v, err := r.U32LE()
	if err != nil {
		return cc, err
	}
	switch typ {
	case TypeNegRsp:
		cc.Negotiation, cc.Flags, cc.SelectedProtocol = NegResponse, flags, v
	case TypeNegFailure:
		cc.Negotiation, cc.FailureCode = NegFailure, v
	default:
		return cc, errors.New(TagNegotiationTypeUnknown, "neither RDP_NEG_RSP nor RDP_NEG_FAILURE", "type", typ)
	}
	if r.Remaining() != 0 {
		return cc, errors.New(TagTrailingBytes, "bytes after the Connection Confirm", "left", r.Remaining())
	}
	return cc, nil
}

// WriteDataHeader appends the header of a Data TPDU (02 f0 80); the PDU it
// carries follows.
func WriteDataHeader(w *wire.Writer) {
	w.U8(DataHeaderLen - 1)
	w.U8(CodeData)
	w.U8(eot)
}

// ReadDataHeader reads the header of a Data TPDU; r is left at the PDU it
// carries.
func ReadDataHeader(r *wire.Reader) error {
	li, err := r.U8()
	if err != nil {
		return err
	}
	if li != DataHeaderLen-1 {
		return errors.New(TagLengthIndicatorMismatch, "Data TPDU length indicator is not 2", "li", li)
	}
	code, err := r.U8()
	if err != nil {
		return err
	}
	if code != CodeData {
		return errors.New(TagUnexpectedCode, "not a Data TPDU", "code", code)
	}
	mark, err := r.U8()
	if err != nil {
		return err
	}
	if mark&eot == 0 {
		return errors.New(TagEOTMissing, "Data TPDU without the end-of-TSDU mark", "byte", mark)
	}
	return nil
}

// DisconnectRequest is an X.224 Disconnect Request.
type DisconnectRequest struct {
	DstRef, SrcRef uint16
	// Reason is the disconnect reason; 0 is "not specified".
	Reason uint8
}

// disconnectLen is the size of a Disconnect Request without user data.
const disconnectLen = 7

// WriteDisconnectRequest appends dr as a Disconnect Request TPDU.
func WriteDisconnectRequest(w *wire.Writer, dr *DisconnectRequest) {
	w.U8(disconnectLen - 1)
	w.U8(CodeDisconnectRequest)
	w.U16BE(dr.DstRef)
	w.U16BE(dr.SrcRef)
	w.U8(dr.Reason)
}

// ParseDisconnectRequest reads a Disconnect Request TPDU; variable-part
// parameters and user data after the reason are skipped.
func ParseDisconnectRequest(tpdu []byte) (DisconnectRequest, error) {
	var dr DisconnectRequest
	r := wire.NewReader(tpdu)
	li, err := r.U8()
	if err != nil {
		return dr, err
	}
	if int(li) != len(tpdu)-1 || li < disconnectLen-1 {
		return dr, errors.New(TagLengthIndicatorMismatch, "length indicator does not match the TPDU", "li", li, "len", len(tpdu))
	}
	code, err := r.U8()
	if err != nil {
		return dr, err
	}
	if code != CodeDisconnectRequest {
		return dr, errors.New(TagUnexpectedCode, "not a Disconnect Request", "code", code)
	}
	if dr.DstRef, err = r.U16BE(); err != nil {
		return dr, err
	}
	if dr.SrcRef, err = r.U16BE(); err != nil {
		return dr, err
	}
	if dr.Reason, err = r.U8(); err != nil {
		return dr, err
	}
	return dr, nil
}

// Code returns the kind of TPDU tpdu is: one of the Code constants, with the
// credit bits of a CR or CC cleared, or the raw byte for another kind.
func Code(tpdu []byte) (uint8, error) {
	r := wire.NewReader(tpdu)
	if err := r.Skip(1); err != nil {
		return 0, err
	}
	c, err := r.U8()
	if err != nil {
		return 0, err
	}
	switch c & 0xf0 {
	case CodeConnectionRequest, CodeConnectionConfirm:
		return c & 0xf0, nil
	}
	return c, nil
}
