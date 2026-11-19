package rdptest

import (
	"context"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/caps"
	"github.com/rm4n0s/mardp/rdp/internal/mcs"
	"github.com/rm4n0s/mardp/rdp/internal/sec"
	"github.com/rm4n0s/mardp/rdp/internal/wire"
	"github.com/rm4n0s/mardp/rdp/internal/x224"
)

// share sends a share PDU on the I/O channel.
func (f *fake) share(pdu func(w *wire.Writer) error) {
	f.indication(func(w *wire.Writer) {
		if err := pdu(w); err != nil {
			f.t.Error(err)
		}
	})
}

// demandActive sends a Demand Active and reads the client's Confirm Active
// and its four finalization PDUs, checking that they come from user 1006.
func (f *fake) demandActive() {
	f.share(func(w *wire.Writer) error {
		var c caps.Caps
		c.Add(caps.SetGeneral)
		c.Add(caps.SetBitmap)
		c.Bitmap = caps.Bitmap{BitsPerPixel: 32, DesktopWidth: 640, DesktopHeight: 480}
		return caps.WriteDemandActive(w, &caps.DemandActive{ShareID: 7, Source: 1002, SourceDescriptor: []byte("RDP\x00"), Caps: c})
	})
	for i := range 5 {
		r := wire.NewReader(f.read())
		if err := x224.ReadDataHeader(&r); err != nil {
			f.t.Error(err)
			return
		}
		d, err := mcs.ReadSendDataRequest(&r)
		if err != nil {
			f.t.Error(err)
			return
		}
		sr := wire.NewReader(d.Data)
		p, err := caps.ReadPDU(&sr)
		if err != nil || d.Initiator != 1006 || d.ChannelID != 1003 || p.Source != 1006 {
			f.t.Errorf("PDU %d: %+v from %d on %d: %v", i, p, d.Initiator, d.ChannelID, err)
			return
		}
		if i == 0 {
			ca, err := caps.ParseConfirmActive(p)
			if err != nil || ca.ShareID != 7 || ca.Originator != 1002 || ca.Caps.Bitmap.DesktopWidth != DefaultWidth {
				f.t.Errorf("confirm active %+v: %v", ca, err)
			}
		}
	}
}

func TestActivate(t *testing.T) {
	certs, err := testCerts()
	if err != nil {
		t.Fatal(err)
	}
	trusted := certs[0]
	sync := func(w *wire.Writer) error { return caps.WriteSynchronize(w, 7, 1002, 1006) }
	control := func(action uint16) func(w *wire.Writer) error {
		return func(w *wire.Writer) error { return caps.WriteControl(w, 7, 1002, caps.Control{Action: action}) }
	}
	fontMap := func(w *wire.Writer) error { return caps.WriteFontMap(w, 7, 1002) }
	tests := []struct {
		name   string
		script func(f *fake)
		route  string // "" for an open session
		meta   map[string]any
	}{
		{"open", func(f *fake) {
			f.demandActive()
			f.share(func(w *wire.Writer) error { w.Write([]byte{0x00, 0x80, 0, 0x42, 1, 2, 0xea, 0x03}); return nil }) // FlowPDU
			for _, p := range []func(w *wire.Writer) error{sync, control(caps.ActionCooperate), control(caps.ActionGrantedControl), fontMap} {
				f.share(p)
			}
		}, "", nil},
		{"data PDU instead of demand active", func(f *fake) { f.share(sync) }, "Client.Activate." + TagPDUUnexpected, nil},
		{"disconnected", func(f *fake) {
			f.indication(func(w *wire.Writer) { sec.WriteSetErrorInfo(w, 7, sec.ErrInfoServerShutdown) })
			f.send(true, func(w *wire.Writer) error {
				return mcs.WriteDisconnectProviderUltimatum(w, mcs.ReasonProviderInitiated)
			})
		}, "Client.Activate->Client.readShare." + TagPDUUnexpected, map[string]any{"errorInfo": sec.ErrInfoServerShutdown}},
		{"font map first", func(f *fake) {
			f.demandActive()
			f.share(fontMap)
		}, "Client.Activate." + TagPDUUnexpected, map[string]any{"type2": caps.Type2FontMap}},
		{"granted before cooperate", func(f *fake) {
			f.demandActive()
			f.share(sync)
			f.share(control(caps.ActionGrantedControl))
		}, "Client.Activate." + TagPDUUnexpected, map[string]any{"action": caps.ActionGrantedControl}},
		{"demand active again", func(f *fake) {
			f.demandActive()
			f.share(func(w *wire.Writer) error {
				return caps.WriteDemandActive(w, &caps.DemandActive{ShareID: 7, Source: 1002})
			})
		}, "Client.Activate->Client.readShareData." + TagPDUUnexpected, nil},
		{"share PDU cut", func(f *fake) {
			f.share(func(w *wire.Writer) error { w.Write([]byte{9, 0}); return nil })
		}, "Client.Activate->Client.readShare->ReadPDU." + caps.TagLengthInvalid, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			nc := run(t, trusted.server, func(f *fake) {
				f.joined()
				f.read() // Client Info
				f.indication(sec.WriteLicenseValidClient)
				tt.script(f)
			})
			c, err := Connect(ctx, nc, Options{TLSConfig: trusted.client, User: "u", Password: "pw"})
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Login(ctx); err != nil {
				t.Fatal(err)
			}
			err = c.Activate(ctx)
			if tt.route != "" {
				assertRoute(t, err, tt.route)
				appErr, _ := errors.FromError(err)
				for k, v := range tt.meta {
					if appErr.Metadata[k] != v {
						t.Errorf("%s = %v (%T), want %v (%T)", k, appErr.Metadata[k], appErr.Metadata[k], v, v)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.ShareID != 7 || c.ServerSource != 1002 || c.ServerCaps.Bitmap.DesktopWidth != 640 {
				t.Errorf("share %d source %d caps %+v", c.ShareID, c.ServerSource, c.ServerCaps)
			}
		})
	}
}
