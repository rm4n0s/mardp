package sec

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

func assertRoute(t *testing.T, err error, route string) {
	t.Helper()
	appErr, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("expected *errors.Error, got %T: %v", err, err)
	}
	if !appErr.HasRoute(route) {
		t.Errorf("unexpected failure path: %s, want %s", appErr.Route(), route)
	}
}

// infoExample is the decrypted Info Packet of MS-RDPBCGR 4.1.10.
func infoExample(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ms-rdpbcgr", "client-info.bin"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMicrosoftInfoPacket(t *testing.T) {
	b := infoExample(t)
	c, err := ParseInfoPacket(b)
	if err != nil {
		t.Fatal(err)
	}
	want := ClientInfo{
		CodePage: 0x04090409,
		Flags: InfoMouse | InfoDisableCtrlAltDel | InfoUnicode | InfoMaximizeShell | InfoCompression |
			InfoEnableWindowsKey | 1<<9 | InfoForceEncryptedCSPDU,
		Domain:        "NTDEV",
		UserName:      "eltons",
		Extended:      true,
		AddressFamily: AFInet,
		Address:       "157.59.242.156",
		Dir:           `C:\depots\w2k3_1\termsrv\newclient\lib\win32\obj\i386\mstscax.dll`,
		Optional:      OptAutoReconnect,
		TimeZone: TimeZone{
			Bias:         480,
			StandardName: "Pacific Standard Time",
			StandardDate: SystemTime{Month: 10, Day: 5, Hour: 2},
			DaylightName: "Pacific Daylight Time",
			DaylightDate: SystemTime{Month: 4, Day: 1, Hour: 2},
			DaylightBias: -60,
		},
		PerformanceFlags: 1,
	}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("got %+v\nwant %+v", c, want)
	}
	if c.Flags != 0x43b3 {
		t.Errorf("flags %#x, want 0x43b3", c.Flags)
	}
	var w wire.Writer
	if err := WriteInfoPacket(&w, &c); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Bytes(), b) {
		t.Errorf("written:\n% x\nwant:\n% x", w.Bytes(), b)
	}
}

// full is a ClientInfo with every optional field.
func full(unicode bool) ClientInfo {
	c := ClientInfo{
		CodePage:       0x0409,
		Flags:          InfoMouse | InfoAutologon | InfoLogonErrors,
		Domain:         "WORKGROUP",
		UserName:       "émile",
		Password:       "s3cr3t-pässword",
		AlternateShell: "shell",
		WorkingDir:     "dir",
		Extended:       true,
		AddressFamily:  AFInet6,
		Address:        "::1",
		Dir:            "/usr/bin/xfreerdp",
		Optional:       OptDynamicDST,
		TimeZone: TimeZone{
			Bias: -120, StandardName: "GTB Standard Time", StandardDate: SystemTime{2024, 10, 0, 5, 4, 0, 0, 0},
			StandardBias: 0, DaylightName: "GTB Daylight Time", DaylightDate: SystemTime{2024, 3, 0, 5, 3, 0, 0, 0}, DaylightBias: -60,
		},
		SessionID:                   7,
		PerformanceFlags:            0x80000181,
		HasCookie:                   true,
		Cookie:                      AutoReconnectCookie{Length: CookieLen, Version: 1, LogonID: 42, SecurityVerifier: [16]byte{1, 2, 3, 15: 16}},
		Reserved1:                   0,
		Reserved2:                   0,
		DynamicDSTKeyName:           "GTB Standard Time",
		DynamicDaylightTimeDisabled: 1,
	}
	if unicode {
		c.Flags |= InfoUnicode
	}
	return c
}

// trimmed is c cut to the optional fields up to opt.
func trimmed(c ClientInfo, opt int) ClientInfo {
	c.Optional = opt
	var zero ClientInfo
	if opt < OptTimeZone {
		c.TimeZone = zero.TimeZone
	}
	if opt < OptSessionID {
		c.SessionID = 0
	}
	if opt < OptPerformanceFlags {
		c.PerformanceFlags = 0
	}
	if opt < OptAutoReconnect {
		c.HasCookie, c.Cookie = false, zero.Cookie
	}
	if opt < OptReserved {
		c.Reserved1, c.Reserved2 = 0, 0
	}
	if opt < OptDynamicDST {
		c.DynamicDSTKeyName, c.DynamicDaylightTimeDisabled = "", 0
	}
	return c
}

