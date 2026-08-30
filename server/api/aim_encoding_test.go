package api

// Tests for Aim's object wire format.
//
// Contract taken from aim/storage/encoding/encoding.pyx and
// encoding_native.pyx, and from response bodies captured off a live
// `aim up`, not from this decoder.
//
// The fixtures in testdata/ are real: a run logging three text pairs
// plus one output-only step, fetched through texts/get-batch/. A
// hand-built byte string would encode the same misunderstanding twice
// and pass anyway.
//
// What these CAN catch: a regression in our decoder.
// What they CANNOT catch: Aim changing its encoding. The format carries
// no version. Only a contract test against a live server closes that,
// and there is not one.

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return b
}

// --- framing ---

func TestFrameLengthsAreNativeNotNetworkOrder(t *testing.T) {
	// The mistake that cost real time during the probe. Aim writes
	// struct.pack('I', ...), which is host order. Reading big-endian
	// turns a 24-byte key into 402653184, so the guard must reject it
	// rather than attempt the allocation.
	body := fixture(t, "texts_get_batch_input.bin")
	swapped := make([]byte, len(body))
	copy(swapped, body)
	n := binary.LittleEndian.Uint32(body[:4])
	binary.BigEndian.PutUint32(swapped[:4], n)

	if _, err := DecodeTree(swapped); err == nil {
		t.Fatal("a byte-swapped length decoded without error")
	} else if !errors.Is(err, ErrMalformed) && !errors.Is(err, ErrTruncated) {
		t.Fatalf("unexpected error kind: %v", err)
	}
}

func TestTruncatedBodyErrorsRatherThanReturningAPartialTree(t *testing.T) {
	body := fixture(t, "texts_get_batch_input.bin")
	for _, cut := range []int{2, 7, len(body) / 2, len(body) - 1} {
		if _, err := DecodeTree(body[:cut]); !errors.Is(err, ErrTruncated) {
			t.Errorf("cut at %d: want ErrTruncated, got %v", cut, err)
		}
	}
}

func TestAnOversizedLengthDoesNotAllocate(t *testing.T) {
	// A corrupt length must fail on the cap, not on make([]byte, n).
	// Without the cap this reserves 4 GiB before any bounds check.
	body := make([]byte, 8)
	binary.LittleEndian.PutUint32(body[:4], 0xFFFFFFFF)
	if _, err := DecodeTree(body); !errors.Is(err, ErrMalformed) {
		t.Fatalf("want ErrMalformed for an oversized frame, got %v", err)
	}
}

// --- values ---

func TestStructureMarkersAreNotValues(t *testing.T) {
	// tagArray/tagObject/tagCustomObject announce shape and carry no
	// data. Decoding one as a value yields an empty string where a
	// nested structure belongs — a mis-decode that renders as a model
	// having produced nothing.
	for _, tc := range []struct {
		name string
		buf  []byte
		kind string
	}{
		{"array", []byte{tagArray}, "array"},
		{"object", []byte{tagObject}, "object"},
		{"custom", append([]byte{tagCustomObject}, []byte("aim.text")...), "custom:aim.text"},
	} {
		got, err := DecodeValue(tc.buf)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		marker, ok := got.(StructureMarker)
		if !ok {
			t.Fatalf("%s: decoded as %T (%v), want StructureMarker", tc.name, got, got)
		}
		if marker.Kind != tc.kind {
			t.Errorf("%s: kind = %q, want %q", tc.name, marker.Kind, tc.kind)
		}
	}
}

func TestAnUnknownTypeTagIsRefused(t *testing.T) {
	// Silently returning nil for a tag Aim added later is the invisible
	// mis-decode this package exists to avoid.
	if _, err := DecodeValue([]byte{99, 0, 0}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("want ErrMalformed for an unknown tag, got %v", err)
	}
}

