package gcc

import (
	"bytes"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/mcs"
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

func readExample(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ms-rdpbcgr", name))
	if err != nil {
		t.Fatal(err)
	}
	return b[7:] // after the TPKT and X.224 Data headers
}

// requestUserData is the MCS user data of Microsoft's Connect-Initial
// (MS-RDPBCGR 4.1.3): ConnectData with the Conference Create Request.
func requestUserData(t *testing.T) []byte {
	t.Helper()
	ci, err := mcs.ParseConnectInitial(readExample(t, "connect-initial.bin"))
	if err != nil {
		t.Fatal(err)
	}
	return ci.UserData
}

// responseUserData is the MCS user data of Microsoft's Connect-Response
// (4.1.4).
func responseUserData(t *testing.T) []byte {
	t.Helper()
	cr, err := mcs.ParseConnectResponse(readExample(t, "connect-response.bin"))
	if err != nil {
		t.Fatal(err)
	}
	return cr.UserData
}

func utf16le(s string, n int) []byte {
	b := make([]byte, n)
	for i, c := range s {
		b[2*i] = byte(c)
	}
	return b
}

func TestConferenceCreateRequestMicrosoft(t *testing.T) {
	ud := requestUserData(t)
	blocks, err := ParseConferenceCreateRequest(ud)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 284 || !bytes.Equal(blocks, ud[23:]) {
		t.Fatalf("client data of %d bytes at % x", len(blocks), blocks[:4])
	}
	var w wire.Writer
	if err := WriteConferenceCreateRequest(&w, blocks); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Bytes(), ud) {
		t.Errorf("written % x\nwant    % x", w.Bytes(), ud)
	}
}

func TestClientDataMicrosoft(t *testing.T) {
	blocks := requestUserData(t)[23:]
	cd, err := ParseClientData(blocks)
	if err != nil {
		t.Fatal(err)
	}
	c := &cd.Core
	want := ClientCore{
		Version: VersionRDP5, DesktopWidth: 1280, DesktopHeight: 1024, ColorDepth: 0xca01, SASSequence: 0xaa03,
		KeyboardLayout: 0x409, ClientBuild: 3790, ClientName: utf16le("ELTONS-DEV2", 32),
		KeyboardType: 4, KeyboardFunctionKey: 12, IMEFileName: make([]byte, 64),
		OptionalFields: OptServerSelectedProtocol, PostBeta2ColorDepth: 0xca01, ClientProductID: 1,
		HighColorDepth: 24, SupportedColorDepths: Support24BPP | Support16BPP | Support15BPP,
		EarlyCapabilityFlags: EarlySupportErrInfoPDU, ClientDigProductID: utf16le("69712-783-0357974-42714", 64),
	}
	if !reflect.DeepEqual(*c, want) {
		t.Errorf("core %+v\nwant %+v", *c, want)
	}
	if c.Name() != "ELTONS-DEV2" || !c.Early(EarlySupportErrInfoPDU) || c.Early(EarlySupportDynvcGFXProtocol) {
		t.Errorf("name %q, early flags %#x", c.Name(), c.EarlyCapabilityFlags)
	}
	if _, _, ok := c.PhysicalSize(); ok {
		t.Error("physical size without the fields")
	}
	if !cd.HasCluster || cd.Cluster != (ClientCluster{Flags: 0x0d}) {
		t.Errorf("cluster %v %+v", cd.HasCluster, cd.Cluster)
	}
	if !cd.HasSecurity || cd.Security != (ClientSecurity{EncryptionMethods: 0x1b}) {
		t.Errorf("security %v %+v", cd.HasSecurity, cd.Security)
	}
	if !cd.HasNetwork || cd.Network.ChannelCount != 3 {
		t.Fatalf("network %v %d", cd.HasNetwork, cd.Network.ChannelCount)
	}
	for i, ch := range []struct {
		name string
		opts uint32
	}{
		{"rdpdr", ChannelOptionInitialized | ChannelOptionCompressRDP},
		{"cliprdr", ChannelOptionInitialized | ChannelOptionEncryptRDP | ChannelOptionCompressRDP | ChannelOptionShowProtocol},
		{"rdpsnd", ChannelOptionInitialized | ChannelOptionEncryptRDP},
	} {
		if got := &cd.Network.Channels[i]; got.NameString() != ch.name || got.Options != ch.opts {
			t.Errorf("channel %d %q %#x, want %q %#x", i, got.NameString(), got.Options, ch.name, ch.opts)
		}
	}
	if cd.HasMonitor || cd.HasMessageChannel || cd.HasMultitransport {
		t.Error("blocks the example does not have")
	}
	var w wire.Writer
	if err := WriteClientData(&w, &cd); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Bytes(), blocks) {
		t.Errorf("written % x\nwant    % x", w.Bytes(), blocks)
	}
}

