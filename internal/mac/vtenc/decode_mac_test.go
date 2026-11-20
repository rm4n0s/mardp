//go:build darwin && mardp_mac

package vtenc

import (
	"bytes"
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/internal/mac/vtdec"
)

// TestMacDecode: what the encoder makes of a page of text, the decoder
// turns back into that page; a frame without a picture and a frame that
// is not one are refused with their tags.
func TestMacDecode(t *testing.T) {
	e, ch := testEncoder(t, Options{Width: 1280, Height: 720, FPS: 60, Bitrate: 8_000_000, FullRange: true})
	if st := e.SubmitText(3); st != 0 {
		t.Fatalf("status %d", st)
	}
	key := <-ch
	if st := e.SubmitText(4); st != 0 {
		t.Fatalf("status %d", st)
	}
	delta := <-ch
	d := vtdec.New()
	defer d.Close()
	img, err := d.Decode(key.data)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 1280 || b.Dy() != 720 {
		t.Fatalf("a picture of %v", b)
	}
	if db := psnr(img, TextPage(1280, 720, 3)); db < 30 {
		t.Errorf("the key frame decodes to %.1f dB against its page", db)
	}
	if img, err = d.Decode(delta.data); err != nil {
		t.Fatal(err)
	}
	if db, other := psnr(img, TextPage(1280, 720, 4)), psnr(img, TextPage(1280, 720, 3)); db < 30 || db <= other {
		t.Errorf("the second frame decodes to %.1f dB against its page, %.1f against the page before", db, other)
	}

	route := func(err error, want string) {
		t.Helper()
		ae, ok := errors.FromError(err)
		if !ok || !ae.HasRoute(want) {
			t.Errorf("error %v, want the route %s", err, want)
		}
	}
	// The parameter sets alone: no picture.
	end := bytes.Index(key.data[4:], []byte{0, 0, 0, 1, 0x65})
	if end < 0 {
		end = bytes.Index(key.data[4:], []byte{0, 0, 0, 1, 0x25})
	}
	if end < 0 {
		t.Fatal("no picture in the key frame")
	}
	_, err = d.Decode(key.data[:end+4])
	route(err, "Decoder.Decode.DecoderFrameInvalid")
	// A slice that is none.
	_, err = d.Decode([]byte{0, 0, 0, 1, 0x65, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	route(err, "Decoder.Decode.DecoderFailed")
}
