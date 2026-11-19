package caps

import (
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// DemandActive is the Demand Active PDU (MS-RDPBCGR 2.2.1.13.1).
type DemandActive struct {
	ShareID uint32
	// Source is pduSource of the Share Control Header.
	Source uint16
	// SourceDescriptor is the sender's name; Microsoft's servers send
	// "RDP\x00".
	SourceDescriptor []byte
	Caps             Caps
	SessionID        uint32
}

// WriteDemandActive appends a Demand Active PDU.
func WriteDemandActive(w *wire.Writer, d *DemandActive) error {
	off := BeginControl(w, TypeDemandActive, d.Source)
	w.U32LE(d.ShareID)
	w.U16LE(uint16(len(d.SourceDescriptor)))
	lc := w.Reserve(2) // lengthCombinedCapabilities
	w.Write(d.SourceDescriptor)
	start := w.Len()
	if err := writeSets(w, &d.Caps); err != nil {
		return err
	}
	if err := w.PatchU16LE(lc, uint16(w.Len()-start)); err != nil {
		return err
	}
	w.U32LE(d.SessionID)
	return EndControl(w, off)
}

// ParseDemandActive reads the Demand Active PDU p.
func ParseDemandActive(p PDU) (DemandActive, error) {
	d := DemandActive{Source: p.Source}
	r := wire.NewReader(p.Body)
	var err error
	if d.ShareID, err = r.U32LE(); err != nil {
		return d, err
	}
	if d.SourceDescriptor, d.Caps, err = readActive(&r); err != nil {
		return d, err
	}
	// sessionId: the client ignores it, and some servers leave it out.
	if r.Remaining() >= 4 {
		if d.SessionID, err = r.U32LE(); err != nil {
			return d, err
		}
	}
	return d, nil
}

// ConfirmActive is the Confirm Active PDU (MS-RDPBCGR 2.2.1.13.2).
type ConfirmActive struct {
	ShareID uint32
	// Source is pduSource of the Share Control Header: the client's user
	// channel.
	Source uint16
	// Originator is originatorID, the server's channel ID (0x03EA).
	Originator       uint16
	SourceDescriptor []byte
	Caps             Caps
}

// WriteConfirmActive appends a Confirm Active PDU.
func WriteConfirmActive(w *wire.Writer, c *ConfirmActive) error {
	off := BeginControl(w, TypeConfirmActive, c.Source)
	w.U32LE(c.ShareID)
	w.U16LE(c.Originator)
	w.U16LE(uint16(len(c.SourceDescriptor)))
	lc := w.Reserve(2)
	w.Write(c.SourceDescriptor)
	start := w.Len()
	if err := writeSets(w, &c.Caps); err != nil {
		return err
	}
	if err := w.PatchU16LE(lc, uint16(w.Len()-start)); err != nil {
		return err
	}
	return EndControl(w, off)
}

// ParseConfirmActive reads the Confirm Active PDU p. Capability sets the
// package does not read are skipped.
func ParseConfirmActive(p PDU) (ConfirmActive, error) {
	c := ConfirmActive{Source: p.Source}
	r := wire.NewReader(p.Body)
	var err error
	if c.ShareID, err = r.U32LE(); err != nil {
		return c, err
	}
	if c.Originator, err = r.U16LE(); err != nil {
		return c, err
	}
	c.SourceDescriptor, c.Caps, err = readActive(&r)
	return c, err
}

// readActive reads lengthSourceDescriptor, lengthCombinedCapabilities, the
// source descriptor and the capability sets, which take
// lengthCombinedCapabilities bytes.
func readActive(r *wire.Reader) ([]byte, Caps, error) {
	ls, err := r.U16LE()
	if err != nil {
		return nil, Caps{}, err
	}
	lc, err := r.U16LE()
	if err != nil {
		return nil, Caps{}, err
	}
	sd, err := r.Bytes(int(ls))
	if err != nil {
		return nil, Caps{}, err
	}
	cr, err := r.Sub(int(lc))
	if err != nil {
		return sd, Caps{}, err
	}
	c, err := readSets(&cr)
	return sd, c, err
}
