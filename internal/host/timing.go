package host

import (
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/rm4n0s/mardp/rdp"
)

// The measurement of the H.264 path (SPEC §14, §15.5): when each frame was
// captured, encoded, queued and written, so that the delay the host adds,
// from the capture of a frame to its last byte written to the socket, is
// a number.

// FrameTiming is when one H.264 frame passed each stage of the host.
type FrameTiming struct {
	// Captured is when the picture was captured; zero when the source
	// does not know.
	Captured time.Time
	// Encoded is when the encoder handed the frame over.
	Encoded time.Time
	// Queued is when it was queued for the session's writer, Written when
	// its last byte was written to the connection.
	Queued, Written time.Time
	// Bytes is the length of what was written; Key says it is a key
	// frame.
	Bytes int
	Key   bool
}

// timingRing is how many frames sent and not yet written the H.264 path
// remembers: more than the session's video queue holds.
const timingRing = 8

// sentFrame is a frame the H.264 path queued, waiting for its write.
type sentFrame struct {
	// start is when it was handed to SendFrame, before rdp queued it.
	start             time.Time
	captured, encoded time.Time
	key               bool
}

// frameTimes pairs the frames the H.264 path sends with their writes: the
// writer writes the video queue in order, so each write is the oldest
// frame remembered, unless it was queued before that frame was sent (video
// of another path, queued before this one started).
type frameTimes struct {
	tell func(FrameTiming)

	mu         sync.Mutex
	ring       [timingRing]sentFrame
	head, size int
}

// sending remembers f, about to be sent.
func (t *frameTimes) sending(f *EncodedFrame) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.size == len(t.ring) {
		// Never, while the ring is larger than the queue; the oldest goes.
		t.head = (t.head + 1) % len(t.ring)
		t.size--
	}
	t.ring[(t.head+t.size)%len(t.ring)] = sentFrame{start: time.Now(), captured: f.Captured, encoded: f.Encoded, key: f.Key}
	t.size++
}

// notSent forgets the frame sending remembered last: it was not queued.
func (t *frameTimes) notSent() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.size > 0 {
		t.size--
	}
}

// written is the session's write hook: it tells of the frame w wrote.
func (t *frameTimes) written(w rdp.VideoWrite) {
	t.mu.Lock()
	if t.size == 0 || w.Queued.Before(t.ring[t.head].start) {
		t.mu.Unlock()
		return
	}
	f := t.ring[t.head]
	t.head = (t.head + 1) % len(t.ring)
	t.size--
	t.mu.Unlock()
	t.tell(FrameTiming{Captured: f.captured, Encoded: f.encoded, Queued: w.Queued, Written: w.Written, Bytes: w.Bytes, Key: f.key})
}

// The stages of a frame that Timings measures.
const (
	stageEncode = iota // capture to encoded
	stageHost          // encoded to queued
	stageWrite         // queued to written
	stageTotal         // capture to written: the delay the host adds
	stageCount
)

// How Timings counts durations: in steps of histogramStep up to
// histogramMax, and the longer ones together.
const (
	histogramStep = 100 * time.Microsecond
	histogramMax  = 2 * time.Second
	histogramLen  = int(histogramMax/histogramStep) + 1
)

// histogram counts durations, without allocating.
type histogram struct {
	counts [histogramLen]uint32
	n      int
	max    time.Duration
}

func (h *histogram) add(d time.Duration) {
	d = max(d, 0)
	h.counts[min(int(d/histogramStep), histogramLen-1)]++
	h.n++
	h.max = max(h.max, d)
}

// spread is the histogram's percentiles: each the upper end of its step,
// so never below the true value; the maximum is exact.
func (h *histogram) spread() Spread {
	if h.n == 0 {
		return Spread{}
	}
	s := Spread{Max: h.max}
	ranks := [3]int{(h.n*50 + 99) / 100, (h.n*95 + 99) / 100, (h.n*99 + 99) / 100}
	out := [3]*time.Duration{&s.P50, &s.P95, &s.P99}
	seen, r := 0, 0
	for i, c := range h.counts {
		seen += int(c)
		for r < len(ranks) && seen >= ranks[r] {
			*out[r] = min(time.Duration(i+1)*histogramStep, h.max)
			r++
		}
		if r == len(ranks) {
			break
		}
	}
	return s
}

