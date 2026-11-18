// Command recorder accepts TCP connections and saves the first bytes each
// client sends (the TPKT with the X.224 Connection Request) to a file, then
// closes the connection. Throwaway (M0, IMPLEMENTATION.md step 0.4); deleted
// in step 0.9.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	addr := flag.String("addr", "0.0.0.0:13389", "listen address")
	out := flag.String("out", "spikes/out/recordings", "folder for the recordings")
	n := flag.Int("n", 1, "connections to record, then exit")
	wait := flag.Duration("wait", 2*time.Minute, "give up after this long")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("listening on", ln.Addr())
	go func() { time.Sleep(*wait); fmt.Fprintln(os.Stderr, "timed out"); os.Exit(3) }()
	for i := 0; i < *n; i++ {
		c, err := ln.Accept()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
		hdr := make([]byte, 4)
		if _, err := io.ReadFull(c, hdr); err != nil {
			fmt.Fprintln(os.Stderr, "header:", err)
			_ = c.Close()
			continue
		}
		size := int(binary.BigEndian.Uint16(hdr[2:]))
		body := make([]byte, max(size-4, 0))
		if _, err := io.ReadFull(c, body); err != nil {
			fmt.Fprintln(os.Stderr, "body:", err)
		}
		_ = c.Close()
		name := filepath.Join(*out, fmt.Sprintf("%s-%d.bin", time.Now().Format("150405"), i))
		if err := os.WriteFile(name, append(hdr, body...), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("recorded %d bytes from %s into %s\n", size, c.RemoteAddr(), name)
	}
}
