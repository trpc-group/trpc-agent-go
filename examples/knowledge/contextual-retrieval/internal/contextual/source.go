//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package contextual contains the implementation details for the contextual
// retrieval example.
package contextual

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// SourceConfig configures index-time contextualization for a source.
type SourceConfig struct {
	// ParentText is the complete parent document shared by the delegated chunks.
	ParentText string
	// Model generates the short context prepended to each embedding text.
	Model model.Model
	// ModelName participates in cache identity.
	ModelName string
	// CachePath stores generated contexts between runs.
	CachePath string
}

type contextGenerator interface {
	Generate(ctx context.Context, parentText, chunkText string) (string, error)
}

type contextualSource struct {
	delegate   source.Source
	parentText string
	generator  contextGenerator
	modelName  string
	cache      *contextCache
}

// NewSource wraps delegate and prepends generated context only to each
// document's EmbeddingText. The original document content and metadata are
// preserved.
func NewSource(delegate source.Source, cfg SourceConfig) (source.Source, error) {
	if cfg.Model == nil {
		return nil, errors.New("context model is required")
	}
	cache, err := openContextCache(cfg.CachePath)
	if err != nil {
		return nil, fmt.Errorf("open context cache: %w", err)
	}
	return newContextualSource(
		delegate,
		cfg.ParentText,
		newModelContextGenerator(cfg.Model),
		cfg.ModelName,
		cache,
	)
}

func newContextualSource(
	delegate source.Source,
	parentText string,
	generator contextGenerator,
	modelName string,
	cache *contextCache,
) (*contextualSource, error) {
	if delegate == nil {
		return nil, errors.New("delegate source is required")
	}
	if strings.TrimSpace(parentText) == "" {
		return nil, errors.New("parent document is empty")
	}
	if generator == nil {
		return nil, errors.New("context generator is required")
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return nil, errors.New("context model name is required")
	}
	if cache == nil {
		return nil, errors.New("context cache is required")
	}
	return &contextualSource{
		delegate:   delegate,
		parentText: parentText,
		generator:  generator,
		modelName:  modelName,
		cache:      cache,
	}, nil
}

func (s *contextualSource) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	docs, err := s.delegate.ReadDocuments(ctx)
	if err != nil {
		return nil, fmt.Errorf("read delegate documents: %w", err)
	}

	contextualized := make([]*document.Document, len(docs))
	for i, doc := range docs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if doc == nil {
			return nil, fmt.Errorf("delegate returned nil document at index %d", i)
		}

		clone := doc.Clone()
		baseText := embeddingText(clone)
		cacheKey := contextCacheKey(s.modelName, s.parentText, clone.Content, baseText)
		contextText, ok := s.cache.get(cacheKey)
		if !ok {
			contextText, err = s.generator.Generate(ctx, s.parentText, clone.Content)
			if err != nil {
				return nil, fmt.Errorf("generate context for document %d: %w", i, err)
			}
			contextText = strings.TrimSpace(contextText)
			if contextText == "" {
				return nil, fmt.Errorf("generate context for document %d: empty context", i)
			}
			if err := s.cache.put(cacheKey, contextText); err != nil {
				return nil, fmt.Errorf("cache context for document %d: %w", i, err)
			}
		}

		clone.EmbeddingText = contextualEmbeddingText(contextText, baseText)
		contextualized[i] = clone
	}
	return contextualized, nil
}

func (s *contextualSource) Name() string {
	return s.delegate.Name()
}

func (s *contextualSource) Type() string {
	return s.delegate.Type()
}

func (s *contextualSource) GetMetadata() map[string]any {
	return s.delegate.GetMetadata()
}

func embeddingText(doc *document.Document) string {
	if doc.EmbeddingText != "" {
		return doc.EmbeddingText
	}
	return doc.Content
}

func contextualEmbeddingText(contextText, baseText string) string {
	return "Context:\n" + strings.TrimSpace(contextText) +
		"\n\n--- Original chunk ---\n" + baseText
}
