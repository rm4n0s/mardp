// Package input reads what a client sends during a session (MS-RDPBCGR
// 2.2.8.1 and 2.2.11.2 to 2.2.11.3): input events in their fast-path form
// (2.2.8.1.2) and their slow-path form (2.2.8.1.1.3), Refresh Rect and
// Suppress Output. It writes them too, for rdptest.
//
// Events of both forms come out as the same Event, read one at a time so
// that reading allocates nothing.
package input

import (
	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors this package returns.
const (
	// TagHeaderInvalid: a fast-path input PDU whose action is not
	// fast-path, or which is encrypted (it never is under TLS: "the
	// FASTPATH_INPUT_ENCRYPTED flag ... SHOULD be dropped").
	TagHeaderInvalid = "InputHeaderInvalid"
	// TagLengthInvalid: a fast-path input PDU whose length is not that of
	// its frame.
	TagLengthInvalid = "InputLengthInvalid"
	// TagEventUnknown: an event code or message type the documents do not
	// define; the server SHOULD drop the connection (3.3.5.8.1.1).
	TagEventUnknown = "InputEventUnknown"
	// TagTrailingData: bytes after the last event the PDU counts.
	TagTrailingData = "InputTrailingData"
	// TagKindInvalid: an event to write in a form that has no such event.
	TagKindInvalid = "InputKindInvalid"
	// TagTooManyEvents: more events to write than the form counts.
	TagTooManyEvents = "InputTooManyEvents"
	// TagValueInvalid: a Suppress Output whose allowDisplayUpdates is
	// neither 0 nor 1.
	TagValueInvalid = "InputValueInvalid"
)

// Kind is what an event is.
type Kind uint8

// The kinds of event.
const (
	KindScancode Kind = iota + 1
	KindUnicode
	KindMouse
	// KindMouseX is an extended mouse event: the fourth and fifth buttons.
	KindMouseX
	KindSync
	// KindMouseRel is a relative mouse event, which the server does not
	// ask for.
	KindMouseRel
	// KindQoE is a quality-of-experience timestamp (fast-path only).
	KindQoE
	// KindUnused is the slow-path TS_UNUSED_EVENT, to be ignored.
	KindUnused
)

// String names k for the log.
func (k Kind) String() string {
	switch k {
	case KindScancode:
		return "scancode"
	case KindUnicode:
		return "unicode"
	case KindMouse:
		return "mouse"
	case KindMouseX:
		return "extended mouse"
	case KindSync:
		return "synchronize"
	case KindMouseRel:
		return "relative mouse"
	case KindQoE:
		return "QoE timestamp"
	case KindUnused:
		return "unused"
	}
	return "unknown"
}

// The Flags of a KindScancode or KindUnicode event, the same in both forms
// (they are the fast-path values; the slow-path ones are converted). A
// Unicode event has only KeyRelease.
const (
	KeyRelease   uint16 = 0x1
	KeyExtended  uint16 = 0x2
	KeyExtended1 uint16 = 0x4
)

// The Flags of a KindSync event, the toggle states (the same in both forms).
const (
	SyncScrollLock uint16 = 0x1
	SyncNumLock    uint16 = 0x2
	SyncCapsLock   uint16 = 0x4
	SyncKanaLock   uint16 = 0x8
)

// The Flags of a KindMouse event, its pointerFlags (2.2.8.1.1.3.1.1.3).
const (
	PtrHWheel        uint16 = 0x0400
	PtrWheel         uint16 = 0x0200
	PtrWheelNegative uint16 = 0x0100
	// PtrWheelRotation masks the wheel's rotation, a 9-bit two's
	// complement value whose sign bit is PtrWheelNegative.
	PtrWheelRotation uint16 = 0x01ff
	PtrMove          uint16 = 0x0800
	PtrDown          uint16 = 0x8000
	PtrButton1       uint16 = 0x1000
	PtrButton2       uint16 = 0x2000
	PtrButton3       uint16 = 0x4000
)

// The Flags of a KindMouseX event (2.2.8.1.1.3.1.1.4).
const (
	PtrXDown    uint16 = 0x8000
	PtrXButton1 uint16 = 0x0001
	PtrXButton2 uint16 = 0x0002
)

// Event is one input event.
type Event struct {
	Kind Kind
	// Flags are the event's flags, as the constants above describe for
	// each kind.
	Flags uint16
	// Code is the scancode (KindScancode) or the UTF-16 code unit
	// (KindUnicode).
	Code uint16
	// X and Y are the position (KindMouse, KindMouseX), or the movement as
	// two's complement (KindMouseRel).
	X, Y uint16
	// Time is the slow-path eventTime, or the timestamp of KindQoE.
	Time uint32
}

// The fast-path event codes (2.2.8.1.2.2).
const (
	fpScancode = 0
	fpMouse    = 1
	fpMouseX   = 2
	fpSync     = 3
	fpUnicode  = 4
	fpMouseRel = 5
	fpQoE      = 6
)

// Bits of the fast-path headers.
const (
	// fpEncrypted is FASTPATH_INPUT_ENCRYPTED in the flags of
	// fpInputHeader, its two high bits.
	fpEncrypted = 0x2
	// fpHeaderEvents is the most events fpInputHeader counts; more are
	// counted by the numEvents byte.
	fpHeaderEvents = 0xf
	// fpMaxEvents is the most events a fast-path input PDU carries.
	fpMaxEvents = 0xff
	// shortLengthMax is the longest PDU whose length fits in one byte.
	shortLengthMax = 0x7f
)

// FastPath reads the events of one fast-path input PDU (2.2.8.1.2).
type FastPath struct {
	r    wire.Reader
	left int
}

// ParseFastPath checks the header of one fast-path input PDU, a whole frame
// as tpkt.ReadFrame returns it. Next reads its events.
func ParseFastPath(frame []byte) (FastPath, error) {
	f := FastPath{r: wire.NewReader(frame)}
	h, err := f.r.U8()
	if err != nil {
		return f, err
	}
	if h&0x03 != 0 || h>>6&fpEncrypted != 0 {
		return f, errors.New(TagHeaderInvalid, "not a plain fast-path input header", "header", h)
	}
	l1, err := f.r.U8()
	if err != nil {
		return f, err
	}
	n := int(l1)
	if l1&0x80 != 0 {
		l2, err := f.r.U8()
		if err != nil {
			return f, err
		}
		n = int(l1&0x7f)<<8 | int(l2)
	}
	if n != len(frame) {
		return f, errors.New(TagLengthInvalid, "PDU length is not the frame's", "length", n, "frame", len(frame))
	}
	f.left = int(h >> 2 & fpHeaderEvents)
	if f.left == 0 {
		// "If the number of events is greater than 15, the numEvents field
		// in the fpInputHeader MUST be set to zero" and the optional
		// numEvents byte counts them.
		c, err := f.r.U8()
		if err != nil {
			return f, err
		}
		f.left = int(c)
	}
	return f, nil
}

// Next reads the next event; it returns false after the last one.
func (f *FastPath) Next() (Event, bool, error) {
	if f.left == 0 {
		if n := f.r.Remaining(); n > 0 {
			return Event{}, false, errors.New(TagTrailingData, "bytes after the last fast-path event", "length", n)
		}
		return Event{}, false, nil
	}
	f.left--
	h, err := f.r.U8()
	if err != nil {
		return Event{}, false, err
	}
	flags, code := uint16(h&0x1f), h>>5
	var e Event
	switch code {
	case fpScancode:
		k, err := f.r.U8()
		if err != nil {
			return Event{}, false, err
		}
		e = Event{Kind: KindScancode, Flags: flags & (KeyRelease | KeyExtended | KeyExtended1), Code: uint16(k)}
	case fpMouse, fpMouseX, fpMouseRel:
		e.Kind = pointerKind(code)
		if e.Flags, err = f.r.U16LE(); err != nil {
			return Event{}, false, err
		}
		if e.X, err = f.r.U16LE(); err != nil {
			return Event{}, false, err
		}
		if e.Y, err = f.r.U16LE(); err != nil {
			return Event{}, false, err
		}
	case fpSync:
		e = Event{Kind: KindSync, Flags: flags & (SyncScrollLock | SyncNumLock | SyncCapsLock | SyncKanaLock)}
	case fpUnicode:
		u, err := f.r.U16LE()
		if err != nil {
			return Event{}, false, err
		}
		e = Event{Kind: KindUnicode, Flags: flags & KeyRelease, Code: u}
	case fpQoE:
		t, err := f.r.U32LE()
		if err != nil {
			return Event{}, false, err
		}
		e = Event{Kind: KindQoE, Time: t}
	default:
		return Event{}, false, errors.New(TagEventUnknown, "an unknown fast-path event code", "code", code, "header", h)
	}
	return e, true, nil
}

// pointerKind is the kind of a fast-path pointer event code.
func pointerKind(code uint8) Kind {
	switch code {
	case fpMouse:
		return KindMouse
	case fpMouseX:
		return KindMouseX
	}
	return KindMouseRel
}

// AppendFastPath appends a fast-path input PDU carrying events, at most 255
// of them and none of KindUnused.
func AppendFastPath(w *wire.Writer, events []Event) error {
	if len(events) > fpMaxEvents {
		return errors.New(TagTooManyEvents, "more events than a fast-path PDU counts", "events", len(events), "max", fpMaxEvents)
	}
	size := 0
	for _, e := range events {
		n, err := fastPathSize(e.Kind)
		if err != nil {
			return err
		}
		size += n
	}
	h := uint8(0)
	if len(events) <= fpHeaderEvents && len(events) > 0 {
		h = uint8(len(events)) << 2
	} else {
		size++ // the numEvents byte
	}
	if short := 2 + size; short <= shortLengthMax {
		w.U8(h)
		w.U8(uint8(short))
	} else {
		w.U8(h)
		w.U16BE(uint16(3+size) | 0x8000)
	}
	if h == 0 {
		w.U8(uint8(len(events)))
	}
	for _, e := range events {
		switch e.Kind {
		case KindScancode:
			w.U8(fpScancode<<5 | uint8(e.Flags&0x1f))
			w.U8(uint8(e.Code))
		case KindMouse, KindMouseX, KindMouseRel:
			code := uint8(fpMouse)
			switch e.Kind {
			case KindMouseX:
				code = fpMouseX
			case KindMouseRel:
				code = fpMouseRel
			}
			w.U8(code << 5)
			w.U16LE(e.Flags)
			w.U16LE(e.X)
			w.U16LE(e.Y)
		case KindSync:
			w.U8(fpSync<<5 | uint8(e.Flags&0x1f))
		case KindUnicode:
			w.U8(fpUnicode<<5 | uint8(e.Flags&0x1f))
			w.U16LE(e.Code)
		case KindQoE:
			w.U8(fpQoE << 5)
			w.U32LE(e.Time)
		}
	}
	return nil
}

// fastPathSize is how many bytes a fast-path event of kind k takes.
func fastPathSize(k Kind) (int, error) {
	switch k {
	case KindScancode:
		return 2, nil
	case KindMouse, KindMouseX, KindMouseRel:
		return 7, nil
	case KindSync:
		return 1, nil
	case KindUnicode:
		return 3, nil
	case KindQoE:
		return 5, nil
	}
	return 0, errors.New(TagKindInvalid, "no such fast-path event", "kind", k.String())
}

// The slow-path message types (2.2.8.1.1.3.1.1).
const (
	slowSync     uint16 = 0x0000
	slowUnused   uint16 = 0x0002
	slowScancode uint16 = 0x0004
	slowUnicode  uint16 = 0x0005
	slowMouse    uint16 = 0x8001
	slowMouseX   uint16 = 0x8002
	slowMouseRel uint16 = 0x8004
)

// The slow-path keyboard flags (2.2.8.1.1.3.1.1.1).
const (
	kbdExtended  uint16 = 0x0100
	kbdExtended1 uint16 = 0x0200
	kbdRelease   uint16 = 0x8000
)

// slowEventLen is the size of a slow-path event: eventTime, messageType and
// 6 bytes of data, whatever the type.
const slowEventLen = 12

// SlowPath reads the events of one Input Event PDU (2.2.8.1.1.3), the body
// of its Data PDU.
type SlowPath struct {
	r    wire.Reader
	left int
}

// ParseSlowPath reads the header of an Input Event PDU's data. Next reads
// its events.
func ParseSlowPath(body []byte) (SlowPath, error) {
	s := SlowPath{r: wire.NewReader(body)}
	n, err := s.r.U16LE()
	if err != nil {
		return s, err
	}
	if err := s.r.Skip(2); err != nil { // pad2Octets
		return s, err
	}
	s.left = int(n)
	return s, nil
}

// Next reads the next event; it returns false after the last one.
func (s *SlowPath) Next() (Event, bool, error) {
	if s.left == 0 {
		if n := s.r.Remaining(); n > 0 {
			return Event{}, false, errors.New(TagTrailingData, "bytes after the last slow-path event", "length", n)
		}
		return Event{}, false, nil
	}
	s.left--
	r, err := s.r.Sub(slowEventLen)
	if err != nil {
		return Event{}, false, err
	}
	// Sub checked the length, so these reads cannot fail.
	t, _ := r.U32LE()
	typ, _ := r.U16LE()
	a, _ := r.U16LE()
	b, _ := r.U16LE()
	c, _ := r.U16LE()
	e := Event{Time: t}
	switch typ {
	case slowSync:
		// pad2Octets, then toggleFlags (32 bits) as b and c.
		e.Kind, e.Flags = KindSync, b&(SyncScrollLock|SyncNumLock|SyncCapsLock|SyncKanaLock)
	case slowUnused:
		e.Kind = KindUnused
	case slowScancode:
		e.Kind, e.Flags, e.Code = KindScancode, keyFlags(a), b
	case slowUnicode:
		e.Kind, e.Flags, e.Code = KindUnicode, keyFlags(a)&KeyRelease, b
	case slowMouse:
		e.Kind, e.Flags, e.X, e.Y = KindMouse, a, b, c
	case slowMouseX:
		e.Kind, e.Flags, e.X, e.Y = KindMouseX, a, b, c
	case slowMouseRel:
		e.Kind, e.Flags, e.X, e.Y = KindMouseRel, a, b, c
	default:
		return Event{}, false, errors.New(TagEventUnknown, "an unknown slow-path message type", "messageType", typ)
	}
	return e, true, nil
}

// keyFlags converts slow-path keyboard flags to the Key flags.
func keyFlags(f uint16) uint16 {
	var k uint16
	if f&kbdRelease != 0 {
		k |= KeyRelease
	}
	if f&kbdExtended != 0 {
		k |= KeyExtended
	}
	if f&kbdExtended1 != 0 {
		k |= KeyExtended1
	}
	return k
}

// AppendSlowPath appends the data of an Input Event PDU carrying events,
// none of KindQoE, for the body of its Data PDU.
func AppendSlowPath(w *wire.Writer, events []Event) error {
	if len(events) > 0xffff {
		return errors.New(TagTooManyEvents, "more events than an Input Event PDU counts", "events", len(events), "max", 0xffff)
	}
	for _, e := range events {
		if e.Kind == KindQoE || e.Kind < KindScancode || e.Kind > KindUnused {
			return errors.New(TagKindInvalid, "no such slow-path event", "kind", e.Kind.String())
		}
	}
	w.U16LE(uint16(len(events)))
	w.U16LE(0)
	for _, e := range events {
		w.U32LE(e.Time)
		switch e.Kind {
		case KindSync:
			w.U16LE(slowSync)
			w.U16LE(0)
			w.U32LE(uint32(e.Flags))
		case KindUnused:
			var pad [6]byte
			w.U16LE(slowUnused)
			w.Write(pad[:])
		case KindScancode, KindUnicode:
			typ := slowScancode
			if e.Kind == KindUnicode {
				typ = slowUnicode
			}
			var f uint16
			if e.Flags&KeyRelease != 0 {
				f |= kbdRelease
			}
			if e.Flags&KeyExtended != 0 {
				f |= kbdExtended
			}
			if e.Flags&KeyExtended1 != 0 {
				f |= kbdExtended1
			}
			w.U16LE(typ)
			w.U16LE(f)
			w.U16LE(e.Code)
			w.U16LE(0)
		default:
			typ := slowMouse
			switch e.Kind {
			case KindMouseX:
				typ = slowMouseX
			case KindMouseRel:
				typ = slowMouseRel
			}
			w.U16LE(typ)
			w.U16LE(e.Flags)
			w.U16LE(e.X)
			w.U16LE(e.Y)
		}
	}
	return nil
}

// Rect is a TS_RECTANGLE16 (2.2.11.1): its right and bottom are inside it.
type Rect struct {
	Left, Top, Right, Bottom uint16
}

func readRect(r *wire.Reader) (Rect, error) {
	sub, err := r.Sub(8)
	if err != nil {
		return Rect{}, err
	}
	// Sub checked the length, so these reads cannot fail.
	var rc Rect
	rc.Left, _ = sub.U16LE()
	rc.Top, _ = sub.U16LE()
	rc.Right, _ = sub.U16LE()
	rc.Bottom, _ = sub.U16LE()
	return rc, nil
}

func appendRect(w *wire.Writer, rc Rect) {
	w.U16LE(rc.Left)
	w.U16LE(rc.Top)
	w.U16LE(rc.Right)
	w.U16LE(rc.Bottom)
}

// ParseRefreshRect reads a Refresh Rect PDU (2.2.11.2.1) from the body of
// its Data PDU and appends its areas to dst. Bytes after them are ignored.
func ParseRefreshRect(body []byte, dst []Rect) ([]Rect, error) {
	r := wire.NewReader(body)
	n, err := r.U8()
	if err != nil {
		return dst, err
	}
	if err := r.Skip(3); err != nil { // pad3Octets
		return dst, err
	}
	for range n {
		rc, err := readRect(&r)
		if err != nil {
			return dst, err
		}
		dst = append(dst, rc)
	}
	return dst, nil
}

// AppendRefreshRect appends the data of a Refresh Rect PDU, at most 255
// areas.
func AppendRefreshRect(w *wire.Writer, areas []Rect) error {
	if len(areas) > 0xff {
		return errors.New(TagTooManyEvents, "more areas than a Refresh Rect counts", "areas", len(areas), "max", 0xff)
	}
	w.U8(uint8(len(areas)))
	w.Write([]byte{0, 0, 0})
	for _, rc := range areas {
		appendRect(w, rc)
	}
	return nil
}

// SuppressOutput is a Suppress Output PDU (2.2.11.3.1).
type SuppressOutput struct {
	// Allow says the client wants display updates again; when false it
	// wants none.
	Allow bool
	// Area is the desktop area the client shows, present when Allow is
	// set.
	Area Rect
}

// ParseSuppressOutput reads a Suppress Output PDU from the body of its Data
// PDU.
func ParseSuppressOutput(body []byte) (SuppressOutput, error) {
	r := wire.NewReader(body)
	a, err := r.U8()
	if err != nil {
		return SuppressOutput{}, err
	}
	if a > 1 {
		return SuppressOutput{}, errors.New(TagValueInvalid, "allowDisplayUpdates is neither 0 nor 1", "allowDisplayUpdates", a)
	}
	if err := r.Skip(3); err != nil { // pad3Octets
		return SuppressOutput{}, err
	}
	s := SuppressOutput{Allow: a == 1}
	if s.Allow {
		// "This field MUST be present if the allowDisplayUpdates field is
		// set to ALLOW_DISPLAY_UPDATES".
		if s.Area, err = readRect(&r); err != nil {
			return SuppressOutput{}, err
		}
	}
	return s, nil
}

// AppendSuppressOutput appends the data of a Suppress Output PDU.
func AppendSuppressOutput(w *wire.Writer, s SuppressOutput) {
	if !s.Allow {
		w.U8(0)
		w.Write([]byte{0, 0, 0})
		return
	}
	w.U8(1)
	w.Write([]byte{0, 0, 0})
	appendRect(w, s.Area)
}
