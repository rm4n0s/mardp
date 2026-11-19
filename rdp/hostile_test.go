package rdp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/caps"
	"github.com/rm4n0s/mardp/rdp/internal/dynvc"
	"github.com/rm4n0s/mardp/rdp/internal/gcc"
	"github.com/rm4n0s/mardp/rdp/internal/input"
	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/sec"
	"github.com/rm4n0s/mardp/rdp/internal/tpkt"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/internal/x224"
)

// The sweep of SPEC §15.4: every parser of what a client sends gets every
// cut of real PDUs, mutations of them, length fields set to their largest,
// bytes added, and random bytes. A parser has to return nil or a tagged
// error, never panic, and never allocate more than a little beyond the size
// of its input: an allocation sized by a length field it did not check
// fails the test.

// sweepAllocLimit is the most bytes one parse may allocate for n bytes of
// input: room for the strings and slices it decodes from them.
func sweepAllocLimit(n int) uint64 { return 8<<10 + 32*uint64(n) }

// sweepParse runs parse on b and fails on what sweepCheck finds.
func sweepParse(t *testing.T, name string, parse func([]byte) error, b []byte) {
	t.Helper()
	if msg := sweepCheck(parse, b); msg != "" {
		t.Fatalf("%s: %s: % x", name, msg, b)
	}
}

// sweepCheck runs parse on b and says what is wrong: an untagged error, or
// an allocation beyond sweepAllocLimit; "" when nothing is.
func sweepCheck(parse func([]byte) error, b []byte) string {
	var untagged error
	measure := func() uint64 {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		err := parse(b)
		runtime.ReadMemStats(&after)
		if _, ok := errors.FromError(err); err != nil && !ok {
			untagged = err
		}
		return after.TotalAlloc - before.TotalAlloc
	}
	// The first error of a kind sets up caches once (the stack's frames);
	// what allocates again on a second try is the parser's.
	n := measure()
	if n > sweepAllocLimit(len(b)) {
		n = measure()
	}
	switch {
	case untagged != nil:
		return fmt.Sprintf("untagged %T %v", untagged, untagged)
	case n > sweepAllocLimit(len(b)):
		return fmt.Sprintf("%d bytes allocated for %d bytes of input", n, len(b))
	}
	return ""
}

func TestSweepCheck(t *testing.T) {
	sized := func(b []byte) error {
		if len(b) < 4 {
			return errors.New("Short", "short")
		}
		// What the sweep is there to catch: a length field trusted.
		buf := make([]byte, binary.LittleEndian.Uint32(b))
		if len(buf) > len(b) {
			return errors.New("TooLong", "too long")
		}
		return nil
	}
	if msg := sweepCheck(sized, []byte{0xff, 0xff, 0xff, 0}); !strings.Contains(msg, "allocated") {
		t.Errorf("an allocation by a length field: %q", msg)
	}
	if msg := sweepCheck(sized, []byte{2, 0, 0, 0}); msg != "" {
		t.Errorf("a small one: %q", msg)
	}
	if msg := sweepCheck(func([]byte) error { return io.EOF }, nil); !strings.Contains(msg, "untagged") {
		t.Errorf("an untagged error: %q", msg)
	}
}

// sweep feeds parse the hostile variants of each seed.
func sweep(t *testing.T, rng *rand.Rand, name string, parse func([]byte) error, seeds ...[]byte) {
	t.Helper()
	for _, seed := range seeds {
		if err := parse(seed); err != nil {
			t.Fatalf("%s: the seed itself fails: %v", name, err)
		}
		for n := range len(seed) {
			sweepParse(t, name, parse, seed[:n])
		}
		// Every 2 and 4 bytes as the largest length a field could hold.
		for i := range len(seed) {
			for _, size := range []int{2, 4} {
				if i+size > len(seed) {
					continue
				}
				m := bytes.Clone(seed)
				for j := range size {
					m[i+j] = 0xff
				}
				sweepParse(t, name, parse, m)
				m[i] = 0x7f
				sweepParse(t, name, parse, m)
			}
		}
		for range 1000 {
			m := bytes.Clone(seed)
			for range 1 + rng.IntN(8) {
				m[rng.IntN(len(m))] = byte(rng.Uint32())
			}
			sweepParse(t, name, parse, m[:rng.IntN(len(m)+1)])
		}
		for range 100 {
			tail := make([]byte, rng.IntN(2048))
			for i := range tail {
				tail[i] = byte(rng.Uint32())
			}
			sweepParse(t, name, parse, append(bytes.Clone(seed), tail...))
		}
	}
	for range 1000 {
		b := make([]byte, rng.IntN(128))
		for i := range b {
			b[i] = byte(rng.Uint32())
		}
		sweepParse(t, name, parse, b)
	}
	big := make([]byte, tpkt.MaxLength)
	for i := range big {
		big[i] = byte(rng.Uint32())
	}
	sweepParse(t, name, parse, big)
}

