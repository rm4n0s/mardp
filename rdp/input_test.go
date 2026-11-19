package rdp

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/rdp/internal/caps"
	"github.com/rm4n0s/mardp/rdp/internal/input"
	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/internal/x224"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// recordSink is an InputSink and a FlowSink that records its calls.
type recordSink struct {
	mu      sync.Mutex
	calls   []string
	changed chan struct{}
}

func newRecordSink() *recordSink { return &recordSink{changed: make(chan struct{}, 1)} }

func (r *recordSink) add(format string, args ...any) {
	r.mu.Lock()
	r.calls = append(r.calls, fmt.Sprintf(format, args...))
	r.mu.Unlock()
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

func (r *recordSink) Key(sc uint8, ext Extended, down bool) { r.add("key %#x %d %v", sc, ext, down) }
func (r *recordSink) Unicode(c rune, down bool)             { r.add("unicode %q %v", c, down) }
func (r *recordSink) MouseMove(x, y int)                    { r.add("move %d %d", x, y) }
func (r *recordSink) MouseButton(b MouseButton, down bool, x, y int) {
	r.add("button %s %v %d %d", b, down, x, y)
}
func (r *recordSink) Wheel(dx, dy int) { r.add("wheel %d %d", dx, dy) }
func (r *recordSink) SyncLocks(caps, num, scroll bool) {
	r.add("locks caps=%v num=%v scroll=%v", caps, num, scroll)
}
func (r *recordSink) KeyFrameWanted()                  { r.add("key frame") }
func (r *recordSink) OutputSuppressed(suppressed bool) { r.add("suppressed %v", suppressed) }

// wait returns the calls once there are n of them.
func (r *recordSink) wait(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		r.mu.Lock()
		calls := slices.Clone(r.calls)
		r.mu.Unlock()
		if len(calls) >= n {
			return calls
		}
		select {
		case <-r.changed:
		case <-deadline:
			t.Fatalf("%d calls, want %d: %q", len(calls), n, calls)
		}
	}
}

// inputCase is an event and the calls it makes.
type inputCase struct {
	e     input.Event
	calls []string
}

// inputCases are events of every kind both forms carry.
var inputCases = []inputCase{
	{input.Event{Kind: input.KindSync, Flags: input.SyncCapsLock | input.SyncNumLock}, []string{"locks caps=true num=true scroll=false"}},
	{input.Event{Kind: input.KindScancode, Code: 0x1e}, []string{"key 0x1e 0 true"}},
	{input.Event{Kind: input.KindScancode, Flags: input.KeyRelease | input.KeyExtended, Code: 0x1d}, []string{"key 0x1d 1 false"}},
	{input.Event{Kind: input.KindScancode, Flags: input.KeyExtended1, Code: 0x1d}, []string{"key 0x1d 2 true"}},
	{input.Event{Kind: input.KindUnicode, Code: 0xe9}, []string{`unicode 'é' true`}},
	{input.Event{Kind: input.KindUnicode, Flags: input.KeyRelease, Code: 0xe9}, []string{`unicode 'é' false`}},
	// U+1F600 as its two surrogates.
	{input.Event{Kind: input.KindUnicode, Code: 0xd83d}, nil},
	{input.Event{Kind: input.KindUnicode, Code: 0xde00}, []string{`unicode '😀' true`}},
	{input.Event{Kind: input.KindMouse, Flags: input.PtrMove, X: 100, Y: 200}, []string{"move 100 200"}},
	{input.Event{Kind: input.KindMouse, Flags: input.PtrDown | input.PtrButton1, X: 100, Y: 200}, []string{"button left true 100 200"}},
	{input.Event{Kind: input.KindMouse, Flags: input.PtrButton2, X: 1, Y: 2}, []string{"button right false 1 2"}},
	{input.Event{Kind: input.KindMouse, Flags: input.PtrDown | input.PtrButton3, X: 3, Y: 4}, []string{"button middle true 3 4"}},
	{input.Event{Kind: input.KindMouse, Flags: input.PtrMove | input.PtrDown | input.PtrButton1, X: 5, Y: 6}, []string{"move 5 6", "button left true 5 6"}},
	{input.Event{Kind: input.KindMouse, Flags: input.PtrWheel | 0x78, X: 9, Y: 9}, []string{"wheel 0 120"}},
	{input.Event{Kind: input.KindMouse, Flags: input.PtrWheel | input.PtrWheelNegative | 0x88}, []string{"wheel 0 -120"}},
	{input.Event{Kind: input.KindMouse, Flags: input.PtrHWheel | 0x78}, []string{"wheel 120 0"}},
	{input.Event{Kind: input.KindMouse, Flags: input.PtrHWheel | input.PtrWheelNegative | 0x88}, []string{"wheel -120 0"}},
	{input.Event{Kind: input.KindMouseX, Flags: input.PtrXDown | input.PtrXButton1, X: 7, Y: 8}, []string{"button back true 7 8"}},
	{input.Event{Kind: input.KindMouseX, Flags: input.PtrXButton2, X: 7, Y: 8}, []string{"button forward false 7 8"}},
	// Relative mouse events are not asked for, and dropped.
	{input.Event{Kind: input.KindMouseRel, Flags: input.PtrMove, X: 1, Y: 1}, nil},
}

