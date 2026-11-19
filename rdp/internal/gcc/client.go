package gcc

import (
	"unicode/utf16"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors reading and writing the data blocks.
const (
	// TagBlockLengthInvalid: a data block whose length is shorter than its
	// header, runs past the user data, or does not match what the block
	// holds.
	TagBlockLengthInvalid = "GCCBlockLengthInvalid"
	// TagBlockDuplicate: a block type that comes twice.
	TagBlockDuplicate = "GCCBlockDuplicate"
	// TagCoreMissing: client or server data without its core block.
	TagCoreMissing = "GCCCoreMissing"
	// TagChannelCountInvalid: more static channels than 31.
	TagChannelCountInvalid = "GCCChannelCountInvalid"
	// TagMonitorCountInvalid: no monitors or more than 16 in a monitor
	// block.
	TagMonitorCountInvalid = "GCCMonitorCountInvalid"
	// TagOptionalFieldsInvalid: a core block to write that says it carries
	// more optional fields than there are.
	TagOptionalFieldsInvalid = "GCCOptionalFieldsInvalid"
)

// Data block types (MS-RDPBCGR 2.2.1.3.1).
const (
	TypeClientCore           uint16 = 0xc001
	TypeClientSecurity       uint16 = 0xc002
	TypeClientNetwork        uint16 = 0xc003
	TypeClientCluster        uint16 = 0xc004
	TypeClientMonitor        uint16 = 0xc005
	TypeClientMessageChannel uint16 = 0xc006
	TypeClientMonitorEx      uint16 = 0xc008
	TypeClientMultitransport uint16 = 0xc00a
	TypeServerCore           uint16 = 0x0c01
	TypeServerSecurity       uint16 = 0x0c02
	TypeServerNetwork        uint16 = 0x0c03
	TypeServerMessageChannel uint16 = 0x0c04
	TypeServerMultitransport uint16 = 0x0c08
)

// headerLen is the size of a data block's header (type and length).
const headerLen = 4

// Client core earlyCapabilityFlags (MS-RDPBCGR 2.2.1.3.2).
const (
	EarlySupportErrInfoPDU        uint16 = 0x0001
	EarlyWant32BPPSession         uint16 = 0x0002
	EarlySupportStatusInfoPDU     uint16 = 0x0004
	EarlyStrongAsymmetricKeys     uint16 = 0x0008
	EarlyRelativeMouseInput       uint16 = 0x0010
	EarlyValidConnectionType      uint16 = 0x0020
	EarlySupportMonitorLayoutPDU  uint16 = 0x0040
	EarlySupportNetcharAutodetect uint16 = 0x0080
	EarlySupportDynvcGFXProtocol  uint16 = 0x0100
	EarlySupportDynamicTimeZone   uint16 = 0x0200
	EarlySupportHeartbeatPDU      uint16 = 0x0400
	EarlySupportSkipChannelJoin   uint16 = 0x0800
)

// Client core supportedColorDepths flags.
const (
	Support24BPP uint16 = 0x0001
	Support16BPP uint16 = 0x0002
	Support15BPP uint16 = 0x0004
	Support32BPP uint16 = 0x0008
)

// The core block's optional fields, in the order they come. A core block
// carries the first ClientCore.OptionalFields of them.
const (
	OptPostBeta2ColorDepth = iota + 1
	OptClientProductID
	OptSerialNumber
	OptHighColorDepth
	OptSupportedColorDepths
	OptEarlyCapabilityFlags
	OptClientDigProductID
	OptConnectionType
	OptPad1Octet
	OptServerSelectedProtocol
	OptDesktopPhysicalWidth
	OptDesktopPhysicalHeight
	OptDesktopOrientation
	OptDesktopScaleFactor
	OptDeviceScaleFactor
	// OptAll is the number of optional fields.
	OptAll = OptDeviceScaleFactor
)

// coreFixedLen is the size of a core block without optional fields, header
// included; optSizes are the sizes of the optional fields.
const coreFixedLen = headerLen + 128

var optSizes = [OptAll]int{2, 2, 4, 2, 2, 2, 64, 1, 1, 4, 4, 4, 2, 4, 4}

// ClientCoreLen is the size of a core block, header included, that carries
// the first n optional fields: the lengths a client may send.
func ClientCoreLen(n int) int {
	l := coreFixedLen
	for _, s := range optSizes[:min(max(n, 0), OptAll)] {
		l += s
	}
	return l
}

// ClientCore is the client's core data (TS_UD_CS_CORE, MS-RDPBCGR
// 2.2.1.3.2). The byte fields point into the input; the fields past
// OptionalFields are zero.
type ClientCore struct {
	Version             uint32
	DesktopWidth        uint16
	DesktopHeight       uint16
	ColorDepth          uint16
	SASSequence         uint16
	KeyboardLayout      uint32
	ClientBuild         uint32
	ClientName          []byte // 32 bytes of UTF-16LE
	KeyboardType        uint32
	KeyboardSubType     uint32
	KeyboardFunctionKey uint32
	IMEFileName         []byte // 64 bytes of UTF-16LE

	OptionalFields         int // how many of the optional fields follow
	PostBeta2ColorDepth    uint16
	ClientProductID        uint16
	SerialNumber           uint32
	HighColorDepth         uint16
	SupportedColorDepths   uint16
	EarlyCapabilityFlags   uint16
	ClientDigProductID     []byte // 64 bytes of UTF-16LE
	ConnectionType         uint8
	Pad1Octet              uint8
	ServerSelectedProtocol uint32
	DesktopPhysicalWidth   uint32
	DesktopPhysicalHeight  uint32
	DesktopOrientation     uint16
	DesktopScaleFactor     uint32
	DeviceScaleFactor      uint32
}

// Has reports whether the block carries optional field f (one of the Opt
// constants).
func (c *ClientCore) Has(f int) bool { return c.OptionalFields >= f }

// Early reports whether the client set flag in earlyCapabilityFlags.
func (c *ClientCore) Early(flag uint16) bool {
	return c.Has(OptEarlyCapabilityFlags) && c.EarlyCapabilityFlags&flag != 0
}

// Name returns clientName as a string.
func (c *ClientCore) Name() string { return utf16String(c.ClientName) }

// PhysicalSize returns the desktop's physical size in millimeters, and false
// when the client did not send it or sent a value outside 10 to 10000, which
// MS-RDPBCGR says to ignore.
func (c *ClientCore) PhysicalSize() (width, height uint32, ok bool) {
	in := func(v uint32) bool { return v >= 10 && v <= 10000 }
	if !c.Has(OptDesktopPhysicalHeight) || !in(c.DesktopPhysicalWidth) || !in(c.DesktopPhysicalHeight) {
		return 0, 0, false
	}
	return c.DesktopPhysicalWidth, c.DesktopPhysicalHeight, true
}

// ScaleFactors returns the desktop and device scale factors in percent, and
// false when the client did not send both or sent values MS-RDPBCGR says to
// ignore (desktop outside 100 to 500, device not 100, 140 or 180).
func (c *ClientCore) ScaleFactors() (desktop, device uint32, ok bool) {
	if !c.Has(OptDeviceScaleFactor) || c.DesktopScaleFactor < 100 || c.DesktopScaleFactor > 500 {
		return 0, 0, false
	}
	switch c.DeviceScaleFactor {
	case 100, 140, 180:
		return c.DesktopScaleFactor, c.DeviceScaleFactor, true
	}
	return 0, 0, false
}

// utf16String decodes a NUL-terminated UTF-16LE field.
func utf16String(b []byte) string {
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

func readClientCore(r *wire.Reader, c *ClientCore) error {
	var err error
	if c.Version, err = r.U32LE(); err != nil {
		return err
	}
	if c.DesktopWidth, err = r.U16LE(); err != nil {
		return err
	}
	if c.DesktopHeight, err = r.U16LE(); err != nil {
		return err
	}
	if c.ColorDepth, err = r.U16LE(); err != nil {
		return err
	}
	if c.SASSequence, err = r.U16LE(); err != nil {
		return err
	}
	if c.KeyboardLayout, err = r.U32LE(); err != nil {
		return err
	}
	if c.ClientBuild, err = r.U32LE(); err != nil {
		return err
	}
	if c.ClientName, err = r.Bytes(32); err != nil {
		return err
	}
	if c.KeyboardType, err = r.U32LE(); err != nil {
		return err
	}
	if c.KeyboardSubType, err = r.U32LE(); err != nil {
		return err
	}
	if c.KeyboardFunctionKey, err = r.U32LE(); err != nil {
		return err
	}
	if c.IMEFileName, err = r.Bytes(64); err != nil {
		return err
	}
	// Each optional field is present only if all before it are: the block
	// ends at a field boundary.
	n := 0
	for left := r.Remaining(); n < OptAll && left >= optSizes[n]; n++ {
		left -= optSizes[n]
	}
	if ClientCoreLen(n)-coreFixedLen != r.Remaining() {
		return errors.New(TagBlockLengthInvalid, "core block ends inside an optional field", "type", TypeClientCore, "fields", n, "remaining", r.Remaining())
	}
	c.OptionalFields = n
	for f := 1; f <= n; f++ {
		switch f {
		case OptPostBeta2ColorDepth:
			c.PostBeta2ColorDepth, err = r.U16LE()
		case OptClientProductID:
			c.ClientProductID, err = r.U16LE()
		case OptSerialNumber:
			c.SerialNumber, err = r.U32LE()
		case OptHighColorDepth:
			c.HighColorDepth, err = r.U16LE()
		case OptSupportedColorDepths:
			c.SupportedColorDepths, err = r.U16LE()
		case OptEarlyCapabilityFlags:
			c.EarlyCapabilityFlags, err = r.U16LE()
		case OptClientDigProductID:
			c.ClientDigProductID, err = r.Bytes(64)
		case OptConnectionType:
			c.ConnectionType, err = r.U8()
		case OptPad1Octet:
			c.Pad1Octet, err = r.U8()
		case OptServerSelectedProtocol:
			c.ServerSelectedProtocol, err = r.U32LE()
		case OptDesktopPhysicalWidth:
			c.DesktopPhysicalWidth, err = r.U32LE()
		case OptDesktopPhysicalHeight:
			c.DesktopPhysicalHeight, err = r.U32LE()
		case OptDesktopOrientation:
			c.DesktopOrientation, err = r.U16LE()
		case OptDesktopScaleFactor:
			c.DesktopScaleFactor, err = r.U32LE()
		case OptDeviceScaleFactor:
			c.DeviceScaleFactor, err = r.U32LE()
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// fixed writes p into exactly n bytes, cut or padded with zeros.
func fixed(w *wire.Writer, p []byte, n int) {
	p = p[:min(len(p), n)]
	w.Write(p)
	w.Zeros(n - len(p))
}

func writeClientCore(w *wire.Writer, c *ClientCore) error {
	if c.OptionalFields < 0 || c.OptionalFields > OptAll {
		return errors.New(TagOptionalFieldsInvalid, "core block with an impossible number of optional fields", "fields", c.OptionalFields)
	}
	w.U16LE(TypeClientCore)
	w.U16LE(uint16(ClientCoreLen(c.OptionalFields)))
	w.U32LE(c.Version)
	w.U16LE(c.DesktopWidth)
	w.U16LE(c.DesktopHeight)
	w.U16LE(c.ColorDepth)
	w.U16LE(c.SASSequence)
	w.U32LE(c.KeyboardLayout)
	w.U32LE(c.ClientBuild)
	fixed(w, c.ClientName, 32)
	w.U32LE(c.KeyboardType)
	w.U32LE(c.KeyboardSubType)
	w.U32LE(c.KeyboardFunctionKey)
	fixed(w, c.IMEFileName, 64)
	for f := 1; f <= c.OptionalFields; f++ {
		switch f {
		case OptPostBeta2ColorDepth:
			w.U16LE(c.PostBeta2ColorDepth)
		case OptClientProductID:
			w.U16LE(c.ClientProductID)
		case OptSerialNumber:
			w.U32LE(c.SerialNumber)
		case OptHighColorDepth:
			w.U16LE(c.HighColorDepth)
		case OptSupportedColorDepths:
			w.U16LE(c.SupportedColorDepths)
		case OptEarlyCapabilityFlags:
			w.U16LE(c.EarlyCapabilityFlags)
		case OptClientDigProductID:
			fixed(w, c.ClientDigProductID, 64)
		case OptConnectionType:
			w.U8(c.ConnectionType)
		case OptPad1Octet:
			w.U8(c.Pad1Octet)
		case OptServerSelectedProtocol:
			w.U32LE(c.ServerSelectedProtocol)
		case OptDesktopPhysicalWidth:
			w.U32LE(c.DesktopPhysicalWidth)
		case OptDesktopPhysicalHeight:
			w.U32LE(c.DesktopPhysicalHeight)
		case OptDesktopOrientation:
			w.U16LE(c.DesktopOrientation)
		case OptDesktopScaleFactor:
			w.U32LE(c.DesktopScaleFactor)
		case OptDeviceScaleFactor:
			w.U32LE(c.DeviceScaleFactor)
		}
	}
	return nil
}

// ClientSecurity is the client's security data (TS_UD_CS_SEC, 2.2.1.3.3).
type ClientSecurity struct {
	EncryptionMethods    uint32
	ExtEncryptionMethods uint32
}

// MaxChannels is the most static virtual channels a client may ask for.
const MaxChannels = 31

// Static virtual channel options (CHANNEL_DEF, 2.2.1.3.4.1).
const (
	ChannelOptionInitialized  uint32 = 0x80000000
	ChannelOptionEncryptRDP   uint32 = 0x40000000
	ChannelOptionCompressRDP  uint32 = 0x00800000
	ChannelOptionShowProtocol uint32 = 0x00200000
)

// ChannelDef is a static virtual channel the client asks for.
type ChannelDef struct {
	Name    [8]byte // 7 ANSI characters and a NUL
	Options uint32
}

// NameString returns the channel's name up to its NUL.
func (c *ChannelDef) NameString() string {
	for i, b := range c.Name {
		if b == 0 {
			return string(c.Name[:i])
		}
	}
	return string(c.Name[:])
}

// ClientNetwork is the client's network data (TS_UD_CS_NET, 2.2.1.3.4): the
// static virtual channels it asks for.
type ClientNetwork struct {
	ChannelCount int
	Channels     [MaxChannels]ChannelDef
}

// ClientCluster is the client's cluster data (TS_UD_CS_CLUSTER, 2.2.1.3.5).
type ClientCluster struct {
	Flags               uint32
	RedirectedSessionID uint32
}

// MaxMonitors is the most monitors a monitor block may hold.
const MaxMonitors = 16

// MonitorPrimary marks the primary monitor (TS_MONITOR_DEF flags).
const MonitorPrimary uint32 = 0x00000001

// MonitorDef is one of the client's monitors (TS_MONITOR_DEF, 2.2.1.3.6.1),
// in virtual desktop coordinates, the primary monitor's top-left at (0, 0).
type MonitorDef struct {
	Left, Top, Right, Bottom int32 // inclusive
	Flags                    uint32
}

// ClientMonitor is the client's monitor data (TS_UD_CS_MONITOR, 2.2.1.3.6).
type ClientMonitor struct {
	Flags        uint32
	MonitorCount int
	Monitors     [MaxMonitors]MonitorDef
}

// ClientMessageChannel is the client's message channel data
// (TS_UD_CS_MCS_MSGCHANNEL, 2.2.1.3.7).
type ClientMessageChannel struct {
	Flags uint32
}

// ClientMultitransport is the client's multitransport data
// (TS_UD_CS_MULTITRANSPORT, 2.2.1.3.8).
type ClientMultitransport struct {
	Flags uint32
}

// ClientData are the client's data blocks. Has* say which came; Core always
// comes. Blocks of other types (monitor extended data, unused data, types
// added later) are skipped.
type ClientData struct {
	Core              ClientCore
	HasSecurity       bool
	Security          ClientSecurity
	HasNetwork        bool
	Network           ClientNetwork
	HasCluster        bool
	Cluster           ClientCluster
	HasMonitor        bool
	Monitor           ClientMonitor
	HasMessageChannel bool
	MessageChannel    ClientMessageChannel
	HasMultitransport bool
	Multitransport    ClientMultitransport
}

// readBlockHeader reads a block's header and returns its type and a reader
// over its contents.
func readBlockHeader(r *wire.Reader) (uint16, wire.Reader, error) {
	typ, err := r.U16LE()
	if err != nil {
		return 0, wire.Reader{}, err
	}
	n, err := r.U16LE()
	if err != nil {
		return 0, wire.Reader{}, err
	}
	if n < headerLen || int(n)-headerLen > r.Remaining() {
		return 0, wire.Reader{}, errors.New(TagBlockLengthInvalid, "data block length outside its header and the user data", "type", typ, "length", n, "left", r.Remaining())
	}
	s, err := r.Sub(int(n) - headerLen)
	return typ, s, err
}

// fixedBlock checks that a block of a fixed size has exactly that size.
func fixedBlock(s *wire.Reader, typ uint16, n int) error {
	if s.Remaining() != n {
		return errors.New(TagBlockLengthInvalid, "data block of the wrong length", "type", typ, "length", s.Remaining()+headerLen, "want", n+headerLen)
	}
	return nil
}

// readU32s reads a block made of exactly the 32-bit fields vs.
func readU32s(s *wire.Reader, typ uint16, vs ...*uint32) error {
	if err := fixedBlock(s, typ, 4*len(vs)); err != nil {
		return err
	}
	for _, v := range vs {
		var err error
		if *v, err = s.U32LE(); err != nil {
			return err
		}
	}
	return nil
}

// ParseClientData reads the client's data blocks from the user data of its
// Conference Create Request.
func ParseClientData(b []byte) (ClientData, error) {
	var cd ClientData
	hasCore := false
	r := wire.NewReader(b)
	for r.Remaining() > 0 {
		typ, s, err := readBlockHeader(&r)
		if err != nil {
			return cd, err
		}
		var seen *bool
		switch typ {
		case TypeClientCore:
			seen = &hasCore
		case TypeClientSecurity:
			seen = &cd.HasSecurity
		case TypeClientNetwork:
			seen = &cd.HasNetwork
		case TypeClientCluster:
			seen = &cd.HasCluster
		case TypeClientMonitor:
			seen = &cd.HasMonitor
		case TypeClientMessageChannel:
			seen = &cd.HasMessageChannel
		case TypeClientMultitransport:
			seen = &cd.HasMultitransport
		default:
			continue
		}
		if *seen {
			return cd, errors.New(TagBlockDuplicate, "client data block that comes twice", "type", typ)
		}
		*seen = true
		switch typ {
		case TypeClientCore:
			err = readClientCore(&s, &cd.Core)
		case TypeClientSecurity:
			err = readU32s(&s, typ, &cd.Security.EncryptionMethods, &cd.Security.ExtEncryptionMethods)
		case TypeClientNetwork:
			err = readClientNetwork(&s, &cd.Network)
		case TypeClientCluster:
			err = readU32s(&s, typ, &cd.Cluster.Flags, &cd.Cluster.RedirectedSessionID)
		case TypeClientMonitor:
			err = readClientMonitor(&s, &cd.Monitor)
		case TypeClientMessageChannel:
			err = readU32s(&s, typ, &cd.MessageChannel.Flags)
		case TypeClientMultitransport:
			err = readU32s(&s, typ, &cd.Multitransport.Flags)
		}
		if err != nil {
			return cd, err
		}
	}
	if !hasCore {
		return cd, errors.New(TagCoreMissing, "client data without a core block")
	}
	return cd, nil
}

func readClientNetwork(s *wire.Reader, n *ClientNetwork) error {
	count, err := s.U32LE()
	if err != nil {
		return err
	}
	if count > MaxChannels {
		return errors.New(TagChannelCountInvalid, "more static channels than 31", "count", count)
	}
	if err := fixedBlock(s, TypeClientNetwork, 12*int(count)); err != nil {
		return err
	}
	n.ChannelCount = int(count)
	for i := range n.ChannelCount {
		name, err := s.Bytes(8)
		if err != nil {
			return err
		}
		copy(n.Channels[i].Name[:], name)
		if n.Channels[i].Options, err = s.U32LE(); err != nil {
			return err
		}
	}
	return nil
}

func readClientMonitor(s *wire.Reader, m *ClientMonitor) error {
	var err error
	if m.Flags, err = s.U32LE(); err != nil {
		return err
	}
	count, err := s.U32LE()
	if err != nil {
		return err
	}
	if count == 0 || count > MaxMonitors {
		return errors.New(TagMonitorCountInvalid, "monitor count not 1 to 16", "count", count)
	}
	if err := fixedBlock(s, TypeClientMonitor, 20*int(count)); err != nil {
		return err
	}
	m.MonitorCount = int(count)
	for i := range m.MonitorCount {
		d := &m.Monitors[i]
		for _, v := range []*int32{&d.Left, &d.Top, &d.Right, &d.Bottom} {
			u, err := s.U32LE()
			if err != nil {
				return err
			}
			*v = int32(u)
		}
		if d.Flags, err = s.U32LE(); err != nil {
			return err
		}
	}
	return nil
}

// WriteClientData appends cd's blocks in the order Microsoft's example has
// them (core, cluster, security, network), then monitor, message channel and
// multitransport. A client writes it; the server's tests write it to read it
// back.
func WriteClientData(w *wire.Writer, cd *ClientData) error {
	if err := writeClientCore(w, &cd.Core); err != nil {
		return err
	}
	if cd.HasCluster {
		w.U16LE(TypeClientCluster)
		w.U16LE(headerLen + 8)
		w.U32LE(cd.Cluster.Flags)
		w.U32LE(cd.Cluster.RedirectedSessionID)
	}
	if cd.HasSecurity {
		w.U16LE(TypeClientSecurity)
		w.U16LE(headerLen + 8)
		w.U32LE(cd.Security.EncryptionMethods)
		w.U32LE(cd.Security.ExtEncryptionMethods)
	}
	if cd.HasNetwork {
		n := &cd.Network
		if n.ChannelCount < 0 || n.ChannelCount > MaxChannels {
			return errors.New(TagChannelCountInvalid, "static channel count not 0 to 31", "count", n.ChannelCount)
		}
		w.U16LE(TypeClientNetwork)
		w.U16LE(uint16(headerLen + 4 + 12*n.ChannelCount))
		w.U32LE(uint32(n.ChannelCount))
		for i := range n.ChannelCount {
			w.Write(n.Channels[i].Name[:])
			w.U32LE(n.Channels[i].Options)
		}
	}
	if cd.HasMonitor {
		m := &cd.Monitor
		if m.MonitorCount < 1 || m.MonitorCount > MaxMonitors {
			return errors.New(TagMonitorCountInvalid, "monitor count not 1 to 16", "count", m.MonitorCount)
		}
		w.U16LE(TypeClientMonitor)
		w.U16LE(uint16(headerLen + 8 + 20*m.MonitorCount))
		w.U32LE(m.Flags)
		w.U32LE(uint32(m.MonitorCount))
		for i := range m.MonitorCount {
			d := &m.Monitors[i]
			w.U32LE(uint32(d.Left))
			w.U32LE(uint32(d.Top))
			w.U32LE(uint32(d.Right))
			w.U32LE(uint32(d.Bottom))
			w.U32LE(d.Flags)
		}
	}
	if cd.HasMessageChannel {
		w.U16LE(TypeClientMessageChannel)
		w.U16LE(headerLen + 4)
		w.U32LE(cd.MessageChannel.Flags)
	}
	if cd.HasMultitransport {
		w.U16LE(TypeClientMultitransport)
		w.U16LE(headerLen + 4)
		w.U32LE(cd.Multitransport.Flags)
	}
	return nil
}
