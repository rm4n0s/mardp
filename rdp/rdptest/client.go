// Package rdptest is a small RDP client for tests of the rdp package and of
// the host: it runs the connection sequence against a server and lets a test
// step through it, or break it on purpose (SPEC §4.1, §15.2). Only tests
// import it.
package rdptest

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/caps"
	"github.com/rm4n0s/mardp/rdp/internal/dynvc"
	"github.com/rm4n0s/mardp/rdp/internal/fastpath"
	"github.com/rm4n0s/mardp/rdp/internal/gcc"
	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/sec"
	"github.com/rm4n0s/mardp/rdp/internal/svc"
	"github.com/rm4n0s/mardp/rdp/internal/tpkt"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/internal/x224"
)

// Tags of the errors the client returns.
const (
	// TagNegotiationFailed: the server answered the Connection Request with
	// RDP_NEG_FAILURE (the code is in the metadata) or selected another
	// protocol than TLS.
	TagNegotiationFailed = "ClientNegotiationFailed"
	// TagTLSHandshakeFailed: the TLS handshake failed; the cause is kept.
	TagTLSHandshakeFailed = "ClientTLSHandshakeFailed"
	// TagWriteFailed: writing to the connection failed; the cause is kept.
	TagWriteFailed = "ClientWriteFailed"
	// TagDeadlineFailed: the context's deadline could not be set on the
	// connection; the cause is kept.
	TagDeadlineFailed = "ClientDeadlineFailed"
	// TagFrameUnexpected: a fast-path PDU where a TPKT was expected.
	TagFrameUnexpected = "ClientFrameUnexpected"
	// TagConnectRefused: a Connect-Response or Conference Create Response
	// with a result other than success.
	TagConnectRefused = "ClientConnectRefused"
	// TagChannelsMismatch: server network data with another number of
	// channels than the client asked for.
	TagChannelsMismatch = "ClientChannelsMismatch"
	// TagAttachRefused: an Attach User Confirm without success.
	TagAttachRefused = "ClientAttachRefused"
	// TagJoinRefused: a Channel Join Confirm without success for a channel
	// the server handed out.
	TagJoinRefused = "ClientJoinRefused"
	// TagLoginRefused: the server answered Client Info with a Disconnect
	// Provider Ultimatum; the metadata has its reason and the Set Error
	// Info code before it ("errorInfo", 0 when there was none).
	TagLoginRefused = "ClientLoginRefused"
	// TagLicenseInvalid: a license error message other than "valid client".
	TagLicenseInvalid = "ClientLicenseInvalid"
)

// What a Client sends when its Options leave a field empty: what FreeRDP and
// Remmina send on automatic security (rdp/testdata/x224), a 1024×768
// desktop, and the two static channels mardp opens.
const (
	DefaultProtocols = x224.ProtocolSSL | x224.ProtocolHybrid | x224.ProtocolHybridEx
	DefaultWidth     = 1024
	DefaultHeight    = 768
	DefaultName      = "rdptest"
)

// DefaultChannels are the static channels a Client asks for when its
// Options name none.
var DefaultChannels = []string{"rdpsnd", "drdynvc"}

// Options say what a Client sends.
type Options struct {
	// TLSConfig is the client's TLS configuration; it has to trust the
	// server's certificate (see Certificate).
	TLSConfig *tls.Config
	// NoNegotiation sends a Connection Request without RDP_NEG_REQ, as
	// FreeRDP does with /sec:rdp.
	NoNegotiation bool
	// Protocols are the protocols offered; 0 means DefaultProtocols.
	Protocols uint32
	// Cookie is the mstshash cookie; "" means DefaultName.
	Cookie string
	// Width and Height are the desktop size asked for; 0 means the default.
	Width, Height uint16
	// Name is the client name; "" means DefaultName.
	Name string
	// Channels are the static channels asked for; nil means
	// DefaultChannels, and an empty slice none.
	Channels []string
	// User, Domain and Password are what Client Info logs in with.
	User, Domain, Password string
	// EditClientInfo changes Client Info before it is sent.
	EditClientInfo func(ci *sec.ClientInfo)
	// EditClientData changes the client's data blocks before they are
	// sent, to test what the server makes of them.
	EditClientData func(cd *gcc.ClientData)
	// EditCaps changes the capability sets of Confirm Active before they
	// are sent.
	EditCaps func(c *caps.Caps)
}