// sinkSessions is a Handler that gives each session a recordSink for its
// input and its flow before it hands the session to the test.
type sinkSessions struct {
	sink   *recordSink
	opened chan *Session
}

func (h *sinkSessions) SessionOpened(ctx context.Context, s *Session) {
	s.SetInputSink(h.sink)
	s.SetFlowSink(h.sink)
	h.opened <- s
	<-ctx.Done()
}

func (h *sinkSessions) next(t *testing.T) *Session {
	t.Helper()
	select {
	case s := <-h.opened:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("no session opened")
	}
	return nil
}

// TestInput sends every kind of event in both forms, mixed, and checks the
// sink gets them in the client's order (SPEC §5.3, §6).
func TestInput(t *testing.T) {
	h := &sinkSessions{sink: newRecordSink(), opened: make(chan *Session, 1)}
	ts := start(t, Config{Handler: h})
	c, _ := ts.open(t, rdptest.Options{})
	h.next(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var fast, slow []input.Event
	var want []string
	for _, ic := range inputCases {
		fast = append(fast, ic.e)
		want = append(want, ic.calls...)
	}
	for _, ic := range inputCases {
		e := ic.e
		e.Time = 1234
		slow = append(slow, e)
		want = append(want, ic.calls...)
	}
	fast = append(fast, input.Event{Kind: input.KindQoE, Time: 99})
	slow = append(slow, input.Event{Kind: input.KindUnused})
	if err := c.SendInput(ctx, fast...); err != nil {
		t.Fatal(err)
	}
	if err := c.SendSlowInput(ctx, slow...); err != nil {
		t.Fatal(err)
	}
	// One event per PDU, alternating forms.
	last := []input.Event{
		{Kind: input.KindMouse, Flags: input.PtrMove, X: 1, Y: 1},
		{Kind: input.KindMouse, Flags: input.PtrMove, X: 2, Y: 2},
		{Kind: input.KindMouse, Flags: input.PtrMove, X: 3, Y: 3},
		{Kind: input.KindMouse, Flags: input.PtrMove, X: 4, Y: 4},
	}
	for i, e := range last {
		send := c.SendInput
		if i%2 == 1 {
			send = c.SendSlowInput
		}
		if err := send(ctx, e); err != nil {
			t.Fatal(err)
		}
		want = append(want, fmt.Sprintf("move %d %d", e.X, e.Y))
	}
	got := h.sink.wait(t, len(want))
	if !slices.Equal(got, want) {
		t.Errorf("calls\n got %q\nwant %q", got, want)
	}
}

// TestSurrogates checks how UTF-16 surrogates that do not pair are
// reported.
func TestSurrogates(t *testing.T) {
	sink := newRecordSink()
	sr := sessionReader{c: &conn{log: slog.New(slog.DiscardHandler)}, s: &Session{}, in: sink}
	tests := []struct {
		name  string
		units []uint16
		up    []bool
		want  []string
	}{
		{"pair", []uint16{0xd83d, 0xde00}, []bool{false, false}, []string{`unicode '😀' true`}},
		{"pair released", []uint16{0xd83d, 0xde00}, []bool{true, true}, []string{`unicode '😀' false`}},
		{"lone low", []uint16{0xde00}, []bool{false}, []string{`unicode '�' true`}},
		{"high then a letter", []uint16{0xd83d, 'a'}, []bool{false, false}, []string{`unicode '�' true`, `unicode 'a' true`}},
		{"high then high", []uint16{0xd83d, 0xd83d, 0xde00}, []bool{false, false, false}, []string{`unicode '�' true`, `unicode '😀' true`}},
		{"down high, up low", []uint16{0xd83d, 0xde00}, []bool{false, true}, []string{`unicode '�' true`, `unicode '�' false`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink.calls = nil
			sr.haveHigh = false
			for i, u := range tt.units {
				f := uint16(0)
				if tt.up[i] {
					f = input.KeyRelease
				}
				sr.event(input.Event{Kind: input.KindUnicode, Flags: f, Code: u})
			}
			if !slices.Equal(sink.calls, tt.want) {
				t.Errorf("%q, want %q", sink.calls, tt.want)
			}
		})
	}
}

// awaitDenied sends a Shutdown Request and reads the Shutdown Request
// Denied the server answers with: the server has then handled everything
// sent before.
func awaitDenied(t *testing.T, ctx context.Context, c *rdptest.Client) {
	t.Helper()
	if err := c.SendShutdownRequest(ctx); err != nil {
		t.Fatal(err)
	}
	d, err := c.ReadShareData(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d.Type2 != caps.Type2ShutdownDenied || d.ShareID != shareID || len(d.Body) != 0 {
		t.Fatalf("type2 %#x share %#x body % x", d.Type2, d.ShareID, d.Body)
	}
}

// TestRequests checks Refresh Rect, Suppress Output and Shutdown Request.
func TestRequests(t *testing.T) {
	h := &sinkSessions{sink: newRecordSink(), opened: make(chan *Session, 1)}
	ts := start(t, Config{Handler: h})
	c, nc := ts.open(t, rdptest.Options{})
	s := h.next(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	awaitDenied(t, ctx, c)
	if err := c.SendRefreshRect(ctx, input.Rect{Right: 1023, Bottom: 767}, input.Rect{Left: 5, Top: 5, Right: 10, Bottom: 10}); err != nil {
		t.Fatal(err)
	}
	h.sink.wait(t, 1)
	if s.OutputSuppressed() {
		t.Error("suppressed before Suppress Output")
	}
	for range 2 { // the second changes nothing
		if err := c.SendSuppressOutput(ctx, input.SuppressOutput{}); err != nil {
			t.Fatal(err)
		}
	}
	awaitDenied(t, ctx, c)
	if !s.OutputSuppressed() {
		t.Error("not suppressed after Suppress Output")
	}
	for range 2 {
		if err := c.SendSuppressOutput(ctx, input.SuppressOutput{Allow: true, Area: input.Rect{Right: 1023, Bottom: 767}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.SendRefreshRect(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"key frame", "suppressed true", "suppressed false", "key frame", "key frame"}
	if got := h.sink.wait(t, len(want)); !slices.Equal(got, want) {
		t.Errorf("calls %q, want %q", got, want)
	}
	if s.OutputSuppressed() {
		t.Error("suppressed after Allow")
	}
	// The client may still disconnect after the denial.
	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	closedWithin(t, nc, 2*time.Second)
}

// TestSinksSetLate checks what a sink set after the client began gets: the
// lock state and the suppression it missed, and nothing else.
func TestSinksSetLate(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h})
	c, _ := ts.open(t, rdptest.Options{})
	s := h.next(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.SendInput(ctx,
		input.Event{Kind: input.KindSync, Flags: input.SyncScrollLock},
		input.Event{Kind: input.KindScancode, Code: 0x10},
		input.Event{Kind: input.KindMouse, Flags: input.PtrMove, X: 1, Y: 1},
	); err != nil {
		t.Fatal(err)
	}
	if err := c.SendRefreshRect(ctx, input.Rect{Right: 1, Bottom: 1}); err != nil {
		t.Fatal(err)
	}
	if err := c.SendSuppressOutput(ctx, input.SuppressOutput{}); err != nil {
		t.Fatal(err)
	}
	awaitDenied(t, ctx, c)
	sink := newRecordSink()
	s.SetInputSink(sink)
	s.SetFlowSink(sink)
	if err := c.SendInput(ctx, input.Event{Kind: input.KindScancode, Code: 0x11}); err != nil {
		t.Fatal(err)
	}
	want := []string{"locks caps=false num=false scroll=true", "suppressed true", "key 0x11 0 true"}
	if got := sink.wait(t, len(want)); !slices.Equal(got, want) {
		t.Errorf("calls %q, want %q", got, want)
	}
	// Without sinks, input and requests are dropped.
	s.SetInputSink(nil)
	s.SetFlowSink(nil)
	if err := c.SendInput(ctx, inputCase{}.e, input.Event{Kind: input.KindScancode, Code: 0x12}); err == nil {
		t.Fatal("an event of no kind was written")
	}
	if err := c.SendInput(ctx, input.Event{Kind: input.KindScancode, Code: 0x12}); err != nil {
		t.Fatal(err)
	}
	if err := c.SendSuppressOutput(ctx, input.SuppressOutput{Allow: true}); err != nil {
		t.Fatal(err)
	}
	awaitDenied(t, ctx, c)
	if got := sink.wait(t, len(want)); len(got) != len(want) {
		t.Errorf("calls after the sinks were taken away: %q", got[len(want):])
	}
}

// TestSessionIgnores sends what the server reads only as far as its header
// (SPEC §5.3): the session goes on.
func TestSessionIgnores(t *testing.T) {
	h := &sinkSessions{sink: newRecordSink(), opened: make(chan *Session, 1)}
	ts := start(t, Config{Handler: h})
	c, _ := ts.open(t, rdptest.Options{})
	h.next(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Data on a static channel.
	if err := c.SendData(ctx, func(w *wire.Writer) error {
		return mcs.WriteSendDataRequest(w, &mcs.SendData{Initiator: c.UserID, ChannelID: c.Joined[2], Data: []byte{1, 2, 3}})
	}); err != nil {
		t.Fatal(err)
	}
	// A Confirm Active, a Control PDU and a FlowPDU, in one Send Data.
	if err := c.SendShare(ctx, func(w *wire.Writer) error {
		if err := caps.WriteConfirmActive(w, &caps.ConfirmActive{ShareID: shareID, Source: c.UserID, Originator: serverInitiator}); err != nil {
			return err
		}
		if err := caps.WriteControl(w, shareID, c.UserID, caps.Control{Action: caps.ActionCooperate}); err != nil {
			return err
		}
		w.Write([]byte{0x00, 0x80, 0x42, 0x00, 0x00, 0x00, 0x00, 0x00})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	awaitDenied(t, ctx, c)
	if err := c.SendInput(ctx, input.Event{Kind: input.KindScancode, Code: 0x1c}); err != nil {
		t.Fatal(err)
	}
	if got := h.sink.wait(t, 1); !slices.Equal(got, []string{"key 0x1c 0 true"}) {
		t.Errorf("calls %q", got)
	}
}

// TestHostileInput sends broken input in a session: the session ends with
// the parser's tagged error (SPEC §15.4).
func TestHostileInput(t *testing.T) {
	p := certs(t)
	s, err := NewServer(Config{Certificate: p.server, Authenticator: StaticAuthenticator(testUser, testPassword), Handler: nopHandler})
	if err != nil {
		t.Fatal(err)
	}
	raw := func(b ...byte) func(ctx context.Context, c *rdptest.Client) error {
		return func(ctx context.Context, c *rdptest.Client) error { return c.WriteFrame(ctx, b) }
	}
	data := func(type2 uint8, body ...byte) func(ctx context.Context, c *rdptest.Client) error {
		return func(ctx context.Context, c *rdptest.Client) error {
			return c.SendShare(ctx, func(w *wire.Writer) error {
				off := caps.BeginData(w, c.ShareID, c.UserID, type2)
				w.Write(body)
				return caps.EndData(w, off)
			})
		}
	}
	const session = "conn.runSession->conn.readSession->"
	tests := []struct {
		name   string
		client func(ctx context.Context, c *rdptest.Client) error
		route  string
	}{
		{"encrypted fast-path", raw(0x84, 0x03, 0x60), session + "sessionReader.fastPath->ParseFastPath." + input.TagHeaderInvalid},
		{"unknown fast-path event", raw(0x04, 0x03, 0xe0), session + "sessionReader.fastPath->FastPath.Next." + input.TagEventUnknown},
		{"fast-path event cut", raw(0x04, 0x03, 0x00), session + "sessionReader.fastPath->FastPath.Next->Reader.U8." + wire.TagTruncated},
		{"unknown slow-path event", data(caps.Type2Input, 1, 0, 0, 0, 0, 0, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0),
			session + "sessionReader.tpkt->sessionReader.data->sessionReader.slowPath->SlowPath.Next." + input.TagEventUnknown},
		{"slow-path event cut", data(caps.Type2Input, 1, 0, 0, 0, 0, 0, 0, 0, 4, 0),
			session + "sessionReader.tpkt->sessionReader.data->sessionReader.slowPath->SlowPath.Next->Reader.Sub." + wire.TagTruncated},
		{"suppress output value", data(caps.Type2SuppressOutput, 7, 0, 0, 0),
			session + "sessionReader.tpkt->sessionReader.data->ParseSuppressOutput." + input.TagValueInvalid},
		{"refresh rect cut", data(caps.Type2RefreshRect, 1, 0, 0, 0, 1, 2),
			session + "sessionReader.tpkt->sessionReader.data->ParseRefreshRect->readRect->Reader.Sub." + wire.TagTruncated},
		{"another share", func(ctx context.Context, c *rdptest.Client) error {
			return c.SendShare(ctx, func(w *wire.Writer) error { return caps.WriteShutdownRequest(w, shareID+1, c.UserID) })
		}, session + "sessionReader.tpkt->sessionReader.data." + TagShareIDInvalid},
		{"another channel", func(ctx context.Context, c *rdptest.Client) error {
			return c.SendData(ctx, func(w *wire.Writer) error {
				return mcs.WriteSendDataRequest(w, &mcs.SendData{Initiator: c.UserID, ChannelID: 2000, Data: []byte{1}})
			})
		}, session + "sessionReader.tpkt." + TagChannelInvalid},
		{"share PDU cut", func(ctx context.Context, c *rdptest.Client) error {
			return c.SendData(ctx, func(w *wire.Writer) error {
				return mcs.WriteSendDataRequest(w, &mcs.SendData{Initiator: c.UserID, ChannelID: ioChannelID, Data: []byte{0x40, 0, 0x17, 0}})
			})
		}, session + "sessionReader.tpkt->ReadPDU." + caps.TagLengthInvalid},
		{"compressed data", func(ctx context.Context, c *rdptest.Client) error {
			return c.SendShare(ctx, func(w *wire.Writer) error {
				off := caps.BeginData(w, c.ShareID, c.UserID, caps.Type2Input)
				if err := caps.EndData(w, off); err != nil {
					return err
				}
				w.Bytes()[off+15] = 0x20 // compressedType: PACKET_COMPRESSED
				return nil
			})
		}, session + "sessionReader.tpkt->sessionReader.data->ParseData." + caps.TagCompressed},
		{"not a data TPDU", raw(3, 0, 0, 7, 2, 0x80, 0x80), session + "sessionReader.tpkt->ReadDataHeader." + x224.TagUnexpectedCode},
		{"attach user in the session", func(ctx context.Context, c *rdptest.Client) error {
			return c.SendData(ctx, func(w *wire.Writer) error { mcs.WriteAttachUserRequest(w); return nil })
		}, session + "sessionReader.tpkt->ReadSendDataRequest->readSendData." + mcs.TagUnexpectedPDU},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc, sc := tcpPair(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				err := s.serveConn(ctx, sc, s.log, func() {})
				if cerr := sc.Close(); cerr != nil {
					t.Error(cerr)
				}
				done <- err
			}()
			c := rdptest.New(cc, rdptest.Options{TLSConfig: p.client, User: testUser, Password: testPassword})
			for _, step := range []func(context.Context) error{c.Negotiate, c.StartTLS, c.ConnectMCS, c.Attach, c.JoinAll, c.Login, c.Activate} {
				if err := step(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if err := tt.client(ctx, c); err != nil {
				t.Logf("client: %v", err)
			}
			assertRoute(t, <-done, tt.route)
		})
	}
}

// nopSink is an InputSink that does nothing.
type nopSink struct{}

func (nopSink) Key(uint8, Extended, bool)               {}
func (nopSink) Unicode(rune, bool)                      {}
func (nopSink) MouseMove(int, int)                      {}
func (nopSink) MouseButton(MouseButton, bool, int, int) {}
func (nopSink) Wheel(int, int)                          {}
func (nopSink) SyncLocks(bool, bool, bool)              {}
func (nopSink) KeyFrameWanted()                         {}
func (nopSink) OutputSuppressed(bool)                   {}

// TestInputAllocations checks that delivering input allocates nothing
// (SPEC §11).
func TestInputAllocations(t *testing.T) {
	var events []input.Event
	for _, ic := range inputCases {
		events = append(events, ic.e)
	}
	var fw, sw wire.Writer
	if err := input.AppendFastPath(&fw, events); err != nil {
		t.Fatal(err)
	}
	if err := input.AppendSlowPath(&sw, events); err != nil {
		t.Fatal(err)
	}
	s := &Session{}
	s.SetInputSink(nopSink{})
	s.SetFlowSink(nopSink{})
	sr := sessionReader{c: &conn{log: slog.New(slog.DiscardHandler)}, s: s}
	sr.takeSinks()
	n := testing.AllocsPerRun(100, func() {
		sr.takeSinks()
		if err := sr.fastPath(fw.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err := sr.slowPath(sw.Bytes()); err != nil {
			t.Fatal(err)
		}
	})
	if n != 0 {
		t.Errorf("%v allocations per run", n)
	}
}
