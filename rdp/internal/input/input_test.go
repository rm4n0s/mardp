package input

import (
	"bytes"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

func assertRoute(t *testing.T, err error, route string) {
	t.Helper()
	appErr, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("expected *errors.Error, got %T: %v", err, err)
	}
	if !appErr.HasRoute(route) {
		t.Errorf("unexpected failure path: %s, want %s", appErr.Route(), route)
	}
}

// readFast reads every event of a fast-path PDU.
func readFast(frame []byte) ([]Event, error) {
	f, err := ParseFastPath(frame)
	if err != nil {
		return nil, err
	}
	var out []Event
	for {
		e, ok, err := f.Next()
		if err != nil || !ok {
			return out, err
		}
		out = append(out, e)
	}
}

// readSlow reads every event of an Input Event PDU's data.
func readSlow(body []byte) ([]Event, error) {
	s, err := ParseSlowPath(body)
	if err != nil {
		return nil, err
	}
	var out []Event
	for {
		e, ok, err := s.Next()
		if err != nil || !ok {
			return out, err
		}
		out = append(out, e)
	}
}

// TestMicrosoftFastPath reads the events of Microsoft's annotated
// Fast-Path Input Event PDU (4.7), decrypted: one mouse move to (683, 367).
// The PDU itself is encrypted and carries a signature; the header here is
// the one the same events have under TLS.
func TestMicrosoftFastPath(t *testing.T) {
	events, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ms-rdpbcgr", "fast-path-input-events.bin"))
	if err != nil {
		t.Fatal(err)
	}
	// fpInputHeader: one event (1<<2), no flags; length 9.
	frame := append([]byte{0x04, 0x09}, events...)
	got, err := readFast(frame)
	if err != nil {
		t.Fatal(err)
	}
	want := Event{Kind: KindMouse, Flags: PtrMove, X: 683, Y: 367}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("%+v, want %+v", got, want)
	}
	var w wire.Writer
	if err := AppendFastPath(&w, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Bytes(), frame) {
		t.Errorf("% x, want % x", w.Bytes(), frame)
	}
	// The example's own header: 0xc4 is one event with the secure checksum
	// and encrypted flags, which under TLS is refused.
	_, err = ParseFastPath([]byte{0xc4, 0x02})
	assertRoute(t, err, "ParseFastPath."+TagHeaderInvalid)
}

