//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reranker

import (
	"context"
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
	out, err := rk.Rerank(context.TODO(), nil, results)
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
	out2, _ := rk2.Rerank(context.TODO(), nil, results)
	if len(out2) != 3 {
		t.Fatalf("expected all results")
	}
}
