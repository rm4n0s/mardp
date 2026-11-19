package rdp

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/caps"
	"github.com/rm4n0s/mardp/rdp/internal/gcc"
	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/sec"
	"github.com/rm4n0s/mardp/rdp/internal/tpkt"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors that end a connection in the capability exchange and
// the connection finalization (SPEC §5.1 steps 7 and 8).
const (
	// TagPDUUnexpected: a share PDU other than the one the sequence
	// expects (a Data PDU instead of Confirm Active, a Deactivate All, ...).
	TagPDUUnexpected = "ConnPDUUnexpected"
	// TagShareIDInvalid: a Confirm Active or Data PDU for another share
	// than the one the server's Demand Active opened.
	TagShareIDInvalid = "ConnShareIDInvalid"
	// TagDesktopSizeInvalid: Config.DesktopSize answered a width or height
	// outside 1 to MaxDesktopSide.
	TagDesktopSizeInvalid = "ConnDesktopSizeInvalid"
	// TagCloseDeadlineFailed: after Session.Close, the deadline for the
	// client to hang up could not be set; the cause is kept.
	TagCloseDeadlineFailed = "SessionCloseDeadlineFailed"
	// TagCloseAbandoned: Close's context ended before the client was told
	// why; the connection was closed without it. The cause is kept.
	TagCloseAbandoned = "SessionCloseAbandoned"
)

// MaxDesktopSide is the largest desktop width or height a client accepts
// (MS-RDPBCGR 2.2.1.3.2, desktopWidth and desktopHeight).
const MaxDesktopSide = 8192

// closeWait is how long a closed session waits for the client to hang up
// after the Disconnect Provider Ultimatum before it closes the connection.
// Closing at once could reset the connection before the client has read the
// reason, when its own last PDUs are still unread.
const closeWait = 2 * time.Second

// maxRequestSize is the Multifragment Update set the server sends: the
// largest Fast-Path Update it could reassemble. Clients send the server
// nothing fragmented; what limits the server's own updates is the size the
// client answers with (Client.MaxRequestSize).
const maxRequestSize = 8 << 20

// Handler is what the application gives the server to get sessions.
type Handler interface {
	// SessionOpened is called on a goroutine of its own when a client has
	// finished the connection sequence. ctx is canceled when the session
	// ends, whoever ends it; the connection stays open until SessionOpened
	// has returned and the client or Session.Close has ended it.
	SessionOpened(ctx context.Context, s *Session)
}

// HandlerFunc is a Handler that is a function.
type HandlerFunc func(ctx context.Context, s *Session)

// SessionOpened calls f.
func (f HandlerFunc) SessionOpened(ctx context.Context, s *Session) { f(ctx, s) }

// CloseReason is why the server ends a session. It tells the client with
// Set Error Info (SPEC §5.4).
type CloseReason int

// The reasons a session is ended for.
const (
	// CloseByHost: the Mac's user ended the session
	// (ERRINFO_RPC_INITIATED_DISCONNECT).
	CloseByHost CloseReason = iota + 1
	// CloseReplaced: another client connected and took the session over
	// (ERRINFO_DISCONNECTED_BY_OTHERCONNECTION, SPEC D13). The server
	// sends it itself when a new session opens.
	CloseReplaced
	// CloseServerStopped: the server is stopping (ERRINFO_SERVER_SHUTDOWN).
	// The server sends it itself when Serve's context is canceled.
	CloseServerStopped
)

// String names r for the log.
func (r CloseReason) String() string {
	switch r {
	case CloseByHost:
		return "closed by the host"
	case CloseReplaced:
		return "replaced by another client"
	case CloseServerStopped:
		return "server stopped"
	}
	return "unknown reason"
}

// errorInfo is the Set Error Info code for r.
func (r CloseReason) errorInfo() uint32 {
	switch r {
	case CloseReplaced:
		return sec.ErrInfoDisconnectedByOther
	case CloseServerStopped:
		return sec.ErrInfoServerShutdown
	}
	return sec.ErrInfoRPCInitiatedDisconnect
}

