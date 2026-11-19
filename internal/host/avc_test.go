package host

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp"
	"github.com/rm4n0s/mardp/rdp/rdptest"
)

func TestRateControl(t *testing.T) {
	tests := []struct {
		name    string
		bitrate int
		sent    int
		skipped bool
		oldest  time.Duration
		want    int
	}{
		{"skipped: a quarter less", 8_000_000, 60, true, 0, 6_000_000},
		{"slow acknowledgements: a quarter less", 8_000_000, 60, false, slowAck + time.Millisecond, 6_000_000},
		{"never below the floor", 1_200_000, 60, true, 0, MinBitrate},
		{"fast: a sixteenth more", 8_000_000, 60, false, fastAck - time.Millisecond, 8_500_000},
		{"never above the cap", 19_500_000, 60, false, 0, 20_000_000},
		{"nothing sent: the same", 8_000_000, 0, false, 0, 8_000_000},
		{"between: the same", 8_000_000, 60, false, fastAck, 8_000_000},
		{"at the slow limit: the same", 8_000_000, 60, false, slowAck, 8_000_000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := rateControl{bitrate: tt.bitrate, max: DefaultMaxBitrate}
			if got := r.update(tt.sent, tt.skipped, tt.oldest); got != tt.want || r.bitrate != tt.want {
				t.Errorf("update = %d, want %d", got, tt.want)
			}
		})
	}
}

// fakeOut is a videoOut whose backlog, path and SendFrame result the test
// sets.
type fakeOut struct {
	mu      sync.Mutex
	backlog rdp.Backlog
	path    rdp.VideoPath
	err     error
	sent    []bool // key flags
	reads   int    // Backlog calls
}

func (o *fakeOut) SendFrame(_ []byte, key bool) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.err != nil {
		return o.err
	}
	o.sent = append(o.sent, key)
	return nil
}

func (o *fakeOut) Backlog() rdp.Backlog {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reads++
	return o.backlog
}

func (o *fakeOut) Path() rdp.VideoPath {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.path
}

func (o *fakeOut) SetWriteHook(func(rdp.VideoWrite)) {}

func (o *fakeOut) set(f func(o *fakeOut)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	f(o)
}

// fakeEncoder records what the session asks of the encoder.
type fakeEncoder struct {
	skip     bool
	skips    int // SetSkip calls
	forced   int
	bitrates []int
	err      error
}

func (e *fakeEncoder) SetBitrate(b int) error {
	e.bitrates = append(e.bitrates, b)
	return e.err
}
func (e *fakeEncoder) ForceKeyFrame() { e.forced++ }
func (e *fakeEncoder) SetSkip(skip bool) {
	e.skip = skip
	e.skips++
}

