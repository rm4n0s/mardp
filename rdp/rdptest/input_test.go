package rdptest

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/rdp/internal/caps"
	"github.com/rm4n0s/mardp/rdp/internal/input"
	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/tpkt"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/internal/x224"
)

// TestSenders checks what each input sender writes, as the fake server
// reads it.
func TestSenders(t *testing.T) {
	certs, err := testCerts()
	if err != nil {
		t.Fatal(err)
	}
	trusted := certs[0]
	frames := make(chan []byte, 8)
	nc := run(t, trusted.server, func(f *fake) {
		f.joined()
		for range 5 {
			_, b, err := tpkt.ReadFrame(f.rw, f.buf)
			if err != nil {
				f.t.Errorf("fake server: %v", err)
				return
			}
			frames <- bytes.Clone(b)
		}
		f.share(func(w *wire.Writer) error { return caps.WriteShutdownDenied(w, 0x000103ea, 1002) })
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Connect(ctx, nc, Options{TLSConfig: trusted.client})
	if err != nil {
		t.Fatal(err)
	}
	c.ShareID = 0x000103ea
	key := input.Event{Kind: input.KindScancode, Code: 0x1e}
	area := input.Rect{Right: 9, Bottom: 9}

	// data reads the Data PDU of a TPKT and checks its type.
	data := func(t *testing.T, b []byte, type2 uint8) []byte {
		t.Helper()
		r := wire.NewReader(b[tpkt.HeaderLen:])
		if err := x224.ReadDataHeader(&r); err != nil {
			t.Fatal(err)
		}
		sd, err := mcs.ReadSendDataRequest(&r)
		if err != nil {
			t.Fatal(err)
		}
		if sd.Initiator != c.UserID || sd.ChannelID != 1003 {
			t.Errorf("initiator %d channel %d", sd.Initiator, sd.ChannelID)
		}
		pr := wire.NewReader(sd.Data)
		p, err := caps.ReadPDU(&pr)
		if err != nil {
			t.Fatal(err)
		}
		d, err := caps.ParseData(p.Body)
		if err != nil {
			t.Fatal(err)
		}
		if d.ShareID != c.ShareID || d.Type2 != type2 || p.Source != c.UserID {
			t.Fatalf("share %#x type2 %#x source %d", d.ShareID, d.Type2, p.Source)
		}
		return d.Body
	}
	tests := []struct {
		name  string
		send  func() error
		check func(t *testing.T, b []byte)
	}{
		{"fast-path input", func() error { return c.SendInput(ctx, key) }, func(t *testing.T, b []byte) {
			f, err := input.ParseFastPath(b)
			if err != nil {
				t.Fatal(err)
			}
			if e, ok, err := f.Next(); err != nil || !ok || e != key {
				t.Errorf("%+v %v %v", e, ok, err)
			}
		}},
		{"slow-path input", func() error { return c.SendSlowInput(ctx, key) }, func(t *testing.T, b []byte) {
			s, err := input.ParseSlowPath(data(t, b, caps.Type2Input))
			if err != nil {
				t.Fatal(err)
			}
			if e, ok, err := s.Next(); err != nil || !ok || e != key {
				t.Errorf("%+v %v %v", e, ok, err)
			}
		}},
		{"refresh rect", func() error { return c.SendRefreshRect(ctx, area) }, func(t *testing.T, b []byte) {
			got, err := input.ParseRefreshRect(data(t, b, caps.Type2RefreshRect), nil)
			if err != nil || len(got) != 1 || got[0] != area {
				t.Errorf("%+v %v", got, err)
			}
		}},
		{"suppress output", func() error { return c.SendSuppressOutput(ctx, input.SuppressOutput{Allow: true, Area: area}) }, func(t *testing.T, b []byte) {
			got, err := input.ParseSuppressOutput(data(t, b, caps.Type2SuppressOutput))
			if err != nil || got != (input.SuppressOutput{Allow: true, Area: area}) {
				t.Errorf("%+v %v", got, err)
			}
		}},
		{"shutdown request", func() error { return c.SendShutdownRequest(ctx) }, func(t *testing.T, b []byte) {
			if body := data(t, b, caps.Type2ShutdownRequest); len(body) != 0 {
				t.Errorf("body % x", body)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.send(); err != nil {
				t.Fatal(err)
			}
			select {
			case b := <-frames:
				tt.check(t, b)
			case <-ctx.Done():
				t.Fatal("the fake server read nothing")
			}
		})
	}
	d, err := c.ReadShareData(ctx)
	if err != nil || d.Type2 != caps.Type2ShutdownDenied {
		t.Errorf("%+v %v", d, err)
	}

	// Events neither form carries are refused before anything is sent.
	if err := c.SendInput(ctx, input.Event{Kind: input.KindUnused}); err == nil {
		t.Error("fast-path sent an unused event")
	}
	if err := c.SendSlowInput(ctx, input.Event{Kind: input.KindQoE}); err == nil {
		t.Error("slow-path sent a QoE timestamp")
	}
	if err := c.SendRefreshRect(ctx, make([]input.Rect, 256)...); err == nil {
		t.Error("256 areas sent")
	}
}
