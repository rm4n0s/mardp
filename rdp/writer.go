package rdp

import (
	"io"
	"sync"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/fastpath"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// Tags of the errors of a session's output.
const (
	// TagQueueFull: the session's queue for this kind of output is full,
	// because the network is slower than what is sent. The caller skips
	// (SPEC §6, §9.5).
	TagQueueFull = "SessionQueueFull"
	// TagSessionOver: output for a session whose connection is over.
	TagSessionOver = "SessionOver"
	// TagFastPathUnsupported: a fast-path update for a client that did not
	// confirm fast-path output.
	TagFastPathUnsupported = "SessionFastPathUnsupported"
)

// queue is one of a session's two output queues (SPEC §4.2).
type queue int

const (
	// queueControl carries control PDUs, the replies to input (the
	// pointer) and sound. The writer takes from it first.
	queueControl queue = iota
	// queueVideo carries video.
	queueVideo
	queueCount
)

func (q queue) String() string {
	if q == queueControl {
		return "control"
	}
	return "video"
}

// How many packets each queue holds, and the room each packet starts with.
// A packet that needs more grows once and keeps what it grew to.
const (
	controlDepth  = 32
	videoDepth    = 4
	controlBuffer = 4 << 10
	videoBuffer   = 64 << 10
	// pduBuffer holds a dynamic channel's PDU (dynvc.MaxPDU).
	pduBuffer = 2 << 10
)

// packet is what one write sends: whole frames, built by the sender.
type packet struct {
	w wire.Writer
	// tmp is room for a PDU that is built before what wraps it, and pdu
	// for one built before tmp wraps it (a dynamic channel's in a static
	// channel's).
	tmp wire.Writer
	pdu wire.Writer
	// done, when set, gets the result of the write.
	done chan error
}

// writer is a session's writer goroutine (SPEC §4.2): it alone writes to
// the connection, one write per packet, control before video. Packets come
// from a free list per queue, made at the start of the session, so sending
// allocates nothing (SPEC §11), and a full queue fails the send at once
// instead of blocking it.
type writer struct {
	out io.Writer
	// fail is called with the error of the write that failed; the writer
	// stops after it.
	fail   func(err error)
	queued [queueCount]chan *packet
	free   [queueCount]chan *packet
	stop   chan struct{}
	halted sync.Once
	exited chan struct{}

	mu  sync.Mutex
	err error // why the writer stopped, nil when it was halted
}

func newWriter(out io.Writer, fail func(err error)) *writer {
	wr := &writer{out: out, fail: fail, stop: make(chan struct{}), exited: make(chan struct{})}
	for q, n := range [queueCount]struct{ depth, size int }{{controlDepth, controlBuffer}, {videoDepth, videoBuffer}} {
		wr.queued[q] = make(chan *packet, n.depth)
		wr.free[q] = make(chan *packet, n.depth)
		for range n.depth {
			wr.free[q] <- &packet{
				w:   wire.NewWriter(make([]byte, 0, n.size)),
				tmp: wire.NewWriter(make([]byte, 0, n.size)),
				pdu: wire.NewWriter(make([]byte, 0, pduBuffer)),
			}
		}
	}
	return wr
}

// send queues on q the frames that build appends to a free packet. It
// never waits for the network.
func (wr *writer) send(q queue, build func(p *packet) error) error {
	p, err := wr.get(q)
	if err != nil {
		return err
	}
	if err := build(p); err != nil {
		wr.free[q] <- p
		return err
	}
	wr.queued[q] <- p
	return nil
}

// pending is the number of q's packets not back on its free list: queued,
// being written, or being built.
func (wr *writer) pending(q queue) int { return cap(wr.free[q]) - len(wr.free[q]) }

// get takes an empty packet from q's free list.
func (wr *writer) get(q queue) (*packet, error) {
	select {
	case <-wr.exited:
		return nil, errors.New(TagSessionOver, "the session's output has stopped", "queue", q.String())
	default:
	}
	select {
	case p := <-wr.free[q]:
		p.w.Reset()
		p.tmp.Reset()
		p.pdu.Reset()
		p.done = nil
		return p, nil
	default:
		return nil, errors.New(TagQueueFull, "the session's queue is full", "queue", q.String())
	}
}

// run writes packets until halt, or until a write fails.
func (wr *writer) run() {
	defer close(wr.exited)
	for {
		q, p, ok := wr.next()
		if !ok {
			return
		}
		var err error
		if _, werr := wr.out.Write(p.w.Bytes()); werr != nil {
			err = errors.NewErr(TagWriteFailed, werr).SetMetadata("length", p.w.Len(), "queue", q.String())
		}
		if p.done != nil {
			p.done <- err
			p.done = nil
		}
		wr.free[q] <- p
		if err != nil {
			wr.mu.Lock()
			wr.err = err
			wr.mu.Unlock()
			wr.fail(err)
			return
		}
	}
}

// next takes the next packet: from the control queue while it has one.
func (wr *writer) next() (queue, *packet, bool) {
	select {
	case p := <-wr.queued[queueControl]:
		return queueControl, p, true
	default:
	}
	select {
	case p := <-wr.queued[queueControl]:
		return queueControl, p, true
	case p := <-wr.queued[queueVideo]:
		return queueVideo, p, true
	case <-wr.stop:
		return 0, nil, false
	}
}

// failure returns the error of the write that failed, nil when none did.
func (wr *writer) failure() error {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	return wr.err
}

// halt stops the writer, dropping what is queued, and waits for it. A
// write in progress has to be unblocked first, by a deadline or by closing
// the connection.
func (wr *writer) halt() {
	wr.halted.Do(func() { close(wr.stop) })
	<-wr.exited
}

// sendShare queues on q one PDU, which body appends, on the I/O channel.
func (s *Session) sendShare(q queue, body func(w *wire.Writer) error) error {
	return s.wr.send(q, func(p *packet) error { return appendIO(&p.w, &p.tmp, body) })
}

// sendUpdate queues on q one fast-path update of type code, fragmented when
// it is larger than a PDU carries (MS-RDPBCGR 2.2.9.1.2.1).
func (s *Session) sendUpdate(q queue, code uint8, data []byte) error {
	if !s.client.FastPathOutput {
		return errors.New(TagFastPathUnsupported, "the client did not confirm fast-path output", "code", code)
	}
	return s.wr.send(q, func(p *packet) error { return fastpath.AppendUpdate(&p.w, code, data, s.client.MaxRequestSize) })
}
