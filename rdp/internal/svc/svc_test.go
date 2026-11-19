package svc

import (
	"bytes"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/rdp/internal/wire"
)

// msExample is the decrypted static virtual channel data of MS-RDPBCGR 4.4,
// Annotated Server-to-Client Virtual Channel PDU (on "cliprdr"): a Channel
// PDU Header of length 8 with FIRST and LAST, then 8 bytes of data.
// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/e84e3ace-266a-485b-85cb-da6418152915
var msExample = []byte{
	0x08, 0x00, 0x00, 0x00, 0x03, 0x00, 0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00,
}

func TestMicrosoftExample(t *testing.T) {
	r := wire.NewReader(msExample)
	h, err := ReadHeader(&r)
	if err != nil {
		t.Fatal(err)
	}
	if h != (Header{Length: 8, Flags: FlagFirst | FlagLast}) {
		t.Errorf("header %+v", h)
	}
	j := Joiner{Max: 8}
	msg, ok, err := j.Add(h, r.Rest())
	if err != nil || !ok || !bytes.Equal(msg, msExample[8:]) {
		t.Fatalf("joined %x %v %v", msg, ok, err)
	}

	// Splitting the data gives the same bytes back.
	w := wire.NewWriter(nil)
	err = Split(msExample[8:], ChunkLength, func(h Header, data []byte) error {
		WriteHeader(&w, h)
		w.Write(data)
		return nil
	})
	if err != nil || !bytes.Equal(w.Bytes(), msExample) {
		t.Errorf("split %x %v", w.Bytes(), err)
	}
}

func TestReadHeaderShort(t *testing.T) {
	for n := range HeaderLen {
		r := wire.NewReader(msExample[:n])
		if _, err := ReadHeader(&r); err == nil {
			t.Errorf("%d bytes read as a header", n)
		}
	}
}

// chunk is what Split hands out: a header and the chunk's length.
type chunk struct {
	h Header
	n int
}

