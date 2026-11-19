package mcs

import (
	"bytes"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/tpkt"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/internal/x224"
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

// payload reads one of Microsoft's examples and returns what follows its
// TPKT and X.224 Data headers.
func payload(t *testing.T, name string) []byte {
	t.Helper()
	frame, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ms-rdpbcgr", name))
	if err != nil {
		t.Fatal(err)
	}
	return unframe(t, frame)
}

func unframe(t *testing.T, frame []byte) []byte {
	t.Helper()
	k, n, err := tpkt.FrameLength(frame)
	if err != nil || k != tpkt.TPKT || n != len(frame) {
		t.Fatalf("frame %v %d of %d bytes: %v", k, n, len(frame), err)
	}
	r := wire.NewReader(frame[tpkt.HeaderLen:])
	if err := x224.ReadDataHeader(&r); err != nil {
		t.Fatal(err)
	}
	return r.Rest()
}

func TestConnectInitialMicrosoft(t *testing.T) {
	b := payload(t, "connect-initial.bin")
	ci, err := ParseConnectInitial(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ci.CallingDomainSelector, []byte{1}) || !bytes.Equal(ci.CalledDomainSelector, []byte{1}) || !ci.UpwardFlag {
		t.Errorf("selectors % x, % x, upward %v", ci.CallingDomainSelector, ci.CalledDomainSelector, ci.UpwardFlag)
	}
	params := []struct {
		name      string
		got, want DomainParameters
	}{
		{"target", ci.Target, DomainParameters{34, 2, 0, 1, 0, 1, 65535, 2}},
		{"minimum", ci.Minimum, DomainParameters{1, 1, 1, 1, 0, 1, 1056, 2}},
		{"maximum", ci.Maximum, DomainParameters{65535, 64535, 65535, 1, 0, 1, 65535, 2}},
	}
	for _, p := range params {
		if p.got != p.want {
			t.Errorf("%s %+v, want %+v", p.name, p.got, p.want)
		}
	}
	if len(ci.UserData) != 307 || !bytes.HasPrefix(ci.UserData, []byte{0x00, 0x05, 0x00, 0x14, 0x7c, 0x00, 0x01}) {
		t.Errorf("user data of %d bytes: % x...", len(ci.UserData), ci.UserData[:8])
	}
	// Written back in strict BER: the five integers Microsoft wrote with
	// their top bit set take one byte more, and the lengths around them grow.
	head := []byte{
		0x7f, 0x65, 0x82, 0x01, 0x99,
		0x04, 0x01, 0x01, 0x04, 0x01, 0x01, 0x01, 0x01, 0xff,
		0x30, 0x1a, 0x02, 0x01, 0x22, 0x02, 0x01, 0x02, 0x02, 0x01, 0x00, 0x02, 0x01, 0x01, 0x02, 0x01, 0x00,
		0x02, 0x01, 0x01, 0x02, 0x03, 0x00, 0xff, 0xff, 0x02, 0x01, 0x02,
		0x30, 0x19, 0x02, 0x01, 0x01, 0x02, 0x01, 0x01, 0x02, 0x01, 0x01, 0x02, 0x01, 0x01, 0x02, 0x01, 0x00,
		0x02, 0x01, 0x01, 0x02, 0x02, 0x04, 0x20, 0x02, 0x01, 0x02,
		0x30, 0x20, 0x02, 0x03, 0x00, 0xff, 0xff, 0x02, 0x03, 0x00, 0xfc, 0x17, 0x02, 0x03, 0x00, 0xff, 0xff,
		0x02, 0x01, 0x01, 0x02, 0x01, 0x00, 0x02, 0x01, 0x01, 0x02, 0x03, 0x00, 0xff, 0xff, 0x02, 0x01, 0x02,
	}
	const userDataAt = 0x69 - 7 // offset of 04 82 01 33 after the TPKT and X.224 headers
	want := append(bytes.Clone(head), b[userDataAt:]...)
	var w wire.Writer
	if err := WriteConnectInitial(&w, &ci); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Bytes(), want) {
		t.Errorf("written % x\nwant    % x", w.Bytes(), want)
	}
}

