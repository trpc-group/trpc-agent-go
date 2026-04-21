//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package proto

import "trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"

type analyzeInput struct{}

type defaultAnalyzer struct{}

func newDefaultAnalyzer() *defaultAnalyzer {
	return &defaultAnalyzer{}
}

// Analyze reserves the edge analysis extension point for future graph-aware parsing.
func (a *defaultAnalyzer) Analyze(input *analyzeInput, nodeSet map[string]bool) ([]*codeast.Edge, error) {
	_ = input
	_ = nodeSet
	return []*codeast.Edge{}, nil
}