// The bytes of each event, from the tables of 2.2.8.1.2.2 (fast-path) and
// 2.2.8.1.1.3.1.1 (slow-path).
func TestEventBytes(t *testing.T) {
	tests := []struct {
		name string
		e    Event
		fast []byte // the event alone; nil when the form has no such event
		slow []byte // the event alone, eventTime included
	}{
		{
			name: "scancode A down",
			e:    Event{Kind: KindScancode, Code: 0x1e, Time: 0x01020304},
			fast: []byte{0x00, 0x1e},
			slow: []byte{0x04, 0x03, 0x02, 0x01, 0x04, 0x00, 0x00, 0x00, 0x1e, 0x00, 0x00, 0x00},
		},
		{
			name: "right ctrl up",
			e:    Event{Kind: KindScancode, Flags: KeyRelease | KeyExtended, Code: 0x1d},
			fast: []byte{0x03, 0x1d},
			slow: []byte{0, 0, 0, 0, 0x04, 0x00, 0x00, 0x81, 0x1d, 0x00, 0x00, 0x00},
		},
		{
			name: "pause, extended1",
			e:    Event{Kind: KindScancode, Flags: KeyExtended1, Code: 0x1d},
			fast: []byte{0x04, 0x1d},
			slow: []byte{0, 0, 0, 0, 0x04, 0x00, 0x00, 0x02, 0x1d, 0x00, 0x00, 0x00},
		},
		{
			name: "mouse left down at (0x0102, 0x0304)",
			e:    Event{Kind: KindMouse, Flags: PtrDown | PtrButton1, X: 0x0102, Y: 0x0304},
			fast: []byte{0x20, 0x00, 0x90, 0x02, 0x01, 0x04, 0x03},
			slow: []byte{0, 0, 0, 0, 0x01, 0x80, 0x00, 0x90, 0x02, 0x01, 0x04, 0x03},
		},
		{
			name: "wheel back one notch",
			e:    Event{Kind: KindMouse, Flags: PtrWheel | PtrWheelNegative | 0x88},
			fast: []byte{0x20, 0x88, 0x03, 0, 0, 0, 0},
			slow: []byte{0, 0, 0, 0, 0x01, 0x80, 0x88, 0x03, 0, 0, 0, 0},
		},
		{
			name: "extended button 2 up",
			e:    Event{Kind: KindMouseX, Flags: PtrXButton2, X: 5, Y: 6},
			fast: []byte{0x40, 0x02, 0x00, 0x05, 0x00, 0x06, 0x00},
			slow: []byte{0, 0, 0, 0, 0x02, 0x80, 0x02, 0x00, 0x05, 0x00, 0x06, 0x00},
		},
		{
			name: "caps and num lock",
			e:    Event{Kind: KindSync, Flags: SyncCapsLock | SyncNumLock},
			fast: []byte{0x66},
			slow: []byte{0, 0, 0, 0, 0x00, 0x00, 0x00, 0x00, 0x06, 0x00, 0x00, 0x00},
		},
		{
			name: "unicode é up",
			e:    Event{Kind: KindUnicode, Flags: KeyRelease, Code: 0xe9},
			fast: []byte{0x81, 0xe9, 0x00},
			slow: []byte{0, 0, 0, 0, 0x05, 0x00, 0x00, 0x80, 0xe9, 0x00, 0x00, 0x00},
		},
		{
			name: "relative move (-1, 2)",
			e:    Event{Kind: KindMouseRel, Flags: PtrMove, X: 0xffff, Y: 2},
			fast: []byte{0xa0, 0x00, 0x08, 0xff, 0xff, 0x02, 0x00},
			slow: []byte{0, 0, 0, 0, 0x04, 0x80, 0x00, 0x08, 0xff, 0xff, 0x02, 0x00},
		},
		{
			name: "QoE timestamp",
			e:    Event{Kind: KindQoE, Time: 0x0a0b0c0d},
			fast: []byte{0xc0, 0x0d, 0x0c, 0x0b, 0x0a},
		},
		{
			name: "unused",
			e:    Event{Kind: KindUnused},
			slow: []byte{0, 0, 0, 0, 0x02, 0x00, 0, 0, 0, 0, 0, 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w wire.Writer
			err := AppendFastPath(&w, []Event{tt.e})
			if tt.fast == nil {
				assertRoute(t, err, "AppendFastPath->fastPathSize."+TagKindInvalid)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				want := append([]byte{0x04, byte(2 + len(tt.fast))}, tt.fast...)
				if !bytes.Equal(w.Bytes(), want) {
					t.Errorf("fast-path % x, want % x", w.Bytes(), want)
				}
				got, err := readFast(want)
				e := tt.e
				e.Time = 0
				if tt.e.Kind == KindQoE {
					e.Time = tt.e.Time
				}
				if err != nil || len(got) != 1 || got[0] != e {
					t.Errorf("fast-path read %+v %v, want %+v", got, err, e)
				}
			}
			w.Reset()
			err = AppendSlowPath(&w, []Event{tt.e})
			if tt.slow == nil {
				assertRoute(t, err, "AppendSlowPath."+TagKindInvalid)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := append([]byte{0x01, 0x00, 0x00, 0x00}, tt.slow...)
			if !bytes.Equal(w.Bytes(), want) {
				t.Errorf("slow-path % x, want % x", w.Bytes(), want)
			}
			got, err := readSlow(want)
			if err != nil || len(got) != 1 || got[0] != tt.e {
				t.Errorf("slow-path read %+v %v, want %+v", got, err, tt.e)
			}
		})
	}
}

// randomEvent returns an event of either form's kinds with random fields,
// as the form reads them back.
func randomEvent(rng *rand.Rand, slow bool) Event {
	kinds := []Kind{KindScancode, KindUnicode, KindMouse, KindMouseX, KindSync, KindMouseRel}
	if slow {
		kinds = append(kinds, KindUnused)
	} else {
		kinds = append(kinds, KindQoE)
	}
	e := Event{Kind: kinds[rng.IntN(len(kinds))]}
	switch e.Kind {
	case KindScancode:
		e.Flags = uint16(rng.IntN(8))
		e.Code = uint16(rng.IntN(256))
	case KindUnicode:
		e.Flags = uint16(rng.IntN(2))
		e.Code = uint16(rng.Uint32())
	case KindMouse, KindMouseX, KindMouseRel:
		e.Flags, e.X, e.Y = uint16(rng.Uint32()), uint16(rng.Uint32()), uint16(rng.Uint32())
	case KindSync:
		e.Flags = uint16(rng.IntN(16))
	case KindQoE:
		e.Time = rng.Uint32()
	}
	if slow {
		e.Time = rng.Uint32()
	}
	return e
}

func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(19, 9))
	for i := range 300 {
		slow := i%2 == 1
		events := make([]Event, rng.IntN(256))
		for j := range events {
			events[j] = randomEvent(rng, slow)
		}
		var w wire.Writer
		var got []Event
		var err error
		if slow {
			if err = AppendSlowPath(&w, events); err != nil {
				t.Fatal(err)
			}
			got, err = readSlow(w.Bytes())
		} else {
			if err = AppendFastPath(&w, events); err != nil {
				t.Fatal(err)
			}
			got, err = readFast(w.Bytes())
		}
		if err != nil {
			t.Fatalf("case %d (%d events): %v", i, len(events), err)
		}
		if len(got) != len(events) {
			t.Fatalf("case %d: %d events, want %d", i, len(got), len(events))
		}
		for j := range events {
			if got[j] != events[j] {
				t.Fatalf("case %d event %d: %+v, want %+v", i, j, got[j], events[j])
			}
		}
	}
}

