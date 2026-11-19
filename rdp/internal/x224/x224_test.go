package x224

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/tpkt"
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

// msCR is the Connection Request of MS-RDPBCGR 4.1.1, TPKT included.
var msCR = []byte{
	0x03, 0x00, 0x00, 0x2c, 0x27, 0xe0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x43, 0x6f, 0x6f, 0x6b, 0x69,
	0x65, 0x3a, 0x20, 0x6d, 0x73, 0x74, 0x73, 0x68, 0x61, 0x73, 0x68, 0x3d, 0x65, 0x6c, 0x74, 0x6f,
	0x6e, 0x73, 0x0d, 0x0a, 0x01, 0x00, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00,
}

// msCC is the Connection Confirm of MS-RDPBCGR 4.1.2, TPKT included.
var msCC = []byte{
	0x03, 0x00, 0x00, 0x13, 0x0e, 0xd0, 0x00, 0x00, 0x12, 0x34, 0x00, 0x02, 0x00, 0x08, 0x00, 0x00,
	0x00, 0x00, 0x00,
}

// tpdu checks a whole frame's TPKT header and returns the TPDU in it.
func tpdu(t *testing.T, frame []byte) []byte {
	t.Helper()
	k, n, err := tpkt.FrameLength(frame)
	if err != nil {
		t.Fatal(err)
	}
	if k != tpkt.TPKT || n != len(frame) {
		t.Fatalf("frame %v of %d bytes, file has %d", k, n, len(frame))
	}
	return frame[tpkt.HeaderLen:]
}

// frameCR writes cr in a TPKT.
func frameCR(t *testing.T, cr *ConnectionRequest) []byte {
	t.Helper()
	var w wire.Writer
	off := tpkt.Begin(&w)
	if err := WriteConnectionRequest(&w, cr); err != nil {
		t.Fatal(err)
	}
	if err := tpkt.End(&w, off); err != nil {
		t.Fatal(err)
	}
	return w.Bytes()
}

func TestConnectionRequestMicrosoft(t *testing.T) {
	cr, err := ParseConnectionRequest(tpdu(t, msCR))
	if err != nil {
		t.Fatal(err)
	}
	if string(cr.Cookie) != "eltons" || cr.RoutingToken != nil || cr.SrcRef != 0 {
		t.Errorf("cookie %q, token %q, src-ref %#x", cr.Cookie, cr.RoutingToken, cr.SrcRef)
	}
	if !cr.HasNegReq || cr.Flags != 0 || cr.RequestedProtocols != ProtocolRDP || cr.HasCorrelation {
		t.Errorf("negotiation %+v", cr)
	}
	if got := frameCR(t, &cr); !bytes.Equal(got, msCR) {
		t.Errorf("written % x\nwant    % x", got, msCR)
	}
}

func TestConnectionRequestRecordings(t *testing.T) {
	tests := []struct {
		file      string
		negReq    bool
		protocols uint32
	}{
		{"cr-freerdp-default.bin", true, ProtocolSSL | ProtocolHybrid | ProtocolHybridEx},
		{"cr-freerdp-tls.bin", true, ProtocolSSL},
		{"cr-freerdp-nla.bin", true, ProtocolSSL | ProtocolHybrid},
		{"cr-freerdp-rdp.bin", false, 0},
		{"cr-remmina-tls.bin", true, ProtocolSSL},
		{"cr-remmina-auto.bin", true, ProtocolSSL | ProtocolHybrid | ProtocolHybridEx},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			frame, err := os.ReadFile(filepath.Join("..", "..", "testdata", "x224", tt.file))
			if err != nil {
				t.Fatal(err)
			}
			cr, err := ParseConnectionRequest(tpdu(t, frame))
			if err != nil {
				t.Fatal(err)
			}
			if string(cr.Cookie) != "mardp" || cr.RoutingToken != nil {
				t.Errorf("cookie %q, token %q", cr.Cookie, cr.RoutingToken)
			}
			if cr.HasNegReq != tt.negReq || cr.Flags != 0 || cr.RequestedProtocols != tt.protocols || cr.HasCorrelation {
				t.Errorf("negotiation %+v", cr)
			}
			if got := frameCR(t, &cr); !bytes.Equal(got, frame) {
				t.Errorf("written % x\nwant    % x", got, frame)
			}
		})
	}
}

