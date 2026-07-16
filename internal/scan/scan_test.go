package scan

import (
	"bytes"
	"testing"
)

func BenchmarkIndexByte(b *testing.B) {
	data := make([]byte, 4096)
	data[len(data)-1] = 'z'
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for range b.N {
		if IndexByte(data, 'z') != len(data)-1 {
			b.Fatal("not found")
		}
	}
}

func TestIndexByte(t *testing.T) {
	for n := 0; n <= 128; n++ {
		b := make([]byte, n)
		for i := range b {
			b[i] = 'a'
		}
		if got := IndexByte(b, 'z'); got != -1 {
			t.Fatalf("n=%d missing: %d", n, got)
		}
		for i := range b {
			b[i] = 'z'
			if got := IndexByte(b, 'z'); got != i {
				t.Fatalf("n=%d i=%d got=%d", n, i, got)
			}
			b[i] = 'a'
		}
	}
}

func TestIndexByteDoesNotReadPastSlice(t *testing.T) {
	for n := 0; n <= 96; n++ {
		backing := make([]byte, n+32)
		for i := n; i < len(backing); i++ {
			backing[i] = 'z'
		}
		if got := IndexByte(backing[:n], 'z'); got != -1 {
			t.Fatalf("n=%d found sentinel beyond slice at %d", n, got)
		}
	}
}

func FuzzIndexByte(f *testing.F) {
	f.Add([]byte("hello"), byte('e'))
	f.Add([]byte{}, byte(0))
	f.Fuzz(func(t *testing.T, data []byte, target byte) {
		want := bytes.IndexByte(data, target)
		if got := IndexByte(data, target); got != want {
			t.Fatalf("got %d want %d", got, want)
		}
	})
}

func TestParsers(t *testing.T) {
	if n, ok := ParseDecimal("18446744073709551615"); ok || n != 0 {
		t.Fatal("accepted decimal overflow")
	}
	if n, ok := ParseHex("7fffffff"); !ok || n != 0x7fffffff {
		t.Fatalf("hex: %d %v", n, ok)
	}
}