func TestConferenceCreateResponseMicrosoft(t *testing.T) {
	ud := responseUserData(t)
	ccr, err := ParseConferenceCreateResponse(ud)
	if err != nil {
		t.Fatal(err)
	}
	if ccr.NodeID != 31219 || ccr.Tag != 1 || ccr.Result != ResultSuccess || !bytes.Equal(ccr.UserData, ud[23:]) || len(ccr.UserData) != 264 {
		t.Fatalf("node %d tag %d result %d, %d bytes of server data", ccr.NodeID, ccr.Tag, ccr.Result, len(ccr.UserData))
	}
	// Written back with the connectPDU's real length, 279 (81 17), where
	// Microsoft wrote 42 (2a).
	want := append(append(bytes.Clone(ud[:7]), 0x81, 0x17), ud[8:]...)
	var w wire.Writer
	if err := WriteConferenceCreateResponse(&w, &ccr); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Bytes(), want) {
		t.Errorf("written % x\nwant    % x", w.Bytes(), want)
	}
	if got, err := ParseConferenceCreateResponse(w.Bytes()); err != nil || !reflect.DeepEqual(got, ccr) {
		t.Errorf("read back %+v, %v", got, err)
	}
}

func TestServerDataMicrosoft(t *testing.T) {
	blocks := responseUserData(t)[23:]
	sd, err := ParseServerData(blocks)
	if err != nil {
		t.Fatal(err)
	}
	if sd.Core != (ServerCore{Version: VersionRDP5, OptionalFields: 1}) {
		t.Errorf("core %+v", sd.Core)
	}
	n := &sd.Network
	if !sd.HasNetwork || n.MCSChannelID != 1003 || n.ChannelCount != 3 || n.ChannelIDs[0] != 1004 || n.ChannelIDs[1] != 1005 || n.ChannelIDs[2] != 1006 {
		t.Errorf("network %v %+v", sd.HasNetwork, *n)
	}
	s := &sd.Security
	if !sd.HasSecurity || s.EncryptionMethod != 2 || s.EncryptionLevel != 2 || len(s.ServerRandom) != 32 || len(s.ServerCertificate) != 184 ||
		s.ServerRandom[0] != 0x10 || !bytes.HasPrefix(s.ServerCertificate, []byte{1, 0, 0, 0, 1, 0, 0, 0}) {
		t.Errorf("security %v %d %d, %d and %d bytes", sd.HasSecurity, s.EncryptionMethod, s.EncryptionLevel, len(s.ServerRandom), len(s.ServerCertificate))
	}
	var w wire.Writer
	if err := WriteServerData(&w, &sd); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Bytes(), blocks) {
		t.Errorf("written % x\nwant    % x", w.Bytes(), blocks)
	}
}

// fullCore has every field set to a value of its own.
func fullCore() ClientCore {
	return ClientCore{
		Version: 0x0008000c, DesktopWidth: 1920, DesktopHeight: 1080, ColorDepth: 0xca01, SASSequence: 0xaa03,
		KeyboardLayout: 0x00010409, ClientBuild: 20, ClientName: utf16le("remmina", 32),
		KeyboardType: 4, KeyboardSubType: 2, KeyboardFunctionKey: 12, IMEFileName: utf16le("ime", 64),
		OptionalFields: OptAll, PostBeta2ColorDepth: 0xca04, ClientProductID: 1, SerialNumber: 7,
		HighColorDepth: 24, SupportedColorDepths: 0x0f, EarlyCapabilityFlags: 0x07a3,
		ClientDigProductID: utf16le("dig", 64), ConnectionType: 6, Pad1Octet: 0, ServerSelectedProtocol: 1,
		DesktopPhysicalWidth: 600, DesktopPhysicalHeight: 340, DesktopOrientation: 90,
		DesktopScaleFactor: 200, DeviceScaleFactor: 140,
	}
}

