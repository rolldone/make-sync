package syncdata

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestStreamOutputIdleTimeoutTrips verifies that a stream with no output is
// aborted once the idle threshold elapses.
func TestStreamOutputIdleTimeoutTrips(t *testing.T) {
	outputChan := make(chan string)
	errorChan := make(chan error)
	defer close(outputChan)
	defer close(errorChan)

	start := time.Now()
	out, err := streamOutputWithIdleTimeout(outputChan, errorChan, 50*time.Millisecond, nil, "remote indexing")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected idle timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "idle timeout") {
		t.Fatalf("expected error to mention idle timeout, got: %v", err)
	}
	if !strings.Contains(err.Error(), "50ms") {
		t.Fatalf("expected error to mention configured duration, got: %v", err)
	}
	if strings.Contains(err.Error(), "timed out after 60 seconds") {
		t.Fatalf("error must not use the old total-timeout message, got: %v", err)
	}
	if out != "" {
		t.Fatalf("expected empty output, got %q", out)
	}
	if elapsed < 50*time.Millisecond {
		t.Fatalf("returned before idle threshold: %v", elapsed)
	}
}

// TestStreamOutputIdleTimeoutResetsOnOutput verifies that continuous output
// keeps the stream alive well past the idle threshold (no total duration cap).
func TestStreamOutputIdleTimeoutResetsOnOutput(t *testing.T) {
	outputChan := make(chan string)
	errorChan := make(chan error)
	defer close(errorChan)

	go func() {
		for i := 0; i < 6; i++ {
			outputChan <- "chunk\n"
			time.Sleep(20 * time.Millisecond)
		}
		close(outputChan)
	}()

	start := time.Now()
	out, err := streamOutputWithIdleTimeout(outputChan, errorChan, 60*time.Millisecond, nil, "remote indexing")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected nil error while output flows, got: %v", err)
	}
	if elapsed <= 60*time.Millisecond {
		t.Fatalf("expected total runtime to exceed idle threshold, got %v", elapsed)
	}
	if got := strings.Count(out, "chunk\n"); got != 6 {
		t.Fatalf("expected 6 chunks in output, got %d (%q)", got, out)
	}
}

// TestStreamOutputPropagatesError verifies label-prefixed error propagation.
func TestStreamOutputPropagatesError(t *testing.T) {
	outputChan := make(chan string)
	errorChan := make(chan error, 1)
	defer close(outputChan)
	defer close(errorChan)

	sentinel := errors.New("boom")
	go func() {
		outputChan <- "partial"
		errorChan <- sentinel
	}()

	out, err := streamOutputWithIdleTimeout(outputChan, errorChan, time.Second, nil, "remote indexing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "remote indexing") {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "partial" {
		t.Fatalf("expected partial output preserved, got %q", out)
	}
}

// TestStreamOutputDefaultIdleTimeout verifies that a non-positive threshold
// falls back to the configured default instead of tripping immediately.
func TestStreamOutputDefaultIdleTimeout(t *testing.T) {
	outputChan := make(chan string)
	errorChan := make(chan error)
	defer close(errorChan)

	go func() {
		outputChan <- "done"
		close(outputChan)
	}()

	out, err := streamOutputWithIdleTimeout(outputChan, errorChan, 0, nil, "remote indexing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "done" {
		t.Fatalf("unexpected output: %q", out)
	}
}
