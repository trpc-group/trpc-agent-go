//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"errors"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// mockProcessor is a test implementation of PreProcessor
type mockProcessor struct {
	name      string
	transform func(string) string
	err       error
}

func (m *mockProcessor) Process(doc *document.Document) (*document.Document, error) {
	if m.err != nil {
		return nil, m.err
	}
	if doc == nil {
		return nil, nil
	}
	return &document.Document{
		ID:       doc.ID,
		Name:     doc.Name,
		Content:  m.transform(doc.Content),
		Metadata: doc.Metadata,
	}, nil
}

func (m *mockProcessor) Name() string {
	return m.name
}

func TestApplyPreProcessors(t *testing.T) {
	t.Run("nil document", func(t *testing.T) {
		result, err := ApplyPreProcessors(nil, &mockProcessor{
			name:      "test",
			transform: func(s string) string { return s },
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != nil {
			t.Errorf("expected nil result for nil input")
		}
	})

	t.Run("no processors", func(t *testing.T) {
		doc := &document.Document{ID: "test", Content: "hello"}
		result, err := ApplyPreProcessors(doc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != doc {
			t.Errorf("expected same document when no processors")
		}
	})

	t.Run("single processor", func(t *testing.T) {
		doc := &document.Document{ID: "test", Content: "hello"}
		result, err := ApplyPreProcessors(doc, &mockProcessor{
			name:      "upper",
			transform: func(s string) string { return s + "_processed" },
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Content != "hello_processed" {
			t.Errorf("expected 'hello_processed', got %q", result.Content)
		}
	})

	t.Run("multiple processors", func(t *testing.T) {
		doc := &document.Document{ID: "test", Content: "hello"}
		result, err := ApplyPreProcessors(doc,
			&mockProcessor{
				name:      "first",
				transform: func(s string) string { return s + "_first" },
			},
			&mockProcessor{
				name:      "second",
				transform: func(s string) string { return s + "_second" },
			},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Content != "hello_first_second" {
			t.Errorf("expected 'hello_first_second', got %q", result.Content)
		}
	})

	t.Run("processor with error", func(t *testing.T) {
		doc := &document.Document{ID: "test", Content: "hello"}
		expectedErr := errors.New("process error")
		_, err := ApplyPreProcessors(doc, &mockProcessor{
			name: "error",
			err:  expectedErr,
		})
		if err != expectedErr {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("nil processor in chain", func(t *testing.T) {
		doc := &document.Document{ID: "test", Content: "hello"}
		result, err := ApplyPreProcessors(doc,
			&mockProcessor{
				name:      "first",
				transform: func(s string) string { return s + "_first" },
			},
			nil,
			&mockProcessor{
				name:      "second",
				transform: func(s string) string { return s + "_second" },
			},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Content != "hello_first_second" {
			t.Errorf("expected 'hello_first_second', got %q", result.Content)
		}
	})
}