// cut keeps the first n optional fields of c and zeroes the others.
func cut(c ClientCore, n int) ClientCore {
	full := c
	c = ClientCore{
		Version: full.Version, DesktopWidth: full.DesktopWidth, DesktopHeight: full.DesktopHeight,
		ColorDepth: full.ColorDepth, SASSequence: full.SASSequence, KeyboardLayout: full.KeyboardLayout,
		ClientBuild: full.ClientBuild, ClientName: full.ClientName, KeyboardType: full.KeyboardType,
		KeyboardSubType: full.KeyboardSubType, KeyboardFunctionKey: full.KeyboardFunctionKey,
		IMEFileName: full.IMEFileName, OptionalFields: n,
	}
	fields := []func(){
		func() { c.PostBeta2ColorDepth = full.PostBeta2ColorDepth },
		func() { c.ClientProductID = full.ClientProductID },
		func() { c.SerialNumber = full.SerialNumber },
		func() { c.HighColorDepth = full.HighColorDepth },
		func() { c.SupportedColorDepths = full.SupportedColorDepths },
		func() { c.EarlyCapabilityFlags = full.EarlyCapabilityFlags },
		func() { c.ClientDigProductID = full.ClientDigProductID },
		func() { c.ConnectionType = full.ConnectionType },
		func() { c.Pad1Octet = full.Pad1Octet },
		func() { c.ServerSelectedProtocol = full.ServerSelectedProtocol },
		func() { c.DesktopPhysicalWidth = full.DesktopPhysicalWidth },
		func() { c.DesktopPhysicalHeight = full.DesktopPhysicalHeight },
		func() { c.DesktopOrientation = full.DesktopOrientation },
		func() { c.DesktopScaleFactor = full.DesktopScaleFactor },
		func() { c.DeviceScaleFactor = full.DeviceScaleFactor },
	}
	for _, f := range fields[:n] {
		f()
	}
	return c
}

// TestClientCoreEveryLength writes and reads a core block of each length a
// client may send, from no optional field (RDP 4.0's 132 bytes) to all 15
// (234 bytes); older clients send shorter blocks.
func TestClientCoreEveryLength(t *testing.T) {
	wantLens := []int{132, 134, 136, 140, 142, 144, 146, 210, 211, 212, 216, 220, 224, 226, 230, 234}
	for n := 0; n <= OptAll; n++ {
		if got := ClientCoreLen(n); got != wantLens[n] {
			t.Errorf("ClientCoreLen(%d) = %d, want %d", n, got, wantLens[n])
		}
		cd := ClientData{Core: cut(fullCore(), n)}
		var w wire.Writer
		if err := WriteClientData(&w, &cd); err != nil {
			t.Fatal(err)
		}
		if w.Len() != wantLens[n] || int(w.Bytes()[2])|int(w.Bytes()[3])<<8 != wantLens[n] {
			t.Errorf("%d fields: block of %d bytes, header says % x", n, w.Len(), w.Bytes()[2:4])
		}
		got, err := ParseClientData(w.Bytes())
		if err != nil {
			t.Fatalf("%d fields: %v", n, err)
		}
		if !reflect.DeepEqual(got.Core, cd.Core) {
			t.Errorf("%d fields: read %+v\nwant %+v", n, got.Core, cd.Core)
		}
		for f := 1; f <= OptAll; f++ {
			if got.Core.Has(f) != (f <= n) {
				t.Errorf("%d fields: Has(%d) = %v", n, f, got.Core.Has(f))
			}
		}
	}
	// A length between two field boundaries is refused.
	full := ClientData{Core: fullCore()}
	var w wire.Writer
	if err := WriteClientData(&w, &full); err != nil {
		t.Fatal(err)
	}
	boundary := map[int]bool{}
	for _, l := range wantLens {
		boundary[l] = true
	}
	for l := 132; l <= 240; l++ {
		if boundary[l] {
			continue
		}
		b := append(bytes.Clone(w.Bytes()), make([]byte, 6)...)[:l]
		b[2], b[3] = byte(l), byte(l>>8)
		_, err := ParseClientData(b)
		assertRoute(t, err, "ParseClientData->readClientCore."+TagBlockLengthInvalid)
	}
}

