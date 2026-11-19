// Package dynvc reads and writes the PDUs of dynamic virtual channels
// (MS-RDPEDYC 2.2), which travel on the drdynvc static channel: the
// capabilities of versions 1 to 3 and their response, the creation of a
// channel and its response, Data First and Data in every length form, and
// close. It splits a message into PDUs that each fit in one static channel
// chunk (3.1.5.1) and joins the PDUs of a message (3.1.5.2.3). Compressed
// data (version 3) is not supported. Sending the PDUs is the caller's.
package dynvc

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors this package returns.
const (
	// TagChannelIDSizeInvalid: a cbId of 3, which no ChannelId size has.
	TagChannelIDSizeInvalid = "DynvcChannelIDSizeInvalid"
	// TagLengthSizeInvalid: a Data First whose Len is 3.
	TagLengthSizeInvalid = "DynvcLengthSizeInvalid"
	// TagCommandUnknown: a PDU whose Cmd the side that reads it does not
	// expect from the other side (Soft-Sync, or an unknown one).
	TagCommandUnknown = "DynvcCommandUnknown"
	// TagCompressed: compressed data, which neither side offered.
	TagCompressed = "DynvcCompressed"
	// TagVersionInvalid: a capabilities PDU or response with a version
	// other than 1, 2 or 3.
	TagVersionInvalid = "DynvcVersionInvalid"
	// TagNameInvalid: a Create Request whose channel name has no NUL at
	// its end, or a name to write that has a NUL in it or is too long for
	// one PDU.
	TagNameInvalid = "DynvcNameInvalid"
	// TagSequence: a Data First while a message is being joined.
	TagSequence = "DynvcSequence"
	// TagLengthInvalid: data beyond the total length a Data First stated.
	TagLengthInvalid = "DynvcLengthInvalid"
	// TagMessageTooLarge: a message longer than the joiner accepts.
	TagMessageTooLarge = "DynvcMessageTooLarge"
)

// MaxPDU is the longest PDU: one static channel chunk of
// CHANNEL_CHUNK_LENGTH bytes (2.2.3.1, 2.2.3.2).
const MaxPDU = 1600

// The Cmd field of the header byte (2.2.1 to 2.2.5).
const (
	CmdCreate              uint8 = 0x01
	CmdDataFirst           uint8 = 0x02
	CmdData                uint8 = 0x03
	CmdClose               uint8 = 0x04
	CmdCapabilities        uint8 = 0x05
	CmdDataFirstCompressed uint8 = 0x06
	CmdDataCompressed      uint8 = 0x07
	CmdSoftSyncRequest     uint8 = 0x08
	CmdSoftSyncResponse    uint8 = 0x09
)

// The versions of the protocol (1.7).
const (
	Version1 uint16 = 1
	Version2 uint16 = 2
	Version3 uint16 = 3
)

// PDU is a PDU read from either side. Which fields are set depends on Cmd.
type PDU struct {
	// Cmd is the PDU's command.
	Cmd uint8
	// Sp is the two bits between cbId and Cmd: the priority class of a
	// Create Request (Pri), the size code of a Data First's Length (Len),
	// unused otherwise.
	Sp uint8
	// ChannelID is the channel of a Create, Data First, Data or Close.
	ChannelID uint32
	// Version is the version of capabilities or their response.
	Version uint16
	// PriorityCharges are those of capabilities of versions 2 and 3.
	PriorityCharges [4]uint16
	// Name is the channel name of a Create Request, without its NUL.
	Name string
	// Status is the CreationStatus of a Create Response: an HRESULT,
	// negative when the client refused the channel.
	Status int32
	// Length is the total length of the message a Data First starts.
	Length uint32
	// Data is the data of a Data First or Data; it points into the bytes
	// that were parsed.
	Data []byte
}

