package sec

import (
	"encoding/binary"
	"log/slog"
	"unicode/utf16"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors of the Client Info PDU.
const (
	// TagStringTooLong: a string field longer than MS-RDPBCGR allows
	// (512 bytes for the logon strings and the client directory, 80 for
	// the address, 254 for the time zone key name, terminator included).
	TagStringTooLong = "SecStringTooLong"
	// TagStringOddLength: a Unicode string field with an odd number of
	// bytes.
	TagStringOddLength = "SecStringOddLength"
	// TagStringUnterminated: a string field whose mandatory null terminator
	// is not zero.
	TagStringUnterminated = "SecStringUnterminated"
	// TagCookieLengthInvalid: cbAutoReconnectCookie neither 0 nor 28.
	TagCookieLengthInvalid = "SecCookieLengthInvalid"
)

// Info Packet flags (MS-RDPBCGR 2.2.1.11.1.1).
const (
	InfoMouse               uint32 = 0x00000001
	InfoDisableCtrlAltDel   uint32 = 0x00000002
	InfoAutologon           uint32 = 0x00000008
	InfoUnicode             uint32 = 0x00000010
	InfoMaximizeShell       uint32 = 0x00000020
	InfoLogonNotify         uint32 = 0x00000040
	InfoCompression         uint32 = 0x00000080
	InfoEnableWindowsKey    uint32 = 0x00000100
	InfoCompressionTypeMask uint32 = 0x00001e00
	InfoRemoteConsoleAudio  uint32 = 0x00002000
	InfoForceEncryptedCSPDU uint32 = 0x00004000
	InfoRail                uint32 = 0x00008000
	InfoLogonErrors         uint32 = 0x00010000
	InfoMouseHasWheel       uint32 = 0x00020000
	InfoPasswordIsSCPin     uint32 = 0x00040000
	InfoNoAudioPlayback     uint32 = 0x00080000
	InfoUsingSavedCreds     uint32 = 0x00100000
	InfoAudioCapture        uint32 = 0x00200000
	InfoVideoDisable        uint32 = 0x00400000
	InfoHiDefRailSupported  uint32 = 0x02000000
)

// Client address families (MS-RDPBCGR 2.2.1.11.1.1.1).
const (
	AFInet  uint16 = 0x0002
	AFInet6 uint16 = 0x0017
)

// The largest string fields, in bytes, terminator included
// (MS-RDPBCGR 2.2.1.11.1.1 and 2.2.1.11.1.1.1).
const (
	MaxLogonString = 512
	MaxAddress     = 80
	MaxClientDir   = 512
	MaxKeyName     = 254
)

// Sizes of the fixed parts.
const (
	infoFixedLen = 18  // CodePage to cbWorkingDir
	timeZoneLen  = 172 // TS_TIME_ZONE_INFORMATION
	timeZoneName = 64  // StandardName and DaylightName: 32 UTF-16 units
	CookieLen    = 28  // ARC_CS_PRIVATE_PACKET
	verifierLen  = 16
)

// The optional fields of the Extended Info Packet after clientDir, in their
// order. Each needs the ones before it, so ClientInfo.Optional is the last
// one present and Has tells whether a field is.
const (
	OptTimeZone         = 1 // clientTimeZone
	OptSessionID        = 2 // clientSessionId
	OptPerformanceFlags = 3 // performanceFlags
	OptAutoReconnect    = 4 // cbAutoReconnectCookie and the cookie
	OptReserved         = 5 // reserved1 and reserved2
	OptDynamicDST       = 6 // cbDynamicDSTTimeZoneKeyName, the name and dynamicDaylightTimeDisabled
)

// SystemTime is TS_SYSTEMTIME (MS-RDPBCGR 2.2.1.11.1.1.1.1.1).
type SystemTime struct {
	Year, Month, DayOfWeek, Day, Hour, Minute, Second, Milliseconds uint16
}

// TimeZone is TS_TIME_ZONE_INFORMATION (MS-RDPBCGR 2.2.1.11.1.1.1.1).
type TimeZone struct {
	Bias         int32
	StandardName string
	StandardDate SystemTime
	StandardBias int32
	DaylightName string
	DaylightDate SystemTime
	DaylightBias int32
}

// AutoReconnectCookie is ARC_CS_PRIVATE_PACKET (MS-RDPBCGR 2.2.4.3).
type AutoReconnectCookie struct {
	Length           uint32
	Version          uint32
	LogonID          uint32
	SecurityVerifier [verifierLen]byte
}

// Secret is a password. It prints and logs as "(hidden)", so a ClientInfo
// that reaches a log or an error message by mistake does not carry it
// (SPEC §13).
type Secret string

// String hides the secret.
func (Secret) String() string { return "(hidden)" }

// GoString hides the secret from %#v.
func (Secret) GoString() string { return "(hidden)" }

// LogValue hides the secret from slog.
func (Secret) LogValue() slog.Value { return slog.StringValue("(hidden)") }

// ClientInfo is the Info Packet (TS_INFO_PACKET, MS-RDPBCGR 2.2.1.11.1.1)
// with its Extended Info Packet (2.2.1.11.1.1.1). Strings are decoded:
// UTF-16 when Flags has InfoUnicode, otherwise one byte per character
// (Windows-1252 agrees with it on every printable ASCII character).
type ClientInfo struct {
	CodePage       uint32
	Flags          uint32
	Domain         string
	UserName       string
	Password       Secret
	AlternateShell string
	WorkingDir     string

	// Extended tells whether the Extended Info Packet is there; the fields
	// below are zero when it is not.
	Extended      bool
	AddressFamily uint16
	Address       string
	Dir           string
	// Optional is the last optional field present (OptTimeZone to
	// OptDynamicDST), 0 for none.
	Optional         int
	TimeZone         TimeZone
	SessionID        uint32
	PerformanceFlags uint32
	// HasCookie tells whether cbAutoReconnectCookie was 28 and Cookie
	// holds the cookie.
	HasCookie                   bool
	Cookie                      AutoReconnectCookie
	Reserved1, Reserved2        uint16
	DynamicDSTKeyName           string
	DynamicDaylightTimeDisabled uint16
}

// Has tells whether the optional field opt (OptTimeZone to OptDynamicDST)
// is present.
func (c *ClientInfo) Has(opt int) bool { return c.Extended && c.Optional >= opt }

// LogValue is what the log shows of the client's info: never the password,
// only whether there is one.
func (c ClientInfo) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("user", c.UserName),
		slog.String("domain", c.Domain),
		slog.Bool("password", c.Password != ""),
		slog.String("flags", hex32(c.Flags)),
		slog.String("address", c.Address),
		slog.String("dir", c.Dir),
		slog.Int("optional", c.Optional),
		slog.String("timezone", c.TimeZone.StandardName),
		slog.String("performance", hex32(c.PerformanceFlags)),
	)
}