func TestClientInfoRoundTrip(t *testing.T) {
	for _, unicode := range []bool{true, false} {
		for opt := 0; opt <= OptDynamicDST; opt++ {
			for _, cookie := range []bool{true, false} {
				c := trimmed(full(unicode), opt)
				if !cookie {
					c.HasCookie, c.Cookie = false, AutoReconnectCookie{}
				}
				t.Run(fmt.Sprintf("unicode=%v/opt=%d/cookie=%v", unicode, opt, cookie), func(t *testing.T) {
					var w wire.Writer
					if err := WriteClientInfo(&w, &c); err != nil {
						t.Fatal(err)
					}
					got, err := ParseClientInfo(w.Bytes())
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, c) {
						t.Errorf("got %+v\nwant %+v", got, c)
					}
					for o := OptTimeZone; o <= OptDynamicDST; o++ {
						if got.Has(o) != (o <= opt) {
							t.Errorf("Has(%d) = %v", o, got.Has(o))
						}
					}
				})
			}
		}
	}
	t.Run("not extended", func(t *testing.T) {
		c := ClientInfo{Flags: InfoUnicode, UserName: "u", Password: "p"}
		var w wire.Writer
		if err := WriteClientInfo(&w, &c); err != nil {
			t.Fatal(err)
		}
		got, err := ParseClientInfo(w.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, c) || got.Has(OptTimeZone) {
			t.Errorf("got %+v, want %+v", got, c)
		}
	})
	t.Run("ANSI replaces what it cannot hold", func(t *testing.T) {
		c := ClientInfo{UserName: "a€b"}
		var w wire.Writer
		if err := WriteInfoPacket(&w, &c); err != nil {
			t.Fatal(err)
		}
		got, err := ParseInfoPacket(w.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if got.UserName != "a?b" {
			t.Errorf("user %q, want a?b", got.UserName)
		}
	})
	t.Run("empty address and directory", func(t *testing.T) {
		b := []byte{0, 0, 0, 0, 0x10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
		b = append(b, 2, 0, 0, 0, 0, 0)
		got, err := ParseInfoPacket(b)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Extended || got.Address != "" || got.Dir != "" {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("long time zone name is cut", func(t *testing.T) {
		c := ClientInfo{Flags: InfoUnicode, Extended: true, Optional: OptTimeZone}
		c.TimeZone.StandardName = strings.Repeat("x", 40)
		var w wire.Writer
		if err := WriteInfoPacket(&w, &c); err != nil {
			t.Fatal(err)
		}
		got, err := ParseInfoPacket(w.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if got.TimeZone.StandardName != strings.Repeat("x", 31) {
			t.Errorf("name %q", got.TimeZone.StandardName)
		}
	})
}

func TestPasswordHidden(t *testing.T) {
	c := full(true)
	pw := string(c.Password)
	for _, f := range []string{"%v", "%+v", "%#v", "%s"} {
		if s := fmt.Sprintf(f, c); strings.Contains(s, pw) {
			t.Errorf("%s shows the password: %s", f, s)
		}
	}
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	log.Info("info", "client", c, "password", c.Password)
	if strings.Contains(buf.String(), pw) {
		t.Errorf("the log shows the password: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"password":true`) {
		t.Errorf("the log does not say a password was sent: %s", buf.String())
	}
}

func TestTruncatedAndHostile(t *testing.T) {
	c := full(true)
	var w wire.Writer
	if err := WriteClientInfo(&w, &c); err != nil {
		t.Fatal(err)
	}
	b := w.Bytes()
	for n := range len(b) {
		got, err := ParseClientInfo(b[:n])
		if err != nil {
			if _, ok := errors.FromError(err); !ok {
				t.Fatalf("length %d: %T %v", n, err, err)
			}
			continue
		}
		if got.Optional > OptDynamicDST {
			t.Fatalf("length %d: optional %d", n, got.Optional)
		}
	}
	rng := rand.New(rand.NewPCG(1, 6))
	for range 20000 {
		m := bytes.Clone(b)
		for range 1 + rng.IntN(8) {
			m[rng.IntN(len(m))] = byte(rng.Uint32())
		}
		m = m[:rng.IntN(len(m)+1)]
		if _, err := ParseClientInfo(m); err != nil {
			if _, ok := errors.FromError(err); !ok {
				t.Fatalf("%T %v", err, err)
			}
		}
		if _, err := ParseLicenseError(m); err != nil {
			if _, ok := errors.FromError(err); !ok {
				t.Fatalf("%T %v", err, err)
			}
		}
		if _, _, err := ParseSetErrorInfo(m); err != nil {
			if _, ok := errors.FromError(err); !ok {
				t.Fatalf("%T %v", err, err)
			}
		}
	}
}

// licenseExample is the decrypted licensing packet of MS-RDPBCGR 4.1.11.
var licenseExample = []byte{0xff, 0x03, 0x10, 0x00, 0x07, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00}

func TestLicenseValidClient(t *testing.T) {
	var w wire.Writer
	WriteLicenseValidClient(&w)
	want := append([]byte{0x80, 0x00, 0x00, 0x00}, licenseExample...)
	if !bytes.Equal(w.Bytes(), want) {
		t.Fatalf("got % x, want % x", w.Bytes(), want)
	}
	l, err := ParseLicenseError(w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if l.ErrorCode != LicenseStatusValidClient || l.StateTransition != LicenseNoTransition ||
		l.BlobType != LicenseErrorBlob || len(l.Blob) != 0 || l.Flags != LicensePreambleVersion3 {
		t.Errorf("got %+v", l)
	}
	withBlob := append([]byte{0x80, 0, 0, 0, 0xff, 0x83, 0x12, 0}, licenseExample[4:]...)
	withBlob[18] = 2
	withBlob = append(withBlob, 'h', 'i')
	l, err = ParseLicenseError(withBlob)
	if err != nil {
		t.Fatal(err)
	}
	if string(l.Blob) != "hi" || l.Flags != LicensePreambleVersion3|LicenseExtendedErrorMsg {
		t.Errorf("got %+v", l)
	}
}

func TestSetErrorInfo(t *testing.T) {
	var w wire.Writer
	WriteSetErrorInfo(&w, 0x000103ea, ErrInfoFreshCredentialsRequired)
	want := []byte{
		0x16, 0x00, 0x17, 0x00, 0x00, 0x00, // totalLength 22, Data PDU, pduSource 0
		0xea, 0x03, 0x01, 0x00, 0x00, 0x01, // shareID, pad1, STREAM_LOW
		0x08, 0x00, 0x2f, 0x00, 0x00, 0x00, // uncompressedLength 8, Set Error Info, no compression
		0x0a, 0x00, 0x00, 0x00,
	}
	if !bytes.Equal(w.Bytes(), want) {
		t.Fatalf("got % x, want % x", w.Bytes(), want)
	}
	share, code, err := ParseSetErrorInfo(w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if share != 0x000103ea || code != ErrInfoFreshCredentialsRequired {
		t.Errorf("share %#x code %#x", share, code)
	}
}

func TestAllocations(t *testing.T) {
	w := wire.NewWriter(make([]byte, 0, 64))
	tests := []struct {
		name string
		run  func()
	}{
		{"license", func() { w.Reset(); WriteLicenseValidClient(&w) }},
		{"set error info", func() { w.Reset(); WriteSetErrorInfo(&w, 1, 2) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if n := testing.AllocsPerRun(100, tt.run); n != 0 {
				t.Errorf("%v allocations per run, want 0", n)
			}
		})
	}
}

func TestErrors(t *testing.T) {
	ex := infoExample(t)
	with := func(b []byte, off int, v ...byte) []byte {
		c := bytes.Clone(b)
		copy(c[off:], v)
		return c
	}
	u16 := func(v int) []byte { return binary.LittleEndian.AppendUint16(nil, uint16(v)) }
	// ex ends with cbAutoReconnectCookie; these add reserved1 and reserved2
	// and a key name length.
	key := func(n int) []byte { return append(append(bytes.Clone(ex), 0, 0, 0, 0), u16(n)...) }
	hdr := func(flags uint16, b []byte) []byte {
		return append(binary.LittleEndian.AppendUint16(nil, flags), append([]byte{0, 0}, b...)...)
	}
	pci := func(b []byte) func() error { return func() error { _, err := ParseClientInfo(b); return err } }
	pip := func(b []byte) func() error { return func() error { _, err := ParseInfoPacket(b); return err } }
	ple := func(b []byte) func() error { return func() error { _, err := ParseLicenseError(b); return err } }
	pse := func(b []byte) func() error { return func() error { _, _, err := ParseSetErrorInfo(b); return err } }
	wip := func(c ClientInfo) func() error {
		return func() error { var w wire.Writer; return WriteInfoPacket(&w, &c) }
	}
	var se wire.Writer
	WriteSetErrorInfo(&se, 1, 2)
	seb := se.Bytes()
	lic := append([]byte{0x80, 0, 0, 0}, licenseExample...)
	long := strings.Repeat("x", 256)
	tests := []struct {
		name  string
		run   func() error
		route string
	}{
		{"encrypted", pci(hdr(FlagInfoPkt|FlagEncrypt, ex)), "ParseClientInfo->readHeader.SecEncrypted"},
		{"not info", pci(hdr(FlagLicensePkt, ex)), "ParseClientInfo->readHeader.SecFlagMissing"},
		{"no header", pci([]byte{0x40, 0}), "ParseClientInfo->readHeader->Reader.U16LE.WireTruncated"},
		{"not license", ple(hdr(FlagInfoPkt, licenseExample)), "ParseLicenseError->readHeader.SecFlagMissing"},
		{"fixed part truncated", pip(ex[:17]), "ParseInfoPacket->Reader.U16LE.WireTruncated"},
		{"domain too long", pip(with(ex, 8, u16(511)...)), "ParseInfoPacket->readString.SecStringTooLong"},
		{"user name odd", pip(with(ex, 10, u16(3)...)), "ParseInfoPacket->readString.SecStringOddLength"},
		{"domain unterminated", pip(with(ex, 28, 'x')), "ParseInfoPacket->readString.SecStringUnterminated"},
		{"domain truncated", pip(ex[:20]), "ParseInfoPacket->readString->Reader.Bytes.WireTruncated"},
		{"address too long", pip(with(ex, 52, u16(82)...)), "ParseInfoPacket->readExtended->readString.SecStringTooLong"},
		{"directory unterminated", pip(with(ex, 0xd8, 'x')), "ParseInfoPacket->readExtended->readString.SecStringUnterminated"},
		{"time zone truncated", pip(ex[:0xda+171]), "readExtended->readTimeZone->Reader.Bytes.WireTruncated"},
		{"cookie length", pip(with(ex, 398, 5)), "ParseInfoPacket->readExtended->readCookie.SecCookieLengthInvalid"},
		{"cookie truncated", pip(append(with(ex, 398, 28), 1, 2, 3)), "readExtended->readCookie->Reader.U32LE.WireTruncated"},
		{"reserved2 missing", pip(append(bytes.Clone(ex), 0, 0)), "ParseInfoPacket->readExtended->Reader.U16LE.WireTruncated"},
		{"key name too long", pip(key(256)), "ParseInfoPacket->readExtended.SecStringTooLong"},
		{"key name odd", pip(key(3)), "ParseInfoPacket->readExtended.SecStringOddLength"},
		{"key name truncated", pip(key(4)), "ParseInfoPacket->readExtended->Reader.Bytes.WireTruncated"},
		{"write user too long", wip(ClientInfo{Flags: InfoUnicode, UserName: long}), "WriteInfoPacket.SecStringTooLong"},
		{"write address too long", wip(ClientInfo{Extended: true, Address: strings.Repeat("1", 80)}), "WriteInfoPacket.SecStringTooLong"},
		{"write directory too long", wip(ClientInfo{Extended: true, Dir: strings.Repeat("d", 512)}), "WriteInfoPacket.SecStringTooLong"},
		{"write key name too long", wip(ClientInfo{Extended: true, Optional: OptDynamicDST, DynamicDSTKeyName: strings.Repeat("k", 128)}), "WriteInfoPacket->writeOptional.SecStringTooLong"},
		{"license not an error", ple(with(lic, 4, 0x02)), "ParseLicenseError.SecLicenseUnexpected"},
		{"license size", ple(with(lic, 6, 0x11)), "ParseLicenseError.SecLicenseSizeInvalid"},
		{"license blob", ple(with(lic, 18, 1)), "ParseLicenseError.SecLicenseSizeInvalid"},
		{"license truncated", ple(lic[:7]), "ParseLicenseError->Reader.U16LE.WireTruncated"},
		{"share length", pse(seb[:21]), "ParseSetErrorInfo.SecShareHeaderInvalid"},
		{"share type", pse(with(seb, 2, 0x11)), "ParseSetErrorInfo.SecShareHeaderInvalid"},
		{"not set error info", pse(with(seb, 14, 0x1f)), "ParseSetErrorInfo.SecNotSetErrorInfo"},
		{"share truncated", pse([]byte{4, 0, 0x17, 0}), "ParseSetErrorInfo->Reader.Skip.WireTruncated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil {
				t.Fatal("no error")
			}
			assertRoute(t, err, tt.route)
		})
	}
}
