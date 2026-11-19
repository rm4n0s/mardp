package host

import (
	"context"
	"encoding/binary"
	"log/slog"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// pcmPacket is a packet the encoder made.
type pcmPacket struct {
	data []byte
	t    time.Duration
}

// encodeAll encodes the buffers and returns the packets.
func encodeAll(t *testing.T, bufs []SoundBuffer) []pcmPacket {
	t.Helper()
	e := NewPCMEncoder()
	var out []pcmPacket
	for i := range bufs {
		err := e.Encode(&bufs[i], func(data []byte, at time.Duration) {
			out = append(out, pcmPacket{data: append([]byte(nil), data...), t: at})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// ramp is a buffer of n frames at at, whose frame i is (first+i)/32767 on
// the left and its negative on the right.
func ramp(first, n int, at time.Duration) SoundBuffer {
	b := SoundBuffer{Left: make([]float32, n), Right: make([]float32, n), Time: at}
	for i := range n {
		v := float32(first+i) / math.MaxInt16
		b.Left[i], b.Right[i] = v, -v
	}
	return b
}

// frameAt returns the left and right samples of frame i of p.
func frameAt(p []byte, i int) (l, r int16) {
	return int16(binary.LittleEndian.Uint16(p[4*i:])), int16(binary.LittleEndian.Uint16(p[4*i+2:]))
}

// TestPCMEncoder: buffers of any length become packets of 20 ms, frames
// interleaved, each with its first frame's time; a gap pads the packet
// being filled with silence; samples are rounded and clipped.
func TestPCMEncoder(t *testing.T) {
	type want struct {
		t time.Duration
		// first is the ramp value of the packet's first frame; frames
		// from silence on are zero.
		first, silence int
	}
	tests := []struct {
		name string
		bufs []SoundBuffer
		want []want
	}{
		{"one packet a buffer", []SoundBuffer{ramp(0, 960, 0), ramp(960, 960, 20*time.Millisecond)},
			[]want{{0, 0, 960}, {20 * time.Millisecond, 960, 960}}},
		{"buffers across packets", []SoundBuffer{ramp(0, 441, time.Second), ramp(441, 441, time.Second+frameTime(441)),
			ramp(882, 441, time.Second+frameTime(882)), ramp(1323, 441, time.Second+frameTime(1323)),
			ramp(1764, 441, time.Second+frameTime(1764))},
			[]want{{time.Second, 0, 960}, {time.Second + 20*time.Millisecond, 960, 960}}},
		{"jitter under 10 ms is no gap", []SoundBuffer{ramp(0, 480, 0), ramp(480, 480, 13*time.Millisecond)},
			[]want{{0, 0, 960}}},
		{"a gap pads", []SoundBuffer{ramp(0, 480, 0), ramp(480, 960, 100*time.Millisecond), ramp(1440, 960, 120*time.Millisecond)},
			[]want{{0, 0, 480}, {100 * time.Millisecond, 480, 960}, {120 * time.Millisecond, 1440, 960}}},
		{"back in time pads", []SoundBuffer{ramp(0, 480, time.Second), ramp(480, 960, 0)},
			[]want{{time.Second, 0, 480}, {0, 480, 960}}},
		{"channels of different lengths", []SoundBuffer{{Left: make([]float32, 960), Right: make([]float32, 1000)}},
			[]want{{0, 0, 0}}},
		{"empty buffers", []SoundBuffer{{}, {Left: make([]float32, 5)}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := encodeAll(t, tt.bufs)
			if len(got) != len(tt.want) {
				t.Fatalf("%d packets, want %d", len(got), len(tt.want))
			}
			for i, w := range tt.want {
				p := got[i]
				if len(p.data) != 3840 || p.t != w.t {
					t.Errorf("packet %d: %d bytes at %v, want 3840 at %v", i, len(p.data), p.t, w.t)
					continue
				}
				for f := range 960 {
					l, r := frameAt(p.data, f)
					wl := int16(w.first + f)
					if f >= w.silence {
						wl = 0
					}
					if l != wl || r != -wl {
						t.Errorf("packet %d frame %d: %d %d, want %d %d", i, f, l, r, wl, -wl)
						break
					}
				}
			}
		})
	}
}

// TestPCMSample: rounding to the nearest step, clipping at full scale.
func TestPCMSample(t *testing.T) {
	for _, tt := range []struct {
		v    float32
		want int16
	}{{0, 0}, {1, 32767}, {-1, -32767}, {2, 32767}, {-3, -32767}, {0.5, 16384}, {-0.5, -16384},
		{1.4 / 32767, 1}, {1.6 / 32767, 2}, {float32(math.NaN()), 0}} {
		if got := pcmSample(tt.v); got != tt.want {
			t.Errorf("pcmSample(%v) = %d, want %d", tt.v, got, tt.want)
		}
	}
}

// TestPCMEncoderAllocs: encoding allocates nothing.
func TestPCMEncoderAllocs(t *testing.T) {
	e := NewPCMEncoder()
	b := ramp(0, 441, 0)
	n := 0
	allocs := testing.AllocsPerRun(200, func() {
		b.Time += frameTime(441)
		if err := e.Encode(&b, func([]byte, time.Duration) { n++ }); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 || n == 0 {
		t.Errorf("%v allocations a buffer, %d packets", allocs, n)
	}
}

// fakeSoundOut is a soundOut whose backlog the test sets.
type fakeSoundOut struct {
	oldest time.Duration
	err    error
	writes []time.Duration
}

func (o *fakeSoundOut) Write(_ []byte, t time.Duration) error {
	if o.err != nil {
		return o.err
	}
	o.writes = append(o.writes, t)
	return nil
}

func (o *fakeSoundOut) Backlog() (int, time.Duration) { return 1, o.oldest }

// TestSoundPath: the gap rule drops sound from when the oldest unconfirmed
// block is older than 500 ms until it is younger than 100 ms; a full queue
// drops the packet; a session without sound stops the capture.
func TestSoundPath(t *testing.T) {
	type step struct {
		oldest time.Duration
		err    error
		sent   bool
	}
	full := errors.New(rdp.TagQueueFull, "full")
	gone := errors.New(rdp.TagAudioUnavailable, "gone")
	tests := []struct {
		name    string
		steps   []step
		stopped bool
		logs    []string
	}{
		{"under the limit", []step{{0, nil, true}, {499 * time.Millisecond, nil, true}, {500 * time.Millisecond, nil, true}}, false, nil},
		{"drops and recovers", []step{
			{100 * time.Millisecond, nil, true}, {501 * time.Millisecond, nil, false}, {300 * time.Millisecond, nil, false},
			{100 * time.Millisecond, nil, false}, {99 * time.Millisecond, nil, true}, {400 * time.Millisecond, nil, true},
		}, false, []string{"the sound is behind: dropping it", "sound resumed"}},
		{"a full queue", []step{{0, full, false}, {0, full, false}, {0, nil, true}}, false,
			[]string{"sound packets dropped: the queue was full"}},
		{"no sound anymore", []step{{0, nil, true}, {0, gone, false}}, true, []string{"the sound stopped"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{}
			out := &fakeSoundOut{}
			stopped := false
			p := &soundPath{out: out, enc: NewPCMEncoder(), log: slog.New(rec), stop: func() { stopped = true }, length: PCMPacket}
			for i, s := range tt.steps {
				out.oldest, out.err = s.oldest, s.err
				before := len(out.writes)
				p.packet(make([]byte, 3840), time.Duration(i)*PCMPacket)
				if sent := len(out.writes) > before; sent != s.sent {
					t.Errorf("step %d: sent %v, want %v", i, sent, s.sent)
				}
			}
			if stopped != tt.stopped {
				t.Errorf("stopped %v, want %v", stopped, tt.stopped)
			}
			for _, msg := range tt.logs {
				if len(rec.find(msg)) != 1 {
					t.Errorf("logged %q %d times, want once", msg, len(rec.find(msg)))
				}
			}
			if r := rec.find("sound resumed"); len(r) == 1 {
				if n := attr(r[0], "dropped").Int64(); n != 3 {
					t.Errorf("resumed after %d dropped, want 3", n)
				}
			}
		})
	}
}

// toneSample is the 16-bit sample of frame k of a ToneSource at freq.
func toneSample(freq float64, k int) int16 {
	return pcmSample(float32(0.25 * math.Sin(2*math.Pi*freq*float64(k)/SoundRate)))
}

// checkTone fails t unless b holds the 20 ms of ToneSource at freq that
// its audio time stamp says.
func checkTone(t *testing.T, b rdptest.AudioBlock, freq float64) {
	t.Helper()
	if len(b.Data) != 3840 {
		t.Fatalf("block %d: %d bytes, want 3840", b.Block, len(b.Data))
	}
	first := int(b.AudioTimeStamp) * SoundRate / 1000
	for f := range 960 {
		l, r := frameAt(b.Data, f)
		if w := toneSample(freq, first+f); l != w || r != w {
			t.Fatalf("block %d at %d ms, frame %d: %d %d, want %d", b.Block, b.AudioTimeStamp, f, l, r, w)
		}
	}
}

// TestSessionSound runs a session with ToneSource and a client that
// confirms at once, then stops confirming for 400 ms of sound and a 400 ms
// stall, then confirms again (SPEC §10.1): the sound arrives in 20 ms PCM
// blocks whose samples are exactly the tone's at their time stamps; the
// stalled client's backlog passes 500 ms and the host drops sound, once,
// until the client has caught up, which leaves a gap in the time stamps.
func TestSessionSound(t *testing.T) {
	const freq = 1000
	s := serve(t, Parts{Screen: &PatternScreen{Frames: 1}, Sound: &ToneSource{Frequency: freq}}, Options{}, 640, 480)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a, err := s.c.NegotiateAudio(ctx, rdptest.AudioOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if f, w := a.Server.Formats, PCMFormat(); len(f) != 1 || f[0].Tag != w.Tag || f[0].Channels != 2 ||
		f[0].SamplesPerSec != SoundRate || f[0].BitsPerSample != 16 || f[0].BlockAlign != 4 {
		t.Fatalf("server formats %+v", f)
	}
	read := func() rdptest.AudioBlock {
		t.Helper()
		b, err := a.ReadBlock(ctx)
		if err != nil {
			t.Fatal(err)
		}
		checkTone(t, b, freq)
		return b
	}
	confirm := func(b rdptest.AudioBlock) {
		t.Helper()
		if err := a.Confirm(ctx, b, 20*time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	var stamps []uint32
	for range 25 {
		b := read()
		stamps = append(stamps, b.AudioTimeStamp)
		confirm(b)
	}
	var held []rdptest.AudioBlock
	for range 20 {
		b := read()
		stamps = append(stamps, b.AudioTimeStamp)
		held = append(held, b)
	}
	time.Sleep(400 * time.Millisecond)
	for _, b := range held {
		confirm(b)
		time.Sleep(20 * time.Millisecond)
	}
	for range 40 {
		b := read()
		stamps = append(stamps, b.AudioTimeStamp)
		confirm(b)
	}
	gaps := 0
	for i := 1; i < len(stamps); i++ {
		switch d := stamps[i] - stamps[i-1]; {
		case d == 20:
		case d > 20:
			gaps++
			if d < 300 {
				t.Errorf("a gap of %d ms after %d ms, want the drop's", d, stamps[i-1])
			}
		default:
			t.Errorf("time stamps %d then %d", stamps[i-1], stamps[i])
		}
	}
	if gaps != 1 {
		t.Errorf("%d gaps in the sound, want 1", gaps)
	}
	if n := len(s.rec.find("the sound is behind: dropping it")); n != 1 {
		t.Errorf("dropping logged %d times, want once", n)
	}
	if r := s.rec.find("sound resumed"); len(r) != 1 || attr(r[0], "dropped").Int64() < 10 {
		t.Errorf("resumed: %v", r)
	}
	if len(s.rec.find("sending sound")) != 1 {
		t.Error("no log of the sound starting")
	}
}

// countingSound is a sound source that counts its captures and plays
// silence.
type countingSound struct{ started atomic.Int64 }

func (c *countingSound) Capture(ctx context.Context, _ func(*SoundBuffer)) error {
	c.started.Add(1)
	<-ctx.Done()
	return nil
}

// TestSessionNoSound: a client without rdpsnd gets no sound, and the
// capture never starts.
func TestSessionNoSound(t *testing.T) {
	src := &countingSound{}
	tests := []struct {
		name string
		opts rdptest.AudioOptions
		log  string
	}{
		{"plays nothing", rdptest.AudioOptions{Flags: 2}, "no sound for this session"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := serve(t, Parts{Screen: &PatternScreen{Frames: 1}, Sound: src}, Options{}, 640, 480)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := s.c.NegotiateAudio(ctx, tt.opts); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for len(s.rec.find(tt.log)) == 0 {
				if time.Now().After(deadline) {
					t.Fatalf("no log %q", tt.log)
				}
				time.Sleep(5 * time.Millisecond)
			}
			if src.started.Load() != 0 {
				t.Error("the sound capture started")
			}
		})
	}
}

// fakeAAC is an AAC encoder of the tests: a packet of each 1024 frames,
// "AAC" and its number.
type fakeAAC struct {
	pending int
	packets int
	closed  atomic.Bool
}

func (f *fakeAAC) Encode(b *SoundBuffer, packet func(data []byte, t time.Duration)) error {
	f.pending += min(len(b.Left), len(b.Right))
	for ; f.pending >= 1024; f.pending -= 1024 {
		packet([]byte{'A', 'A', 'C', byte(f.packets)}, frameTime(f.packets*1024))
		f.packets++
	}
	return nil
}

func (f *fakeAAC) Close() error {
	f.closed.Store(true)
	return nil
}

func TestAudioFormats(t *testing.T) {
	aac := func() (SoundEncoder, error) { return &fakeAAC{}, nil }
	tests := []struct {
		name  string
		parts Parts
		want  []rdp.AudioFormat
	}{
		{"no sound", Parts{AAC: aac}, nil},
		{"PCM", Parts{Sound: &ToneSource{}}, []rdp.AudioFormat{PCMFormat()}},
		{"AAC and PCM", Parts{Sound: &ToneSource{}, AAC: aac}, []rdp.AudioFormat{AACFormat(), PCMFormat()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.parts.Screen = &PatternScreen{}
			h, err := New(tt.parts, Options{})
			if err != nil {
				t.Fatal(err)
			}
			got := h.AudioFormats()
			if len(got) != len(tt.want) {
				t.Fatalf("formats %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i].Tag != tt.want[i].Tag || string(got[i].Extra) != string(tt.want[i].Extra) ||
					got[i].AvgBytesPerSec != tt.want[i].AvgBytesPerSec {
					t.Errorf("format %d %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
	if f := AACFormat(); f.SamplesPerSec != SoundRate || f.Channels != 2 || f.AvgBytesPerSec != AACBitrate/8 {
		t.Errorf("AAC format %+v", f)
	}
}

// TestSessionAAC: a client that takes AAC gets the AAC encoder's packets,
// one a block, and the encoder is closed when the session ends; a client
// that takes PCM alone gets PCM; a host whose AAC encoder fails to start
// sends nothing.
func TestSessionAAC(t *testing.T) {
	tests := []struct {
		name    string
		formats []uint16
		fail    bool
		want    uint16
	}{
		{"AAC and PCM", nil, false, rdp.AudioTagAAC},
		{"AAC alone", []uint16{rdp.AudioTagAAC}, false, rdp.AudioTagAAC},
		{"PCM alone", []uint16{rdp.AudioTagPCM}, false, rdp.AudioTagPCM},
		{"no AAC encoder", nil, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var made atomic.Pointer[fakeAAC]
			aac := func() (SoundEncoder, error) {
				if tt.fail {
					return nil, errors.New("FakeAACFailed", "no encoder")
				}
				f := &fakeAAC{}
				made.Store(f)
				return f, nil
			}
			src := &ToneSource{Frequency: 1000}
			s := serve(t, Parts{Screen: &PatternScreen{Frames: 1}, Sound: src, AAC: aac}, Options{}, 640, 480)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			opts := rdptest.AudioOptions{}
			if tt.formats != nil {
				opts.Formats = rdptest.AudioTags(tt.formats...)
			}
			a, err := s.c.NegotiateAudio(ctx, opts)
			if err != nil {
				t.Fatal(err)
			}
			if f := a.Server.Formats; len(f) != 2 || f[0].Tag != rdp.AudioTagAAC || f[1].Tag != rdp.AudioTagPCM {
				t.Fatalf("server formats %+v, want AAC then PCM", f)
			}
			if tt.fail {
				deadline := time.Now().Add(5 * time.Second)
				for len(s.rec.find("no AAC encoder")) == 0 {
					if time.Now().After(deadline) {
						t.Fatal("no log of the failed encoder")
					}
					time.Sleep(5 * time.Millisecond)
				}
				if len(s.rec.find("sending sound")) != 0 {
					t.Error("sound sent")
				}
				return
			}
			for k := range 10 {
				b, err := a.ReadBlock(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if tag := a.Formats[b.FormatNo].Tag; tag != tt.want {
					t.Fatalf("block %d in format %#x, want %#x", k, tag, tt.want)
				}
				if tt.want == rdp.AudioTagAAC {
					if string(b.Data) != string([]byte{'A', 'A', 'C', byte(k)}) {
						t.Errorf("block %d is % x", k, b.Data)
					}
				} else {
					checkTone(t, b, 1000)
				}
				if err := a.Confirm(ctx, b, 20*time.Millisecond); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.c.Close(); err != nil {
				t.Fatal(err)
			}
			f := made.Load()
			if (f != nil) != (tt.want == rdp.AudioTagAAC) {
				t.Fatalf("an AAC encoder made: %v", f != nil)
			}
			if f == nil {
				return
			}
			deadline := time.Now().Add(5 * time.Second)
			for !f.closed.Load() {
				if time.Now().After(deadline) {
					t.Fatal("the AAC encoder was not closed")
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}
