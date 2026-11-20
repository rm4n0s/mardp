// Package vtenc is the H.264 encoder of the H.264 path (SPEC §9.3, §4.2):
// a hardware VTCompressionSession of VideoToolbox in real-time mode with
// low-latency rate control, Constrained High, no frame reordering, BT.709,
// with the range of the NV12 frames it gets. It implements
// host.VideoEncoder.
//
// # Frames
//
// Frames do not pass through Go: the capture hands its pixel buffers to the
// encoder in C (step 3.7), and only the encoded frames come out. The skip
// flag and a pending forced key frame are read there, at each frame, so a
// skipped frame costs no crossing into Go. The first frame is a key frame,
// and after it only P-frames, until a key frame is forced.
//
// Each encoded frame reaches the frame function once, as one access unit
// in Annex B (4-byte start codes), the sequence and picture parameter sets
// in front of every key frame. The calls come one at a time on a thread of
// VideoToolbox; the frame's data is valid only during the call, since its
// buffer is reused for the next frame.
//
// # Teardown
//
// Close waits for the frames that are being encoded, whose calls come
// before it returns, and then nothing calls back any more. Close must not
// be called from the frame function, and the capture that feeds the
// encoder is stopped before it.
package vtenc

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/internal/host"
)

// Tags of the errors of the encoder.
const (
	// TagEncoderUnsupported: the package runs on a system that is not
	// macOS.
	TagEncoderUnsupported = "EncoderUnsupported"
	// TagEncoderConfigInvalid: options that are not an encoder's.
	TagEncoderConfigInvalid = "EncoderConfigInvalid"
	// TagEncoderCreateFailed: VideoToolbox refused the session or one of
	// its settings.
	TagEncoderCreateFailed = "EncoderCreateFailed"
	// TagEncoderClosed: the encoder is closed.
	TagEncoderClosed = "EncoderClosed"
	// TagEncoderBitrateInvalid: a bitrate that is not one.
	TagEncoderBitrateInvalid = "EncoderBitrateInvalid"
	// TagEncoderBitrateFailed: VideoToolbox refused a new bitrate.
	TagEncoderBitrateFailed = "EncoderBitrateFailed"
	// TagEncoderSampleInvalid: an encoded frame whose NAL units run past
	// its end. It is logged and the frame dropped.
	TagEncoderSampleInvalid = "EncoderSampleInvalid"
)

// Defaults of the options.
const (
	// DefaultBitrate is the bitrate of a session's start (SPEC §9.5).
	DefaultBitrate = 8_000_000
	// DefaultFPS is the frame rate the encoder expects (SPEC §9.4).
	DefaultFPS = 60
)

// Profile is the H.264 profile of the stream (SPEC §9.3).
type Profile int

// The profiles. Constrained High is the one §9.3 chose; the others are its
// fallbacks if a client cannot decode it.
const (
	ProfileConstrainedHigh Profile = iota
	ProfileMain
	ProfileConstrainedBaseline
)

func (p Profile) String() string {
	switch p {
	case ProfileConstrainedHigh:
		return "constrained-high"
	case ProfileMain:
		return "main"
	case ProfileConstrainedBaseline:
		return "constrained-baseline"
	}
	return "unknown"
}

// Options configure an Encoder.
type Options struct {
	// Width and Height are the frames' size in pixels.
	Width, Height int
	// FullRange says the frames are full range NV12 (420f), not video
	// range (420v); the stream signals the range it gets (SPEC §9.3).
	FullRange bool
	// Profile is the stream's profile.
	Profile Profile
	// Bitrate is the starting bitrate in bits a second; 0 is
	// DefaultBitrate.
	Bitrate int
	// FPS is the frame rate the encoder expects; 0 is DefaultFPS.
	FPS int
	// NoRefine keeps the encoder from encoding a still picture again
	// until it is sharp (SPEC §9.3): for tests that count the frames they
	// submit.
	NoRefine bool
	// Logger receives the log; nil discards it.
	Logger *slog.Logger
}

// Frame is one encoded frame.
type Frame struct {
	// Data is the access unit in Annex B, valid only during the call.
	Data []byte
	// Key says it is a key frame (an IDR picture), which the parameter
	// sets precede.
	Key bool
	// PTS is the presentation time the capture gave the picture.
	PTS time.Duration
	// Captured is when the picture was captured, from its presentation
	// time on the host clock; zero when that is unknown. Encoded is when
	// the encoder handed the frame over (SPEC §14).
	Captured, Encoded time.Time
}

// Stats count what the encoder did.
type Stats struct {
	// Frames and KeyFrames are the frames delivered and the key frames
	// among them.
	Frames, KeyFrames int64
	// Dropped are the frames VideoToolbox dropped, Failed the ones it
	// failed to encode or that came out malformed.
	Dropped, Failed int64
	// Repeats are the frames, among Frames, that encoded a still picture
	// again to sharpen it (SPEC §9.3).
	Repeats int64
}