// Client is what the server knows about a client when its session opens:
// its core data (MS-RDPBCGR 2.2.1.3.2), its login and the capability sets of
// its Confirm Active (2.2.1.13.2).
type Client struct {
	// Name is the client's computer name.
	Name string
	// Remote is the client's address.
	Remote net.Addr
	// User and Domain are what the client logged in as.
	User, Domain string
	// Build is the client's build number.
	Build uint32
	// DesktopWidth and DesktopHeight are the desktop size the client asked
	// for (SPEC §9.2); the session's size is Session.DesktopSize.
	DesktopWidth, DesktopHeight int
	// KeyboardLayout is the client's input locale identifier (0x409 for US
	// English); KeyboardType, KeyboardSubType and KeyboardFunctionKeys
	// describe the keyboard.
	KeyboardLayout, KeyboardType, KeyboardSubType, KeyboardFunctionKeys uint32
	// GraphicsPipeline says the client supports the graphics pipeline
	// (RNS_UD_CS_SUPPORT_DYNVC_GFX_PROTOCOL).
	GraphicsPipeline bool
	// Channels are the static channels the client asked for, in its order.
	Channels []string

	// FastPathOutput says the client takes fast-path output.
	FastPathOutput bool
	// DesktopResize says the client can follow a new desktop size.
	DesktopResize bool
	// SkipAlpha says the client takes 32-bit bitmaps without their alpha
	// (DRAW_ALLOW_SKIP_ALPHA).
	SkipAlpha bool
	// InputScancodes, InputUnicode, InputExtendedMouse,
	// InputHorizontalWheel, InputRelativeMouse and InputFastPath are the
	// input the client says it sends (INPUT_FLAG_SCANCODES, _UNICODE,
	// _MOUSEX, _MOUSE_HWHEEL, _MOUSE_RELATIVE and _FASTPATH_INPUT2).
	InputScancodes, InputUnicode, InputExtendedMouse, InputHorizontalWheel, InputRelativeMouse, InputFastPath bool
	// AlphaPointer says the client takes the New Pointer Update, so its
	// pointer shapes keep their alpha: it gave a pointerCacheSize that is
	// not 0. PointerCacheSize is how many shapes the client caches: that
	// size, or without it the size of its 24 bpp color pointer cache.
	// ColorPointer is the client's colorPointerFlag, which MS-RDPBCGR
	// says to ignore.
	AlphaPointer     bool
	PointerCacheSize int
	ColorPointer     bool
	// LargePointer is the largest pointer side the client allows in Large
	// Pointer updates: 384, 96, or 0 when it has no Large Pointer
	// Capability Set.
	LargePointer int
	// MaxRequestSize is the largest fast-path update the client
	// reassembles from fragments, 0 when it did not say.
	MaxRequestSize int
	// SurfaceBits and FrameMarker say the client takes the Surface Bits
	// and Frame Marker surface commands.
	SurfaceBits, FrameMarker bool
	// FrameAcknowledge says the client acknowledges frames, with at most
	// MaxUnacknowledgedFrames of them unacknowledged (0: no limit stated).
	FrameAcknowledge        bool
	MaxUnacknowledgedFrames int
}

// Session is one client's open session (SPEC §6).
type Session struct {
	c      *conn
	client Client
	width  int
	height int
	cancel context.CancelFunc
	// wr writes everything the session sends.
	wr *writer
	// video is the session's video output.
	video   Video
	pointer Pointer
	// channels are the static channels the server reads.
	channels []*staticChannel
	// dvc is the manager of dynamic channels, nil when the client joined
	// no drdynvc; dvcWait is how long the client has to answer its
	// capabilities.
	dvc     *dvcManager
	dvcWait time.Duration

	mu sync.Mutex
	// reason is why the server ended the session; 0 while it is open or
	// when the client ended it.
	reason CloseReason
	// ended is set when the connection is over.
	ended bool
	// input and flow are the sinks the application set; inputGen and
	// flowGen count the times they were set.
	input    InputSink
	inputGen uint64
	flow     FlowSink
	flowGen  uint64

	// suppressed is set while the client wants no output.
	suppressed atomic.Bool
}