func TestAnEmptyValueBufferIsTruncatedNotNil(t *testing.T) {
	if _, err := DecodeValue(nil); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestIntegerValuesAreNativeOrder(t *testing.T) {
	buf := make([]byte, 9)
	buf[0] = tagInt
	binary.LittleEndian.PutUint64(buf[1:], 1)
	got, err := DecodeValue(buf)
	if err != nil {
		t.Fatal(err)
	}
	// Read big-endian this would be 72057594037927936.
	if got != int64(1) {
		t.Fatalf("int64 = %v, want 1 — byte order is wrong", got)
	}
}

// --- paths ---

func TestPathIntegersAreBigEndianUnlikeValues(t *testing.T) {
	// The two orders in one format is the trap. Aim encodes path
	// integers big-endian on purpose so keys sort lexicographically in
	// RocksDB, while values use native order.
	key := []byte{'v', 'a', 'l', 's', pathSentinel, pathSentinel,
		0, 0, 0, 0, 0, 0, 0, 7, pathSentinel}
	path, err := DecodePath(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 2 || path[0] != "vals" || path[1] != int64(7) {
		t.Fatalf("path = %#v, want [vals 7]", path)
	}
}

func TestAMultibyteSegmentIsNotSplitByByteIndex(t *testing.T) {
	// The sentinel cannot appear inside UTF-8, so a delimiter collision
	// is impossible by construction — but byte-vs-rune indexing is a
	// real way to corrupt a multibyte segment.
	key := append([]byte("Übersetze"), pathSentinel)
	path, err := DecodePath(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 1 || path[0] != "Übersetze" {
		t.Fatalf("path = %#v", path)
	}
}

func TestATruncatedIntegerPathSegmentErrors(t *testing.T) {
	key := []byte{pathSentinel, pathSentinel, 0, 0, 0} // 8 bytes promised, 3 given
	if _, err := DecodePath(key); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

// --- the captured response, end to end ---

func TestTheCapturedInputSequenceDecodes(t *testing.T) {
	seq, err := ParseObjectSequence(fixture(t, "texts_get_batch_input.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if seq.Name != "sample/completions/input" {
		t.Errorf("name = %q", seq.Name)
	}
	if len(seq.Steps) != 3 {
		t.Fatalf("got %d records, want 3: %#v", len(seq.Steps), seq.Steps)
	}
	want := map[int64]string{
		0: "def fib(n):",
		1: "Übersetze: good morning",
		2: "", // logged as the empty string, and it must survive as one
	}
	for step, text := range want {
		rec, ok := seq.At(step)
		if !ok {
			t.Errorf("step %d missing", step)
			continue
		}
		if rec.Text != text {
			t.Errorf("step %d: text = %q, want %q", step, rec.Text, text)
		}
	}
}

func TestTheCapturedOutputSequenceHasAStepTheInputDoesNot(t *testing.T) {
	// Step 3 is output-only: unconditional generation. This is what
	// makes joining by position wrong.
	seq, err := ParseObjectSequence(fixture(t, "texts_get_batch_output.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if len(seq.Steps) != 4 {
		t.Fatalf("got %d records, want 4", len(seq.Steps))
	}
	rec, ok := seq.At(3)
	if !ok {
		t.Fatal("step 3 missing from the output sequence")
	}
	if rec.Text != "unconditional" {
		t.Errorf("step 3 text = %q", rec.Text)
	}
}

func TestANewlineSurvivesTheRoundTrip(t *testing.T) {
	seq, err := ParseObjectSequence(fixture(t, "texts_get_batch_output.bin"))
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := seq.At(0)
	if !ok {
		t.Fatal("step 0 missing")
	}
	if len(rec.Text) == 0 || rec.Text[0] != '\n' {
		t.Fatalf("leading newline lost: %q", rec.Text)
	}
}

func TestAGarbagePathIndexDoesNotHangTheParser(t *testing.T) {
	// A record index comes off the wire. An earlier version counted
	// 0..max to order records, so a mis-decoded path integer — 2^56
	// rather than 3 — spun the handler forever instead of returning.
	// Found by a mutation that flipped the path byte order; the fix is
	// to iterate the indexes that exist.
	body := fixture(t, "texts_get_batch_input.bin")
	entries, err := DecodeTree(body)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm the fixture really does carry integer path segments, so
	// this test cannot pass by exercising nothing.
	var sawIndex bool
	for _, e := range entries {
		for _, seg := range e.Path {
			if _, ok := seg.(int64); ok {
				sawIndex = true
			}
		}
	}
	if !sawIndex {
		t.Fatal("fixture has no integer path segments; this test proves nothing")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Same body, path integers read in the wrong byte order — which
		// is what a corrupt or version-shifted response looks like.
		swapped := make([]byte, len(body))
		copy(swapped, body)
		for i := 0; i+8 < len(swapped); i++ {
			if swapped[i] == pathSentinel && i+1 < len(swapped) && swapped[i+1] == pathSentinel {
				seg := swapped[i+2 : i+10]
				for a, b := 0, len(seg)-1; a < b; a, b = a+1, b-1 {
					seg[a], seg[b] = seg[b], seg[a]
				}
			}
		}
		_, _ = ParseObjectSequence(swapped)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ParseObjectSequence did not return on a garbage record index")
	}
}
