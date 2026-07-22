// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package shellsafe

import "testing"

func TestPreviewList(t *testing.T) {
	if got := PreviewList(nil, 3); got != "" {
		t.Fatalf("nil list: got %q want empty", got)
	}
	if got := PreviewList([]string{"a", "b"}, 3); got != "a, b" {
		t.Fatalf("short list: got %q", got)
	}
	got := PreviewList([]string{"a", "b", "c", "d", "e"}, 2)
	if got != "a, b, ... (3 more)" {
		t.Fatalf("truncation: got %q", got)
	}
}

// TestParser_SeamAllowsReplacement verifies that Parse and Policy.Check route
// through the replaceable commandParser seam and that restore reinstates it.
func TestParser_SeamAllowsReplacement(t *testing.T) {
	called := 0
	stub := func(src string) ([][]string, error) {
		called++
		if src == "fail" {
			return nil, errSeamStub
		}
		return [][]string{{"echo", src}}, nil
	}
	restore := withParser(stub)
	defer restore()

	pipe, err := Parse("hello")
	if err != nil {
		t.Fatalf("expected stub to succeed: %v", err)
	}
	if got, want := pipe.Commands[0], []string{"echo", "hello"}; !equal(got, want) {
		t.Fatalf("stub argv: got %v want %v", got, want)
	}
	if called != 1 {
		t.Fatalf("stub call count: got %d want 1", called)
	}
	if _, err := Parse("fail"); err != errSeamStub {
		t.Fatalf("expected sentinel err from stub, got: %v", err)
	}

	restore()
	pipe, err = Parse("echo back-on-real-parser")
	if err != nil {
		t.Fatalf("real parser should be restored: %v", err)
	}
	if got, want := pipe.Commands[0],
		[]string{"echo", "back-on-real-parser"}; !equal(got, want) {
		t.Fatalf("real parser argv: got %v want %v", got, want)
	}
}

var errSeamStub = errSeamStubT("stub forced failure")

type errSeamStubT string

func (e errSeamStubT) Error() string { return string(e) }