func TestAVCPath(t *testing.T) {
	out := &fakeOut{path: rdp.VideoAVC420}
	stops := 0
	rec := &recorder{}
	a := &avcPath{out: out, log: slog.New(rec), rate: &rateControl{bitrate: StartBitrate, max: DefaultMaxBitrate}, stop: func() { stops++ }}
	frames := 0
	a.onFrame = func() { frames++ }
	f := &EncodedFrame{Data: []byte{0, 0, 0, 1, 5}, Key: true}

	// Before the encoder exists, nothing happens.
	a.frame(f)
	a.tick()
	a.period()
	a.KeyFrameWanted()
	if len(out.sent) != 0 || frames != 0 {
		t.Fatalf("sent before the encoder: %v", out.sent)
	}

	enc := &fakeEncoder{}
	a.started(enc)
	if enc.skip || enc.skips != 1 {
		t.Errorf("started: skip %v after %d calls", enc.skip, enc.skips)
	}
	a.frame(f)
	if !slices.Equal(out.sent, []bool{true}) || frames != 1 || enc.skip {
		t.Errorf("sent %v, frames %d, skip %v", out.sent, frames, enc.skip)
	}

	steps := []struct {
		name    string
		backlog rdp.Backlog
		skip    bool
	}{
		{"two on their way", rdp.Backlog{Frames: 2}, false},
		{"three on their way", rdp.Backlog{Frames: 3}, true},
		{"back to one", rdp.Backlog{Frames: 1, Oldest: 20 * time.Millisecond}, false},
		{"one queued", rdp.Backlog{Queued: 1}, true},
		{"empty", rdp.Backlog{}, false},
	}
	for _, s := range steps {
		out.set(func(o *fakeOut) { o.backlog = s.backlog })
		a.tick()
		if enc.skip != s.skip {
			t.Errorf("%s: skip %v, want %v", s.name, enc.skip, s.skip)
		}
	}
	// A frame that fills the backlog skips the next at once.
	out.set(func(o *fakeOut) { o.backlog = rdp.Backlog{Frames: 3} })
	a.frame(&EncodedFrame{Data: []byte{0, 0, 0, 1, 1}})
	if !enc.skip {
		t.Error("not skipping after the frame that filled the backlog")
	}
	// The skip lasted a frame's time (interval is 0 here): the period
	// skipped, so a quarter less.
	a.tick()
	a.period()
	if !slices.Equal(enc.bitrates, []int{6_000_000}) {
		t.Errorf("bitrates %v", enc.bitrates)
	}
	// A period of fast acknowledgements: a sixteenth more.
	out.set(func(o *fakeOut) { o.backlog = rdp.Backlog{Frames: 1, Oldest: 10 * time.Millisecond} })
	a.tick()
	a.frame(f)
	a.period()
	if !slices.Equal(enc.bitrates, []int{6_000_000, 6_375_000}) || enc.skip {
		t.Errorf("bitrates %v, skip %v", enc.bitrates, enc.skip)
	}
	// A failed SetBitrate is logged.
	enc.err = errors.New("X", "x")
	a.tick()
	a.frame(f)
	a.period()
	enc.err = nil
	if len(rec.find("the bitrate was not changed")) != 1 {
		t.Error("a failed SetBitrate was not logged")
	}

	// A Refresh Rect forces a key frame.
	forced := enc.forced
	a.KeyFrameWanted()
	if enc.forced != forced+1 {
		t.Errorf("forced %d, want %d", enc.forced, forced+1)
	}

	// Suppressed output skips everything, and its periods change nothing.
	a.OutputSuppressed(true)
	if !enc.skip {
		t.Error("not skipping while suppressed")
	}
	a.tick()
	n := len(enc.bitrates)
	a.period()
	if !enc.skip || len(enc.bitrates) != n {
		t.Errorf("suppressed: skip %v, bitrates %v", enc.skip, enc.bitrates)
	}
	a.OutputSuppressed(false)
	if enc.skip {
		t.Error("still skipping after the resume")
	}

	// The errors of SendFrame.
	errs := []struct {
		tag     string
		forced  int
		skipped bool
		closed  bool
	}{
		{rdp.TagKeyFrameWanted, 1, false, false},
		{rdp.TagQueueFull, 1, true, false},
		{"Other", 1, false, false},
		{rdp.TagSessionOver, 0, false, true},
	}
	for _, e := range errs {
		a.mu.Lock()
		a.closed, a.skipped = false, false
		a.mu.Unlock()
		out.set(func(o *fakeOut) { o.err = errors.New(e.tag, "x") })
		forced := enc.forced
		a.frame(f)
		if enc.forced-forced != e.forced || a.skipped != e.skipped || a.closed != e.closed {
			t.Errorf("%s: forced %d, skipped %v, closed %v", e.tag, enc.forced-forced, a.skipped, a.closed)
		}
	}
	if len(rec.find("an H.264 frame was not sent")) != 1 {
		t.Error("the other error was not logged")
	}

	// The session left the H.264 path: SendFrame says so, or the tick sees
	// it; the path stops once.
	a.mu.Lock()
	a.closed = false
	a.mu.Unlock()
	out.set(func(o *fakeOut) { o.err = errors.New(rdp.TagH264Unavailable, "x") })
	a.frame(f)
	if stops != 1 || !a.closed {
		t.Errorf("stops %d, closed %v", stops, a.closed)
	}
	a.mu.Lock()
	a.closed = false
	a.mu.Unlock()
	out.set(func(o *fakeOut) { o.err, o.path = nil, rdp.VideoPlanar })
	a.tick()
	a.tick()
	if stops != 2 {
		t.Errorf("stops %d, want 2", stops)
	}
	sent := len(out.sent)
	a.frame(f)
	a.end()
	a.frame(f)
	if len(out.sent) != sent {
		t.Error("a frame went after the path ended")
	}
}

