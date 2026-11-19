// Package svc frames static virtual channel data (MS-RDPBCGR 2.2.6.1): the
// Channel PDU Header in front of each chunk, the splitting of a message into
// chunks (3.1.5.2.1) and the joining of chunks into the message
// (3.1.5.2.2.1). The MCS Send Data PDUs around the chunks are the caller's.
package svc

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors this package returns.
const (
	// TagCompressed: a compressed chunk, though the server offered no
	// compression of channel data.
	TagCompressed = "SvcCompressed"
	// TagChunkSequence: a chunk without CHANNEL_FLAG_FIRST while no message
	// is being joined, or one with it while one is.
	TagChunkSequence = "SvcChunkSequence"
	// TagLengthInvalid: chunks whose lengths do not add up to the
	// message's total length, or a later chunk that states another total.
	TagLengthInvalid = "SvcLengthInvalid"
	// TagMessageTooLarge: a message longer than the joiner accepts.
	TagMessageTooLarge = "SvcMessageTooLarge"
	// TagChunkSizeInvalid: Split asked for chunks of no bytes.
	TagChunkSizeInvalid = "SvcChunkSizeInvalid"
)

// HeaderLen is the length of the Channel PDU Header.
const HeaderLen = 8

// ChunkLength is CHANNEL_CHUNK_LENGTH, the largest chunk when neither side's
// Virtual Channel Capability Set states VCChunkSize (3.1.5.2.1).
const ChunkLength = 1600

// The flags of the Channel PDU Header (2.2.6.1.1).
const (
	FlagFirst            uint32 = 0x00000001
	FlagLast             uint32 = 0x00000002
	FlagShowProtocol     uint32 = 0x00000010
	FlagSuspend          uint32 = 0x00000020
	FlagResume           uint32 = 0x00000040
	FlagShadowPersistent uint32 = 0x00000080
	FlagCompressed       uint32 = 0x00200000
	FlagAtFront          uint32 = 0x00400000
	FlagFlushed          uint32 = 0x00800000
)

// Header is the Channel PDU Header: the total length of the message the
// chunk belongs to, and the flags.
type Header struct {
	Length uint32
	Flags  uint32
}

// ReadHeader reads a Channel PDU Header.
func ReadHeader(r *wire.Reader) (Header, error) {
	var h Header
	var err error
	if h.Length, err = r.U32LE(); err != nil {
		return h, err
	}
	if h.Flags, err = r.U32LE(); err != nil {
		return h, err
	}
	return h, nil
}

// WriteHeader appends h.
func WriteHeader(w *wire.Writer, h Header) {
	w.U32LE(h.Length)
	w.U32LE(h.Flags)
}

// Split calls chunk for each chunk of msg, in order: at most size bytes
// each, with the header that goes in front of it. A message of one chunk
// says FIRST and LAST; a longer one says FIRST on its first chunk, LAST on
// its last and neither in between, as Microsoft's example does (3.1.5.2.2.1).
// An empty message is one empty chunk.
func Split(msg []byte, size int, chunk func(h Header, data []byte) error) error {
	if size <= 0 {
		return errors.New(TagChunkSizeInvalid, "chunks of no bytes", "size", size)
	}
	h := Header{Length: uint32(len(msg)), Flags: FlagFirst}
	for {
		n := min(size, len(msg))
		if n == len(msg) {
			h.Flags |= FlagLast
		}
		if err := chunk(h, msg[:n]); err != nil {
			return err
		}
		msg = msg[n:]
		if len(msg) == 0 {
			return nil
		}
		h.Flags = 0
	}
}

// Joiner joins the chunks of one channel into messages. It keeps its buffer
// from message to message, and it grows the buffer as chunks arrive, not to
// the total length the first chunk states, so a client that states a long
// message and sends little costs little.
type Joiner struct {
	// Max is the longest message accepted.
	Max int

	buf   []byte
	open  bool
	total uint32
}

// Add takes the next chunk, data with the header h in front of it. It
// returns the whole message and true when data completes one; the message
// is valid until the next Add, or, for a message of one chunk, as long as
// data is. Chunks that say SUSPEND or RESUME carry no data and are skipped.
func (j *Joiner) Add(h Header, data []byte) ([]byte, bool, error) {
	if h.Flags&FlagCompressed != 0 {
		j.open = false
		return nil, false, errors.New(TagCompressed, "a compressed chunk", "flags", h.Flags)
	}
	if h.Flags&(FlagSuspend|FlagResume) != 0 {
		return nil, false, nil
	}
	first, last := h.Flags&FlagFirst != 0, h.Flags&FlagLast != 0
	switch {
	case first && j.open:
		j.open = false
		return nil, false, errors.New(TagChunkSequence, "a first chunk inside a message", "flags", h.Flags)
	case first:
		if int64(h.Length) > int64(j.Max) {
			return nil, false, errors.New(TagMessageTooLarge, "a message longer than accepted", "length", h.Length, "max", j.Max)
		}
		if last {
			if int(h.Length) != len(data) {
				return nil, false, errors.New(TagLengthInvalid, "a message of one chunk whose length is not the chunk's",
					"length", h.Length, "chunk", len(data))
			}
			return data, true, nil
		}
		j.open, j.total, j.buf = true, h.Length, j.buf[:0]
	case !j.open:
		return nil, false, errors.New(TagChunkSequence, "a chunk without a first one", "flags", h.Flags)
	case h.Length != j.total:
		j.open = false
		return nil, false, errors.New(TagLengthInvalid, "a chunk that states another total length", "length", h.Length, "total", j.total)
	}
	if int64(len(j.buf))+int64(len(data)) > int64(j.total) {
		j.open = false
		return nil, false, errors.New(TagLengthInvalid, "chunks longer than their message", "length", len(j.buf)+len(data), "total", j.total)
	}
	j.buf = append(j.buf, data...)
	if !last {
		return nil, false, nil
	}
	j.open = false
	if len(j.buf) != int(j.total) {
		return nil, false, errors.New(TagLengthInvalid, "chunks shorter than their message", "length", len(j.buf), "total", j.total)
	}
	return j.buf, true, nil
}