func TestConnectionRequestForms(t *testing.T) {
	id := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	tests := []struct {
		name string
		cr   ConnectionRequest
	}{
		{"nothing after the fixed part", ConnectionRequest{SrcRef: 7}},
		{"negotiation only", ConnectionRequest{HasNegReq: true, Flags: ReqRestrictedAdminModeRequired, RequestedProtocols: ProtocolSSL}},
		{"routing token", ConnectionRequest{RoutingToken: []byte("Cookie: msts=3640205228.15629.0000"), HasNegReq: true, RequestedProtocols: ProtocolHybrid}},
		{"correlation info", ConnectionRequest{Cookie: []byte("u"), HasNegReq: true, Flags: ReqCorrelationInfoPresent, RequestedProtocols: ProtocolHybridEx, HasCorrelation: true, CorrelationID: id}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frame := frameCR(t, &tt.cr)
			got, err := ParseConnectionRequest(frame[tpkt.HeaderLen:])
			if err != nil {
				t.Fatal(err)
			}
			if got.SrcRef != tt.cr.SrcRef || !bytes.Equal(got.Cookie, tt.cr.Cookie) || !bytes.Equal(got.RoutingToken, tt.cr.RoutingToken) ||
				got.HasNegReq != tt.cr.HasNegReq || got.Flags != tt.cr.Flags || got.RequestedProtocols != tt.cr.RequestedProtocols ||
				got.HasCorrelation != tt.cr.HasCorrelation || got.CorrelationID != tt.cr.CorrelationID {
				t.Errorf("read back %+v\nwant      %+v", got, tt.cr)
			}
		})
	}
}

func TestConnectionConfirm(t *testing.T) {
	tests := []struct {
		name  string
		cc    ConnectionConfirm
		frame []byte
	}{
		{"Microsoft 4.1.2", ConnectionConfirm{SrcRef: 0x1234, Negotiation: NegResponse, SelectedProtocol: ProtocolRDP}, msCC},
		{"TLS with extended client data", ConnectionConfirm{Negotiation: NegResponse, Flags: RspExtendedClientDataSupported, SelectedProtocol: ProtocolSSL},
			[]byte{0x03, 0x00, 0x00, 0x13, 0x0e, 0xd0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0x01, 0x08, 0x00, 0x01, 0x00, 0x00, 0x00}},
		{"SSL required", ConnectionConfirm{Negotiation: NegFailure, FailureCode: FailureSSLRequiredByServer},
			[]byte{0x03, 0x00, 0x00, 0x13, 0x0e, 0xd0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x03, 0x00, 0x08, 0x00, 0x01, 0x00, 0x00, 0x00}},
		{"no negotiation", ConnectionConfirm{},
			[]byte{0x03, 0x00, 0x00, 0x0b, 0x06, 0xd0, 0x00, 0x00, 0x00, 0x00, 0x00}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w wire.Writer
			off := tpkt.Begin(&w)
			WriteConnectionConfirm(&w, &tt.cc)
			if err := tpkt.End(&w, off); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(w.Bytes(), tt.frame) {
				t.Errorf("written % x\nwant    % x", w.Bytes(), tt.frame)
			}
			got, err := ParseConnectionConfirm(tpdu(t, tt.frame))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.cc {
				t.Errorf("read %+v, want %+v", got, tt.cc)
			}
		})
	}
}

func TestDataHeader(t *testing.T) {
	var w wire.Writer
	WriteDataHeader(&w)
	w.U8(0x64) // the first byte of an MCS PDU
	if !bytes.Equal(w.Bytes(), []byte{0x02, 0xf0, 0x80, 0x64}) {
		t.Errorf("written % x", w.Bytes())
	}
	r := wire.NewReader(w.Bytes())
	if err := ReadDataHeader(&r); err != nil {
		t.Fatal(err)
	}
	if r.Offset() != DataHeaderLen {
		t.Errorf("left at %d", r.Offset())
	}
}