// recordSink is a flow sink that records its calls.
type recordSink struct{ calls []string }

func (r *recordSink) KeyFrameWanted() { r.calls = append(r.calls, "key") }
func (r *recordSink) OutputSuppressed(s bool) {
	if s {
		r.calls = append(r.calls, "suppressed")
	} else {
		r.calls = append(r.calls, "resumed")
	}
}

// TestAVCPathIdle: run reads the backlog only once a rate period while
// frames go freely or none come (SPEC §11 rule 6), ticks while the backlog
// holds the encoder back, clearing the skip as soon as it drains, and stops
// ticking then.
func TestAVCPathIdle(t *testing.T) {
	out := &fakeOut{path: rdp.VideoAVC420}
	enc := &fakeEncoder{}
	a := &avcPath{out: out, log: slog.New(&recorder{}), rate: &rateControl{bitrate: StartBitrate, max: DefaultMaxBitrate},
		stop: func() {}, interval: 16 * time.Millisecond, wake: make(chan struct{}, 1)}
	a.started(enc)
	reads := func() int {
		out.mu.Lock()
		defer out.mu.Unlock()
		return out.reads
	}
	skipping := func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return enc.skip
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.run(ctx, time.Millisecond, time.Hour)
	}()
	defer func() {
		cancel()
		<-done
	}()
	f := &EncodedFrame{Data: []byte{0, 0, 0, 1, 5}, Key: true}

	// Frames that go freely, then none: no ticks.
	a.frame(f)
	a.frame(f)
	time.Sleep(100 * time.Millisecond)
	if n := reads(); n != 2 {
		t.Errorf("the backlog was read %d times for two frames and 100 ms of nothing, want 2", n)
	}

	// The backlog holds the encoder back: the ticker reads it until it
	// drains.
	out.set(func(o *fakeOut) { o.backlog = rdp.Backlog{Frames: maxUnacked + 1} })
	a.frame(f)
	if !skipping() {
		t.Fatal("the encoder does not skip behind a backlog")
	}
	deadline := time.Now().Add(5 * time.Second)
	for reads() < 10 {
		if time.Now().After(deadline) {
			t.Fatalf("the backlog was read %d times in 5 s while it held the encoder back", reads())
		}
		time.Sleep(time.Millisecond)
	}
	out.set(func(o *fakeOut) { o.backlog = rdp.Backlog{} })
	for skipping() {
		if time.Now().After(deadline) {
			t.Fatal("the skip stayed after the backlog drained")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	before := reads()
	time.Sleep(100 * time.Millisecond)
	if n := reads() - before; n != 0 {
		t.Errorf("the backlog was read %d times in 100 ms after it drained", n)
	}
}

func TestFlowSwitch(t *testing.T) {
	avc := false
	f := &flowSwitch{want: func() bool { return avc }}
	// Nothing runs: the calls only keep the state.
	f.KeyFrameWanted()
	f.OutputSuppressed(true)

	cancels := 0
	bitmaps := &recordSink{}
	f.set(bitmaps, false, func() { cancels++ })
	f.OutputSuppressed(false)
	f.KeyFrameWanted()
	if want := []string{"suppressed", "resumed", "key"}; !slices.Equal(bitmaps.calls, want) || cancels != 0 {
		t.Errorf("calls %q, cancels %d", bitmaps.calls, cancels)
	}
	// The surface was created with H.264: the bitmap path ends.
	avc = true
	f.KeyFrameWanted()
	if cancels != 1 || len(bitmaps.calls) != 3 {
		t.Errorf("calls %q, cancels %d", bitmaps.calls, cancels)
	}
	h264 := &recordSink{}
	f.set(h264, true, func() { cancels++ })
	f.KeyFrameWanted()
	if !slices.Equal(h264.calls, []string{"key"}) || cancels != 1 {
		t.Errorf("calls %q, cancels %d", h264.calls, cancels)
	}
	// The path changed while none ran, after the next was chosen: it ends
	// as it is set.
	f.set(nil, false, nil)
	avc = false
	f.KeyFrameWanted()
	late := &recordSink{}
	f.set(late, true, func() { cancels++ })
	if cancels != 2 || len(late.calls) != 0 {
		t.Errorf("a path set after the change: calls %q, cancels %d", late.calls, cancels)
	}
}

// avcClient reads the frames of a session on the H.264 path like a client
// that takes delay to show each one, acknowledging each after it.
type avcClient struct {
	t     *testing.T
	c     *rdptest.Client
	ch    uint32
	total uint32
	// snd, when set, is the sound, whose blocks are read and confirmed
	// between frames; stamps are their audio time stamps.
	snd    *rdptest.Audio
	stamps []uint32
}

// avcFrame is an H.264 frame of PatternVideo as the client got it.
type avcFrame struct {
	n   uint64
	key bool
	// age is how long ago it was made when the client got it.
	age time.Duration
}

// next reads frames until one of H.264, acknowledging each after delay.
func (a *avcClient) next(delay time.Duration) avcFrame {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		f, err := a.c.ReadGFXFrame(ctx, a.ch)
		if err != nil {
			a.t.Fatal(err)
		}
		got := time.Now()
		time.Sleep(delay)
		a.total++
		if err := a.c.AckFrame(ctx, a.ch, 0, f.ID, a.total); err != nil {
			a.t.Fatal(err)
		}
		for a.snd != nil && a.snd.Pending() {
			b, err := a.snd.ReadBlock(ctx)
			if err != nil {
				a.t.Fatal(err)
			}
			if err := a.snd.Confirm(ctx, b, 0); err != nil {
				a.t.Fatal(err)
			}
			a.stamps = append(a.stamps, b.AudioTimeStamp)
		}
		if len(f.H264) == 0 {
			continue // a frame of tiles, sent before the switch
		}
		n, made, key, ok := PatternVideoFrame(f.H264[0])
		if !ok {
			a.t.Fatalf("frame %d is not one of PatternVideo", f.ID)
		}
		return avcFrame{n: n, key: key, age: got.Sub(made)}
	}
}

