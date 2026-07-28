package safeio

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCopyPassesThroughWhatFits(t *testing.T) {
	const payload = "a book, of modest size"

	var dst bytes.Buffer
	n, err := Copy(&dst, strings.NewReader(payload), 1024)
	if err != nil {
		t.Fatalf("copying %d bytes under a 1024 limit: %v", len(payload), err)
	}
	if n != int64(len(payload)) {
		t.Errorf("reported %d bytes copied, want %d", n, len(payload))
	}
	if dst.String() != payload {
		t.Errorf("destination holds %q, want %q", dst.String(), payload)
	}
}

// The boundary is the interesting case: a source exactly at the limit is
// allowed, one byte more is not.
func TestCopyAcceptsExactlyTheLimit(t *testing.T) {
	payload := strings.Repeat("x", 64)

	var dst bytes.Buffer
	n, err := Copy(&dst, strings.NewReader(payload), 64)
	if err != nil {
		t.Fatalf("a source exactly at the limit was refused: %v", err)
	}
	if n != 64 || dst.Len() != 64 {
		t.Errorf("copied %d bytes into %d, want 64 and 64", n, dst.Len())
	}
}

func TestCopyRefusesMoreThanTheLimit(t *testing.T) {
	payload := strings.Repeat("x", 65)

	var dst bytes.Buffer
	_, err := Copy(&dst, strings.NewReader(payload), 64)
	if err == nil {
		t.Fatal("a source over the limit was accepted")
	}
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("error %v does not wrap ErrTooLarge", err)
	}
	if !strings.Contains(err.Error(), "64") {
		t.Errorf("the error does not say what the limit was: %v", err)
	}
}

// A bomb does not announce itself: the reader keeps producing bytes. What
// matters is that the copy stops rather than running until the disk is full.
func TestCopyStopsReadingAnEndlessSource(t *testing.T) {
	const limit = 4096

	endless := &countingReader{}
	var dst bytes.Buffer
	_, err := Copy(&dst, endless, limit)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("an endless source produced %v, want ErrTooLarge", err)
	}
	// One byte past the limit is what tells "at" from "over"; anything much
	// beyond that would mean the guard is not bounding the read.
	if endless.read > limit+1 {
		t.Errorf("read %d bytes from an endless source, limit was %d", endless.read, limit)
	}
}

func TestCopyRejectsANegativeLimit(t *testing.T) {
	var dst bytes.Buffer
	if _, err := Copy(&dst, strings.NewReader("x"), -1); err == nil {
		t.Error("a negative limit was accepted")
	}
}

func TestCopyBufferBehavesLikeCopy(t *testing.T) {
	buf := make([]byte, 8)

	var fits bytes.Buffer
	if _, err := CopyBuffer(&fits, strings.NewReader("short"), buf, 64); err != nil {
		t.Errorf("copying through a buffer under the limit: %v", err)
	}

	var over bytes.Buffer
	_, err := CopyBuffer(&over, strings.NewReader(strings.Repeat("x", 65)), buf, 64)
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("copying through a buffer over the limit produced %v, want ErrTooLarge", err)
	}
}

// countingReader never runs out, and records how much was taken from it.
type countingReader struct{ read int }

func (r *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	r.read += len(p)
	return len(p), nil
}

var _ io.Reader = (*countingReader)(nil)
