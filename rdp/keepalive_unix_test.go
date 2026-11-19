//go:build darwin || linux

package rdp

import (
	"net"
	"syscall"
	"testing"
)

// readKeepAlive reads SO_KEEPALIVE, the idle time (the option idleOpt), the
// interval and the count of probes from tc's socket.
func readKeepAlive(t *testing.T, tc *net.TCPConn, idleOpt int) (on bool, idle, interval, count int) {
	t.Helper()
	rc, err := tc.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var opts [4]int
	var serr error
	err = rc.Control(func(fd uintptr) {
		for i, o := range [][2]int{
			{syscall.SOL_SOCKET, syscall.SO_KEEPALIVE},
			{syscall.IPPROTO_TCP, idleOpt},
			{syscall.IPPROTO_TCP, syscall.TCP_KEEPINTVL},
			{syscall.IPPROTO_TCP, syscall.TCP_KEEPCNT},
		} {
			if opts[i], serr = syscall.GetsockoptInt(int(fd), o[0], o[1]); serr != nil {
				return
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if serr != nil {
		t.Fatal(serr)
	}
	return opts[0] != 0, opts[1], opts[2], opts[3]
}
