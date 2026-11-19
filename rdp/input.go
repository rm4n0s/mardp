package rdp

import (
	"unicode/utf16"
	"unicode/utf8"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/caps"
	"github.com/rm4n0s/mardp/rdp/internal/input"
	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/tpkt"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/internal/x224"
)

// Extended is the prefix of an extended scancode.
type Extended uint8

// The prefixes a key's scancode may have.
const (
	// NotExtended: a plain scancode.
	NotExtended Extended = iota
	// ExtendedE0: the 0xE0 prefix (KBDFLAGS_EXTENDED): the right Ctrl and
	// Alt, the arrows and the keys above them, the keypad's Enter and /,
	// the Windows keys.
	ExtendedE0
	// ExtendedE1: the 0xE1 prefix (KBDFLAGS_EXTENDED1), which only Pause
	// sends: 0x1D with the prefix, then 0x45 without it.
	ExtendedE1
)

// MouseButton is a mouse button.
type MouseButton uint8

// The mouse buttons a client reports (SPEC §10.5).
const (
	ButtonLeft MouseButton = iota + 1
	ButtonRight
	ButtonMiddle
	// ButtonBack and ButtonForward are the fourth and fifth buttons
	// (PTRXFLAGS_BUTTON1 and PTRXFLAGS_BUTTON2).
	ButtonBack
	ButtonForward
)

// String names b for the log.
func (b MouseButton) String() string {
	switch b {
	case ButtonLeft:
		return "left"
	case ButtonRight:
		return "right"
	case ButtonMiddle:
		return "middle"
	case ButtonBack:
		return "back"
	case ButtonForward:
		return "forward"
	}
	return "unknown"
}

// InputSink gets the client's input (SPEC §6). The calls come in the order
// the client sent the events, one at a time, on the goroutine that reads the
// session's connection: a call that takes long holds up the reading.
type InputSink interface {
	// Key reports a key going down or up by its PC scancode (set 1) and
	// prefix. A key held down repeats as more calls with down set.
	Key(scancode uint8, extended Extended, down bool)
	// Unicode reports a character the client typed through an input
	// method, going down or up. A character outside the Basic
	// Multilingual Plane, which the client sends as two UTF-16 surrogates,
	// is reported once; a surrogate without its pair is reported as
	// utf8.RuneError.
	Unicode(r rune, down bool)
	// MouseMove reports the pointer's position on the session's desktop.
	MouseMove(x, y int)
	// MouseButton reports a button going down or up at a position.
	MouseButton(button MouseButton, down bool, x, y int)
	// Wheel reports a turn of the wheel, in 120ths of a notch: dy positive
	// away from the user (scrolling up), dx positive to the right.
	Wheel(dx, dy int)
	// SyncLocks reports the state of the lock keys, which the client sends
	// when its window gets the focus.
	SyncLocks(caps, num, scroll bool)
}

// FlowSink gets what the client asks of the output (SPEC §5.3, §6). The
// calls come on the goroutine that reads the session's connection, like
// InputSink's.
type FlowSink interface {
	// KeyFrameWanted reports that the client wants the whole desktop again
	// (Refresh Rect), or that it shows it again after OutputSuppressed.
	KeyFrameWanted()
	// OutputSuppressed reports that the client stopped showing the desktop
	// (its window is minimized: capture can pause), or, with false, that it
	// shows it again, followed by KeyFrameWanted.
	OutputSuppressed(suppressed bool)
}

// SetInputSink gives the session's input to sink; nil drops it. Input that
// comes before a sink is set is dropped, except the lock state of the last
// synchronize event, which the new sink gets before the next input.
func (s *Session) SetInputSink(sink InputSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.input = sink
	s.inputGen++
}

// SetFlowSink gives the client's requests about the output to sink; nil
// drops them. When the output is suppressed as it is set, its first call is
// OutputSuppressed(true), before the next PDU the client sends is handled.
func (s *Session) SetFlowSink(sink FlowSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flow = sink
	s.flowGen++
}

// OutputSuppressed says the client asked for no output (Suppress Output)
// and has not asked for it again.
func (s *Session) OutputSuppressed() bool { return s.suppressed.Load() }

// sessionReader is what the goroutine that reads a session keeps between
// PDUs: the sinks it took last, and the state of the input it delivers.
type sessionReader struct {
	c *conn
	s *Session

	in      InputSink
	inGen   uint64
	flow    FlowSink
	flowGen uint64

	// locks is the toggle state of the last synchronize event, when
	// haveLocks is set.
	locks     uint16
	haveLocks bool
	// high is a high surrogate waiting for its low one, when haveHigh is
	// set; highDown is whether it went down.
	high     uint16
	highDown bool
	haveHigh bool

	// rects is room for the areas of a Refresh Rect.
	rects []input.Rect
}

