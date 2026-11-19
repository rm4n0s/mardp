package config

import (
	"fmt"
	"testing"
	"time"
)

func TestGuard(t *testing.T) {
	clock := time.Unix(1_000_000, 0)
	g := NewGuard(func() time.Time { return clock })
	const addr = "10.0.0.7:50000"
	checked := 0
	login := func(addr string, ok bool) LoginResult {
		return g.Login(addr, func() bool { checked++; return ok })
	}
	type step struct {
		advance  time.Duration
		addr     string
		ok       bool
		want     LoginResult
		checking bool
	}
	steps := []step{
		{0, addr, false, LoginResult{Failures: 1}, true},
		{0, addr, false, LoginResult{Failures: 2}, true},
		{0, addr, false, LoginResult{Failures: 3}, true},
		{0, addr, false, LoginResult{Failures: 4}, true},
		// The fifth failure: a minute's wait. Another port of the same
		// machine waits too, without its password checked.
		{0, addr, false, LoginResult{Failures: 5, Wait: time.Minute}, true},
		{30 * time.Second, "10.0.0.7:50001", true, LoginResult{Failures: 5, Wait: 30 * time.Second}, false},
		// Another machine does not.
		{0, "10.0.0.8:50000", false, LoginResult{Failures: 1}, true},
		// After the wait, each failure doubles it.
		{30 * time.Second, addr, false, LoginResult{Failures: 6, Wait: 2 * time.Minute}, true},
		{2 * time.Minute, addr, false, LoginResult{Failures: 7, Wait: 4 * time.Minute}, true},
		{4 * time.Minute, addr, false, LoginResult{Failures: 8, Wait: 8 * time.Minute}, true},
		{8 * time.Minute, addr, false, LoginResult{Failures: 9, Wait: 16 * time.Minute}, true},
		{16 * time.Minute, addr, false, LoginResult{Failures: 10, Wait: 32 * time.Minute}, true},
		// Up to an hour.
		{32 * time.Minute, addr, false, LoginResult{Failures: 11, Wait: time.Hour}, true},
		{time.Hour, addr, false, LoginResult{Failures: 12, Wait: time.Hour}, true},
		// A success forgets the failures.
		{time.Hour, addr, true, LoginResult{OK: true}, true},
		{0, addr, false, LoginResult{Failures: 1}, true},
		// So does a day without a failure, even while waiting.
		{0, addr, false, LoginResult{Failures: 2}, true},
		{0, addr, false, LoginResult{Failures: 3}, true},
		{0, addr, false, LoginResult{Failures: 4}, true},
		{0, addr, false, LoginResult{Failures: 5, Wait: time.Minute}, true},
		{24 * time.Hour, addr, false, LoginResult{Failures: 1}, true},
		// An address without a port.
		{0, "fe80::1", false, LoginResult{Failures: 1}, true},
		{0, "[fe80::1]:3000", false, LoginResult{Failures: 2}, true},
	}
	for i, s := range steps {
		clock = clock.Add(s.advance)
		before := checked
		got := login(s.addr, s.ok)
		if got.Checked != s.checking {
			t.Fatalf("step %d: Checked %v, want %v", i, got.Checked, s.checking)
		}
		got.Checked = false
		if got != s.want {
			t.Fatalf("step %d: %+v, want %+v", i, got, s.want)
		}
		if (checked > before) != s.checking {
			t.Fatalf("step %d: checked %v, want %v", i, checked > before, s.checking)
		}
	}
}

func TestGuardRoom(t *testing.T) {
	clock := time.Unix(1_000_000, 0)
	g := NewGuard(func() time.Time { return clock })
	for i := range maxAddresses {
		clock = clock.Add(time.Second)
		g.Login(fmt.Sprintf("10.%d.%d.1:1", i/256, i%256), func() bool { return false })
	}
	if len(g.addrs) != maxAddresses {
		t.Fatalf("%d addresses", len(g.addrs))
	}
	// One more drops the oldest.
	clock = clock.Add(time.Second)
	g.Login("192.168.0.1:1", func() bool { return false })
	if len(g.addrs) != maxAddresses {
		t.Errorf("%d addresses, want %d", len(g.addrs), maxAddresses)
	}
	if _, ok := g.addrs["10.0.0.1"]; ok {
		t.Error("the oldest address was kept")
	}
	if _, ok := g.addrs["10.0.1.1"]; !ok {
		t.Error("the second oldest was dropped")
	}
	// A day later, every old one goes at once.
	clock = clock.Add(forgetAfter)
	g.Login("192.168.0.2:1", func() bool { return false })
	if len(g.addrs) != 1 {
		t.Errorf("%d addresses after a day, want 1", len(g.addrs))
	}
}

func TestGuardDefaultClock(t *testing.T) {
	g := NewGuard(nil)
	if r := g.Login("a:1", func() bool { return true }); !r.OK {
		t.Error(r)
	}
}
