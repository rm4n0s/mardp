package sec

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors of the Set Error Info PDU.
const (
	// TagShareHeaderInvalid: a Share Control Header whose totalLength is
	// not the size of the PDU, or whose type is not a Data PDU.
	TagShareHeaderInvalid = "SecShareHeaderInvalid"
	// TagNotSetErrorInfo: a Data PDU of another type than Set Error Info.
	TagNotSetErrorInfo = "SecNotSetErrorInfo"
)

// Share header values (MS-RDPBCGR 2.2.8.1.1.1).
const (
	pduTypeData          uint16 = 0x0007
	protocolVersion      uint16 = 0x0010
	streamLow            uint8  = 0x01
	pduType2SetErrorInfo uint8  = 0x2f
	shareDataHeaderLen          = 18
	// uncompressedLength counts from pduType2 on: the 4 bytes of the
	// header after it, then the data (Microsoft's Client Control PDU
	// example: totalLength 26, uncompressedLength 12).
	uncompressedBase = 14
)

// Set Error Info codes the server sends (MS-RDPBCGR 2.2.5.1.1).
const (
	ErrInfoNone                      uint32 = 0x00000000
	ErrInfoRPCInitiatedDisconnect    uint32 = 0x00000001
	ErrInfoDisconnectedByOther       uint32 = 0x00000005
	ErrInfoServerDeniedConnection    uint32 = 0x00000007
	ErrInfoInsufficientPrivileges    uint32 = 0x00000009
	ErrInfoFreshCredentialsRequired  uint32 = 0x0000000a
	ErrInfoRPCInitiatedDisconnByUser uint32 = 0x0000000b
	ErrInfoLogoffByUser              uint32 = 0x0000000c
	ErrInfoServerShutdown            uint32 = 0x00000019
)

// setErrorInfoLen is the size of the Set Error Info PDU.
const setErrorInfoLen = shareDataHeaderLen + 4

// WriteSetErrorInfo appends a Set Error Info PDU (MS-RDPBCGR 2.2.5.1) with
// code, in the share shareID. TLS is in use, so there is no security header
// (2.2.8.1.1.2). pduSource is 0, as 2.2.5.1.1 requires.
func WriteSetErrorInfo(w *wire.Writer, shareID, code uint32) {
	w.U16LE(setErrorInfoLen)
	w.U16LE(protocolVersion | pduTypeData)
	w.U16LE(0)
	w.U32LE(shareID)
	w.U8(0)
	w.U8(streamLow)
	w.U16LE(setErrorInfoLen - uncompressedBase)
	w.U8(pduType2SetErrorInfo)
	w.U8(0)
	w.U16LE(0)
	w.U32LE(code)
}

// ParseSetErrorInfo reads a Set Error Info PDU and returns its share ID and
// code.
func ParseSetErrorInfo(b []byte) (shareID, code uint32, err error) {
	r := wire.NewReader(b)
	total, err := r.U16LE()
	if err != nil {
		return 0, 0, err
	}
	typ, err := r.U16LE()
	if err != nil {
		return 0, 0, err
	}
	if int(total) != len(b) || typ&0x0f != pduTypeData {
		return 0, 0, errors.New(TagShareHeaderInvalid, "not a Data PDU of the length it says", "totalLength", total, "length", len(b), "pduType", typ)
	}
	if err := r.Skip(2); err != nil { // pduSource
		return 0, 0, err
	}
	if shareID, err = r.U32LE(); err != nil {
		return 0, 0, err
	}
	if err := r.Skip(4); err != nil { // pad1, streamID, uncompressedLength
		return 0, 0, err
	}
	t2, err := r.U8()
	if err != nil {
		return 0, 0, err
	}
	if t2 != pduType2SetErrorInfo {
		return 0, 0, errors.New(TagNotSetErrorInfo, "a Data PDU other than Set Error Info", "pduType2", t2)
	}
	if err := r.Skip(3); err != nil { // compressedType, compressedLength
		return 0, 0, err
	}
	code, err = r.U32LE()
	return shareID, code, err
}
