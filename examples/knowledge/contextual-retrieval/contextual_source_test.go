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
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	knowledgeutil "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type fakeSource struct {
	docs     []*document.Document
	metadata map[string]any
	err      error
}

func (s *fakeSource) ReadDocuments(context.Context) ([]*document.Document, error) {
	return s.docs, s.err
}

func (*fakeSource) Name() string { return "test source" }

func (*fakeSource) Type() string { return source.TypeFile }

func (s *fakeSource) GetMetadata() map[string]any {
	metadata := make(map[string]any, len(s.metadata))
	for key, value := range s.metadata {
		metadata[key] = value
	}
	return metadata
}

type fakeResolver struct {
	resolve func(context.Context, *document.Document) (string, error)
}

func (r fakeResolver) Resolve(
	ctx context.Context,
	doc *document.Document,
) (string, error) {
	return r.resolve(ctx, doc)
}

type fakeContextProvider struct {
	identity string
	generate func(context.Context, string, *document.Document) (string, error)

	mu    sync.Mutex
	calls int
}

func (p *fakeContextProvider) Generate(
	ctx context.Context,
	parent string,
	doc *document.Document,
) (string, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return p.generate(ctx, parent, doc)
}

func (p *fakeContextProvider) Identity() string { return p.identity }

func (p *fakeContextProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func TestContextualSourceChangesOnlyEmbeddingText(t *testing.T) {
	original := testDocuments()
	baseline, err := newContextualSource(contextualSourceConfig{
		Delegate:                   &fakeSource{docs: original, metadata: map[string]any{"team": "rag"}},
		Variant:                    indexVariantBaseline,
		Workers:                    2,
		PromptVersion:              contextPromptVersionV1,
		EmbeddingTextFormatVersion: embeddingTextFormatVersionV1,
	})
	if err != nil {
		t.Fatalf("new baseline source: %v", err)
	}
	baselineDocs, err := baseline.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("read baseline documents: %v", err)
	}

	provider := &fakeContextProvider{
		identity: "fake:model-a",
		generate: func(_ context.Context, _ string, doc *document.Document) (string, error) {
			return "Context for " + doc.Content, nil
		},
	}
	cache, err := openContextCache(filepath.Join(t.TempDir(), "contexts.jsonl"))
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	contextual, err := newContextualSource(contextualSourceConfig{
		Delegate: &fakeSource{docs: original, metadata: map[string]any{"team": "rag"}},
		Variant:  indexVariantContextual,
		Provider: provider,
		Resolver: fakeResolver{resolve: func(_ context.Context, doc *document.Document) (string, error) {
			return "Parent for " + doc.Content, nil
		}},
		Cache:                      cache,
		Workers:                    2,
		PromptVersion:              contextPromptVersionV1,
		EmbeddingTextFormatVersion: embeddingTextFormatVersionV1,
	})
	if err != nil {
		t.Fatalf("new contextual source: %v", err)
	}
	contextualDocs, err := contextual.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("read contextual documents: %v", err)
	}

	if len(baselineDocs) != len(contextualDocs) {
		t.Fatalf("document count differs: baseline=%d contextual=%d", len(baselineDocs), len(contextualDocs))
	}
	for i := range baselineDocs {
		baselineDoc := baselineDocs[i]
		contextualDoc := contextualDocs[i]
		if baselineDoc.Content != contextualDoc.Content {
			t.Errorf("document %d content changed", i)
		}
		if !reflect.DeepEqual(baselineDoc.Metadata, contextualDoc.Metadata) {
			t.Errorf("document %d metadata changed: baseline=%v contextual=%v", i, baselineDoc.Metadata, contextualDoc.Metadata)
		}
		if baselineDoc.ID != contextualDoc.ID || baselineDoc.Name != contextualDoc.Name {
			t.Errorf("document %d identity fields changed before framework indexing", i)
		}
		if contextualDoc.EmbeddingText == baselineDoc.EmbeddingText {
			t.Errorf("document %d embedding text did not change", i)
		}
		want := contextualEmbeddingText("Context for "+baselineDoc.Content, baselineDoc.EmbeddingText)
		if contextualDoc.EmbeddingText != want {
			t.Errorf("document %d embedding text = %q, want %q", i, contextualDoc.EmbeddingText, want)
		}
	}
	if original[0].EmbeddingText != "" || original[1].EmbeddingText != "specialized embedding text" {
		t.Fatal("wrapper mutated documents owned by its delegate")
	}
	if provider.callCount() != len(original) {
		t.Fatalf("provider calls = %d, want %d", provider.callCount(), len(original))
	}

	metadata := contextual.GetMetadata()
	if metadata[metadataIndexVariant] != indexVariantContextual {
		t.Errorf("index variant metadata = %v", metadata[metadataIndexVariant])
	}
	if metadata[metadataProviderIdentity] != provider.Identity() {
		t.Errorf("provider identity metadata = %v", metadata[metadataProviderIdentity])
	}
	if metadata[metadataContextSetDigest] == "" {
		t.Error("context set digest is empty")
	}
}

