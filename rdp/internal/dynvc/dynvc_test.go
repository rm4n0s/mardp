package dynvc

import (
	"bytes"
	"encoding/hex"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func checkRoute(t *testing.T, err error, route string) {
	t.Helper()
	appErr, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("expected *errors.Error, got %T %v", err, err)
	}
	if !appErr.HasRoute(route) {
		t.Errorf("unexpected failure path: %s, want %s", appErr.Route(), route)
	}
}

// TestMicrosoftExamples reads the annotated PDUs of MS-RDPEDYC 4.1 to 4.4
// and writes them again.
func TestMicrosoftExamples(t *testing.T) {
	// 4.1.1: the example sets Sp, which the server SHOULD leave at 0.
	p, err := ParseServer(unhex(t, "58 00 02 00 33 33 11 11 3d 0a a7 04"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Cmd != CmdCapabilities || p.Sp != 2 || p.Version != Version2 || p.PriorityCharges != [4]uint16{13107, 4369, 2621, 1191} {
		t.Errorf("4.1.1: %+v", p)
	}
	w := wire.NewWriter(nil)
	if err := AppendCaps(&w, Version2, p.PriorityCharges); err != nil {
		t.Fatal(err)
	}
	if want := unhex(t, "50 00 02 00 33 33 11 11 3d 0a a7 04"); !bytes.Equal(w.Bytes(), want) {
		t.Errorf("4.1.1 written: % x", w.Bytes())
	}

	exact := []struct {
		name  string
		bytes string
		want  PDU
		write func(w *wire.Writer) error
		parse func([]byte) (PDU, error)
	}{
		{"4.1.2", "50 00 02 00", PDU{Cmd: CmdCapabilities, Version: Version2},
			func(w *wire.Writer) error { return AppendCapsResponse(w, Version2) }, ParseClient},
		{"4.2.1", "10 03 74 65 73 74 64 76 63 00", PDU{Cmd: CmdCreate, ChannelID: 3, Name: "testdvc"},
			func(w *wire.Writer) error { return AppendCreateRequest(w, 3, 0, "testdvc") }, ParseServer},
		{"4.2.2", "10 03 00 00 00 00", PDU{Cmd: CmdCreate, ChannelID: 3},
			func(w *wire.Writer) error { AppendCreateResponse(w, 3, 0); return nil }, ParseClient},
		{"4.4.1 from the server", "40 03", PDU{Cmd: CmdClose, ChannelID: 3},
			func(w *wire.Writer) error { AppendClose(w, 3); return nil }, ParseServer},
		{"4.4.1 from the client", "40 03", PDU{Cmd: CmdClose, ChannelID: 3},
			func(w *wire.Writer) error { AppendClose(w, 3); return nil }, ParseClient},
	}
	for _, tc := range exact {
		b := unhex(t, tc.bytes)
		p, err := tc.parse(b)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !equalPDU(p, tc.want) {
			t.Errorf("%s: %+v, want %+v", tc.name, p, tc.want)
		}
		w := wire.NewWriter(nil)
		if err := tc.write(&w); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(w.Bytes(), b) {
			t.Errorf("%s written: % x, want % x", tc.name, w.Bytes(), b)
		}
	}

	// 4.3.1 and 4.3.2: a message of 3,195 bytes on channel 3 is a Data
	// First with 1,596 bytes, a Data with 1,598 and a Data with 1. The
	// example's Data PDUs set Sp to 1, which the sender SHOULD leave at 0.
	msg := bytes.Repeat([]byte{0x71}, 3195)
	examples := [][]byte{
		append(unhex(t, "24 03 7b 0c"), msg[:1596]...),
		append(unhex(t, "34 03"), msg[:1598]...),
		unhex(t, "34 03 71"),
	}
	var pdus [][]byte
	w = wire.NewWriter(nil)
	if err := Split(&w, 3, msg, func(b []byte) error { pdus = append(pdus, slices.Clone(b)); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(pdus) != len(examples) {
		t.Fatalf("%d PDUs, want %d", len(pdus), len(examples))
	}
	j := Joiner{Max: 1 << 20}
	for i, ex := range examples {
		want := slices.Clone(ex)
		if i > 0 {
			want[0] &^= 0x0c
		}
		if !bytes.Equal(pdus[i], want) {
			t.Errorf("PDU %d: % x, want % x", i, pdus[i][:4], want[:4])
		}
		p, err := ParseClient(ex)
		if err != nil {
			t.Fatal(err)
		}
		got, ok, err := j.Add(p)
		if err != nil {
			t.Fatal(err)
		}
		if ok != (i == len(examples)-1) {
			t.Fatalf("PDU %d completes the message: %v", i, ok)
		}
		if ok && !bytes.Equal(got, msg) {
			t.Errorf("joined %d bytes", len(got))
		}
	}
}

func equalPDU(a, b PDU) bool {
	return a.Cmd == b.Cmd && a.Sp == b.Sp && a.ChannelID == b.ChannelID && a.Version == b.Version &&
		a.PriorityCharges == b.PriorityCharges && a.Name == b.Name && a.Status == b.Status &&
		a.Length == b.Length && bytes.Equal(a.Data, b.Data)
}

// TestCapsVersions writes and reads capabilities of each version.
func TestCapsVersions(t *testing.T) {
	charges := [4]uint16{936, 3276, 9362, 21845}
	for _, tc := range []struct {
		version uint16
		bytes   string
	}{
		{Version1, "50 00 01 00"},
		{Version2, "50 00 02 00 a8 03 cc 0c 92 24 55 55"},
		{Version3, "50 00 03 00 a8 03 cc 0c 92 24 55 55"},
	} {
		w := wire.NewWriter(nil)
		if err := AppendCaps(&w, tc.version, charges); err != nil {
			t.Fatal(err)
		}
		if want := unhex(t, tc.bytes); !bytes.Equal(w.Bytes(), want) {
			t.Errorf("version %d: % x, want % x", tc.version, w.Bytes(), want)
		}
		p, err := ParseServer(w.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		want := PDU{Cmd: CmdCapabilities, Version: tc.version}
		if tc.version > Version1 {
			want.PriorityCharges = charges
		}
		if !equalPDU(p, want) {
			t.Errorf("version %d read: %+v", tc.version, p)
		}
		w.Reset()
		if err := AppendCapsResponse(&w, tc.version); err != nil {
			t.Fatal(err)
		}
		r, err := ParseClient(w.Bytes())
		if err != nil || r.Version != tc.version || w.Len() != 4 {
			t.Errorf("response of version %d: %+v %v", tc.version, r, err)
		}
	}
}

// TestLengthForms writes and reads every size of ChannelId and of a Data
// First's Length.
func TestLengthForms(t *testing.T) {
	data := []byte{1, 2, 3}
	for _, id := range []struct {
		id   uint32
		cbID uint8
	}{{3, 0}, {0x1234, 1}, {0x12345678, 2}} {
		for _, total := range []struct {
			total uint32
			len   uint8
		}{{200, 0}, {3195, 1}, {70000, 2}} {
			w := wire.NewWriter(nil)
			AppendDataFirst(&w, id.id, total.total, data)
			b := w.Bytes()
			if want := CmdDataFirst<<4 | total.len<<2 | id.cbID; b[0] != want {
				t.Errorf("id %#x, total %d: header %#x, want %#x", id.id, total.total, b[0], want)
			}
			if n := 1 + codeLen(id.cbID) + codeLen(total.len) + len(data); len(b) != n {
				t.Errorf("id %#x, total %d: %d bytes, want %d", id.id, total.total, len(b), n)
			}
			for _, parse := range []func([]byte) (PDU, error){ParseClient, ParseServer} {
				p, err := parse(b)
				if err != nil {
					t.Fatal(err)
				}
				want := PDU{Cmd: CmdDataFirst, Sp: total.len, ChannelID: id.id, Length: total.total, Data: data}
				if !equalPDU(p, want) {
					t.Errorf("read %+v, want %+v", p, want)
				}
			}
		}
		for _, pdu := range []struct {
			cmd   uint8
			write func(w *wire.Writer)
		}{
			{CmdData, func(w *wire.Writer) { AppendData(w, id.id, data) }},
			{CmdClose, func(w *wire.Writer) { AppendClose(w, id.id) }},
			{CmdCreate, func(w *wire.Writer) { AppendCreateResponse(w, id.id, -2147467259) }},
		} {
			w := wire.NewWriter(nil)
			pdu.write(&w)
			if w.Bytes()[0] != pdu.cmd<<4|id.cbID {
				t.Errorf("cmd %d, id %#x: header %#x", pdu.cmd, id.id, w.Bytes()[0])
			}
			p, err := ParseClient(w.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if p.Cmd != pdu.cmd || p.ChannelID != id.id {
				t.Errorf("read %+v", p)
			}
			if pdu.cmd == CmdCreate && p.Status != -2147467259 {
				t.Errorf("status %#x", p.Status)
			}
			if pdu.cmd == CmdData && !bytes.Equal(p.Data, data) {
				t.Errorf("data % x", p.Data)
			}
		}
		w := wire.NewWriter(nil)
		if err := AppendCreateRequest(&w, id.id, 2, "Microsoft::Windows::RDS::Graphics"); err != nil {
			t.Fatal(err)
		}
		p, err := ParseServer(w.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if p.Cmd != CmdCreate || p.Sp != 2 || p.ChannelID != id.id || p.Name != "Microsoft::Windows::RDS::Graphics" {
			t.Errorf("create request read: %+v", p)
		}
	}
}

// TestSplitJoin splits messages of every size around the PDU's limits on
// channels of each ChannelId size and joins them again.
func TestSplitJoin(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	sizes := []int{0, 1, 1590, 1595, 1596, 1597, 1598, 1599, 1600, 3195, 3196, 70000}
	for range 50 {
		sizes = append(sizes, rng.IntN(20000))
	}
	for _, id := range []uint32{3, 0x1234, 0x12345678} {
		for _, n := range sizes {
			msg := make([]byte, n)
			for i := range msg {
				msg[i] = byte(rng.Uint32())
			}
			j := Joiner{Max: 1 << 20}
			var got []byte
			count := 0
			w := wire.NewWriter(nil)
			err := Split(&w, id, msg, func(b []byte) error {
				if len(b) > MaxPDU {
					t.Fatalf("id %#x, %d bytes: a PDU of %d bytes", id, n, len(b))
				}
				p, err := ParseClient(b)
				if err != nil {
					return err
				}
				if p.ChannelID != id || (count == 0) != (p.Cmd == CmdDataFirst || dataHeader(id)+n <= MaxPDU) {
					t.Fatalf("id %#x, %d bytes: PDU %d is %+v", id, n, count, p.Cmd)
				}
				count++
				m, ok, err := j.Add(p)
				if ok {
					got = slices.Clone(m)
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, msg) {
				t.Errorf("id %#x, %d bytes: joined %d bytes in %d PDUs", id, n, len(got), count)
			}
			if dataHeader(id)+n <= MaxPDU && count != 1 {
				t.Errorf("id %#x, %d bytes: %d PDUs for what fits in one", id, n, count)
			}
		}
	}
}

func dataHeader(id uint32) int { return 1 + codeLen(sizeCode(id)) }

func TestSplitFails(t *testing.T) {
	stop := &errors.Error{Tag: "Stop"}
	for _, n := range []int{10, 5000} {
		calls := 0
		w := wire.NewWriter(nil)
		err := Split(&w, 1, make([]byte, n), func([]byte) error { calls++; return stop })
		if !errors.Is(err, stop) || calls != 1 {
			t.Errorf("%d bytes: %v after %d calls", n, err, calls)
		}
	}
	calls := 0
	w := wire.NewWriter(nil)
	err := Split(&w, 1, make([]byte, 5000), func([]byte) error {
		calls++
		if calls == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || calls != 2 {
		t.Errorf("a later PDU: %v after %d calls", err, calls)
	}
}

// TestJoiner covers what Split never makes: a whole message in a Data
// First, and the failures.
func TestJoiner(t *testing.T) {
	j := Joiner{Max: 10}
	m, ok, err := j.Add(PDU{Cmd: CmdDataFirst, Length: 3, Data: []byte{1, 2, 3}})
	if err != nil || !ok || !bytes.Equal(m, []byte{1, 2, 3}) {
		t.Errorf("a whole Data First: % x %v %v", m, ok, err)
	}
	if m, ok, err = j.Add(PDU{Cmd: CmdDataFirst, Length: 0}); err != nil || !ok || len(m) != 0 {
		t.Errorf("an empty Data First: % x %v %v", m, ok, err)
	}

	const add = "Joiner.Add."
	for _, tc := range []struct {
		name  string
		pdus  []PDU
		route string
	}{
		{"a Data First in a message", []PDU{
			{Cmd: CmdDataFirst, Length: 5, Data: []byte{1}},
			{Cmd: CmdDataFirst, Length: 5, Data: []byte{1}},
		}, add + TagSequence},
		{"too long", []PDU{{Cmd: CmdDataFirst, Length: 11}}, add + TagMessageTooLarge},
		{"a Data too long", []PDU{{Cmd: CmdData, Data: make([]byte, 11)}}, add + TagMessageTooLarge},
		{"more data than the total", []PDU{{Cmd: CmdDataFirst, Length: 2, Data: []byte{1, 2, 3}}}, add + TagLengthInvalid},
		{"data beyond the total", []PDU{
			{Cmd: CmdDataFirst, Length: 4, Data: []byte{1, 2}},
			{Cmd: CmdData, Data: []byte{3, 4, 5}},
		}, add + TagLengthInvalid},
		{"a close", []PDU{{Cmd: CmdClose}}, add + TagCommandUnknown},
	} {
		j := Joiner{Max: 10}
		var err error
		for _, p := range tc.pdus {
			if _, _, err = j.Add(p); err != nil {
				break
			}
		}
		if err == nil {
			t.Errorf("%s: no error", tc.name)
			continue
		}
		checkRoute(t, err, tc.route)
		// The joiner starts over after a failure.
		if m, ok, err := j.Add(PDU{Cmd: CmdData, Data: []byte{9}}); err != nil || !ok || !bytes.Equal(m, []byte{9}) {
			t.Errorf("%s: after it % x %v %v", tc.name, m, ok, err)
		}
	}
}

func TestParseFails(t *testing.T) {
	client, server := "ParseClient->", "ParseServer->"
	for _, tc := range []struct {
		name   string
		bytes  string
		server bool
		route  string
	}{
		{"empty", "", false, client + "readHeader->Reader.U8." + wire.TagTruncated},
		{"cbId 3", "33 03", false, client + "parseCommon->readID." + TagChannelIDSizeInvalid},
		{"cbId 3 in a create request", "13 03 41 00", true, server + "readID." + TagChannelIDSizeInvalid},
		{"Len 3", "2c 03 01", false, client + "parseCommon." + TagLengthSizeInvalid},
		{"no length", "24 03 01", false, client + "parseCommon->Reader.U16LE." + wire.TagTruncated},
		{"a soft-sync response", "90 00", false, client + "parseCommon." + TagCommandUnknown},
		{"a soft-sync request", "80 00", true, server + "parseCommon." + TagCommandUnknown},
		{"data first compressed", "60 03 00", false, client + "parseCommon." + TagCompressed},
		{"data compressed", "70 03 00", true, server + "parseCommon." + TagCompressed},
		{"version 4", "50 00 04 00", false, client + "readVersion." + TagVersionInvalid},
		{"version 0", "50 00 00 00", true, server + "readVersion." + TagVersionInvalid},
		{"empty from the server", "", true, server + "readHeader->Reader.U8." + wire.TagTruncated},
		{"a version cut", "50 00 01", true, server + "readVersion->Reader.U16LE." + wire.TagTruncated},
		{"no pad", "50", false, client + "readVersion->Reader.Skip." + wire.TagTruncated},
		{"no charges", "50 00 02 00 01 00", true, server + "Reader.U16LE." + wire.TagTruncated},
		{"no status", "10 03 00 00", false, client + "Reader.U32LE." + wire.TagTruncated},
		{"no channel", "10", false, client + "readID->Reader.U8." + wire.TagTruncated},
		{"a name without NUL", "10 03 41 42", true, "ParseServer." + TagNameInvalid},
		{"no name", "10 03", true, "ParseServer." + TagNameInvalid},
		{"close without channel", "41 03", false, client + "parseCommon->readID->Reader.U16LE." + wire.TagTruncated},
		{"data without channel", "32", false, client + "parseCommon->readID->Reader.U32LE." + wire.TagTruncated},
		{"data first without channel", "20", false, client + "parseCommon->readID->Reader.U8." + wire.TagTruncated},
		{"a 1-byte length cut", "20 03", false, client + "parseCommon->Reader.U8." + wire.TagTruncated},
		{"a 4-byte length cut", "28 03 01 02", false, client + "parseCommon->Reader.U32LE." + wire.TagTruncated},
	} {
		parse := ParseClient
		if tc.server {
			parse = ParseServer
		}
		_, err := parse(unhex(t, tc.bytes))
		if err == nil {
			t.Errorf("%s: no error", tc.name)
			continue
		}
		checkRoute(t, err, tc.route)
	}
	// A name with a NUL before the last stops at it.
	p, err := ParseServer(unhex(t, "10 03 41 00 42 00"))
	if err != nil || p.Name != "A" {
		t.Errorf("a name with two NULs: %q %v", p.Name, err)
	}
}

func TestWriteFails(t *testing.T) {
	w := wire.NewWriter(nil)
	for _, tc := range []struct {
		name  string
		err   error
		route string
	}{
		{"caps of version 4", AppendCaps(&w, 4, [4]uint16{}), "AppendCaps." + TagVersionInvalid},
		{"caps of version 0", AppendCaps(&w, 0, [4]uint16{}), "AppendCaps." + TagVersionInvalid},
		{"a response of version 4", AppendCapsResponse(&w, 4), "AppendCapsResponse." + TagVersionInvalid},
		{"a name with a NUL", AppendCreateRequest(&w, 1, 0, "a\x00b"), "AppendCreateRequest." + TagNameInvalid},
		{"a name too long", AppendCreateRequest(&w, 1, 0, strings.Repeat("a", MaxPDU-2)), "AppendCreateRequest." + TagNameInvalid},
	} {
		if tc.err == nil {
			t.Errorf("%s: no error", tc.name)
			continue
		}
		checkRoute(t, tc.err, tc.route)
	}
	if w.Len() != 0 {
		t.Errorf("failed writes wrote % x", w.Bytes())
	}
	if err := AppendCreateRequest(&w, 1, 0, strings.Repeat("a", MaxPDU-3)); err != nil || w.Len() != MaxPDU {
		t.Errorf("the longest name: %d bytes, %v", w.Len(), err)
	}
}
