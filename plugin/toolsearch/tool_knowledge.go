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
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ToolKnowledge is a tool knowledge base that uses a vector store to store tools and their embeddings.
type ToolKnowledge struct {
	s vectorstore.VectorStore
	e embedder.Embedder

	mu    sync.RWMutex
	tools map[string]tool.Tool
}

// ToolKnowledgeOption is a function that configures the ToolKnowledge.
type ToolKnowledgeOption func(*ToolKnowledge)

// WithVectorStore sets the vector store for the ToolKnowledge.
func WithVectorStore(s vectorstore.VectorStore) ToolKnowledgeOption {
	return func(k *ToolKnowledge) {
		k.s = s
	}
}

// NewToolKnowledge creates a new ToolKnowledge.
func NewToolKnowledge(e embedder.Embedder, opts ...ToolKnowledgeOption) (*ToolKnowledge, error) {
	k := &ToolKnowledge{
		s:     inmemory.New(), // default vector store
		e:     e,
		tools: make(map[string]tool.Tool),
		mu:    sync.RWMutex{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(k)
		}
	}
	return k, nil
}

func (k *ToolKnowledge) search(ctx context.Context, candidates map[string]tool.Tool, query string, topK int) (context.Context, []string, *model.Usage, error) {
	embedding, u, err := k.e.GetEmbeddingWithUsage(ctx, query)
	if err != nil {
		return ctx, nil, nil, err
	}
	usage := &model.Usage{}
	if promptTokens, ok := u["prompt_tokens"]; ok {
		if t, ok := promptTokens.(int64); ok {
			usage.PromptTokens += int(t)
		}
	}
	if totalTokens, ok := u["total_tokens"]; ok {
		if t, ok := totalTokens.(int64); ok {
			usage.TotalTokens += int(t)
		}
	}
	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	results, err := k.s.Search(ctx, &vectorstore.SearchQuery{
		Vector:     embedding,
		SearchMode: vectorstore.SearchModeVector,
		Limit:      topK,
		Filter: &vectorstore.SearchFilter{
			IDs: names,
		},
	})
	if err != nil {
		return ctx, nil, usage, err
	}
	tools := make([]string, 0, len(results.Results))
	for _, result := range results.Results {
		tools = append(tools, result.Document.ID)
	}
	return ctx, tools, usage, nil
}

func (k *ToolKnowledge) upsert(ctx context.Context, ts map[string]tool.Tool) (*model.Usage, error) {
	usage := &model.Usage{}
	k.mu.Lock()
	defer k.mu.Unlock()
	for name, t := range ts {
		if _, ok := k.tools[name]; ok {
			continue
		}
		embedding, u, err := k.e.GetEmbeddingWithUsage(ctx, toolToText(t))
		if err != nil {
			return nil, err
		}
		if promptTokens, ok := u["prompt_tokens"]; ok {
			if t, ok := promptTokens.(int64); ok {
				usage.PromptTokens += int(t)
			}
		}
		if totalTokens, ok := u["total_tokens"]; ok {
			if t, ok := totalTokens.(int64); ok {
				usage.TotalTokens += int(t)
			}
		}
		if err := k.s.Add(ctx, &document.Document{ID: name}, embedding); err != nil {
			return nil, err
		}
		k.tools[name] = t
	}
	return usage, nil
}

func toolToText(t tool.Tool) string {
	if t == nil {
		return ""
	}
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