// Client returns what the server knows about the client.
func (s *Session) Client() *Client { return &s.client }

// DesktopSize returns the session's desktop size, which the server stated in
// Demand Active.
func (s *Session) DesktopSize() (width, height int) { return s.width, s.height }

// Close ends the session for reason: Set Error Info, when the client said it
// understands it, and a Disconnect Provider Ultimatum (SPEC §5.4), ahead of
// any video still queued. The session's context is canceled. Close returns
// when they are written; on a session that is over it does nothing. When
// ctx ends first, the connection is closed without them.
func (s *Session) Close(ctx context.Context, reason CloseReason) error {
	s.mu.Lock()
	if s.ended || s.reason != 0 {
		s.mu.Unlock()
		return nil
	}
	s.reason = reason
	s.mu.Unlock()
	// The session's context ends after the write: a handler passes its own
	// context to Close, which would abandon the write at once.
	defer s.cancel()
	if err := ctx.Err(); err != nil {
		s.closeConn()
		return errors.NewErr(TagCloseAbandoned, err)
	}
	done := make(chan error, 1)
	err := s.wr.send(queueControl, func(p *packet) error {
		p.done = done
		return s.c.appendDisconnect(&p.w, &p.tmp, reason.errorInfo())
	})
	if err != nil {
		s.closeConn()
		return err
	}
	select {
	case err = <-done:
	case <-s.wr.exited:
		// The writer stops after it reports the write, so a report may be
		// waiting.
		select {
		case err = <-done:
		default:
			return errors.New(TagSessionOver, "the session ended before the client was told", "reason", reason.String())
		}
	case <-ctx.Done():
		s.closeConn()
		return errors.NewErr(TagCloseAbandoned, ctx.Err())
	}
	if err != nil {
		return err
	}
	if err := s.c.nc.SetReadDeadline(time.Now().Add(closeWait)); err != nil {
		s.closeConn()
		return errors.NewErr(TagCloseDeadlineFailed, err)
	}
	return nil
}

func (s *Session) closeConn() {
	if err := s.c.nc.Close(); err != nil {
		s.c.log.Debug("closing the connection", "err", err)
	}
}

// closedBy returns why the server ended the session, 0 when it did not.
func (s *Session) closedBy() CloseReason {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

// end marks the connection over, so that Close does nothing.
func (s *Session) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = true
}

// newClient gathers what the client said about itself before activation.
func (c *conn) newClient() Client {
	core := &c.client.Core
	return Client{
		Name:                 core.Name(),
		Remote:               c.nc.RemoteAddr(),
		User:                 c.user,
		Domain:               c.domain,
		Build:                core.ClientBuild,
		DesktopWidth:         int(core.DesktopWidth),
		DesktopHeight:        int(core.DesktopHeight),
		KeyboardLayout:       core.KeyboardLayout,
		KeyboardType:         core.KeyboardType,
		KeyboardSubType:      core.KeyboardSubType,
		KeyboardFunctionKeys: core.KeyboardFunctionKey,
		GraphicsPipeline:     core.Early(gcc.EarlySupportDynvcGFXProtocol),
		Channels:             c.channelNames(),
	}
}