// TestFastPathHeader checks the forms of fpInputHeader and the length.
func TestFastPathHeader(t *testing.T) {
	sync := Event{Kind: KindSync}
	tests := []struct {
		name   string
		events int
		head   []byte
	}{
		{"none", 0, []byte{0x00, 0x03, 0x00}},
		{"one", 1, []byte{0x04, 0x03}},
		{"fifteen in the header", 15, []byte{0x3c, 0x11}},
		{"sixteen in numEvents", 16, []byte{0x00, 0x13, 0x10}},
		{"a long length", 200, []byte{0x00, 0x80, 0xcc, 0xc8}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := make([]Event, tt.events)
			for i := range events {
				events[i] = sync
			}
			var w wire.Writer
			if err := AppendFastPath(&w, events); err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(w.Bytes(), tt.head) || w.Len() != len(tt.head)+tt.events {
				t.Errorf("% x", w.Bytes())
			}
			got, err := readFast(w.Bytes())
			if err != nil || len(got) != tt.events {
				t.Errorf("%d events, %v", len(got), err)
			}
		})
	}
}

func TestRequests(t *testing.T) {
	areas := []Rect{{0, 0, 1919, 1079}, {10, 20, 30, 40}}
	var w wire.Writer
	if err := AppendRefreshRect(&w, areas); err != nil {
		t.Fatal(err)
	}
	want := []byte{2, 0, 0, 0, 0, 0, 0, 0, 0x7f, 0x07, 0x37, 0x04, 10, 0, 20, 0, 30, 0, 40, 0}
	if !bytes.Equal(w.Bytes(), want) {
		t.Errorf("refresh rect % x", w.Bytes())
	}
	got, err := ParseRefreshRect(w.Bytes(), nil)
	if err != nil || len(got) != 2 || got[0] != areas[0] || got[1] != areas[1] {
		t.Errorf("%+v %v", got, err)
	}
	if got, err := ParseRefreshRect([]byte{0, 0, 0, 0}, nil); err != nil || len(got) != 0 {
		t.Errorf("no areas: %+v %v", got, err)
	}

	for _, tt := range []struct {
		s    SuppressOutput
		want []byte
	}{
		{SuppressOutput{}, []byte{0, 0, 0, 0}},
		{SuppressOutput{Allow: true, Area: Rect{0, 0, 1023, 767}}, []byte{1, 0, 0, 0, 0, 0, 0, 0, 0xff, 0x03, 0xff, 0x02}},
	} {
		w.Reset()
		AppendSuppressOutput(&w, tt.s)
		if !bytes.Equal(w.Bytes(), tt.want) {
			t.Errorf("suppress output % x, want % x", w.Bytes(), tt.want)
		}
		got, err := ParseSuppressOutput(tt.want)
		if err != nil || got != tt.s {
			t.Errorf("%+v %v, want %+v", got, err, tt.s)
		}
	}
}

