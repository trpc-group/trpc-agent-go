//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package event

import (
	"testing"
)

func TestAppendTagString(t *testing.T) {
	// Empty existing returns tag directly
	if got := AppendTagString("", "a"); got != "a" {
		t.Fatalf("expected 'a', got %q", got)
	}
	// Empty tag returns existing unchanged
	if got := AppendTagString("x", ""); got != "x" {
		t.Fatalf("expected 'x', got %q", got)
	}
	// Append with delimiter
	want := "x" + TagDelimiter + "y"
	if got := AppendTagString("x", "y"); got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
	// Avoid duplicate
	if got := AppendTagString("x", "x"); got != "x" {
		t.Fatalf("expected 'x', got %q", got)
	}
}

func TestAddTag(t *testing.T) {
	// nil event should be no-op (no panic)
	AddTag(nil, "a")

	e := &Event{}
	AddTag(e, "a")
	if e.Tag != "a" {
		t.Fatalf("expected 'a', got %q", e.Tag)
	}
	// duplicate should not be appended
	AddTag(e, "a")
	if e.Tag != "a" {
		t.Fatalf("expected 'a' after duplicate, got %q", e.Tag)
	}
	// different tag should be appended with delimiter
	AddTag(e, "b")
	want := "a" + TagDelimiter + "b"
	if e.Tag != want {
		t.Fatalf("expected %q, got %q", want, e.Tag)
	}
}

func TestEventHasTag(t *testing.T) {
	// nil event returns false
	var eNil *Event
	if eNil.HasTag("a") {
		t.Fatalf("nil event should not have any tag")
	}

	e := &Event{Tag: "a"}
	if !e.HasTag("a") {
		t.Fatalf("expected HasTag to find existing tag 'a'")
	}
	if e.HasTag("") {
		t.Fatalf("empty tag should return false")
	}
	e.Tag = "a" + TagDelimiter + "b"
	if !e.HasTag("b") || !e.HasTag("a") {
		t.Fatalf("expected HasTag to find both 'a' and 'b'")
	}
	if e.HasTag("ab") {
		t.Fatalf("should not match substrings")
	}
}