func TestContextualSourceCacheHitSkipsProvider(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "contexts.jsonl")
	firstProvider := &fakeContextProvider{
		identity: "fake:model-a",
		generate: func(context.Context, string, *document.Document) (string, error) {
			return "cached context", nil
		},
	}
	first := newTestContextualSource(t, cachePath, firstProvider, contextPromptVersionV1, "parent", testDocuments()[:1])
	if _, err := first.ReadDocuments(context.Background()); err != nil {
		t.Fatalf("populate cache: %v", err)
	}

	secondProvider := &fakeContextProvider{
		identity: "fake:model-a",
		generate: func(context.Context, string, *document.Document) (string, error) {
			return "", errors.New("provider must not be called on cache hit")
		},
	}
	second := newTestContextualSource(t, cachePath, secondProvider, contextPromptVersionV1, "parent", testDocuments()[:1])
	docs, err := second.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("read cached documents: %v", err)
	}
	if secondProvider.callCount() != 0 {
		t.Fatalf("provider calls = %d, want 0", secondProvider.callCount())
	}
	if !strings.Contains(docs[0].EmbeddingText, "cached context") {
		t.Errorf("cached context missing from embedding text: %q", docs[0].EmbeddingText)
	}
	info, err := os.Stat(cachePath)
	if err != nil {
		t.Fatalf("stat cache: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("cache mode = %o, want 600", got)
	}
}

func TestContextualSourceFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		generate   func(context.Context, string, *document.Document) (string, error)
		wantErr    string
		cacheOnly  bool
		providerID string
	}{
		{
			name: "provider error",
			generate: func(context.Context, string, *document.Document) (string, error) {
				return "", errors.New("provider unavailable")
			},
			wantErr:    "provider unavailable",
			providerID: "fake:error",
		},
		{
			name: "empty context",
			generate: func(context.Context, string, *document.Document) (string, error) {
				return "  ", nil
			},
			wantErr:    "empty context",
			providerID: "fake:empty",
		},
		{
			name: "cache-only miss",
			generate: func(context.Context, string, *document.Document) (string, error) {
				return "unexpected", nil
			},
			wantErr:    "cache miss",
			cacheOnly:  true,
			providerID: "fake:cache-only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &fakeContextProvider{identity: tt.providerID, generate: tt.generate}
			cache, err := openContextCache(filepath.Join(t.TempDir(), "contexts.jsonl"))
			if err != nil {
				t.Fatalf("open cache: %v", err)
			}
			src, err := newContextualSource(contextualSourceConfig{
				Delegate: &fakeSource{docs: testDocuments()[:1]},
				Variant:  indexVariantContextual,
				Provider: provider,
				Resolver: fakeResolver{resolve: func(context.Context, *document.Document) (string, error) {
					return "parent", nil
				}},
				Cache:                      cache,
				Workers:                    1,
				CacheOnly:                  tt.cacheOnly,
				PromptVersion:              contextPromptVersionV1,
				EmbeddingTextFormatVersion: embeddingTextFormatVersionV1,
			})
			if err != nil {
				t.Fatalf("new source: %v", err)
			}
			if _, err := src.ReadDocuments(context.Background()); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ReadDocuments error = %v, want containing %q", err, tt.wantErr)
			}
			if tt.cacheOnly && provider.callCount() != 0 {
				t.Fatalf("cache-only provider calls = %d, want 0", provider.callCount())
			}
		})
	}
}

func TestContextualSourceHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	provider := &fakeContextProvider{
		identity: "fake:blocking",
		generate: func(ctx context.Context, _ string, _ *document.Document) (string, error) {
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		},
	}
	src := newTestContextualSource(
		t,
		filepath.Join(t.TempDir(), "contexts.jsonl"),
		provider,
		contextPromptVersionV1,
		"parent",
		testDocuments()[:1],
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := src.ReadDocuments(ctx)
		done <- err
	}()
	<-started
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ReadDocuments error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ReadDocuments did not stop after cancellation")
	}
}

