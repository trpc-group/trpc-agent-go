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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// DefaultMaxPromptBytes is the default cumulative context-generation prompt
// budget for unique cache misses.
const DefaultMaxPromptBytes int64 = 4 << 20

// SourceConfig configures index-time contextualization for a source.
type SourceConfig struct {
	// ParentText is the complete parent document shared by the delegated chunks.
	ParentText string
	// Model generates the short context prepended to each embedding text.
	Model model.Model
	// ModelName participates in cache identity.
	ModelName string
	// ModelEndpoint participates in cache identity without being persisted in
	// clear text. Leave it empty when the model adapter uses its default.
	ModelEndpoint string
	// CachePath stores generated contexts between runs.
	CachePath string
	// MaxPromptBytes limits cumulative context-generation prompt bytes for
	// unique cache misses. Zero uses DefaultMaxPromptBytes.
	MaxPromptBytes int64
}

type contextGenerator interface {
	Generate(ctx context.Context, parentText, chunkText string) (string, error)
}

type contextualSource struct {
	delegate           source.Source
	parentText         string
	generator          contextGenerator
	generationIdentity string
	cache              *contextCache
	maxPromptBytes     int64
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
	generationIdentity, err := contextGenerationIdentity(cfg.ModelName, cfg.ModelEndpoint)
	if err != nil {
		return nil, err
	}
	return newContextualSource(
		delegate,
		cfg.ParentText,
		newModelContextGenerator(cfg.Model),
		generationIdentity,
		cache,
		cfg.MaxPromptBytes,
	)
}

func newContextualSource(
	delegate source.Source,
	parentText string,
	generator contextGenerator,
	generationIdentity string,
	cache *contextCache,
	maxPromptBytes int64,
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
	generationIdentity = strings.TrimSpace(generationIdentity)
	if generationIdentity == "" {
		return nil, errors.New("context generation identity is required")
	}
	if cache == nil {
		return nil, errors.New("context cache is required")
	}
	if maxPromptBytes < 0 {
		return nil, errors.New("maximum context prompt bytes must not be negative")
	}
	if maxPromptBytes == 0 {
		maxPromptBytes = DefaultMaxPromptBytes
	}
	return &contextualSource{
		delegate:           delegate,
		parentText:         parentText,
		generator:          generator,
		generationIdentity: generationIdentity,
		cache:              cache,
		maxPromptBytes:     maxPromptBytes,
	}, nil
}

type preparedDocument struct {
	document *document.Document
	baseText string
	cacheKey string
}

func (s *contextualSource) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	docs, err := s.delegate.ReadDocuments(ctx)
	if err != nil {
		return nil, fmt.Errorf("read delegate documents: %w", err)
	}

	prepared := make([]preparedDocument, len(docs))
	uncachedKeys := make(map[string]struct{})
	var totalPromptBytes int64
	for i, doc := range docs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if doc == nil {
			return nil, fmt.Errorf("delegate returned nil document at index %d", i)
		}

		clone := doc.Clone()
		baseText := embeddingText(clone)
		cacheKey := contextCacheKey(
			s.generationIdentity,
			s.parentText,
			clone.Content,
			baseText,
		)
		prepared[i] = preparedDocument{document: clone, baseText: baseText, cacheKey: cacheKey}
		if _, ok := s.cache.get(cacheKey); ok {
			continue
		}
		if _, ok := uncachedKeys[cacheKey]; ok {
			continue
		}
		uncachedKeys[cacheKey] = struct{}{}
		promptBytes := contextRequestBytes(s.parentText, clone.Content)
		estimatedPromptBytes := totalPromptBytes + promptBytes
		if estimatedPromptBytes > s.maxPromptBytes {
			return nil, fmt.Errorf(
				"context generation requires at least %d prompt bytes for a %d-byte parent and "+
					"%d unique cache misses, exceeding the %d-byte limit; reduce the input or "+
					"raise the contextual prompt budget after reviewing provider cost",
				estimatedPromptBytes,
				len(s.parentText),
				len(uncachedKeys),
				s.maxPromptBytes,
			)
		}
		totalPromptBytes = estimatedPromptBytes
	}

	contextualized := make([]*document.Document, len(prepared))
	for i, item := range prepared {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		clone := item.document
		cacheKey := item.cacheKey
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

		clone.EmbeddingText = contextualEmbeddingText(contextText, item.baseText)
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

// embeddingText mirrors knowledge.Load's baseline embedding-text construction.
// Keep the behavioral test against a real Knowledge.Load when this changes.
func embeddingText(doc *document.Document) string {
	if doc.EmbeddingText != "" {
		return doc.EmbeddingText
	}
	if doc.Metadata == nil {
		return doc.Content
	}

	var prefix strings.Builder
	if fileName, ok := doc.Metadata[source.MetaFileName].(string); ok && fileName != "" {
		prefix.WriteString("file: ")
		prefix.WriteString(fileName)
	}
	if chunkIndex, ok := embeddingChunkIndex(doc.Metadata[source.MetaChunkIndex]); ok && chunkIndex > 0 {
		if prefix.Len() > 0 {
			prefix.WriteString(" | ")
		}
		prefix.WriteString("chunk: ")
		prefix.WriteString(strconv.Itoa(chunkIndex))
	}
	if headerPath, ok := doc.Metadata[source.MetaMarkdownHeaderPath].(string); ok && headerPath != "" {
		if prefix.Len() > 0 {
			prefix.WriteString(" | ")
		}
		prefix.WriteString("section: ")
		prefix.WriteString(headerPath)
	}
	if prefix.Len() == 0 {
		return doc.Content
	}
	return prefix.String() + "\n" + doc.Content
}

// embeddingChunkIndex mirrors the conversion accepted by knowledge.Load when
// it builds baseline embedding text. The behavioral test using Knowledge.Load
// protects this example from silently drifting from that framework contract.
func embeddingChunkIndex(value any) (int, bool) {
	if value == nil {
		return 0, false
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if rv.IsNil() {
			return 0, false
		}
	}
	switch value := value.(type) {
	case int:
		return value, true
	case float64:
		return int(value), true
	case float32:
		return int(value), true
	case string:
		if value == "" || value == "<nil>" {
			return 0, false
		}
		converted, err := strconv.Atoi(value)
		return converted, err == nil
	case json.Number:
		converted, err := value.Int64()
		return int(converted), err == nil
	default:
		converted, err := strconv.Atoi(fmt.Sprintf("%v", value))
		return converted, err == nil
	}
}

func contextualEmbeddingText(contextText, baseText string) string {
	return "Context:\n" + strings.TrimSpace(contextText) +
		"\n\n--- Original chunk ---\n" + baseText
}
