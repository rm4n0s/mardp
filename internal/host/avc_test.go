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
	// Periods: free sends everything, slow is a client that shows 40 of
	// 60 frames, slower one that shows 30, most one that shows 55.
	free, slow, slower, most := seen{sent: 60}, seen{sent: 40, missed: 20}, seen{sent: 30, missed: 30}, seen{sent: 55, missed: 5}
	type step struct {
		p          seen
		want       int
		clientPace bool
	}
	tests := []struct {
		name    string
		bitrate int
		steps   []step
	}{
		{"free: a sixteenth more", 8_000_000, []step{{free, 8_500_000, false}}},
		{"never above the cap", 19_500_000, []step{{free, 20_000_000, false}}},
		{"nothing sent: the same", 8_000_000, []step{{seen{}, 8_000_000, false}}},
		{"a few missed: the same", 8_000_000, []step{{most, 8_000_000, false}, {seen{sent: 57, missed: 4}, 8_000_000, false}}},
		{"under four missed: a sixteenth more", 8_000_000, []step{{seen{sent: 10, missed: 3}, 8_500_000, false}}},
		{"a frame lost to a full queue: a quarter less, every time", 8_000_000,
			[]step{{seen{sent: 60, lost: true}, 6_000_000, false}, {seen{sent: 60, lost: true}, 4_500_000, false}}},
		{"never below the floor", 1_200_000, []step{{seen{sent: 60, lost: true}, MinBitrate, false}}},
		{"held back: a quarter less, as a trial", 8_000_000, []step{{slow, 6_000_000, false}}},
		{"the network: the fall brings the frames back and stays", 8_000_000, []step{
			{slower, 6_000_000, false},
			{slow, 4_500_000, false}, // more of the frames went, still held back: again
			{free, 4_781_250, false}, // it helped: free again, and up
		}},
		{"the network: half of the missed frames came back", 8_000_000, []step{
			{seen{sent: 48, missed: 12}, 6_000_000, false},
			{most, 6_000_000, false},
		}},
		{"a trial that seemed to help, and the next that did not: back to where the run began", 8_000_000, []step{
			{slower, 6_000_000, false},
			{slow, 4_500_000, false},
			{slow, 8_000_000, true},
			{slow, 8_000_000, false},
		}},
		{"the screen changed less after the fall: this fall is undone, nothing learned", 8_000_000, []step{
			{slower, 6_000_000, false},
			{slow, 4_500_000, false},
			{seen{sent: 42, missed: 3}, 6_000_000, false},
			{slow, 4_500_000, false},
		}},
		{"a slow client: the fall brings nothing, the bitrate goes back and stays", 8_000_000, []step{
			{slow, 6_000_000, false},
			{slow, 8_000_000, true},
			{slow, 8_000_000, false},
			{slower, 8_000_000, false},
		}},
		{"a slow client whose motion began inside the period", 8_000_000, []step{
			{seen{sent: 20, missed: 10}, 6_000_000, false},
			{slow, 8_000_000, true}, // twice the frames, the same share
		}},
		{"the screen stopped changing after the fall: back, nothing learned", 8_000_000, []step{
			{slow, 6_000_000, false},
			{seen{sent: 3}, 8_000_000, false},
			{slow, 6_000_000, false},
		}},
		{"a frame lost during a trial: the fall stays, and one more", 8_000_000, []step{
			{slow, 6_000_000, false},
			{seen{sent: 40, missed: 20, lost: true}, 4_500_000, false},
			{slow, 3_375_000, false},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := rateControl{bitrate: tt.bitrate, max: DefaultMaxBitrate}
			for i, s := range tt.steps {
				got, pace := r.update(s.p)
				if got != s.want || r.bitrate != s.want || pace != s.clientPace {
					t.Fatalf("step %d: update = %d, %v, want %d, %v", i, got, pace, s.want, s.clientPace)
				}
			}
		})
	}
}

