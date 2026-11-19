package sec

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors of the licensing PDUs.
const (
	// TagLicenseUnexpected: a licensing message of another type than the
	// error alert.
	TagLicenseUnexpected = "SecLicenseUnexpected"
	// TagLicenseSizeInvalid: a licensing preamble whose wMsgSize is not the
	// size of the message, or an error blob longer than the message.
	TagLicenseSizeInvalid = "SecLicenseSizeInvalid"
)

// Licensing values (MS-RDPBCGR 2.2.1.12.1).
const (
	LicenseErrorAlert        uint8  = 0xff // bMsgType ERROR_ALERT
	LicensePreambleVersion3  uint8  = 0x03 // PREAMBLE_VERSION_3_0
	LicenseExtendedErrorMsg  uint8  = 0x80 // EXTENDED_ERROR_MSG_SUPPORTED
	LicenseStatusValidClient uint32 = 0x00000007
	LicenseNoTransition      uint32 = 0x00000002 // ST_NO_TRANSITION
	LicenseErrorBlob         uint16 = 0x0004     // BB_ERROR_BLOB
)

// licensePreambleLen is the size of LICENSE_PREAMBLE.
const licensePreambleLen = 4

// LicenseError is a licensing preamble with a LICENSE_ERROR_MESSAGE
// (MS-RDPBCGR 2.2.1.12.1.3). Blob points into the input.
type LicenseError struct {
	Flags           uint8
	ErrorCode       uint32
	StateTransition uint32
	BlobType        uint16
	Blob            []byte
}

// WriteLicenseValidClient appends the data of the Server License Error PDU
// that ends licensing with the client valid (MS-RDPBCGR 2.2.1.12, the
// decrypted packet of 4.1.11): a security header with SEC_LICENSE_PKT, then
// an error alert with STATUS_VALID_CLIENT, ST_NO_TRANSITION and an empty
// BB_ERROR_BLOB.
func WriteLicenseValidClient(w *wire.Writer) {
	writeHeader(w, FlagLicensePkt)
	w.U8(LicenseErrorAlert)
	w.U8(LicensePreambleVersion3)
	w.U16LE(16)
	w.U32LE(LicenseStatusValidClient)
	w.U32LE(LicenseNoTransition)
	w.U16LE(LicenseErrorBlob)
	w.U16LE(0)
}

// ParseLicenseError reads the data of a licensing PDU that has to be an
// error alert: the security header, which has to say SEC_LICENSE_PKT, the
// preamble and the error message.
func ParseLicenseError(b []byte) (LicenseError, error) {
	var l LicenseError
	r := wire.NewReader(b)
	if err := readHeader(&r, FlagLicensePkt); err != nil {
		return l, err
	}
	typ, err := r.U8()
	if err != nil {
		return l, err
	}
	if l.Flags, err = r.U8(); err != nil {
		return l, err
	}
	size, err := r.U16LE()
	if err != nil {
		return l, err
	}
	if typ != LicenseErrorAlert {
		return l, errors.New(TagLicenseUnexpected, "a licensing message other than an error alert", "type", typ)
	}
	if int(size) != r.Remaining()+licensePreambleLen {
		return l, errors.New(TagLicenseSizeInvalid, "wMsgSize is not the size of the licensing message", "size", size, "actual", r.Remaining()+licensePreambleLen)
	}
	if l.ErrorCode, err = r.U32LE(); err != nil {
		return l, err
	}
	if l.StateTransition, err = r.U32LE(); err != nil {
		return l, err
	}
	if l.BlobType, err = r.U16LE(); err != nil {
		return l, err
	}
	n, err := r.U16LE()
	if err != nil {
		return l, err
	}
	if int(n) > r.Remaining() {
		return l, errors.New(TagLicenseSizeInvalid, "an error blob longer than the licensing message", "length", n, "remaining", r.Remaining())
	}
	l.Blob, err = r.Bytes(int(n))
	return l, err
}
