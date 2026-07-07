//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
	itool "trpc.group/trpc-go/trpc-agent-go/tool"
)

type deepSearchToolSet struct {
	tools []itool.Tool
}

// NewDeepSearchToolSet returns the activatable DeepSearch memory tools.
func NewDeepSearchToolSet() itool.ToolSet {
	return &deepSearchToolSet{
		tools: []itool.Tool{
			NewCueSearchTool(),
			NewTagExpandTool(),
			NewContentLoadTool(),
		},
	}
}

func (s *deepSearchToolSet) Tools(context.Context) []itool.Tool {
	return append([]itool.Tool(nil), s.tools...)
}

func (s *deepSearchToolSet) Close() error { return nil }

func (s *deepSearchToolSet) Name() string { return deepsearch.ToolSetName }
