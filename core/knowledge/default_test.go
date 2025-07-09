package knowledge

import (
	"context"
	"fmt"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/source"
)

// mockSource is a simple mock source for testing.
type mockSource struct {
	name     string
	docCount int
}

func (m *mockSource) Name() string {
	return m.name
}

func (m *mockSource) Type() string {
	return "mock"
}

func (m *mockSource) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	docs := make([]*document.Document, m.docCount)
	for i := 0; i < m.docCount; i++ {
		docs[i] = &document.Document{
			ID:      fmt.Sprintf("doc-%d", i),
			Name:    fmt.Sprintf("Document %d", i),
			Content: fmt.Sprintf("Content for document %d", i),
		}
	}
	return docs, nil
}

func TestBuiltinKnowledge_Load_WithOptions(t *testing.T) {
	// Create a knowledge instance with mock sources.
	kb := New(
		WithSources([]source.Source{
			&mockSource{name: "test-source-1", docCount: 5},
			&mockSource{name: "test-source-2", docCount: 3},
		}),
	)

	ctx := context.Background()

	// Test with default options (should show progress).
	err := kb.Load(ctx)
	if err != nil {
		t.Errorf("Load with default options failed: %v", err)
	}

	// Test with progress disabled.
	err = kb.Load(ctx, WithShowProgress(false))
	if err != nil {
		t.Errorf("Load with progress disabled failed: %v", err)
	}

	// Test with custom progress step size.
	err = kb.Load(ctx, WithProgressStepSize(2))
	if err != nil {
		t.Errorf("Load with custom progress step size failed: %v", err)
	}

	// Test with multiple options.
	err = kb.Load(ctx, WithShowProgress(true), WithProgressStepSize(1))
	if err != nil {
		t.Errorf("Load with multiple options failed: %v", err)
	}
}

func TestBuiltinKnowledge_Load_WithNoSources(t *testing.T) {
	// Create a knowledge instance with no sources.
	kb := New()

	ctx := context.Background()

	// Should not fail when there are no sources.
	err := kb.Load(ctx)
	if err != nil {
		t.Errorf("Load with no sources failed: %v", err)
	}
}
