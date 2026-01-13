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

func TestCharDedup_Preprocess(t *testing.T) {
	tests := []struct {
		name         string
		charsToDedup []string
		input        []*document.Document
		expected     []*document.Document
	}{
		{
			name:         "Dedup spaces",
			charsToDedup: []string{" "},
			input: []*document.Document{
				{Content: "Hello   World"},
				{Content: "Foo  Bar"},
			},
			expected: []*document.Document{
				{Content: "Hello World"},
				{Content: "Foo Bar"},
			},
		},
		{
			name:         "Dedup multiple chars",
			charsToDedup: []string{"\n", " "},
			input: []*document.Document{
				{Content: "Hello\n\n\nWorld   !"},
			},
			expected: []*document.Document{
				{Content: "Hello\nWorld !"},
			},
		},
		{
			name:         "No consecutive chars",
			charsToDedup: []string{" "},
			input: []*document.Document{
				{Content: "Hello World"},
			},
			expected: []*document.Document{
				{Content: "Hello World"},
			},
		},
		{
			name:         "Empty string param",
			charsToDedup: []string{""}, // Should be ignored
			input: []*document.Document{
				{Content: "Hello   World"},
			},
			expected: []*document.Document{
				{Content: "Hello   World"},
			},
		},
		{
			name:         "Empty input",
			charsToDedup: []string{" "},
			input:        []*document.Document{},
			expected:     []*document.Document{},
		},
		{
			name:         "Nil document",
			charsToDedup: []string{" "},
			input:        []*document.Document{nil},
			expected:     []*document.Document{},
		},
		{
			name:         "Dedup regex special char $2",
			charsToDedup: []string{"$2"},
			input: []*document.Document{
				{Content: "$2$2$2"},
			},
			expected: []*document.Document{
				{Content: "$2"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dedup := transform.NewCharDedup(tt.charsToDedup...)
			result, err := dedup.Preprocess(tt.input)
			assert.NoError(t, err)
			assert.Equal(t, len(tt.expected), len(result))

			for i, doc := range result {
				assert.Equal(t, tt.expected[i].Content, doc.Content)
			}
		})
	}
}

func TestCharDedup_Postprocess(t *testing.T) {
	dedup := transform.NewCharDedup(" ")
	input := []*document.Document{{Content: "abc"}}
	result, err := dedup.Postprocess(input)
	assert.NoError(t, err)
	assert.Equal(t, input, result)
}

func TestCharDedup_Name(t *testing.T) {
	dedup := transform.NewCharDedup(" ")
	assert.Equal(t, "CharDedup", dedup.Name())
}

func TestCharDedup_MetadataPreservation(t *testing.T) {
	input := []*document.Document{
		{
			Content: "test  test",
			Metadata: map[string]any{
				"key": "value",
			},
			CreatedAt: time.Now(),
		},
	}

	dedup := transform.NewCharDedup(" ")
	result, err := dedup.Preprocess(input)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, "test test", result[0].Content)
	assert.Equal(t, "value", result[0].Metadata["key"])

	// Modify result metadata to verify it's a copy
	result[0].Metadata["key"] = "new_value"
	assert.Equal(t, "value", input[0].Metadata["key"])
}
