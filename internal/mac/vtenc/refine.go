package vtenc

import (
	"sync"
	"time"
)

// Sharpening a still picture (SPEC §9.3).
//
// The capture hands over a picture only when the screen changed, and the
// encoder gives each frame the share of the bitrate one frame has. A
// change of the whole screen (a window that moved, a page that scrolled, a
// new window) therefore comes out coarse, and if nothing changes after it
// the client keeps that coarse picture: letters cannot be read. When the
// encoder skipped the newest picture for the backlog, or VideoToolbox
// dropped it, the client does not even have it.
//
// So every refineEvery in which no new picture came, the encoder encodes
// the newest one again: each repeat spends the bitrate of the time since
// the last frame on what the frames before left coarse. It stops when
// quietRepeats repeats in a row came out small, which means there is
// nothing left to add, or after maxRefine. A new picture starts it over.
// Nothing runs while the screen is still and the picture is sharp (SPEC
// §11 rule 6).
//
// The repeats are a tenth of a second apart, not a frame's time: the
// bitrate is per second, so fewer repeats carry as much, and a client
// that decodes slowly is not kept busy by a still screen. It is also
// longer than the time between a film's frames, so a video is not padded
// with repeats.
//
// A run of small repeats alone does not mean the picture is sharp:
// VideoToolbox's rate control sends frames that add nothing while it has
// spent more than its bitrate, which is just when the picture is coarse
// (measured at 2 Mbit/s after a second of motion: over a second of such
// frames, the changed letters unreadable all the while). So the run ends
// the sharpening only when the frames of the last spendWindow took no
// more than a second's bitrate: then the rate control has bitrate to
// spend and chose not to.
const (
	// refineEvery is the time between two repeats, and how long no new
	// picture must have come before the first.
	refineEvery = 100 * time.Millisecond
	// quietRepeats is how many small repeats in a row end the sharpening.
	quietRepeats = 3
	// spendWindow is how far back the frames' bytes count against the
	// bitrate.
	spendWindow = 4 * time.Second
	// maxRefine is the longest a picture is sharpened.
	maxRefine = 10 * time.Second
	// spendSlots is how many frames the window remembers: more than
	// spendWindow holds at 60 a second.
	spendSlots = 256
	// What session.repeat returns beside an OSStatus.
	repeatSkipped = 1
	repeatNothing = 2
)

// smallFrame is the size in bytes under which a repeat added nothing: what
// a frame of width by height that only says "the same as before" takes,
// with room to spare.
func smallFrame(width, height int) int { return 1024 + width*height/1024 }

// refiner is the state of the sharpening.
type refiner struct {
	off   bool
	small int
	most  int // the most repeats of one picture
	// tick, when not nil, replaces the ticker's goroutine in tests: it is
	// called when the sharpening starts.
	tick func()

	mu      sync.Mutex
	running bool
	// seen is the session's count of pictures at the last tick.
	seen uint32
	// quiet counts the small repeats in a row, repeats the repeats of
	// this picture.
	quiet, repeats int
	// bitrate is the encoder's bitrate now.
	bitrate int
	// spent remembers when the last frames came out and their bytes, as
	// a ring; next is its next slot.
	spent [spendSlots]spend
	next  int
	// now is the clock.
	now func() time.Time
}

// spend is one frame's bytes and when it came out.
type spend struct {
	at    time.Time
	bytes int
}

// note remembers a frame of n bytes. The caller holds mu.
func (r *refiner) note(n int) {
	r.spent[r.next] = spend{at: r.now(), bytes: n}
	r.next = (r.next + 1) % spendSlots
}

// spare says the frames of the last spendWindow took no more than a
// second's bitrate, so the rate control has bitrate to spend. The caller
// holds mu.
func (r *refiner) spare() bool {
	since := r.now().Add(-spendWindow)
	total := 0
	for i := range r.spent {
		if r.spent[i].at.After(since) {
			total += r.spent[i].bytes
		}
	}
	return total <= r.bitrate/8
}

func (r *refiner) init(cfg config, off bool) {
	r.off = off
	r.small = smallFrame(cfg.width, cfg.height)
	r.most = int(maxRefine / refineEvery)
	r.bitrate = cfg.bitrate
	r.now = time.Now
}

// setBitrate notes the encoder's new bitrate.
func (r *refiner) setBitrate(bitsPerSecond int) {
	r.mu.Lock()
	r.bitrate = bitsPerSecond
	r.mu.Unlock()
}

// kick starts the sharpening unless it runs. fresh says the picture is a
// new one, or must go again whole (a key frame is wanted, VideoToolbox
// dropped the frame): what was sharpened so far does not count.
func (e *Encoder) kick(fresh bool) {
	r := &e.ref
	if r.off {
		return
	}
	r.mu.Lock()
	if fresh {
		r.quiet, r.repeats = 0, 0
	}
	start := !r.running
	if start {
		r.running = true
	}
	r.mu.Unlock()
	if !start {
		return
	}
	if r.tick != nil {
		r.tick()
		return
	}
	go e.refine()
}

// refined notes a frame that came out, for the sharpening.
func (e *Encoder) refined(o output) {
	r := &e.ref
	if r.off {
		return
	}
	r.mu.Lock()
	r.note(len(o.avcc))
	r.mu.Unlock()
	if !o.repeat {
		e.kick(true)
		return
	}
	r.mu.Lock()
	if len(o.avcc) <= r.small && !o.key {
		r.quiet++
	} else {
		r.quiet = 0
	}
	r.mu.Unlock()
}

// refine runs the sharpening until it has nothing left to do.
func (e *Encoder) refine() {
	t := time.NewTicker(refineEvery)
	defer t.Stop()
	for range t.C {
		if !e.refineTick() {
			return
		}
	}
}

// refineTick is one tick of the sharpening: it encodes the newest picture
// again when no new one came since the last tick. It returns false when the
// sharpening ends: the picture is sharp, the session skips (SetSkip starts
// it again), there is no picture, or the encoder is closed.
func (e *Encoder) refineTick() bool {
	r := &e.ref
	// Close frees the session: it is not used after closed is set.
	e.closeMu.Lock()
	defer e.closeMu.Unlock()
	r.mu.Lock()
	if e.closed {
		r.running = false
		r.mu.Unlock()
		return false
	}
	if c := e.s.captured(); c != r.seen {
		// The screen changes: the capture's own frames go.
		r.seen = c
		r.quiet, r.repeats = 0, 0
		r.mu.Unlock()
		return true
	}
	if r.quiet >= quietRepeats && r.spare() || r.repeats >= r.most {
		r.running = false
		r.mu.Unlock()
		return false
	}
	r.mu.Unlock()
	// Not under r.mu: the frame may come out before repeat returns.
	st := e.s.repeat()
	r.mu.Lock()
	defer r.mu.Unlock()
	switch st {
	case 0:
		r.repeats++
		return true
	case repeatSkipped, repeatNothing:
	default:
		e.log.Warn("VideoToolbox refused to encode the still picture again", "status", st)
	}
	r.running = false
	return false
}
