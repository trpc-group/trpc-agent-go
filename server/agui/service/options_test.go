package service

import "testing"

func TestWithPath(t *testing.T) {
	opts := &Options{}
	WithPath("/sse")(opts)

	if opts.Path != "/sse" {
		t.Fatalf("path mismatch: got %q", opts.Path)
	}
}

func TestOptionsDefaultValues(t *testing.T) {
	opts := &Options{}
	if opts.Path != "" {
		t.Fatalf("expected zero default path, got %q", opts.Path)
	}
}