// takeSinks takes the sinks the application set since the last PDU, and
// gives a new one the state it missed.
func (sr *sessionReader) takeSinks() {
	s := sr.s
	s.mu.Lock()
	in, inGen, flow, flowGen := s.input, s.inputGen, s.flow, s.flowGen
	s.mu.Unlock()
	if inGen != sr.inGen {
		sr.in, sr.inGen = in, inGen
		if in != nil && sr.haveLocks {
			sr.syncLocks()
		}
	}
	if flowGen != sr.flowGen {
		sr.flow, sr.flowGen = flow, flowGen
		if flow != nil && s.suppressed.Load() {
			flow.OutputSuppressed(true)
		}
	}
}

func (sr *sessionReader) syncLocks() {
	sr.in.SyncLocks(sr.locks&input.SyncCapsLock != 0, sr.locks&input.SyncNumLock != 0, sr.locks&input.SyncScrollLock != 0)
}

// fastPath delivers the events of a fast-path input PDU.
func (sr *sessionReader) fastPath(frame []byte) error {
	f, err := input.ParseFastPath(frame)
	if err != nil {
		return err
	}
	for {
		e, ok, err := f.Next()
		if err != nil || !ok {
			return err
		}
		sr.event(e)
	}
}

// slowPath delivers the events of an Input Event PDU.
func (sr *sessionReader) slowPath(body []byte) error {
	p, err := input.ParseSlowPath(body)
	if err != nil {
		return err
	}
	for {
		e, ok, err := p.Next()
		if err != nil || !ok {
			return err
		}
		sr.event(e)
	}
}

// event delivers one event to the input sink.
func (sr *sessionReader) event(e input.Event) {
	switch e.Kind {
	case input.KindSync:
		sr.locks, sr.haveLocks = e.Flags, true
		if sr.in != nil {
			sr.syncLocks()
		}
	case input.KindUnicode:
		sr.unicode(e.Code, e.Flags&input.KeyRelease == 0)
	case input.KindScancode:
		if sr.in == nil {
			return
		}
		ext := NotExtended
		switch {
		case e.Flags&input.KeyExtended1 != 0:
			ext = ExtendedE1
		case e.Flags&input.KeyExtended != 0:
			ext = ExtendedE0
		}
		sr.in.Key(uint8(e.Code), ext, e.Flags&input.KeyRelease == 0)
	case input.KindMouse:
		if sr.in != nil {
			sr.mouse(e.Flags, int(e.X), int(e.Y))
		}
	case input.KindMouseX:
		if sr.in == nil {
			return
		}
		down := e.Flags&input.PtrXDown != 0
		if e.Flags&input.PtrXButton1 != 0 {
			sr.in.MouseButton(ButtonBack, down, int(e.X), int(e.Y))
		}
		if e.Flags&input.PtrXButton2 != 0 {
			sr.in.MouseButton(ButtonForward, down, int(e.X), int(e.Y))
		}
	case input.KindMouseRel:
		// The server does not ask for relative mouse events
		// (INPUT_FLAG_MOUSE_RELATIVE).
		sr.c.log.Debug("relative mouse event ignored")
	}
}

// mouse delivers a mouse event: a wheel turn, or a move and the buttons it
// names (2.2.8.1.1.3.1.1.3).
func (sr *sessionReader) mouse(f uint16, x, y int) {
	if f&(input.PtrWheel|input.PtrHWheel) != 0 {
		// The rotation is 9 bits of two's complement; the position is
		// not used.
		v := int(f & input.PtrWheelRotation)
		if f&input.PtrWheelNegative != 0 {
			v -= 0x200
		}
		if f&input.PtrWheel != 0 {
			sr.in.Wheel(0, v)
		} else {
			sr.in.Wheel(v, 0)
		}
		return
	}
	if f&input.PtrMove != 0 {
		sr.in.MouseMove(x, y)
	}
	down := f&input.PtrDown != 0
	if f&input.PtrButton1 != 0 {
		sr.in.MouseButton(ButtonLeft, down, x, y)
	}
	if f&input.PtrButton2 != 0 {
		sr.in.MouseButton(ButtonRight, down, x, y)
	}
	if f&input.PtrButton3 != 0 {
		sr.in.MouseButton(ButtonMiddle, down, x, y)
	}
}

