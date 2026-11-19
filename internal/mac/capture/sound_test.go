package capture

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/rm4n0s/mardp/internal/host"
)

// TestSoundCapture: a sound stream leaves out the default apps and the
// process's own sound, asks for a small picture it drops, and is started
// again when the apps it leaves out change.
func TestSoundCapture(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want []string
	}{
		{"UTM by default", Options{}, DefaultExcludeApps},
		{"the apps given", Options{ExcludeApps: []string{"com.example.a", "org.b."}}, []string{"com.example.a", "org.b."}},
		{"none", Options{ExcludeApps: []string{}}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeMac("M", map[string]uint32{"M": 1})
			f.apps = 1
			s := newSource(tt.opts, f)
			s.wait, s.retry, s.recheck = 10*time.Millisecond, time.Millisecond, 0
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- (&Sound{src: s}).Capture(ctx, func(*host.SoundBuffer) {}) }()
			st := f.next(t)
			want := streamMode{sound: true, exclude: tt.want}
			if !reflect.DeepEqual(st.mode, want) {
				t.Errorf("stream mode %+v, want %+v", st.mode, want)
			}
			if st.cfg != (host.ScreenConfig{Width: soundSide, Height: soundSide, MaxFPS: 1}) {
				t.Errorf("stream config %+v", st.cfg)
			}
			if st.sk.frame != nil || st.sk.sound == nil {
				t.Error("a sound stream without its sound function")
			}
			// ScreenCaptureKit does not answer: the stream goes on.
			f.set(func(f *fakeMac) { f.apps = -1 })
			time.Sleep(50 * time.Millisecond)
			if _, live := f.state(); live != 1 {
				t.Fatalf("%d streams live", live)
			}
			// UTM opens a window: a new stream leaves it out.
			f.set(func(f *fakeMac) { f.apps = 2 })
			st2 := f.next(t)
			if st2.apps != 2 || !reflect.DeepEqual(st2.mode, want) {
				t.Errorf("new stream: %d apps, mode %+v", st2.apps, st2.mode)
			}
			cancel()
			if err := wait(t, done); err != nil {
				t.Fatal(err)
			}
			if _, live := f.state(); live != 0 {
				t.Errorf("%d streams live", live)
			}
		})
	}
}

// TestDeliverSound: buffers go to the sound function with their time from
// the capture's first buffer, silent ones and broken ones do not.
func TestDeliverSound(t *testing.T) {
	type got struct {
		left, right []float32
		t           time.Duration
	}
	var calls []got
	sk := &sink{sound: func(b *host.SoundBuffer) {
		calls = append(calls, got{slices.Clone(b.Left), slices.Clone(b.Right), b.Time})
	}}
	quiet := make([]float32, 4)
	loud := []float32{0, 0.5, -0.25, 0}
	steps := []struct {
		name        string
		left, right []float32
		t           time.Duration
		want        *got
	}{
		{"the first is silent, but sets the start", quiet, quiet, 10 * time.Second, nil},
		{"sound", loud, quiet, 10*time.Second + 20*time.Millisecond, &got{loud, quiet, 20 * time.Millisecond}},
		{"sound on the right", quiet, loud, 10*time.Second + 40*time.Millisecond, &got{quiet, loud, 40 * time.Millisecond}},
		{"silence", quiet, quiet, 10*time.Second + 60*time.Millisecond, nil},
		{"channels of different lengths", loud, loud[:2], 10*time.Second + 80*time.Millisecond, nil},
		{"empty", nil, nil, 10*time.Second + 100*time.Millisecond, nil},
		{"before the start: at 0", loud, loud, 9 * time.Second, &got{loud, loud, 0}},
	}
	for _, st := range steps {
		before := len(calls)
		sk.deliverSound(st.left, st.right, st.t)
		if (len(calls) > before) != (st.want != nil) {
			t.Fatalf("%s: called %v", st.name, len(calls) > before)
		}
		if st.want != nil && !reflect.DeepEqual(calls[len(calls)-1], *st.want) {
			t.Errorf("%s: %+v, want %+v", st.name, calls[len(calls)-1], *st.want)
		}
	}
	if sk.snd.Left != nil || sk.snd.Right != nil {
		t.Error("the sink keeps the buffer after the call")
	}
}

func TestDeliverSoundAllocs(t *testing.T) {
	sk := &sink{sound: func(*host.SoundBuffer) {}}
	l, r := make([]float32, 960), make([]float32, 960)
	l[5] = 0.1
	if n := testing.AllocsPerRun(100, func() { sk.deliverSound(l, r, time.Second) }); n != 0 {
		t.Errorf("%v allocations per buffer", n)
	}
}
