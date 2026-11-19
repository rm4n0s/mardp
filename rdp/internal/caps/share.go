// Package caps reads and writes the PDUs of the Capabilities Exchange and
// Connection Finalization phases of MS-RDPBCGR (1.3.1.1): the Share Control
// and Share Data Headers that every share PDU begins with (2.2.8.1.1.1), the
// Demand Active and Confirm Active PDUs with their capability sets
// (2.2.1.13, 2.2.7), and the Synchronize, Control, Font List and Font Map
// PDUs (2.2.1.14 to 2.2.1.22).
//
// TLS is in use, so a share PDU has no security header (2.2.8.1.1.2): the
// server's security data says there is no encryption.
//
// Readers are lenient where Microsoft's own examples (section 4.1) break the
// rules: pad1 and padding are ignored, uncompressedLength and streamID are
// not checked, and a capability set may be shorter than its structure when
// the server does not read it (the example's Font Capability Set has 4
// bytes).
package caps

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors this package returns.
const (
	// TagLengthInvalid: a Share Control Header whose totalLength is smaller
	// than the header or larger than the bytes there are.
	TagLengthInvalid = "CapsLengthInvalid"
	// TagVersionInvalid: a Share Control Header whose PDUVersion is not
	// TS_PROTOCOL_VERSION.
	TagVersionInvalid = "CapsVersionInvalid"
	// TagCompressed: a Data PDU compressed with bulk compression, which the
	// server does not support.
	TagCompressed = "CapsCompressed"
	// TagTooLong: a PDU being written that is longer than totalLength can
	// say.
	TagTooLong = "CapsTooLong"
	// TagSetInvalid: a capability set whose lengthCapability is smaller
	// than its header or runs past the capabilities, or fewer sets than
	// numberCapabilities says.
	TagSetInvalid = "CapsSetInvalid"
	// TagSetShort: a capability set the server reads that is shorter than
	// the fields it reads.
	TagSetShort = "CapsSetShort"
)

// PDU types, the type subfield of pduType (MS-RDPBCGR 2.2.8.1.1.1.1).
const (
	TypeDemandActive   uint16 = 0x1
	TypeConfirmActive  uint16 = 0x3
	TypeDeactivateAll  uint16 = 0x6
	TypeData           uint16 = 0x7
	TypeServerRedirect uint16 = 0xa
)

// Data PDU types, pduType2 (MS-RDPBCGR 2.2.8.1.1.1.2).
const (
	Type2Update                uint8 = 0x02
	Type2Control               uint8 = 0x14
	Type2Pointer               uint8 = 0x1b
	Type2Input                 uint8 = 0x1c
	Type2Synchronize           uint8 = 0x1f
	Type2RefreshRect           uint8 = 0x21
	Type2PlaySound             uint8 = 0x22
	Type2SuppressOutput        uint8 = 0x23
	Type2ShutdownRequest       uint8 = 0x24
	Type2ShutdownDenied        uint8 = 0x25
	Type2SaveSessionInfo       uint8 = 0x26
	Type2FontList              uint8 = 0x27
	Type2FontMap               uint8 = 0x28
	Type2SetKeyboardIndicators uint8 = 0x29
	Type2PersistentKeyList     uint8 = 0x2b
	Type2BitmapCacheError      uint8 = 0x2c
	Type2SetKeyboardIMEStatus  uint8 = 0x2d
	Type2OffscreenCacheError   uint8 = 0x2e
	Type2SetErrorInfo          uint8 = 0x2f
	Type2DrawNineGridError     uint8 = 0x30
	Type2DrawGDIPlusError      uint8 = 0x31
	Type2ARCStatus             uint8 = 0x32
	Type2StatusInfo            uint8 = 0x36
	Type2MonitorLayout         uint8 = 0x37
)

// Stream IDs of the Share Data Header.
const (
	StreamUndefined uint8 = 0x00
	StreamLow       uint8 = 0x01
	StreamMedium    uint8 = 0x02
	StreamHigh      uint8 = 0x04
)

// Header sizes.
const (
	ShareControlLen = 6
	ShareDataLen    = 18
)

const (
	// protocolVersion is TS_PROTOCOL_VERSION in the PDUVersion subfield,
	// the top 12 bits of pduType.
	protocolVersion uint16 = 0x0010
	// flowMarker in totalLength makes the PDU a T.128 FlowPDU, which MUST
	// be ignored (2.2.8.1.1.1.1). A FlowPDU has 8 bytes (T.128 8.5).
	flowMarker = 0x8000
	flowPDULen = 8
	// uncompressedBase: uncompressedLength counts from pduType2 on, the 4
	// bytes of the header after it and then the data (Microsoft's client
	// examples: totalLength 26, uncompressedLength 12).
	uncompressedBase = 14
	// packetCompressed is PACKET_COMPRESSED in compressedType.
	packetCompressed uint8 = 0x20
)