// addCaps adds what a Confirm Active says to cl.
func addCaps(cl *Client, cc *caps.Caps) {
	cl.FastPathOutput = cc.Has(caps.SetGeneral) && cc.General.ExtraFlags&caps.ExtraFastPathOutput != 0
	cl.DesktopResize = cc.Has(caps.SetBitmap) && cc.Bitmap.DesktopResize
	cl.SkipAlpha = cc.Has(caps.SetBitmap) && cc.Bitmap.DrawingFlags&caps.DrawAllowSkipAlpha != 0
	if cc.Has(caps.SetInput) {
		f := cc.Input.Flags
		cl.InputScancodes = f&caps.InputScancodes != 0
		cl.InputUnicode = f&caps.InputUnicode != 0
		cl.InputExtendedMouse = f&caps.InputMouseX != 0
		cl.InputHorizontalWheel = f&caps.InputMouseHWheel != 0
		cl.InputRelativeMouse = f&caps.InputMouseRelative != 0
		cl.InputFastPath = f&caps.InputFastPath2 != 0
	}
	if cc.Has(caps.SetPointer) {
		cl.ColorPointer = cc.Pointer.Color
		cl.PointerCacheSize = int(cc.Pointer.ColorCacheSize)
		if cc.Pointer.HasCacheSize && cc.Pointer.CacheSize != 0 {
			cl.AlphaPointer = true
			cl.PointerCacheSize = int(cc.Pointer.CacheSize)
		}
	}
	if cc.Has(caps.SetLargePointer) {
		switch {
		case cc.LargePointer&caps.LargePointer384 != 0:
			cl.LargePointer = 384
		case cc.LargePointer&caps.LargePointer96 != 0:
			cl.LargePointer = 96
		}
	}
	if cc.Has(caps.SetMultifragmentUpdate) {
		cl.MaxRequestSize = int(cc.MaxRequestSize)
	}
	if cc.Has(caps.SetSurfaceCommands) {
		cl.SurfaceBits = cc.SurfaceCommands&(caps.SurfaceSetSurfaceBits|caps.SurfaceStreamSurfaceBits) != 0
		cl.FrameMarker = cc.SurfaceCommands&caps.SurfaceFrameMarker != 0
	}
	if cc.Has(caps.SetFrameAcknowledge) {
		cl.FrameAcknowledge = true
		cl.MaxUnacknowledgedFrames = int(cc.MaxUnacknowledgedFrames)
	}
}

// serverCaps are the capability sets of the server's Demand Active (SPEC
// §5.2): 32 bits per pixel at the session's size, fast-path output, the
// input of §5.2, color and large pointers, no orders.
func serverCaps(width, height int) caps.Caps {
	var c caps.Caps
	for _, t := range []uint16{
		caps.SetGeneral, caps.SetBitmap, caps.SetOrder, caps.SetPointer, caps.SetInput,
		caps.SetVirtualChannel, caps.SetShare, caps.SetFont, caps.SetMultifragmentUpdate,
		caps.SetLargePointer, caps.SetSurfaceCommands, caps.SetFrameAcknowledge,
	} {
		c.Add(t)
	}
	c.General = caps.General{
		OSMajor:        caps.OSMajorWindows,
		OSMinor:        caps.OSMinorWindowsNT,
		ExtraFlags:     caps.ExtraFastPathOutput | caps.ExtraLongCredentials | caps.ExtraNoBitmapCompHdr,
		RefreshRect:    true,
		SuppressOutput: true,
	}
	c.Bitmap = caps.Bitmap{BitsPerPixel: 32, DesktopWidth: uint16(width), DesktopHeight: uint16(height)}
	c.Pointer = caps.Pointer{Color: true, ColorCacheSize: 25, CacheSize: 25, HasCacheSize: true}
	c.Input = caps.Input{Flags: caps.InputScancodes | caps.InputMouseX | caps.InputUnicode | caps.InputFastPath2 | caps.InputMouseHWheel}
	c.ShareNodeID = serverInitiator
	c.FontFlags = caps.FontSupportList
	c.MaxRequestSize = maxRequestSize
	c.LargePointer = caps.LargePointer96 | caps.LargePointer384
	c.SurfaceCommands = caps.SurfaceSetSurfaceBits | caps.SurfaceFrameMarker | caps.SurfaceStreamSurfaceBits
	return c
}

