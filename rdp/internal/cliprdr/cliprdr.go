// Package cliprdr reads and writes the PDUs of the clipboard channel
// (MS-RDPECLIP 2.2), which travel on the cliprdr static channel: the
// capabilities, Monitor Ready, the Format List and its response, the Format
// Data Request and its response, and the text of the two text formats. Both
// sides are here: the server's in the library, the client's in rdptest.
// Files, the temporary directory and locking are not. Sending the PDUs is
// the caller's.
package cliprdr

import (
	"unicode/utf16"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors this package returns.
const (
	// TagHeaderInvalid: a message shorter than the PDU header, or whose
	// header states more bytes than the message has.
	TagHeaderInvalid = "CliprdrHeaderInvalid"
	// TagBodyInvalid: a body too short for its type.
	TagBodyInvalid = "CliprdrBodyInvalid"
	// TagFormatsInvalid: a Format List whose names do not fit its body.
	TagFormatsInvalid = "CliprdrFormatsInvalid"
)

// The msgType of the PDU header (2.2.1).
const (
	MsgMonitorReady       uint16 = 0x0001
	MsgFormatList         uint16 = 0x0002
	MsgFormatListResponse uint16 = 0x0003
	MsgFormatDataRequest  uint16 = 0x0004
	MsgFormatDataResponse uint16 = 0x0005
	MsgTempDirectory      uint16 = 0x0006
	MsgCaps               uint16 = 0x0007
	MsgFileContentsReq    uint16 = 0x0008
	MsgFileContentsResp   uint16 = 0x0009
	MsgLockClipData       uint16 = 0x000a
	MsgUnlockClipData     uint16 = 0x000b
)

// The msgFlags of the PDU header (2.2.1).
const (
	FlagResponseOK   uint16 = 0x0001
	FlagResponseFail uint16 = 0x0002
	// FlagASCIINames: the short format names of a Format List are 8-bit.
	FlagASCIINames uint16 = 0x0004
)

// HeaderLength is the length of the PDU header (CLIPRDR_HEADER).
const HeaderLength = 8

// The general capability set (2.2.2.1.1.1).
const (
	capsTypeGeneral   uint16 = 0x0001
	capsGeneralLength uint16 = 12
	capsVersion2      uint32 = 0x00000002
	// CapsLongFormatNames: the side writes and reads the Long Format
	// Names of a Format List.
	CapsLongFormatNames uint32 = 0x00000002
)

// The standard clipboard formats of text (2.2.3.1, Windows' CF_ values).
const (
	// FormatText is CF_TEXT: 8-bit text, ended by a zero.
	FormatText uint32 = 1
	// FormatUnicodeText is CF_UNICODETEXT: UTF-16LE, ended by a zero.
	FormatUnicodeText uint32 = 13
)

// shortNameLength is the length of a short format name, in bytes.
const shortNameLength = 32

// PDU is a clipboard PDU: its type, its flags and its body.
type PDU struct {
	Type, Flags uint16
	Data        []byte
}

// ReadPDU reads the PDU in msg; Data is a part of msg. Bytes after the
// length the header states are padding.
func ReadPDU(msg []byte) (PDU, error) {
	r := wire.NewReader(msg)
	if r.Remaining() < HeaderLength {
		return PDU{}, errors.New(TagHeaderInvalid, "a clipboard message shorter than its header", "length", len(msg))
	}
	var p PDU
	p.Type, _ = r.U16LE()  // the length was checked
	p.Flags, _ = r.U16LE() // the length was checked
	n, _ := r.U32LE()      // the length was checked
	if int64(n) > int64(r.Remaining()) {
		return PDU{}, errors.New(TagHeaderInvalid, "a clipboard PDU's header states more bytes than it has",
			"type", p.Type, "dataLen", n, "have", r.Remaining())
	}
	p.Data = r.Rest()[:n]
	return p, nil
}

// begin appends the header of a PDU and returns where its length goes.
func begin(w *wire.Writer, t, flags uint16) int {
	w.U16LE(t)
	w.U16LE(flags)
	return w.Reserve(4)
}

// end writes the length of the PDU begun at off.
func end(w *wire.Writer, off int) {
	_ = w.PatchU32LE(off, uint32(w.Len()-off-4)) // off is begin's
}

// AppendCaps appends a Clipboard Capabilities PDU with the general
// capability set: version 2 and generalFlags.
func AppendCaps(w *wire.Writer, generalFlags uint32) {
	off := begin(w, MsgCaps, 0)
	w.U16LE(1) // cCapabilitiesSets
	w.U16LE(0) // pad1
	w.U16LE(capsTypeGeneral)
	w.U16LE(capsGeneralLength)
	w.U32LE(capsVersion2)
	w.U32LE(generalFlags)
	end(w, off)
}

// ReadCaps reads the body of a Clipboard Capabilities PDU: the general
// flags, 0 when it has no general capability set.
func ReadCaps(data []byte) (uint32, error) {
	r := wire.NewReader(data)
	n, err := r.U16LE()
	if err != nil {
		return 0, errors.New(TagBodyInvalid, "clipboard capabilities without their count", "length", len(data))
	}
	if err := r.Skip(2); err != nil {
		return 0, errors.New(TagBodyInvalid, "clipboard capabilities cut short", "length", len(data))
	}
	for range n {
		t, err1 := r.U16LE()
		size, err2 := r.U16LE()
		if err1 != nil || err2 != nil || size < 4 || int(size)-4 > r.Remaining() {
			return 0, errors.New(TagBodyInvalid, "a clipboard capability set that does not fit", "type", t, "length", size)
		}
		set, _ := r.Sub(int(size) - 4) // the length was checked
		if t != capsTypeGeneral {
			continue
		}
		if set.Remaining() < 8 {
			return 0, errors.New(TagBodyInvalid, "a general clipboard capability set too short", "length", size)
		}
		_ = set.Skip(4)         // the version; the length was checked
		flags, _ := set.U32LE() // the length was checked
		return flags, nil
	}
	return 0, nil
}

// AppendMonitorReady appends a Monitor Ready PDU.
func AppendMonitorReady(w *wire.Writer) {
	end(w, begin(w, MsgMonitorReady, 0))
}

// Format is a clipboard format a side has: its ID, and its name when it is
// not a standard one.
type Format struct {
	ID   uint32
	Name string
}

// AppendFormatList appends a Format List PDU with formats: Long Format
// Names when long, else Short Format Names in UTF-16, a name cut at 15
// characters.
func AppendFormatList(w *wire.Writer, formats []Format, long bool) {
	off := begin(w, MsgFormatList, 0)
	for _, f := range formats {
		w.U32LE(f.ID)
		name := utf16.Encode([]rune(f.Name))
		if long {
			for _, u := range name {
				w.U16LE(u)
			}
			w.U16LE(0)
			continue
		}
		name = name[:min(len(name), shortNameLength/2-1)]
		for _, u := range name {
			w.U16LE(u)
		}
		w.Zeros(shortNameLength - 2*len(name))
	}
	end(w, off)
}

// ReadFormatList reads the formats of the Format List PDU p: Long Format
// Names when long, else Short Format Names, 8-bit when p has
// FlagASCIINames.
func ReadFormatList(p PDU, long bool) ([]Format, error) {
	r := wire.NewReader(p.Data)
	var formats []Format
	for r.Remaining() > 0 {
		id, err := r.U32LE()
		if err != nil {
			return nil, errors.New(TagFormatsInvalid, "a clipboard format cut short", "formats", len(formats), "left", r.Remaining())
		}
		f := Format{ID: id}
		switch {
		case long:
			var name []uint16
			for {
				u, err := r.U16LE()
				if err != nil {
					return nil, errors.New(TagFormatsInvalid, "a long format name without its end", "id", id, "formats", len(formats))
				}
				if u == 0 {
					break
				}
				name = append(name, u)
			}
			f.Name = string(utf16.Decode(name))
		default:
			b, err := r.Bytes(shortNameLength)
			if err != nil {
				return nil, errors.New(TagFormatsInvalid, "a short format name cut short", "id", id, "formats", len(formats))
			}
			if p.Flags&FlagASCIINames != 0 {
				f.Name = latin1(b)
			} else {
				f.Name = DecodeText(b)
			}
		}
		formats = append(formats, f)
	}
	return formats, nil
}

// AppendFormatListResponse appends a Format List Response PDU.
func AppendFormatListResponse(w *wire.Writer, ok bool) {
	end(w, begin(w, MsgFormatListResponse, response(ok)))
}

// response is the flag of a response.
func response(ok bool) uint16 {
	if ok {
		return FlagResponseOK
	}
	return FlagResponseFail
}

// AppendFormatDataRequest appends a Format Data Request PDU for the format
// id.
func AppendFormatDataRequest(w *wire.Writer, id uint32) {
	off := begin(w, MsgFormatDataRequest, 0)
	w.U32LE(id)
	end(w, off)
}

// ReadFormatDataRequest reads the body of a Format Data Request PDU: the
// format asked for.
func ReadFormatDataRequest(data []byte) (uint32, error) {
	r := wire.NewReader(data)
	id, err := r.U32LE()
	if err != nil {
		return 0, errors.New(TagBodyInvalid, "a Format Data Request without its format", "length", len(data))
	}
	return id, nil
}

// AppendFormatDataResponse appends a Format Data Response PDU: data when
// ok, a failure with no data otherwise.
func AppendFormatDataResponse(w *wire.Writer, ok bool, data []byte) {
	off := begin(w, MsgFormatDataResponse, response(ok))
	if ok {
		w.Write(data)
	}
	end(w, off)
}

// EncodeText appends s to dst as FormatUnicodeText carries it: UTF-16LE,
// each line ended by CR LF (a lone LF gets its CR), and a zero at the end.
func EncodeText(dst []byte, s string) []byte {
	put := func(u uint16) { dst = append(dst, byte(u), byte(u>>8)) }
	prev := rune(0)
	for _, r := range s {
		if r == '\n' && prev != '\r' {
			put('\r')
		}
		prev = r
		if r >= 0x10000 {
			r1, r2 := utf16.EncodeRune(r)
			put(uint16(r1))
			put(uint16(r2))
			continue
		}
		put(uint16(r))
	}
	put(0)
	return dst
}

// DecodeText is the text of b, UTF-16LE as FormatUnicodeText carries it:
// up to its first zero, a CR LF turned into LF. A lone surrogate becomes
// U+FFFD; an odd last byte is dropped.
func DecodeText(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		v := uint16(b[i]) | uint16(b[i+1])<<8
		if v == 0 {
			break
		}
		if v == '\n' && len(u) > 0 && u[len(u)-1] == '\r' {
			u = u[:len(u)-1]
		}
		u = append(u, v)
	}
	return string(utf16.Decode(u))
}

// EncodeText8 appends s to dst as FormatText carries it: one byte a
// character, '?' for a character over U+00FF, each line ended by CR LF,
// and a zero at the end.
func EncodeText8(dst []byte, s string) []byte {
	prev := rune(0)
	for _, r := range s {
		if r == '\n' && prev != '\r' {
			dst = append(dst, '\r')
		}
		prev = r
		if r > 0xff {
			r = '?'
		}
		dst = append(dst, byte(r))
	}
	return append(dst, 0)
}

// latin1 is the text of b up to its first zero, one byte a character.
func latin1(b []byte) string {
	r := make([]rune, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		r = append(r, rune(c))
	}
	return string(r)
}