func TestDisconnectRequest(t *testing.T) {
	dr := DisconnectRequest{DstRef: 0x1234, SrcRef: 0, Reason: 0}
	want := []byte{0x06, 0x80, 0x12, 0x34, 0x00, 0x00, 0x00}
	var w wire.Writer
	WriteDisconnectRequest(&w, &dr)
	if !bytes.Equal(w.Bytes(), want) {
		t.Errorf("written % x", w.Bytes())
	}
	got, err := ParseDisconnectRequest(want)
	if err != nil {
		t.Fatal(err)
	}
	if got != dr {
		t.Errorf("read %+v", got)
	}
	// User data after the reason is skipped.
	got, err = ParseDisconnectRequest([]byte{0x08, 0x80, 0x00, 0x00, 0x00, 0x01, 0x02, 0xe0, 0x00})
	if err != nil || got.SrcRef != 1 || got.Reason != 2 {
		t.Errorf("with user data: %+v, %v", got, err)
	}
}

func TestCode(t *testing.T) {
	tests := []struct {
		tpdu []byte
		want uint8
	}{
		{msCR[4:], CodeConnectionRequest},
		{msCC[4:], CodeConnectionConfirm},
		{[]byte{0x02, 0xf0, 0x80}, CodeData},
		{[]byte{0x06, 0x80, 0, 0, 0, 0, 0}, CodeDisconnectRequest},
		{[]byte{0x06, 0xe5}, CodeConnectionRequest}, // credit bits
		{[]byte{0x04, 0x70}, 0x70},                  // error TPDU, left as it is
	}
	for _, tt := range tests {
		got, err := Code(tt.tpdu)
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("Code(% x) = %#x, want %#x", tt.tpdu, got, tt.want)
		}
	}
	_, err := Code([]byte{0x02})
	assertRoute(t, err, "Code->Reader.U8."+wire.TagTruncated)
	_, err = Code(nil)
	assertRoute(t, err, "Code->Reader.Skip."+wire.TagTruncated)
}

// cr builds a Connection Request TPDU: the fixed part with the right length
// indicator, then rest.
func cr(code, class byte, rest ...byte) []byte {
	b := append([]byte{0, code, 0, 0, 0, 0, class}, rest...)
	b[0] = byte(len(b) - 1)
	return b
}

