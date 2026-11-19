package rdp

import (
	"sync"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/dynvc"
	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/svc"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors of dynamic virtual channels (SPEC §5.3).
const (
	// TagDVCUnavailable: a dynamic channel was asked for, but the client
	// joined no drdynvc, or did not answer the capabilities in time.
	TagDVCUnavailable = "SessionDVCUnavailable"
	// TagDVCNotOpen: data or a close for a dynamic channel that the client
	// has not accepted yet, or that is closed.
	TagDVCNotOpen = "SessionDVCNotOpen"
	// TagDVCCapsUnexpected: a second capabilities response from the
	// client.
	TagDVCCapsUnexpected = "SessionDVCCapsUnexpected"
	// TagDVCVersionInvalid: a capabilities response with a newer version
	// than the server offered.
	TagDVCVersionInvalid = "SessionDVCVersionInvalid"
	// TagDVCUnknown: a Create Response or data for a channel the server
	// did not create, or has forgotten.
	TagDVCUnknown = "SessionDVCUnknown"
	// TagDVCStateInvalid: a PDU that the channel's state does not allow:
	// a Create Response for a channel that is open, data or a close for
	// one the client has not answered.
	TagDVCStateInvalid = "SessionDVCStateInvalid"
)

// dvcVersion is the version the server offers (MS-RDPEDYC 1.7): 2, with
// priority classes. Version 3 would let both sides compress data, which
// the server does not do (SPEC §5).
const dvcVersion = dynvc.Version2

// dvcCharges are the priority charges of the server's capabilities: the
// shares of 70, 20, 7 and 3 percent of Microsoft's example (2.2.1.1.2).
// Clients do not act on them.
var dvcCharges = [4]uint16{936, 3276, 9362, 21845}

// dvcCapsWait is how long the client has to answer the capabilities; after
// it no channel is created (MS-RDPEDYC 3.3.3.1).
const dvcCapsWait = 10 * time.Second

// dvcMaxMessage is the longest message the server joins on a dynamic
// channel. What clients send on them is short (capabilities, frame
// acknowledgements).
const dvcMaxMessage = 1 << 20

// dvcTimedOut is the status a channel is refused with when the client did
// not answer the capabilities in time: HRESULT_FROM_WIN32(ERROR_TIMEOUT).
const dvcTimedOut int32 = -0x7ff8fa4c

// dvcListener gets what happens to a dynamic channel, on the session's
// reading goroutine (dvcRefused also on a timer's).
type dvcListener interface {
	// dvcOpened: the client accepted the channel.
	dvcOpened(d *dvc)
	// dvcRefused: the client refused the channel with the HRESULT status,
	// or did not answer the capabilities in time (dvcTimedOut).
	dvcRefused(d *dvc, status int32)
	// dvcReceived gets a whole message; msg is valid until it returns. An
	// error ends the session.
	dvcReceived(d *dvc, msg []byte) error
	// dvcClosed: the client closed the channel.
	dvcClosed(d *dvc)
}

// dvcState is where a dynamic channel is in its life.
type dvcState int

const (
	// dvcPending: waiting for the capabilities response to be created.
	dvcPending dvcState = iota
	// dvcOpening: the Create Request is sent.
	dvcOpening
	dvcOpen
	// dvcClosing: the server sent Close and waits for the client's.
	dvcClosing
	dvcGone
)

// dvc is a dynamic channel the server created.
type dvc struct {
	m    *dvcManager
	id   uint32
	name string
	l    dvcListener
	// state is guarded by m.mu.
	state dvcState
	// join is used by the reading goroutine alone.
	join dynvc.Joiner
}

// dvcManager is the server's DVC manager on drdynvc (MS-RDPEDYC 3.3).
type dvcManager struct {
	s  *Session
	ch *staticChannel

	mu sync.Mutex
	// ready is set by the capabilities response, failed when it did not
	// come in time or the session ended.
	ready, failed bool
	version       uint16
	next          uint32
	channels      map[uint32]*dvc
	timer         *time.Timer
}

// startDVC sends the capabilities on drdynvc when the client joined it,
// and gives the client wait to answer them.
func (s *Session) startDVC(wait time.Duration) {
	ch := s.channel("drdynvc")
	if ch == nil {
		return
	}
	m := &dvcManager{s: s, ch: ch, next: 1, channels: make(map[uint32]*dvc)}
	s.dvc = m
	ch.setHandler(m.receive)
	m.mu.Lock()
	defer m.mu.Unlock()
	err := m.sendPDU(func(w *wire.Writer) error { return dynvc.AppendCaps(w, dvcVersion, dvcCharges) })
	if err != nil {
		s.c.log.Debug("sending the dynamic channels' capabilities", "err", err)
		m.failed = true
		return
	}
	m.timer = time.AfterFunc(wait, m.timeout)
}

// stopDVC stops the manager when the session ends.
func (s *Session) stopDVC() {
	m := s.dvc
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failed = true
	if m.timer != nil {
		m.timer.Stop()
	}
}

// timeout refuses the channels waiting for the capabilities response.
func (m *dvcManager) timeout() {
	m.mu.Lock()
	if m.ready || m.failed {
		m.mu.Unlock()
		return
	}
	m.failed = true
	var refused []*dvc
	for id, d := range m.channels {
		d.state = dvcGone
		delete(m.channels, id)
		refused = append(refused, d)
	}
	m.mu.Unlock()
	m.s.c.log.Warn("the client did not answer the dynamic channels' capabilities", "refused", len(refused))
	for _, d := range refused {
		d.l.dvcRefused(d, dvcTimedOut)
	}
}

// openDVC creates the dynamic channel name; l gets what happens to it. The
// Create Request goes once the client has answered the capabilities.
func (s *Session) openDVC(name string, l dvcListener) (*dvc, error) {
	m := s.dvc
	if m == nil {
		return nil, errors.New(TagDVCUnavailable, "the client joined no drdynvc", "name", name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failed {
		return nil, errors.New(TagDVCUnavailable, "the client did not answer the dynamic channels' capabilities", "name", name)
	}
	d := &dvc{m: m, id: m.next, name: name, l: l}
	d.join.Max = dvcMaxMessage
	if m.ready {
		if err := m.create(d); err != nil {
			return nil, err
		}
	}
	m.next++
	m.channels[d.id] = d
	return d, nil
}

// create sends d's Create Request. m.mu is held.
func (m *dvcManager) create(d *dvc) error {
	if err := m.sendPDU(func(w *wire.Writer) error { return dynvc.AppendCreateRequest(w, d.id, 0, d.name) }); err != nil {
		return err
	}
	d.state = dvcOpening
	return nil
}

// send queues on q the message msg on d.
func (d *dvc) send(q queue, msg []byte) error {
	m := d.m
	m.mu.Lock()
	state := d.state
	m.mu.Unlock()
	if state != dvcOpen {
		return errors.New(TagDVCNotOpen, "data for a dynamic channel that is not open", "name", d.name, "state", int(state))
	}
	return m.s.wr.send(q, func(p *packet) error {
		return dynvc.Split(&p.pdu, d.id, msg, func(b []byte) error { return appendDVCPDU(p, m.ch) })
	})
}

// close closes d: the server sends Close and drops what the client sends
// on d until its own Close.
func (d *dvc) close() error {
	m := d.m
	m.mu.Lock()
	defer m.mu.Unlock()
	if d.state != dvcOpen {
		return errors.New(TagDVCNotOpen, "a close for a dynamic channel that is not open", "name", d.name, "state", int(d.state))
	}
	if err := m.sendPDU(func(w *wire.Writer) error { dynvc.AppendClose(w, d.id); return nil }); err != nil {
		return err
	}
	d.state = dvcClosing
	return nil
}

// sendPDU queues on the control queue the one DVC PDU that build appends.
func (m *dvcManager) sendPDU(build func(w *wire.Writer) error) error {
	return m.s.wr.send(queueControl, func(p *packet) error {
		if err := build(&p.pdu); err != nil {
			return err
		}
		return appendDVCPDU(p, m.ch)
	})
}

// appendDVCPDU appends to p the DVC PDU in p.pdu as a static channel
// message of one chunk on ch (MS-RDPEDYC 2.1): a DVC PDU fits in one.
func appendDVCPDU(p *packet, ch *staticChannel) error {
	p.tmp.Reset()
	svc.WriteHeader(&p.tmp, svc.Header{Length: uint32(p.pdu.Len()), Flags: svc.FlagFirst | svc.FlagLast})
	p.tmp.Write(p.pdu.Bytes())
	return appendChannelData(p, ch)
}

// appendChannelData appends to p one Send Data Indication on ch with what
// p.tmp holds.
func appendChannelData(p *packet, ch *staticChannel) error {
	return appendData(&p.w, func(w *wire.Writer) error {
		return mcs.WriteSendDataIndication(w, &mcs.SendData{
			Initiator:    serverInitiator,
			ChannelID:    ch.id,
			Priority:     mcs.PriorityHigh,
			Segmentation: mcs.SegmentationWhole,
			Data:         p.tmp.Bytes(),
		})
	})
}

// receive handles a message the client sent on drdynvc: one DVC PDU.
func (m *dvcManager) receive(msg []byte) error {
	p, err := dynvc.ParseClient(msg)
	if err != nil {
		return err
	}
	switch p.Cmd {
	case dynvc.CmdCapabilities:
		return m.caps(p)
	case dynvc.CmdCreate:
		return m.created(p)
	case dynvc.CmdClose:
		return m.closed(p)
	}
	return m.data(p)
}

// caps handles the capabilities response: the channels asked for until
// now are created.
func (m *dvcManager) caps(p dynvc.PDU) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.ready:
		return errors.New(TagDVCCapsUnexpected, "a second capabilities response", "version", p.Version)
	case m.failed:
		m.s.c.log.Debug("a capabilities response after the time for it", "version", p.Version)
		return nil
	case p.Version > dvcVersion:
		return errors.New(TagDVCVersionInvalid, "a capabilities response of a newer version than offered",
			"version", p.Version, "offered", dvcVersion)
	}
	m.ready, m.version = true, p.Version
	m.timer.Stop()
	m.s.c.log.Debug("dynamic channels ready", "version", p.Version)
	for id := uint32(1); id < m.next; id++ {
		if d := m.channels[id]; d != nil && d.state == dvcPending {
			if err := m.create(d); err != nil {
				return err
			}
		}
	}
	return nil
}

// lookup returns the channel of p. m.mu is held.
func (m *dvcManager) lookup(p dynvc.PDU) (*dvc, error) {
	d := m.channels[p.ChannelID]
	if d == nil {
		return nil, errors.New(TagDVCUnknown, "a PDU for a dynamic channel the server did not create",
			"cmd", p.Cmd, "channel", p.ChannelID)
	}
	return d, nil
}

// created handles a Create Response.
func (m *dvcManager) created(p dynvc.PDU) error {
	m.mu.Lock()
	d, err := m.lookup(p)
	if err == nil && d.state != dvcOpening {
		err = errors.New(TagDVCStateInvalid, "a Create Response for a channel not being created",
			"channel", p.ChannelID, "name", d.name, "state", int(d.state))
	}
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if p.Status < 0 {
		d.state = dvcGone
		delete(m.channels, d.id)
		m.mu.Unlock()
		m.s.c.log.Info("dynamic channel refused", "name", d.name, "status", uint32(p.Status))
		d.l.dvcRefused(d, p.Status)
		return nil
	}
	d.state = dvcOpen
	m.mu.Unlock()
	m.s.c.log.Debug("dynamic channel open", "name", d.name, "channel", d.id)
	d.l.dvcOpened(d)
	return nil
}

// data handles a Data First or Data.
func (m *dvcManager) data(p dynvc.PDU) error {
	m.mu.Lock()
	d, err := m.lookup(p)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	state := d.state
	m.mu.Unlock()
	switch state {
	case dvcOpen:
	case dvcClosing:
		m.s.c.log.Debug("data for a closing dynamic channel dropped", "name", d.name, "length", len(p.Data))
		return nil
	default:
		return errors.New(TagDVCStateInvalid, "data for a channel the client has not accepted",
			"channel", p.ChannelID, "name", d.name)
	}
	msg, ok, err := d.join.Add(p)
	if err != nil || !ok {
		return err
	}
	return d.l.dvcReceived(d, msg)
}

// closed handles the client's Close: of a channel it closes, or its answer
// to the server's.
func (m *dvcManager) closed(p dynvc.PDU) error {
	m.mu.Lock()
	d := m.channels[p.ChannelID]
	if d == nil {
		m.mu.Unlock()
		m.s.c.log.Debug("a close for an unknown dynamic channel ignored", "channel", p.ChannelID)
		return nil
	}
	state := d.state
	if state != dvcOpen && state != dvcClosing {
		m.mu.Unlock()
		return errors.New(TagDVCStateInvalid, "a close for a channel the client has not accepted",
			"channel", p.ChannelID, "name", d.name)
	}
	d.state = dvcGone
	delete(m.channels, d.id)
	m.mu.Unlock()
	if state == dvcOpen {
		m.s.c.log.Debug("dynamic channel closed by the client", "name", d.name)
		d.l.dvcClosed(d)
	}
	return nil
}
