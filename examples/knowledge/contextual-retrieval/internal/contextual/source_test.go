//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package contextual

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

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
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
	return s.metadata
}

type fakeGenerator struct {
	generate func(context.Context, string, string) (string, error)

	mu    sync.Mutex
	calls int
}

func (g *fakeGenerator) Generate(
	ctx context.Context,
	parentText string,
	chunkText string,
) (string, error) {
	g.mu.Lock()
	g.calls++
	g.mu.Unlock()
	return g.generate(ctx, parentText, chunkText)
}

func (g *fakeGenerator) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

func TestSourceChangesOnlyEmbeddingText(t *testing.T) {
	original := testDocuments()
	metadata := map[string]any{"team": "rag"}
	generator := &fakeGenerator{generate: func(
		_ context.Context,
		parentText string,
		chunkText string,
	) (string, error) {
		if parentText != "parent document" {
			t.Fatalf("parent = %q, want parent document", parentText)
		}
		return "Context for " + chunkText, nil
	}}
	src := newTestSource(t, &fakeSource{docs: original, metadata: metadata}, generator, "parent document")

	got, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments: %v", err)
	}
	if len(got) != len(original) {
		t.Fatalf("document count = %d, want %d", len(got), len(original))
	}
	for i := range original {
		want := original[i]
		actual := got[i]
		if actual == want {
			t.Errorf("document %d was not cloned", i)
		}
		if actual.ID != want.ID || actual.Name != want.Name || actual.Content != want.Content {
			t.Errorf("document %d identity or content changed", i)
		}
		if !reflect.DeepEqual(actual.Metadata, want.Metadata) {
			t.Errorf("document %d metadata changed: got %v want %v", i, actual.Metadata, want.Metadata)
		}
		wantEmbedding := contextualEmbeddingText("Context for "+want.Content, embeddingText(want))
		if actual.EmbeddingText != wantEmbedding {
			t.Errorf("document %d EmbeddingText = %q, want %q", i, actual.EmbeddingText, wantEmbedding)
		}
	}
	if original[0].EmbeddingText != "" || original[1].EmbeddingText != "specialized embedding text" {
		t.Fatal("source mutated documents owned by its delegate")
	}
	if generator.callCount() != len(original) {
		t.Fatalf("generator calls = %d, want %d", generator.callCount(), len(original))
	}
	if src.Name() != "test source" || src.Type() != source.TypeFile {
		t.Fatalf("source identity was not delegated: name=%q type=%q", src.Name(), src.Type())
	}
	if !reflect.DeepEqual(src.GetMetadata(), metadata) {
		t.Fatalf("source metadata = %v, want %v", src.GetMetadata(), metadata)
	}
}

func TestSourceCacheHitSkipsGenerator(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "contexts.json")
	docs := testDocuments()[:1]
	firstGenerator := &fakeGenerator{generate: func(
		context.Context,
		string,
		string,
	) (string, error) {
		return "cached context", nil
	}}
	first := newTestSourceAtPath(t, &fakeSource{docs: docs}, firstGenerator, "parent", cachePath)
	if _, err := first.ReadDocuments(context.Background()); err != nil {
		t.Fatalf("populate cache: %v", err)
	}

	secondGenerator := &fakeGenerator{generate: func(
		context.Context,
		string,
		string,
	) (string, error) {
		return "", errors.New("generator must not run on a cache hit")
	}}
	second := newTestSourceAtPath(t, &fakeSource{docs: docs}, secondGenerator, "parent", cachePath)
	got, err := second.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("read cached documents: %v", err)
	}
	if secondGenerator.callCount() != 0 {
		t.Fatalf("generator calls = %d, want 0", secondGenerator.callCount())
	}
	if !strings.Contains(got[0].EmbeddingText, "cached context") {
		t.Fatalf("EmbeddingText = %q, want cached context", got[0].EmbeddingText)
	}
}

func TestSourceFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		delegate *fakeSource
		generate func(context.Context, string, string) (string, error)
		wantErr  string
	}{
		{
			name:     "delegate error",
			delegate: &fakeSource{err: errors.New("read failed")},
			generate: func(context.Context, string, string) (string, error) { return "unused", nil },
			wantErr:  "read failed",
		},
		{
			name:     "nil document",
			delegate: &fakeSource{docs: []*document.Document{nil}},
			generate: func(context.Context, string, string) (string, error) { return "unused", nil },
			wantErr:  "nil document",
		},
		{
			name:     "generator error",
			delegate: &fakeSource{docs: testDocuments()[:1]},
			generate: func(context.Context, string, string) (string, error) {
				return "", errors.New("provider unavailable")
			},
			wantErr: "provider unavailable",
		},
		{
			name:     "empty context",
			delegate: &fakeSource{docs: testDocuments()[:1]},
			generate: func(context.Context, string, string) (string, error) { return "  ", nil },
			wantErr:  "empty context",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := newTestSource(t, tt.delegate, &fakeGenerator{generate: tt.generate}, "parent")
			if _, err := src.ReadDocuments(context.Background()); err == nil ||
				!strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ReadDocuments error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestSourceHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	generator := &fakeGenerator{generate: func(
		ctx context.Context,
		_ string,
		_ string,
	) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}}
	src := newTestSource(t, &fakeSource{docs: testDocuments()[:1]}, generator, "parent")
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

func TestSourceReusesContextForDuplicateChunks(t *testing.T) {
	docs := []*document.Document{
		testDocument("duplicate chunk", 0),
		testDocument("duplicate chunk", 1),
	}
	generator := &fakeGenerator{generate: func(
		context.Context,
		string,
		string,
	) (string, error) {
		return "shared context", nil
	}}
	src := newTestSource(t, &fakeSource{docs: docs}, generator, "parent")
	got, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments: %v", err)
	}
	if generator.callCount() != 1 {
		t.Fatalf("generator calls = %d, want 1", generator.callCount())
	}
	for i, doc := range got {
		if !strings.Contains(doc.EmbeddingText, "shared context") {
			t.Errorf("document %d is missing shared context", i)
		}
	}
}

func TestSourceWrapsPublicFileSource(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "parent.md")
	content := "# Retrieval\n\nA parent document gives meaning to each smaller chunk. " +
		"The original chunk should still be returned to the Agent."
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	generator := &fakeGenerator{generate: func(
		context.Context,
		string,
		string,
	) (string, error) {
		return "This chunk belongs to a contextual retrieval document.", nil
	}}
	cache, err := openContextCache(filepath.Join(directory, "contexts.json"))
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	src, err := newContextualSource(
		filesource.New(
			[]string{path},
			filesource.WithChunkSize(60),
			filesource.WithChunkOverlap(10),
		),
		content,
		generator,
		"test-model",
		cache,
	)
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
	}
}

func TestNewContextualSourceValidatesRequiredInputs(t *testing.T) {
	cache, err := openContextCache(filepath.Join(t.TempDir(), "contexts.json"))
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	generator := &fakeGenerator{generate: func(context.Context, string, string) (string, error) {
		return "context", nil
	}}
	tests := []struct {
		name      string
		delegate  source.Source
		parent    string
		generator contextGenerator
		modelName string
		cache     *contextCache
	}{
		{"delegate", nil, "parent", generator, "model", cache},
		{"parent", &fakeSource{}, " ", generator, "model", cache},
		{"generator", &fakeSource{}, "parent", nil, "model", cache},
		{"model name", &fakeSource{}, "parent", generator, " ", cache},
		{"cache", &fakeSource{}, "parent", generator, "model", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := newContextualSource(
				tt.delegate,
				tt.parent,
				tt.generator,
				tt.modelName,
				tt.cache,
			); err == nil {
				t.Fatal("newContextualSource unexpectedly succeeded")
			}
		})
	}
}

func newTestSource(
	t *testing.T,
	delegate source.Source,
	generator contextGenerator,
	parent string,
) *contextualSource {
	t.Helper()
	return newTestSourceAtPath(
		t,
		delegate,
		generator,
		parent,
		filepath.Join(t.TempDir(), "contexts.json"),
	)
}

func newTestSourceAtPath(
	t *testing.T,
	delegate source.Source,
	generator contextGenerator,
	parent string,
	cachePath string,
) *contextualSource {
	t.Helper()
	cache, err := openContextCache(cachePath)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	src, err := newContextualSource(delegate, parent, generator, "test-model", cache)
	if err != nil {
		t.Fatalf("new source: %v", err)
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
			"custom":              fmt.Sprintf("value-%d", index),
		},
	}
}