func TestClientCoreHelpers(t *testing.T) {
	c := fullCore()
	if w, h, ok := c.PhysicalSize(); !ok || w != 600 || h != 340 {
		t.Errorf("physical size %d %d %v", w, h, ok)
	}
	if d, v, ok := c.ScaleFactors(); !ok || d != 200 || v != 140 {
		t.Errorf("scale factors %d %d %v", d, v, ok)
	}
	if !c.Early(EarlySupportDynvcGFXProtocol) || c.Early(EarlySupportSkipChannelJoin) {
		t.Errorf("early flags %#x", c.EarlyCapabilityFlags)
	}
	ignored := []func(c *ClientCore){
		func(c *ClientCore) { c.DesktopPhysicalWidth = 9 },
		func(c *ClientCore) { c.DesktopPhysicalHeight = 10001 },
		func(c *ClientCore) { *c = cut(*c, OptDesktopPhysicalWidth) },
	}
	for i, f := range ignored {
		c := fullCore()
		f(&c)
		if _, _, ok := c.PhysicalSize(); ok {
			t.Errorf("physical size %d not ignored", i)
		}
	}
	scales := []func(c *ClientCore){
		func(c *ClientCore) { c.DesktopScaleFactor = 99 },
		func(c *ClientCore) { c.DesktopScaleFactor = 501 },
		func(c *ClientCore) { c.DeviceScaleFactor = 120 },
		func(c *ClientCore) { *c = cut(*c, OptDesktopScaleFactor) },
	}
	for i, f := range scales {
		c := fullCore()
		f(&c)
		if _, _, ok := c.ScaleFactors(); ok {
			t.Errorf("scale factors %d not ignored", i)
		}
	}
	short := cut(fullCore(), OptSupportedColorDepths)
	if short.Early(EarlySupportErrInfoPDU) {
		t.Error("early flag without the field")
	}
	ch := ChannelDef{Name: [8]byte{'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h'}}
	if ch.NameString() != "abcdefgh" {
		t.Errorf("unterminated name %q", ch.NameString())
	}
}

func TestClientDataRoundTrip(t *testing.T) {
	cd := ClientData{
		Core:              cut(fullCore(), OptServerSelectedProtocol),
		HasSecurity:       true,
		HasNetwork:        true,
		Network:           ClientNetwork{ChannelCount: 2},
		HasMonitor:        true,
		Monitor:           ClientMonitor{MonitorCount: 2},
		HasMessageChannel: true,
		HasMultitransport: true,
		Multitransport:    ClientMultitransport{Flags: 0x305},
	}
	copy(cd.Network.Channels[0].Name[:], "drdynvc")
	cd.Network.Channels[0].Options = ChannelOptionInitialized
	copy(cd.Network.Channels[1].Name[:], "rdpsnd")
	cd.Monitor.Monitors[0] = MonitorDef{Left: 0, Top: 0, Right: 1919, Bottom: 1079, Flags: MonitorPrimary}
	cd.Monitor.Monitors[1] = MonitorDef{Left: -1280, Top: -200, Right: -1, Bottom: 823}
	var w wire.Writer
	if err := WriteClientData(&w, &cd); err != nil {
		t.Fatal(err)
	}
	// A monitor extended data block, which is skipped.
	w.Write([]byte{0x08, 0xc0, 0x10, 0x00, 0, 0, 0, 0, 20, 0, 0, 0, 0, 0, 0, 0})
	got, err := ParseClientData(w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, cd) {
		t.Errorf("read %+v\nwant %+v", got, cd)
	}
}