// output is one frame as VideoToolbox handed it over.
type output struct {
	status  int  // an OSStatus; not 0 when encoding failed
	dropped bool // VideoToolbox dropped the frame
	key     bool
	repeat  bool     // the newest picture encoded again, not a new one
	sets    [][]byte // the parameter sets, on key frames
	nalLen  int      // the length of the NAL unit lengths in avcc
	avcc    []byte   // the NAL units, each after its length
	pts     time.Duration
	// age is how long ago the picture was captured, by its presentation
	// time on the host clock; negative when unknown.
	age time.Duration
}

// maxAge is the oldest a picture can be when its frame comes out: an age
// beyond it, or below 0, comes from a presentation time on another clock.
const maxAge = 10 * time.Second

// config is what the backend makes a session of.
type config struct {
	width, height int
	fullRange     bool
	profile       Profile
	bitrate, fps  int
}

// createFailure says why a session was not made.
type createFailure struct {
	step   string // what VideoToolbox refused
	status int    // its OSStatus
}

// session is one VTCompressionSession.
type session interface {
	// setBitrate sets the average bitrate; it returns the OSStatus.
	setBitrate(bitsPerSecond int) int
	// forceKeyFrame makes the next frame encoded a key frame.
	forceKeyFrame()
	// setSkip makes the session skip the frames it gets, or encode them.
	setSkip(skip bool)
	// close waits for the frames being encoded and invalidates the
	// session; no output reaches the encoder after it returns.
	close()
	// submitter returns the C function frames are submitted to and its
	// first argument.
	submitter() (fn, arg unsafe.Pointer)
	// repeat encodes the newest picture submitted again; it returns the
	// OSStatus, repeatSkipped when the session skips, and repeatNothing
	// when no picture was submitted yet.
	repeat() int
	// captured counts the pictures submitted.
	captured() uint32
	// stale says the newest picture submitted was skipped.
	stale() bool
}

// backend is VideoToolbox, or a fake.
type backend interface {
	// create makes a session of cfg whose outputs go to enc.deliver.
	create(cfg config, enc *Encoder) (session, *createFailure)
}

// Encoder is one H.264 encoder. Its methods may be called from any
// goroutine.
type Encoder struct {
	cfg   config
	frame func(*Frame)
	log   *slog.Logger
	s     session

	// mu serializes the deliveries and guards buf and out.
	mu  sync.Mutex
	buf []byte
	out Frame

	// closeMu orders Close against the setters; it is not held while the
	// session closes, since the frames it waits for may call the setters
	// from the frame function.
	closeMu   sync.Mutex
	closed    bool
	closeOnce sync.Once

	frames, keys, dropped, failed, repeats atomic.Int64

	// ref sharpens a still picture (refine.go).
	ref refiner
}

var _ host.VideoEncoder = (*Encoder)(nil)