func split(t *testing.T, msg []byte, size int) []chunk {
	t.Helper()
	var got []chunk
	if err := Split(msg, size, func(h Header, data []byte) error { got = append(got, chunk{h, len(data)}); return nil }); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSplit(t *testing.T) {
	tests := []struct {
		name string
		len  int
		size int
		want []chunk
	}{
		// MS-RDPBCGR 3.1.5.2.2.1: 2,062 bytes in chunks of 1,000.
		// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/a542bf19-1c86-4c80-ab3e-61449653abf6
		{"Microsoft's example", 2062, 1000, []chunk{{Header{2062, FlagFirst}, 1000}, {Header{2062, 0}, 1000}, {Header{2062, FlagLast}, 62}}},
		{"empty", 0, ChunkLength, []chunk{{Header{0, FlagFirst | FlagLast}, 0}}},
		{"one chunk", ChunkLength, ChunkLength, []chunk{{Header{ChunkLength, FlagFirst | FlagLast}, ChunkLength}}},
		{"one byte more", ChunkLength + 1, ChunkLength, []chunk{{Header{ChunkLength + 1, FlagFirst}, ChunkLength}, {Header{ChunkLength + 1, FlagLast}, 1}}},
		{"two whole", 2 * ChunkLength, ChunkLength, []chunk{{Header{2 * ChunkLength, FlagFirst}, ChunkLength}, {Header{2 * ChunkLength, FlagLast}, ChunkLength}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := split(t, make([]byte, tt.len), tt.size); !slices.Equal(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSplitFails(t *testing.T) {
	err := Split([]byte{1}, 0, func(Header, []byte) error { return nil })
	route(t, err, "TestSplitFails->Split."+TagChunkSizeInvalid)

	stop := errors.New("TestStop", "stop")
	if err := Split(make([]byte, 3000), ChunkLength, func(Header, []byte) error { return stop }); !errors.Is(err, stop) {
		t.Errorf("the chunk's error: %v", err)
	}
}

func route(t *testing.T, err error, want string) {
	t.Helper()
	appErr, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("expected *errors.Error, got %T (%v)", err, err)
	}
	if !appErr.HasRoute(want) {
		t.Errorf("unexpected failure path: %s, want %s", appErr.Route(), want)
	}
}

// TestJoinRoundTrip splits seeded random messages into random chunk sizes
// and joins them with one joiner, which keeps its buffer.
func TestJoinRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	j := Joiner{Max: 1 << 16}
	for i := range 200 {
		msg := make([]byte, rng.IntN(1<<16))
		for k := range msg {
			msg[k] = byte(rng.Uint32())
		}
		size := 1 + rng.IntN(4000)
		var got []byte
		n := 0
		err := Split(msg, size, func(h Header, data []byte) error {
			whole, ok, err := j.Add(h, data)
			if err != nil {
				return err
			}
			if ok {
				got = slices.Clone(whole)
				n++
			}
			return nil
		})
		if err != nil || n != 1 || !bytes.Equal(got, msg) {
			t.Fatalf("message %d (%d bytes, chunks of %d): %d joined, %v", i, len(msg), size, n, err)
		}
	}
}

func TestJoinSequences(t *testing.T) {
	c := func(length, flags uint32, data ...byte) chunk { return chunk{Header{length, flags}, len(data)} }
	tests := []struct {
		name  string
		seq   []chunk
		route string // the failure of the last chunk, "" when it completes the message
		want  int    // the message's length
	}{
		{"one chunk", []chunk{c(2, FlagFirst|FlagLast, 1, 2)}, "", 2},
		{"show protocol", []chunk{c(2, FlagFirst|FlagShowProtocol, 1), c(2, FlagLast|FlagShowProtocol, 2)}, "", 2},
		{"three", []chunk{c(3, FlagFirst, 1), c(3, 0, 2), c(3, FlagLast, 3)}, "", 3},
		{"suspend skipped", []chunk{c(2, FlagFirst, 1), c(0, FlagSuspend), c(0, FlagResume), c(2, FlagLast, 2)}, "", 2},
		{"empty", []chunk{c(0, FlagFirst|FlagLast)}, "", 0},
		{"at the limit", []chunk{c(4, FlagFirst, 1, 2), c(4, FlagLast, 3, 4)}, "", 4},
		{"compressed", []chunk{c(1, FlagFirst|FlagLast|FlagCompressed, 1)}, "Joiner.Add." + TagCompressed, 0},
		{"compressed later", []chunk{c(2, FlagFirst, 1), c(2, FlagLast|FlagCompressed, 2)}, "Joiner.Add." + TagCompressed, 0},
		{"first inside", []chunk{c(2, FlagFirst, 1), c(2, FlagFirst, 2)}, "Joiner.Add." + TagChunkSequence, 0},
		{"no first", []chunk{c(2, 0, 1)}, "Joiner.Add." + TagChunkSequence, 0},
		{"last without first", []chunk{c(2, FlagLast, 1)}, "Joiner.Add." + TagChunkSequence, 0},
		{"too large", []chunk{c(5, FlagFirst, 1)}, "Joiner.Add." + TagMessageTooLarge, 0},
		{"too large in one", []chunk{c(5, FlagFirst|FlagLast, 1, 2, 3, 4, 5)}, "Joiner.Add." + TagMessageTooLarge, 0},
		{"huge", []chunk{c(0xffffffff, FlagFirst, 1)}, "Joiner.Add." + TagMessageTooLarge, 0},
		{"one chunk short", []chunk{c(2, FlagFirst|FlagLast, 1)}, "Joiner.Add." + TagLengthInvalid, 0},
		{"one chunk long", []chunk{c(1, FlagFirst|FlagLast, 1, 2)}, "Joiner.Add." + TagLengthInvalid, 0},
		{"another total", []chunk{c(3, FlagFirst, 1), c(4, FlagLast, 2, 3)}, "Joiner.Add." + TagLengthInvalid, 0},
		{"longer than the total", []chunk{c(2, FlagFirst, 1), c(2, 0, 2, 3)}, "Joiner.Add." + TagLengthInvalid, 0},
		{"shorter than the total", []chunk{c(3, FlagFirst, 1), c(3, FlagLast, 2)}, "Joiner.Add." + TagLengthInvalid, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j := Joiner{Max: 4}
			for i, ch := range tt.seq {
				msg, ok, err := j.Add(ch.h, make([]byte, ch.n))
				if i < len(tt.seq)-1 {
					if err != nil || ok {
						t.Fatalf("chunk %d: ok %v err %v", i, ok, err)
					}
					continue
				}
				if tt.route != "" {
					if ok {
						t.Fatal("joined")
					}
					route(t, err, tt.route)
					// The joiner starts over with the next first chunk.
					if _, ok, err := j.Add(Header{1, FlagFirst | FlagLast}, []byte{9}); !ok || err != nil {
						t.Errorf("after the failure: ok %v err %v", ok, err)
					}
					return
				}
				if err != nil || !ok || len(msg) != tt.want {
					t.Fatalf("joined %d bytes, ok %v err %v", len(msg), ok, err)
				}
			}
		})
	}
}
