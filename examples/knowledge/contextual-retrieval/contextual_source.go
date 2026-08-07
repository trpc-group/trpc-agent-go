//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

const (
	indexVariantBaseline   = "baseline"
	indexVariantContextual = "contextual"

	contextualMethodVersion       = "contextual-retrieval-example/v1"
	embeddingTextFormatVersionV1  = "contextual-embedding-text/v1"
	metadataIndexVariant          = "index_variant"
	metadataMethodVersion         = "contextual_method_version"
	metadataProviderIdentity      = "context_provider_identity"
	metadataPromptVersion         = "prompt_version"
	metadataEmbeddingFormat       = "embedding_text_format_version"
	metadataContextSetDigest      = "context_set_digest"
	baselineProviderIdentity      = "none"
	contextualSourceNameSeparator = " / "
)

type parentResolver interface {
	Resolve(ctx context.Context, chunk *document.Document) (string, error)
}

type contextualSourceConfig struct {
	Delegate                   source.Source
	Variant                    string
	Provider                   contextProvider
	Resolver                   parentResolver
	Cache                      *contextCache
	Workers                    int
	CacheOnly                  bool
	PromptVersion              string
	EmbeddingTextFormatVersion string
}

type contextualSource struct {
	delegate                   source.Source
	variant                    string
	provider                   contextProvider
	resolver                   parentResolver
	cache                      *contextCache
	workers                    int
	cacheOnly                  bool
	promptVersion              string
	embeddingTextFormatVersion string

	mu               sync.RWMutex
	contextSetDigest string
	inflightMu       sync.Mutex
	inflight         map[string]*contextGeneration
}

type contextGeneration struct {
	done        chan struct{}
	contextText string
	err         error
}

func newContextualSource(cfg contextualSourceConfig) (*contextualSource, error) {
	if cfg.Delegate == nil {
		return nil, errors.New("delegate source is required")
	}
	if cfg.Variant != indexVariantBaseline && cfg.Variant != indexVariantContextual {
		return nil, fmt.Errorf("unsupported index variant %q", cfg.Variant)
	}
	if strings.TrimSpace(cfg.PromptVersion) == "" {
		return nil, errors.New("prompt version is required")
	}
	if strings.TrimSpace(cfg.EmbeddingTextFormatVersion) == "" {
		return nil, errors.New("embedding text format version is required")
	}
	if cfg.Variant == indexVariantContextual {
		if cfg.Workers <= 0 {
			return nil, errors.New("context workers must be greater than zero")
		}
		if cfg.Provider == nil {
			return nil, errors.New("context provider is required for contextual indexing")
		}
		if strings.TrimSpace(cfg.Provider.Identity()) == "" {
			return nil, errors.New("context provider identity is required")
		}
		if cfg.Resolver == nil {
			return nil, errors.New("parent resolver is required for contextual indexing")
		}
		if cfg.Cache == nil {
			return nil, errors.New("context cache is required for contextual indexing")
		}
	} else if cfg.Workers <= 0 {
		cfg.Workers = 1
	}

	return &contextualSource{
		delegate:                   cfg.Delegate,
		variant:                    cfg.Variant,
		provider:                   cfg.Provider,
		resolver:                   cfg.Resolver,
		cache:                      cfg.Cache,
		workers:                    cfg.Workers,
		cacheOnly:                  cfg.CacheOnly,
		promptVersion:              strings.TrimSpace(cfg.PromptVersion),
		embeddingTextFormatVersion: strings.TrimSpace(cfg.EmbeddingTextFormatVersion),
		inflight:                   make(map[string]*contextGeneration),
	}, nil
}

