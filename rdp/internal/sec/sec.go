// Package sec reads and writes the PDUs of the RDP Security Commencement,
// Secure Settings Exchange and Licensing phases that a TLS server needs
// (MS-RDPBCGR 1.3.1.1): the Basic Security Header (2.2.8.1.1.2.1), the
// Client Info PDU (2.2.1.11), the license error message that says the client
// is valid (2.2.1.12), and the Set Error Info PDU that tells a client why it
// is being disconnected (2.2.5.1).
//
// TLS encrypts everything, so no PDU here is encrypted with Standard RDP
// Security: a security header that says SEC_ENCRYPT is refused.
package sec

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors this package returns.
const (
	// TagEncrypted: a security header with SEC_ENCRYPT; Standard RDP
	// Security is not used, since TLS encrypts the connection.
	TagEncrypted = "SecEncrypted"
	// TagFlagMissing: a security header without the flag the PDU needs
	// (SEC_INFO_PKT on Client Info, SEC_LICENSE_PKT on a licensing PDU).
	TagFlagMissing = "SecFlagMissing"
)

// HeaderLen is the size of the Basic Security Header.
const HeaderLen = 4

// Security header flags (MS-RDPBCGR 2.2.8.1.1.2.1).
const (
	FlagExchangePkt      uint16 = 0x0001
	FlagEncrypt          uint16 = 0x0008
	FlagInfoPkt          uint16 = 0x0040
	FlagLicensePkt       uint16 = 0x0080
	FlagLicenseEncryptCS uint16 = 0x0200
	FlagRedirectionPkt   uint16 = 0x0400
	FlagSecureChecksum   uint16 = 0x0800
	FlagFlagsHiValid     uint16 = 0x8000
)

// readHeader reads a Basic Security Header and checks that it has want and
// is not encrypted. flagsHi is read and ignored.
func readHeader(r *wire.Reader, want uint16) error {
	flags, err := r.U16LE()
	if err != nil {
		return err
	}
	if _, err := r.U16LE(); err != nil {
		return err
	}
	if flags&FlagEncrypt != 0 {
		return errors.New(TagEncrypted, "an encrypted PDU; TLS is the only security", "flags", flags)
	}
	if flags&want == 0 {
		return errors.New(TagFlagMissing, "the security header lacks the PDU's flag", "flags", flags, "want", want)
	}
	return nil
}

// writeHeader appends a Basic Security Header with flags and flagsHi 0.
func writeHeader(w *wire.Writer, flags uint16) {
	w.U16LE(flags)
	w.U16LE(0)
}
