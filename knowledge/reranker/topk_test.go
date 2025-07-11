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

package reranker

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

func TestTopKReranker(t *testing.T) {
	results := []*Result{
		{Document: &document.Document{ID: "1"}, Score: 0.9},
		{Document: &document.Document{ID: "2"}, Score: 0.8},
		{Document: &document.Document{ID: "3"}, Score: 0.7},
	}

	rk := NewTopKReranker(WithK(2))
	out, err := rk.Rerank(nil, results)
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 results")
	}
	if out[0].Document.ID != "1" || out[1].Document.ID != "2" {
		t.Fatalf("order preserved")
	}

	// K greater than len.
	rk2 := NewTopKReranker(WithK(10))
	out2, _ := rk2.Rerank(nil, results)
	if len(out2) != 3 {
		t.Fatalf("expected all results")
	}
}