func (s *contextualSource) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	docs, err := s.delegate.ReadDocuments(ctx)
	if err != nil {
		return nil, fmt.Errorf("read delegate documents: %w", err)
	}
	clones := make([]*document.Document, len(docs))
	for i, doc := range docs {
		if doc == nil {
			return nil, fmt.Errorf("delegate returned nil document at index %d", i)
		}
		clones[i] = doc.Clone()
		clones[i].EmbeddingText = baseEmbeddingText(clones[i])
	}

	if s.variant == indexVariantContextual {
		if err := s.contextualizeDocuments(ctx, clones); err != nil {
			return nil, err
		}
	}

	digest := documentSetDigest(clones)
	s.mu.Lock()
	s.contextSetDigest = digest
	s.mu.Unlock()
	return clones, nil
}

func (s *contextualSource) Name() string {
	return s.delegate.Name() + contextualSourceNameSeparator + s.variant
}

func (s *contextualSource) Type() string {
	return s.delegate.Type()
}

func (s *contextualSource) GetMetadata() map[string]any {
	metadata := make(map[string]any)
	for key, value := range s.delegate.GetMetadata() {
		metadata[key] = value
	}

	providerIdentity := baselineProviderIdentity
	if s.provider != nil {
		providerIdentity = s.provider.Identity()
	}
	s.mu.RLock()
	digest := s.contextSetDigest
	s.mu.RUnlock()

	metadata[metadataIndexVariant] = s.variant
	metadata[metadataMethodVersion] = contextualMethodVersion
	metadata[metadataProviderIdentity] = providerIdentity
	metadata[metadataPromptVersion] = s.promptVersion
	metadata[metadataEmbeddingFormat] = s.embeddingTextFormatVersion
	metadata[metadataContextSetDigest] = digest
	return metadata
}

func (s *contextualSource) contextualizeDocuments(
	ctx context.Context,
	docs []*document.Document,
) error {
	if len(docs) == 0 {
		return nil
	}

	workerCount := s.workers
	if workerCount > len(docs) {
		workerCount = len(docs)
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan int)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	recordError := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		errMu.Unlock()
	}

	for worker := 0; worker < workerCount; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-workCtx.Done():
					return
				case index, ok := <-jobs:
					if !ok {
						return
					}
					if err := s.contextualizeDocument(workCtx, docs[index]); err != nil {
						recordError(fmt.Errorf("contextualize document %d: %w", index, err))
						return
					}
				}
			}
		}()
	}

sendLoop:
	for index := range docs {
		select {
		case <-workCtx.Done():
			break sendLoop
		case jobs <- index:
		}
	}
	close(jobs)
	wg.Wait()

	errMu.Lock()
	err := firstErr
	errMu.Unlock()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (s *contextualSource) contextualizeDocument(
	ctx context.Context,
	doc *document.Document,
) error {
	parentText, err := s.resolver.Resolve(ctx, doc)
	if err != nil {
		return fmt.Errorf("resolve parent document: %w", err)
	}

	descriptor := contextDescriptor{
		ProviderIdentity:           s.provider.Identity(),
		PromptVersion:              s.promptVersion,
		EmbeddingTextFormatVersion: s.embeddingTextFormatVersion,
		ParentSHA256:               hashText(parentText),
		ChunkSHA256:                hashText(doc.Content),
	}
	contextText, err := s.contextForDescriptor(ctx, descriptor, parentText, doc)
	if err != nil {
		return err
	}

	doc.EmbeddingText = contextualEmbeddingText(contextText, doc.EmbeddingText)
	return nil
}

func (s *contextualSource) contextForDescriptor(
	ctx context.Context,
	descriptor contextDescriptor,
	parentText string,
	doc *document.Document,
) (string, error) {
	if contextText, ok := s.cache.get(descriptor); ok {
		return contextText, nil
	}
	if s.cacheOnly {
		return "", errors.New("context cache miss in cache-only mode")
	}

	key := descriptor.key()
	s.inflightMu.Lock()
	if contextText, ok := s.cache.get(descriptor); ok {
		s.inflightMu.Unlock()
		return contextText, nil
	}
	if pending, ok := s.inflight[key]; ok {
		s.inflightMu.Unlock()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-pending.done:
			return pending.contextText, pending.err
		}
	}
	pending := &contextGeneration{done: make(chan struct{})}
	s.inflight[key] = pending
	s.inflightMu.Unlock()

	contextText, err := s.provider.Generate(ctx, parentText, doc)
	if err != nil {
		err = fmt.Errorf("generate context: %w", err)
	} else {
		contextText = strings.TrimSpace(contextText)
		if contextText == "" {
			err = errors.New("context provider returned empty context")
		} else if cacheErr := s.cache.put(descriptor, contextText); cacheErr != nil {
			err = fmt.Errorf("persist context: %w", cacheErr)
		}
	}

	s.inflightMu.Lock()
	pending.contextText = contextText
	pending.err = err
	delete(s.inflight, key)
	close(pending.done)
	s.inflightMu.Unlock()
	return contextText, err
}

