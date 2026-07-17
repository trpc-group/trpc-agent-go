//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor_test

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Keep the original extractor contract source-compatible for custom
// implementations that do not expose built-in policy metadata.
var _ extractor.MemoryExtractor = (*legacyMemoryExtractor)(nil)

type legacyMemoryExtractor struct{}

func (*legacyMemoryExtractor) Extract(
	context.Context,
	[]model.Message,
	[]*memory.Entry,
) ([]*extractor.Operation, error) {
	return nil, nil
}

func (*legacyMemoryExtractor) ShouldExtract(*extractor.ExtractionContext) bool {
	return true
}

func (*legacyMemoryExtractor) SetPrompt(string) {}

func (*legacyMemoryExtractor) SetModel(model.Model) {}
