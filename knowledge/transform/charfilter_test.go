//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//
//

package transform_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

func TestCharFilter_Preprocess(t *testing.T) {
	tests := []struct {
		name          string
		charsToRemove []string
		input         []*document.Document
		expected      []*document.Document
	}{
		{
			name:          "Remove newlines",
			charsToRemove: []string{"\n"},
			input: []*document.Document{
				{Content: "Hello\nWorld"},
				{Content: "Foo\nBar\nBaz"},
			},
			expected: []*document.Document{
				{Content: "HelloWorld"},
				{Content: "FooBarBaz"},
			},
		},
		{
			name:          "Remove multiple chars",
			charsToRemove: []string{"\n", "\t"},
			input: []*document.Document{
				{Content: "Hello\n\tWorld"},
			},
			expected: []*document.Document{
				{Content: "HelloWorld"},
			},
		},
		{
			name:          "No match",
			charsToRemove: []string{"x"},
			input: []*document.Document{
				{Content: "Hello World"},
			},
			expected: []*document.Document{
				{Content: "Hello World"},
			},
		},
		{
			name:          "Empty chars to remove should be ignored",
			charsToRemove: []string{"\n", "", "\t"},
			input: []*document.Document{
				{Content: "Hello\n\tWorld"},
			},
			expected: []*document.Document{
				{Content: "HelloWorld"},
			},
		},
		{
			name:          "Empty input",
			charsToRemove: []string{"\n"},
			input:         []*document.Document{},
			expected:      []*document.Document{},
		},
		{
			name:          "Nil document",
			charsToRemove: []string{"\n"},
			input:         []*document.Document{nil},
			expected:      []*document.Document{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := transform.NewCharFilter(tt.charsToRemove...)
			result, err := filter.Preprocess(tt.input)
			assert.NoError(t, err)
			assert.Equal(t, len(tt.expected), len(result))

			for i, doc := range result {
				assert.Equal(t, tt.expected[i].Content, doc.Content)
				// Check that metadata is preserved (using nil input for metadata in this simple test)
				// Real check would ensure metadata is copied
				if tt.input[i] != nil {
					assert.Equal(t, tt.input[i].ID, doc.ID)
				}
			}
		})
	}
}

func TestCharFilter_Postprocess(t *testing.T) {
	filter := transform.NewCharFilter("a")
	input := []*document.Document{{Content: "abc"}}
	result, err := filter.Postprocess(input)
	assert.NoError(t, err)
	assert.Equal(t, input, result)
}

func TestCharFilter_Name(t *testing.T) {
	filter := transform.NewCharFilter("a")
	assert.Equal(t, "CharFilter", filter.Name())
}

func TestCharFilter_MetadataPreservation(t *testing.T) {
	input := []*document.Document{
		{
			Content: "test",
			Metadata: map[string]any{
				"key": "value",
			},
			CreatedAt: time.Now(),
		},
	}

	filter := transform.NewCharFilter("t")
	result, err := filter.Preprocess(input)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, "es", result[0].Content)
	assert.Equal(t, "value", result[0].Metadata["key"])

	// Modify result metadata to verify it's a copy
	result[0].Metadata["key"] = "new_value"
	assert.Equal(t, "value", input[0].Metadata["key"])
}
