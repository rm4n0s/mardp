// Package tpkt frames RDP's traffic on the TCP stream (T.123 and
// MS-RDPBCGR 2.2.9.1.2). Every PDU is either a TPKT (version 3, a 4-byte
// header with a big-endian length) carrying an X.224 TPDU, or a fast-path PDU,
// whose first byte has 0 in its two low bits. The first byte tells which.
package tpkt

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors this package returns.
const (
	// TagUnknownFrame: a first byte that starts neither a TPKT nor a
	// fast-path PDU.
	TagUnknownFrame = "FrameUnknown"
	// TagHeaderTruncated: fewer header bytes than the frame's kind needs.
	TagHeaderTruncated = "FrameHeaderTruncated"
	// TagLengthInvalid: a length shorter than the smallest valid frame.
	TagLengthInvalid = "FrameLengthInvalid"
	// TagTooLong: a TPKT whose contents do not fit its 16-bit length.
	TagTooLong = "TPKTTooLong"
)

// Version is the TPKT version RDP uses, and so the first byte of a TPKT.
const Version = 3

// HeaderLen is the size of a TPKT header.
const HeaderLen = 4

// MinLength is the smallest TPKT RDP sends: the header and the 3-byte header
// of an X.224 Data TPDU.
const MinLength = HeaderLen + 3

// MaxLength is the largest TPKT (its length field is 16 bits).
const MaxLength = 0xffff

// Kind is what a frame is.
type Kind int

// The kinds of frame.
const (
	Unknown Kind = iota
	TPKT
	FastPath
)

// String names the kind for logs.
func (k Kind) String() string {
	switch k {
	case TPKT:
		return "TPKT"
	case FastPath:
		return "fast-path"
	default:
		return "unknown"
	}
}

// Classify tells a frame's kind from its first byte: 3 for a TPKT, and a
// value whose two low bits are 0 (FASTPATH_INPUT_ACTION_FASTPATH,
// FASTPATH_OUTPUT_ACTION_FASTPATH) for fast-path.
func Classify(b0 byte) Kind {
	switch {
	case b0 == Version:
		return TPKT
	case b0&0x03 == 0:
		return FastPath
	default:
		return Unknown
	}
}

// HeaderSize is how many bytes, from the first two, hold the frame's length:
// 4 for a TPKT, 2 or 3 for fast-path (one length byte, or two when the high
// bit of the first is set), 0 for an unknown kind.
func HeaderSize(b0, b1 byte) int {
	switch Classify(b0) {
	case TPKT:
		return HeaderLen
	case FastPath:
		if b1&0x80 != 0 {
			return 3
		}
		return 2
	default:
		return 0
	}
}

// FrameLength reads a frame's header and returns its kind and total length,
// header included. hdr holds at least HeaderSize bytes.
func FrameLength(hdr []byte) (Kind, int, error) {
	if len(hdr) < 2 {
		return Unknown, 0, errors.New(TagHeaderTruncated, "frame header shorter than 2 bytes", "have", len(hdr))
	}
	k := Classify(hdr[0])
	need := HeaderSize(hdr[0], hdr[1])
	if k == Unknown {
		return Unknown, 0, errors.New(TagUnknownFrame, "neither a TPKT nor a fast-path PDU", "byte", hdr[0])
	}
	if len(hdr) < need {
		return k, 0, errors.New(TagHeaderTruncated, "frame header truncated", "kind", k.String(), "need", need, "have", len(hdr))
	}
	var n int
	if k == TPKT {
		n = int(hdr[2])<<8 | int(hdr[3])
		if n < MinLength {
			return k, 0, errors.New(TagLengthInvalid, "TPKT length below the minimum", "length", n, "min", MinLength)
		}
		return k, n, nil
	}
	if need == 3 {
		n = int(hdr[1]&0x7f)<<8 | int(hdr[2])
	} else {
		n = int(hdr[1])
	}
	if n <= need {
		return k, 0, errors.New(TagLengthInvalid, "fast-path length not past its header", "length", n, "header", need)
	}
	return k, n, nil
}

// Begin appends a TPKT header whose length End fills in, and returns its
// offset.
func Begin(w *wire.Writer) int {
	off := w.Len()
	w.U8(Version)
	w.U8(0)
	w.U16BE(0)
	return off
}

// End sets the length of the TPKT that Begin started at off to everything
// written since.
func End(w *wire.Writer, off int) error {
	n := w.Len() - off
	if n > MaxLength {
		return errors.New(TagTooLong, "TPKT longer than 65535 bytes", "length", n)
	}
	return w.PatchU16BE(off+2, uint16(n))
}
