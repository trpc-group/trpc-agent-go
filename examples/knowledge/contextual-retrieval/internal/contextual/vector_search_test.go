//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package contextual

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

type capturingKnowledge struct {
	request *knowledge.SearchRequest
}

func (k *capturingKnowledge) Search(
	_ context.Context,
	request *knowledge.SearchRequest,
) (*knowledge.SearchResult, error) {
	k.request = request
	return &knowledge.SearchResult{}, nil
}

func TestVectorOnlyOverridesSearchModeWithoutMutatingRequest(t *testing.T) {
	inner := &capturingKnowledge{}
	wrapped := VectorOnly(inner)
	request := &knowledge.SearchRequest{
		Query:      "query",
		SearchMode: vectorstore.SearchModeHybrid,
	}
	if _, err := wrapped.Search(context.Background(), request); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if request.SearchMode != vectorstore.SearchModeHybrid {
		t.Fatalf("caller request search mode was mutated: %d", request.SearchMode)
	}
	if inner.request == nil || inner.request.SearchMode != vectorstore.SearchModeVector {
		t.Fatalf("inner search mode = %v, want vector", inner.request)
	}
}

func TestVectorOnlyPassesThroughNilRequest(t *testing.T) {
	inner := &capturingKnowledge{}
	if _, err := VectorOnly(inner).Search(context.Background(), nil); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if inner.request != nil {
		t.Fatalf("inner request = %v, want nil", inner.request)
	}
}