// activate runs the capability exchange and the connection finalization
// (SPEC §5.1 steps 7 and 8, MS-RDPBCGR 1.3.1.1): Demand Active, the
// client's Confirm Active, and the Synchronize, Control and Font PDUs, each
// of the client's answered as it comes. It returns the session, which is
// open once Font Map is sent.
func (c *conn) activate(desktopSize func(*Client) (int, int)) (*Session, error) {
	s := &Session{c: c, client: c.newClient()}
	s.video.s = s
	s.openChannels()
	s.width, s.height = s.client.DesktopWidth, s.client.DesktopHeight
	if desktopSize != nil {
		s.width, s.height = desktopSize(&s.client)
	}
	if s.width < 1 || s.width > MaxDesktopSide || s.height < 1 || s.height > MaxDesktopSide {
		return nil, errors.New(TagDesktopSizeInvalid, "Config.DesktopSize answered a size no client accepts",
			"width", s.width, "height", s.height, "max", MaxDesktopSide)
	}
	da := caps.DemandActive{
		ShareID:          shareID,
		Source:           serverInitiator,
		SourceDescriptor: []byte("RDP\x00"),
		Caps:             serverCaps(s.width, s.height),
	}
	if err := c.sendIO(func(w *wire.Writer) error { return caps.WriteDemandActive(w, &da) }); err != nil {
		return nil, err
	}
	p, err := c.readShare()
	if err != nil {
		return nil, err
	}
	if p.Type != caps.TypeConfirmActive {
		return nil, errors.New(TagPDUUnexpected, "another PDU than Confirm Active after Demand Active", "type", p.Type)
	}
	ca, err := caps.ParseConfirmActive(p)
	if err != nil {
		return nil, err
	}
	if ca.ShareID != shareID {
		return nil, errors.New(TagShareIDInvalid, "a Confirm Active for another share", "shareID", ca.ShareID, "want", shareID)
	}
	addCaps(&s.client, &ca.Caps)
	s.pointer.init(s)
	c.log.Debug("confirm active", "sets", ca.Caps.Sets, "fastPathOutput", s.client.FastPathOutput,
		"maxRequestSize", s.client.MaxRequestSize, "input", ca.Caps.Input.Flags)
	if err := c.finalize(); err != nil {
		return nil, err
	}
	return s, nil
}

// finalize answers the client's finalization PDUs until its Font List
// (MS-RDPBCGR 2.2.1.14 to 2.2.1.22): Synchronize with Synchronize, Control
// Cooperate with Cooperate, Control Request with Granted, Font List with
// Font Map. Other Data PDUs (Persistent Key List, ...) are ignored.
func (c *conn) finalize() error {
	for {
		p, err := c.readShare()
		if err != nil {
			return err
		}
		if p.Type != caps.TypeData {
			return errors.New(TagPDUUnexpected, "another PDU than a Data PDU in the connection finalization", "type", p.Type)
		}
		d, err := caps.ParseData(p.Body)
		if err != nil {
			return err
		}
		if d.ShareID != shareID {
			return errors.New(TagShareIDInvalid, "a Data PDU for another share", "shareID", d.ShareID, "want", shareID)
		}
		var answer func(w *wire.Writer) error
		switch d.Type2 {
		case caps.Type2Synchronize:
			if _, err := caps.ParseSynchronize(d.Body); err != nil {
				return err
			}
			answer = func(w *wire.Writer) error { return caps.WriteSynchronize(w, shareID, serverInitiator, c.userID) }
		case caps.Type2Control:
			ctl, err := caps.ParseControl(d.Body)
			if err != nil {
				return err
			}
			switch ctl.Action {
			case caps.ActionCooperate:
				answer = func(w *wire.Writer) error {
					return caps.WriteControl(w, shareID, serverInitiator, caps.Control{Action: caps.ActionCooperate})
				}
			case caps.ActionRequestControl:
				answer = func(w *wire.Writer) error {
					return caps.WriteControl(w, shareID, serverInitiator,
						caps.Control{Action: caps.ActionGrantedControl, GrantID: c.userID, ControlID: serverInitiator})
				}
			}
		case caps.Type2FontList:
			if _, err := caps.ParseFonts(d.Body); err != nil {
				return err
			}
			return c.sendIO(func(w *wire.Writer) error { return caps.WriteFontMap(w, shareID, serverInitiator) })
		default:
			c.log.Debug("data PDU ignored in the finalization", "type2", d.Type2)
		}
		if answer != nil {
			if err := c.sendIO(answer); err != nil {
				return err
			}
		}
	}
}