func hex32(v uint32) string {
	const digits = "0123456789abcdef"
	b := [10]byte{'0', 'x'}
	for i := 9; i >= 2; i-- {
		b[i] = digits[v&0xf]
		v >>= 4
	}
	return string(b[:])
}

// ParseClientInfo reads the data of a Client Info PDU (MS-RDPBCGR 2.2.1.11.1):
// the security header, which has to say SEC_INFO_PKT, then the Info Packet.
func ParseClientInfo(b []byte) (ClientInfo, error) {
	r := wire.NewReader(b)
	if err := readHeader(&r, FlagInfoPkt); err != nil {
		return ClientInfo{}, err
	}
	return ParseInfoPacket(r.Rest())
}

// ParseInfoPacket reads an Info Packet with its Extended Info Packet. Bytes
// after the last field are ignored.
func ParseInfoPacket(b []byte) (ClientInfo, error) {
	var c ClientInfo
	r := wire.NewReader(b)
	var err error
	if c.CodePage, err = r.U32LE(); err != nil {
		return c, err
	}
	if c.Flags, err = r.U32LE(); err != nil {
		return c, err
	}
	var cb [5]uint16
	for i := range cb {
		if cb[i], err = r.U16LE(); err != nil {
			return c, err
		}
	}
	unicode := c.Flags&InfoUnicode != 0
	term := 1
	if unicode {
		term = 2
	}
	fields := [5]*string{&c.Domain, &c.UserName, nil, &c.AlternateShell, &c.WorkingDir}
	names := [5]string{"Domain", "UserName", "Password", "AlternateShell", "WorkingDir"}
	for i, f := range fields {
		s, err := readString(&r, unicode, int(cb[i])+term, MaxLogonString, names[i])
		if err != nil {
			return c, err
		}
		if f == nil {
			c.Password = Secret(s)
		} else {
			*f = s
		}
	}
	if r.Remaining() == 0 {
		return c, nil
	}
	c.Extended = true
	return c, readExtended(&r, &c, unicode)
}