func TestContextualSourcePreservesOrderWithConcurrentWorkers(t *testing.T) {
	docs := []*document.Document{
		testDocument("slow", 0),
		testDocument("fast", 1),
		testDocument("medium", 2),
	}
	delays := map[string]time.Duration{
		"slow":   40 * time.Millisecond,
		"fast":   2 * time.Millisecond,
		"medium": 15 * time.Millisecond,
	}
	provider := &fakeContextProvider{
		identity: "fake:concurrent",
		generate: func(_ context.Context, _ string, doc *document.Document) (string, error) {
			time.Sleep(delays[doc.Content])
			return "context-" + doc.Content, nil
		},
	}
	src := newTestContextualSource(
		t,
		filepath.Join(t.TempDir(), "contexts.jsonl"),
		provider,
		contextPromptVersionV1,
		"parent",
		docs,
	)
	src.workers = 3
	got, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments: %v", err)
	}
	for i, want := range []string{"slow", "fast", "medium"} {
		if got[i].Content != want {
			t.Errorf("document %d content = %q, want %q", i, got[i].Content, want)
		}
		if !strings.Contains(got[i].EmbeddingText, "context-"+want) {
			t.Errorf("document %d has wrong context: %q", i, got[i].EmbeddingText)
		}
	}
}

func TestContextualSourceDeduplicatesConcurrentContextGeneration(t *testing.T) {
	docs := []*document.Document{
		testDocument("duplicate chunk", 0),
		testDocument("duplicate chunk", 1),
	}
	provider := &fakeContextProvider{
		identity: "fake:deduplicated",
		generate: func(context.Context, string, *document.Document) (string, error) {
			time.Sleep(20 * time.Millisecond)
			return "shared context", nil
		},
	}
	src := newTestContextualSource(
		t,
		filepath.Join(t.TempDir(), "contexts.jsonl"),
		provider,
		contextPromptVersionV1,
		"same parent",
		docs,
	)
	src.workers = 2
	got, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments: %v", err)
	}
	if provider.callCount() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.callCount())
	}
	for i, doc := range got {
		if !strings.Contains(doc.EmbeddingText, "shared context") {
			t.Errorf("document %d missing shared context", i)
		}
	}
}

func TestContextualSourceIdentityTracksInputs(t *testing.T) {
	build := func(providerID, promptVersion, contextText string) map[string]any {
		provider := &fakeContextProvider{
			identity: providerID,
			generate: func(context.Context, string, *document.Document) (string, error) {
				return contextText, nil
			},
		}
		src := newTestContextualSource(
			t,
			filepath.Join(t.TempDir(), "contexts.jsonl"),
			provider,
			promptVersion,
			"parent",
			testDocuments()[:1],
		)
		if _, err := src.ReadDocuments(context.Background()); err != nil {
			t.Fatalf("ReadDocuments: %v", err)
		}
		return src.GetMetadata()
	}

	base := build("fake:model-a", "prompt/v1", "context-a")
	providerChanged := build("fake:model-b", "prompt/v1", "context-a")
	promptChanged := build("fake:model-a", "prompt/v2", "context-a")
	contextChanged := build("fake:model-a", "prompt/v1", "context-b")
	if reflect.DeepEqual(base, providerChanged) {
		t.Error("provider identity change did not change source identity")
	}
	if reflect.DeepEqual(base, promptChanged) {
		t.Error("prompt version change did not change source identity")
	}
	if base[metadataContextSetDigest] == contextChanged[metadataContextSetDigest] {
		t.Error("context change did not change context set digest")
	}
}

func TestLocalFileParentResolver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "parent.md")
	if err := os.WriteFile(path, []byte("parent document"), 0o600); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	resolver, err := newLocalFileParentResolver([]string{path})
	if err != nil {
		t.Fatalf("new resolver: %v", err)
	}
	doc := testDocument("chunk", 0)
	doc.Metadata[source.MetaFilePath] = path
	got, err := resolver.Resolve(context.Background(), doc)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "parent document" {
		t.Fatalf("Resolve = %q, want parent document", got)
	}

	outside := filepath.Join(dir, "outside.md")
	doc.Metadata[source.MetaFilePath] = outside
	if _, err := resolver.Resolve(context.Background(), doc); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("outside path error = %v", err)
	}
	if _, err := newLocalFileParentResolver([]string{filepath.Join(dir, "parent.pdf")}); err == nil {
		t.Fatal("PDF input was accepted")
	}
}

func TestContextualSourceWrapsPublicFileSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "parent.md")
	content := "# Retrieval\n\nA parent document gives meaning to each smaller chunk. " +
		"The original chunk should still be returned to the Agent."
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	resolver, err := newLocalFileParentResolver([]string{path})
	if err != nil {
		t.Fatalf("new resolver: %v", err)
	}
	cache, err := openContextCache(filepath.Join(dir, "contexts.jsonl"))
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	provider := &fakeContextProvider{
		identity: "fake:file-source",
		generate: func(context.Context, string, *document.Document) (string, error) {
			return "This chunk belongs to a contextual retrieval document.", nil
		},
	}
	src, err := newContextualSource(contextualSourceConfig{
		Delegate: filesource.New(
			[]string{path},
			filesource.WithChunkSize(60),
			filesource.WithChunkOverlap(10),
		),
		Variant:                    indexVariantContextual,
		Provider:                   provider,
		Resolver:                   resolver,
		Cache:                      cache,
		Workers:                    2,
		PromptVersion:              contextPromptVersionV1,
		EmbeddingTextFormatVersion: embeddingTextFormatVersionV1,
	})
	if err != nil {
		t.Fatalf("new source: %v", err)
	}
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("file source returned no chunks")
	}
	for i, doc := range docs {
		if strings.Contains(doc.Content, "Context:\n") {
			t.Errorf("document %d content was rewritten: %q", i, doc.Content)
		}
		if !strings.HasPrefix(doc.EmbeddingText, "Context:\n") {
			t.Errorf("document %d embedding text is not contextual: %q", i, doc.EmbeddingText)
		}
		if doc.Metadata[source.MetaFilePath] != path {
			t.Errorf("document %d file path metadata = %v, want %s", i, doc.Metadata[source.MetaFilePath], path)
		}
	}
}

func TestModelContextProviderResponseHandling(t *testing.T) {
	t.Run("aggregates deltas", func(t *testing.T) {
		llm := &fakeModel{responses: []*model.Response{
			{Choices: []model.Choice{{Delta: model.Message{Content: "short "}}}},
			{Choices: []model.Choice{{Delta: model.Message{Content: "context"}}}},
		}}
		provider, err := newModelContextProvider(llm, "test-model", "test-provider")
		if err != nil {
			t.Fatalf("new provider: %v", err)
		}
		got, err := provider.Generate(context.Background(), "parent", testDocument("chunk", 0))
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if got != "short context" {
			t.Fatalf("Generate = %q, want short context", got)
		}
		if llm.request == nil || llm.request.Temperature == nil ||
			*llm.request.Temperature != 0 {
			t.Fatal("context request did not set temperature to zero")
		}
	})

	t.Run("response error", func(t *testing.T) {
		llm := &fakeModel{responses: []*model.Response{{
			Error: &model.ResponseError{Message: "upstream failed"},
		}}}
		provider, _ := newModelContextProvider(llm, "test-model", "test-provider")
		if _, err := provider.Generate(context.Background(), "parent", testDocument("chunk", 0)); err == nil ||
			!strings.Contains(err.Error(), "upstream failed") {
			t.Fatalf("Generate error = %v", err)
		}
	})

	t.Run("empty response", func(t *testing.T) {
		provider, _ := newModelContextProvider(&fakeModel{}, "test-model", "test-provider")
		if _, err := provider.Generate(context.Background(), "parent", testDocument("chunk", 0)); err == nil ||
			!strings.Contains(err.Error(), "empty context") {
			t.Fatalf("Generate error = %v", err)
		}
	})
}

func TestConfigureVectorStoreNamespace(t *testing.T) {
	t.Setenv("PGVECTOR_TABLE", "existing_table")
	got, err := configureVectorStoreNamespace(
		knowledgeutil.VectorStorePGVector,
		"business_trial",
		indexVariantContextual,
	)
	if err != nil {
		t.Fatalf("configure namespace: %v", err)
	}
	if got != "business_trial_contextual" {
		t.Fatalf("namespace = %q, want business_trial_contextual", got)
	}
	if table := os.Getenv("PGVECTOR_TABLE"); table != got {
		t.Fatalf("PGVECTOR_TABLE = %q, want %q", table, got)
	}

	if _, err := configureVectorStoreNamespace(
		knowledgeutil.VectorStorePGVector,
		"Invalid-Name",
		indexVariantBaseline,
	); err == nil {
		t.Fatal("invalid namespace was accepted")
	}
	if _, err := configureVectorStoreNamespace(
		knowledgeutil.VectorStoreType("unknown"),
		"business_trial",
		indexVariantBaseline,
	); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unknown vector store error = %v", err)
	}
}

