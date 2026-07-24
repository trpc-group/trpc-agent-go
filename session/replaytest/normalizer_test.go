//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestNormalizer_ID(t *testing.T) {
	n := NewNormalizer()
	tests := []struct {
		input string
		kind  string
		want  string
	}{
		{"session-abc", "session", "<session-id>"},
		{"evt-123", "event", "<event-id>"},
		{"mem-456", "memory", "<memory-id>"},
		{"", "anything", ""},
	}
	for _, tt := range tests {
		got := n.NormalizeID(tt.input, tt.kind)
		if got != tt.want {
			t.Errorf("NormalizeID(%q, %q) = %q, want %q", tt.input, tt.kind, got, tt.want)
		}
	}
}

func TestNormalizer_Timestamp(t *testing.T) {
	n := NewNormalizer()

	// Verify UTC conversion.
	now := time.Now()
	normalized := n.NormalizeTimestamp(now)
	if normalized.Location() != time.UTC {
		t.Error("expected UTC location")
	}

	// Verify zero time is preserved.
	zero := time.Time{}
	if !n.NormalizeTimestamp(zero).IsZero() {
		t.Error("expected zero time to remain zero")
	}
}

func TestNormalizer_JSON(t *testing.T) {
	n := NewNormalizer()
	input := []byte(`{"z":1,"a":{"nested":2,"b":3}}`)
	output := n.NormalizeJSON(input)
	expected := `{"a":{"b":3,"nested":2},"z":1}`
	if string(output) != expected {
		t.Errorf("expected %q, got %q", expected, string(output))
	}
}

func TestNormalizer_Float(t *testing.T) {
	n := NewNormalizer()
	tests := []struct {
		input float64
		want  float64
	}{
		{0, 0},
		{1.234, 1.23},
		{1.235, 1.24},
		{3.14159, 3.14},
	}
	for _, tt := range tests {
		got := n.NormalizeFloat(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeFloat(%f) = %f, want %f", tt.input, got, tt.want)
		}
	}
}

func TestNormalizer_FloatsEqual(t *testing.T) {
	n := NewNormalizer()
	if !n.FloatsEqual(1.0, 1.005, 0.01) {
		t.Error("expected 1.0 and 1.005 to be equal")
	}
	if n.FloatsEqual(1.0, 1.02, 0.01) {
		t.Error("expected 1.0 and 1.02 to differ")
	}
}

func TestNormalizer_StateMap(t *testing.T) {
	n := NewNormalizer()
	input := session.StateMap{"b": []byte("2"), "a": []byte("1")}
	output := n.NormalizeStateMap(input)

	// Verify all keys and values are preserved.
	if string(output["a"]) != "1" {
		t.Errorf("expected output['a']='1', got %q", string(output["a"]))
	}
	if string(output["b"]) != "2" {
		t.Errorf("expected output['b']='2', got %q", string(output["b"]))
	}
	if len(output) != 2 {
		t.Errorf("expected 2 keys, got %d", len(output))
	}
}

func TestNormalizer_StringSlice(t *testing.T) {
	n := NewNormalizer()
	input := []string{"z", "a", "m"}
	output := n.NormalizeStringSlice(input)
	expected := []string{"a", "m", "z"}
	if len(output) != 3 || output[0] != expected[0] || output[1] != expected[1] || output[2] != expected[2] {
		t.Errorf("expected [a m z], got %v", output)
	}
}

func TestNormalizer_NilInputs(t *testing.T) {
	n := NewNormalizer()
	if n.NormalizeSession(nil) != nil {
		t.Error("expected nil for nil session")
	}
	if n.NormalizeEvents(nil) != nil {
		t.Error("expected nil for nil events")
	}
	if n.NormalizeMemories(nil) != nil {
		t.Error("expected nil for nil memories")
	}
	if n.NormalizeJSON(nil) != nil {
		t.Error("expected nil for nil JSON")
	}
}

func TestNormalizer_PrivateMetaDropped(t *testing.T) {
	n := NewNormalizer()
	sess := &session.Session{
		ID:          "sess-1",
		ServiceMeta: map[string]string{"internal": "value"},
	}
	normalized := n.NormalizeSession(sess)
	if normalized == nil {
		t.Fatal("expected non-nil session")
	}
	// ServiceMeta should not be present.
	if normalized.ServiceMeta != nil {
		t.Error("expected ServiceMeta to be dropped")
	}
}
