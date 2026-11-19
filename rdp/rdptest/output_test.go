package rdptest

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/rdp/internal/caps"
	"github.com/rm4n0s/mardp/rdp/internal/fastpath"
	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/sec"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

func TestReadUpdate(t *testing.T) {
	certs, err := testCerts()
	if err != nil {
		t.Fatal(err)
	}
	trusted := certs[0]
	update := func(code uint8, data []byte) []byte {
		var w wire.Writer
		if err := fastpath.AppendUpdate(&w, code, data, 1<<20); err != nil {
			t.Fatal(err)
		}
		return w.Bytes()
	}
	big := bytes.Repeat([]byte{1, 2, 3}, fastpath.MaxFragment)
	tests := []struct {
		name   string
		script func(f *fake)
		route  string // "" for the update
		want   []byte
	}{
		{"after a share PDU", func(f *fake) {
			f.indication(func(w *wire.Writer) { sec.WriteSetErrorInfo(w, 7, 0) })
			f.write(update(fastpath.UpdateBitmap, []byte{4, 5}))
		}, "", []byte{4, 5}},
		{"fragmented", func(f *fake) { f.write(update(fastpath.UpdateBitmap, big)) }, "", big},
		{"disconnected", func(f *fake) {
			f.send(true, func(w *wire.Writer) error {
				return mcs.WriteDisconnectProviderUltimatum(w, mcs.ReasonProviderInitiated)
			})
		}, "Client.ReadUpdate." + TagPDUUnexpected, nil},
		{"two updates in a PDU", func(f *fake) {
			// One PDU: header, length 13, then two Pointer Position updates.
			f.write([]byte{0x00, 0x0d, 0x08, 0x02, 0x00, 1, 2, 0x08, 0x02, 0x00, 3, 4, 0})
		}, "Client.ReadUpdate." + TagPDUUnexpected, nil},
		{"encrypted", func(f *fake) { f.write([]byte{0x80, 0x05, 0x05, 0x00, 0x00}) }, "Client.ReadUpdate->ParsePDU." + fastpath.TagHeaderInvalid, nil},
		{"update cut", func(f *fake) { f.write([]byte{0x00, 0x04, 0x05, 0x01}) }, "Client.ReadUpdate->ReadUpdate->Reader.", nil},
		{"last without first", func(f *fake) { f.write([]byte{0x00, 0x05, 0x15, 0x00, 0x00}) }, "Client.ReadUpdate->Reassembler.Add." + fastpath.TagFragmentSequence, nil},
		{"not a data TPDU", func(f *fake) {
			f.send(false, func(w *wire.Writer) error { w.Write([]byte{0x02, 0x70, 0x80}); return nil })
		}, "Client.ReadUpdate->ReadDataHeader.", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			nc := run(t, trusted.server, func(f *fake) {
				f.joined()
				tt.script(f)
			})
			c, err := Connect(ctx, nc, Options{TLSConfig: trusted.client})
			if err != nil {
				t.Fatal(err)
			}
			c.Caps.Add(caps.SetMultifragmentUpdate)
			c.Caps.MaxRequestSize = 1 << 20
			u, err := c.ReadUpdate(ctx)
			if tt.route != "" {
				assertRoute(t, err, tt.route)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(u.Data, tt.want) {
				t.Errorf("got %d bytes, want %d", len(u.Data), len(tt.want))
			}
		})
	}
}
