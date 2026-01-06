//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type toolIndex interface {
	search(ctx context.Context, query string, topK int) (map[string]tool.Tool, error)
	upsert(ctx context.Context, candidateTools map[string]tool.Tool) error
	rewriteQuery(ctx context.Context, query string) (string, error)
}
