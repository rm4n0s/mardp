package sec

import (
	"unicode/utf16"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// WriteClientInfo appends the data of a Client Info PDU: a security header
// with SEC_INFO_PKT, then the Info Packet of c.
func WriteClientInfo(w *wire.Writer, c *ClientInfo) error {
	writeHeader(w, FlagInfoPkt)
	return WriteInfoPacket(w, c)
}

// WriteInfoPacket appends c as an Info Packet, with the Extended Info Packet
// when c.Extended is set and its optional fields up to c.Optional. Strings
// are written as UTF-16 when c.Flags has InfoUnicode, otherwise as one byte
// per character (a character above U+00FF becomes '?').
func WriteInfoPacket(w *wire.Writer, c *ClientInfo) error {
	unicode := c.Flags&InfoUnicode != 0
	logon := [5]string{c.Domain, c.UserName, string(c.Password), c.AlternateShell, c.WorkingDir}
	names := [5]string{"Domain", "UserName", "Password", "AlternateShell", "WorkingDir"}
	var enc [5][]byte
	for i, s := range logon {
		enc[i] = encode(s, unicode)
		if len(enc[i]) > MaxLogonString {
			return errors.New(TagStringTooLong, "a string field too long", "field", names[i], "length", len(enc[i]), "max", MaxLogonString)
		}
	}
	w.U32LE(c.CodePage)
	w.U32LE(c.Flags)
	for _, b := range enc {
		w.U16LE(uint16(len(b) - terminator(unicode)))
	}
	for _, b := range enc {
		w.Write(b)
	}
	if !c.Extended {
		return nil
	}
	addr, dir := encode(c.Address, unicode), encode(c.Dir, unicode)
	if len(addr) > MaxAddress {
		return errors.New(TagStringTooLong, "a string field too long", "field", "clientAddress", "length", len(addr), "max", MaxAddress)
	}
	if len(dir) > MaxClientDir {
		return errors.New(TagStringTooLong, "a string field too long", "field", "clientDir", "length", len(dir), "max", MaxClientDir)
	}
	w.U16LE(c.AddressFamily)
	w.U16LE(uint16(len(addr)))
	w.Write(addr)
	w.U16LE(uint16(len(dir)))
	w.Write(dir)
	return writeOptional(w, c)
}

func writeOptional(w *wire.Writer, c *ClientInfo) error {
	if c.Optional < OptTimeZone {
		return nil
	}
	writeTimeZone(w, &c.TimeZone)
	if c.Optional < OptSessionID {
		return nil
	}
	w.U32LE(c.SessionID)
	if c.Optional < OptPerformanceFlags {
		return nil
	}
	w.U32LE(c.PerformanceFlags)
	if c.Optional < OptAutoReconnect {
		return nil
	}
	if c.HasCookie {
		w.U16LE(CookieLen)
		w.U32LE(c.Cookie.Length)
		w.U32LE(c.Cookie.Version)
		w.U32LE(c.Cookie.LogonID)
		w.Write(c.Cookie.SecurityVerifier[:])
	} else {
		w.U16LE(0)
	}
	if c.Optional < OptReserved {
		return nil
	}
	w.U16LE(c.Reserved1)
	w.U16LE(c.Reserved2)
	if c.Optional < OptDynamicDST {
		return nil
	}
	name := encode(c.DynamicDSTKeyName, true)
	name = name[:len(name)-2] // no terminator
	if len(name) > MaxKeyName {
		return errors.New(TagStringTooLong, "a string field too long", "field", "dynamicDSTTimeZoneKeyName", "length", len(name), "max", MaxKeyName)
	}
	w.U16LE(uint16(len(name)))
	w.Write(name)
	w.U16LE(c.DynamicDaylightTimeDisabled)
	return nil
}

func writeTimeZone(w *wire.Writer, tz *TimeZone) {
	w.U32LE(uint32(tz.Bias))
	fixedUTF16(w, tz.StandardName)
	writeSystemTime(w, &tz.StandardDate)
	w.U32LE(uint32(tz.StandardBias))
	fixedUTF16(w, tz.DaylightName)
	writeSystemTime(w, &tz.DaylightDate)
	w.U32LE(uint32(tz.DaylightBias))
}

func writeSystemTime(w *wire.Writer, st *SystemTime) {
	for _, v := range [...]uint16{st.Year, st.Month, st.DayOfWeek, st.Day, st.Hour, st.Minute, st.Second, st.Milliseconds} {
		w.U16LE(v)
	}
}

// fixedUTF16 appends s as a 64-byte time zone name: at most 31 UTF-16 units,
// then zeros.
func fixedUTF16(w *wire.Writer, s string) {
	b := encode(s, true)
	if len(b) > timeZoneName {
		b = append(b[:timeZoneName-2], 0, 0)
	}
	w.Write(b)
	w.Zeros(timeZoneName - len(b))
}

func terminator(unicode bool) int {
	if unicode {
		return 2
	}
	return 1
}

// encode returns s with its null terminator, as UTF-16LE or one byte per
// character.
func encode(s string, unicode bool) []byte {
	if !unicode {
		b := make([]byte, 0, len(s)+1)
		for _, r := range s {
			if r > 0xff {
				r = '?'
			}
			b = append(b, byte(r))
		}
		return append(b, 0)
	}
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, 2*len(u)+2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return append(b, 0, 0)
}
