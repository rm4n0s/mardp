// Package mcs reads and writes the T.125 Multipoint Communication Service
// PDUs RDP uses (MS-RDPBCGR 2.2.1.3 to 2.2.1.9, 2.2.2.3): the Connect-Initial
// and Connect-Response in BER, and the domain PDUs in aligned PER (Erect
// Domain, Attach User and its confirm, Channel Join and its confirm, Send Data
// Request and Indication, Disconnect Provider Ultimatum).
//
// The functions work on what follows the X.224 Data header. What they return
// points into the input instead of copying it.
package mcs

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors this package returns.
const (
	// TagTrailingBytes: bytes after the last field of a PDU or a structure.
	TagTrailingBytes = "MCSTrailingBytes"
	// TagUnexpectedPDU: a domain PDU of another kind than the one expected.
	TagUnexpectedPDU = "MCSUnexpectedPDU"
	// TagResultOutOfRange: a Result above rt-user-rejected (15) to write.
	TagResultOutOfRange = "MCSResultOutOfRange"
	// TagResultInvalid: a Connect-Response result outside the Result
	// enumeration.
	TagResultInvalid = "MCSResultInvalid"
	// TagReasonInvalid: a Disconnect Provider Ultimatum reason outside the
	// enumeration.
	TagReasonInvalid = "MCSReasonInvalid"
	// TagPriorityInvalid: a data priority above low (3) to write.
	TagPriorityInvalid = "MCSPriorityInvalid"
	// TagSegmentationInvalid: segmentation bits other than begin and end to
	// write.
	TagSegmentationInvalid = "MCSSegmentationInvalid"
)

// The BER tags of the connect PDUs (T.125 section 11).
var (
	ConnectInitialTag  = wire.BERTag{Class: wire.BERApplication, Constructed: true, Number: 101}
	ConnectResponseTag = wire.BERTag{Class: wire.BERApplication, Constructed: true, Number: 102}
)

// Result values (T.125 Result).
const (
	ResultSuccessful             uint8 = 0
	ResultDomainMerging          uint8 = 1
	ResultDomainNotHierarchical  uint8 = 2
	ResultNoSuchChannel          uint8 = 3
	ResultNoSuchDomain           uint8 = 4
	ResultNoSuchUser             uint8 = 5
	ResultNotAdmitted            uint8 = 6
	ResultOtherUserID            uint8 = 7
	ResultParametersUnacceptable uint8 = 8
	ResultTokenNotAvailable      uint8 = 9
	ResultTokenNotPossessed      uint8 = 10
	ResultTooManyChannels        uint8 = 11
	ResultTooManyTokens          uint8 = 12
	ResultTooManyUsers           uint8 = 13
	ResultUnspecifiedFailure     uint8 = 14
	ResultUserRejected           uint8 = 15
)

// DomainParameters are the limits of an MCS domain (T.125 section 7). Their
// INTEGERs are read unsigned (SPEC §5.1).
type DomainParameters struct {
	MaxChannelIDs   uint32
	MaxUserIDs      uint32
	MaxTokenIDs     uint32
	NumPriorities   uint32
	MinThroughput   uint32
	MaxHeight       uint32
	MaxMCSPDUSize   uint32
	ProtocolVersion uint32
}

func readDomainParameters(r *wire.Reader) (DomainParameters, error) {
	var p DomainParameters
	n, err := r.BERHeader(wire.BERSequenceTag)
	if err != nil {
		return p, err
	}
	s, err := r.Sub(n)
	if err != nil {
		return p, err
	}
	for _, f := range []*uint32{
		&p.MaxChannelIDs, &p.MaxUserIDs, &p.MaxTokenIDs, &p.NumPriorities,
		&p.MinThroughput, &p.MaxHeight, &p.MaxMCSPDUSize, &p.ProtocolVersion,
	} {
		if *f, err = s.BERUint(); err != nil {
			return p, err
		}
	}
	if s.Remaining() != 0 {
		return p, errors.New(TagTrailingBytes, "bytes after DomainParameters", "left", s.Remaining())
	}
	return p, nil
}

// writeDomainParameters appends p with a short-form length, as Microsoft's
// examples have it: eight INTEGERs take at most 56 bytes.
func writeDomainParameters(w *wire.Writer, p *DomainParameters) error {
	vs := [...]uint32{
		p.MaxChannelIDs, p.MaxUserIDs, p.MaxTokenIDs, p.NumPriorities,
		p.MinThroughput, p.MaxHeight, p.MaxMCSPDUSize, p.ProtocolVersion,
	}
	n := 0
	for _, v := range vs {
		n += 2 + berUintSize(v)
	}
	w.BERTag(wire.BERSequenceTag)
	if err := w.BERLength(n); err != nil {
		return err
	}
	for _, v := range vs {
		w.BERInteger(int64(v))
	}
	return nil
}

// berUintSize is how many content bytes strict BER takes for v: the fewest
// that hold it with a 0 sign bit.
func berUintSize(v uint32) int {
	n := 1
	for n < 5 && uint64(v)>>(8*n-1) != 0 {
		n++
	}
	return n
}