// The bitrate stays for ten periods after a fall that did not help, then
// tries again, and stays twice as long each time in a row, at most
// maxHold; a fall that helps starts the count anew.
func TestRateControlHold(t *testing.T) {
	slow, free := seen{sent: 40, missed: 20}, seen{sent: 60}
	r := rateControl{bitrate: 8_000_000, max: 8_000_000}
	// trial runs periods of a slow client until the next fall, and
	// returns how many came before it.
	trial := func() int {
		t.Helper()
		for n := 0; n < 2*maxHold; n++ {
			if got, _ := r.update(slow); got != 8_000_000 {
				if got, pace := r.update(slow); got != 8_000_000 || !pace {
					t.Fatalf("after the trial: %d, %v", got, pace)
				}
				return n
			}
		}
		t.Fatal("no trial")
		return 0
	}
	for i, want := range []int{0, 10, 20, 40, 80, 160, 320, 320} {
		if got := trial(); got != want {
			t.Errorf("trial %d: after %d periods, want %d", i, got, want)
		}
	}
	// Free periods count too, and raise nothing above the cap.
	for range 320 {
		r.update(free)
	}
	// Now the network: the fall helps, and the next slow client is tried
	// after ten periods again.
	if got, _ := r.update(seen{sent: 30, missed: 30}); got != 6_000_000 {
		t.Fatalf("the fall: %d", got)
	}
	if got, pace := r.update(free); got != 6_375_000 || pace {
		t.Fatalf("after the fall that helped: %d, %v", got, pace)
	}
	r.bitrate = 8_000_000
	if got := trial(); got != 0 {
		t.Errorf("the trial after a fall that helped: after %d periods", got)
	}
	if got := trial(); got != 10 {
		t.Errorf("the next: after %d periods, want 10", got)
	}
}

// TestRateControlModels runs the rate control for five minutes against a
// model of each thing that misses frames. A network that carries 3 Mbit/s
// lets through the share of 60 frames that fits: the bitrate comes down
// to about what it carries and stays there. A client that shows 40 frames
// a second whatever the bitrate keeps its bitrate: it is a quarter lower
// for one period in a trial, and trials grow rare.
func TestRateControlModels(t *testing.T) {
	const periods = 300
	t.Run("a network of 3 Mbit/s", func(t *testing.T) {
		const carries = 3_000_000
		r := rateControl{bitrate: StartBitrate, max: DefaultMaxBitrate}
		for i := range periods {
			sent := min(60, 60*carries/r.bitrate)
			b, pace := r.update(seen{sent: sent, missed: 60 - sent})
			if pace {
				t.Fatalf("period %d: the network taken for a slow client at %d", i, b)
			}
			if i >= 10 && (b < carries*5/8 || b > carries*5/4) {
				t.Fatalf("period %d: bitrate %d on a network of %d", i, b, carries)
			}
		}
	})
	t.Run("a client of 40 frames a second", func(t *testing.T) {
		r := rateControl{bitrate: StartBitrate, max: DefaultMaxBitrate}
		low := 0
		for i := range periods {
			b, _ := r.update(seen{sent: 40, missed: 20})
			if b < StartBitrate-StartBitrate/4 {
				t.Fatalf("period %d: bitrate %d", i, b)
			}
			if b < StartBitrate {
				low++
			}
		}
		// Trials after 0, 10, 20, 40, 80 and 160 periods of staying.
		if r.bitrate != StartBitrate || low != 5 {
			t.Errorf("bitrate %d at the end, %d periods under %d", r.bitrate, low, StartBitrate)
		}
	})
}

