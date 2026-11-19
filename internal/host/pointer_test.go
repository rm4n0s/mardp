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
)

// fakeTarget records what the poller sends.
type fakeTarget struct {
	mu   sync.Mutex
	got  []string
	fail error
}

func (f *fakeTarget) SetShape(img *rdp.Image, hotX, hotY int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, "shape")
	return f.fail
}

func (f *fakeTarget) Hide() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, "hide")
	return f.fail
}

func (f *fakeTarget) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.got)
}

func TestPointerPoll(t *testing.T) {
	src := &StaticPointer{}
	dst := &fakeTarget{}
	rec := &recorder{}
	p := newPointerPoll(src, dst, slog.New(rec))
	steps := []struct {
		name  string
		shape PointerShape
		err   error
		fail  error
		want  []string
	}{
		{"first shape", testShape(1), nil, nil, []string{"shape"}},
		{"same shape", testShape(1), nil, nil, []string{"shape"}},
		{"new shape", testShape(2), nil, nil, []string{"shape", "shape"}},
		{"hidden", PointerShape{}, nil, nil, []string{"shape", "shape", "hide"}},
		{"still hidden", PointerShape{Hash: 9}, nil, nil, []string{"shape", "shape", "hide"}},
		{"queue full", testShape(3), nil, errors.New(rdp.TagQueueFull, "full"), []string{"shape", "shape", "hide", "shape"}},
		{"tried again", testShape(3), nil, nil, []string{"shape", "shape", "hide", "shape", "shape"}},
		{"read fails", testShape(4), errors.New("TestPointerFailed", "no pointer"), nil, []string{"shape", "shape", "hide", "shape", "shape"}},
		{"read fails again", testShape(4), errors.New("TestPointerFailed", "no pointer"), nil, []string{"shape", "shape", "hide", "shape", "shape"}},
		{"other send failure", testShape(5), nil, errors.New("TestShapeFailed", "bad"), []string{"shape", "shape", "hide", "shape", "shape", "shape"}},
		{"not tried again", testShape(5), nil, nil, []string{"shape", "shape", "hide", "shape", "shape", "shape"}},
	}
	for _, s := range steps {
		src.Set(s.shape, s.err)
		dst.mu.Lock()
		dst.fail = s.fail
		dst.mu.Unlock()
		p.poll()
		if got := dst.sent(); !slices.Equal(got, s.want) {
			t.Fatalf("%s: sent %v, want %v", s.name, got, s.want)
		}
		if p.off {
			t.Fatalf("%s: the poller stopped", s.name)
		}
	}
	warns := rec.find("the pointer could not be read")
	if len(warns) != 1 {
		t.Fatalf("read failure logged %d times, want once", len(warns))
	}
	err, _ := attr(warns[0], "err").Any().(error)
	assertRoute(t, err, "TestPointerPoll.TestPointerFailed")
	warns = rec.find("the pointer was not sent")
	if len(warns) != 1 {
		t.Fatalf("send failure logged %d times, want once", len(warns))
	}
	err, _ = attr(warns[0], "err").Any().(error)
	assertRoute(t, err, "TestPointerPoll.TestShapeFailed")
}

func TestPointerPollStops(t *testing.T) {
	for _, tag := range []string{rdp.TagFastPathUnsupported, rdp.TagSessionOver} {
		t.Run(tag, func(t *testing.T) {
			src := &StaticPointer{}
			src.Set(testShape(1), nil)
			dst := &fakeTarget{fail: errors.New(tag, "no")}
			p := newPointerPoll(src, dst, slog.New(slog.DiscardHandler))
			done := make(chan struct{})
			go func() {
				p.run(context.Background())
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("the poller did not stop")
			}
		})
	}
}

func TestPointerPollWakes(t *testing.T) {
	src := &StaticPointer{}
	p := newPointerPoll(src, &fakeTarget{}, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.run(ctx)
		close(done)
	}()
	waitReads := func(n int) {
		t.Helper()
		deadline := time.Now().Add(pointerInterval / 2)
		for src.Reads() < n {
			if time.Now().After(deadline) {
				t.Fatalf("%d reads, want %d before the timer", src.Reads(), n)
			}
			time.Sleep(time.Millisecond)
		}
	}
	waitReads(1) // at the start
	p.kickNow()  // after a frame
	waitReads(2)
	p.afterMouse()
	waitReads(3)
	// Then the timer.
	time.Sleep(pointerInterval + 50*time.Millisecond)
	if src.Reads() < 4 {
		t.Errorf("%d reads, want the timer's too", src.Reads())
	}
	cancel()
	<-done
}
