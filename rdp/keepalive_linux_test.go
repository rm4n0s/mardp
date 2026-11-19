package rdp

import (
	"net"
	"syscall"
	"testing"
)

// keepAliveSettings reads the keep-alive options of tc from its socket.
func keepAliveSettings(t *testing.T, tc *net.TCPConn) (on bool, idle, interval, count int) {
	t.Helper()
	return readKeepAlive(t, tc, syscall.TCP_KEEPIDLE)
}
