package caps

import (
	"encoding/binary"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Capability set types (MS-RDPBCGR 2.2.1.13.1.1.1; the Frame Acknowledge
// set is MS-RDPRFX 2.2.1.3).
const (
	SetGeneral                uint16 = 0x01
	SetBitmap                 uint16 = 0x02
	SetOrder                  uint16 = 0x03
	SetBitmapCache            uint16 = 0x04
	SetControl                uint16 = 0x05
	SetActivation             uint16 = 0x07
	SetPointer                uint16 = 0x08
	SetShare                  uint16 = 0x09
	SetColorCache             uint16 = 0x0a
	SetSound                  uint16 = 0x0c
	SetInput                  uint16 = 0x0d
	SetFont                   uint16 = 0x0e
	SetBrush                  uint16 = 0x0f
	SetGlyphCache             uint16 = 0x10
	SetOffscreenCache         uint16 = 0x11
	SetBitmapCacheHostSupport uint16 = 0x12
	SetBitmapCacheRev2        uint16 = 0x13
	SetVirtualChannel         uint16 = 0x14
	SetDrawNineGridCache      uint16 = 0x15
	SetDrawGDIPlus            uint16 = 0x16
	SetRAIL                   uint16 = 0x17
	SetWindow                 uint16 = 0x18
	SetCompDesk               uint16 = 0x19
	SetMultifragmentUpdate    uint16 = 0x1a
	SetLargePointer           uint16 = 0x1b
	SetSurfaceCommands        uint16 = 0x1c
	SetBitmapCodecs           uint16 = 0x1d
	SetFrameAcknowledge       uint16 = 0x1e
	setHeaderLen                     = 4
	maxSetType                       = 63 // Caps.Sets records types up to this one
)

// General Capability Set values (2.2.7.1.1).
const (
	OSMajorWindows   uint16 = 0x0001
	OSMajorMacintosh uint16 = 0x0003
	OSMajorUnix      uint16 = 0x0004
	OSMinorWindowsNT uint16 = 0x0003
	OSMinorNativeX   uint16 = 0x0007

	ExtraFastPathOutput  uint16 = 0x0001
	ExtraLongCredentials uint16 = 0x0004
	ExtraAutoReconnect   uint16 = 0x0008
	ExtraSaltedChecksum  uint16 = 0x0010
	ExtraNoBitmapCompHdr uint16 = 0x0400
	capsProtocolVersion  uint16 = 0x0200
)

// Input Capability Set flags (2.2.7.1.6).
const (
	InputScancodes     uint16 = 0x0001
	InputMouseX        uint16 = 0x0004
	InputFastPath      uint16 = 0x0008
	InputUnicode       uint16 = 0x0010
	InputFastPath2     uint16 = 0x0020
	InputMouseRelative uint16 = 0x0080
	InputMouseHWheel   uint16 = 0x0100
	InputQoETimestamps uint16 = 0x0200
)

// Large Pointer Capability Set flags (2.2.7.2.7).
const (
	LargePointer96  uint16 = 0x0001
	LargePointer384 uint16 = 0x0002
)

// Surface Commands Capability Set flags (2.2.7.2.9).
const (
	SurfaceSetSurfaceBits    uint32 = 0x00000002
	SurfaceFrameMarker       uint32 = 0x00000010
	SurfaceStreamSurfaceBits uint32 = 0x00000040
)

// Other values of the capability sets.
const (
	SoundBeeps        uint16 = 0x0001 // soundFlags, 2.2.7.1.11
	FontSupportList   uint16 = 0x0001 // fontSupportFlags, 2.2.7.2.5
	VCCompressionSC   uint32 = 0x0001 // VCCAPS_COMPR_SC, 2.2.7.1.10
	VCCompressionCS8K uint32 = 0x0002 // VCCAPS_COMPR_CS_8K
	orderNegotiate    uint16 = 0x0002 // NEGOTIATEORDERSUPPORT, 2.2.7.1.3
	orderZeroBounds   uint16 = 0x0008 // ZEROBOUNDSDELTASSUPPORT
)

// General is the General Capability Set (2.2.7.1.1).
type General struct {
	OSMajor, OSMinor uint16
	// ExtraFlags holds the Extra flags.
	ExtraFlags uint16
	// RefreshRect and SuppressOutput are the server's: whether it takes
	// the Refresh Rect and Suppress Output PDUs.
	RefreshRect, SuppressOutput bool
}

// Bitmap is the Bitmap Capability Set (2.2.7.1.2).
type Bitmap struct {
	BitsPerPixel                uint16
	DesktopWidth, DesktopHeight uint16
	// DesktopResize says whether the sender supports a new desktop size
	// through the Deactivation-Reactivation Sequence.
	DesktopResize bool
	DrawingFlags  uint8
}

// Pointer is the Pointer Capability Set (2.2.7.1.5).
type Pointer struct {
	Color          bool
	ColorCacheSize uint16
	// CacheSize is pointerCacheSize; HasCacheSize says whether the set
	// has the field, which older clients leave out.
	CacheSize    uint16
	HasCacheSize bool
}

// Input is the Input Capability Set (2.2.7.1.6), without the IME file
// name. The keyboard fields are the client's; the server sends zeros.
type Input struct {
	// Flags holds the Input flags.
	Flags                uint16
	KeyboardLayout       uint32
	KeyboardType         uint32
	KeyboardSubType      uint32
	KeyboardFunctionKeys uint32
}

// VirtualChannel is the Virtual Channel Capability Set (2.2.7.1.10).
type VirtualChannel struct {
	Flags uint32
	// ChunkSize is VCChunkSize, 0 when the set leaves it out.
	ChunkSize uint32
}

// Caps holds the capability sets of a Demand Active or Confirm Active PDU
// that the server writes or reads. A set that a PDU carries and Caps has no
// field for is skipped.
type Caps struct {
	// Sets has bit 1<<type for every set a PDU carries, or is to carry.
	// Writing includes only the sets this package knows how to write: those
	// with a field below, and the Order set (with no orders).
	Sets           uint64
	General        General
	Bitmap         Bitmap
	Pointer        Pointer
	Input          Input
	VirtualChannel VirtualChannel
	// SoundFlags is soundFlags of the Sound set.
	SoundFlags uint16
	// ShareNodeID is nodeID of the Share set.
	ShareNodeID uint16
	// FontFlags is fontSupportFlags of the Font set.
	FontFlags uint16
	// MaxRequestSize is MaxRequestSize of the Multifragment Update set.
	MaxRequestSize uint32
	// LargePointer is largePointerSupportFlags of the Large Pointer set.
	LargePointer uint16
	// SurfaceCommands is cmdFlags of the Surface Commands set.
	SurfaceCommands uint32
	// MaxUnacknowledgedFrames is maxUnacknowledgedFrameCount of the Frame
	// Acknowledge set.
	MaxUnacknowledgedFrames uint32
}

// Has says whether the PDU carries the set of type t.
func (c *Caps) Has(t uint16) bool {
	return t <= maxSetType && c.Sets&(1<<t) != 0
}

// Add marks the set of type t as carried.
func (c *Caps) Add(t uint16) {
	if t <= maxSetType {
		c.Sets |= 1 << t
	}
}

// writeOrder is the sets Write writes, in this order.
var writeOrder = [...]uint16{
	SetGeneral, SetBitmap, SetOrder, SetPointer, SetInput, SetVirtualChannel, SetSound,
	SetShare, SetFont, SetMultifragmentUpdate, SetLargePointer, SetSurfaceCommands, SetFrameAcknowledge,
}

// writeSets appends numberCapabilities, pad2Octets and the sets c carries
// that this package writes.
func writeSets(w *wire.Writer, c *Caps) error {
	n := w.Reserve(2)
	w.U16LE(0) // pad2Octets
	count := 0
	for _, t := range writeOrder {
		if !c.Has(t) {
			continue
		}
		if err := writeSet(w, t, c); err != nil {
			return err
		}
		count++
	}
	return w.PatchU16LE(n, uint16(count))
}

// writeSet appends the set of type t.
func writeSet(w *wire.Writer, t uint16, c *Caps) error {
	off := w.Len()
	w.U16LE(t)
	w.U16LE(0) // lengthCapability, filled in below
	switch t {
	case SetGeneral:
		g := &c.General
		w.U16LE(g.OSMajor)
		w.U16LE(g.OSMinor)
		w.U16LE(capsProtocolVersion)
		w.U16LE(0) // pad2octetsA
		w.U16LE(0) // compressionTypes
		w.U16LE(g.ExtraFlags)
		w.U16LE(0) // updateCapabilityFlag
		w.U16LE(0) // remoteUnshareFlag
		w.U16LE(0) // compressionLevel
		w.U8(boolByte(g.RefreshRect))
		w.U8(boolByte(g.SuppressOutput))
	case SetBitmap:
		b := &c.Bitmap
		w.U16LE(b.BitsPerPixel)
		w.U16LE(1) // receive1BitPerPixel
		w.U16LE(1) // receive4BitsPerPixel
		w.U16LE(1) // receive8BitsPerPixel
		w.U16LE(b.DesktopWidth)
		w.U16LE(b.DesktopHeight)
		w.U16LE(0) // pad2octets
		w.U16LE(uint16(boolByte(b.DesktopResize)))
		w.U16LE(1) // bitmapCompressionFlag: MUST be TRUE
		w.U8(0)    // highColorFlags
		w.U8(b.DrawingFlags)
		w.U16LE(1) // multipleRectangleSupport: MUST be TRUE
		w.U16LE(0) // pad2octetsB
	case SetOrder:
		// No drawing orders (SPEC §5.2): the orderSupport array is all
		// zeros, and the fields the client ignores have the values
		// 2.2.7.1.3 says it assumes.
		w.Zeros(16) // terminalDescriptor
		w.U32LE(0)  // pad4octetsA
		w.U16LE(1)  // desktopSaveXGranularity
		w.U16LE(20) // desktopSaveYGranularity
		w.U16LE(0)  // pad2octetsA
		w.U16LE(1)  // maximumOrderLevel: ORD_LEVEL_1_ORDERS
		w.U16LE(0)  // numberFonts
		w.U16LE(orderNegotiate | orderZeroBounds)
		w.Zeros(32)        // orderSupport
		w.U16LE(0)         // textFlags
		w.U16LE(0)         // orderSupportExFlags
		w.U32LE(0)         // pad4octetsB
		w.U32LE(480 * 480) // desktopSaveSize
		w.U16LE(0)         // pad2octetsC
		w.U16LE(0)         // pad2octetsD
		w.U16LE(0)         // textANSICodePage
		w.U16LE(0)         // pad2octetsE
	case SetPointer:
		p := &c.Pointer
		w.U16LE(uint16(boolByte(p.Color)))
		w.U16LE(p.ColorCacheSize)
		if p.HasCacheSize {
			w.U16LE(p.CacheSize)
		}
	case SetInput:
		in := &c.Input
		w.U16LE(in.Flags)
		w.U16LE(0) // pad2octetsA
		w.U32LE(in.KeyboardLayout)
		w.U32LE(in.KeyboardType)
		w.U32LE(in.KeyboardSubType)
		w.U32LE(in.KeyboardFunctionKeys)
		w.Zeros(64) // imeFileName
	case SetVirtualChannel:
		w.U32LE(c.VirtualChannel.Flags)
		if c.VirtualChannel.ChunkSize != 0 {
			w.U32LE(c.VirtualChannel.ChunkSize)
		}
	case SetSound:
		w.U16LE(c.SoundFlags)
		w.U16LE(0) // pad2octetsA
	case SetShare:
		w.U16LE(c.ShareNodeID)
		w.U16LE(0) // pad2octets
	case SetFont:
		w.U16LE(c.FontFlags)
		w.U16LE(0) // pad2octets
	case SetMultifragmentUpdate:
		w.U32LE(c.MaxRequestSize)
	case SetLargePointer:
		w.U16LE(c.LargePointer)
	case SetSurfaceCommands:
		w.U32LE(c.SurfaceCommands)
		w.U32LE(0) // reserved
	case SetFrameAcknowledge:
		w.U32LE(c.MaxUnacknowledgedFrames)
	}
	return w.PatchU16LE(off+2, uint16(w.Len()-off))
}

func boolByte(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// readSets reads numberCapabilities, pad2Octets and the sets from r, which
// holds lengthCombinedCapabilities bytes.
func readSets(r *wire.Reader) (Caps, error) {
	var c Caps
	n, err := r.U16LE()
	if err != nil {
		return c, err
	}
	if err := r.Skip(2); err != nil { // pad2Octets
		return c, err
	}
	for i := range int(n) {
		if r.Remaining() < setHeaderLen {
			return c, errors.New(TagSetInvalid, "fewer capability sets than numberCapabilities says",
				"numberCapabilities", n, "read", i, "left", r.Remaining())
		}
		t, err := r.U16LE()
		if err != nil {
			return c, err
		}
		l, err := r.U16LE()
		if err != nil {
			return c, err
		}
		if l < setHeaderLen || int(l)-setHeaderLen > r.Remaining() {
			return c, errors.New(TagSetInvalid, "a capability set whose length does not fit",
				"type", t, "lengthCapability", l, "left", r.Remaining()+setHeaderLen)
		}
		b, err := r.Bytes(int(l) - setHeaderLen)
		if err != nil {
			return c, err
		}
		if err := readSet(b, t, &c); err != nil {
			return c, err
		}
		c.Add(t)
	}
	return c, nil
}

// setMin is the size of the fields the server reads of each set it reads,
// after the set's header.
var setMin = map[uint16]int{
	SetGeneral:             18,
	SetBitmap:              24,
	SetPointer:             4,
	SetInput:               20,
	SetVirtualChannel:      4,
	SetSound:               2,
	SetShare:               2,
	SetMultifragmentUpdate: 4,
	SetLargePointer:        2,
	SetSurfaceCommands:     4,
	SetFrameAcknowledge:    4,
}

// readSet reads the fields of a set of type t from b, the set without its
// header. Sets the package does not read are skipped.
func readSet(b []byte, t uint16, c *Caps) error {
	if m, ok := setMin[t]; ok && len(b) < m {
		return errors.New(TagSetShort, "a capability set shorter than its fields",
			"type", t, "lengthCapability", len(b)+setHeaderLen, "min", m+setHeaderLen)
	}
	// setMin checked the fields read unconditionally; the optional ones are
	// checked where they are read.
	u16 := func(off int) uint16 { return binary.LittleEndian.Uint16(b[off:]) }
	u32 := func(off int) uint32 { return binary.LittleEndian.Uint32(b[off:]) }
	switch t {
	case SetGeneral:
		// osMajorType, osMinorType, protocolVersion, pad2octetsA,
		// compressionTypes, extraFlags, updateCapabilityFlag,
		// remoteUnshareFlag, compressionLevel, refreshRectSupport,
		// suppressOutputSupport.
		c.General = General{OSMajor: u16(0), OSMinor: u16(2), ExtraFlags: u16(10)}
		if len(b) >= 20 {
			c.General.RefreshRect, c.General.SuppressOutput = b[18] != 0, b[19] != 0
		}
	case SetBitmap:
		// preferredBitsPerPixel, receive1/4/8, desktopWidth, desktopHeight,
		// pad2octets, desktopResizeFlag, bitmapCompressionFlag,
		// highColorFlags, drawingFlags, multipleRectangleSupport, pad.
		c.Bitmap = Bitmap{
			BitsPerPixel: u16(0), DesktopWidth: u16(8), DesktopHeight: u16(10),
			DesktopResize: u16(14) != 0, DrawingFlags: b[19],
		}
	case SetPointer:
		c.Pointer = Pointer{Color: u16(0) != 0, ColorCacheSize: u16(2)}
		if len(b) >= 6 {
			c.Pointer.CacheSize, c.Pointer.HasCacheSize = u16(4), true
		}
	case SetInput:
		// inputFlags, pad2octetsA, then the four keyboard fields.
		c.Input = Input{
			Flags: u16(0), KeyboardLayout: u32(4), KeyboardType: u32(8),
			KeyboardSubType: u32(12), KeyboardFunctionKeys: u32(16),
		}
	case SetVirtualChannel:
		c.VirtualChannel = VirtualChannel{Flags: u32(0)}
		if len(b) >= 8 {
			c.VirtualChannel.ChunkSize = u32(4)
		}
	case SetSound:
		c.SoundFlags = u16(0)
	case SetShare:
		c.ShareNodeID = u16(0)
	case SetFont:
		// Microsoft's Demand Active example has a Font set of 4 bytes, with
		// no fontSupportFlags.
		if len(b) >= 2 {
			c.FontFlags = u16(0)
		}
	case SetMultifragmentUpdate:
		c.MaxRequestSize = u32(0)
	case SetLargePointer:
		c.LargePointer = u16(0)
	case SetSurfaceCommands:
		c.SurfaceCommands = u32(0)
	case SetFrameAcknowledge:
		c.MaxUnacknowledgedFrames = u32(0)
	}
	return nil
}