func baseEmbeddingText(doc *document.Document) string {
	if doc == nil {
		return ""
	}
	if doc.EmbeddingText != "" {
		return doc.EmbeddingText
	}
	return doc.Content
}

func contextualEmbeddingText(contextText, baseText string) string {
	return "Context:\n" + strings.TrimSpace(contextText) +
		"\n\n--- Original chunk ---\n" + baseText
}

func documentSetDigest(docs []*document.Document) string {
	parts := make([]string, 0, len(docs)*5)
	for index, doc := range docs {
		parts = append(parts,
			strconv.Itoa(index),
			metadataString(doc.Metadata, source.MetaURI),
			metadataString(doc.Metadata, source.MetaChunkIndex),
			hashText(doc.Content),
			hashText(doc.EmbeddingText),
		)
	}
	return hashParts(parts...)
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	return fmt.Sprint(metadata[key])
}

type localFileParentResolver struct {
	allowedPaths map[string]struct{}
	mu           sync.RWMutex
	parents      map[string]string
}

func newLocalFileParentResolver(paths []string) (*localFileParentResolver, error) {
	if len(paths) == 0 {
		return nil, errors.New("at least one input file is required")
	}
	resolver := &localFileParentResolver{
		allowedPaths: make(map[string]struct{}, len(paths)),
		parents:      make(map[string]string, len(paths)),
	}
	for _, path := range paths {
		absolutePath, err := normalizeSupportedTextPath(path)
		if err != nil {
			return nil, err
		}
		resolver.allowedPaths[absolutePath] = struct{}{}
	}
	return resolver, nil
}

func (r *localFileParentResolver) Resolve(
	ctx context.Context,
	chunk *document.Document,
) (string, error) {
	if chunk == nil {
		return "", errors.New("chunk is nil")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path, ok := chunk.Metadata[source.MetaFilePath].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return "", errors.New("chunk is missing local file path metadata")
	}
	absolutePath, err := normalizeSupportedTextPath(path)
	if err != nil {
		return "", err
	}
	if _, ok := r.allowedPaths[absolutePath]; !ok {
		return "", errors.New("chunk file path is outside the configured input set")
	}
	r.mu.RLock()
	parentText, ok := r.parents[absolutePath]
	r.mu.RUnlock()
	if ok {
		return parentText, nil
	}

	content, err := os.ReadFile(absolutePath)
	if err != nil {
		return "", fmt.Errorf("read parent document: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !utf8.Valid(content) {
		return "", errors.New("parent document is not valid UTF-8 text")
	}
	if strings.TrimSpace(string(content)) == "" {
		return "", errors.New("parent document is empty")
	}
	parentText = string(content)
	r.mu.Lock()
	if cached, exists := r.parents[absolutePath]; exists {
		parentText = cached
	} else {
		r.parents[absolutePath] = parentText
	}
	r.mu.Unlock()
	return parentText, nil
}

func normalizeSupportedTextPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("input file path is empty")
	}
	extension := strings.ToLower(filepath.Ext(path))
	switch extension {
	case ".md", ".markdown", ".txt", ".text":
	default:
		return "", fmt.Errorf("unsupported input extension %q; use local .md or .txt files", extension)
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve input path: %w", err)
	}
	return filepath.Clean(absolutePath), nil
}