// readTestdata reads a file of testdata.
func readTestdata(t *testing.T, elem ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"testdata"}, elem...)...))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// readSharePDUs parses one share PDU after another, the way a Send Data Request
// carries them, until none is left.
func readSharePDUs(b []byte, each func(caps.PDU) error) error {
	r := wire.NewReader(b)
	for r.Remaining() > 0 {
		p, err := caps.ReadPDU(&r)
		if err != nil {
			return err
		}
		if err := each(p); err != nil {
			return err
		}
	}
	return nil
}

func TestHostileSweep(t *testing.T) {
	rng := rand.New(rand.NewPCG(12, 1))
	connectInitial := readTestdata(t, "ms-rdpbcgr", "connect-initial.bin")
	var requests [][]byte
	for _, f := range []string{"cr-freerdp-default.bin", "cr-freerdp-rdp.bin", "cr-remmina-tls.bin"} {
		requests = append(requests, readTestdata(t, "x224", f))
	}

	// The frames: TPKT and fast-path.
	var fp wire.Writer
	events := []input.Event{
		{Kind: input.KindScancode, Code: 0x1e}, {Kind: input.KindUnicode, Code: 'a'},
		{Kind: input.KindMouse, Flags: input.PtrMove, X: 10, Y: 20}, {Kind: input.KindSync, Flags: 1},
	}
	if err := input.AppendFastPath(&fp, events); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, tpkt.MaxLength)
	sweep(t, rng, "tpkt.ReadFrame", func(b []byte) error {
		_, _, err := tpkt.ReadFrame(bytes.NewReader(b), buf)
		return err
	}, append(append([][]byte{}, requests...), connectInitial, fp.Bytes())...)

	// X.224.
	var tpdus [][]byte
	for _, r := range requests {
		tpdus = append(tpdus, r[tpkt.HeaderLen:])
	}
	sweep(t, rng, "x224.ParseConnectionRequest", func(b []byte) error {
		_, err := x224.ParseConnectionRequest(b)
		return err
	}, tpdus...)
	var dr wire.Writer
	x224.WriteDisconnectRequest(&dr, &x224.DisconnectRequest{Reason: 1})
	sweep(t, rng, "x224.ParseDisconnectRequest", func(b []byte) error {
		_, err := x224.ParseDisconnectRequest(b)
		return err
	}, dr.Bytes())

	// MCS Connect-Initial, GCC and the client's data blocks.
	r := wire.NewReader(connectInitial[tpkt.HeaderLen:])
	if err := x224.ReadDataHeader(&r); err != nil {
		t.Fatal(err)
	}
	sweep(t, rng, "x224.ReadDataHeader", func(b []byte) error {
		r := wire.NewReader(b)
		return x224.ReadDataHeader(&r)
	}, connectInitial[tpkt.HeaderLen:])
	ciBytes := r.Rest()
	ci, err := mcs.ParseConnectInitial(ciBytes)
	if err != nil {
		t.Fatal(err)
	}
	sweep(t, rng, "mcs.ParseConnectInitial", func(b []byte) error {
		_, err := mcs.ParseConnectInitial(b)
		return err
	}, ciBytes)
	blocks, err := gcc.ParseConferenceCreateRequest(ci.UserData)
	if err != nil {
		t.Fatal(err)
	}
	sweep(t, rng, "gcc.ParseConferenceCreateRequest", func(b []byte) error {
		_, err := gcc.ParseConferenceCreateRequest(b)
		return err
	}, ci.UserData)
	sweep(t, rng, "gcc.ParseClientData", func(b []byte) error {
		_, err := gcc.ParseClientData(b)
		return err
	}, blocks)

	// The MCS domain PDUs a client sends, each read by every reader, as
	// the server does not know which comes.
	var domain [][]byte
	for _, write := range []func(w *wire.Writer) error{
		func(w *wire.Writer) error { mcs.WriteErectDomainRequest(w, &mcs.ErectDomainRequest{}); return nil },
		func(w *wire.Writer) error { mcs.WriteAttachUserRequest(w); return nil },
		func(w *wire.Writer) error {
			return mcs.WriteChannelJoinRequest(w, &mcs.ChannelJoinRequest{Initiator: 1007, ChannelID: 1003})
		},
		func(w *wire.Writer) error {
			return mcs.WriteSendDataRequest(w, &mcs.SendData{Initiator: 1007, ChannelID: 1003, Priority: 1, Segmentation: 3, Data: []byte{1, 2, 3}})
		},
		func(w *wire.Writer) error { return mcs.WriteDisconnectProviderUltimatum(w, mcs.ReasonUserRequested) },
	} {
		var w wire.Writer
		if err := write(&w); err != nil {
			t.Fatal(err)
		}
		domain = append(domain, w.Bytes())
	}
	sweep(t, rng, "mcs domain PDUs", func(b []byte) error {
		readers := []func(r *wire.Reader) error{
			func(r *wire.Reader) error { _, err := mcs.ReadErectDomainRequest(r); return err },
			mcs.ReadAttachUserRequest,
			func(r *wire.Reader) error { _, err := mcs.ReadChannelJoinRequest(r); return err },
			func(r *wire.Reader) error { _, err := mcs.ReadSendDataRequest(r); return err },
			func(r *wire.Reader) error { _, err := mcs.ReadDisconnectProviderUltimatum(r); return err },
		}
		var ok error = errors.New("NoReaderTookIt", "no reader read it")
		for _, read := range readers {
			r := wire.NewReader(b)
			err := read(&r)
			if err == nil {
				ok = nil
				continue
			}
			if _, tagged := errors.FromError(err); !tagged {
				return err
			}
		}
		return ok
	}, domain...)

	// The Client Info PDU.
	infoPacket := readTestdata(t, "ms-rdpbcgr", "client-info.bin")
	info, err := sec.ParseInfoPacket(infoPacket)
	if err != nil {
		t.Fatal(err)
	}
	sweep(t, rng, "sec.ParseInfoPacket", func(b []byte) error {
		_, err := sec.ParseInfoPacket(b)
		return err
	}, infoPacket)
	var ciw wire.Writer
	if err := sec.WriteClientInfo(&ciw, &info); err != nil {
		t.Fatal(err)
	}
	sweep(t, rng, "sec.ParseClientInfo", func(b []byte) error {
		_, err := sec.ParseClientInfo(b)
		return err
	}, ciw.Bytes())

	// The share PDUs: Confirm Active and the finalization PDUs.
	confirm := readTestdata(t, "ms-rdpbcgr", "confirm-active.bin")
	sweep(t, rng, "caps.ParseConfirmActive", func(b []byte) error {
		return readSharePDUs(b, func(p caps.PDU) error {
			_, err := caps.ParseConfirmActive(p)
			return err
		})
	}, confirm)
	var finals [][]byte
	for _, f := range []string{"client-synchronize.bin", "client-control-cooperate.bin", "client-control-request.bin", "client-font-list.bin", "shutdown-request.bin"} {
		finals = append(finals, readTestdata(t, "ms-rdpbcgr", f))
	}
	for _, d := range []struct {
		type2 uint8
		body  func(w *wire.Writer) error
	}{
		{caps.Type2RefreshRect, func(w *wire.Writer) error {
			return input.AppendRefreshRect(w, []input.Rect{{Left: 1, Top: 2, Right: 3, Bottom: 4}})
		}},
		{caps.Type2SuppressOutput, func(w *wire.Writer) error {
			input.AppendSuppressOutput(w, input.SuppressOutput{Allow: true, Area: input.Rect{Right: 9, Bottom: 9}})
			return nil
		}},
		{caps.Type2Input, func(w *wire.Writer) error { return input.AppendSlowPath(w, events) }},
	} {
		var w wire.Writer
		off := caps.BeginData(&w, 0x103ea, 1007, d.type2)
		if err := d.body(&w); err != nil {
			t.Fatal(err)
		}
		if err := caps.EndData(&w, off); err != nil {
			t.Fatal(err)
		}
		finals = append(finals, w.Bytes())
	}
	var rects []input.Rect
	sweep(t, rng, "share Data PDUs", func(b []byte) error {
		return readSharePDUs(b, func(p caps.PDU) error {
			if p.Flow {
				return nil
			}
			d, err := caps.ParseData(p.Body)
			if err != nil {
				return err
			}
			switch d.Type2 {
			case caps.Type2Synchronize:
				_, err = caps.ParseSynchronize(d.Body)
			case caps.Type2Control:
				_, err = caps.ParseControl(d.Body)
			case caps.Type2FontList:
				_, err = caps.ParseFonts(d.Body)
			case caps.Type2RefreshRect:
				rects, err = input.ParseRefreshRect(d.Body, rects[:0])
			case caps.Type2SuppressOutput:
				_, err = input.ParseSuppressOutput(d.Body)
			case caps.Type2Input:
				_, err = input.ParseSlowPath(d.Body)
			}
			return err
		})
	}, finals...)

	// Input, both ways.
	var sp wire.Writer
	if err := input.AppendSlowPath(&sp, events); err != nil {
		t.Fatal(err)
	}
	sweep(t, rng, "input.ParseFastPath", func(b []byte) error {
		_, err := input.ParseFastPath(b)
		return err
	}, fp.Bytes())
	sweep(t, rng, "input.ParseSlowPath", func(b []byte) error {
		_, err := input.ParseSlowPath(b)
		return err
	}, sp.Bytes())

	// What a client sends on dynamic channels.
	sweep(t, rng, "dynvc.ParseClient", func(b []byte) error {
		_, err := dynvc.ParseClient(b)
		return err
	}, []byte{0x50, 0x00, 0x02, 0x00}, []byte{0x10, 0x03, 0x00, 0x00, 0x00, 0x00},
		append([]byte{0x24, 0x03, 0x7b, 0x0c}, bytes.Repeat([]byte{0x71}, 64)...),
		[]byte{0x34, 0x03, 0x71}, []byte{0x40, 0x03})
}
