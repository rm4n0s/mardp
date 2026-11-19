package caps

import (
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Control PDU actions (MS-RDPBCGR 2.2.1.15.1).
const (
	ActionRequestControl uint16 = 0x0001
	ActionGrantedControl uint16 = 0x0002
	ActionDetach         uint16 = 0x0003
	ActionCooperate      uint16 = 0x0004
)

// Values of the finalization PDUs.
const (
	syncMessageTypeSync uint16 = 0x0001 // SYNCMSGTYPE_SYNC, 2.2.1.14.1
	fontListFirstLast   uint16 = 0x0003 // FONTLIST_FIRST | FONTLIST_LAST, 2.2.1.18.1
	fontListEntrySize   uint16 = 0x0032 // 2.2.1.18.1
	fontMapEntrySize    uint16 = 0x0004 // 2.2.1.22.1
)

// Synchronize is the Synchronize PDU (2.2.1.14.1).
type Synchronize struct {
	MessageType uint16
	// TargetUser is the MCS channel ID of the user it is meant for: the
	// server's (0x03EA) from the client, the client's user channel from the
	// server.
	TargetUser uint16
}

// WriteSynchronize appends a Synchronize PDU from source to targetUser.
func WriteSynchronize(w *wire.Writer, shareID uint32, source, targetUser uint16) error {
	off := BeginData(w, shareID, source, Type2Synchronize)
	w.U16LE(syncMessageTypeSync)
	w.U16LE(targetUser)
	return EndData(w, off)
}

// ParseSynchronize reads a Synchronize PDU from the body of its Data PDU.
// messageType is returned and not checked.
func ParseSynchronize(body []byte) (Synchronize, error) {
	r := wire.NewReader(body)
	var s Synchronize
	var err error
	if s.MessageType, err = r.U16LE(); err != nil {
		return s, err
	}
	s.TargetUser, err = r.U16LE()
	return s, err
}

// Control is the Control PDU (2.2.1.15.1).
type Control struct {
	Action    uint16
	GrantID   uint16
	ControlID uint32
}

// WriteControl appends a Control PDU from source.
func WriteControl(w *wire.Writer, shareID uint32, source uint16, c Control) error {
	off := BeginData(w, shareID, source, Type2Control)
	w.U16LE(c.Action)
	w.U16LE(c.GrantID)
	w.U32LE(c.ControlID)
	return EndData(w, off)
}

// ParseControl reads a Control PDU from the body of its Data PDU.
func ParseControl(body []byte) (Control, error) {
	r := wire.NewReader(body)
	var c Control
	var err error
	if c.Action, err = r.U16LE(); err != nil {
		return c, err
	}
	if c.GrantID, err = r.U16LE(); err != nil {
		return c, err
	}
	c.ControlID, err = r.U32LE()
	return c, err
}

// Fonts is the Font List PDU (2.2.1.18.1) or the Font Map PDU (2.2.1.22.1),
// which have the same fields; neither lists fonts any more.
type Fonts struct {
	Number, Total, Flags, EntrySize uint16
}

// WriteFontList appends a Font List PDU from source with the values
// 2.2.1.18.1 asks for: no fonts, first and last, entries of 50 bytes.
func WriteFontList(w *wire.Writer, shareID uint32, source uint16) error {
	return writeFonts(w, shareID, source, Type2FontList, fontListEntrySize)
}

// WriteFontMap appends a Font Map PDU from source with the values 2.2.1.22.1
// asks for: no fonts, first and last, entries of 4 bytes.
func WriteFontMap(w *wire.Writer, shareID uint32, source uint16) error {
	return writeFonts(w, shareID, source, Type2FontMap, fontMapEntrySize)
}

func writeFonts(w *wire.Writer, shareID uint32, source uint16, type2 uint8, entrySize uint16) error {
	off := BeginData(w, shareID, source, type2)
	w.U16LE(0) // numberFonts / numberEntries
	w.U16LE(0) // totalNumFonts / totalNumEntries
	w.U16LE(fontListFirstLast)
	w.U16LE(entrySize)
	return EndData(w, off)
}

// ParseFonts reads a Font List or Font Map PDU from the body of its Data
// PDU.
func ParseFonts(body []byte) (Fonts, error) {
	r := wire.NewReader(body)
	var f Fonts
	var err error
	if f.Number, err = r.U16LE(); err != nil {
		return f, err
	}
	if f.Total, err = r.U16LE(); err != nil {
		return f, err
	}
	if f.Flags, err = r.U16LE(); err != nil {
		return f, err
	}
	f.EntrySize, err = r.U16LE()
	return f, err
}
