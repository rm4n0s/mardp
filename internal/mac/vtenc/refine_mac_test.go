//go:build darwin && mardp_mac

package vtenc

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/internal/mac/vtdec"
)

// psnr is the peak signal-to-noise ratio of got against want, in dB, over
// red, green and blue: about 30 is a soft picture, 40 a clean one.
func psnr(got, want *image.RGBA) float64 {
	var sum float64
	n := 0
	for i := 0; i+3 < len(want.Pix) && i+3 < len(got.Pix); i += 4 {
		for c := range 3 {
			d := float64(got.Pix[i+c]) - float64(want.Pix[i+c])
			sum += d * d
			n++
		}
	}
	if sum == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(255*255/(sum/float64(n)))
}

// savePicture writes the part r of img as name.png into the folder
// MARDP_PICTURES names, when it is set.
func savePicture(t *testing.T, name string, img *image.RGBA, r image.Rectangle) {
	t.Helper()
	dir := os.Getenv("MARDP_PICTURES")
	if dir == "" {
		return
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img.SubImage(r)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".png"), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestMacRefine is the picture proof of the sharpening (SPEC §9.3), with
// the real VideoToolbox and no permission. The screen shows a page of
// text, then a second of pages that each differ wholly from the one
// before, 60 a second, as text that scrolls fast or a window that is
// dragged; then it stops on a page. The client's decoder shows what it has
// when the screen stops, which is all a client had before the sharpening,
// and what it has once the encoder has stopped sending. MARDP_PICTURES
// names a folder for the pictures.
func TestMacRefine(t *testing.T) {
	for _, bitrate := range []int{8_000_000, 2_000_000} {
		t.Run(fmt.Sprintf("%d Mbit", bitrate/1_000_000), func(t *testing.T) {
			ch := make(chan encoded, 4096)
			e, err := New(Options{Width: 1920, Height: 1080, FPS: 60, Bitrate: bitrate, FullRange: true}, func(f *Frame) {
				ch <- encoded{data: bytes.Clone(f.Data), key: f.Key, pts: f.PTS}
			})
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			dec := vtdec.New()
			defer dec.Close()

			// drain decodes the frames that come until none came for
			// quiet, and returns the picture after the last (shown when
			// none came), how many came and their bytes.
			drain := func(shown *image.RGBA, quiet time.Duration) (last *image.RGBA, frames, size int) {
				t.Helper()
				last = shown
				for {
					select {
					case f := <-ch:
						img, err := dec.Decode(f.data)
						if err != nil {
							t.Fatal(err)
						}
						last = img
						frames++
						size += len(f.data)
					case <-time.After(quiet):
						return last, frames, size
					}
				}
			}
			if st := e.SubmitText(0); st != 0 {
				t.Fatalf("the first page: status %d", st)
			}
			shown, frames, size := drain(nil, 1500*time.Millisecond)
			if shown == nil {
				t.Fatal("no frame of the first page")
			}
			t.Logf("the first page: %d frames, %d bytes", frames, size)

			// A second of motion, then the screen stops.
			const pages = 60
			want := TextPage(1920, 1080, pages)
			if want == nil {
				t.Fatal("no page")
			}
			next := time.Now()
			for page := 1; page <= pages; page++ {
				if st := e.SubmitText(uint32(page)); st != 0 {
					t.Fatalf("page %d: status %d", page, st)
				}
				next = next.Add(time.Second / 60)
				time.Sleep(time.Until(next))
			}
			// Before the first repeat, a tenth of a second after the
			// last page: what the client has as the screen stops.
			before, frames, size := drain(shown, refineEvery/2)
			moved := e.Stats()
			after, more, moreSize := drain(before, 1500*time.Millisecond)
			took := time.Since(next) - 1500*time.Millisecond
			st := e.Stats()
			pb, pa := psnr(before, want), psnr(after, want)
			t.Logf("the motion: %d pages, %d frames, %d bytes (%d dropped by VideoToolbox)", pages, frames, size, moved.Dropped)
			t.Logf("the still screen after it: %d more frames, %d bytes, all repeats, over %v", more, moreSize, took.Round(100*time.Millisecond))
			t.Logf("against the screen: %.1f dB as the screen stops, %.1f dB once the encoder stopped", pb, pa)
			part := image.Rect(0, 0, 960, 220)
			tag := fmt.Sprintf("%dmbit", bitrate/1_000_000)
			savePicture(t, tag+"-0-the-screen", want, part)
			savePicture(t, tag+"-1-as-the-screen-stops", before, part)
			savePicture(t, tag+"-2-once-sharpened", after, part)
			if pa < 38 {
				t.Errorf("the still picture is %.1f dB against the screen, want at least 38: letters are not clean", pa)
			}
			if st.Repeats == 0 || more == 0 {
				t.Error("the encoder did not encode the still picture again")
			}
			// And it stops: nothing more comes.
			select {
			case f := <-ch:
				t.Errorf("a frame of %d bytes after the sharpening ended", len(f.data))
			case <-time.After(time.Second):
			}
		})
	}
}
