package config

import (
	"net"
	"sync"
	"time"
)

// The waiting rule for failed logins (SPEC §8.2).
const (
	// FreeFailures is how many failed logins an address has before it
	// waits.
	FreeFailures = 5
	// FirstWait is the first wait; each failure after it doubles the wait,
	// up to MaxWait.
	FirstWait = time.Minute
	MaxWait   = time.Hour
	// forgetAfter is how long after its last failure an address starts
	// again from no failures.
	forgetAfter = 24 * time.Hour
	// maxAddresses bounds the addresses kept; the oldest go first.
	maxAddresses = 4096
)

// Guard keeps the failed logins of each address and makes an address wait
// after FreeFailures of them. Its methods may be called from any goroutine.
type Guard struct {
	now func() time.Time

	mu    sync.Mutex
	addrs map[string]*failures
}

// failures are the failed logins of one address.
type failures struct {
	count int
	last  time.Time // the last failure
	until time.Time // the end of the wait
}

// NewGuard makes a Guard with the clock now; nil is time.Now.
func NewGuard(now func() time.Time) *Guard {
	if now == nil {
		now = time.Now
	}
	return &Guard{now: now, addrs: map[string]*failures{}}
}

// host is the address without its port: every connection of one machine
// counts together.
func host(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// LoginResult is what Guard.Login decided.
type LoginResult struct {
	// OK says the login succeeded.
	OK bool
	// Checked says that the password was checked: false when the login
	// was refused because the address waits.
	Checked bool
	// Wait is how long the address must still wait; after a failure that
	// was checked, the wait it starts, if any.
	Wait time.Duration
	// Failures is the address's count of failed logins after this one.
	Failures int
}

// Login runs check, which says whether the login is right, for a client at
// addr, unless addr is waiting. A failure counts; a success forgets the
// address's failures.
func (g *Guard) Login(addr string, check func() bool) LoginResult {
	h := host(addr)
	g.mu.Lock()
	now := g.now()
	if f, ok := g.addrs[h]; ok {
		if now.Sub(f.last) >= forgetAfter {
			delete(g.addrs, h)
		} else if wait := f.until.Sub(now); wait > 0 {
			g.mu.Unlock()
			return LoginResult{Wait: wait, Failures: f.count}
		}
	}
	g.mu.Unlock()

	// The check is slow (PBKDF2): it runs without the lock.
	ok := check()

	g.mu.Lock()
	defer g.mu.Unlock()
	now = g.now()
	if ok {
		delete(g.addrs, h)
		return LoginResult{OK: true, Checked: true}
	}
	f := g.addrs[h]
	if f == nil {
		g.makeRoom(now)
		f = &failures{}
		g.addrs[h] = f
	}
	f.count++
	f.last = now
	if f.count >= FreeFailures {
		wait := MaxWait
		if n := f.count - FreeFailures; n < 6 {
			wait = min(FirstWait<<n, MaxWait)
		}
		f.until = now.Add(wait)
	}
	return LoginResult{Checked: true, Failures: f.count, Wait: max(f.until.Sub(now), 0)}
}

// makeRoom drops forgotten addresses, and the oldest one when the map is
// still full. The caller holds mu.
func (g *Guard) makeRoom(now time.Time) {
	if len(g.addrs) < maxAddresses {
		return
	}
	var oldest string
	for h, f := range g.addrs {
		if now.Sub(f.last) >= forgetAfter {
			delete(g.addrs, h)
			continue
		}
		if oldest == "" || f.last.Before(g.addrs[oldest].last) {
			oldest = h
		}
	}
	if len(g.addrs) >= maxAddresses {
		delete(g.addrs, oldest)
	}
}
