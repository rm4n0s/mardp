// Package fastpath frames the server's fast-path output (MS-RDPBCGR
// 2.2.9.1.2): Fast-Path Update PDUs, each carrying one update or one
// fragment of it, and the reassembly a client does (3.2.5.9.3.1), which
// rdptest uses.
package fastpath

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors this package returns.
const (
	// TagUpdateTooLarge: an update larger than the client reassembles.
	TagUpdateTooLarge = "FastPathUpdateTooLarge"
	// TagUpdateCodeInvalid: an update code that does not fit in 4 bits.
	TagUpdateCodeInvalid = "FastPathUpdateCodeInvalid"
	// TagHeaderInvalid: a PDU whose action is not fast-path, or which is
	// encrypted (it never is under TLS).
	TagHeaderInvalid = "FastPathHeaderInvalid"
	// TagLengthInvalid: a PDU whose length is not that of its frame.
	TagLengthInvalid = "FastPathLengthInvalid"
	// TagFragmentSequence: fragments out of the order of 3.2.5.9.3.1, or
	// whose update code or compression changes within a sequence.
	TagFragmentSequence = "FastPathFragmentSequence"
	// TagReassemblyTooLarge: fragments that add up to more than the
	// reassembly buffer holds.
	TagReassemblyTooLarge = "FastPathReassemblyTooLarge"
)

// The update codes of the updateHeader (2.2.9.1.2.1).
const (
	UpdateOrders          uint8 = 0x0
	UpdateBitmap          uint8 = 0x1
	UpdatePalette         uint8 = 0x2
	UpdateSynchronize     uint8 = 0x3
	UpdateSurfaceCommands uint8 = 0x4
	UpdatePointerHidden   uint8 = 0x5
	UpdatePointerDefault  uint8 = 0x6
	UpdatePointerPosition uint8 = 0x8
	UpdateColorPointer    uint8 = 0x9
	UpdateCachedPointer   uint8 = 0xa
	UpdateNewPointer      uint8 = 0xb
	UpdateLargePointer    uint8 = 0xc
)

// The fragmentation values of the updateHeader.
const (
	FragmentSingle uint8 = 0x0
	FragmentLast   uint8 = 0x1
	FragmentFirst  uint8 = 0x2
	FragmentNext   uint8 = 0x3
)

// compressionUsed is FASTPATH_OUTPUT_COMPRESSION_USED: compressionFlags
// follows the updateHeader.
const compressionUsed = 0x2

// MaxPDULength is the longest PDU the server sends: "the overall PDU length
// SHOULD be less than or equal to 16,383 bytes" (2.2.9.1.2).
const MaxPDULength = 16383

const (
	// pduHeaderLen is fpOutputHeader with two length bytes.
	pduHeaderLen = 3
	// updateHeaderLen is updateHeader and size, without compressionFlags,
	// which the server never sends.
	updateHeaderLen = 3
	// shortLengthMax is the longest PDU whose length fits in one byte.
	shortLengthMax = 0x7f
)

// MaxFragment is the most update data one PDU carries.
const MaxFragment = MaxPDULength - pduHeaderLen - updateHeaderLen

// MaxUpdate returns the largest update a client takes whose Multifragment
// Update set states maxRequestSize (0 when it sent none): what fits in one
// PDU, or more when its reassembly buffer is larger.
func MaxUpdate(maxRequestSize int) int {
	return max(maxRequestSize, MaxFragment)
}

// AppendUpdate appends the PDUs that carry one update of type code with
// data: one PDU when data fits in MaxFragment, otherwise a FIRST fragment,
// NEXT fragments and a LAST one, a PDU each. maxUpdate is the largest update
// the client takes (MaxUpdate). The PDUs are not encrypted, since TLS
// carries them, and not compressed.
func AppendUpdate(w *wire.Writer, code uint8, data []byte, maxUpdate int) error {
	if code > 0xf {
		return errors.New(TagUpdateCodeInvalid, "update code wider than 4 bits", "code", code)
	}
	if len(data) > MaxUpdate(maxUpdate) {
		return errors.New(TagUpdateTooLarge, "update larger than the client reassembles", "length", len(data), "max", MaxUpdate(maxUpdate))
	}
	if len(data) <= MaxFragment {
		appendPDU(w, code, FragmentSingle, data)
		return nil
	}
	for off := 0; off < len(data); off += MaxFragment {
		end := min(off+MaxFragment, len(data))
		frag := FragmentNext
		switch {
		case off == 0:
			frag = FragmentFirst
		case end == len(data):
			frag = FragmentLast
		}
		appendPDU(w, code, frag, data[off:end])
	}
	return nil
}