// run reads frames for d, each shown in delay, and returns them.
func (a *avcClient) run(d, delay time.Duration) []avcFrame {
	a.t.Helper()
	var fs []avcFrame
	for end := time.Now().Add(d); time.Now().Before(end); {
		fs = append(fs, a.next(delay))
	}
	return fs
}

// oldest is the largest age of fs.
func oldest(fs []avcFrame) time.Duration {
	var m time.Duration
	for _, f := range fs {
		m = max(m, f.age)
	}
	return m
}

// TestSessionH264 runs a session on the H.264 path with PatternVideo and a
// client that is fast, then slow, then fast again (SPEC §9.5): while it is
// slow, frames are skipped before encoding so that none it gets is older
// than its three frames on their way allow, and the bitrate falls a
// quarter at a time; it recovers once the client is fast. The sound,
// meanwhile, keeps flowing without a gap (SPEC §10.1). A Refresh Rect and
// a resume each bring a key frame.
func TestSessionH264(t *testing.T) {
	video := &PatternVideo{}
	parts := Parts{Screen: &PatternScreen{Frames: 1}, Video: video, Sound: &ToneSource{}}
	s := serve(t, parts, Options{rateEvery: 100 * time.Millisecond}, 1280, 720)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.c.KeepChannel(s.c.ChannelID("rdpsnd"))
	s.c.KeepChannel(s.c.ChannelID("drdynvc"))
	g, err := s.c.StartGFX(ctx, rdptest.AVC420Caps())
	if err != nil {
		t.Fatal(err)
	}
	snd, err := s.c.NegotiateAudio(ctx, rdptest.AudioOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := &avcClient{t: t, c: s.c, ch: g.ChannelID, snd: snd}

	first := a.next(0)
	if !first.key || first.n != 0 {
		t.Errorf("first frame %+v", first)
	}
	st := video.Stats()
	if st.Captures != 1 || st.Config != (VideoConfig{Width: 1280, Height: 720, MaxFPS: DefaultVideoFPS, Bitrate: StartBitrate}) {
		t.Errorf("capture %+v", st)
	}

	// Fast: about 60 frames a second, and the bitrate rises.
	start := time.Now()
	fast := a.run(time.Second, 0)
	rate := float64(len(fast)) / time.Since(start).Seconds()
	st = video.Stats()
	if rate < 40 || rate > 66 {
		t.Errorf("%.1f frames a second from a fast client, cap %d", rate, DefaultVideoFPS)
	}
	if len(st.Bitrates) == 0 || st.Bitrates[len(st.Bitrates)-1] <= StartBitrate {
		t.Errorf("the bitrate did not rise: %v", st.Bitrates)
	}
	high := st.Bitrates[len(st.Bitrates)-1]
	skippedFast := st.Skipped

	// Slow: 20 frames a second. Each frame waits behind at most the two
	// on their way, plus a frame time of the source and a tick.
	soundBefore := len(a.stamps)
	slow := a.run(1500*time.Millisecond, 50*time.Millisecond)
	if n := len(a.stamps) - soundBefore; n < 60 {
		t.Errorf("%d blocks of sound in 1.5 s of slow video, want about 75", n)
	}
	st = video.Stats()
	limit := 3*50*time.Millisecond + 50*time.Millisecond
	t.Logf("fast: %d frames, oldest %v; slow: %d frames, oldest %v, %d skipped; bitrates %v",
		len(fast), oldest(fast), len(slow), oldest(slow), st.Skipped-skippedFast, st.Bitrates)
	if o := oldest(slow); o > limit {
		t.Errorf("a frame %v old reached the slow client, limit %v", o, limit)
	}
	if st.Skipped-skippedFast < 30 {
		t.Errorf("%d frames skipped for a client at a third of the rate", st.Skipped-skippedFast)
	}
	low := st.Bitrates[len(st.Bitrates)-1]
	if low > high*3/4 {
		t.Errorf("the bitrate did not fall: %d after %d", low, high)
	}
	for i := 1; i < len(st.Bitrates); i++ {
		if st.Bitrates[i] < st.Bitrates[i-1]-st.Bitrates[i-1]/4 {
			t.Errorf("the bitrate fell more than a quarter: %d to %d", st.Bitrates[i-1], st.Bitrates[i])
		}
	}

	// Fast again: the bitrate recovers.
	a.run(1500*time.Millisecond, 0)
	st = video.Stats()
	if back := st.Bitrates[len(st.Bitrates)-1]; back <= low {
		t.Errorf("the bitrate did not recover: %d after %d", back, low)
	}
	if len(a.stamps) < 150 {
		t.Errorf("%d blocks of sound in about 4 s", len(a.stamps))
	}
	for i := 1; i < len(a.stamps); i++ {
		if a.stamps[i]-a.stamps[i-1] != 20 {
			t.Errorf("a gap in the sound: %d ms then %d ms", a.stamps[i-1], a.stamps[i])
		}
	}
	if n := len(s.rec.find("the sound is behind: dropping it")); n != 0 {
		t.Errorf("the sound was dropped %d times", n)
	}
	a.snd = nil

	// A Refresh Rect brings a key frame within a few frames.
	if err := s.c.SendRefresh(ctx, 0, 0, 1280, 720); err != nil {
		t.Fatal(err)
	}
	keyWithin(t, a, 10, "after a Refresh Rect")

	// Suppressed, nothing is encoded; the resume brings a key frame.
	if err := s.c.SendSuppress(ctx, true, 1280, 720); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	before := video.Stats()
	time.Sleep(300 * time.Millisecond)
	during := video.Stats()
	if during.Frames-before.Frames > 1 || during.Skipped-before.Skipped < 10 {
		t.Errorf("suppressed: %d frames made, %d skipped", during.Frames-before.Frames, during.Skipped-before.Skipped)
	}
	if err := s.c.SendSuppress(ctx, false, 1280, 720); err != nil {
		t.Fatal(err)
	}
	keyWithin(t, a, 5, "after the resume")
}

// keyWithin fails t unless one of the next n H.264 frames is a key
// frame.
func keyWithin(t *testing.T, a *avcClient, n int, what string) {
	t.Helper()
	for range n {
		if a.next(0).key {
			return
		}
	}
	t.Errorf("no key frame in %d frames %s", n, what)
}

// TestSessionPathSwitch starts on the bitmap path, goes to H.264 when the
// client's surface takes it, and back to the bitmap path (Planar through
// the pipeline) when the client advertises again without H.264.
func TestSessionPathSwitch(t *testing.T) {
	video := &PatternVideo{}
	parts := Parts{Screen: &PatternScreen{}, Video: video}
	s := serve(t, parts, Options{VideoFPS: 30, MaxBitrate: 500_000, statsEvery: 50 * time.Millisecond}, 640, 360)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Bitmaps first (StartGFX skips their updates; ReadUpdate would skip
	// the dynamic channels' request instead).
	time.Sleep(200 * time.Millisecond)
	if n := video.Stats().Captures; n != 0 {
		t.Errorf("%d video captures before the pipeline", n)
	}
	if p := s.h.VideoPath(); p != rdp.VideoBitmaps {
		t.Errorf("path %v before the pipeline", p)
	}
	g, err := s.c.StartGFX(ctx, rdptest.AVC420Caps())
	if err != nil {
		t.Fatal(err)
	}
	a := &avcClient{t: t, c: s.c, ch: g.ChannelID}
	if f := a.next(0); !f.key {
		t.Errorf("first H.264 frame %+v", f)
	}
	a.run(200*time.Millisecond, 0)
	st := video.Stats()
	if st.Captures != 1 || st.Config.MaxFPS != 30 || st.Config.Bitrate != MinBitrate {
		t.Errorf("capture %+v", st)
	}
	if len(s.rec.find("video path")) == 0 {
		t.Error("the switch was not logged")
	}
	if p := s.h.VideoPath(); p != rdp.VideoAVC420 {
		t.Errorf("path %v", p)
	}
	// The frames the client acknowledged, and how long it took.
	if info, ok := s.h.Session(); !ok || info.Video != rdp.VideoAVC420 || info.FPS <= 0 || info.Delay <= 0 || info.Bitrate <= 0 {
		t.Errorf("session %+v, %v", info, ok)
	}

	// Without H.264 the session goes back to tiles through the pipeline.
	if _, err := s.c.AdvertiseGFX(ctx, g.ChannelID, rdptest.PlanarCaps()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		f, err := s.c.ReadGFXFrame(ctx, g.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		a.total++
		if err := s.c.AckFrame(ctx, g.ChannelID, 0, f.ID, a.total); err != nil {
			t.Fatal(err)
		}
		if f.Tiles > 0 && len(f.H264) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no tiles after the client stopped taking H.264")
		}
	}
	if n := video.Stats().Captures; n != 1 {
		t.Errorf("%d captures", n)
	}
	if p := s.h.VideoPath(); p != rdp.VideoPlanar {
		t.Errorf("path %v", p)
	}
	if err := s.c.Close(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); s.h.VideoPath() != 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the path stays after the session")
		}
	}
}

// errVideo is a video source that fails.
type errVideo struct{ err error }

func (v errVideo) CaptureVideo(context.Context, VideoConfig, func(VideoEncoder), func(*EncodedFrame)) error {
	return v.err
}

// TestSessionVideoFails ends the session when the capture into the
// encoder fails, as when the bitmap capture does.
func TestSessionVideoFails(t *testing.T) {
	parts := Parts{Screen: &PatternScreen{Frames: 1}, Video: errVideo{errors.New("TestVideoFailed", "no encoder")}}
	s := serve(t, parts, Options{}, 640, 480)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.c.StartGFX(ctx, rdptest.AVC420Caps()); err != nil {
		t.Fatal(err)
	}
	if _, info, err := s.c.AwaitDisconnect(ctx); err != nil || info == 0 {
		t.Fatalf("disconnect: info %d, %v", info, err)
	}
	recs := s.rec.find("the capture stopped")
	if len(recs) != 1 {
		t.Fatalf("logged %d times", len(recs))
	}
	err, _ := attr(recs[0], "err").Any().(error)
	assertRoute(t, err, "TestSessionVideoFails.TestVideoFailed")
}
