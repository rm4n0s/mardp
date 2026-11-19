package wire

import (
	"testing"
)

// mixed is one of every form, as writeMixed writes it.
var mixed = func() []byte {
	w := NewWriter(make([]byte, 0, 128))
	if err := writeMixed(&w); err != nil {
		panic(err)
	}
	return w.Bytes()
}()

func writeMixed(w *Writer) error {
	w.U8(1)
	w.U16LE(2)
	w.U16BE(3)
	w.U32LE(4)
	w.U32BE(5)
	w.U64LE(6)
	if err := w.PERLength(300); err != nil {
		return err
	}
	if err := w.PERInteger16(1007, 1001); err != nil {
		return err
	}
	w.PERUint(70000)
	w.BERTag(BERTag{Class: BERApplication, Constructed: true, Number: 101})
	off := w.BERReserveLength()
	start := w.Len()
	w.BERInteger(65535)
	w.BEREnumerated(2)
	w.BERBoolean(true)
	if err := w.BEROctetString([]byte{1, 2, 3, 4}); err != nil {
		return err
	}
	return w.PatchBERLength(off, w.Len()-start)
}

func readMixed(r *Reader) (uint64, error) {
	var sum uint64
	a, err := r.U8()
	if err != nil {
		return 0, err
	}
	b, err := r.U16LE()
	if err != nil {
		return 0, err
	}
	c, err := r.U16BE()
	if err != nil {
		return 0, err
	}
	d, err := r.U32LE()
	if err != nil {
		return 0, err
	}
	e, err := r.U32BE()
	if err != nil {
		return 0, err
	}
	f, err := r.U64LE()
	if err != nil {
		return 0, err
	}
	g, err := r.PERLength()
	if err != nil {
		return 0, err
	}
	h, err := r.PERInteger16(1001)
	if err != nil {
		return 0, err
	}
	i, err := r.PERUint()
	if err != nil {
		return 0, err
	}
	if _, err := r.BERHeader(BERTag{Class: BERApplication, Constructed: true, Number: 101}); err != nil {
		return 0, err
	}
	j, err := r.BERInteger()
	if err != nil {
		return 0, err
	}
	k, err := r.BEREnumerated()
	if err != nil {
		return 0, err
	}
	l, err := r.BERBoolean()
	if err != nil {
		return 0, err
	}
	m, err := r.BEROctetString()
	if err != nil {
		return 0, err
	}
	sum = uint64(a) + uint64(b) + uint64(c) + uint64(d) + uint64(e) + f + uint64(g) + uint64(h) + uint64(i) + uint64(j) + uint64(k) + uint64(len(m))
	if l {
		sum++
	}
	return sum, nil
}

func TestMixedRoundTrip(t *testing.T) {
	r := NewReader(mixed)
	sum, err := readMixed(&r)
	if err != nil {
		t.Fatal(err)
	}
	const want = 1 + 2 + 3 + 4 + 5 + 6 + 300 + 1007 + 70000 + 65535 + 2 + 1 + 4
	if sum != want || r.Remaining() != 0 {
		t.Errorf("sum %d (left %d), want %d", sum, r.Remaining(), want)
	}
}

func TestNoAllocations(t *testing.T) {
	buf := make([]byte, 0, 128)
	tests := []struct {
		name string
		run  func()
	}{
		{"read", func() {
			r := NewReader(mixed)
			if _, err := readMixed(&r); err != nil {
				t.Fatal(err)
			}
		}},
		{"write", func() {
			w := NewWriter(buf)
			if err := writeMixed(&w); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if n := testing.AllocsPerRun(100, tt.run); n != 0 {
				t.Errorf("%v allocations per run, want 0", n)
			}
		})
	}
}

func BenchmarkRead(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(mixed)))
	for b.Loop() {
		r := NewReader(mixed)
		if _, err := readMixed(&r); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWrite(b *testing.B) {
	buf := make([]byte, 0, 128)
	b.ReportAllocs()
	b.SetBytes(int64(len(mixed)))
	for b.Loop() {
		w := NewWriter(buf)
		if err := writeMixed(&w); err != nil {
			b.Fatal(err)
		}
	}
}