// ConnectInitial is an MCS Connect-Initial (MS-RDPBCGR 2.2.1.3). UserData is
// the GCC Conference Create Request.
type ConnectInitial struct {
	CallingDomainSelector []byte
	CalledDomainSelector  []byte
	UpwardFlag            bool
	Target                DomainParameters
	Minimum               DomainParameters
	Maximum               DomainParameters
	UserData              []byte
}

// ParseConnectInitial reads a Connect-Initial.
func ParseConnectInitial(b []byte) (ConnectInitial, error) {
	var ci ConnectInitial
	r := wire.NewReader(b)
	n, err := r.BERHeader(ConnectInitialTag)
	if err != nil {
		return ci, err
	}
	s, err := r.Sub(n)
	if err != nil {
		return ci, err
	}
	if r.Remaining() != 0 {
		return ci, errors.New(TagTrailingBytes, "bytes after the Connect-Initial", "left", r.Remaining())
	}
	if ci.CallingDomainSelector, err = s.BEROctetString(); err != nil {
		return ci, err
	}
	if ci.CalledDomainSelector, err = s.BEROctetString(); err != nil {
		return ci, err
	}
	if ci.UpwardFlag, err = s.BERBoolean(); err != nil {
		return ci, err
	}
	if ci.Target, err = readDomainParameters(&s); err != nil {
		return ci, err
	}
	if ci.Minimum, err = readDomainParameters(&s); err != nil {
		return ci, err
	}
	if ci.Maximum, err = readDomainParameters(&s); err != nil {
		return ci, err
	}
	if ci.UserData, err = s.BEROctetString(); err != nil {
		return ci, err
	}
	if s.Remaining() != 0 {
		return ci, errors.New(TagTrailingBytes, "bytes after the Connect-Initial's user data", "left", s.Remaining())
	}
	return ci, nil
}

// WriteConnectInitial appends ci. A client writes it; the server's tests
// write it to read it back.
func WriteConnectInitial(w *wire.Writer, ci *ConnectInitial) error {
	w.BERTag(ConnectInitialTag)
	off := w.BERReserveLength()
	start := w.Len()
	if err := w.BEROctetString(ci.CallingDomainSelector); err != nil {
		return err
	}
	if err := w.BEROctetString(ci.CalledDomainSelector); err != nil {
		return err
	}
	w.BERBoolean(ci.UpwardFlag)
	for _, p := range []*DomainParameters{&ci.Target, &ci.Minimum, &ci.Maximum} {
		if err := writeDomainParameters(w, p); err != nil {
			return err
		}
	}
	if err := w.BEROctetString(ci.UserData); err != nil {
		return err
	}
	return w.PatchBERLength(off, w.Len()-start)
}

// ConnectResponse is an MCS Connect-Response (MS-RDPBCGR 2.2.1.4). UserData
// is the GCC Conference Create Response.
type ConnectResponse struct {
	Result          uint8
	CalledConnectID uint32
	Parameters      DomainParameters
	UserData        []byte
}

// WriteConnectResponse appends cr.
func WriteConnectResponse(w *wire.Writer, cr *ConnectResponse) error {
	if cr.Result > ResultUserRejected {
		return errors.New(TagResultOutOfRange, "result above rt-user-rejected", "result", cr.Result)
	}
	w.BERTag(ConnectResponseTag)
	off := w.BERReserveLength()
	start := w.Len()
	w.BEREnumerated(int64(cr.Result))
	w.BERInteger(int64(cr.CalledConnectID))
	if err := writeDomainParameters(w, &cr.Parameters); err != nil {
		return err
	}
	if err := w.BEROctetString(cr.UserData); err != nil {
		return err
	}
	return w.PatchBERLength(off, w.Len()-start)
}

// ParseConnectResponse reads a Connect-Response. A client reads it; the
// server's tests read what it wrote.
func ParseConnectResponse(b []byte) (ConnectResponse, error) {
	var cr ConnectResponse
	r := wire.NewReader(b)
	n, err := r.BERHeader(ConnectResponseTag)
	if err != nil {
		return cr, err
	}
	s, err := r.Sub(n)
	if err != nil {
		return cr, err
	}
	if r.Remaining() != 0 {
		return cr, errors.New(TagTrailingBytes, "bytes after the Connect-Response", "left", r.Remaining())
	}
	result, err := s.BEREnumerated()
	if err != nil {
		return cr, err
	}
	if result < 0 || result > int64(ResultUserRejected) {
		return cr, errors.New(TagResultInvalid, "result outside the enumeration", "result", result)
	}
	cr.Result = uint8(result)
	if cr.CalledConnectID, err = s.BERUint(); err != nil {
		return cr, err
	}
	if cr.Parameters, err = readDomainParameters(&s); err != nil {
		return cr, err
	}
	if cr.UserData, err = s.BEROctetString(); err != nil {
		return cr, err
	}
	if s.Remaining() != 0 {
		return cr, errors.New(TagTrailingBytes, "bytes after the Connect-Response's user data", "left", s.Remaining())
	}
	return cr, nil
}
