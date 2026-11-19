// Package gcc reads and writes the T.124 Generic Conference Control PDUs RDP
// carries in the MCS Connect-Initial and Connect-Response (the Conference
// Create Request and Response, MS-RDPBCGR 2.2.1.3 and 2.2.1.4), and the
// settings blocks inside them: the client's data blocks (2.2.1.3.2 to
// 2.2.1.3.8) and the server's (2.2.1.4.2 to 2.2.1.4.4).
//
// The functions work on the MCS user data. What they return points into the
// input instead of copying it.
package gcc

import (
	"bytes"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors this package returns.
const (
	// TagObjectInvalid: ConnectData's key is not the T.124 object identifier.
	TagObjectInvalid = "GCCObjectInvalid"
	// TagUnexpectedPDU: a ConnectGCCPDU of another kind than the one expected.
	TagUnexpectedPDU = "GCCUnexpectedPDU"
	// TagOptionalUnsupported: a Conference Create Request with optional
	// fields RDP does not use (passwords, privileges, a description, a text
	// conference name).
	TagOptionalUnsupported = "GCCOptionalUnsupported"
	// TagUserDataMissing: a Conference Create Request or Response without
	// user data, or with no user data set.
	TagUserDataMissing = "GCCUserDataMissing"
	// TagUserDataKeyInvalid: the user data set's key is not the H.221
	// non-standard key RDP uses ("Duca" from the client, "McDn" from the
	// server).
	TagUserDataKeyInvalid = "GCCUserDataKeyInvalid"
	// TagResultOutOfRange: a Conference Create Response result above 7 to
	// write.
	TagResultOutOfRange = "GCCResultOutOfRange"
)

// t124Object is ConnectData's key: the object identifier
// {itu-t(0) recommendation(0) t(20) t124(124) version(0) 1}, as its encoded
// bytes.
var t124Object = [...]byte{0x00, 0x14, 0x7c, 0x00, 0x01}

// The H.221 non-standard keys of the user data sets.
var (
	clientKey = [...]byte{'D', 'u', 'c', 'a'}
	serverKey = [...]byte{'M', 'c', 'D', 'n'}
)

// ConferenceCreateResponse results (T.124).
const (
	ResultSuccess                      uint8 = 0
	ResultUserRejected                 uint8 = 1
	ResultResourcesNotAvailable        uint8 = 2
	ResultRejectedForSymmetryBreaking  uint8 = 3
	ResultLockedConferenceNotSupported uint8 = 4
)

// readConnectData reads ConnectData's key and the length of its connectPDU.
// The length is not checked against what follows: Microsoft's own Conference
// Create Response example states 42 for 279 bytes, and clients ignore it
// (MS-RDPBCGR 4.1.4).
func readConnectData(r *wire.Reader) error {
	choice, err := r.U8()
	if err != nil {
		return err
	}
	n, err := r.PERLength()
	if err != nil {
		return err
	}
	if choice != 0 || n != len(t124Object) {
		return errors.New(TagObjectInvalid, "ConnectData's key is not an object identifier of 5 bytes", "choice", choice, "length", n)
	}
	obj, err := r.Bytes(n)
	if err != nil {
		return err
	}
	if !bytes.Equal(obj, t124Object[:]) {
		return errors.New(TagObjectInvalid, "ConnectData's key is not T.124's object identifier", "object", obj)
	}
	if _, err := r.PERLength(); err != nil {
		return err
	}
	return nil
}

// readUserData reads the SET OF UserData that ends both PDUs and returns the
// value of its first set, which has to carry the key want.
func readUserData(r *wire.Reader, want []byte) ([]byte, error) {
	sets, err := r.PERLength()
	if err != nil {
		return nil, err
	}
	if sets == 0 {
		return nil, errors.New(TagUserDataMissing, "no user data set")
	}
	head, err := r.U8()
	if err != nil {
		return nil, err
	}
	// Bit 7: the value is present; bit 6: the key is an H.221 non-standard
	// identifier (choice 1 of 2).
	if head&0xc0 != 0xc0 {
		return nil, errors.New(TagUserDataKeyInvalid, "user data set without a value or an H.221 key", "head", head)
	}
	n, err := r.U8() // SIZE(4..255): the length minus 4
	if err != nil {
		return nil, err
	}
	key, err := r.Bytes(int(n) + 4)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(key, want) {
		return nil, errors.New(TagUserDataKeyInvalid, "user data set with another key", "key", key)
	}
	size, err := r.PERLength()
	if err != nil {
		return nil, err
	}
	return r.Bytes(size)
}

// writeUserData appends one user data set with key and value.
func writeUserData(w *wire.Writer, key, value []byte) error {
	w.U8(1)    // one set
	w.U8(0xc0) // value present, H.221 non-standard key
	w.U8(uint8(len(key) - 4))
	w.Write(key)
	if err := w.PERLength(len(value)); err != nil {
		return err
	}
	w.Write(value)
	return nil
}

// ParseConferenceCreateRequest reads the client's ConnectData with its
// Conference Create Request and returns the user data: the client's data
// blocks (see ParseClientData).
func ParseConferenceCreateRequest(b []byte) ([]byte, error) {
	r := wire.NewReader(b)
	if err := readConnectData(&r); err != nil {
		return nil, err
	}
	b0, err := r.U8()
	if err != nil {
		return nil, err
	}
	b1, err := r.U8()
	if err != nil {
		return nil, err
	}
	// b0: ConnectGCCPDU's extension bit and 3-bit choice (0 is
	// conferenceCreateRequest), the request's extension bit (extensions come
	// after the user data and are not read), convenerPassword, password,
	// conductorPrivileges.
	if b0&0xf0 != 0 {
		return nil, errors.New(TagUnexpectedPDU, "not a Conference Create Request", "byte", b0)
	}
	// b1: conductedPrivileges, nonConductedPrivileges, conferenceDescription,
	// callerIdentifier, userData, ConferenceName's extension bit, text, pad.
	if b0&0x07 != 0 || b1&0xf6 != 0 {
		return nil, errors.New(TagOptionalUnsupported, "Conference Create Request with optional fields RDP does not use", "bytes", uint16(b0)<<8|uint16(b1))
	}
	if b1&0x08 == 0 {
		return nil, errors.New(TagUserDataMissing, "Conference Create Request without user data")
	}
	// ConferenceName's numeric string: SIZE(1..255) as the length minus 1,
	// then 4 bits a digit. The 4 bits of lockedConference, listedConference,
	// conducibleConference and TerminationMethod's extension bit follow, and
	// the terminationMethod bit and padding fill the next octet: whether the
	// digits are odd or even in number, that is (n+1)/2 + 1 octets.
	n, err := r.U8()
	if err != nil {
		return nil, err
	}
	if err := r.Skip((int(n)+2)/2 + 1); err != nil {
		return nil, err
	}
	return readUserData(&r, clientKey[:])
}

// WriteConferenceCreateRequest appends ConnectData with a Conference Create
// Request carrying userData, as Microsoft's example has it (MS-RDPBCGR 4.1.3):
// conference name "1", no optional fields. A client writes it; the server's
// tests write it to read it back.
func WriteConferenceCreateRequest(w *wire.Writer, userData []byte) error {
	w.U8(0) // key: object identifier
	w.U8(uint8(len(t124Object)))
	w.Write(t124Object[:])
	off := w.Reserve(2)
	start := w.Len()
	w.Write([]byte{
		0x00,       // conferenceCreateRequest, no optional fields so far
		0x08,       // userData present
		0x00, 0x10, // conference name "1", flags clear
		0x00, // terminationMethod automatic
	})
	if err := writeUserData(w, clientKey[:], userData); err != nil {
		return err
	}
	return patchPERLength2(w, off, w.Len()-start)
}

// patchPERLength2 fills a two-byte length determinant reserved at off. The
// connectPDU's length always takes the two-byte form there, as in Microsoft's
// example, so the reservation is known before the contents.
func patchPERLength2(w *wire.Writer, off, n int) error {
	if n > wire.PERMaxLength {
		return errors.New(wire.TagPERLengthOutOfRange, "connectPDU too long for one PER length", "length", n)
	}
	return w.PatchU16BE(off, 0x8000|uint16(n))
}

// ConferenceCreateResponse is the server's Conference Create Response.
// UserData holds the server's data blocks (see ParseServerData).
type ConferenceCreateResponse struct {
	NodeID   uint16 // a UserID, at least 1001
	Tag      uint32
	Result   uint8
	UserData []byte
}

// WriteConferenceCreateResponse appends ConnectData with ccr. Unlike
// Microsoft's example, the connectPDU's length is the real one.
func WriteConferenceCreateResponse(w *wire.Writer, ccr *ConferenceCreateResponse) error {
	if ccr.Result > 7 {
		return errors.New(TagResultOutOfRange, "result does not fit 3 bits", "result", ccr.Result)
	}
	w.U8(0)
	w.U8(uint8(len(t124Object)))
	w.Write(t124Object[:])
	off := w.Reserve(2)
	start := w.Len()
	w.U8(0x14) // conferenceCreateResponse (1), userData present
	if err := w.PERInteger16(ccr.NodeID, 1001); err != nil {
		return err
	}
	w.PERUint(ccr.Tag)
	w.U8(ccr.Result << 4)
	if err := writeUserData(w, serverKey[:], ccr.UserData); err != nil {
		return err
	}
	return patchPERLength2(w, off, w.Len()-start)
}

// ParseConferenceCreateResponse reads the server's ConnectData with its
// Conference Create Response. A client reads it; the server's tests read
// what it wrote.
func ParseConferenceCreateResponse(b []byte) (ConferenceCreateResponse, error) {
	var ccr ConferenceCreateResponse
	r := wire.NewReader(b)
	if err := readConnectData(&r); err != nil {
		return ccr, err
	}
	b0, err := r.U8()
	if err != nil {
		return ccr, err
	}
	// ConnectGCCPDU's extension bit and choice (1), the response's extension
	// bit, userData present, padding.
	if b0&0xf0 != 0x10 {
		return ccr, errors.New(TagUnexpectedPDU, "not a Conference Create Response", "byte", b0)
	}
	if b0&0x04 == 0 {
		return ccr, errors.New(TagUserDataMissing, "Conference Create Response without user data")
	}
	if ccr.NodeID, err = r.PERInteger16(1001); err != nil {
		return ccr, err
	}
	if ccr.Tag, err = r.PERUint(); err != nil {
		return ccr, err
	}
	res, err := r.U8() // extension bit, 3-bit result, padding
	if err != nil {
		return ccr, err
	}
	ccr.Result = res >> 4 & 7
	if ccr.UserData, err = readUserData(&r, serverKey[:]); err != nil {
		return ccr, err
	}
	return ccr, nil
}
