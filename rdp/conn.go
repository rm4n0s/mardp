package rdp

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/gcc"
	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/tpkt"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/internal/x224"
)

// Tags of the errors that end a connection during its sequence.
const (
	// TagDeadlineFailed: the login deadline could not be set on the
	// connection; the cause is kept.
	TagDeadlineFailed = "ConnDeadlineFailed"
	// TagFrameUnexpected: a fast-path PDU, or a TPDU other than the one
	// expected, during the connection sequence.
	TagFrameUnexpected = "ConnFrameUnexpected"
	// TagTLSRequired: a client that does not offer TLS. It got the
	// negotiation failure SSL_REQUIRED_BY_SERVER (SPEC §5.1).
	TagTLSRequired = "ConnTLSRequired"
	// TagTLSHandshakeFailed: the TLS handshake failed; the cause is kept.
	TagTLSHandshakeFailed = "ConnTLSHandshakeFailed"
	// TagWriteFailed: writing to the connection failed; the cause is kept.
	TagWriteFailed = "ConnWriteFailed"
	// TagProtocolMismatch: client core data whose serverSelectedProtocol is
	// not the protocol the server selected, which a downgrade would cause.
	TagProtocolMismatch = "ConnProtocolMismatch"
	// TagInitiatorInvalid: a Channel Join Request from another user than
	// the one attached.
	TagInitiatorInvalid = "ConnInitiatorInvalid"
)

// The MCS channels the server hands out (as in MS-RDPBCGR 4.1.4 and 4.1.7):
// the I/O channel, then one for each static channel the client asked for,
// then the user channel.
const ioChannelID = 1003

// serverDomainParameters are the domain parameters the server answers with,
// whatever the client proposes: those of Microsoft's Connect-Response
// (MS-RDPBCGR 4.1.4), which every client accepts. RDP uses none of them
// beyond the channel count and the PDU size, which are within them.
var serverDomainParameters = mcs.DomainParameters{
	MaxChannelIDs:   34,
	MaxUserIDs:      3,
	MaxTokenIDs:     0,
	NumPriorities:   1,
	MinThroughput:   0,
	MaxHeight:       1,
	MaxMCSPDUSize:   65528,
	ProtocolVersion: 2,
}

// conn is one connection going through the connection sequence.
type conn struct {
	log     *slog.Logger
	nc      net.Conn      // the TCP connection
	rw      io.ReadWriter // nc, then the TLS connection over it
	release func()        // gives back the place among connections not logged in
	buf     []byte        // the frame being read
	w       wire.Writer   // the frame being written

	requested uint32 // the protocols the client offered
	// blocks holds a copy of the client's data blocks, which client points
	// into.
	blocks []byte
	client gcc.ClientData
	userID uint16
	// channels are the channels the client joins: the user channel, the
	// I/O channel, then the static channels in the client's order.
	channels []uint16
	joined   []bool
}

// serveConn runs the connection sequence on nc. It returns when the
// connection ends, with the reason.
func (s *Server) serveConn(ctx context.Context, nc net.Conn, log *slog.Logger, release func()) error {
	c := &conn{
		log:     log,
		nc:      nc,
		rw:      nc,
		release: release,
		buf:     make([]byte, tpkt.MaxLength),
		w:       wire.NewWriter(make([]byte, 0, 1024)),
	}
	if err := nc.SetDeadline(time.Now().Add(s.loginTimeout)); err != nil {
		return errors.NewErr(TagDeadlineFailed, err)
	}
	stop := context.AfterFunc(ctx, func() {
		if err := nc.Close(); err != nil {
			log.Debug("closing the connection", "err", err)
		}
	})
	defer stop()
	if err := c.negotiate(); err != nil {
		return err
	}
	if err := c.startTLS(ctx, s.tls); err != nil {
		return err
	}
	if err := c.connectMCS(); err != nil {
		return err
	}
	if err := c.attachUser(); err != nil {
		return err
	}
	if err := c.joinChannels(); err != nil {
		return err
	}
	return c.awaitLogin()
}