// clientPaceMsg is what the path logs when a fall of the bitrate did not
// help.
const clientPaceMsg = "a lower bitrate did not bring more frames: the client sets the pace, and the bitrate stays"

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
	clock := time.Unix(1000, 0)
	a := &avcPath{out: out, log: slog.New(rec), rate: &rateControl{bitrate: StartBitrate, max: DefaultMaxBitrate}, stop: func() { stops++ },
		interval: time.Second / 60, now: func() time.Time { return clock }}
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
	// The backlog held the encoder back for half the period: 30 frames
	// missed, so a quarter less, as a trial.
	clock = clock.Add(500 * time.Millisecond)
	out.set(func(o *fakeOut) { o.backlog = rdp.Backlog{Frames: 2} })
	a.tick()
	a.mu.Lock()
	if a.held != 500*time.Millisecond {
		t.Errorf("held %v", a.held)
	}
	a.mu.Unlock()
	a.period()
	if !slices.Equal(enc.bitrates, []int{6_000_000}) {
		t.Errorf("bitrates %v", enc.bitrates)
	}
	// The next period is held back across its end, and no more frames
	// went: the client sets the pace, and the bitrate goes back.
	out.set(func(o *fakeOut) { o.backlog = rdp.Backlog{Frames: 3} })
	a.frame(f)
	clock = clock.Add(500 * time.Millisecond)
	a.period()
	if !slices.Equal(enc.bitrates, []int{6_000_000, 8_000_000}) || len(rec.find(clientPaceMsg)) != 1 {
		t.Errorf("bitrates %v, %d logged", enc.bitrates, len(rec.find(clientPaceMsg)))
	}
	// What was held after the period's end counts in the next, and
	// changes nothing while the bitrate stays.
	clock = clock.Add(400 * time.Millisecond)
	out.set(func(o *fakeOut) { o.backlog = rdp.Backlog{} })
	a.tick()
	a.mu.Lock()
	if a.held != 400*time.Millisecond {
		t.Errorf("held %v after the period's end", a.held)
	}
	a.mu.Unlock()
	a.period()
	if len(enc.bitrates) != 2 {
		t.Errorf("bitrates %v", enc.bitrates)
	}
	// A period where frames went freely: a sixteenth more.
	out.set(func(o *fakeOut) { o.backlog = rdp.Backlog{Frames: 1, Oldest: 10 * time.Millisecond} })
	a.tick()
	a.frame(f)
	a.period()
	if !slices.Equal(enc.bitrates, []int{6_000_000, 8_000_000, 8_500_000}) || enc.skip {
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
		tag    string
		forced int
		lost   bool
		closed bool
	}{
		{rdp.TagKeyFrameWanted, 1, false, false},
		{rdp.TagQueueFull, 1, true, false},
		{"Other", 1, false, false},
		{rdp.TagSessionOver, 0, false, true},
	}
	for _, e := range errs {
		a.mu.Lock()
		a.closed, a.lost = false, false
		a.mu.Unlock()
		out.set(func(o *fakeOut) { o.err = errors.New(e.tag, "x") })
		forced := enc.forced
		a.frame(f)
		if enc.forced-forced != e.forced || a.lost != e.lost || a.closed != e.closed {
			t.Errorf("%s: forced %d, lost %v, closed %v", e.tag, enc.forced-forced, a.lost, a.closed)
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
// than its three frames on their way allow; the bitrate tries a quarter
// less, finds that it brings no more frames, since the client and not the
// network is slow, and goes back: a slow client costs frames, not the
// picture's quality. The sound, meanwhile, keeps flowing without a gap
// (SPEC §10.1). A Refresh Rect and a resume each bring a key frame.
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
	// The bitrate tried a quarter less and went back to what it was, and
	// the path said why. (The periods of this test are 100 ms, a handful
	// of frames each, so a trial may seem to help by chance; the run of
	// trials still ends where it began.)
	if n := len(s.rec.find(clientPaceMsg)); n == 0 {
		t.Errorf("the path did not find that the client sets the pace: bitrates %v", st.Bitrates)
	}
	fell, back := false, false
	for i := 1; i < len(st.Bitrates); i++ {
		if st.Bitrates[i] < st.Bitrates[i-1]-st.Bitrates[i-1]/4 {
			t.Errorf("the bitrate fell more than a quarter: %d to %d", st.Bitrates[i-1], st.Bitrates[i])
		}
		fell = fell || st.Bitrates[i] < st.Bitrates[i-1]
		back = back || fell && st.Bitrates[i] >= high
	}
	if !back {
		t.Errorf("the bitrate did not go back to %d for a slow client: %v", high, st.Bitrates)
	}

	// Fast again: the bitrate is at least what it was.
	a.run(1500*time.Millisecond, 0)
	st = video.Stats()
	if back := st.Bitrates[len(st.Bitrates)-1]; back < high {
		t.Errorf("the bitrate did not recover: %d, was %d", back, high)
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
// TestCaptures: the host counts the captures it runs: the screen's on the
// bitmap path, the encoder's once the graphics pipeline takes H.264, the
// sound's once the client takes sound, and none after the client
// disconnects.
func TestCaptures(t *testing.T) {
	parts := Parts{Screen: &PatternScreen{}, Video: &PatternVideo{}, Sound: &ToneSource{}}
	s := serve(t, parts, Options{}, 640, 480)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	await := func(what string, want Captures) {
		t.Helper()
		for s.h.Captures() != want {
			if ctx.Err() != nil {
				t.Fatalf("%s: captures %+v, want %+v", what, s.h.Captures(), want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	await("the bitmap path", Captures{Screen: 1})
	s.c.KeepChannel(s.c.ChannelID("rdpsnd"))
	s.c.KeepChannel(s.c.ChannelID("drdynvc"))
	if _, err := s.c.StartGFX(ctx, rdptest.AVC420Caps()); err != nil {
		t.Fatal(err)
	}
	await("the H.264 path", Captures{Video: 1})
	if _, err := s.c.NegotiateAudio(ctx, rdptest.AudioOptions{}); err != nil {
		t.Fatal(err)
	}
	await("the sound", Captures{Video: 1, Sound: 1})
	if err := s.c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	await("after the session", Captures{})
}

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