func TestConnectResponseMicrosoft(t *testing.T) {
	b := payload(t, "connect-response.bin")
	cr, err := ParseConnectResponse(b)
	if err != nil {
		t.Fatal(err)
	}
	if cr.Result != ResultSuccessful || cr.CalledConnectID != 0 {
		t.Errorf("result %d, connect id %d", cr.Result, cr.CalledConnectID)
	}
	if want := (DomainParameters{34, 3, 0, 1, 0, 1, 65528, 2}); cr.Parameters != want {
		t.Errorf("parameters %+v, want %+v", cr.Parameters, want)
	}
	if len(cr.UserData) != 287 || !bytes.HasPrefix(cr.UserData, []byte{0x00, 0x05, 0x00, 0x14, 0x7c, 0x00, 0x01, 0x2a}) {
		t.Errorf("user data of %d bytes", len(cr.UserData))
	}
	var w wire.Writer
	if err := WriteConnectResponse(&w, &cr); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Bytes(), b) {
		t.Errorf("written % x\nwant    % x", w.Bytes(), b)
	}
}

func TestConnectRoundTrip(t *testing.T) {
	ci := ConnectInitial{
		CallingDomainSelector: []byte{1}, CalledDomainSelector: []byte{1},
		Target:   DomainParameters{0xffffffff, 0x80, 0x7f, 0x8000, 0, 1, 0x800000, 2},
		UserData: bytes.Repeat([]byte{0xab}, 20),
	}
	var w wire.Writer
	if err := WriteConnectInitial(&w, &ci); err != nil {
		t.Fatal(err)
	}
	got, err := ParseConnectInitial(w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got.Target != ci.Target || got.UpwardFlag || !bytes.Equal(got.UserData, ci.UserData) {
		t.Errorf("read back %+v", got)
	}
	cr := ConnectResponse{Result: ResultUserRejected, CalledConnectID: 70000, Parameters: ci.Target}
	w.Reset()
	if err := WriteConnectResponse(&w, &cr); err != nil {
		t.Fatal(err)
	}
	gotR, err := ParseConnectResponse(w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if gotR.Result != cr.Result || gotR.CalledConnectID != cr.CalledConnectID || gotR.Parameters != cr.Parameters || len(gotR.UserData) != 0 {
		t.Errorf("read back %+v", gotR)
	}
}

// pdu is one domain PDU of each kind with its bytes, read and written by the
// functions under test.
type pdu struct {
	name  string
	enc   []byte
	read  func(r *wire.Reader) (any, error)
	write func(w *wire.Writer) error
	want  any
}

var userData412 = bytes.Repeat([]byte{0x5a}, 412)

var licenseData = []byte{
	0x88, 0x02, 0x02, 0x03, 0x8d, 0x43, 0x9a, 0xab, 0xd5, 0x2a, 0x31, 0x39, 0x62, 0x4d,
	0xc1, 0xec, 0x0d, 0x99, 0x88, 0xe6, 0xda, 0xab, 0x2c, 0x02, 0x72, 0x4d, 0x49, 0x90,
}

func domainPDUs() []pdu {
	return []pdu{
		// MS-RDPBCGR 4.1.5.
		{"Erect Domain Request", []byte{0x04, 0x01, 0x00, 0x01, 0x00},
			func(r *wire.Reader) (any, error) { return ReadErectDomainRequest(r) },
			func(w *wire.Writer) error { WriteErectDomainRequest(w, &ErectDomainRequest{}); return nil },
			ErectDomainRequest{}},
		{"Erect Domain Request, large", []byte{0x04, 0x02, 0x01, 0x00, 0x04, 0xff, 0xff, 0xff, 0xff},
			func(r *wire.Reader) (any, error) { return ReadErectDomainRequest(r) },
			func(w *wire.Writer) error {
				WriteErectDomainRequest(w, &ErectDomainRequest{SubHeight: 256, SubInterval: 0xffffffff})
				return nil
			},
			ErectDomainRequest{SubHeight: 256, SubInterval: 0xffffffff}},
		// MS-RDPBCGR 4.1.6.
		{"Attach User Request", []byte{0x28},
			func(r *wire.Reader) (any, error) { return nil, ReadAttachUserRequest(r) },
			func(w *wire.Writer) error { WriteAttachUserRequest(w); return nil },
			nil},
		// MS-RDPBCGR 4.1.7.
		{"Attach User Confirm", []byte{0x2e, 0x00, 0x00, 0x06},
			func(r *wire.Reader) (any, error) { return ReadAttachUserConfirm(r) },
			func(w *wire.Writer) error {
				return WriteAttachUserConfirm(w, &AttachUserConfirm{HasInitiator: true, Initiator: 1007})
			},
			AttachUserConfirm{HasInitiator: true, Initiator: 1007}},
		{"Attach User Confirm, too many users", []byte{0x2d, 0xa0},
			func(r *wire.Reader) (any, error) { return ReadAttachUserConfirm(r) },
			func(w *wire.Writer) error {
				return WriteAttachUserConfirm(w, &AttachUserConfirm{Result: ResultTooManyUsers})
			},
			AttachUserConfirm{Result: ResultTooManyUsers}},
		// MS-RDPBCGR 4.1.8, the request for the I/O channel.
		{"Channel Join Request", []byte{0x38, 0x00, 0x06, 0x03, 0xeb},
			func(r *wire.Reader) (any, error) { return ReadChannelJoinRequest(r) },
			func(w *wire.Writer) error {
				return WriteChannelJoinRequest(w, &ChannelJoinRequest{Initiator: 1007, ChannelID: 1003})
			},
			ChannelJoinRequest{Initiator: 1007, ChannelID: 1003}},
		// MS-RDPBCGR 4.1.8, the confirm for the user channel.
		{"Channel Join Confirm", []byte{0x3e, 0x00, 0x00, 0x06, 0x03, 0xef, 0x03, 0xef},
			func(r *wire.Reader) (any, error) { return ReadChannelJoinConfirm(r) },
			func(w *wire.Writer) error {
				return WriteChannelJoinConfirm(w, &ChannelJoinConfirm{Initiator: 1007, Requested: 1007, HasChannelID: true, ChannelID: 1007})
			},
			ChannelJoinConfirm{Initiator: 1007, Requested: 1007, HasChannelID: true, ChannelID: 1007}},
		{"Channel Join Confirm, no such channel", []byte{0x3c, 0x60, 0x00, 0x06, 0x03, 0xf0},
			func(r *wire.Reader) (any, error) { return ReadChannelJoinConfirm(r) },
			func(w *wire.Writer) error {
				return WriteChannelJoinConfirm(w, &ChannelJoinConfirm{Result: ResultNoSuchChannel, Initiator: 1007, Requested: 1008})
			},
			ChannelJoinConfirm{Result: ResultNoSuchChannel, Initiator: 1007, Requested: 1008}},
		// MS-RDPBCGR 4.1.10, the head of the Client Info PDU (its 412 bytes
		// of user data stand in here).
		{"Send Data Request", append([]byte{0x64, 0x00, 0x06, 0x03, 0xeb, 0x70, 0x81, 0x9c}, userData412...),
			func(r *wire.Reader) (any, error) { return ReadSendDataRequest(r) },
			func(w *wire.Writer) error {
				return WriteSendDataRequest(w, &SendData{Initiator: 1007, ChannelID: 1003, Priority: PriorityHigh, Segmentation: SegmentationWhole, Data: userData412})
			},
			SendData{Initiator: 1007, ChannelID: 1003, Priority: PriorityHigh, Segmentation: SegmentationWhole, Data: userData412}},
		// MS-RDPBCGR 4.1.11, the License Error PDU.
		{"Send Data Indication", append([]byte{0x68, 0x00, 0x01, 0x03, 0xeb, 0x70, 0x1c}, licenseData...),
			func(r *wire.Reader) (any, error) { return ReadSendDataIndication(r) },
			func(w *wire.Writer) error {
				return WriteSendDataIndication(w, &SendData{Initiator: 1002, ChannelID: 1003, Priority: PriorityHigh, Segmentation: SegmentationWhole, Data: licenseData})
			},
			SendData{Initiator: 1002, ChannelID: 1003, Priority: PriorityHigh, Segmentation: SegmentationWhole, Data: licenseData}},
		// T.125: choice 8 in six bits, then rn-user-requested (3) in three.
		{"Disconnect Provider Ultimatum", []byte{0x21, 0x80},
			func(r *wire.Reader) (any, error) { return ReadDisconnectProviderUltimatum(r) },
			func(w *wire.Writer) error { return WriteDisconnectProviderUltimatum(w, ReasonUserRequested) },
			ReasonUserRequested},
		{"Disconnect Provider Ultimatum, channel purged", []byte{0x22, 0x00},
			func(r *wire.Reader) (any, error) { return ReadDisconnectProviderUltimatum(r) },
			func(w *wire.Writer) error { return WriteDisconnectProviderUltimatum(w, ReasonChannelPurged) },
			ReasonChannelPurged},
	}
}

func equal(a, b any) bool {
	da, ok := a.(SendData)
	if !ok {
		return a == b
	}
	db := b.(SendData)
	return da.Initiator == db.Initiator && da.ChannelID == db.ChannelID && da.Priority == db.Priority &&
		da.Segmentation == db.Segmentation && bytes.Equal(da.Data, db.Data)
}

func TestDomainPDUs(t *testing.T) {
	for _, tt := range domainPDUs() {
		t.Run(tt.name, func(t *testing.T) {
			r := wire.NewReader(tt.enc)
			got, err := tt.read(&r)
			if err != nil {
				t.Fatal(err)
			}
			if !equal(got, tt.want) {
				t.Errorf("read %+v, want %+v", got, tt.want)
			}
			var w wire.Writer
			if err := tt.write(&w); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(w.Bytes(), tt.enc) {
				t.Errorf("written % x\nwant    % x", w.Bytes(), tt.enc)
			}
			if c := Choice(tt.enc[0]); c == 0 {
				t.Errorf("choice 0")
			}
		})
	}
}

// TestTruncated cuts every PDU at every length: each prefix fails with a
// tagged error.
func TestTruncated(t *testing.T) {
	ci := payload(t, "connect-initial.bin")
	cr := payload(t, "connect-response.bin")
	for _, tt := range domainPDUs() {
		for n := range len(tt.enc) {
			r := wire.NewReader(tt.enc[:n])
			if _, err := tt.read(&r); err == nil {
				t.Errorf("%s cut at %d: no error", tt.name, n)
			} else if _, ok := errors.FromError(err); !ok {
				t.Errorf("%s cut at %d: %T", tt.name, n, err)
			}
		}
	}
	for n := range len(ci) {
		if _, err := ParseConnectInitial(ci[:n]); err == nil {
			t.Errorf("Connect-Initial cut at %d: no error", n)
		}
	}
	for n := range len(cr) {
		if _, err := ParseConnectResponse(cr[:n]); err == nil {
			t.Errorf("Connect-Response cut at %d: no error", n)
		}
	}
}

// TestHostileInput feeds every reader seeded random bytes and random
// mutations of valid PDUs: none may panic, and every failure is tagged.
func TestHostileInput(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	ci := payload(t, "connect-initial.bin")
	cr := payload(t, "connect-response.bin")
	readers := []func([]byte) error{
		func(b []byte) error { _, err := ParseConnectInitial(b); return err },
		func(b []byte) error { _, err := ParseConnectResponse(b); return err },
	}
	for _, tt := range domainPDUs() {
		readers = append(readers, func(b []byte) error { r := wire.NewReader(b); _, err := tt.read(&r); return err })
	}
	seeds := [][]byte{ci, cr}
	for _, tt := range domainPDUs() {
		seeds = append(seeds, tt.enc)
	}
	buf := make([]byte, 0, 512)
	for i := range 20000 {
		buf = buf[:0]
		if i%2 == 0 {
			for range rng.IntN(64) {
				buf = append(buf, byte(rng.UintN(256)))
			}
		} else {
			buf = append(buf, seeds[rng.IntN(len(seeds))]...)
			for range 1 + rng.IntN(4) {
				if len(buf) > 0 {
					buf[rng.IntN(len(buf))] = byte(rng.UintN(256))
				}
			}
		}
		for _, read := range readers {
			if err := read(buf); err != nil {
				if _, ok := errors.FromError(err); !ok {
					t.Fatalf("% x: untagged %T %v", buf, err, err)
				}
			}
		}
	}
}

func TestConnectErrors(t *testing.T) {
	ci := payload(t, "connect-initial.bin")
	cr := payload(t, "connect-response.bin")
	withByte := func(b []byte, i int, v byte) []byte {
		c := bytes.Clone(b)
		c[i] = v
		return c
	}
	// The response with one byte more of user data inside its outer length
	// (the user data's own length unchanged): trailing bytes in the SEQUENCE.
	crInner := append(withByte(withByte(cr, 4, 0x46), 0, 0x7f), 0x00)
	// The target parameters (the SEQUENCE at offset 14, 0x19 bytes of
	// contents) with one byte more after the eighth INTEGER, and the lengths
	// around them grown by one.
	ciParams := func() []byte {
		const end = 16 + 0x19
		c := append(bytes.Clone(ci[:end]), 0x00)
		c = append(c, ci[end:]...)
		c[4]++
		c[15]++
		return c
	}()
	tests := []struct {
		name  string
		run   func() error
		route string
	}{
		{"initial: wrong tag", func() error { _, err := ParseConnectInitial(withByte(ci, 1, 0x66)); return err },
			"ParseConnectInitial->Reader.BERHeader." + wire.TagBERTagMismatch},
		{"initial: trailing after", func() error { _, err := ParseConnectInitial(append(bytes.Clone(ci), 0)); return err },
			"ParseConnectInitial." + TagTrailingBytes},
		{"initial: trailing inside", func() error {
			c := append(bytes.Clone(ci), 0)
			c[4]++
			_, err := ParseConnectInitial(c)
			return err
		}, "ParseConnectInitial." + TagTrailingBytes},
		{"initial: no upward flag", func() error { _, err := ParseConnectInitial(withByte(ci, 11, 0x02)); return err },
			"ParseConnectInitial->Reader.BERBoolean->Reader.BERHeader." + wire.TagBERTagMismatch},
		{"initial: parameters trailing", func() error { _, err := ParseConnectInitial(ciParams); return err },
			"ParseConnectInitial->readDomainParameters." + TagTrailingBytes},
		{"initial: parameter not an integer", func() error { _, err := ParseConnectInitial(withByte(ci, 16, 0x04)); return err },
			"ParseConnectInitial->readDomainParameters->Reader.BERUint->Reader.BERHeader." + wire.TagBERTagMismatch},
		{"initial: parameters not a sequence", func() error { _, err := ParseConnectInitial(withByte(ci, 14, 0x31)); return err },
			"ParseConnectInitial->readDomainParameters->Reader.BERHeader." + wire.TagBERTagMismatch},
		{"response: wrong tag", func() error { _, err := ParseConnectResponse(withByte(cr, 1, 0x65)); return err },
			"ParseConnectResponse->Reader.BERHeader." + wire.TagBERTagMismatch},
		{"response: trailing after", func() error { _, err := ParseConnectResponse(append(bytes.Clone(cr), 0)); return err },
			"ParseConnectResponse." + TagTrailingBytes},
		{"response: trailing inside", func() error { _, err := ParseConnectResponse(crInner); return err },
			"ParseConnectResponse." + TagTrailingBytes},
		{"response: result 16", func() error { _, err := ParseConnectResponse(withByte(cr, 7, 0x10)); return err },
			"ParseConnectResponse." + TagResultInvalid},
		{"response: result negative", func() error { _, err := ParseConnectResponse(withByte(cr, 7, 0xff)); return err },
			"ParseConnectResponse." + TagResultInvalid},
		{"write response: result 16", func() error { var w wire.Writer; return WriteConnectResponse(&w, &ConnectResponse{Result: 16}) },
			"WriteConnectResponse." + TagResultOutOfRange},
		{"write initial: whole too long", func() error {
			var w wire.Writer
			return WriteConnectInitial(&w, &ConnectInitial{UserData: make([]byte, 0xfff0)})
		}, "WriteConnectInitial->Writer.PatchBERLength." + wire.TagBERLengthOutOfRange},
		{"write response: whole too long", func() error {
			var w wire.Writer
			return WriteConnectResponse(&w, &ConnectResponse{UserData: make([]byte, 0xfff0)})
		}, "WriteConnectResponse->Writer.PatchBERLength." + wire.TagBERLengthOutOfRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, tt.run(), tt.route)
		})
	}
}