// send writes one TPKT whose contents body appends.
func (c *conn) send(body func(w *wire.Writer) error) error {
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

// sendData writes one X.224 Data TPDU whose MCS PDU body appends.
func (c *conn) sendData(body func(w *wire.Writer) error) error {
	return c.send(func(w *wire.Writer) error {
		x224.WriteDataHeader(w)
		return body(w)
	})
}

// readTPKT reads the next frame, which has to be a TPKT, and returns its
// TPDU.
func (c *conn) readTPKT() ([]byte, error) {
	k, f, err := tpkt.ReadFrame(c.rw, c.buf)
	if err != nil {
		return nil, err
	}
	if k != tpkt.TPKT {
		return nil, errors.New(TagFrameUnexpected, "a fast-path PDU during the connection sequence", "length", len(f))
	}
	return f[tpkt.HeaderLen:], nil
}

// readData reads the next frame, which has to be an X.224 Data TPDU, and
// returns a reader at the MCS PDU it carries.
func (c *conn) readData() (wire.Reader, error) {
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

// negotiate answers the X.224 Connection Request: TLS when the client offers
// it, the failure SSL_REQUIRED_BY_SERVER otherwise (SPEC §5.1 step 1).
func (c *conn) negotiate() error {
	tpdu, err := c.readTPKT()
	if err != nil {
		return err
	}
	cr, err := x224.ParseConnectionRequest(tpdu)
	if err != nil {
		return err
	}
	c.log.Debug("connection request", "cookie", string(cr.Cookie), "negotiation", cr.HasNegReq, "protocols", cr.RequestedProtocols)
	if !cr.HasNegReq || cr.RequestedProtocols&x224.ProtocolSSL == 0 {
		cc := x224.ConnectionConfirm{Negotiation: x224.NegFailure, FailureCode: x224.FailureSSLRequiredByServer}
		if err := c.send(func(w *wire.Writer) error { x224.WriteConnectionConfirm(w, &cc); return nil }); err != nil {
			return err
		}
		return errors.New(TagTLSRequired, "the client does not offer TLS", "negotiation", cr.HasNegReq, "protocols", cr.RequestedProtocols)
	}
	c.requested = cr.RequestedProtocols
	cc := x224.ConnectionConfirm{
		Negotiation:      x224.NegResponse,
		Flags:            x224.RspExtendedClientDataSupported,
		SelectedProtocol: x224.ProtocolSSL,
	}
	return c.send(func(w *wire.Writer) error { x224.WriteConnectionConfirm(w, &cc); return nil })
}

// startTLS runs the TLS handshake as the server (SPEC §5.1 step 2, §8.1).
func (c *conn) startTLS(ctx context.Context, cfg *tls.Config) error {
	tc := tls.Server(c.nc, cfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		return errors.NewErr(TagTLSHandshakeFailed, err)
	}
	st := tc.ConnectionState()
	c.log.Debug("TLS", "version", tls.VersionName(st.Version), "cipher", tls.CipherSuiteName(st.CipherSuite))
	c.rw = tc
	return nil
}

// connectMCS reads the MCS Connect-Initial with the client's data blocks and
// answers with the Connect-Response and the server's (SPEC §5.1 step 3).
func (c *conn) connectMCS() error {
	r, err := c.readData()
	if err != nil {
		return err
	}
	ci, err := mcs.ParseConnectInitial(r.Rest())
	if err != nil {
		return err
	}
	blocks, err := gcc.ParseConferenceCreateRequest(ci.UserData)
	if err != nil {
		return err
	}
	// The frame buffer is read into again; what the session keeps of the
	// client's data points into this copy.
	c.blocks = bytes.Clone(blocks)
	if c.client, err = gcc.ParseClientData(c.blocks); err != nil {
		return err
	}
	core := &c.client.Core
	if core.Has(gcc.OptServerSelectedProtocol) && core.ServerSelectedProtocol != x224.ProtocolSSL {
		return errors.New(TagProtocolMismatch, "the client's core data names another protocol than the one selected", "client", core.ServerSelectedProtocol, "selected", x224.ProtocolSSL)
	}
	n := c.client.Network.ChannelCount // 0 without a network block
	sd := gcc.ServerData{
		Core: gcc.ServerCore{
			Version:                  gcc.VersionRDP10,
			OptionalFields:           2,
			ClientRequestedProtocols: c.requested,
		},
		HasNetwork:  true,
		Network:     gcc.ServerNetwork{MCSChannelID: ioChannelID, ChannelCount: n},
		HasSecurity: true, // no RDP encryption: TLS does it
	}
	c.userID = ioChannelID + uint16(n) + 1
	c.channels = make([]uint16, 0, 2+n)
	c.channels = append(c.channels, c.userID, ioChannelID)
	for i := range n {
		id := ioChannelID + 1 + uint16(i)
		sd.Network.ChannelIDs[i] = id
		c.channels = append(c.channels, id)
	}
	c.joined = make([]bool, len(c.channels))
	c.log.Debug("client data", "name", core.Name(), "width", core.DesktopWidth, "height", core.DesktopHeight,
		"build", core.ClientBuild, "layout", core.KeyboardLayout, "channels", c.channelNames())

	var sw wire.Writer
	if err := gcc.WriteServerData(&sw, &sd); err != nil {
		return err
	}
	var gw wire.Writer
	if err := gcc.WriteConferenceCreateResponse(&gw, &gcc.ConferenceCreateResponse{
		NodeID: mcs.UserIDBase, Tag: 1, Result: gcc.ResultSuccess, UserData: sw.Bytes(),
	}); err != nil {
		return err
	}
	return c.sendData(func(w *wire.Writer) error {
		return mcs.WriteConnectResponse(w, &mcs.ConnectResponse{
			Result:     mcs.ResultSuccessful,
			Parameters: serverDomainParameters,
			UserData:   gw.Bytes(),
		})
	})
}

// channelNames lists the static channels the client asked for, for the log.
func (c *conn) channelNames() []string {
	n := c.client.Network.ChannelCount
	names := make([]string, n)
	for i := range n {
		names[i] = c.client.Network.Channels[i].NameString()
	}
	return names
}

// attachUser reads the Erect Domain Request and the Attach User Request and
// gives the client its user ID (SPEC §5.1 step 4).
func (c *conn) attachUser() error {
	r, err := c.readData()
	if err != nil {
		return err
	}
	if _, err := mcs.ReadErectDomainRequest(&r); err != nil {
		return err
	}
	if r, err = c.readData(); err != nil {
		return err
	}
	if err := mcs.ReadAttachUserRequest(&r); err != nil {
		return err
	}
	return c.sendData(func(w *wire.Writer) error {
		return mcs.WriteAttachUserConfirm(w, &mcs.AttachUserConfirm{
			Result: mcs.ResultSuccessful, HasInitiator: true, Initiator: c.userID,
		})
	})
}

// joinChannels answers Channel Join Requests until the client has joined
// the user channel, the I/O channel and every static channel. A channel the
// server did not hand out is refused with rt-no-such-channel.
func (c *conn) joinChannels() error {
	left := len(c.channels)
	for left > 0 {
		r, err := c.readData()
		if err != nil {
			return err
		}
		req, err := mcs.ReadChannelJoinRequest(&r)
		if err != nil {
			return err
		}
		if req.Initiator != c.userID {
			return errors.New(TagInitiatorInvalid, "a Channel Join Request from another user", "initiator", req.Initiator, "user", c.userID)
		}
		cf := mcs.ChannelJoinConfirm{Initiator: c.userID, Requested: req.ChannelID}
		if i := c.channelIndex(req.ChannelID); i >= 0 {
			cf.Result, cf.HasChannelID, cf.ChannelID = mcs.ResultSuccessful, true, req.ChannelID
			if !c.joined[i] {
				c.joined[i] = true
				left--
			}
		} else {
			cf.Result = mcs.ResultNoSuchChannel
			c.log.Debug("join of an unknown channel refused", "channel", req.ChannelID)
		}
		if err := c.sendData(func(w *wire.Writer) error { return mcs.WriteChannelJoinConfirm(w, &cf) }); err != nil {
			return err
		}
	}
	c.log.Debug("channels joined", "user", c.userID, "channels", len(c.channels))
	return nil
}

func (c *conn) channelIndex(id uint16) int {
	for i, ch := range c.channels {
		if ch == id {
			return i
		}
	}
	return -1
}

// awaitLogin waits for the client's next PDUs. Login (SPEC §5.1 step 5)
// comes in step 1.6; until then the connection is held until the login
// deadline closes it, and what the client sends is read and dropped.
func (c *conn) awaitLogin() error {
	for {
		if _, _, err := tpkt.ReadFrame(c.rw, c.buf); err != nil {
			return err
		}
	}
}