// newEncoder makes an Encoder on a backend.
func newEncoder(opts Options, frame func(*Frame), be backend) (*Encoder, error) {
	if opts.Width <= 0 || opts.Height <= 0 || opts.Width > host.MaxDesktopSide || opts.Height > host.MaxDesktopSide ||
		opts.Bitrate < 0 || opts.FPS < 0 || opts.Profile < ProfileConstrainedHigh || opts.Profile > ProfileConstrainedBaseline || frame == nil {
		return nil, errors.New(TagEncoderConfigInvalid, "options that are not an encoder's",
			"width", opts.Width, "height", opts.Height, "bitrate", opts.Bitrate, "fps", opts.FPS,
			"profile", int(opts.Profile), "frame", frame != nil)
	}
	cfg := config{width: opts.Width, height: opts.Height, fullRange: opts.FullRange, profile: opts.Profile,
		bitrate: opts.Bitrate, fps: opts.FPS}
	if cfg.bitrate == 0 {
		cfg.bitrate = DefaultBitrate
	}
	if cfg.fps == 0 {
		cfg.fps = DefaultFPS
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	e := &Encoder{cfg: cfg, frame: frame, log: log}
	e.ref.init(cfg, opts.NoRefine)
	s, f := be.create(cfg, e)
	if f != nil {
		return nil, errors.New(TagEncoderCreateFailed, "VideoToolbox refused the encoder",
			"step", f.step, "status", f.status, "width", cfg.width, "height", cfg.height, "profile", cfg.profile.String())
	}
	e.s = s
	log.Info("H.264 encoder", "width", cfg.width, "height", cfg.height, "profile", cfg.profile.String(),
		"fullRange", cfg.fullRange, "bitrate", cfg.bitrate, "fps", cfg.fps)
	return e, nil
}

// SetBitrate sets the average bitrate, without a restart (SPEC §9.5).
func (e *Encoder) SetBitrate(bitsPerSecond int) error {
	if bitsPerSecond <= 0 {
		return errors.New(TagEncoderBitrateInvalid, "a bitrate that is not one", "bitrate", bitsPerSecond)
	}
	e.closeMu.Lock()
	defer e.closeMu.Unlock()
	if e.closed {
		return errors.New(TagEncoderClosed, "the encoder is closed", "bitrate", bitsPerSecond)
	}
	if st := e.s.setBitrate(bitsPerSecond); st != 0 {
		return errors.New(TagEncoderBitrateFailed, "VideoToolbox refused the bitrate", "bitrate", bitsPerSecond, "status", st)
	}
	e.ref.setBitrate(bitsPerSecond)
	return nil
}

// ForceKeyFrame makes the next frame encoded a key frame. On a still
// screen the newest picture is encoded again for it.
func (e *Encoder) ForceKeyFrame() {
	e.closeMu.Lock()
	defer e.closeMu.Unlock()
	if !e.closed {
		e.s.forceKeyFrame()
		e.kick(true)
	}
}

// SetSkip makes the encoder skip the frames it gets, or encode them again.
// A forced key frame waits for the first frame encoded. When it encodes
// again, the newest picture goes if it was skipped, and a still picture
// goes on getting sharper.
func (e *Encoder) SetSkip(skip bool) {
	e.closeMu.Lock()
	defer e.closeMu.Unlock()
	if !e.closed {
		e.s.setSkip(skip)
		if !skip {
			e.kick(e.s.stale())
		}
	}
}

// Submitter returns the C function the capture hands its frames to, and
// the argument it passes first (SPEC §4.2):
//
//	long fn(void *arg, CVPixelBufferRef pb, CMTime pts)
//
// fn encodes the NV12 pixel buffer pb at pts unless the encoder skips; it
// returns VideoToolbox's OSStatus, or 1 for a skipped frame, and may be
// called from any thread. Both stay valid until Close, which the capture
// must not outlive.
func (e *Encoder) Submitter() (fn, arg unsafe.Pointer) { return e.s.submitter() }

// Stats returns the counts so far.
func (e *Encoder) Stats() Stats {
	return Stats{Frames: e.frames.Load(), KeyFrames: e.keys.Load(), Dropped: e.dropped.Load(), Failed: e.failed.Load(),
		Repeats: e.repeats.Load()}
}

// Close waits for the frames being encoded and ends the session. It may be
// called more than once. The frame function may call the setters while
// Close waits; they do nothing then.
func (e *Encoder) Close() {
	e.closeMu.Lock()
	e.closed = true
	e.closeMu.Unlock()
	e.closeOnce.Do(e.s.close)
}

// deliver turns an output into a Frame for the frame function.
func (e *Encoder) deliver(o output) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if o.dropped {
		e.dropped.Add(1)
		e.log.Debug("VideoToolbox dropped a frame", "pts", o.pts)
		// The client did not get this picture: it is encoded again.
		e.kick(true)
		return
	}
	if o.status != 0 {
		e.failed.Add(1)
		e.log.Warn("VideoToolbox failed to encode a frame", "status", o.status, "pts", o.pts)
		return
	}
	e.refined(o)
	buf, err := appendAnnexB(e.buf[:0], o.sets, o.nalLen, o.avcc)
	if err != nil {
		e.failed.Add(1)
		e.log.Warn("an encoded frame is malformed", "err", err)
		return
	}
	e.buf = buf
	e.frames.Add(1)
	if o.key {
		e.keys.Add(1)
	}
	if o.repeat {
		e.repeats.Add(1)
	}
	e.out = Frame{Data: buf, Key: o.key, PTS: o.pts, Encoded: time.Now()}
	if o.age >= 0 && o.age < maxAge {
		e.out.Captured = e.out.Encoded.Add(-o.age)
	}
	e.frame(&e.out)
}

// startCode is the start code put in front of each NAL unit.
var startCode = [4]byte{0, 0, 0, 1}

// appendAnnexB appends to dst the parameter sets and the NAL units of avcc,
// each preceded by its length in nalLen bytes, in Annex B.
func appendAnnexB(dst []byte, sets [][]byte, nalLen int, avcc []byte) ([]byte, error) {
	if nalLen != 1 && nalLen != 2 && nalLen != 4 {
		return dst, errors.New(TagEncoderSampleInvalid, "a NAL unit length size H.264 does not have", "nalLength", nalLen)
	}
	for _, s := range sets {
		dst = append(dst, startCode[:]...)
		dst = append(dst, s...)
	}
	for off := 0; off < len(avcc); {
		if len(avcc)-off < nalLen {
			return dst, errors.New(TagEncoderSampleInvalid, "a NAL unit length cut short", "offset", off, "length", len(avcc))
		}
		n := 0
		for _, b := range avcc[off : off+nalLen] {
			n = n<<8 | int(b)
		}
		off += nalLen
		if n == 0 || n > len(avcc)-off {
			return dst, errors.New(TagEncoderSampleInvalid, "a NAL unit that is empty or runs past the frame",
				"offset", off, "nal", n, "length", len(avcc))
		}
		dst = append(dst, startCode[:]...)
		dst = append(dst, avcc[off:off+n]...)
		off += n
	}
	return dst, nil
}