func TestServerDataRoundTrip(t *testing.T) {
	for _, tt := range []struct {
		name string
		sd   ServerData
		want []byte
	}{
		{"what mardp sends", ServerData{
			Core:        ServerCore{Version: VersionRDP10, OptionalFields: 2, ClientRequestedProtocols: 0x0b, EarlyCapabilityFlags: ServerEarlyDynamicDSTSupported},
			HasNetwork:  true,
			Network:     ServerNetwork{MCSChannelID: 1003, ChannelCount: 2, ChannelIDs: [MaxChannels]uint16{1004, 1005}},
			HasSecurity: true,
		}, []byte{
			0x01, 0x0c, 0x10, 0x00, 0x05, 0x00, 0x08, 0x00, 0x0b, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00,
			0x03, 0x0c, 0x0c, 0x00, 0xeb, 0x03, 0x02, 0x00, 0xec, 0x03, 0xed, 0x03,
			0x02, 0x0c, 0x0c, 0x00, 0, 0, 0, 0, 0, 0, 0, 0,
		}},
		{"oldest core, no channels", ServerData{
			Core:       ServerCore{Version: VersionRDP4},
			HasNetwork: true,
			Network:    ServerNetwork{MCSChannelID: 1003},
		}, []byte{
			0x01, 0x0c, 0x08, 0x00, 0x01, 0x00, 0x08, 0x00,
			0x03, 0x0c, 0x08, 0x00, 0xeb, 0x03, 0x00, 0x00,
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var w wire.Writer
			if err := WriteServerData(&w, &tt.sd); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(w.Bytes(), tt.want) {
				t.Errorf("written % x\nwant    % x", w.Bytes(), tt.want)
			}
			// With a message channel block, which is skipped.
			b := append(bytes.Clone(w.Bytes()), 0x04, 0x0c, 0x06, 0x00, 0xef, 0x03)
			got, err := ParseServerData(b)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.sd) {
				t.Errorf("read %+v\nwant %+v", got, tt.sd)
			}
		})
	}
}

// hostileCases are the parsers under test with a valid input each.
func hostileCases(t *testing.T) []struct {
	name  string
	parse func([]byte) error
	valid []byte
} {
	req := requestUserData(t)
	resp := responseUserData(t)
	var w wire.Writer
	cd := ClientData{Core: fullCore(), HasMonitor: true, Monitor: ClientMonitor{MonitorCount: 1}, HasMessageChannel: true, HasMultitransport: true}
	if err := WriteClientData(&w, &cd); err != nil {
		t.Fatal(err)
	}
	return []struct {
		name  string
		parse func([]byte) error
		valid []byte
	}{
		{"request", func(b []byte) error { _, err := ParseConferenceCreateRequest(b); return err }, req},
		{"response", func(b []byte) error { _, err := ParseConferenceCreateResponse(b); return err }, resp},
		{"client data", func(b []byte) error { _, err := ParseClientData(b); return err }, req[23:]},
		{"client data, all blocks", func(b []byte) error { _, err := ParseClientData(b); return err }, w.Bytes()},
		{"server data", func(b []byte) error { _, err := ParseServerData(b); return err }, resp[23:]},
	}
}

// TestTruncated cuts each valid input at every length. The data block
// parsers accept a cut at a block boundary, so only the cuts inside a block,
// or before the core block ends, have to fail.
func TestTruncated(t *testing.T) {
	for _, tt := range hostileCases(t) {
		for n := range len(tt.valid) {
			err := tt.parse(tt.valid[:n])
			if err == nil {
				if tt.name == "request" || tt.name == "response" {
					t.Errorf("%s cut at %d: no error", tt.name, n)
				}
				continue
			}
			if _, ok := errors.FromError(err); !ok {
				t.Errorf("%s cut at %d: %T", tt.name, n, err)
			}
		}
	}
}

func TestHostileInput(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	cases := hostileCases(t)
	buf := make([]byte, 0, 512)
	for i := range 20000 {
		buf = buf[:0]
		if i%2 == 0 {
			for range rng.IntN(64) {
				buf = append(buf, byte(rng.UintN(256)))
			}
		} else {
			buf = append(buf, cases[rng.IntN(len(cases))].valid...)
			for range 1 + rng.IntN(4) {
				buf[rng.IntN(len(buf))] = byte(rng.UintN(256))
			}
		}
		for _, c := range cases {
			if err := c.parse(buf); err != nil {
				if _, ok := errors.FromError(err); !ok {
					t.Fatalf("%s % x: untagged %T %v", c.name, buf, err, err)
				}
			}
		}
	}
}

