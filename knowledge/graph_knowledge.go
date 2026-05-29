//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package knowledge

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
)

// GraphKnowledge is a knowledge base with graph-native query capabilities.
type GraphKnowledge interface {
	Knowledge
	Traverse(ctx context.Context, query *graph.TraverseQuery) (*graph.TraverseResult, error)
	FindPaths(ctx context.Context, query *graph.PathQuery) (*graph.PathResult, error)
}