// ParseClient parses a PDU a client sends: a capabilities response, a
// Create Response, Data First, Data or Close.
func ParseClient(b []byte) (PDU, error) {
	r := wire.NewReader(b)
	p, cbID, err := readHeader(&r)
	if err != nil {
		return PDU{}, err
	}
	switch p.Cmd {
	case CmdCapabilities:
		if err := readVersion(&r, &p); err != nil {
			return PDU{}, err
		}
		return p, nil
	case CmdCreate:
		if p.ChannelID, err = readID(&r, cbID); err != nil {
			return PDU{}, err
		}
		status, err := r.U32LE()
		if err != nil {
			return PDU{}, err
		}
		p.Status = int32(status)
		return p, nil
	}
	return parseCommon(&r, p, cbID)
}

// ParseServer parses a PDU a server sends: capabilities of version 1, 2 or
// 3, a Create Request, Data First, Data or Close.
func ParseServer(b []byte) (PDU, error) {
	r := wire.NewReader(b)
	p, cbID, err := readHeader(&r)
	if err != nil {
		return PDU{}, err
	}
	switch p.Cmd {
	case CmdCapabilities:
		if err := readVersion(&r, &p); err != nil {
			return PDU{}, err
		}
		if p.Version >= Version2 {
			for i := range p.PriorityCharges {
				if p.PriorityCharges[i], err = r.U16LE(); err != nil {
					return PDU{}, err
				}
			}
		}
		return p, nil
	case CmdCreate:
		if p.ChannelID, err = readID(&r, cbID); err != nil {
			return PDU{}, err
		}
		name := r.Rest()
		n := len(name) - 1
		if n < 0 || name[n] != 0 {
			return PDU{}, errors.New(TagNameInvalid, "a Create Request whose channel name has no NUL at its end", "length", len(name))
		}
		for i := range n {
			if name[i] == 0 {
				n = i
				break
			}
		}
		p.Name = string(name[:n])
		return p, nil
	}
	return parseCommon(&r, p, cbID)
}

// readHeader reads the header byte; it returns cbId apart.
func readHeader(r *wire.Reader) (PDU, uint8, error) {
	h, err := r.U8()
	if err != nil {
		return PDU{}, 0, err
	}
	return PDU{Cmd: h >> 4, Sp: (h >> 2) & 3}, h & 3, nil
}

// readVersion reads the Pad and Version of capabilities or their response.
func readVersion(r *wire.Reader, p *PDU) error {
	if err := r.Skip(1); err != nil {
		return err
	}
	v, err := r.U16LE()
	if err != nil {
		return err
	}
	if v < Version1 || v > Version3 {
		return errors.New(TagVersionInvalid, "capabilities of an unknown version", "version", v)
	}
	p.Version = v
	return nil
}

// parseCommon parses the PDUs both sides send: Data First, Data and Close.
func parseCommon(r *wire.Reader, p PDU, cbID uint8) (PDU, error) {
	var err error
	switch p.Cmd {
	case CmdDataFirst:
		if p.ChannelID, err = readID(r, cbID); err != nil {
			return PDU{}, err
		}
		switch p.Sp {
		case 0:
			var v uint8
			v, err = r.U8()
			p.Length = uint32(v)
		case 1:
			var v uint16
			v, err = r.U16LE()
			p.Length = uint32(v)
		case 2:
			p.Length, err = r.U32LE()
		default:
			return PDU{}, errors.New(TagLengthSizeInvalid, "a Data First whose Len is 3")
		}
		if err != nil {
			return PDU{}, err
		}
		p.Data = r.Rest()
		return p, nil
	case CmdData:
		if p.ChannelID, err = readID(r, cbID); err != nil {
			return PDU{}, err
		}
		p.Data = r.Rest()
		return p, nil
	case CmdClose:
		if p.ChannelID, err = readID(r, cbID); err != nil {
			return PDU{}, err
		}
		return p, nil
	case CmdDataFirstCompressed, CmdDataCompressed:
		return PDU{}, errors.New(TagCompressed, "compressed data, which was not offered", "cmd", p.Cmd)
	}
	return PDU{}, errors.New(TagCommandUnknown, "a PDU this side does not expect", "cmd", p.Cmd)
}

