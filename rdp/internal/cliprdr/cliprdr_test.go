package cliprdr

import (
	"bytes"
	"encoding/hex"
	"slices"
	"strings"
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// unhex turns a dump into bytes.
func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// assertRoute fails unless err is an *errors.Error whose route has route.
func assertRoute(t *testing.T, err error, route string) {
	t.Helper()
	appErr, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("expected *errors.Error, got %T (%v)", err, err)
	}
	if !appErr.HasRoute(route) {
		t.Errorf("route %s, want %s", appErr.Route(), route)
	}
}

func TestWrite(t *testing.T) {
	tests := []struct {
		name  string
		write func(w *wire.Writer)
		want  string
	}{
		// MS-RDPECLIP 4.1.1, with the server's flags of this library.
		{"caps", func(w *wire.Writer) { AppendCaps(w, CapsLongFormatNames) },
			"07 00 00 00 10 00 00 00  01 00 00 00  01 00 0c 00 02 00 00 00 02 00 00 00"},
		// MS-RDPECLIP 4.1.2.
		{"monitor ready", AppendMonitorReady, "01 00 00 00 00 00 00 00"},
		{"long format list", func(w *wire.Writer) {
			AppendFormatList(w, []Format{{ID: FormatUnicodeText}, {ID: 0xc004, Name: "Ab"}}, true)
		}, "02 00 00 00 10 00 00 00  0d 00 00 00 00 00  04 c0 00 00 41 00 62 00 00 00"},
		{"short format list", func(w *wire.Writer) {
			AppendFormatList(w, []Format{{ID: FormatText}, {ID: 0xc004, Name: "0123456789abcdefgh"}}, false)
		}, "02 00 00 00 48 00 00 00  01 00 00 00" + strings.Repeat(" 00", 32) +
			" 04 c0 00 00 30 00 31 00 32 00 33 00 34 00 35 00 36 00 37 00 38 00 39 00 61 00 62 00 63 00 64 00 65 00 00 00"},
		{"empty format list", func(w *wire.Writer) { AppendFormatList(w, nil, true) }, "02 00 00 00 00 00 00 00"},
		{"format list response", func(w *wire.Writer) { AppendFormatListResponse(w, true) }, "03 00 01 00 00 00 00 00"},
		{"format list refused", func(w *wire.Writer) { AppendFormatListResponse(w, false) }, "03 00 02 00 00 00 00 00"},
		{"format data request", func(w *wire.Writer) { AppendFormatDataRequest(w, FormatUnicodeText) }, "04 00 00 00 04 00 00 00 0d 00 00 00"},
		{"format data response", func(w *wire.Writer) { AppendFormatDataResponse(w, true, []byte{0x68, 0, 0x69, 0, 0, 0}) },
			"05 00 01 00 06 00 00 00 68 00 69 00 00 00"},
		{"format data refused", func(w *wire.Writer) { AppendFormatDataResponse(w, false, []byte{1, 2}) }, "05 00 02 00 00 00 00 00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := wire.NewWriter(nil)
			tt.write(&w)
			if want := unhex(t, tt.want); !bytes.Equal(w.Bytes(), want) {
				t.Errorf("wrote\n%x\nwant\n%x", w.Bytes(), want)
			}
			p, err := ReadPDU(w.Bytes())
			if err != nil || len(p.Data) != w.Len()-HeaderLength {
				t.Errorf("read back: %+v %v", p, err)
			}
		})
	}
}

func TestReadPDU(t *testing.T) {
	p, err := ReadPDU(unhex(t, "04 00 00 00 04 00 00 00 0d 00 00 00 ff ff ff ff"))
	if err != nil || p.Type != MsgFormatDataRequest || p.Flags != 0 || !bytes.Equal(p.Data, []byte{0x0d, 0, 0, 0}) {
		t.Errorf("with padding: %+v %v", p, err)
	}
	for _, msg := range []string{"", "04 00 00 00 04 00 00", "04 00 00 00 05 00 00 00 0d 00 00 00"} {
		_, err := ReadPDU(unhex(t, msg))
		assertRoute(t, err, "ReadPDU."+TagHeaderInvalid)
	}
}

