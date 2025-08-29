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