// Client is one connection to a server. Connect runs the whole sequence; the
// step methods run it one step at a time, in the order Connect calls them.
type Client struct {
	opts Options
	nc   net.Conn
	rw   io.ReadWriter
	buf  []byte
	w    wire.Writer

	// Selected is the protocol the server selected.
	Selected uint32
	// Server is the server's data blocks; ServerBlocks holds them.
	Server       gcc.ServerData
	ServerBlocks []byte
	// Parameters are the domain parameters of the Connect-Response.
	Parameters mcs.DomainParameters
	// UserID is the user ID the server attached the client as.
	UserID uint16
	// Joined are the channels joined, in order: the user channel, the I/O
	// channel, then the static channels.
	Joined []uint16
	// ShareID, ServerSource and ServerCaps are the share ID, the source and
	// the capability sets of the server's Demand Active.
	ShareID      uint32
	ServerSource uint16
	ServerCaps   caps.Caps
	// Caps are the capability sets the client confirmed.
	Caps caps.Caps

	// reassembly joins fragmented fast-path updates.
	reassembly fastpath.Reassembler
	// joiners join the chunks of each static channel ReadChannel reads.
	joiners map[uint16]*svc.Joiner
	// kept holds, for each channel KeepChannel keeps, the chunks
	// ReadChannel read while it read another.
	kept map[uint16][]keptChunk
	// dvcJoiners join the PDUs of each dynamic channel ReadDVCMessage
	// reads.
	dvcJoiners map[uint32]*dynvc.Joiner
}

// New returns a client on nc that has not sent anything yet.
func New(nc net.Conn, opts Options) *Client {
	if opts.Protocols == 0 {
		opts.Protocols = DefaultProtocols
	}
	if opts.Cookie == "" {
		opts.Cookie = DefaultName
	}
	if opts.Width == 0 {
		opts.Width = DefaultWidth
	}
	if opts.Height == 0 {
		opts.Height = DefaultHeight
	}
	if opts.Name == "" {
		opts.Name = DefaultName
	}
	if opts.Channels == nil {
		opts.Channels = DefaultChannels
	}
	return &Client{opts: opts, nc: nc, rw: nc, buf: make([]byte, tpkt.MaxLength)}
}

// Connect runs the connection sequence on nc up to the end of the channel
// joins.
func Connect(ctx context.Context, nc net.Conn, opts Options) (*Client, error) {
	c := New(nc, opts)
	steps := []func(context.Context) error{c.Negotiate, c.StartTLS, c.ConnectMCS, c.Attach, c.JoinAll}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return c, err
		}
	}
	return c, nil
}

// Close closes the connection.
func (c *Client) Close() error {
	return c.nc.Close()
}

// CloseWrite ends the client's half of the connection, so the server reads
// the end of the stream, when the connection can be half-closed (TCP). With
// TLS started, it sends TLS's close_notify first.
func (c *Client) CloseWrite() error {
	if tc, ok := c.rw.(*tls.Conn); ok {
		if err := tc.CloseWrite(); err != nil {
			return errors.NewErr(TagWriteFailed, err).SetMetadata("part", "close_notify")
		}
	}
	if hc, ok := c.nc.(interface{ CloseWrite() error }); ok {
		if err := hc.CloseWrite(); err != nil {
			return errors.NewErr(TagWriteFailed, err).SetMetadata("part", "close")
		}
	}
	return nil
}

// watch makes ctx's deadline and cancellation end what the client does on
// the connection until the returned function is called.
func (c *Client) watch(ctx context.Context) (func(), error) {
	d, _ := ctx.Deadline() // the zero time, no deadline, when it has none
	if err := c.nc.SetDeadline(d); err != nil {
		return nil, errors.NewErr(TagDeadlineFailed, err)
	}
	stop := context.AfterFunc(ctx, func() {
		// A deadline in the past wakes a blocked read or write, and the step
		// fails with its error. If the connection is already closed, there
		// is nothing to wake.
		if err := c.nc.SetDeadline(time.Unix(1, 0)); err != nil {
			return
		}
	})
	return func() { stop() }, nil
}