// readID reads a ChannelId of the size cbID says.
func readID(r *wire.Reader, cbID uint8) (uint32, error) {
	switch cbID {
	case 0:
		v, err := r.U8()
		return uint32(v), err
	case 1:
		v, err := r.U16LE()
		return uint32(v), err
	case 2:
		return r.U32LE()
	}
	return 0, errors.New(TagChannelIDSizeInvalid, "a cbId of 3")
}

// sizeCode is the cbId or Len code of the smallest field that holds v.
func sizeCode(v uint32) uint8 {
	switch {
	case v <= 0xff:
		return 0
	case v <= 0xffff:
		return 1
	}
	return 2
}

// codeLen is the length in bytes of a field of size code c.
func codeLen(c uint8) int { return 1 << c }

// writeSized writes v in a field of size code c.
func writeSized(w *wire.Writer, c uint8, v uint32) {
	switch c {
	case 0:
		w.U8(uint8(v))
	case 1:
		w.U16LE(uint16(v))
	default:
		w.U32LE(v)
	}
}

// header is the header byte.
func header(cmd, sp, cbID uint8) uint8 { return cmd<<4 | sp<<2 | cbID }

// AppendCaps appends capabilities of version (DYNVC_CAPS_VERSION1 to 3,
// 2.2.1.1); charges are left out of version 1.
func AppendCaps(w *wire.Writer, version uint16, charges [4]uint16) error {
	if version < Version1 || version > Version3 {
		return errors.New(TagVersionInvalid, "capabilities of an unknown version", "version", version)
	}
	w.U8(header(CmdCapabilities, 0, 0))
	w.U8(0)
	w.U16LE(version)
	if version >= Version2 {
		for _, c := range charges {
			w.U16LE(c)
		}
	}
	return nil
}

// AppendCapsResponse appends a capabilities response for version
// (DYNVC_CAPS_RSP, 2.2.1.2).
func AppendCapsResponse(w *wire.Writer, version uint16) error {
	if version < Version1 || version > Version3 {
		return errors.New(TagVersionInvalid, "a capabilities response of an unknown version", "version", version)
	}
	w.U8(header(CmdCapabilities, 0, 0))
	w.U8(0)
	w.U16LE(version)
	return nil
}

// AppendCreateRequest appends a Create Request for the channel id named
// name with the priority class pri (DYNVC_CREATE_REQ, 2.2.2.1).
func AppendCreateRequest(w *wire.Writer, id uint32, pri uint8, name string) error {
	c := sizeCode(id)
	if 1+codeLen(c)+len(name)+1 > MaxPDU {
		return errors.New(TagNameInvalid, "a channel name too long for one PDU", "length", len(name))
	}
	for i := range len(name) {
		if name[i] == 0 {
			return errors.New(TagNameInvalid, "a channel name with a NUL in it", "name", name)
		}
	}
	w.U8(header(CmdCreate, pri&3, c))
	writeSized(w, c, id)
	w.Write([]byte(name))
	w.U8(0)
	return nil
}

// AppendCreateResponse appends a Create Response for the channel id with
// the HRESULT status (DYNVC_CREATE_RSP, 2.2.2.2).
func AppendCreateResponse(w *wire.Writer, id uint32, status int32) {
	c := sizeCode(id)
	w.U8(header(CmdCreate, 0, c))
	writeSized(w, c, id)
	w.U32LE(uint32(status))
}

// AppendClose appends a Close for the channel id (DYNVC_CLOSE, 2.2.4).
func AppendClose(w *wire.Writer, id uint32) {
	c := sizeCode(id)
	w.U8(header(CmdClose, 0, c))
	writeSized(w, c, id)
}

// AppendDataFirst appends a Data First on the channel id for a message of
// total bytes that starts with data (DYNVC_DATA_FIRST, 2.2.3.1), with the
// smallest field for the length.
func AppendDataFirst(w *wire.Writer, id, total uint32, data []byte) {
	c, l := sizeCode(id), sizeCode(total)
	w.U8(header(CmdDataFirst, l, c))
	writeSized(w, c, id)
	writeSized(w, l, total)
	w.Write(data)
}

