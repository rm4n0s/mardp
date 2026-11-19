package host

import (
	"context"
	"encoding/binary"
	"sync"
	"time"
)

// PatternVideo is a video source of stand-in H.264 frames: MaxFPS frames a
// second, each the size the bitrate gives a frame (a key frame three
// times that), so a slow client feels the bitrate. A frame's data is a
// start code, a NAL header (5 for a key frame, 1 otherwise), its number
// and the time it was made (PatternVideoFrame reads them), then zeros; it
// is captured and encoded when it is made. It keeps what the session asked
// of its encoder.
type PatternVideo struct {
	mu    sync.Mutex
	stats PatternVideoStats
	// skip and force are the encoder's state; bitrate its bitrate.
	skip, force bool
	bitrate     int
}

// PatternVideoStats is what a PatternVideo did and was asked.
type PatternVideoStats struct {
	// Captures is how many times CaptureVideo was called.
	Captures int
	// Config is what the last capture was asked for.
	Config VideoConfig
	// Frames and KeyFrames count the frames made; Skipped the frames not
	// made because the encoder skipped them.
	Frames, KeyFrames, Skipped int
	// Bitrates are the bitrates the session set, in order.
	Bitrates []int
	// Forced counts ForceKeyFrame calls.
	Forced int
}

// patternVideoHeader is the length of what PatternVideoFrame reads.
const patternVideoHeader = 5 + 8 + 8

// Stats returns what v did and was asked so far.
func (v *PatternVideo) Stats() PatternVideoStats {
	v.mu.Lock()
	defer v.mu.Unlock()
	st := v.stats
	st.Bitrates = append([]int(nil), st.Bitrates...)
	return st
}

// PatternVideoFrame reads a frame of PatternVideo: its number from 0, the
// time it was made and whether it is a key frame; ok is false when data
// is not one.
func PatternVideoFrame(data []byte) (n uint64, made time.Time, key, ok bool) {
	if len(data) < patternVideoHeader || data[0] != 0 || data[1] != 0 || data[2] != 0 || data[3] != 1 {
		return 0, time.Time{}, false, false
	}
	n = binary.BigEndian.Uint64(data[5:])
	made = time.Unix(0, int64(binary.BigEndian.Uint64(data[13:])))
	return n, made, data[4] == 5, true
}

// CaptureVideo implements VideoSource.
func (v *PatternVideo) CaptureVideo(ctx context.Context, cfg VideoConfig, started func(VideoEncoder), frame func(*EncodedFrame)) error {
	fps := max(cfg.MaxFPS, 1)
	v.mu.Lock()
	v.stats.Captures++
	v.stats.Config = cfg
	v.skip, v.force, v.bitrate = false, true, cfg.Bitrate
	v.mu.Unlock()
	started(patternEncoder{v})
	var buf []byte
	t := time.NewTicker(time.Second / time.Duration(fps))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		v.mu.Lock()
		if v.skip {
			v.stats.Skipped++
			v.mu.Unlock()
			continue
		}
		key := v.force
		v.force = false
		size := max(v.bitrate/8/fps, patternVideoHeader)
		if key {
			size *= 3
			v.stats.KeyFrames++
		}
		n := v.stats.Frames
		v.stats.Frames++
		v.mu.Unlock()
		buf = append(buf[:0], make([]byte, size)...)
		copy(buf, []byte{0, 0, 0, 1, 1})
		if key {
			buf[4] = 5
		}
		now := time.Now()
		binary.BigEndian.PutUint64(buf[5:], uint64(n))
		binary.BigEndian.PutUint64(buf[13:], uint64(now.UnixNano()))
		frame(&EncodedFrame{Data: buf, Key: key, Captured: now, Encoded: now})
	}
}

// patternEncoder is the VideoEncoder of a PatternVideo's capture.
type patternEncoder struct{ v *PatternVideo }

func (e patternEncoder) SetBitrate(bitsPerSecond int) error {
	e.v.mu.Lock()
	defer e.v.mu.Unlock()
	e.v.bitrate = bitsPerSecond
	e.v.stats.Bitrates = append(e.v.stats.Bitrates, bitsPerSecond)
	return nil
}

func (e patternEncoder) ForceKeyFrame() {
	e.v.mu.Lock()
	defer e.v.mu.Unlock()
	e.v.force = true
	e.v.stats.Forced++
}

func (e patternEncoder) SetSkip(skip bool) {
	e.v.mu.Lock()
	defer e.v.mu.Unlock()
	e.v.skip = skip
}