func TestErrors(t *testing.T) {
	many := make([]Event, 256)
	for i := range many {
		many[i] = Event{Kind: KindSync}
	}
	tests := []struct {
		name  string
		run   func() error
		route string
	}{
		{"encrypted", func() error { _, err := ParseFastPath([]byte{0x84, 0x02}); return err },
			"ParseFastPath." + TagHeaderInvalid},
		{"not fast-path", func() error { _, err := ParseFastPath([]byte{0x01, 0x02}); return err },
			"ParseFastPath." + TagHeaderInvalid},
		{"length", func() error { _, err := ParseFastPath([]byte{0x04, 0x04, 0x60}); return err },
			"ParseFastPath." + TagLengthInvalid},
		{"long length", func() error { _, err := ParseFastPath([]byte{0x04, 0x80, 0x04}); return err },
			"ParseFastPath." + TagLengthInvalid},
		{"unknown fast-path event", func() error { _, err := readFast([]byte{0x04, 0x03, 0xe0}); return err },
			"readFast->FastPath.Next." + TagEventUnknown},
		{"fast-path trailing", func() error { _, err := readFast([]byte{0x04, 0x04, 0x60, 0x00}); return err },
			"readFast->FastPath.Next." + TagTrailingData},
		{"unknown slow-path event", func() error {
			_, err := readSlow([]byte{1, 0, 0, 0, 0, 0, 0, 0, 0x03, 0x00, 0, 0, 0, 0, 0, 0})
			return err
		}, "readSlow->SlowPath.Next." + TagEventUnknown},
		{"slow-path trailing", func() error { _, err := readSlow([]byte{0, 0, 0, 0, 9}); return err },
			"readSlow->SlowPath.Next." + TagTrailingData},
		{"fast-path too many", func() error { var w wire.Writer; return AppendFastPath(&w, many) },
			"AppendFastPath." + TagTooManyEvents},
		{"slow-path too many", func() error { var w wire.Writer; return AppendSlowPath(&w, make([]Event, 0x10000)) },
			"AppendSlowPath." + TagTooManyEvents},
		{"slow-path no kind", func() error { var w wire.Writer; return AppendSlowPath(&w, []Event{{}}) },
			"AppendSlowPath." + TagKindInvalid},
		{"refresh too many", func() error { var w wire.Writer; return AppendRefreshRect(&w, make([]Rect, 256)) },
			"AppendRefreshRect." + TagTooManyEvents},
		{"suppress value", func() error { _, err := ParseSuppressOutput([]byte{2, 0, 0, 0}); return err },
			"ParseSuppressOutput." + TagValueInvalid},
		{"suppress without area", func() error { _, err := ParseSuppressOutput([]byte{1, 0, 0, 0, 1}); return err },
			"ParseSuppressOutput->readRect->Reader.Sub." + wire.TagTruncated},
		{"refresh truncated", func() error { _, err := ParseRefreshRect([]byte{2, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8}, nil); return err },
			"ParseRefreshRect->readRect->Reader.Sub." + wire.TagTruncated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, tt.run(), tt.route)
		})
	}
}