func readExtended(r *wire.Reader, c *ClientInfo, unicode bool) error {
	var err error
	if c.AddressFamily, err = r.U16LE(); err != nil {
		return err
	}
	n, err := r.U16LE()
	if err != nil {
		return err
	}
	if c.Address, err = readString(r, unicode, int(n), MaxAddress, "clientAddress"); err != nil {
		return err
	}
	if n, err = r.U16LE(); err != nil {
		return err
	}
	if c.Dir, err = readString(r, unicode, int(n), MaxClientDir, "clientDir"); err != nil {
		return err
	}
	if r.Remaining() == 0 {
		return nil
	}
	if err := readTimeZone(r, &c.TimeZone); err != nil {
		return err
	}
	c.Optional = OptTimeZone
	if r.Remaining() == 0 {
		return nil
	}
	if c.SessionID, err = r.U32LE(); err != nil {
		return err
	}
	c.Optional = OptSessionID
	if r.Remaining() == 0 {
		return nil
	}
	if c.PerformanceFlags, err = r.U32LE(); err != nil {
		return err
	}
	c.Optional = OptPerformanceFlags
	if r.Remaining() == 0 {
		return nil
	}
	if err := readCookie(r, c); err != nil {
		return err
	}
	c.Optional = OptAutoReconnect
	if r.Remaining() == 0 {
		return nil
	}
	if c.Reserved1, err = r.U16LE(); err != nil {
		return err
	}
	if c.Reserved2, err = r.U16LE(); err != nil {
		return err
	}
	c.Optional = OptReserved
	if r.Remaining() == 0 {
		return nil
	}
	if n, err = r.U16LE(); err != nil {
		return err
	}
	if n > MaxKeyName {
		return errors.New(TagStringTooLong, "a string field too long", "field", "dynamicDSTTimeZoneKeyName", "length", n, "max", MaxKeyName)
	}
	if n%2 != 0 {
		return errors.New(TagStringOddLength, "a Unicode string field of an odd length", "field", "dynamicDSTTimeZoneKeyName", "length", n)
	}
	name, err := r.Bytes(int(n))
	if err != nil {
		return err
	}
	c.DynamicDSTKeyName = decodeUTF16(name)
	if c.DynamicDaylightTimeDisabled, err = r.U16LE(); err != nil {
		return err
	}
	c.Optional = OptDynamicDST
	return nil
}

func readCookie(r *wire.Reader, c *ClientInfo) error {
	n, err := r.U16LE()
	if err != nil {
		return err
	}
	switch n {
	case 0:
		return nil
	case CookieLen:
	default:
		return errors.New(TagCookieLengthInvalid, "cbAutoReconnectCookie is neither 0 nor 28", "length", n)
	}
	k := &c.Cookie
	for _, v := range []*uint32{&k.Length, &k.Version, &k.LogonID} {
		if *v, err = r.U32LE(); err != nil {
			return err
		}
	}
	v, err := r.Bytes(verifierLen)
	if err != nil {
		return err
	}
	copy(k.SecurityVerifier[:], v)
	c.HasCookie = true
	return nil
}

func readTimeZone(r *wire.Reader, tz *TimeZone) error {
	b, err := r.Bytes(timeZoneLen)
	if err != nil {
		return err
	}
	le := binary.LittleEndian
	tz.Bias = int32(le.Uint32(b[0:]))
	tz.StandardName = decodeUTF16(b[4 : 4+timeZoneName])
	systemTime(b[68:], &tz.StandardDate)
	tz.StandardBias = int32(le.Uint32(b[84:]))
	tz.DaylightName = decodeUTF16(b[88 : 88+timeZoneName])
	systemTime(b[152:], &tz.DaylightDate)
	tz.DaylightBias = int32(le.Uint32(b[168:]))
	return nil
}

// systemTime decodes the 16 bytes of a TS_SYSTEMTIME at the start of b.
func systemTime(b []byte, st *SystemTime) {
	for i, v := range []*uint16{&st.Year, &st.Month, &st.DayOfWeek, &st.Day, &st.Hour, &st.Minute, &st.Second, &st.Milliseconds} {
		*v = binary.LittleEndian.Uint16(b[2*i:])
	}
}

// readString reads a string field of n bytes, its null terminator included,
// and decodes it. n is 0 for an absent field (the address and directory of
// some clients), which gives "".
func readString(r *wire.Reader, unicode bool, n, maxLen int, field string) (string, error) {
	if n == 0 {
		return "", nil
	}
	if n > maxLen {
		return "", errors.New(TagStringTooLong, "a string field too long", "field", field, "length", n, "max", maxLen)
	}
	if unicode && n%2 != 0 {
		return "", errors.New(TagStringOddLength, "a Unicode string field of an odd length", "field", field, "length", n)
	}
	b, err := r.Bytes(n)
	if err != nil {
		return "", err
	}
	if b[n-1] != 0 || (unicode && b[n-2] != 0) {
		return "", errors.New(TagStringUnterminated, "a string field without its null terminator", "field", field, "length", n)
	}
	if unicode {
		return decodeUTF16(b[:n-2]), nil
	}
	return decodeLatin1(b[:n-1]), nil
}

// decodeUTF16 decodes UTF-16LE up to the first null character.
func decodeUTF16(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		c := uint16(b[i]) | uint16(b[i+1])<<8
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}

// decodeLatin1 decodes one byte per character up to the first null.
func decodeLatin1(b []byte) string {
	r := make([]rune, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		r = append(r, rune(c))
	}
	return string(r)
}