// readShare reads the next share PDU the client sends on the I/O channel,
// skipping FlowPDUs and what comes on the static channels. A Send Data
// Request carrying more than one share PDU is not expected before the
// session; the bytes after the first are ignored.
func (c *conn) readShare() (caps.PDU, error) {
	for {
		r, err := c.readData()
		if err != nil {
			return caps.PDU{}, err
		}
		d, err := mcs.ReadSendDataRequest(&r)
		if err != nil {
			return caps.PDU{}, err
		}
		if d.Initiator != c.userID || c.channelIndex(d.ChannelID) < 0 {
			return caps.PDU{}, errors.New(TagChannelInvalid, "Send Data from another user or on a channel not handed out",
				"initiator", d.Initiator, "channel", d.ChannelID, "user", c.userID)
		}
		if d.ChannelID != ioChannelID {
			c.log.Debug("static channel data ignored before the session", "channel", d.ChannelID, "length", len(d.Data))
			continue
		}
		sr := wire.NewReader(d.Data)
		p, err := caps.ReadPDU(&sr)
		if err != nil {
			return caps.PDU{}, err
		}
		if !p.Flow {
			return p, nil
		}
	}
}

// runSession hands the open session to h and reads what the client sends
// until the session ends (SPEC §5.3, §5.4). It first replaces the session
// open in o, if any, and waits for it to end (SPEC D13). It cancels the
// session's context and waits for h before it returns. When ctx is canceled
// the session is closed with CloseServerStopped.
func (c *conn) runSession(ctx context.Context, s *Session, h Handler, o *openSession) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.cancel = cancel
	s.wr = newWriter(c.rw, func(err error) {
		c.log.Debug("writing to the client", "err", err)
		s.closeConn()
	})
	go s.wr.run()
	stop := context.AfterFunc(ctx, func() {
		if err := s.Close(context.Background(), CloseServerStopped); err != nil {
			c.log.Debug("closing the session as the server stops", "err", err)
		}
	})
	defer stop()
	done := make(chan struct{})
	defer close(done)
	if prev := o.replace(s, done); prev != nil {
		c.log.Info("replacing the open session", "client", prev.s.client.Name)
		// A client that reads nothing would hold Close forever: after
		// closeWait the connection is closed without the reason.
		cctx, ccancel := context.WithTimeout(ctx, closeWait)
		if err := prev.s.Close(cctx, CloseReplaced); err != nil {
			c.log.Debug("closing the replaced session", "err", err)
		}
		ccancel()
		<-prev.done
	}
	defer o.leave(s)
	c.log.Info("session opened", "client", s.client.Name, "width", s.width, "height", s.height,
		"graphicsPipeline", s.client.GraphicsPipeline)
	var wg sync.WaitGroup
	// A session replaced or stopped before the one it replaced has ended
	// never reaches the application.
	if sctx.Err() == nil {
		s.startDVC(s.dvcWait)
		wg.Go(func() { h.SessionOpened(sctx, s) })
	}
	err := c.readSession(s)
	s.end()
	s.stopDVC()
	cancel()
	// What is still queued is dropped; a write the client does not read
	// fails now.
	if derr := c.nc.SetWriteDeadline(time.Now()); derr != nil {
		c.log.Debug("ending the session's writes", "err", derr)
	}
	s.wr.halt()
	wg.Wait()
	return err
}

// readSession reads the client's PDUs until it disconnects or, after
// Session.Close, hangs up or lets closeWait pass. It delivers the client's
// input and requests (SPEC §5.3).
func (c *conn) readSession(s *Session) error {
	sr := sessionReader{c: c, s: s}
	for {
		k, f, err := tpkt.ReadFrame(c.rw, c.buf)
		if err != nil {
			if r := s.closedBy(); r != 0 {
				c.log.Info("session closed", "reason", r.String())
				return nil
			}
			// A failed write closes the connection; that is why the read
			// failed.
			if werr := s.wr.failure(); werr != nil {
				return werr
			}
			return err
		}
		sr.takeSinks()
		if k == tpkt.FastPath {
			if err := sr.fastPath(f); err != nil {
				return err
			}
			continue
		}
		over, err := sr.tpkt(f)
		if err != nil || over {
			return err
		}
	}
}