func TestDomainErrors(t *testing.T) {
	read := func(b []byte, f func(r *wire.Reader) error) error {
		r := wire.NewReader(b)
		return f(&r)
	}
	erect := func(r *wire.Reader) error { _, err := ReadErectDomainRequest(r); return err }
	auc := func(r *wire.Reader) error { _, err := ReadAttachUserConfirm(r); return err }
	cjr := func(r *wire.Reader) error { _, err := ReadChannelJoinRequest(r); return err }
	cjc := func(r *wire.Reader) error { _, err := ReadChannelJoinConfirm(r); return err }
	sdr := func(r *wire.Reader) error { _, err := ReadSendDataRequest(r); return err }
	sdi := func(r *wire.Reader) error { _, err := ReadSendDataIndication(r); return err }
	dpu := func(r *wire.Reader) error { _, err := ReadDisconnectProviderUltimatum(r); return err }
	tests := []struct {
		name  string
		run   func() error
		route string
	}{
		{"erect: wrong choice", func() error { return read([]byte{0x28}, erect) }, "ReadErectDomainRequest." + TagUnexpectedPDU},
		{"erect: trailing", func() error { return read([]byte{0x04, 0x01, 0x00, 0x01, 0x00, 0x00}, erect) }, "ReadErectDomainRequest." + TagTrailingBytes},
		{"erect: bad subHeight", func() error { return read([]byte{0x04, 0x00}, erect) },
			"ReadErectDomainRequest->Reader.PERUint." + wire.TagPERIntegerLengthInvalid},
		{"attach request: wrong choice", func() error { return read([]byte{0x04}, ReadAttachUserRequest) }, "ReadAttachUserRequest." + TagUnexpectedPDU},
		{"attach request: trailing", func() error { return read([]byte{0x28, 0x00}, ReadAttachUserRequest) }, "ReadAttachUserRequest." + TagTrailingBytes},
		{"attach request: empty", func() error { return read(nil, ReadAttachUserRequest) }, "ReadAttachUserRequest->Reader.U8." + wire.TagTruncated},
		{"attach confirm: wrong choice", func() error { return read([]byte{0x28, 0x00}, auc) }, "ReadAttachUserConfirm." + TagUnexpectedPDU},
		{"attach confirm: trailing", func() error { return read([]byte{0x2c, 0x00, 0x00}, auc) }, "ReadAttachUserConfirm." + TagTrailingBytes},
		{"attach confirm: initiator truncated", func() error { return read([]byte{0x2e, 0x00, 0x00}, auc) },
			"ReadAttachUserConfirm->Reader.PERInteger16->Reader.U16BE." + wire.TagTruncated},
		{"attach confirm: no second byte", func() error { return read([]byte{0x2e}, auc) }, "ReadAttachUserConfirm->Reader.U8." + wire.TagTruncated},
		{"join request: wrong choice", func() error { return read([]byte{0x3c, 0, 0, 0, 0}, cjr) }, "ReadChannelJoinRequest." + TagUnexpectedPDU},
		{"join request: trailing", func() error { return read([]byte{0x38, 0x00, 0x06, 0x03, 0xeb, 0x00}, cjr) }, "ReadChannelJoinRequest." + TagTrailingBytes},
		{"join request: initiator past 65535", func() error { return read([]byte{0x38, 0xfc, 0x17, 0x03, 0xeb}, cjr) },
			"ReadChannelJoinRequest->Reader.PERInteger16." + wire.TagPERIntegerOutOfRange},
		{"join confirm: wrong choice", func() error { return read([]byte{0x38, 0, 0, 0, 0, 0}, cjc) }, "ReadChannelJoinConfirm." + TagUnexpectedPDU},
		{"join confirm: trailing", func() error { return read([]byte{0x3c, 0x00, 0x00, 0x06, 0x03, 0xef, 0x00}, cjc) }, "ReadChannelJoinConfirm." + TagTrailingBytes},
		{"join confirm: channel truncated", func() error { return read([]byte{0x3e, 0x00, 0x00, 0x06, 0x03, 0xef, 0x03}, cjc) },
			"ReadChannelJoinConfirm->Reader.PERInteger16->Reader.U16BE." + wire.TagTruncated},
		{"send data request: indication", func() error { return read([]byte{0x68, 0, 0, 0, 0, 0x70, 0}, sdr) }, "ReadSendDataRequest->readSendData." + TagUnexpectedPDU},
		{"send data indication: request", func() error { return read([]byte{0x64, 0, 0, 0, 0, 0x70, 0}, sdi) }, "ReadSendDataIndication->readSendData." + TagUnexpectedPDU},
		{"send data: trailing", func() error { return read([]byte{0x64, 0, 0, 0, 0, 0x70, 0x01, 0xaa, 0xbb}, sdr) }, "ReadSendDataRequest->readSendData." + TagTrailingBytes},
		{"send data: data beyond input", func() error { return read([]byte{0x64, 0, 0, 0, 0, 0x70, 0x05, 0xaa}, sdr) },
			"ReadSendDataRequest->readSendData->Reader.Bytes." + wire.TagTruncated},
		{"send data: fragmented length", func() error { return read([]byte{0x64, 0, 0, 0, 0, 0x70, 0xc1}, sdr) },
			"ReadSendDataRequest->readSendData->Reader.PERLength." + wire.TagPERLengthFragmented},
		{"ultimatum: wrong choice", func() error { return read([]byte{0x28, 0x00}, dpu) }, "ReadDisconnectProviderUltimatum." + TagUnexpectedPDU},
		{"ultimatum: reason 5", func() error { return read([]byte{0x22, 0x80}, dpu) }, "ReadDisconnectProviderUltimatum." + TagReasonInvalid},
		{"ultimatum: trailing", func() error { return read([]byte{0x21, 0x80, 0x00}, dpu) }, "ReadDisconnectProviderUltimatum." + TagTrailingBytes},
		{"ultimatum: no reason byte", func() error { return read([]byte{0x21}, dpu) }, "ReadDisconnectProviderUltimatum->Reader.U8." + wire.TagTruncated},
		{"write ultimatum: reason 5", func() error { var w wire.Writer; return WriteDisconnectProviderUltimatum(&w, 5) },
			"WriteDisconnectProviderUltimatum." + TagReasonInvalid},
		{"write attach confirm: result 16", func() error { var w wire.Writer; return WriteAttachUserConfirm(&w, &AttachUserConfirm{Result: 16}) },
			"WriteAttachUserConfirm." + TagResultOutOfRange},
		{"write attach confirm: initiator 1000", func() error {
			var w wire.Writer
			return WriteAttachUserConfirm(&w, &AttachUserConfirm{HasInitiator: true, Initiator: 1000})
		}, "WriteAttachUserConfirm->Writer.PERInteger16." + wire.TagPERIntegerBelowMinimum},
		{"write join confirm: result 16", func() error { var w wire.Writer; return WriteChannelJoinConfirm(&w, &ChannelJoinConfirm{Result: 16}) },
			"WriteChannelJoinConfirm." + TagResultOutOfRange},
		{"write join confirm: initiator 0", func() error { var w wire.Writer; return WriteChannelJoinConfirm(&w, &ChannelJoinConfirm{}) },
			"WriteChannelJoinConfirm->Writer.PERInteger16." + wire.TagPERIntegerBelowMinimum},
		{"write join request: initiator 0", func() error { var w wire.Writer; return WriteChannelJoinRequest(&w, &ChannelJoinRequest{}) },
			"WriteChannelJoinRequest->Writer.PERInteger16." + wire.TagPERIntegerBelowMinimum},
		{"write send data: priority 4", func() error {
			var w wire.Writer
			return WriteSendDataIndication(&w, &SendData{Initiator: 1002, Priority: 4})
		}, "WriteSendDataIndication->writeSendData." + TagPriorityInvalid},
		{"write send data: segmentation 4", func() error {
			var w wire.Writer
			return WriteSendDataRequest(&w, &SendData{Initiator: 1002, Segmentation: 4})
		}, "WriteSendDataRequest->writeSendData." + TagSegmentationInvalid},
		{"write send data: initiator 0", func() error { var w wire.Writer; return WriteSendDataIndication(&w, &SendData{}) },
			"WriteSendDataIndication->writeSendData->Writer.PERInteger16." + wire.TagPERIntegerBelowMinimum},
		{"write send data: data too long", func() error {
			var w wire.Writer
			return WriteSendDataIndication(&w, &SendData{Initiator: 1002, Data: make([]byte, 0x4000)})
		}, "WriteSendDataIndication->writeSendData->Writer.PERLength." + wire.TagPERLengthOutOfRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, tt.run(), tt.route)
		})
	}
}