func TestParseConnectionRequestErrors(t *testing.T) {
	neg := []byte{0x01, 0x00, 0x08, 0x00, 0x01, 0x00, 0x00, 0x00}
	negCorr := []byte{0x01, 0x08, 0x08, 0x00, 0x01, 0x00, 0x00, 0x00}
	corr := append([]byte{0x06, 0x00, 0x24, 0x00}, make([]byte, 32)...)
	const p = "ParseConnectionRequest"
	tests := []struct {
		name  string
		tpdu  []byte
		route string
	}{
		{"empty", nil, p + "->Reader.U8." + wire.TagTruncated},
		{"length indicator", []byte{0x05, 0xe0}, p + "." + TagLengthIndicatorMismatch},
		{"no code", []byte{0x00}, p + "->Reader.U8." + wire.TagTruncated},
		{"wrong code", cr(0xd0, 0), p + "." + TagUnexpectedCode},
		{"no dst-ref", []byte{0x02, 0xe0, 0x00}, p + "->Reader.U16BE." + wire.TagTruncated},
		{"no src-ref", []byte{0x04, 0xe0, 0, 0, 0}, p + "->Reader.U16BE." + wire.TagTruncated},
		{"no class", []byte{0x05, 0xe0, 0, 0, 0, 0}, p + "->Reader.U8." + wire.TagTruncated},
		{"class 2", cr(0xe0, 0x20), p + "." + TagClassUnsupported},
		{"cookie without CR LF", cr(0xe0, 0, []byte("Cookie: mstshash=x")...), p + "." + TagCookieUnterminated},
		{"not a negotiation request", cr(0xe0, 0, append([]byte("x\r\n"), 0x02)...), p + "." + TagNegotiationTypeUnknown},
		{"no flags", cr(0xe0, 0, 0x01), p + "->Reader.U8." + wire.TagTruncated},
		{"no length", cr(0xe0, 0, 0x01, 0x00, 0x08), p + "->Reader.U16LE." + wire.TagTruncated},
		{"length 9", cr(0xe0, 0, 0x01, 0x00, 0x09, 0x00, 0, 0, 0, 0), p + "." + TagNegotiationLengthInvalid},
		{"no protocols", cr(0xe0, 0, 0x01, 0x00, 0x08, 0x00, 0), p + "->Reader.U32LE." + wire.TagTruncated},
		{"correlation without flag", cr(0xe0, 0, append(neg, corr...)...), p + "." + TagCorrelationFlagMismatch},
		{"flag without correlation", cr(0xe0, 0, negCorr...), p + "." + TagCorrelationFlagMismatch},
		{"correlation of wrong type", cr(0xe0, 0, append(negCorr, append([]byte{0x07}, corr[1:]...)...)...), p + "." + TagNegotiationTypeUnknown},
		{"correlation no flags", cr(0xe0, 0, append(negCorr, 0x06)...), p + "->Reader.U8." + wire.TagTruncated},
		{"correlation no length", cr(0xe0, 0, append(negCorr, 0x06, 0x00)...), p + "->Reader.U16LE." + wire.TagTruncated},
		{"correlation length 35", cr(0xe0, 0, append(negCorr, 0x06, 0x00, 0x23, 0x00)...), p + "." + TagNegotiationLengthInvalid},
		{"correlation id truncated", cr(0xe0, 0, append(negCorr, corr[:10]...)...), p + "->Reader.Bytes." + wire.TagTruncated},
		{"correlation reserved truncated", cr(0xe0, 0, append(negCorr, corr[:30]...)...), p + "->Reader.Skip." + wire.TagTruncated},
		{"trailing bytes", cr(0xe0, 0, append(append(negCorr, corr...), 0xff)...), p + "." + TagTrailingBytes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseConnectionRequest(tt.tpdu)
			assertRoute(t, err, tt.route)
		})
	}
}

func TestWriteConnectionRequestErrors(t *testing.T) {
	const p = "WriteConnectionRequest"
	tests := []struct {
		name  string
		cr    ConnectionRequest
		route string
	}{
		{"CR in cookie", ConnectionRequest{Cookie: []byte("a\rb")}, p + "." + TagCookieInvalid},
		{"LF in token", ConnectionRequest{RoutingToken: []byte("a\nb")}, p + "." + TagCookieInvalid},
		{"too long", ConnectionRequest{Cookie: bytes.Repeat([]byte("a"), 240)}, p + "." + TagTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w wire.Writer
			assertRoute(t, WriteConnectionRequest(&w, &tt.cr), tt.route)
		})
	}
}

