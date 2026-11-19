package rdp

import (
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/gcc"
	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/sec"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors that end a connection at login.
const (
	// TagLoginFailed: a client that sent no user name or password, a smart
	// card PIN, or credentials the Authenticator refused. It got Set Error
	// Info and a Disconnect Provider Ultimatum (SPEC §5.4, §8.2). The
	// metadata names the user and the reason, never the password.
	TagLoginFailed = "ConnLoginFailed"
	// TagChannelInvalid: Send Data from another user than the one attached,
	// or on another channel than the one the PDU belongs on.
	TagChannelInvalid = "ConnChannelInvalid"
	// TagClearDeadlineFailed: the login deadline could not be taken off the
	// connection after the login; the cause is kept.
	TagClearDeadlineFailed = "ConnClearDeadlineFailed"
)

// The server's own MCS identities, as in Microsoft's examples (MS-RDPBCGR
// 4.1.11 onwards): it sends as user 1002, and its share is 0x000103ea.
const (
	serverInitiator = 1002
	shareID         = 0x000103ea
)

// Why a login failed, in the log and the error's metadata.
const (
	reasonNoCredentials    = "no credentials"
	reasonSmartCard        = "smart card"
	reasonWrongCredentials = "wrong credentials"
)

// LoginRequest is what a client logs in with. Printing it or logging it
// leaves the password out.
type LoginRequest struct {
	User     string
	Domain   string
	Password string
	// Remote is the client's address, so that an Authenticator can make an
	// address wait after failed logins (SPEC §8.2).
	Remote net.Addr
}

// String describes r without its password.
func (r LoginRequest) String() string {
	return "LoginRequest{User:" + r.User + " Domain:" + r.Domain + " Remote:" + addrString(r.Remote) + "}"
}

// GoString is String, so that %#v leaves the password out too.
func (r LoginRequest) GoString() string { return r.String() }

// LogValue logs r without its password.
func (r LoginRequest) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("user", r.User),
		slog.String("domain", r.Domain),
		slog.String("remote", addrString(r.Remote)),
	)
}

func addrString(a net.Addr) string {
	if a == nil {
		return ""
	}
	return a.String()
}

// Authenticator answers whether a client may log in. It is called once for
// each client that sends a user name and a password, from the connection's
// goroutine, so several may run at once.
type Authenticator func(LoginRequest) bool

// StaticAuthenticator accepts one user name and password. It compares
// SHA-256 sums in constant time, so the time it takes says nothing about
// the password or its length.
func StaticAuthenticator(user, password string) Authenticator {
	u, p := sha256.Sum256([]byte(user)), sha256.Sum256([]byte(password))
	return func(r LoginRequest) bool {
		ru, rp := sha256.Sum256([]byte(r.User)), sha256.Sum256([]byte(r.Password))
		return subtle.ConstantTimeCompare(u[:], ru[:])&subtle.ConstantTimeCompare(p[:], rp[:]) == 1
	}
}

// login reads Client Info and logs the client in (SPEC §5.1 steps 5 and 6,
// §8.2): the license "valid client" when the Authenticator accepts it, the
// logon failure and the end of the connection otherwise. After the login
// the connection no longer counts among those not logged in, and has no
// deadline.
func (c *conn) login(auth Authenticator) error {
	r, err := c.readData()
	if err != nil {
		return err
	}
	d, err := mcs.ReadSendDataRequest(&r)
	if err != nil {
		return err
	}
	if d.Initiator != c.userID || d.ChannelID != ioChannelID {
		return errors.New(TagChannelInvalid, "Client Info from another user or on another channel than I/O",
			"initiator", d.Initiator, "channel", d.ChannelID, "user", c.userID)
	}
	ci, err := sec.ParseClientInfo(d.Data)
	if err != nil {
		return err
	}
	c.log.Debug("client info", "info", ci)
	var reason string
	switch {
	case ci.UserName == "" || ci.Password == "":
		reason = reasonNoCredentials
	case ci.Flags&sec.InfoPasswordIsSCPin != 0:
		reason = reasonSmartCard
	case !auth(LoginRequest{User: ci.UserName, Domain: ci.Domain, Password: string(ci.Password), Remote: c.nc.RemoteAddr()}):
		reason = reasonWrongCredentials
	}
	if reason != "" {
		if err := c.disconnect(sec.ErrInfoFreshCredentialsRequired); err != nil {
			return err
		}
		return errors.New(TagLoginFailed, "the client did not log in", "user", ci.UserName, "domain", ci.Domain, "reason", reason)
	}
	if err := c.sendIO(func(w *wire.Writer) error { sec.WriteLicenseValidClient(w); return nil }); err != nil {
		return err
	}
	if err := c.nc.SetDeadline(time.Time{}); err != nil {
		return errors.NewErr(TagClearDeadlineFailed, err)
	}
	c.release()
	c.user, c.domain = ci.UserName, ci.Domain
	c.log.Info("logged in", "user", ci.UserName, "domain", ci.Domain)
	return nil
}

// sendIO writes one PDU, which body appends, to the client on the I/O
// channel.
func (c *conn) sendIO(body func(w *wire.Writer) error) error {
	return c.write(func(w *wire.Writer) error { return appendIO(w, &c.pdu, body) })
}

// appendIO appends one PDU, which body appends to tmp first, sent to the
// client on the I/O channel.
func appendIO(w, tmp *wire.Writer, body func(w *wire.Writer) error) error {
	tmp.Reset()
	if err := body(tmp); err != nil {
		return err
	}
	return appendData(w, func(w *wire.Writer) error {
		return mcs.WriteSendDataIndication(w, &mcs.SendData{
			Initiator:    serverInitiator,
			ChannelID:    ioChannelID,
			Priority:     mcs.PriorityHigh,
			Segmentation: mcs.SegmentationWhole,
			Data:         tmp.Bytes(),
		})
	})
}

// disconnect tells the client why it is disconnected, with Set Error Info
// when the client said it understands it, and ends the MCS domain with a
// Disconnect Provider Ultimatum (SPEC §5.4), in one write. The caller closes
// the connection.
func (c *conn) disconnect(code uint32) error {
	return c.write(func(w *wire.Writer) error { return c.appendDisconnect(w, &c.pdu, code) })
}

// appendDisconnect appends what disconnect writes, using tmp for the Set
// Error Info PDU.
func (c *conn) appendDisconnect(w, tmp *wire.Writer, code uint32) error {
	if c.client.Core.Early(gcc.EarlySupportErrInfoPDU) {
		if err := appendIO(w, tmp, func(w *wire.Writer) error { sec.WriteSetErrorInfo(w, shareID, code); return nil }); err != nil {
			return err
		}
	}
	return appendData(w, func(w *wire.Writer) error {
		return mcs.WriteDisconnectProviderUltimatum(w, mcs.ReasonProviderInitiated)
	})
}