// Spread is how a duration was spread over the frames.
type Spread struct {
	P50, P95, P99, Max time.Duration
}

// LogValue implements slog.LogValuer: milliseconds, to a tenth.
func (s Spread) LogValue() slog.Value {
	ms := func(d time.Duration) float64 {
		return float64(d.Round(100*time.Microsecond)) / float64(time.Millisecond)
	}
	return slog.GroupValue(slog.Float64("p50", ms(s.P50)), slog.Float64("p95", ms(s.P95)),
		slog.Float64("p99", ms(s.P99)), slog.Float64("max", ms(s.Max)))
}

// TimingSummary is what Timings saw over a span.
type TimingSummary struct {
	// Frames were written in Span, from the first write to the last;
	// Bytes is their length, KeyFrames how many were key frames.
	Frames, KeyFrames int
	Bytes             int64
	Span              time.Duration
	// NoCapture counts the frames whose capture time was unknown, which
	// Encode and Total leave out.
	NoCapture int
	// Encode is from capture to encoded, Host from encoded to queued,
	// Write from queued to written, and Total from capture to written.
	Encode, Host, Write, Total Spread
}

// Bitrate is the bits a second written over the span; 0 for a span of
// one frame.
func (s TimingSummary) Bitrate() int {
	if s.Span <= 0 {
		return 0
	}
	return int(float64(s.Bytes) * 8 / s.Span.Seconds())
}

// FPS is the frames a second written over the span.
func (s TimingSummary) FPS() float64 {
	if s.Span <= 0 || s.Frames < 2 {
		return 0
	}
	return float64(s.Frames-1) / s.Span.Seconds()
}

// LogValue implements slog.LogValuer.
func (s TimingSummary) LogValue() slog.Value {
	return slog.GroupValue(slog.Int("frames", s.Frames), slog.Int("keyFrames", s.KeyFrames),
		slog.Float64("fps", math.Round(s.FPS()*10)/10), slog.Int("bitrate", s.Bitrate()), slog.Int("noCapture", s.NoCapture),
		slog.Any("encode", s.Encode), slog.Any("host", s.Host), slog.Any("write", s.Write), slog.Any("total", s.Total))
}

// Timings gathers the FrameTiming of many frames into percentiles. Its
// methods may be called from any goroutine; Add allocates nothing.
type Timings struct {
	mu          sync.Mutex
	frames      int
	keys        int
	noCapture   int
	bytes       int64
	first, last time.Time
	stages      [stageCount]histogram
}

// Add counts f.
func (t *Timings) Add(f FrameTiming) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.frames == 0 {
		t.first = f.Written
	}
	t.last = f.Written
	t.frames++
	t.bytes += int64(f.Bytes)
	if f.Key {
		t.keys++
	}
	if f.Captured.IsZero() {
		t.noCapture++
	} else {
		t.stages[stageEncode].add(f.Encoded.Sub(f.Captured))
		t.stages[stageTotal].add(f.Written.Sub(f.Captured))
	}
	t.stages[stageHost].add(f.Queued.Sub(f.Encoded))
	t.stages[stageWrite].add(f.Written.Sub(f.Queued))
}

// Take returns what t counted since it was made or last taken, and starts
// counting anew.
func (t *Timings) Take() TimingSummary {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := TimingSummary{Frames: t.frames, KeyFrames: t.keys, Bytes: t.bytes, Span: t.last.Sub(t.first), NoCapture: t.noCapture,
		Encode: t.stages[stageEncode].spread(), Host: t.stages[stageHost].spread(),
		Write: t.stages[stageWrite].spread(), Total: t.stages[stageTotal].spread()}
	t.frames, t.keys, t.noCapture, t.bytes = 0, 0, 0, 0
	t.first, t.last = time.Time{}, time.Time{}
	for i := range t.stages {
		t.stages[i] = histogram{}
	}
	return s
}