// WriteFrame writes b as it is: a whole frame, or anything a test wants the
// server to get.
func (c *Client) WriteFrame(ctx context.Context, b []byte) error {
	stop, err := c.watch(ctx)
	if err != nil {
		return err
	}
	defer stop()
	if _, err := c.rw.Write(b); err != nil {
		return errors.NewErr(TagWriteFailed, err).SetMetadata("length", len(b))
	}
	return nil
}

// ReadFrame reads the next frame from the server, TPKT or fast-path. The
// bytes are valid until the next read.
func (c *Client) ReadFrame(ctx context.Context) (tpkt.Kind, []byte, error) {
	stop, err := c.watch(ctx)
	if err != nil {
		return tpkt.Unknown, nil, err
	}
	defer stop()
	return tpkt.ReadFrame(c.rw, c.buf)
}

func (c *Client) send(body func(w *wire.Writer) error) error {
	c.w.Reset()
	off := tpkt.Begin(&c.w)
	if err := body(&c.w); err != nil {
		return err
	}
	if err := tpkt.End(&c.w, off); err != nil {
		return err
	}
	if _, err := c.rw.Write(c.w.Bytes()); err != nil {
		return errors.NewErr(TagWriteFailed, err).SetMetadata("length", c.w.Len())
	}
	return nil
}

// SendData writes one X.224 Data TPDU whose MCS PDU body appends.
func (c *Client) SendData(ctx context.Context, body func(w *wire.Writer) error) error {
	stop, err := c.watch(ctx)
	if err != nil {
		return err
	}
	defer stop()
	return c.sendData(body)
}

func (c *Client) sendData(body func(w *wire.Writer) error) error {
	return c.send(func(w *wire.Writer) error {
		x224.WriteDataHeader(w)
		return body(w)
	})
}

func (c *Client) readTPKT() ([]byte, error) {
	k, f, err := tpkt.ReadFrame(c.rw, c.buf)
	if err != nil {
		return nil, err
	}
	if k != tpkt.TPKT {
		return nil, errors.New(TagFrameUnexpected, "a fast-path PDU where a TPKT was expected", "length", len(f))
	}
	return f[tpkt.HeaderLen:], nil
}

// ReadData reads the next frame, which has to be an X.224 Data TPDU, and
// returns a reader at its MCS PDU. The bytes are valid until the next read.
func (c *Client) ReadData(ctx context.Context) (wire.Reader, error) {
	stop, err := c.watch(ctx)
	if err != nil {
		return wire.Reader{}, err
	}
	defer stop()
	return c.readData()
}

func (c *Client) readData() (wire.Reader, error) {
	tpdu, err := c.readTPKT()
	if err != nil {
		return wire.Reader{}, err
	}
	r := wire.NewReader(tpdu)
	if err := x224.ReadDataHeader(&r); err != nil {
		return r, err
	}
	return r, nil
}

// Negotiate sends the X.224 Connection Request and reads the Confirm. The
// server has to select TLS.
func (c *Client) Negotiate(ctx context.Context) error {
	stop, err := c.watch(ctx)
	if err != nil {
		return err
	}
	defer stop()
	cr := x224.ConnectionRequest{
		Cookie:             []byte(c.opts.Cookie),
		HasNegReq:          !c.opts.NoNegotiation,
		RequestedProtocols: c.opts.Protocols,
	}
	if err := c.send(func(w *wire.Writer) error { return x224.WriteConnectionRequest(w, &cr) }); err != nil {
		return err
	}
	tpdu, err := c.readTPKT()
	if err != nil {
		return err
	}
	cc, err := x224.ParseConnectionConfirm(tpdu)
	if err != nil {
		return err
	}
	if cc.Negotiation != x224.NegResponse || cc.SelectedProtocol != x224.ProtocolSSL {
		return errors.New(TagNegotiationFailed, "the server did not select TLS",
			"negotiation", int(cc.Negotiation), "failure", cc.FailureCode, "selected", cc.SelectedProtocol)
	}
	c.Selected = cc.SelectedProtocol
	return nil
}