// AppendData appends a Data on the channel id (DYNVC_DATA, 2.2.3.2).
func AppendData(w *wire.Writer, id uint32, data []byte) {
	c := sizeCode(id)
	w.U8(header(CmdData, 0, c))
	writeSized(w, c, id)
	w.Write(data)
}

// Split cuts msg for the channel id into PDUs of at most MaxPDU bytes
// (3.1.5.1): one Data when it fits, otherwise a Data First and Data PDUs.
// Each PDU is built in w, which is reset first, and handed to pdu, which
// may not keep it.
func Split(w *wire.Writer, id uint32, msg []byte, pdu func(b []byte) error) error {
	dataHeader := 1 + codeLen(sizeCode(id))
	if dataHeader+len(msg) <= MaxPDU {
		w.Reset()
		AppendData(w, id, msg)
		return pdu(w.Bytes())
	}
	first := MaxPDU - dataHeader - codeLen(sizeCode(uint32(len(msg))))
	w.Reset()
	AppendDataFirst(w, id, uint32(len(msg)), msg[:first])
	if err := pdu(w.Bytes()); err != nil {
		return err
	}
	for rest := msg[first:]; len(rest) > 0; {
		n := min(len(rest), MaxPDU-dataHeader)
		w.Reset()
		AppendData(w, id, rest[:n])
		if err := pdu(w.Bytes()); err != nil {
			return err
		}
		rest = rest[n:]
	}
	return nil
}

// Joiner joins the Data First and Data PDUs of one channel into messages
// (3.1.5.2.3). Its buffer grows with the data that arrives, not to the
// length a Data First states.
type Joiner struct {
	// Max is the longest message it accepts.
	Max   int
	buf   []byte
	total uint32
	open  bool
}

// Add adds the Data First or Data p. It returns the message and true when
// p completes one; the message is valid until the next Add. A Data that
// follows no Data First is a message of its own.
func (j *Joiner) Add(p PDU) ([]byte, bool, error) {
	switch p.Cmd {
	case CmdDataFirst:
		if j.open {
			j.open = false
			return nil, false, errors.New(TagSequence, "a Data First while a message is being joined",
				"channel", p.ChannelID, "joined", len(j.buf), "total", j.total)
		}
		if uint64(p.Length) > uint64(j.Max) {
			return nil, false, errors.New(TagMessageTooLarge, "a message longer than the joiner accepts",
				"channel", p.ChannelID, "length", p.Length, "max", j.Max)
		}
		if uint64(len(p.Data)) > uint64(p.Length) {
			return nil, false, errors.New(TagLengthInvalid, "a Data First with more data than its total length",
				"channel", p.ChannelID, "data", len(p.Data), "total", p.Length)
		}
		if len(p.Data) == int(p.Length) {
			return p.Data, true, nil
		}
		j.buf = append(j.buf[:0], p.Data...)
		j.total, j.open = p.Length, true
		return nil, false, nil
	case CmdData:
		if !j.open {
			if len(p.Data) > j.Max {
				return nil, false, errors.New(TagMessageTooLarge, "a message longer than the joiner accepts",
					"channel", p.ChannelID, "length", len(p.Data), "max", j.Max)
			}
			return p.Data, true, nil
		}
		if uint64(len(j.buf))+uint64(len(p.Data)) > uint64(j.total) {
			j.open = false
			return nil, false, errors.New(TagLengthInvalid, "data beyond the total length of the message",
				"channel", p.ChannelID, "joined", len(j.buf), "data", len(p.Data), "total", j.total)
		}
		j.buf = append(j.buf, p.Data...)
		if len(j.buf) < int(j.total) {
			return nil, false, nil
		}
		j.open = false
		return j.buf, true, nil
	}
	return nil, false, errors.New(TagCommandUnknown, "a PDU that carries no data", "cmd", p.Cmd)
}
