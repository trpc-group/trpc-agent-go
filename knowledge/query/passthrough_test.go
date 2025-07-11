//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
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