// PDU is one share PDU: its Share Control Header and what follows it.
type PDU struct {
	// Flow is set for a T.128 FlowPDU, which is to be ignored; the other
	// fields are then zero.
	Flow bool
	// Type is the type subfield of pduType, one of the Type constants.
	Type uint16
	// Source is pduSource, the MCS channel ID of the sender.
	Source uint16
	// Body is the PDU after the Share Control Header, a slice of the input.
	Body []byte
}

// ReadPDU reads one share PDU from r: totalLength bytes, or a FlowPDU. A
// Send Data PDU may carry more than one; the caller reads while bytes
// remain.
func ReadPDU(r *wire.Reader) (PDU, error) {
	rest := r.Rest()
	total, err := r.U16LE()
	if err != nil {
		return PDU{}, err
	}
	if total == flowMarker {
		if err := r.Skip(min(flowPDULen-2, r.Remaining())); err != nil {
			return PDU{}, err
		}
		return PDU{Flow: true}, nil
	}
	if int(total) < ShareControlLen || int(total) > len(rest) {
		return PDU{}, errors.New(TagLengthInvalid, "a Share Control Header whose totalLength does not fit",
			"totalLength", total, "length", len(rest))
	}
	typ, err := r.U16LE()
	if err != nil {
		return PDU{}, err
	}
	if typ&0xfff0 != protocolVersion {
		return PDU{}, errors.New(TagVersionInvalid, "a Share Control Header with another PDUVersion than 1", "pduType", typ)
	}
	src, err := r.U16LE()
	if err != nil {
		return PDU{}, err
	}
	body, err := r.Bytes(int(total) - ShareControlLen)
	if err != nil {
		return PDU{}, err
	}
	return PDU{Type: typ & 0x000f, Source: src, Body: body}, nil
}

// Data is the Share Data Header of a Data PDU and what follows it.
type Data struct {
	ShareID  uint32
	StreamID uint8
	// Type2 is pduType2, one of the Type2 constants.
	Type2 uint8
	// Body is the PDU after the Share Data Header, a slice of the input.
	Body []byte
}

// ParseData reads the Share Data Header from the body of a PDU of type
// TypeData. pad1, streamID and uncompressedLength are not checked, since
// Microsoft's own examples get them wrong.
func ParseData(body []byte) (Data, error) {
	r := wire.NewReader(body)
	var d Data
	var err error
	if d.ShareID, err = r.U32LE(); err != nil {
		return d, err
	}
	if err := r.Skip(1); err != nil { // pad1
		return d, err
	}
	if d.StreamID, err = r.U8(); err != nil {
		return d, err
	}
	if err := r.Skip(2); err != nil { // uncompressedLength
		return d, err
	}
	if d.Type2, err = r.U8(); err != nil {
		return d, err
	}
	ct, err := r.U8()
	if err != nil {
		return d, err
	}
	if ct&packetCompressed != 0 {
		return d, errors.New(TagCompressed, "a compressed Data PDU", "compressedType", ct, "pduType2", d.Type2)
	}
	if err := r.Skip(2); err != nil { // compressedLength
		return d, err
	}
	d.Body = r.Rest()
	return d, nil
}

// BeginControl appends a Share Control Header of type typ from source and
// returns its offset, for EndControl to fill in totalLength.
func BeginControl(w *wire.Writer, typ, source uint16) int {
	off := w.Reserve(2)
	w.U16LE(protocolVersion | typ)
	w.U16LE(source)
	return off
}

// EndControl fills in the totalLength of the PDU BeginControl began at off.
func EndControl(w *wire.Writer, off int) error {
	n := w.Len() - off
	if n > 0xffff || n == flowMarker {
		return errors.New(TagTooLong, "a share PDU longer than totalLength can say", "length", n)
	}
	return w.PatchU16LE(off, uint16(n))
}

// BeginData appends the Share Control and Share Data Headers of a Data PDU
// of type type2 from source, in the low-priority stream and uncompressed,
// and returns its offset, for EndData to fill in the lengths.
func BeginData(w *wire.Writer, shareID uint32, source uint16, type2 uint8) int {
	off := BeginControl(w, TypeData, source)
	w.U32LE(shareID)
	w.U8(0) // pad1
	w.U8(StreamLow)
	w.U16LE(0) // uncompressedLength, filled in by EndData
	w.U8(type2)
	w.U8(0)    // compressedType
	w.U16LE(0) // compressedLength
	return off
}

// EndData fills in totalLength and uncompressedLength of the Data PDU
// BeginData began at off.
func EndData(w *wire.Writer, off int) error {
	if err := EndControl(w, off); err != nil {
		return err
	}
	return w.PatchU16LE(off+12, uint16(w.Len()-off-uncompressedBase))
}
