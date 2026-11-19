//go:build darwin && mardp_mac

// These tests use AudioToolbox's AAC encoder and decoder; they need no
// permission, and they play nothing.

package aacenc

import (
	"bytes"
	"math"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/internal/host"
)

// tone is sample i of a 1 kHz tone at a quarter of full scale.
func tone(i int) float32 { return float32(0.25 * math.Sin(2*math.Pi*1000*float64(i)/host.SoundRate)) }

// macPacket is a packet the encoder made.
type macPacket struct {
	data []byte
	t    time.Duration
}

// encodeTone encodes frames of the tone, starting at start, in buffers of
// 480 frames, and then a buffer of silence a second later: the silence
// the gap brings before it brings the whole tone out.
func encodeTone(t *testing.T, e *Encoder, start time.Duration, frames int) []macPacket {
	t.Helper()
	var ps []macPacket
	packet := func(data []byte, ts time.Duration) { ps = append(ps, macPacket{bytes.Clone(data), ts}) }
	b := &host.SoundBuffer{Left: make([]float32, 480), Right: make([]float32, 480)}
	for off := 0; off < frames; off += 480 {
		for i := range b.Left {
			b.Left[i], b.Right[i] = tone(off+i), -tone(off+i)
		}
		b.Time = start + frameTime(off)
		if err := e.Encode(b, packet); err != nil {
			t.Fatal(err)
		}
	}
	clear(b.Left)
	clear(b.Right)
	b.Time = start + frameTime(frames) + time.Second
	if err := e.Encode(b, packet); err != nil {
		t.Fatal(err)
	}
	return ps
}

func TestMacEncode(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := e.Close(); err != nil {
			t.Error(err)
		}
	}()
	bitrate, maxPacket := infoOf(e)
	cookie := cookieOf(e)
	t.Logf("priming %d, bitrate %d, largest packet %d, cookie % x", e.Priming(), bitrate, maxPacket, cookie)
	if e.Priming() != 2112 {
		t.Errorf("priming %d, want 2112", e.Priming())
	}
	if bitrate != Bitrate {
		t.Errorf("bitrate %d, want %d", bitrate, Bitrate)
	}
	if maxPacket <= 0 || maxPacket > maxPacketBytes {
		t.Errorf("largest packet %d, want at most %d", maxPacket, maxPacketBytes)
	}
	if !bytes.Contains(cookie, []byte{0x11, 0x90}) {
		t.Errorf("the cookie % x has no AudioSpecificConfig 11 90 (AAC-LC, 48 kHz, stereo)", cookie)
	}
	const frames = 48000
	start := 3 * time.Second
	ps := encodeTone(t, e, start, frames)
	// The tone, its delay and the silence that brought it out, and the
	// first packet after the gap.
	if want := (frames + 2112 + PacketFrames - 1) / PacketFrames; len(ps) < want {
		t.Fatalf("%d packets, want at least %d", len(ps), want)
	}
	data := []byte{}
	sizes := []int32{}
	total := 0
	for k, p := range ps {
		if len(p.data) == 0 || len(p.data) > maxPacketBytes {
			t.Errorf("packet %d is %d bytes", k, len(p.data))
		}
		if len(p.data) > 1 && p.data[0] == 0xff && p.data[1]&0xf0 == 0xf0 {
			t.Errorf("packet %d starts with an ADTS sync word", k)
		}
		if want := max(start+frameTime(k*PacketFrames-2112), 0); p.t != want {
			t.Errorf("packet %d at %v, want %v", k, p.t, want)
		}
		data = append(data, p.data...)
		sizes = append(sizes, int32(len(p.data)))
		total += len(p.data)
	}
	if rate := total * 8 * host.SoundRate / (len(sizes) * PacketFrames); rate > 2*Bitrate {
		t.Errorf("%d bit/s", rate)
	}
	pcm, err := testDecode(cookie, data, sizes)
	if err != nil {
		t.Fatal(err)
	}
	// The decoded sound has the encoder's delay at its start; past it, it
	// is the tone, left and right.
	var sig, noise float64
	for i := 2048; i < frames-2048; i++ {
		j := 2112 + i
		if 2*j+1 >= len(pcm) {
			t.Fatalf("%d frames decoded", len(pcm)/2)
		}
		l, r := float64(tone(i)), -float64(tone(i))
		sig += l*l + r*r
		dl, dr := float64(pcm[2*j])-l, float64(pcm[2*j+1])-r
		noise += dl*dl + dr*dr
	}
	snr := 10 * math.Log10(sig/noise)
	t.Logf("%d packets, %d bytes, %d frames decoded, %.1f dB", len(ps), total, len(pcm)/2, snr)
	if snr < 30 {
		t.Errorf("the decoded tone is %.1f dB above its noise, want 30", snr)
	}
}

func TestMacCreateFails(t *testing.T) {
	e, err := newAt(1)
	if e != nil {
		_ = e.Close()
		t.Fatal("an encoder at 1 bit/s")
	}
	assertRoute(t, err, "newAt.AACEncoderCreateFailed")
}

func TestMacEncodeFails(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	b := buf(0, 8192)
	_, err = e.conv.encode(b.Left, b.Right, make([]byte, 4), make([]int32, maxPackets))
	assertRoute(t, err, "audioConverter.encode.AACEncodeFailed")
}