// unicode delivers a UTF-16 code unit, joining surrogate pairs.
func (sr *sessionReader) unicode(u uint16, down bool) {
	if sr.haveHigh {
		sr.haveHigh = false
		if u >= 0xdc00 && u <= 0xdfff && down == sr.highDown {
			sr.char(utf16.DecodeRune(rune(sr.high), rune(u)), down)
			return
		}
		sr.char(utf8.RuneError, sr.highDown)
	}
	switch {
	case u >= 0xd800 && u <= 0xdbff:
		sr.high, sr.highDown, sr.haveHigh = u, down, true
	case u >= 0xdc00 && u <= 0xdfff:
		sr.char(utf8.RuneError, down)
	default:
		sr.char(rune(u), down)
	}
}

func (sr *sessionReader) char(r rune, down bool) {
	if sr.in != nil {
		sr.in.Unicode(r, down)
	}
}

// tpkt handles a TPKT the client sends in the session. It returns true when
// the client disconnected.
func (sr *sessionReader) tpkt(frame []byte) (bool, error) {
	c := sr.c
	r := wire.NewReader(frame[tpkt.HeaderLen:])
	if err := x224.ReadDataHeader(&r); err != nil {
		return false, err
	}
	if b := r.Rest(); len(b) > 0 && mcs.Choice(b[0]) == mcs.ChoiceDisconnectProviderUltimatum {
		reason, err := mcs.ReadDisconnectProviderUltimatum(&r)
		if err != nil {
			return false, err
		}
		c.log.Info("session closed by the client", "reason", reason)
		return true, nil
	}
	d, err := mcs.ReadSendDataRequest(&r)
	if err != nil {
		return false, err
	}
	if d.Initiator != c.userID || c.channelIndex(d.ChannelID) < 0 {
		return false, errors.New(TagChannelInvalid, "Send Data from another user or on a channel not handed out",
			"initiator", d.Initiator, "channel", d.ChannelID, "user", c.userID)
	}
	if d.ChannelID != ioChannelID {
		c.log.Debug("static channel data ignored", "channel", d.ChannelID, "length", len(d.Data))
		return false, nil
	}
	pr := wire.NewReader(d.Data)
	for pr.Remaining() > 0 {
		p, err := caps.ReadPDU(&pr)
		if err != nil {
			return false, err
		}
		if p.Flow {
			continue
		}
		if p.Type != caps.TypeData {
			c.log.Debug("share PDU ignored in the session", "type", p.Type)
			continue
		}
		if err := sr.data(p.Body); err != nil {
			return false, err
		}
	}
	return false, nil
}

// data handles a Data PDU the client sends in the session (SPEC §5.3).
func (sr *sessionReader) data(body []byte) error {
	c, s := sr.c, sr.s
	d, err := caps.ParseData(body)
	if err != nil {
		return err
	}
	if d.ShareID != shareID {
		return errors.New(TagShareIDInvalid, "a Data PDU for another share", "shareID", d.ShareID, "want", shareID)
	}
	switch d.Type2 {
	case caps.Type2Input:
		return sr.slowPath(d.Body)
	case caps.Type2ShutdownRequest:
		// "the server MUST respond with the Shutdown Request Denied PDU"
		// while a user is logged on (2.2.2.2); the client then decides
		// whether to disconnect.
		err := s.sendShare(queueControl, func(w *wire.Writer) error {
			return caps.WriteShutdownDenied(w, shareID, serverInitiator)
		})
		if err != nil {
			c.log.Debug("denying the client's shutdown request", "err", err)
		}
	case caps.Type2RefreshRect:
		if sr.rects, err = input.ParseRefreshRect(d.Body, sr.rects[:0]); err != nil {
			return err
		}
		c.log.Debug("refresh rect", "areas", len(sr.rects))
		if sr.flow != nil {
			sr.flow.KeyFrameWanted()
		}
	case caps.Type2SuppressOutput:
		so, err := input.ParseSuppressOutput(d.Body)
		if err != nil {
			return err
		}
		if was := s.suppressed.Swap(!so.Allow); was == !so.Allow {
			return nil
		}
		c.log.Debug("suppress output", "allow", so.Allow)
		if sr.flow != nil {
			sr.flow.OutputSuppressed(!so.Allow)
			if so.Allow {
				sr.flow.KeyFrameWanted()
			}
		}
	default:
		c.log.Debug("data PDU ignored in the session", "type2", d.Type2)
	}
	return nil
}
