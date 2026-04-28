//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeast

// Extractor converts a language-specific parse unit into semantic AST nodes.
type Extractor[T any] interface {
	Extract(input T) ([]*Node, error)
}

// Analyzer derives graph relationships from a language-specific parse unit.
type Analyzer[T any] interface {
	Analyze(input T, nodeSet map[string]bool) ([]*Edge, error)
}