// StartTLS runs the TLS handshake as the client.
func (c *Client) StartTLS(ctx context.Context) error {
	cfg := c.opts.TLSConfig
	if cfg == nil {
		cfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	tc := tls.Client(c.nc, cfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		return errors.NewErr(TagTLSHandshakeFailed, err)
	}
	c.rw = tc
	return nil
}

// ClientData is what the client sends in its Connect-Initial before
// Options.EditClientData changes it: the core block of a recent client with
// every optional field up to serverSelectedProtocol, and the security,
// network and cluster blocks.
func (c *Client) ClientData() gcc.ClientData {
	cd := gcc.ClientData{
		Core: gcc.ClientCore{
			Version:                gcc.VersionRDP10,
			DesktopWidth:           c.opts.Width,
			DesktopHeight:          c.opts.Height,
			ColorDepth:             0xca01, // RNS_UD_COLOR_8BPP: superseded by the fields below
			SASSequence:            0xaa03,
			KeyboardLayout:         0x409,
			ClientBuild:            20000,
			ClientName:             utf16Name(c.opts.Name, 32),
			KeyboardType:           4,
			KeyboardFunctionKey:    12,
			IMEFileName:            make([]byte, 64),
			OptionalFields:         gcc.OptServerSelectedProtocol,
			PostBeta2ColorDepth:    0xca01,
			ClientProductID:        1,
			HighColorDepth:         24,
			SupportedColorDepths:   gcc.Support32BPP | gcc.Support24BPP | gcc.Support16BPP | gcc.Support15BPP,
			EarlyCapabilityFlags:   gcc.EarlySupportErrInfoPDU | gcc.EarlyWant32BPPSession | gcc.EarlySupportDynvcGFXProtocol,
			ClientDigProductID:     make([]byte, 64),
			ConnectionType:         6, // CONNECTION_TYPE_LAN
			ServerSelectedProtocol: c.Selected,
		},
		HasSecurity: true,
		HasNetwork:  true,
		Network:     gcc.ClientNetwork{ChannelCount: len(c.opts.Channels)},
		HasCluster:  true,
		Cluster:     gcc.ClientCluster{Flags: 0x0d},
	}
	for i, name := range c.opts.Channels {
		copy(cd.Network.Channels[i].Name[:7], name)
		cd.Network.Channels[i].Options = gcc.ChannelOptionInitialized
	}
	return cd
}

// utf16Name writes s as UTF-16LE into n bytes, NUL-padded.
func utf16Name(s string, n int) []byte {
	b := make([]byte, n)
	for i, r := range []rune(s) {
		if 2*i+1 >= n-2 {
			break
		}
		b[2*i], b[2*i+1] = byte(r), byte(r>>8)
	}
	return b
}

// clientParameters are the domain parameters of Microsoft's Connect-Initial
// (MS-RDPBCGR 4.1.3).
var clientParameters = [3]mcs.DomainParameters{
	{MaxChannelIDs: 34, MaxUserIDs: 2, MaxTokenIDs: 0, NumPriorities: 1, MinThroughput: 0, MaxHeight: 1, MaxMCSPDUSize: 65535, ProtocolVersion: 2},
	{MaxChannelIDs: 1, MaxUserIDs: 1, MaxTokenIDs: 1, NumPriorities: 1, MinThroughput: 0, MaxHeight: 1, MaxMCSPDUSize: 1056, ProtocolVersion: 2},
	{MaxChannelIDs: 65535, MaxUserIDs: 64535, MaxTokenIDs: 65535, NumPriorities: 1, MinThroughput: 0, MaxHeight: 1, MaxMCSPDUSize: 65535, ProtocolVersion: 2},
}

// ConnectMCS sends the MCS Connect-Initial with the client's data blocks
// and reads the Connect-Response with the server's.
func (c *Client) ConnectMCS(ctx context.Context) error {
	stop, err := c.watch(ctx)
	if err != nil {
		return err
	}
	defer stop()
	cd := c.ClientData()
	if c.opts.EditClientData != nil {
		c.opts.EditClientData(&cd)
	}
	var bw, gw wire.Writer
	if err := gcc.WriteClientData(&bw, &cd); err != nil {
		return err
	}
	if err := gcc.WriteConferenceCreateRequest(&gw, bw.Bytes()); err != nil {
		return err
	}
	if err := c.sendData(func(w *wire.Writer) error {
		return mcs.WriteConnectInitial(w, &mcs.ConnectInitial{
			CallingDomainSelector: []byte{1},
			CalledDomainSelector:  []byte{1},
			UpwardFlag:            true,
			Target:                clientParameters[0],
			Minimum:               clientParameters[1],
			Maximum:               clientParameters[2],
			UserData:              gw.Bytes(),
		})
	}); err != nil {
		return err
	}
	r, err := c.readData()
	if err != nil {
		return err
	}
	cr, err := mcs.ParseConnectResponse(r.Rest())
	if err != nil {
		return err
	}
	if cr.Result != mcs.ResultSuccessful {
		return errors.New(TagConnectRefused, "Connect-Response without success", "result", cr.Result)
	}
	ccr, err := gcc.ParseConferenceCreateResponse(cr.UserData)
	if err != nil {
		return err
	}
	if ccr.Result != gcc.ResultSuccess {
		return errors.New(TagConnectRefused, "Conference Create Response without success", "result", ccr.Result)
	}
	c.ServerBlocks = bytes.Clone(ccr.UserData)
	if c.Server, err = gcc.ParseServerData(c.ServerBlocks); err != nil {
		return err
	}
	if !c.Server.HasNetwork || c.Server.Network.ChannelCount != len(c.opts.Channels) {
		return errors.New(TagChannelsMismatch, "server network data does not answer every channel",
			"network", c.Server.HasNetwork, "channels", c.Server.Network.ChannelCount, "asked", len(c.opts.Channels))
	}
	c.Parameters = cr.Parameters
	return nil
}

// Attach sends the Erect Domain Request and the Attach User Request and
// reads the user ID from the Attach User Confirm.
func (c *Client) Attach(ctx context.Context) error {
	stop, err := c.watch(ctx)
	if err != nil {
		return err
	}
	defer stop()
	if err := c.sendData(func(w *wire.Writer) error {
		mcs.WriteErectDomainRequest(w, &mcs.ErectDomainRequest{})
		return nil
	}); err != nil {
		return err
	}
	if err := c.sendData(func(w *wire.Writer) error { mcs.WriteAttachUserRequest(w); return nil }); err != nil {
		return err
	}
	r, err := c.readData()
	if err != nil {
		return err
	}
	a, err := mcs.ReadAttachUserConfirm(&r)
	if err != nil {
		return err
	}
	if a.Result != mcs.ResultSuccessful || !a.HasInitiator {
		return errors.New(TagAttachRefused, "Attach User Confirm without success", "result", a.Result)
	}
	c.UserID = a.Initiator
	return nil
}

// Join sends one Channel Join Request as initiator and returns the
// Confirm, whatever its result.
func (c *Client) Join(ctx context.Context, initiator, channel uint16) (mcs.ChannelJoinConfirm, error) {
	stop, err := c.watch(ctx)
	if err != nil {
		return mcs.ChannelJoinConfirm{}, err
	}
	defer stop()
	return c.join(initiator, channel)
}

func (c *Client) join(initiator, channel uint16) (mcs.ChannelJoinConfirm, error) {
	if err := c.sendData(func(w *wire.Writer) error {
		return mcs.WriteChannelJoinRequest(w, &mcs.ChannelJoinRequest{Initiator: initiator, ChannelID: channel})
	}); err != nil {
		return mcs.ChannelJoinConfirm{}, err
	}
	r, err := c.readData()
	if err != nil {
		return mcs.ChannelJoinConfirm{}, err
	}
	return mcs.ReadChannelJoinConfirm(&r)
}

// JoinAll joins the user channel, the I/O channel and every static channel,
// in that order, as FreeRDP does.
func (c *Client) JoinAll(ctx context.Context) error {
	stop, err := c.watch(ctx)
	if err != nil {
		return err
	}
	defer stop()
	ids := append([]uint16{c.UserID, c.Server.Network.MCSChannelID}, c.Server.Network.ChannelIDs[:c.Server.Network.ChannelCount]...)
	for _, id := range ids {
		cf, err := c.join(c.UserID, id)
		if err != nil {
			return err
		}
		if cf.Result != mcs.ResultSuccessful || !cf.HasChannelID || cf.ChannelID != id {
			return errors.New(TagJoinRefused, "Channel Join Confirm without success", "channel", id, "result", cf.Result)
		}
		c.Joined = append(c.Joined, id)
	}
	return nil
}

// ClientInfo is the Client Info the client sends before
// Options.EditClientInfo changes it: Unicode strings, automatic logon with
// the Options' credentials, and the extended part with a time zone, as
// FreeRDP sends.
func (c *Client) ClientInfo() sec.ClientInfo {
	return sec.ClientInfo{
		CodePage:      0,
		Flags:         sec.InfoMouse | sec.InfoUnicode | sec.InfoAutologon | sec.InfoLogonErrors | sec.InfoMouseHasWheel | sec.InfoDisableCtrlAltDel,
		Domain:        c.opts.Domain,
		UserName:      c.opts.User,
		Password:      sec.Secret(c.opts.Password),
		Extended:      true,
		AddressFamily: sec.AFInet,
		Address:       "127.0.0.1",
		Dir:           "C:\\Windows\\System32\\mstscax.dll",
		Optional:      sec.OptPerformanceFlags,
		TimeZone: sec.TimeZone{
			StandardName: "UTC", DaylightName: "UTC",
		},
		PerformanceFlags: 0x80, // TS_PERF_ENABLE_FONT_SMOOTHING
	}
}

// Login sends Client Info on the I/O channel and reads the answer: the
// license "valid client", or Set Error Info and a Disconnect Provider
// Ultimatum, which it returns as TagLoginRefused.
func (c *Client) Login(ctx context.Context) error {
	stop, err := c.watch(ctx)
	if err != nil {
		return err
	}
	defer stop()
	ci := c.ClientInfo()
	if c.opts.EditClientInfo != nil {
		c.opts.EditClientInfo(&ci)
	}
	var iw wire.Writer
	if err := sec.WriteClientInfo(&iw, &ci); err != nil {
		return err
	}
	if err := c.sendData(func(w *wire.Writer) error {
		return mcs.WriteSendDataRequest(w, &mcs.SendData{
			Initiator: c.UserID, ChannelID: c.Server.Network.MCSChannelID,
			Priority: mcs.PriorityHigh, Segmentation: mcs.SegmentationWhole, Data: iw.Bytes(),
		})
	}); err != nil {
		return err
	}
	var errorInfo uint32
	for {
		r, err := c.readData()
		if err != nil {
			return err
		}
		if b := r.Rest(); len(b) > 0 && mcs.Choice(b[0]) == mcs.ChoiceDisconnectProviderUltimatum {
			reason, err := mcs.ReadDisconnectProviderUltimatum(&r)
			if err != nil {
				return err
			}
			return errors.New(TagLoginRefused, "the server disconnected after Client Info", "reason", reason, "errorInfo", errorInfo)
		}
		d, err := mcs.ReadSendDataIndication(&r)
		if err != nil {
			return err
		}
		if len(d.Data) >= 2 && binary.LittleEndian.Uint16(d.Data)&sec.FlagLicensePkt != 0 {
			l, err := sec.ParseLicenseError(d.Data)
			if err != nil {
				return err
			}
			if l.ErrorCode != sec.LicenseStatusValidClient || l.StateTransition != sec.LicenseNoTransition {
				return errors.New(TagLicenseInvalid, "a license error other than valid client", "code", l.ErrorCode, "transition", l.StateTransition)
			}
			return nil
		}
		if _, errorInfo, err = sec.ParseSetErrorInfo(d.Data); err != nil {
			return err
		}
	}
}