func TestErrors(t *testing.T) {
	req := requestUserData(t)
	resp := responseUserData(t)
	clientBlocks := req[23:]
	serverBlocks := resp[23:]
	with := func(b []byte, i int, v byte) []byte {
		c := bytes.Clone(b)
		c[i] = v
		return c
	}
	block := func(typ uint16, body ...byte) []byte {
		n := 4 + len(body)
		return append([]byte{byte(typ), byte(typ >> 8), byte(n), byte(n >> 8)}, body...)
	}
	core := clientBlocks[:216]
	coreOnly := func(extra ...byte) []byte { return append(bytes.Clone(core), extra...) }
	srvCore := block(TypeServerCore, 4, 0, 8, 0)
	srvOnly := func(extra ...byte) []byte { return append(bytes.Clone(srvCore), extra...) }
	pcr := func(b []byte) func() error {
		return func() error { _, err := ParseConferenceCreateRequest(b); return err }
	}
	pcs := func(b []byte) func() error {
		return func() error { _, err := ParseConferenceCreateResponse(b); return err }
	}
	pcd := func(b []byte) func() error { return func() error { _, err := ParseClientData(b); return err } }
	psd := func(b []byte) func() error { return func() error { _, err := ParseServerData(b); return err } }
	wsd := func(sd ServerData) func() error {
		return func() error { var w wire.Writer; return WriteServerData(&w, &sd) }
	}
	wcd := func(cd ClientData) func() error {
		return func() error { var w wire.Writer; return WriteClientData(&w, &cd) }
	}
	tests := []struct {
		name  string
		run   func() error
		route string
	}{
		{"request: key not an object", pcr(with(req, 0, 1)), "ParseConferenceCreateRequest->readConnectData." + TagObjectInvalid},
		{"request: object of 6 bytes", pcr(with(req, 1, 6)), "ParseConferenceCreateRequest->readConnectData." + TagObjectInvalid},
		{"request: another object", pcr(with(req, 6, 2)), "ParseConferenceCreateRequest->readConnectData." + TagObjectInvalid},
		{"request: a response", pcr(with(req, 9, 0x14)), "ParseConferenceCreateRequest." + TagUnexpectedPDU},
		{"request: password", pcr(with(req, 9, 0x02)), "ParseConferenceCreateRequest." + TagOptionalUnsupported},
		{"request: text name", pcr(with(req, 10, 0x0a)), "ParseConferenceCreateRequest." + TagOptionalUnsupported},
		{"request: no user data", pcr(with(req, 10, 0x00)), "ParseConferenceCreateRequest." + TagUserDataMissing},
		{"request: no user data set", pcr(with(req, 14, 0)), "ParseConferenceCreateRequest->readUserData." + TagUserDataMissing},
		{"request: no value", pcr(with(req, 15, 0x40)), "ParseConferenceCreateRequest->readUserData." + TagUserDataKeyInvalid},
		{"request: server key", pcr(append(append(bytes.Clone(req[:17]), 'M', 'c', 'D', 'n'), req[21:]...)),
			"ParseConferenceCreateRequest->readUserData." + TagUserDataKeyInvalid},
		{"request: user data beyond input", pcr(with(req, 22, 0x1d)), "ParseConferenceCreateRequest->readUserData->Reader.Bytes." + wire.TagTruncated},
		{"response: a request", pcs(with(resp, 8, 0x00)), "ParseConferenceCreateResponse." + TagUnexpectedPDU},
		{"response: no user data", pcs(with(resp, 8, 0x10)), "ParseConferenceCreateResponse." + TagUserDataMissing},
		{"response: client key", pcs(with(resp, 18, 'D')), "ParseConferenceCreateResponse->readUserData." + TagUserDataKeyInvalid},
		{"response: object", pcs(with(resp, 2, 0x15)), "ParseConferenceCreateResponse->readConnectData." + TagObjectInvalid},
		{"write response: result 8", func() error {
			var w wire.Writer
			return WriteConferenceCreateResponse(&w, &ConferenceCreateResponse{NodeID: 1001, Result: 8})
		}, "WriteConferenceCreateResponse." + TagResultOutOfRange},
		{"write response: node 1000", func() error {
			var w wire.Writer
			return WriteConferenceCreateResponse(&w, &ConferenceCreateResponse{NodeID: 1000})
		}, "WriteConferenceCreateResponse->Writer.PERInteger16." + wire.TagPERIntegerBelowMinimum},
		{"write response: too long", func() error {
			var w wire.Writer
			return WriteConferenceCreateResponse(&w, &ConferenceCreateResponse{NodeID: 1001, UserData: make([]byte, 0x3ff8)})
		}, "WriteConferenceCreateResponse->patchPERLength2." + wire.TagPERLengthOutOfRange},
		{"write request: user data too long", func() error {
			var w wire.Writer
			return WriteConferenceCreateRequest(&w, make([]byte, 0x4000))
		}, "WriteConferenceCreateRequest->writeUserData->Writer.PERLength." + wire.TagPERLengthOutOfRange},
		{"write request: too long", func() error {
			var w wire.Writer
			return WriteConferenceCreateRequest(&w, make([]byte, 0x3ff8))
		}, "WriteConferenceCreateRequest->patchPERLength2." + wire.TagPERLengthOutOfRange},

		{"client: block shorter than its header", pcd(coreOnly(0x02, 0xc0, 0x03, 0x00)), "ParseClientData->readBlockHeader." + TagBlockLengthInvalid},
		{"client: block past the input", pcd(coreOnly(0x02, 0xc0, 0x0d, 0x00, 0, 0, 0, 0, 0, 0, 0, 0)), "ParseClientData->readBlockHeader." + TagBlockLengthInvalid},
		{"client: header cut", pcd(coreOnly(0x02, 0xc0, 0x0c)), "ParseClientData->readBlockHeader->Reader.U16LE." + wire.TagTruncated},
		{"client: core cut in its fixed part", pcd(block(TypeClientCore, make([]byte, 100)...)), "ParseClientData->readClientCore->Reader.Bytes." + wire.TagTruncated},
		{"client: security of 16 bytes", pcd(coreOnly(block(TypeClientSecurity, make([]byte, 12)...)...)), "ParseClientData->readU32s->fixedBlock." + TagBlockLengthInvalid},
		{"client: cluster of 8 bytes", pcd(coreOnly(block(TypeClientCluster, make([]byte, 4)...)...)), "ParseClientData->readU32s->fixedBlock." + TagBlockLengthInvalid},
		{"client: message channel of 12 bytes", pcd(coreOnly(block(TypeClientMessageChannel, make([]byte, 8)...)...)), "ParseClientData->readU32s->fixedBlock." + TagBlockLengthInvalid},
		{"client: multitransport of 4 bytes", pcd(coreOnly(block(TypeClientMultitransport)...)), "ParseClientData->readU32s->fixedBlock." + TagBlockLengthInvalid},
		{"client: two security blocks", pcd(coreOnly(append(block(TypeClientSecurity, make([]byte, 8)...), block(TypeClientSecurity, make([]byte, 8)...)...)...)),
			"ParseClientData." + TagBlockDuplicate},
		{"client: two cores", pcd(coreOnly(core...)), "ParseClientData." + TagBlockDuplicate},
		{"client: no core", pcd(block(TypeClientSecurity, make([]byte, 8)...)), "ParseClientData." + TagCoreMissing},
		{"client: nothing", pcd(nil), "ParseClientData." + TagCoreMissing},
		{"client: 32 channels", pcd(coreOnly(block(TypeClientNetwork, 32, 0, 0, 0)...)), "ParseClientData->readClientNetwork." + TagChannelCountInvalid},
		{"client: channels short of the count", pcd(coreOnly(block(TypeClientNetwork, append([]byte{2, 0, 0, 0}, make([]byte, 12)...)...)...)),
			"ParseClientData->readClientNetwork->fixedBlock." + TagBlockLengthInvalid},
		{"client: network without a count", pcd(coreOnly(block(TypeClientNetwork, 1, 0)...)), "ParseClientData->readClientNetwork->Reader.U32LE." + wire.TagTruncated},
		{"client: no monitors", pcd(coreOnly(block(TypeClientMonitor, make([]byte, 8)...)...)), "ParseClientData->readClientMonitor." + TagMonitorCountInvalid},
		{"client: 17 monitors", pcd(coreOnly(block(TypeClientMonitor, 0, 0, 0, 0, 17, 0, 0, 0)...)), "ParseClientData->readClientMonitor." + TagMonitorCountInvalid},
		{"client: monitors short of the count", pcd(coreOnly(block(TypeClientMonitor, append([]byte{0, 0, 0, 0, 1, 0, 0, 0}, make([]byte, 16)...)...)...)),
			"ParseClientData->readClientMonitor->fixedBlock." + TagBlockLengthInvalid},
		{"client: monitor without a count", pcd(coreOnly(block(TypeClientMonitor, 0, 0, 0, 0)...)), "ParseClientData->readClientMonitor->Reader.U32LE." + wire.TagTruncated},
		{"write client: 16 optional fields", wcd(ClientData{Core: ClientCore{OptionalFields: 16}}), "WriteClientData->writeClientCore." + TagOptionalFieldsInvalid},
		{"write client: 32 channels", wcd(ClientData{HasNetwork: true, Network: ClientNetwork{ChannelCount: 32}}), "WriteClientData." + TagChannelCountInvalid},
		{"write client: no monitors", wcd(ClientData{HasMonitor: true}), "WriteClientData." + TagMonitorCountInvalid},

		{"server: two cores", psd(srvOnly(srvCore...)), "ParseServerData." + TagBlockDuplicate},
		{"server: no core", psd(block(TypeServerSecurity, make([]byte, 8)...)), "ParseServerData." + TagCoreMissing},
		{"server: core of 6 bytes", psd(block(TypeServerCore, 4, 0)), "ParseServerData->readServerCore." + TagBlockLengthInvalid},
		{"server: block past the input", psd(srvOnly(0x02, 0x0c, 0x0d, 0x00)), "ParseServerData->readBlockHeader." + TagBlockLengthInvalid},
		{"server: security none with a random", psd(srvOnly(block(TypeServerSecurity, make([]byte, 12)...)...)),
			"ParseServerData->readServerSecurity->fixedBlock." + TagBlockLengthInvalid},
		{"server: security lengths past the block", psd(srvOnly(block(TypeServerSecurity, 2, 0, 0, 0, 2, 0, 0, 0, 32, 0, 0, 0, 0, 0, 0, 0)...)),
			"ParseServerData->readServerSecurity." + TagSecurityInvalid},
		{"server: security cut", psd(srvOnly(block(TypeServerSecurity, 2, 0, 0, 0)...)), "ParseServerData->readServerSecurity->Reader.U32LE." + wire.TagTruncated},
		{"server: 32 channels", psd(srvOnly(block(TypeServerNetwork, 0xeb, 0x03, 32, 0)...)), "ParseServerData->readServerNetwork." + TagChannelCountInvalid},
		{"server: no pad", psd(srvOnly(block(TypeServerNetwork, 0xeb, 0x03, 1, 0, 0xec, 0x03)...)), "ParseServerData->readServerNetwork->fixedBlock." + TagBlockLengthInvalid},
		{"server: network cut", psd(srvOnly(block(TypeServerNetwork, 0xeb, 0x03)...)), "ParseServerData->readServerNetwork->Reader.U16LE." + wire.TagTruncated},
		{"server: two networks", psd(srvOnly(append(serverBlocks[12:28:28], serverBlocks[12:28]...)...)), "ParseServerData." + TagBlockDuplicate},
		{"write server: 3 optional fields", wsd(ServerData{Core: ServerCore{OptionalFields: 3}}), "WriteServerData." + TagOptionalFieldsInvalid},
		{"write server: 32 channels", wsd(ServerData{HasNetwork: true, Network: ServerNetwork{ChannelCount: 32}}), "WriteServerData." + TagChannelCountInvalid},
		{"write server: random without encryption", wsd(ServerData{HasSecurity: true, Security: ServerSecurity{ServerRandom: make([]byte, 32)}}), "WriteServerData." + TagSecurityInvalid},
		{"write server: security too long", wsd(ServerData{HasSecurity: true, Security: ServerSecurity{EncryptionMethod: 2, ServerCertificate: make([]byte, 0x10000)}}),
			"WriteServerData." + TagSecurityInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, tt.run(), tt.route)
		})
	}
}

func TestNoAllocations(t *testing.T) {
	req := requestUserData(t)
	resp := responseUserData(t)
	sd, err := ParseServerData(resp[23:])
	if err != nil {
		t.Fatal(err)
	}
	ccr := ConferenceCreateResponse{NodeID: 31219, Tag: 1, UserData: resp[23:]}
	buf := make([]byte, 0, 1024)
	tests := []struct {
		name string
		run  func()
	}{
		{"parse request and client data", func() {
			b, err := ParseConferenceCreateRequest(req)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseClientData(b); err != nil {
				t.Fatal(err)
			}
		}},
		{"write server data and response", func() {
			w := wire.NewWriter(buf)
			if err := WriteServerData(&w, &sd); err != nil {
				t.Fatal(err)
			}
			w.Reset()
			if err := WriteConferenceCreateResponse(&w, &ccr); err != nil {
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
