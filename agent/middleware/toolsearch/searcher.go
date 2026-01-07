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

type searcher interface {
	Search(ctx context.Context, candidates map[string]tool.Tool, query string, topK int) ([]string, error)
}
