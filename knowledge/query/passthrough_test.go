//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package query

import "testing"

func TestPassthroughEnhancer(t *testing.T) {
	pe := NewPassthroughEnhancer()
	req := &Request{Query: "hello"}
	enhanced, err := pe.EnhanceQuery(nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enhanced.Enhanced != "hello" {
		t.Fatalf("expected same query")
	}
	if len(enhanced.Keywords) != 1 || enhanced.Keywords[0] != "hello" {
		t.Fatalf("keywords mismatch")
	}
}

// TestPassthroughEnhancer_WithOptions tests constructor with options.
func TestPassthroughEnhancer_WithOptions(t *testing.T) {
	// Test with a mock option function to cover the option loop.
	optionCalled := false
	mockOption := func(p *PassthroughEnhancer) {
		optionCalled = true
	}

	pe := NewPassthroughEnhancer(mockOption)
	if !optionCalled {
		t.Fatal("option function was not called")
	}

	// Verify the enhancer still works correctly.
	req := &Request{Query: "test"}
	enhanced, err := pe.EnhanceQuery(nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enhanced.Enhanced != "test" {
		t.Fatalf("expected same query, got %s", enhanced.Enhanced)
	}
}