func TestParseConnectionConfirmErrors(t *testing.T) {
	const p = "ParseConnectionConfirm"
	tests := []struct {
		name  string
		tpdu  []byte
		route string
	}{
		{"empty", nil, p + "->Reader.U8." + wire.TagTruncated},
		{"length indicator", []byte{0x03, 0xd0}, p + "." + TagLengthIndicatorMismatch},
		{"no code", []byte{0x00}, p + "->Reader.U8." + wire.TagTruncated},
		{"wrong code", cr(0xe0, 0), p + "." + TagUnexpectedCode},
		{"no dst-ref", []byte{0x02, 0xd0, 0x00}, p + "->Reader.U16BE." + wire.TagTruncated},
		{"no src-ref", []byte{0x04, 0xd0, 0, 0, 0}, p + "->Reader.U16BE." + wire.TagTruncated},
		{"no class", []byte{0x05, 0xd0, 0, 0, 0, 0}, p + "->Reader.U8." + wire.TagTruncated},
		{"class 4", cr(0xd0, 0x40), p + "." + TagClassUnsupported},
		{"no flags", cr(0xd0, 0, 0x02), p + "->Reader.U8." + wire.TagTruncated},
		{"no length", cr(0xd0, 0, 0x02, 0x00), p + "->Reader.U16LE." + wire.TagTruncated},
		{"length 7", cr(0xd0, 0, 0x02, 0x00, 0x07, 0x00), p + "." + TagNegotiationLengthInvalid},
		{"no value", cr(0xd0, 0, 0x02, 0x00, 0x08, 0x00, 0x01), p + "->Reader.U32LE." + wire.TagTruncated},
		{"request type", cr(0xd0, 0, 0x01, 0x00, 0x08, 0x00, 0, 0, 0, 0), p + "." + TagNegotiationTypeUnknown},
		{"trailing bytes", cr(0xd0, 0, 0x02, 0x00, 0x08, 0x00, 0, 0, 0, 0, 0xff), p + "." + TagTrailingBytes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseConnectionConfirm(tt.tpdu)
			assertRoute(t, err, tt.route)
		})
	}
}

func TestReadDataHeaderErrors(t *testing.T) {
	const p = "ReadDataHeader"
	tests := []struct {
		name  string
		b     []byte
		route string
	}{
		{"empty", nil, p + "->Reader.U8." + wire.TagTruncated},
		{"length indicator", []byte{0x03, 0xf0, 0x80}, p + "." + TagLengthIndicatorMismatch},
		{"no code", []byte{0x02}, p + "->Reader.U8." + wire.TagTruncated},
		{"wrong code", []byte{0x02, 0xe0, 0x80}, p + "." + TagUnexpectedCode},
		{"no EOT byte", []byte{0x02, 0xf0}, p + "->Reader.U8." + wire.TagTruncated},
		{"EOT missing", []byte{0x02, 0xf0, 0x00}, p + "." + TagEOTMissing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := wire.NewReader(tt.b)
			assertRoute(t, ReadDataHeader(&r), tt.route)
		})
	}
}

func TestParseDisconnectRequestErrors(t *testing.T) {
	const p = "ParseDisconnectRequest"
	tests := []struct {
		name  string
		tpdu  []byte
		route string
	}{
		{"empty", nil, p + "->Reader.U8." + wire.TagTruncated},
		{"length indicator", []byte{0x06, 0x80, 0, 0}, p + "." + TagLengthIndicatorMismatch},
		{"too short", []byte{0x04, 0x80, 0, 0, 0}, p + "." + TagLengthIndicatorMismatch},
		{"wrong code", []byte{0x06, 0x70, 0, 0, 0, 0, 0}, p + "." + TagUnexpectedCode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseDisconnectRequest(tt.tpdu)
			assertRoute(t, err, tt.route)
		})
	}
}

func TestNoAllocations(t *testing.T) {
	buf := make([]byte, 0, 128)
	confirm := ConnectionConfirm{Negotiation: NegResponse, Flags: RspExtendedClientDataSupported, SelectedProtocol: ProtocolSSL}
	tests := []struct {
		name string
		run  func()
	}{
		{"parse request", func() {
			if _, err := ParseConnectionRequest(msCR[4:]); err != nil {
				t.Fatal(err)
			}
		}},
		{"write confirm", func() {
			w := wire.NewWriter(buf)
			off := tpkt.Begin(&w)
			WriteConnectionConfirm(&w, &confirm)
			if err := tpkt.End(&w, off); err != nil {
				t.Fatal(err)
			}
		}},
		{"data header", func() {
			w := wire.NewWriter(buf)
			WriteDataHeader(&w)
			r := wire.NewReader(w.Bytes())
			if err := ReadDataHeader(&r); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if n := testing.AllocsPerRun(100, tt.run); n != 0 {
				t.Errorf("%v allocations per run, want 0", n)
			}
		})
	}
}
