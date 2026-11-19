package host

import (
	"context"
	"fmt"
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

func (f *fakeTarget) Move(x, y int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, fmt.Sprintf("move %d,%d", x, y))
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
	p := newPointerPoll(src, dst, slog.New(rec), time.Now)
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
			p := newPointerPoll(src, dst, slog.New(slog.DiscardHandler), time.Now)
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
	p := newPointerPoll(src, &fakeTarget{}, slog.New(slog.DiscardHandler), time.Now)
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

func TestPointerPollPosition(t *testing.T) {
	src := &StaticPointer{}
	dst := &fakeTarget{}
	rec := &recorder{}
	now := time.Unix(1000, 0)
	p := newPointerPoll(src, dst, slog.New(rec), func() time.Time { return now })
	p.px, p.py = 0.5, 0.5
	var cx, cy float64
	var moves uint64
	p.client = func() (float64, float64, uint64) { return cx, cy, moves }
	at := func(x, y float64) PointerShape { return PointerShape{OnDisplay: true, X: x, Y: y} }
	clientMoves := func(x, y float64) {
		cx, cy = x, y
		moves++
		p.afterMouse()
	}
	steps := []struct {
		name   string
		before func()
		shape  PointerShape
		fail   error
		want   []string
	}{
		{"the start puts the client's pointer", nil, at(100, 200), nil, []string{"move 50,100", "hide"}},
		{"not moved", nil, at(100, 200), nil, nil},
		{"the Mac moved it", nil, at(300, 201), nil, []string{"move 150,101"}},
		{"the client moved it: too soon", func() { clientMoves(400, 400) }, at(410, 400), nil, nil},
		{"settled where the client put it", func() { now = now.Add(pointerSettle) }, at(400, 400), nil, nil},
		{"within the slack", nil, at(401, 401.5), nil, nil},
		{"the Mac did not follow", func() { clientMoves(500, 500); now = now.Add(pointerSettle) }, at(400, 400), nil, []string{"move 200,200"}},
		{"off the display", nil, PointerShape{X: 9, Y: 9}, nil, nil},
		{"queue full", nil, at(10, 10), errors.New(rdp.TagQueueFull, "full"), []string{"move 5,5"}},
		{"tried again", nil, at(10, 10), nil, []string{"move 5,5"}},
		{"other failure", nil, at(20, 20), errors.New("TestMoveFailed", "bad"), []string{"move 10,10"}},
		{"not tried again", nil, at(20, 20), nil, nil},
		{"another failure is not logged", nil, at(30, 30), errors.New("TestMoveFailed", "bad"), []string{"move 15,15"}},
	}
	for _, s := range steps {
		if s.before != nil {
			s.before()
		}
		src.Set(s.shape, nil)
		dst.mu.Lock()
		dst.fail, dst.got = s.fail, nil
		dst.mu.Unlock()
		p.poll()
		if got := dst.sent(); !slices.Equal(got, s.want) {
			t.Fatalf("%s: sent %v, want %v", s.name, got, s.want)
		}
	}
	warns := rec.find("the pointer's position was not sent")
	if len(warns) != 1 {
		t.Fatalf("logged %d times, want once", len(warns))
	}
	err, _ := attr(warns[0], "err").Any().(error)
	assertRoute(t, err, "TestPointerPollPosition.TestMoveFailed")
}

func TestPointerPollPositionStops(t *testing.T) {
	src := &StaticPointer{}
	src.Set(PointerShape{OnDisplay: true, X: 1, Y: 1}, nil)
	dst := &fakeTarget{fail: errors.New(rdp.TagFastPathUnsupported, "no")}
	p := newPointerPoll(src, dst, slog.New(slog.DiscardHandler), time.Now)
	p.px, p.py = 1, 1
	p.poll()
	if !p.off {
		t.Fatal("the poller goes on")
	}
	if got := dst.sent(); !slices.Equal(got, []string{"move 1,1"}) {
		t.Errorf("sent %v", got)
	}
}

func TestPointerPollNoScale(t *testing.T) {
	src := &StaticPointer{}
	src.Set(PointerShape{OnDisplay: true, X: 1, Y: 1}, nil)
	dst := &fakeTarget{}
	p := newPointerPoll(src, dst, slog.New(slog.DiscardHandler), time.Now)
	p.poll()
	if got := dst.sent(); !slices.Equal(got, []string{"hide"}) {
		t.Errorf("sent %v", got)
	}
}
