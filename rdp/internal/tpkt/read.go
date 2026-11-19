package tpkt

import (
	"io"

	"github.com/rm4n0s/errors"
)

// Tags of ReadFrame's errors.
const (
	// TagReadFailed: reading from the connection failed; the cause (io.EOF,
	// a deadline, a closed connection) is kept.
	TagReadFailed = "FrameReadFailed"
	// TagBufferTooSmall: a buffer that cannot hold the largest frame.
	TagBufferTooSmall = "FrameBufferTooSmall"
)

// ReadFrame reads one whole frame, TPKT or fast-path, from r into buf and
// returns its kind and its bytes, header included, as a slice of buf. buf
// holds at least MaxLength bytes, so any frame fits.
//
// ReadFrame takes no context: like io.ReadFull, it blocks in r. The caller
// ends a read by setting a deadline on the connection or closing it.
func ReadFrame(r io.Reader, buf []byte) (Kind, []byte, error) {
	if len(buf) < MaxLength {
		return Unknown, nil, errors.New(TagBufferTooSmall, "frame buffer smaller than the largest frame", "size", len(buf), "need", MaxLength)
	}
	if _, err := io.ReadFull(r, buf[:2]); err != nil {
		return Unknown, nil, errors.NewErr(TagReadFailed, err).SetMetadata("part", "header")
	}
	need := max(HeaderSize(buf[0], buf[1]), 2)
	if need > 2 {
		if _, err := io.ReadFull(r, buf[2:need]); err != nil {
			return Unknown, nil, errors.NewErr(TagReadFailed, err).SetMetadata("part", "header")
		}
	}
	k, n, err := FrameLength(buf[:need])
	if err != nil {
		return k, nil, err
	}
	if _, err := io.ReadFull(r, buf[need:n]); err != nil {
		return k, nil, errors.NewErr(TagReadFailed, err).SetMetadata("part", "body", "length", n)
	}
	return k, buf[:n], nil
}
