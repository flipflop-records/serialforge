package framing

import (
	"bytes"
	"testing"
)

func drain(f Framer) [][]byte {
	var frames [][]byte
	for {
		fr, ok := f.Next()
		if !ok {
			break
		}
		frames = append(frames, fr)
	}
	return frames
}

func TestRawFramerEmitsEachPushVerbatim(t *testing.T) {
	f, err := New(KindRaw, Options{})
	if err != nil {
		t.Fatal(err)
	}
	f.Push([]byte("ab"))
	f.Push([]byte("cd"))
	got := drain(f)
	want := [][]byte{[]byte("ab"), []byte("cd")}
	if len(got) != 2 || !bytes.Equal(got[0], want[0]) || !bytes.Equal(got[1], want[1]) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLineFramerSplitsOnNewline(t *testing.T) {
	f, err := New(KindLine, Options{TrimCR: true})
	if err != nil {
		t.Fatal(err)
	}
	f.Push([]byte("hello\r\nworld\r\npart"))
	got := drain(f)
	if len(got) != 2 || string(got[0]) != "hello" || string(got[1]) != "world" {
		t.Fatalf("got %q, want [hello world]", got)
	}
	// "part" has no trailing newline yet — must not be emitted.
	if _, ok := f.Next(); ok {
		t.Fatal("Next() returned a frame for an incomplete line")
	}
	f.Push([]byte("ial\n"))
	got = drain(f)
	if len(got) != 1 || string(got[0]) != "partial" {
		t.Fatalf("got %q, want [partial]", got)
	}
}

func TestFixedFramerEmitsExactSizeChunks(t *testing.T) {
	f, err := New(KindFixed, Options{Size: 4})
	if err != nil {
		t.Fatal(err)
	}
	f.Push([]byte{1, 2, 3})
	if _, ok := f.Next(); ok {
		t.Fatal("Next() returned a frame before 4 bytes accumulated")
	}
	f.Push([]byte{4, 5, 6, 7, 8})
	got := drain(f)
	if len(got) != 2 {
		t.Fatalf("got %d frames, want 2: %v", len(got), got)
	}
	if !bytes.Equal(got[0], []byte{1, 2, 3, 4}) || !bytes.Equal(got[1], []byte{5, 6, 7, 8}) {
		t.Fatalf("got %v, want [[1 2 3 4] [5 6 7 8]]", got)
	}
}

func TestDelimiterFramerSplitsOnArbitrarySequence(t *testing.T) {
	f, err := New(KindDelimiter, Options{Delimiter: []byte{0xC0}})
	if err != nil {
		t.Fatal(err)
	}
	f.Push([]byte{0xAA, 0x55, 0xC0, 0x01, 0x02, 0xC0})
	got := drain(f)
	if len(got) != 2 || !bytes.Equal(got[0], []byte{0xAA, 0x55}) || !bytes.Equal(got[1], []byte{0x01, 0x02}) {
		t.Fatalf("got %v", got)
	}
}

func TestResetDiscardsPartialFrame(t *testing.T) {
	f, err := New(KindFixed, Options{Size: 4})
	if err != nil {
		t.Fatal(err)
	}
	f.Push([]byte{1, 2, 3})
	f.Reset()
	f.Push([]byte{4, 5, 6, 7})
	got := drain(f)
	if len(got) != 1 || !bytes.Equal(got[0], []byte{4, 5, 6, 7}) {
		t.Fatalf("Reset() did not discard the pre-reset partial bytes: got %v", got)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := New(KindFixed, Options{Size: 0}); err == nil {
		t.Error("KindFixed with Size 0 should error")
	}
	if _, err := New(KindDelimiter, Options{}); err == nil {
		t.Error("KindDelimiter with no Delimiter should error")
	}
	if _, err := New("bogus", Options{}); err == nil {
		t.Error("unknown Kind should error")
	}
}
