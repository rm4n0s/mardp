package gcc

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// TagSecurityInvalid: server security data whose random or certificate
// lengths do not fit its encryption method and level.
const TagSecurityInvalid = "GCCSecurityInvalid"

// Server core earlyCapabilityFlags (MS-RDPBCGR 2.2.1.4.2).
const (
	ServerEarlyEdgeActionsV1          uint32 = 0x00000001
	ServerEarlyDynamicDSTSupported    uint32 = 0x00000002
	ServerEarlyEdgeActionsV2          uint32 = 0x00000004
	ServerEarlySkipChannelJoinSupport uint32 = 0x00000008
)

// RDP versions of the core blocks' version field.
const (
	VersionRDP4  uint32 = 0x00080001
	VersionRDP5  uint32 = 0x00080004 // 5.0 to 8.1
	VersionRDP10 uint32 = 0x00080005
)

// ServerCore is the server's core data (TS_UD_SC_CORE, 2.2.1.4.2).
// OptionalFields is how many of clientRequestedProtocols and
// earlyCapabilityFlags it carries, in that order.
type ServerCore struct {
	Version                  uint32
	OptionalFields           int
	ClientRequestedProtocols uint32
	EarlyCapabilityFlags     uint32
}

// Encryption methods and levels of the security data (2.2.1.4.3). With
// TLS (Enhanced RDP Security) both are none.
const (
	EncryptionMethodNone uint32 = 0
	EncryptionLevelNone  uint32 = 0
)

// ServerSecurity is the server's security data (TS_UD_SC_SEC1, 2.2.1.4.3).
// With method and level both none it carries neither a random nor a
// certificate.
type ServerSecurity struct {
	EncryptionMethod  uint32
	EncryptionLevel   uint32
	ServerRandom      []byte
	ServerCertificate []byte
}

// ServerNetwork is the server's network data (TS_UD_SC_NET, 2.2.1.4.4): the
// I/O channel's ID and one channel ID for each channel the client asked for,
// in the same order.
type ServerNetwork struct {
	MCSChannelID uint16
	ChannelCount int
	ChannelIDs   [MaxChannels]uint16
}

// ServerData are the server's data blocks. The server writes all three.
// Blocks of other types (message channel, multitransport) are skipped when
// read.
type ServerData struct {
	Core        ServerCore
	HasSecurity bool
	Security    ServerSecurity
	HasNetwork  bool
	Network     ServerNetwork
}

// WriteServerData appends sd's blocks in the order Microsoft's example has
// them: core, network, security.
func WriteServerData(w *wire.Writer, sd *ServerData) error {
	c := &sd.Core
	if c.OptionalFields < 0 || c.OptionalFields > 2 {
		return errors.New(TagOptionalFieldsInvalid, "server core block with an impossible number of optional fields", "fields", c.OptionalFields)
	}
	w.U16LE(TypeServerCore)
	w.U16LE(uint16(headerLen + 4 + 4*c.OptionalFields))
	w.U32LE(c.Version)
	if c.OptionalFields >= 1 {
		w.U32LE(c.ClientRequestedProtocols)
	}
	if c.OptionalFields >= 2 {
		w.U32LE(c.EarlyCapabilityFlags)
	}
	if sd.HasNetwork {
		n := &sd.Network
		if n.ChannelCount < 0 || n.ChannelCount > MaxChannels {
			return errors.New(TagChannelCountInvalid, "static channel count not 0 to 31", "count", n.ChannelCount)
		}
		pad := n.ChannelCount % 2
		w.U16LE(TypeServerNetwork)
		w.U16LE(uint16(headerLen + 4 + 2*(n.ChannelCount+pad)))
		w.U16LE(n.MCSChannelID)
		w.U16LE(uint16(n.ChannelCount))
		for _, id := range n.ChannelIDs[:n.ChannelCount] {
			w.U16LE(id)
		}
		if pad != 0 {
			w.U16LE(0)
		}
	}
	if sd.HasSecurity {
		s := &sd.Security
		none := s.EncryptionMethod == EncryptionMethodNone && s.EncryptionLevel == EncryptionLevelNone
		if none && (len(s.ServerRandom) != 0 || len(s.ServerCertificate) != 0) {
			return errors.New(TagSecurityInvalid, "server random or certificate without encryption", "random", len(s.ServerRandom), "certificate", len(s.ServerCertificate))
		}
		n := headerLen + 8
		if !none {
			n += 8 + len(s.ServerRandom) + len(s.ServerCertificate)
		}
		if n > 0xffff {
			return errors.New(TagSecurityInvalid, "server security block longer than 65535 bytes", "length", n)
		}
		w.U16LE(TypeServerSecurity)
		w.U16LE(uint16(n))
		w.U32LE(s.EncryptionMethod)
		w.U32LE(s.EncryptionLevel)
		if !none {
			w.U32LE(uint32(len(s.ServerRandom)))
			w.U32LE(uint32(len(s.ServerCertificate)))
			w.Write(s.ServerRandom)
			w.Write(s.ServerCertificate)
		}
	}
	return nil
}

