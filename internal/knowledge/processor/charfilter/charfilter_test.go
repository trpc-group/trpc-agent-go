//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package charfilter

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

func TestCharFilter_Process(t *testing.T) {
	tests := []struct {
		name        string
		filter      *CharFilter
		input       string
		expected    string
		description string
	}{
		{
			name:        "remove newlines",
			filter:      New("\n"),
			input:       "hello\nworld\n",
			expected:    "helloworld",
			description: "should remove all newlines",
		},
		{
			name:        "remove tabs and newlines",
			filter:      New("\n", "\t"),
			input:       "hello\tworld\nfoo",
			expected:    "helloworldfoo",
			description: "should remove tabs and newlines",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := &document.Document{ID: "test", Content: tt.input}
			result, err := tt.filter.Process(doc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Content != tt.expected {
				t.Errorf("%s: expected %q, got %q", tt.description, tt.expected, result.Content)
			}
		})
	}
}

func TestCharFilter_NilInput(t *testing.T) {
	filter := New("\n")

	result, err := filter.Process(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for nil input")
	}
}

func TestCharFilter_Chinese(t *testing.T) {
	filter := New("\n", "\t")

	doc := &document.Document{
		ID:      "test",
		Content: "你好\n世界\t测试",
	}

	result, err := filter.Process(doc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "你好世界测试"
	if result.Content != expected {
		t.Errorf("Chinese handling failed: expected %q, got %q", expected, result.Content)
	}
}

func TestCharFilter_PreservesMetadata(t *testing.T) {
	filter := New("\n")

	doc := &document.Document{
		ID:      "test",
		Name:    "test-doc",
		Content: "hello\nworld",
		Metadata: map[string]any{
			"key1": "value1",
			"key2": 123,
		},
	}

	result, err := filter.Process(doc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check metadata is preserved
	if result.ID != doc.ID {
		t.Errorf("ID not preserved")
	}
	if result.Name != doc.Name {
		t.Errorf("Name not preserved")
	}
	if result.Metadata["key1"] != "value1" {
		t.Errorf("metadata key1 not preserved")
	}
	if result.Metadata["key2"] != 123 {
		t.Errorf("metadata key2 not preserved")
	}
}

func TestApply(t *testing.T) {
	t.Run("with chars to remove", func(t *testing.T) {
		doc := &document.Document{ID: "test", Content: "hello\nworld"}
		result, err := Apply(doc, []string{"\n"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Content != "helloworld" {
			t.Errorf("expected 'helloworld', got %q", result.Content)
		}
	})

	t.Run("with empty chars", func(t *testing.T) {
		doc := &document.Document{ID: "test", Content: "hello\nworld"}
		result, err := Apply(doc, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Content != "hello\nworld" {
			t.Errorf("expected original content, got %q", result.Content)
		}
	})

	t.Run("with nil doc", func(t *testing.T) {
		result, err := Apply(nil, []string{"\n"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != nil {
			t.Errorf("expected nil result")
		}
	})
}

func TestNewWithReplacements(t *testing.T) {
	filter := NewWithReplacements(
		[]string{"\t"},
		map[string]string{"\n": " "},
	)

	doc := &document.Document{
		ID:      "test",
		Content: "hello\tworld\nfoo",
	}

	result, err := filter.Process(doc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "helloworld foo"
	if result.Content != expected {
		t.Errorf("expected %q, got %q", expected, result.Content)
	}
}
