package rdp

import (
	"bytes"
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/rdp/internal/rdpsnd"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

// aacFormat is an AAC format with the extra bytes of a HEAACWAVEINFO and
// an AudioSpecificConfig, as the server offers it in step 4.2.
var aacFormat = AudioFormat{Tag: AudioTagAAC, Channels: 2, SamplesPerSec: 48000, AvgBytesPerSec: 16000,
	BlockAlign: 1, BitsPerSample: 16, Extra: []byte{0, 0, 0xfe, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x11, 0x90}}

// testAudioFormats are what the tests' servers offer, in this order.
var testAudioFormats = []AudioFormat{PCMFormat(48000, 2), aacFormat}

// waitAudio waits until the session's sound is set up and returns its
// format.
func waitAudio(t *testing.T, s *Session) AudioFormat {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if f, ok := s.Audio().Format(); ok {
			return f
		}
		if time.Now().After(deadline) {
			t.Fatal("the sound was not set up")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// waitConfirmed waits until no block is unconfirmed and returns the delay.
func waitConfirmed(t *testing.T, s *Session) time.Duration {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if d, n := s.Audio().Delay(); n == 0 {
			return d
		}
		if time.Now().After(deadline) {
			_, n := s.Audio().Delay()
			t.Fatalf("%d blocks unconfirmed", n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// pick returns the server's formats in the order of the tags given, each
// once.
func pick(tags ...uint16) func([]rdpsnd.Format) []rdpsnd.Format {
	return func(server []rdpsnd.Format) []rdpsnd.Format {
		var out []rdpsnd.Format
		for _, tag := range tags {
			for _, f := range server {
				if f.Tag == tag {
					out = append(out, f)
				}
			}
		}
		return out
	}
}

// TestAudio negotiates each format with clients of version 8 (Wave2) and
// 5 (WaveInfo and Wave): the server takes the first of its formats the
// client has, trains, and sends blocks numbered from 0 that the client
// gets back whole; the client's confirms give Delay; Close ends the sound
// (MS-RDPEA 3.3.5).
func TestAudio(t *testing.T) {
	tests := []struct {
		name     string
		version  uint16
		formats  func([]rdpsnd.Format) []rdpsnd.Format
		want     AudioFormat
		formatNo uint16
	}{
		{"PCM, Wave2", 8, nil, testAudioFormats[0], 0},
		{"AAC, Wave2", 8, pick(AudioTagAAC), aacFormat, 0},
		{"the server's order, the client's index", 8, pick(AudioTagAAC, AudioTagPCM), testAudioFormats[0], 1},
		{"PCM, WaveInfo and Wave", 5, pick(AudioTagPCM), testAudioFormats[0], 0},
		{"AAC, WaveInfo and Wave", 6, pick(AudioTagAAC), aacFormat, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newSessions()
			ts := start(t, Config{Handler: h, AudioFormats: testAudioFormats})
			c, _ := ts.open(t, rdptest.Options{})
			s := h.next(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			a, err := c.NegotiateAudio(ctx, rdptest.AudioOptions{Version: tt.version, Formats: tt.formats, Quality: rdpsnd.QualityHigh})
			if err != nil {
				t.Fatal(err)
			}
			if a.Server.Version != rdpsnd.VersionWave2 || a.Server.LastBlock != 0xff || len(a.Server.Formats) != 2 {
				t.Errorf("server formats: version %d last block %d, %d formats", a.Server.Version, a.Server.LastBlock, len(a.Server.Formats))
			}
			if a.Training.Type != rdpsnd.MsgTraining || a.Training.PackSize != 0 {
				t.Errorf("training %+v", a.Training)
			}
			got := waitAudio(t, s)
			if !got.wire().Equal(tt.want.wire()) {
				t.Errorf("format %+v, want %+v", got, tt.want)
			}
			if tt.version >= rdpsnd.VersionQualityMode {
				ts.log.find(t, "sound ready")
			}
			rng := rand.New(rand.NewPCG(1, uint64(tt.version)))
			for i, n := range []int{5, 1920, 7000} {
				data := make([]byte, n)
				for j := range data {
					data[j] = byte(rng.Uint32())
				}
				if err := s.Audio().Write(data, time.Duration(100+i*10)*time.Millisecond); err != nil {
					t.Fatal(err)
				}
				b, err := a.ReadBlock(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if b.Block != uint8(i) || b.FormatNo != tt.formatNo || b.Wave2 != (tt.version >= rdpsnd.VersionWave2) {
					t.Errorf("block %d: number %d format %d wave2 %v", i, b.Block, b.FormatNo, b.Wave2)
				}
				if b.Wave2 && b.AudioTimeStamp != uint32(100+i*10) {
					t.Errorf("block %d: audio time stamp %d", i, b.AudioTimeStamp)
				}
				if !bytes.Equal(b.Data, data) {
					t.Errorf("block %d: %d bytes back, not the %d sent", i, len(b.Data), len(data))
				}
				if _, n := s.Audio().Delay(); n != 1 {
					t.Errorf("block %d: %d unconfirmed, want 1", i, n)
				}
				time.Sleep(3 * time.Millisecond)
				if err := a.Confirm(ctx, b, 20*time.Millisecond); err != nil {
					t.Fatal(err)
				}
				if d := waitConfirmed(t, s); d < 3*time.Millisecond {
					t.Errorf("block %d: delay %v, want at least the 3ms before the confirm", i, d)
				}
			}
			if err := s.Audio().Close(); err != nil {
				t.Fatal(err)
			}
			p, err := a.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if p.Type != rdpsnd.MsgClose {
				t.Errorf("type %#x after Close, want Close", p.Type)
			}
			assertRoute(t, s.Audio().Write([]byte{1, 2, 3, 4, 5}, 0), "Audio.Write."+TagAudioUnavailable)
			if _, ok := s.Audio().Format(); ok {
				t.Error("a format after Close")
			}
			if err := s.Audio().Close(); err != nil {
				t.Errorf("a second Close: %v", err)
			}
		})
	}
}

// TestAudioUnavailable: a session without sound has no format, and Write
// fails with TagAudioUnavailable, until the channel is set up and after
// the client says it plays nothing or has none of the server's formats.
func TestAudioUnavailable(t *testing.T) {
	other := rdpsnd.Format{Tag: AudioTagPCM, Channels: 1, SamplesPerSec: 8000, AvgBytesPerSec: 16000, BlockAlign: 2, BitsPerSample: 16}
	tests := []struct {
		name    string
		formats []AudioFormat
		opts    rdptest.Options
		// audio negotiates with these options, when set.
		audio *rdptest.AudioOptions
		log   string
	}{
		{"no rdpsnd", testAudioFormats, rdptest.Options{Channels: []string{"drdynvc"}}, nil, ""},
		{"no formats", nil, rdptest.Options{}, nil, ""},
		{"plays nothing", testAudioFormats, rdptest.Options{}, &rdptest.AudioOptions{Flags: rdpsnd.CapsVolume},
			"no sound: the client plays none"},
		{"no common format", testAudioFormats, rdptest.Options{},
			&rdptest.AudioOptions{Formats: func([]rdpsnd.Format) []rdpsnd.Format { return []rdpsnd.Format{other} }, NoTraining: true},
			"no sound: the client has none of the server's formats"},
		{"no formats at all", testAudioFormats, rdptest.Options{}, &rdptest.AudioOptions{Formats: pick()},
			"no sound: the client has none of the server's formats"},
		{"training unconfirmed", testAudioFormats, rdptest.Options{}, &rdptest.AudioOptions{NoTraining: true}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newSessions()
			ts := start(t, Config{Handler: h, AudioFormats: tt.formats})
			c, _ := ts.open(t, tt.opts)
			s := h.next(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if tt.audio != nil {
				if _, err := c.NegotiateAudio(ctx, *tt.audio); err != nil {
					t.Fatal(err)
				}
			}
			if tt.log != "" {
				ts.log.find(t, tt.log)
			}
			if _, ok := s.Audio().Format(); ok {
				t.Error("a format")
			}
			assertRoute(t, s.Audio().Write(make([]byte, 100), 0), "Audio.Write."+TagAudioUnavailable)
			if err := s.Audio().Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		})
	}
}

// TestAudioClientUnavailable: the client's sound helpers need rdpsnd.
func TestAudioClientUnavailable(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h, AudioFormats: testAudioFormats})
	c, _ := ts.open(t, rdptest.Options{Channels: []string{}})
	h.next(t)
	_, err := c.NegotiateAudio(context.Background(), rdptest.AudioOptions{})
	assertRoute(t, err, "Client.NegotiateAudio."+rdptest.TagAudioUnavailable)
}

// TestAudioIgnores: malformed PDUs, PDUs out of turn and confirms of
// blocks never sent are ignored; the sound goes on (MS-RDPEA 3.3.5.2).
func TestAudioIgnores(t *testing.T) {
	h := newSessions()
	ts := start(t, Config{Handler: h, AudioFormats: testAudioFormats})
	c, _ := ts.open(t, rdptest.Options{})
	s := h.next(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// A Training Confirm of another Training first: the sound waits for
	// the right one.
	a, err := c.NegotiateAudio(ctx, rdptest.AudioOptions{NoTraining: true})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := a.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	send := func(msg []byte) {
		t.Helper()
		if err := c.SendChannel(ctx, c.ChannelID("rdpsnd"), msg); err != nil {
			t.Fatal(err)
		}
	}
	send([]byte{rdpsnd.MsgTraining, 0, 4, 0, byte(tr.TimeStamp + 1), byte((tr.TimeStamp + 1) >> 8), 0, 0})
	ts.log.find(t, "a Training Confirm of another Training ignored")
	if _, ok := s.Audio().Format(); ok {
		t.Fatal("sound set up by another Training's confirm")
	}
	send([]byte{rdpsnd.MsgTraining, 0, 4, 0, byte(tr.TimeStamp), byte(tr.TimeStamp >> 8), 0, 0})
	waitAudio(t, s)
	for _, msg := range [][]byte{
		{rdpsnd.MsgWaveConfirm, 0, 0xff, 0xff},             // a body longer than the PDU
		{0x42, 0, 0, 0},                                    // an unknown type
		{rdpsnd.MsgWaveConfirm, 0, 4, 0, 0, 0, 7, 0},       // a block never sent
		{rdpsnd.MsgFormats, 0, 0, 0},                       // formats out of turn, and malformed
		{rdpsnd.MsgTraining, 0, 4, 0, 0, 0, 0, 0},          // training out of turn
		{rdpsnd.MsgQualityMode, 0, 4, 0, 2, 0, 0, 0},       // quality mode, kept
		{rdpsnd.MsgWaveConfirm, 0, 4, 0, 0, 0, 0, 0, 0xff}, // a byte after the body
	} {
		send(msg)
	}
	ts.log.find(t, "a sound PDU ignored")
	ts.log.find(t, "a Wave Confirm of no block ignored")
	ts.log.find(t, "a sound PDU out of turn ignored")
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	if err := s.Audio().Write(data, 0); err != nil {
		t.Fatal(err)
	}
	b, err := a.ReadBlock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if b.Block != 0 || !bytes.Equal(b.Data, data) {
		t.Errorf("block %d data %v", b.Block, b.Data)
	}
	if err := a.Confirm(ctx, b, 0); err != nil {
		t.Fatal(err)
	}
	waitConfirmed(t, s)
}

// TestNewServerAudioFormats: formats that do not fit one PDU are refused.
func TestNewServerAudioFormats(t *testing.T) {
	big := PCMFormat(44100, 2)
	big.Extra = make([]byte, 0xfff0)
	_, err := NewServer(Config{Handler: nopHandler, Certificate: certs(t).server, Authenticator: StaticAuthenticator(testUser, testPassword),
		AudioFormats: []AudioFormat{big, big}})
	assertRoute(t, err, "NewServer->AppendServerFormats->appendFormats."+rdpsnd.TagFormatsInvalid)
}

// TestAudioWriteAllocs: Write allocates nothing once its buffers have grown
// (SPEC §11).
func TestAudioWriteAllocs(t *testing.T) {
	for _, version := range []uint16{rdpsnd.VersionWave2, 5} {
		sig := make(signal)
		wr := newWriter(sig, func(error) {})
		go wr.run()
		s := &Session{wr: wr, client: Client{FastPathOutput: true, MaxRequestSize: 0x3f0000}}
		a := s.Audio()
		a.s, a.ch, a.state, a.version = s, &staticChannel{name: "rdpsnd", id: 1004}, audioReady, version
		data := make([]byte, 3840)
		var err error
		for range controlDepth {
			if err = a.Write(data, time.Second); err != nil {
				t.Fatal(err)
			}
			<-sig
		}
		n := testing.AllocsPerRun(100, func() {
			if err = a.Write(data, time.Second); err == nil {
				<-sig
			}
		})
		wr.halt()
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("version %d: %v allocations per Write, want 0", version, n)
		}
	}
}