func TestCaps(t *testing.T) {
	tests := []struct {
		name, data string
		want       uint32
		route      string
	}{
		// MS-RDPECLIP 4.1.1's body: long names, files, no paths, locking.
		{"general", "01 00 00 00 01 00 0c 00 02 00 00 00 0e 00 00 00", 0x0e, ""},
		{"another set first", "02 00 00 00 09 00 06 00 aa bb 01 00 0c 00 01 00 00 00 02 00 00 00", 2, ""},
		{"no sets", "00 00 00 00", 0, ""},
		{"no general set", "01 00 00 00 09 00 04 00", 0, ""},
		{"no count", "01", 0, "ReadCaps." + TagBodyInvalid},
		{"no padding", "01 00 00", 0, "ReadCaps." + TagBodyInvalid},
		{"set cut short", "01 00 00 00 01 00 0c 00 02 00", 0, "ReadCaps." + TagBodyInvalid},
		{"set shorter than its header", "01 00 00 00 01 00 03 00", 0, "ReadCaps." + TagBodyInvalid},
		{"general set too short", "01 00 00 00 01 00 08 00 02 00 00 00", 0, "ReadCaps." + TagBodyInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReadCaps(unhex(t, tt.data))
			if tt.route != "" {
				assertRoute(t, err, tt.route)
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("flags %#x, %v; want %#x", got, err, tt.want)
			}
		})
	}
}

func TestFormatList(t *testing.T) {
	formats := []Format{{ID: FormatUnicodeText}, {ID: FormatText}, {ID: 0xc0a1, Name: "HTML Format"}, {ID: 0xc0a2, Name: "μ"}}
	for _, long := range []bool{true, false} {
		w := wire.NewWriter(nil)
		AppendFormatList(&w, formats, long)
		p, err := ReadPDU(w.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		got, err := ReadFormatList(p, long)
		if err != nil || !slices.Equal(got, formats) {
			t.Errorf("long %v: read %+v, %v", long, got, err)
		}
	}
	// Short names in 8 bits, as a client with CB_ASCII_NAMES writes them.
	ascii := PDU{Type: MsgFormatList, Flags: FlagASCIINames, Data: append([]byte{0xa1, 0xc0, 0, 0, 'H', 'T', 'M', 'L'}, make([]byte, 28)...)}
	got, err := ReadFormatList(ascii, false)
	if err != nil || !slices.Equal(got, []Format{{ID: 0xc0a1, Name: "HTML"}}) {
		t.Errorf("8-bit names: %+v, %v", got, err)
	}
	if got, err := ReadFormatList(PDU{}, true); err != nil || got != nil {
		t.Errorf("an empty list: %+v, %v", got, err)
	}
	bad := []struct {
		name string
		long bool
		data string
	}{
		{"an ID cut short", true, "0d 00"},
		{"a long name without its end", true, "0d 00 00 00 41 00"},
		{"a short name cut short", false, "0d 00 00 00 41 00 00 00"},
	}
	for _, tt := range bad {
		_, err := ReadFormatList(PDU{Data: unhex(t, tt.data)}, tt.long)
		if err == nil {
			t.Errorf("%s: no error", tt.name)
			continue
		}
		assertRoute(t, err, "ReadFormatList."+TagFormatsInvalid)
	}
}

func TestFormatDataRequest(t *testing.T) {
	if id, err := ReadFormatDataRequest([]byte{0x0d, 0, 0, 0}); err != nil || id != FormatUnicodeText {
		t.Errorf("id %d, %v", id, err)
	}
	_, err := ReadFormatDataRequest([]byte{0x0d, 0})
	assertRoute(t, err, "ReadFormatDataRequest."+TagBodyInvalid)
}

func TestText(t *testing.T) {
	tests := []struct {
		name, text string
		wire       string
		back       string // what DecodeText gives, when not text
	}{
		{"plain", "hi", "68 00 69 00 00 00", ""},
		{"empty", "", "00 00", ""},
		{"lines", "a\nb\n", "61 00 0d 00 0a 00 62 00 0d 00 0a 00 00 00", ""},
		{"lines of Windows", "a\r\nb", "61 00 0d 00 0a 00 62 00 00 00", "a\nb"},
		{"greek and an emoji", "μ😀", "bc 03 3d d8 00 de 00 00", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EncodeText(nil, tt.text)
			if want := unhex(t, tt.wire); !bytes.Equal(got, want) {
				t.Errorf("encoded\n%x\nwant\n%x", got, want)
			}
			want := tt.back
			if want == "" {
				want = tt.text
			}
			if back := DecodeText(got); back != want {
				t.Errorf("decoded %q, want %q", back, want)
			}
		})
	}
	// What follows the zero is not text; a lone surrogate and an odd byte
	// do no harm.
	if got := DecodeText(unhex(t, "68 00 00 00 69 00")); got != "h" {
		t.Errorf("after the zero: %q", got)
	}
	if got := DecodeText(unhex(t, "3d d8 68 00 69")); got != "�h" {
		t.Errorf("a lone surrogate: %q", got)
	}
	if got := EncodeText([]byte{9}, "a"); !bytes.Equal(got, []byte{9, 'a', 0, 0, 0}) {
		t.Errorf("appended: %x", got)
	}
	if got := EncodeText8([]byte{9}, "aé\nμ"); !bytes.Equal(got, []byte{9, 'a', 0xe9, '\r', '\n', '?', 0}) {
		t.Errorf("8-bit text: %x", got)
	}
}