func TestNoAllocations(t *testing.T) {
	ci := payload(t, "connect-initial.bin")
	sdr := append([]byte{0x64, 0x00, 0x06, 0x03, 0xeb, 0x70, 0x81, 0x9c}, userData412...)
	resp := ConnectResponse{Parameters: DomainParameters{34, 3, 0, 1, 0, 1, 65528, 2}, UserData: make([]byte, 287)}
	ind := SendData{Initiator: 1002, ChannelID: 1003, Priority: PriorityHigh, Segmentation: SegmentationWhole, Data: licenseData}
	buf := make([]byte, 0, 1024)
	tests := []struct {
		name string
		run  func()
	}{
		{"parse Connect-Initial", func() {
			if _, err := ParseConnectInitial(ci); err != nil {
				t.Fatal(err)
			}
		}},
		{"write Connect-Response", func() {
			w := wire.NewWriter(buf)
			if err := WriteConnectResponse(&w, &resp); err != nil {
				t.Fatal(err)
			}
		}},
		{"read Send Data Request", func() {
			r := wire.NewReader(sdr)
			if _, err := ReadSendDataRequest(&r); err != nil {
				t.Fatal(err)
			}
		}},
		{"write Send Data Indication", func() {
			w := wire.NewWriter(buf)
			if err := WriteSendDataIndication(&w, &ind); err != nil {
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