// appendPDU appends one PDU with one update of at most MaxFragment bytes.
func appendPDU(w *wire.Writer, code, frag uint8, data []byte) {
	// fpOutputHeader: action FASTPATH_OUTPUT_ACTION_FASTPATH (0) in the two
	// low bits, no flags.
	w.U8(0)
	// The length counts itself: one byte up to 127, two beyond.
	if short := 2 + updateHeaderLen + len(data); short <= shortLengthMax {
		w.U8(uint8(short))
	} else {
		w.U16BE(uint16(pduHeaderLen+updateHeaderLen+len(data)) | 0x8000)
	}
	w.U8(code | frag<<4)
	w.U16LE(uint16(len(data)))
	w.Write(data)
}

// Update is one Fast-Path Update (2.2.9.1.2.1) as a PDU carries it, or a
// whole update after reassembly.
type Update struct {
	Code          uint8
	Fragmentation uint8
	// Compressed says compressionFlags is present, with its value in
	// CompressionFlags.
	Compressed       bool
	CompressionFlags uint8
	// Data points into the bytes read.
	Data []byte
}

// ParsePDU checks the header of one fast-path output PDU, a whole frame as
// tpkt.ReadFrame returns it, and returns a reader at its updates.
func ParsePDU(frame []byte) (wire.Reader, error) {
	r := wire.NewReader(frame)
	h, err := r.U8()
	if err != nil {
		return r, err
	}
	if h&0x03 != 0 || h>>6 != 0 {
		return r, errors.New(TagHeaderInvalid, "not a plain fast-path output header", "header", h)
	}
	l1, err := r.U8()
	if err != nil {
		return r, err
	}
	n := int(l1)
	if l1&0x80 != 0 {
		l2, err := r.U8()
		if err != nil {
			return r, err
		}
		n = int(l1&0x7f)<<8 | int(l2)
	}
	if n != len(frame) {
		return r, errors.New(TagLengthInvalid, "PDU length is not the frame's", "length", n, "frame", len(frame))
	}
	return r, nil
}

// ReadUpdate reads the next update of a PDU.
func ReadUpdate(r *wire.Reader) (Update, error) {
	h, err := r.U8()
	if err != nil {
		return Update{}, err
	}
	u := Update{Code: h & 0xf, Fragmentation: h >> 4 & 0x3, Compressed: h>>6&compressionUsed != 0}
	if u.Compressed {
		if u.CompressionFlags, err = r.U8(); err != nil {
			return Update{}, err
		}
	}
	n, err := r.U16LE()
	if err != nil {
		return Update{}, err
	}
	if u.Data, err = r.Bytes(int(n)); err != nil {
		return Update{}, err
	}
	return u, nil
}

// Reassembler joins fragments into whole updates (3.2.5.9.3.1).
type Reassembler struct {
	// Max is the size of the reassembly buffer, the MaxRequestSize the
	// client states.
	Max int

	buf  []byte
	open bool
	head Update
}

// Add takes the next update read. It returns the whole update and true when
// u is unfragmented or the LAST fragment; the data of a reassembled update
// is valid until the next Add.
func (a *Reassembler) Add(u Update) (Update, bool, error) {
	if u.Fragmentation == FragmentSingle {
		if a.open {
			return Update{}, false, errors.New(TagFragmentSequence, "an unfragmented update inside a sequence", "code", u.Code)
		}
		return u, true, nil
	}
	if u.Fragmentation == FragmentFirst {
		if a.open {
			return Update{}, false, errors.New(TagFragmentSequence, "a FIRST fragment inside a sequence", "code", u.Code)
		}
		a.open, a.head, a.buf = true, u, a.buf[:0]
	} else {
		if !a.open {
			return Update{}, false, errors.New(TagFragmentSequence, "a fragment without a FIRST one", "code", u.Code, "fragmentation", u.Fragmentation)
		}
		if u.Code != a.head.Code || u.Compressed != a.head.Compressed || u.CompressionFlags != a.head.CompressionFlags {
			a.open = false
			return Update{}, false, errors.New(TagFragmentSequence, "a fragment of another update", "code", u.Code, "first", a.head.Code)
		}
	}
	if len(a.buf)+len(u.Data) > a.Max {
		a.open = false
		return Update{}, false, errors.New(TagReassemblyTooLarge, "fragments larger than the reassembly buffer", "length", len(a.buf)+len(u.Data), "max", a.Max)
	}
	a.buf = append(a.buf, u.Data...)
	if u.Fragmentation != FragmentLast {
		return Update{}, false, nil
	}
	a.open = false
	whole := a.head
	whole.Fragmentation = FragmentSingle
	whole.Data = a.buf
	return whole, true, nil
}