// ParseServerData reads the server's data blocks from the user data of its
// Conference Create Response. A client reads them; the server's tests read
// what it wrote.
func ParseServerData(b []byte) (ServerData, error) {
	var sd ServerData
	hasCore := false
	r := wire.NewReader(b)
	for r.Remaining() > 0 {
		typ, s, err := readBlockHeader(&r)
		if err != nil {
			return sd, err
		}
		var seen *bool
		switch typ {
		case TypeServerCore:
			seen = &hasCore
		case TypeServerSecurity:
			seen = &sd.HasSecurity
		case TypeServerNetwork:
			seen = &sd.HasNetwork
		default:
			continue
		}
		if *seen {
			return sd, errors.New(TagBlockDuplicate, "server data block that comes twice", "type", typ)
		}
		*seen = true
		switch typ {
		case TypeServerCore:
			err = readServerCore(&s, &sd.Core)
		case TypeServerSecurity:
			err = readServerSecurity(&s, &sd.Security)
		case TypeServerNetwork:
			err = readServerNetwork(&s, &sd.Network)
		}
		if err != nil {
			return sd, err
		}
	}
	if !hasCore {
		return sd, errors.New(TagCoreMissing, "server data without a core block")
	}
	return sd, nil
}

func readServerCore(s *wire.Reader, c *ServerCore) error {
	switch s.Remaining() {
	case 4, 8, 12:
	default:
		return errors.New(TagBlockLengthInvalid, "server core block of the wrong length", "type", TypeServerCore, "length", s.Remaining()+headerLen)
	}
	c.OptionalFields = s.Remaining()/4 - 1
	var err error
	if c.Version, err = s.U32LE(); err != nil {
		return err
	}
	if c.OptionalFields >= 1 {
		if c.ClientRequestedProtocols, err = s.U32LE(); err != nil {
			return err
		}
	}
	if c.OptionalFields >= 2 {
		if c.EarlyCapabilityFlags, err = s.U32LE(); err != nil {
			return err
		}
	}
	return nil
}

func readServerSecurity(s *wire.Reader, sec *ServerSecurity) error {
	var err error
	if sec.EncryptionMethod, err = s.U32LE(); err != nil {
		return err
	}
	if sec.EncryptionLevel, err = s.U32LE(); err != nil {
		return err
	}
	if sec.EncryptionMethod == EncryptionMethodNone && sec.EncryptionLevel == EncryptionLevelNone {
		return fixedBlock(s, TypeServerSecurity, 0)
	}
	randomLen, err := s.U32LE()
	if err != nil {
		return err
	}
	certLen, err := s.U32LE()
	if err != nil {
		return err
	}
	if uint64(randomLen)+uint64(certLen) != uint64(s.Remaining()) {
		return errors.New(TagSecurityInvalid, "server random and certificate lengths do not match the block", "random", randomLen, "certificate", certLen, "remaining", s.Remaining())
	}
	if sec.ServerRandom, err = s.Bytes(int(randomLen)); err != nil {
		return err
	}
	sec.ServerCertificate, err = s.Bytes(int(certLen))
	return err
}

func readServerNetwork(s *wire.Reader, n *ServerNetwork) error {
	var err error
	if n.MCSChannelID, err = s.U16LE(); err != nil {
		return err
	}
	count, err := s.U16LE()
	if err != nil {
		return err
	}
	if count > MaxChannels {
		return errors.New(TagChannelCountInvalid, "more static channels than 31", "count", count)
	}
	if err := fixedBlock(s, TypeServerNetwork, 2*(int(count)+int(count)%2)); err != nil {
		return err
	}
	n.ChannelCount = int(count)
	for i := range n.ChannelCount {
		if n.ChannelIDs[i], err = s.U16LE(); err != nil {
			return err
		}
	}
	return nil
}