// parseAny reads b as each of the forms the package reads.
func parseAny(b []byte) []error {
	_, e1 := readFast(b)
	_, e2 := readSlow(b)
	_, e3 := ParseRefreshRect(b, nil)
	_, e4 := ParseSuppressOutput(b)
	return []error{e1, e2, e3, e4}
}

// TestHostile cuts and mutates valid PDUs of every form: every failure is a
// tagged error, and nothing panics (SPEC §15.4).
func TestHostile(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 77))
	var seeds [][]byte
	for _, slow := range []bool{false, true} {
		events := make([]Event, 40)
		for i := range events {
			events[i] = randomEvent(rng, slow)
		}
		var w wire.Writer
		var err error
		if slow {
			err = AppendSlowPath(&w, events)
		} else {
			err = AppendFastPath(&w, events)
		}
		if err != nil {
			t.Fatal(err)
		}
		seeds = append(seeds, w.Bytes())
	}
	var w wire.Writer
	if err := AppendRefreshRect(&w, []Rect{{1, 2, 3, 4}, {5, 6, 7, 8}}); err != nil {
		t.Fatal(err)
	}
	seeds = append(seeds, w.Bytes())
	var s wire.Writer
	AppendSuppressOutput(&s, SuppressOutput{Allow: true, Area: Rect{1, 2, 3, 4}})
	seeds = append(seeds, s.Bytes())
	check := func(b []byte) {
		t.Helper()
		for _, err := range parseAny(b) {
			if err == nil {
				continue
			}
			if _, ok := errors.FromError(err); !ok {
				t.Fatalf("% x: %T %v", b, err, err)
			}
		}
	}
	for _, seed := range seeds {
		for n := range len(seed) {
			check(seed[:n])
		}
		for range 5000 {
			m := bytes.Clone(seed)
			for range 1 + rng.IntN(8) {
				m[rng.IntN(len(m))] = byte(rng.Uint32())
			}
			check(m[:rng.IntN(len(m)+1)])
		}
	}
	for range 5000 {
		b := make([]byte, rng.IntN(64))
		for i := range b {
			b[i] = byte(rng.Uint32())
		}
		check(b)
	}
}

func TestAllocations(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	fast := make([]Event, 30)
	slow := make([]Event, 30)
	for i := range fast {
		fast[i] = randomEvent(rng, false)
		slow[i] = randomEvent(rng, true)
	}
	var fw, sw wire.Writer
	if err := AppendFastPath(&fw, fast); err != nil {
		t.Fatal(err)
	}
	if err := AppendSlowPath(&sw, slow); err != nil {
		t.Fatal(err)
	}
	suppress := []byte{1, 0, 0, 0, 0, 0, 0, 0, 0xff, 0x03, 0xff, 0x02}
	n := testing.AllocsPerRun(100, func() {
		f, err := ParseFastPath(fw.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		for {
			_, ok, err := f.Next()
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				break
			}
		}
		s, err := ParseSlowPath(sw.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		for {
			_, ok, err := s.Next()
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				break
			}
		}
		if _, err := ParseSuppressOutput(suppress); err != nil {
			t.Fatal(err)
		}
	})
	if n != 0 {
		t.Errorf("%v allocations per run", n)
	}
}

func BenchmarkFastPath(b *testing.B) {
	rng := rand.New(rand.NewPCG(3, 4))
	events := make([]Event, 10)
	for i := range events {
		events[i] = randomEvent(rng, false)
	}
	var w wire.Writer
	if err := AppendFastPath(&w, events); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		f, err := ParseFastPath(w.Bytes())
		if err != nil {
			b.Fatal(err)
		}
		for {
			_, ok, err := f.Next()
			if err != nil {
				b.Fatal(err)
			}
			if !ok {
				break
			}
		}
	}
}
