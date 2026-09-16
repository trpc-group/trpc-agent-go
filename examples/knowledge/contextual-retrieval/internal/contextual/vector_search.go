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

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

type vectorOnlyKnowledge struct {
	knowledge.Knowledge
}

// VectorOnly returns a Knowledge wrapper that uses dense vector search without
// mutating the caller's request.
func VectorOnly(inner knowledge.Knowledge) knowledge.Knowledge {
	return vectorOnlyKnowledge{Knowledge: inner}
}

func (k vectorOnlyKnowledge) Search(
	ctx context.Context,
	request *knowledge.SearchRequest,
) (*knowledge.SearchResult, error) {
	if request == nil {
		return k.Knowledge.Search(ctx, nil)
	}
	requestCopy := *request
	requestCopy.SearchMode = vectorstore.SearchModeVector
	return k.Knowledge.Search(ctx, &requestCopy)
}
