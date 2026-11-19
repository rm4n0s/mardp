// Package gfx reads and writes the PDUs of the graphics pipeline
// (MS-RDPEGFX) that the server sends and the client answers, and the RDP 8
// segments that wrap what the server sends, without compressing it.
package gfx

import (
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors the package returns.
const (
	// TagLengthInvalid: a PDU whose pduLength is shorter than its header or
	// longer than the data, or a Reset Graphics that is not 340 bytes.
	TagLengthInvalid = "GfxLengthInvalid"
	// TagCapsInvalid: a capability set longer than the PDU.
	TagCapsInvalid = "GfxCapsInvalid"
	// TagCountInvalid: a count over what the PDU allows (cache entries,
	// monitors).
	TagCountInvalid = "GfxCountInvalid"
	// TagSegmentInvalid: segmented data with an unknown descriptor, cut
	// segments or a size that does not add up.
	TagSegmentInvalid = "GfxSegmentInvalid"
	// TagCompressed: a segment compressed with RDP 8.0 bulk compression,
	// which the package does not decompress.
	TagCompressed = "GfxCompressed"
	// TagSizeInvalid: a surface or output larger than the protocol allows.
	TagSizeInvalid = "GfxSizeInvalid"
)

// HeaderLen is the length of RDPGFX_HEADER.
const HeaderLen = 8

// The command IDs (2.2.1.5).
const (
	CmdWireToSurface1         uint16 = 0x0001
	CmdWireToSurface2         uint16 = 0x0002
	CmdDeleteEncodingContext  uint16 = 0x0003
	CmdSolidFill              uint16 = 0x0004
	CmdSurfaceToSurface       uint16 = 0x0005
	CmdSurfaceToCache         uint16 = 0x0006
	CmdCacheToSurface         uint16 = 0x0007
	CmdEvictCacheEntry        uint16 = 0x0008
	CmdCreateSurface          uint16 = 0x0009
	CmdDeleteSurface          uint16 = 0x000A
	CmdStartFrame             uint16 = 0x000B
	CmdEndFrame               uint16 = 0x000C
	CmdFrameAcknowledge       uint16 = 0x000D
	CmdResetGraphics          uint16 = 0x000E
	CmdMapSurfaceToOutput     uint16 = 0x000F
	CmdCacheImportOffer       uint16 = 0x0010
	CmdCacheImportReply       uint16 = 0x0011
	CmdCapsAdvertise          uint16 = 0x0012
	CmdCapsConfirm            uint16 = 0x0013
	CmdMapSurfaceToWindow     uint16 = 0x0015
	CmdQoEFrameAcknowledge    uint16 = 0x0016
	CmdMapSurfaceToScaledOut  uint16 = 0x0017
	CmdMapSurfaceToScaledWind uint16 = 0x0018
)

// The capability set versions (2.2.1.6).
const (
	Version8   uint32 = 0x00080004
	Version81  uint32 = 0x00080105
	Version10  uint32 = 0x000A0002
	Version101 uint32 = 0x000A0100
	Version102 uint32 = 0x000A0200
	Version103 uint32 = 0x000A0301
	Version104 uint32 = 0x000A0400
	Version105 uint32 = 0x000A0502
	Version106 uint32 = 0x000A0600
	Version107 uint32 = 0x000A0701
)

// The capability flags (2.2.3).
const (
	FlagThinClient       uint32 = 0x00000001
	FlagSmallCache       uint32 = 0x00000002
	FlagAVC420Enabled    uint32 = 0x00000010
	FlagAVCDisabled      uint32 = 0x00000020
	FlagAVCThinClient    uint32 = 0x00000040
	FlagScaledMapDisable uint32 = 0x00000080
)

// The pixel formats of a surface (2.2.1.4).
const (
	PixelFormatXRGB uint8 = 0x20
	PixelFormatARGB uint8 = 0x21
)

// The codecs of a Wire To Surface 1 (2.2.2.1).
const (
	// CodecUncompressed: the pixels left to right, top to bottom, in the
	// surface's format.
	CodecUncompressed uint16 = 0x0000
	// CodecPlanar: MS-RDPEGDI's Planar stream, its scan lines top-down.
	CodecPlanar uint16 = 0x000A
	// CodecAVC420: H.264 in YUV420, with its metablock (2.2.4.4).
	CodecAVC420 uint16 = 0x000B
)

// wireToSurface1Len is the length of a Wire To Surface 1 before its bitmap
// data.
const wireToSurface1Len = HeaderLen + 17

// The queueDepth of a frame acknowledgement that suspends them (2.2.2.13).
const SuspendFrameAcknowledgement uint32 = 0xFFFFFFFF

// The limits of the PDUs.
const (
	// MaxCacheEntries bounds cacheEntriesCount: it "MUST be less than
	// 5462" (2.2.2.16).
	MaxCacheEntries = 5462
	// MaxMonitors bounds monitorCount (2.2.2.14).
	MaxMonitors = 16
	// MaxOutput is the largest width and height of the output (2.2.2.14).
	MaxOutput = 32766
	// resetGraphicsLen is the length every Reset Graphics has.
	resetGraphicsLen = 340
	// monitorLen is the length of TS_MONITOR_DEF.
	monitorLen = 20
)

// CapSet is a capability set: its version and data (2.2.1.6).
type CapSet struct {
	Version uint32
	Data    []byte
}

// Flags returns the set's flags, the first four bytes of its data, or 0
// when it has none.
func (c CapSet) Flags() uint32 {
	if len(c.Data) < 4 {
		return 0
	}
	return uint32(c.Data[0]) | uint32(c.Data[1])<<8 | uint32(c.Data[2])<<16 | uint32(c.Data[3])<<24
}

// Valid says whether the set is one of 2.2.3 with the length its version
// states: 16 bytes for 10.1, 4 for the others.
func (c CapSet) Valid() bool {
	switch c.Version {
	case Version101:
		return len(c.Data) == 16
	case Version8, Version81, Version10, Version102, Version103, Version104, Version105, Version106, Version107:
		return len(c.Data) == 4
	}
	return false
}

// AVC420 says whether a client that sent the set decodes H.264 in YUV420
// mode: the AVC420 flag in 8.1, the absence of "AVC disabled" in 10.x.
func (c CapSet) AVC420() bool {
	switch c.Version {
	case Version81:
		return c.Flags()&FlagAVC420Enabled != 0
	case Version101:
		return true
	case Version10, Version102, Version103, Version104, Version105, Version106, Version107:
		return c.Flags()&FlagAVCDisabled == 0
	}
	return false
}

// NewCapSet returns the capability set version with flags.
func NewCapSet(version, flags uint32) CapSet {
	if version == Version101 {
		return CapSet{Version: version, Data: make([]byte, 16)}
	}
	return CapSet{Version: version, Data: []byte{byte(flags), byte(flags >> 8), byte(flags >> 16), byte(flags >> 24)}}
}

// Monitor is a TS_MONITOR_DEF (MS-RDPBCGR 2.2.1.3.6.1): its edges, right
// and bottom included.
type Monitor struct {
	Left, Top, Right, Bottom int32
	Flags                    uint32
}

// MonitorPrimary marks the primary monitor.
const MonitorPrimary uint32 = 0x00000001

// PDU is a graphics PDU, with the fields of its command.
type PDU struct {
	Cmd uint16
	// CapSets are a Caps Advertise's sets; CapSet is a Caps Confirm's.
	CapSets []CapSet
	CapSet  CapSet
	// QueueDepth, FrameID and TotalFramesDecoded are a Frame
	// Acknowledge's; FrameID is also a QoE Frame Acknowledge's, a Start
	// Frame's and an End Frame's.
	QueueDepth         uint32
	FrameID            uint32
	TotalFramesDecoded uint32
	// Timestamp is a Start Frame's or a QoE Frame Acknowledge's.
	Timestamp uint32
	// CacheEntries is the number of a Cache Import Offer's entries;
	// CacheSlots are a Cache Import Reply's.
	CacheEntries int
	CacheSlots   []uint16
	// Width, Height and Monitors are a Reset Graphics'; Width and Height
	// also a Create Surface's.
	Width, Height uint32
	Monitors      []Monitor
	// SurfaceID and PixelFormat are a Create Surface's; SurfaceID,
	// OriginX and OriginY a Map Surface To Output's.
	SurfaceID        uint16
	PixelFormat      uint8
	OriginX, OriginY uint32
	// CodecID, Left, Top, Right and Bottom are a Wire To Surface 1's, with
	// its SurfaceID and PixelFormat and its bitmap data in Body; Right and
	// Bottom are exclusive.
	CodecID                  uint16
	Left, Top, Right, Bottom uint16
	// Body is what follows the header of a PDU the package does not
	// parse.
	Body []byte
}

// readPDUs calls each with every PDU in b, a whole message, and parse with
// the body of each.
func readPDUs(b []byte, each func(p PDU) error, parse func(p *PDU, r *wire.Reader) error) error {
	r := wire.NewReader(b)
	for r.Remaining() > 0 {
		cmd, err := r.U16LE()
		if err != nil {
			return err
		}
		if _, err := r.U16LE(); err != nil {
			return err
		}
		n, err := r.U32LE()
		if err != nil {
			return err
		}
		if n < HeaderLen || int64(n)-HeaderLen > int64(r.Remaining()) {
			return errors.New(TagLengthInvalid, "a PDU length that does not fit the data",
				"cmd", cmd, "length", n, "rest", r.Remaining())
		}
		body, err := r.Sub(int(n) - HeaderLen)
		if err != nil {
			return err
		}
		p := PDU{Cmd: cmd}
		if err := parse(&p, &body); err != nil {
			return err
		}
		if err := each(p); err != nil {
			return err
		}
	}
	return nil
}

// ReadClient calls each with every PDU in b, a message the client sent:
// Caps Advertise, Frame Acknowledge, QoE Frame Acknowledge and Cache
// Import Offer are parsed, other commands come with their body. Each PDU's
// data is b's.
func ReadClient(b []byte, each func(p PDU) error) error {
	return readPDUs(b, each, parseClient)
}

func parseClient(p *PDU, r *wire.Reader) error {
	var err error
	switch p.Cmd {
	case CmdCapsAdvertise:
		var n uint16
		if n, err = r.U16LE(); err != nil {
			return err
		}
		for range n {
			c, err := readCapSet(r)
			if err != nil {
				return err
			}
			p.CapSets = append(p.CapSets, c)
		}
	case CmdFrameAcknowledge:
		if p.QueueDepth, err = r.U32LE(); err != nil {
			return err
		}
		if p.FrameID, err = r.U32LE(); err != nil {
			return err
		}
		p.TotalFramesDecoded, err = r.U32LE()
	case CmdQoEFrameAcknowledge:
		if p.FrameID, err = r.U32LE(); err != nil {
			return err
		}
		if p.Timestamp, err = r.U32LE(); err != nil {
			return err
		}
		err = r.Skip(4)
	case CmdCacheImportOffer:
		var n uint16
		if n, err = r.U16LE(); err != nil {
			return err
		}
		if n >= MaxCacheEntries {
			return errors.New(TagCountInvalid, "too many cache entries offered", "count", n)
		}
		p.CacheEntries = int(n)
		err = r.Skip(8 * int(n))
	default:
		p.Body = r.Rest()
	}
	return err
}

// readCapSet reads a capability set.
func readCapSet(r *wire.Reader) (CapSet, error) {
	v, err := r.U32LE()
	if err != nil {
		return CapSet{}, err
	}
	n, err := r.U32LE()
	if err != nil {
		return CapSet{}, err
	}
	if int64(n) > int64(r.Remaining()) {
		return CapSet{}, errors.New(TagCapsInvalid, "a capability set longer than the PDU",
			"version", v, "length", n, "rest", r.Remaining())
	}
	data, err := r.Bytes(int(n))
	if err != nil {
		return CapSet{}, err
	}
	return CapSet{Version: v, Data: data}, nil
}

// ReadServer calls each with every PDU in b, the unwrapped data of a
// message the server sent: Caps Confirm, Reset Graphics, Create Surface,
// Map Surface To Output, Start Frame, End Frame and Cache Import Reply are
// parsed, other commands come with their body. Each PDU's data is b's.
func ReadServer(b []byte, each func(p PDU) error) error {
	return readPDUs(b, each, parseServer)
}

func parseServer(p *PDU, r *wire.Reader) error {
	var err error
	switch p.Cmd {
	case CmdCapsConfirm:
		p.CapSet, err = readCapSet(r)
	case CmdResetGraphics:
		return parseResetGraphics(p, r)
	case CmdCreateSurface:
		if p.SurfaceID, err = r.U16LE(); err != nil {
			return err
		}
		var w, h uint16
		if w, err = r.U16LE(); err != nil {
			return err
		}
		if h, err = r.U16LE(); err != nil {
			return err
		}
		p.Width, p.Height = uint32(w), uint32(h)
		p.PixelFormat, err = r.U8()
	case CmdMapSurfaceToOutput:
		if p.SurfaceID, err = r.U16LE(); err != nil {
			return err
		}
		if err = r.Skip(2); err != nil {
			return err
		}
		if p.OriginX, err = r.U32LE(); err != nil {
			return err
		}
		p.OriginY, err = r.U32LE()
	case CmdWireToSurface1:
		return parseWireToSurface1(p, r)
	case CmdStartFrame:
		if p.Timestamp, err = r.U32LE(); err != nil {
			return err
		}
		p.FrameID, err = r.U32LE()
	case CmdEndFrame:
		p.FrameID, err = r.U32LE()
	case CmdCacheImportReply:
		var n uint16
		if n, err = r.U16LE(); err != nil {
			return err
		}
		if n >= MaxCacheEntries {
			return errors.New(TagCountInvalid, "too many cache slots imported", "count", n)
		}
		p.CacheSlots = make([]uint16, n)
		for i := range p.CacheSlots {
			if p.CacheSlots[i], err = r.U16LE(); err != nil {
				return err
			}
		}
	default:
		p.Body = r.Rest()
	}
	return err
}

func parseWireToSurface1(p *PDU, r *wire.Reader) error {
	var err error
	if p.SurfaceID, err = r.U16LE(); err != nil {
		return err
	}
	if p.CodecID, err = r.U16LE(); err != nil {
		return err
	}
	if p.PixelFormat, err = r.U8(); err != nil {
		return err
	}
	for _, v := range []*uint16{&p.Left, &p.Top, &p.Right, &p.Bottom} {
		if *v, err = r.U16LE(); err != nil {
			return err
		}
	}
	n, err := r.U32LE()
	if err != nil {
		return err
	}
	if int64(n) != int64(r.Remaining()) {
		return errors.New(TagLengthInvalid, "a bitmap data length that is not the rest of the PDU",
			"length", n, "rest", r.Remaining())
	}
	p.Body = r.Rest()
	return nil
}

func parseResetGraphics(p *PDU, r *wire.Reader) error {
	if r.Remaining() != resetGraphicsLen-HeaderLen {
		return errors.New(TagLengthInvalid, "a Reset Graphics that is not 340 bytes", "length", r.Remaining()+HeaderLen)
	}
	var err error
	if p.Width, err = r.U32LE(); err != nil {
		return err
	}
	if p.Height, err = r.U32LE(); err != nil {
		return err
	}
	n, err := r.U32LE()
	if err != nil {
		return err
	}
	if n > MaxMonitors {
		return errors.New(TagCountInvalid, "too many monitors", "count", n)
	}
	p.Monitors = make([]Monitor, n)
	for i := range p.Monitors {
		m := &p.Monitors[i]
		for _, v := range []*int32{&m.Left, &m.Top, &m.Right, &m.Bottom} {
			u, err := r.U32LE()
			if err != nil {
				return err
			}
			*v = int32(u)
		}
		if m.Flags, err = r.U32LE(); err != nil {
			return err
		}
	}
	return nil
}

// header appends a PDU's header with the length n, header included.
func header(w *wire.Writer, cmd uint16, n int) {
	w.U16LE(cmd)
	w.U16LE(0)
	w.U32LE(uint32(n))
}

// Begin appends a header for cmd whose length is patched by End; it
// returns where the PDU starts.
func Begin(w *wire.Writer, cmd uint16) int {
	off := w.Len()
	header(w, cmd, 0)
	return off
}

// End patches the length of the PDU that Begin started at off.
func End(w *wire.Writer, off int) error {
	return w.PatchU32LE(off+4, uint32(w.Len()-off))
}

func appendCapSet(w *wire.Writer, c CapSet) {
	w.U32LE(c.Version)
	w.U32LE(uint32(len(c.Data)))
	w.Write(c.Data)
}

// AppendCapsConfirm appends a Caps Confirm with the set c.
func AppendCapsConfirm(w *wire.Writer, c CapSet) {
	header(w, CmdCapsConfirm, HeaderLen+8+len(c.Data))
	appendCapSet(w, c)
}

// AppendCapsAdvertise appends a Caps Advertise with sets, as a client
// sends it.
func AppendCapsAdvertise(w *wire.Writer, sets []CapSet) {
	n := HeaderLen + 2
	for _, c := range sets {
		n += 8 + len(c.Data)
	}
	header(w, CmdCapsAdvertise, n)
	w.U16LE(uint16(len(sets)))
	for _, c := range sets {
		appendCapSet(w, c)
	}
}

// AppendResetGraphics appends a Reset Graphics for an output of width ×
// height with monitors, padded to 340 bytes.
func AppendResetGraphics(w *wire.Writer, width, height uint32, monitors []Monitor) error {
	if width == 0 || height == 0 || width > MaxOutput || height > MaxOutput {
		return errors.New(TagSizeInvalid, "an output size the protocol does not allow", "width", width, "height", height)
	}
	if len(monitors) > MaxMonitors {
		return errors.New(TagCountInvalid, "too many monitors", "count", len(monitors))
	}
	header(w, CmdResetGraphics, resetGraphicsLen)
	w.U32LE(width)
	w.U32LE(height)
	w.U32LE(uint32(len(monitors)))
	for _, m := range monitors {
		w.U32LE(uint32(m.Left))
		w.U32LE(uint32(m.Top))
		w.U32LE(uint32(m.Right))
		w.U32LE(uint32(m.Bottom))
		w.U32LE(m.Flags)
	}
	w.Zeros(resetGraphicsLen - HeaderLen - 12 - monitorLen*len(monitors))
	return nil
}

// AppendCreateSurface appends a Create Surface of width × height.
func AppendCreateSurface(w *wire.Writer, id uint16, width, height int, format uint8) error {
	if width <= 0 || height <= 0 || width > MaxOutput || height > MaxOutput {
		return errors.New(TagSizeInvalid, "a surface size the protocol does not allow", "width", width, "height", height)
	}
	header(w, CmdCreateSurface, HeaderLen+7)
	w.U16LE(id)
	w.U16LE(uint16(width))
	w.U16LE(uint16(height))
	w.U8(format)
	return nil
}

// AppendMapSurfaceToOutput appends a Map Surface To Output of surface id
// at x, y.
func AppendMapSurfaceToOutput(w *wire.Writer, id uint16, x, y uint32) {
	header(w, CmdMapSurfaceToOutput, HeaderLen+12)
	w.U16LE(id)
	w.U16LE(0)
	w.U32LE(x)
	w.U32LE(y)
}

// BeginWireToSurface1 starts a Wire To Surface 1 of the area left, top to
// right, bottom (exclusive) of surface, and returns its offset; the bitmap
// data is appended next, and EndWireToSurface1 finishes the PDU.
func BeginWireToSurface1(w *wire.Writer, surface uint16, format uint8, left, top, right, bottom uint16) int {
	off := w.Len()
	header(w, CmdWireToSurface1, 0)
	w.U16LE(surface)
	w.U16LE(0)
	w.U8(format)
	w.U16LE(left)
	w.U16LE(top)
	w.U16LE(right)
	w.U16LE(bottom)
	w.U32LE(0)
	return off
}

// EndWireToSurface1 finishes the Wire To Surface 1 that
// BeginWireToSurface1 started at off: its codec, the length of its bitmap
// data and its own.
func EndWireToSurface1(w *wire.Writer, off int, codec uint16) error {
	if err := w.PatchU16LE(off+HeaderLen+2, codec); err != nil {
		return err
	}
	if err := w.PatchU32LE(off+wireToSurface1Len-4, uint32(w.Len()-off-wireToSurface1Len)); err != nil {
		return err
	}
	return End(w, off)
}

// AppendStartFrame appends a Start Frame.
func AppendStartFrame(w *wire.Writer, timestamp, frameID uint32) {
	header(w, CmdStartFrame, HeaderLen+8)
	w.U32LE(timestamp)
	w.U32LE(frameID)
}

// AppendEndFrame appends an End Frame.
func AppendEndFrame(w *wire.Writer, frameID uint32) {
	header(w, CmdEndFrame, HeaderLen+4)
	w.U32LE(frameID)
}

// AppendFrameAcknowledge appends a Frame Acknowledge, as a client sends
// it.
func AppendFrameAcknowledge(w *wire.Writer, queueDepth, frameID, total uint32) {
	header(w, CmdFrameAcknowledge, HeaderLen+12)
	w.U32LE(queueDepth)
	w.U32LE(frameID)
	w.U32LE(total)
}

// AppendQoEFrameAcknowledge appends a QoE Frame Acknowledge, as a client
// sends it.
func AppendQoEFrameAcknowledge(w *wire.Writer, frameID, timestamp uint32, se, edr uint16) {
	header(w, CmdQoEFrameAcknowledge, HeaderLen+12)
	w.U32LE(frameID)
	w.U32LE(timestamp)
	w.U16LE(se)
	w.U16LE(edr)
}

// AppendCacheImportOffer appends a Cache Import Offer of keys, as a
// client sends it.
func AppendCacheImportOffer(w *wire.Writer, keys []uint64) {
	header(w, CmdCacheImportOffer, HeaderLen+2+8*len(keys))
	w.U16LE(uint16(len(keys)))
	for _, k := range keys {
		w.U64LE(k)
	}
}

// AppendCacheImportReply appends a Cache Import Reply with slots.
func AppendCacheImportReply(w *wire.Writer, slots []uint16) {
	header(w, CmdCacheImportReply, HeaderLen+2+2*len(slots))
	w.U16LE(uint16(len(slots)))
	for _, s := range slots {
		w.U16LE(s)
	}
}

// Timestamp returns t as a Start Frame's timestamp (2.2.2.11): its UTC
// time of day in hours, minutes, seconds and milliseconds.
func Timestamp(t time.Time) uint32 {
	t = t.UTC()
	return uint32(t.Hour())<<22 | uint32(t.Minute())<<16 | uint32(t.Second())<<10 | uint32(t.Nanosecond()/int(time.Millisecond))
}

// The RDP 8 segments (2.2.5).
const (
	DescriptorSingle    uint8 = 0xE0
	DescriptorMultipart uint8 = 0xE1
	// bulkRDP8 is the header of an uncompressed segment: the RDP 8.0
	// compression type without PACKET_COMPRESSED.
	bulkRDP8           uint8 = 0x04
	bulkCompressed     uint8 = 0x20
	bulkTypeMask       uint8 = 0x0F
	compressionTypeRDP uint8 = 0x04
	// MaxSegment is the most data an uncompressed segment carries
	// (3.1.9.1.2.1).
	MaxSegment = 65535
)

// Wrap appends data to w as RDP_SEGMENTED_DATA, uncompressed: one segment
// when it fits in one, otherwise segments of MaxSegment bytes.
func Wrap(w *wire.Writer, data []byte) {
	if len(data) <= MaxSegment {
		w.U8(DescriptorSingle)
		w.U8(bulkRDP8)
		w.Write(data)
		return
	}
	n := (len(data) + MaxSegment - 1) / MaxSegment
	w.U8(DescriptorMultipart)
	w.U16LE(uint16(n))
	w.U32LE(uint32(len(data)))
	for rest := data; len(rest) > 0; {
		k := min(len(rest), MaxSegment)
		w.U32LE(uint32(k + 1))
		w.U8(bulkRDP8)
		w.Write(rest[:k])
		rest = rest[k:]
	}
}

// WrappedLen is how long Wrap makes n bytes of data.
func WrappedLen(n int) int {
	if n <= MaxSegment {
		return 2 + n
	}
	segs := (n + MaxSegment - 1) / MaxSegment
	return 7 + 5*segs + n
}

// Unwrap returns the data of b, RDP_SEGMENTED_DATA whose segments are not
// compressed: b's own bytes for one segment, joined into dst for several.
func Unwrap(b, dst []byte) ([]byte, error) {
	r := wire.NewReader(b)
	d, err := r.U8()
	if err != nil {
		return nil, err
	}
	switch d {
	case DescriptorSingle:
		return segment(r.Rest())
	case DescriptorMultipart:
	default:
		return nil, errors.New(TagSegmentInvalid, "an unknown segment descriptor", "descriptor", d)
	}
	n, err := r.U16LE()
	if err != nil {
		return nil, err
	}
	size, err := r.U32LE()
	if err != nil {
		return nil, err
	}
	if int64(size) > int64(r.Remaining()) {
		return nil, errors.New(TagSegmentInvalid, "an uncompressed size larger than the data",
			"size", size, "rest", r.Remaining())
	}
	out := dst[:0]
	for range n {
		k, err := r.U32LE()
		if err != nil {
			return nil, err
		}
		if int64(k) > int64(r.Remaining()) {
			return nil, errors.New(TagSegmentInvalid, "a segment longer than the data", "size", k, "rest", r.Remaining())
		}
		raw, err := r.Bytes(int(k))
		if err != nil {
			return nil, err
		}
		data, err := segment(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, data...)
	}
	if r.Remaining() != 0 || len(out) != int(size) {
		return nil, errors.New(TagSegmentInvalid, "segments that do not add up to the uncompressed size",
			"size", size, "joined", len(out), "rest", r.Remaining())
	}
	return out, nil
}

// segment returns the data of one RDP8_BULK_ENCODED_DATA.
func segment(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New(TagSegmentInvalid, "a segment without its header")
	}
	h := b[0]
	if h&bulkCompressed != 0 {
		return nil, errors.New(TagCompressed, "a compressed segment", "header", h)
	}
	if h&bulkTypeMask != compressionTypeRDP {
		return nil, errors.New(TagSegmentInvalid, "a segment of another compression type", "header", h)
	}
	return b[1:], nil
}
