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
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type ToolKnowledge struct {
	s     vectorstore.VectorStore
	e     embedder.Embedder
	tools map[string]tool.Tool
}

type ToolKnowledgeOption func(*ToolKnowledge)

func WithVectorStore(s vectorstore.VectorStore) ToolKnowledgeOption {
	return func(k *ToolKnowledge) {
		k.s = s
	}
}

func NewToolKnowledge(e embedder.Embedder, opts ...ToolKnowledgeOption) *ToolKnowledge {
	k := &ToolKnowledge{
		s:     inmemory.New(), // default vector store
		e:     e,
		tools: make(map[string]tool.Tool),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(k)
		}
	}
	return k
}

func (k *ToolKnowledge) search(ctx context.Context, query string, topK int) (map[string]tool.Tool, error) {
	if err := k.ensureReady(); err != nil {
		return nil, err
	}
	embedding, err := k.e.GetEmbedding(ctx, query)
	if err != nil {
		return nil, err
	}
	results, err := k.s.Search(ctx, &vectorstore.SearchQuery{Vector: embedding, SearchMode: vectorstore.SearchModeVector, Limit: topK})
	if err != nil {
		return nil, err
	}
	tools := make(map[string]tool.Tool, len(results.Results))
	for _, result := range results.Results {
		tools[result.Document.ID] = k.tools[result.Document.ID]
	}
	return tools, nil
}

func (k *ToolKnowledge) upsert(ctx context.Context, ts map[string]tool.Tool) error {
	if err := k.ensureReady(); err != nil {
		return err
	}
	for name, t := range ts {
		if _, ok := k.tools[name]; ok {
			continue
		}
		embedding, err := k.e.GetEmbedding(ctx, toolToText(t))
		if err != nil {
			return err
		}
		if err := k.s.Add(ctx, &document.Document{ID: name}, embedding); err != nil {
			return err
		}
		k.tools[name] = t
	}
	return nil
}

func (k *ToolKnowledge) ensureReady() error {
	if k == nil {
		return fmt.Errorf("ToolKnowledge: nil")
	}
	if k.s == nil {
		k.s = inmemory.New()
	}
	if k.tools == nil {
		k.tools = make(map[string]tool.Tool)
	}
	if k.e == nil {
		return fmt.Errorf("ToolKnowledge: embedder is nil")
	}
	return nil
}

func toolToText(t tool.Tool) string {
	decl := t.Declaration()
	if decl == nil {
		return ""
	}

	textParts := []string{
		fmt.Sprintf("Tool: %s", decl.Name),
		fmt.Sprintf("Description: %s", decl.Description),
	}

	// Add parameter information (mirrors the Python implementation).
	if decl.InputSchema != nil && len(decl.InputSchema.Properties) > 0 {
		keys := make([]string, 0, len(decl.InputSchema.Properties))
		for k := range decl.InputSchema.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		paramDescriptions := make([]string, 0, len(keys))
		for _, paramName := range keys {
			paramInfo := decl.InputSchema.Properties[paramName]
			if paramInfo == nil {
				continue
			}

			paramDesc := strings.TrimSpace(paramInfo.Description)
			paramType := strings.TrimSpace(paramInfo.Type)
			if paramType == "" {
				// Best-effort inference (useful for partially-filled schemas).
				if paramInfo.Items != nil {
					paramType = "array"
				} else if len(paramInfo.Properties) > 0 {
					paramType = "object"
				}
			}

			paramDescriptions = append(
				paramDescriptions,
				fmt.Sprintf("%s (%s): %s", paramName, paramType, paramDesc),
			)
		}

		if len(paramDescriptions) > 0 {
			textParts = append(textParts, "Parameters: "+strings.Join(paramDescriptions, ", "))
		}
	}

	return strings.Join(textParts, "\n")
}