func TestParseInputPaths(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.md")
	second := filepath.Join(dir, "second.txt")
	paths, err := parseInputPaths(first + ", " + second + "," + first)
	if err != nil {
		t.Fatalf("parse input paths: %v", err)
	}
	if len(paths) != 2 || paths[0] != first || paths[1] != second {
		t.Fatalf("paths = %v, want [%s %s]", paths, first, second)
	}
	if _, err := parseInputPaths(filepath.Join(dir, "unsupported.pdf")); err == nil {
		t.Fatal("unsupported input was accepted")
	}
}

func TestVectorOnlyKnowledgeOverridesSearchModeWithoutMutatingRequest(t *testing.T) {
	inner := &capturingKnowledge{}
	wrapped := vectorOnlyKnowledge{Knowledge: inner}
	request := &knowledge.SearchRequest{
		Query:      "query",
		SearchMode: vectorstore.SearchModeHybrid,
	}
	if _, err := wrapped.Search(context.Background(), request); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if request.SearchMode != vectorstore.SearchModeHybrid {
		t.Fatalf("caller request search mode was mutated: %d", request.SearchMode)
	}
	if inner.request == nil || inner.request.SearchMode != vectorstore.SearchModeVector {
		t.Fatalf("inner search mode = %v, want vector", inner.request)
	}
}

type capturingKnowledge struct {
	request *knowledge.SearchRequest
}

func (k *capturingKnowledge) Search(
	_ context.Context,
	request *knowledge.SearchRequest,
) (*knowledge.SearchResult, error) {
	k.request = request
	return &knowledge.SearchResult{}, nil
}

type fakeModel struct {
	responses []*model.Response
	err       error
	request   *model.Request
}

func (m *fakeModel) GenerateContent(
	_ context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	m.request = request
	if m.err != nil {
		return nil, m.err
	}
	responses := make(chan *model.Response, len(m.responses))
	for _, response := range m.responses {
		responses <- response
	}
	close(responses)
	return responses, nil
}

func (*fakeModel) Info() model.Info { return model.Info{Name: "fake"} }

func newTestContextualSource(
	t *testing.T,
	cachePath string,
	provider contextProvider,
	promptVersion string,
	parent string,
	docs []*document.Document,
) *contextualSource {
	t.Helper()
	cache, err := openContextCache(cachePath)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	src, err := newContextualSource(contextualSourceConfig{
		Delegate: &fakeSource{docs: docs},
		Variant:  indexVariantContextual,
		Provider: provider,
		Resolver: fakeResolver{resolve: func(context.Context, *document.Document) (string, error) {
			return parent, nil
		}},
		Cache:                      cache,
		Workers:                    2,
		PromptVersion:              promptVersion,
		EmbeddingTextFormatVersion: embeddingTextFormatVersionV1,
	})
	if err != nil {
		t.Fatalf("new contextual source: %v", err)
	}
	return src
}

func testDocuments() []*document.Document {
	return []*document.Document{
		testDocument("first chunk", 0),
		{
			ID:            "doc-1",
			Name:          "second",
			Content:       "second chunk",
			EmbeddingText: "specialized embedding text",
			Metadata: map[string]any{
				source.MetaURI:        "file:///second.md",
				source.MetaChunkIndex: 1,
				source.MetaFilePath:   "/second.md",
				"custom":              "value-1",
			},
		},
	}
}

func testDocument(content string, index int) *document.Document {
	return &document.Document{
		ID:      fmt.Sprintf("doc-%d", index),
		Name:    fmt.Sprintf("chunk-%d", index),
		Content: content,
		Metadata: map[string]any{
			source.MetaURI:        fmt.Sprintf("file:///document-%d.md", index),
			source.MetaChunkIndex: index,
			source.MetaFilePath:   fmt.Sprintf("/document-%d.md", index),
			"custom":              fmt.Sprintf("value-%d", index),
		},
	}
}
