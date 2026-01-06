//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package chardedup

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

func TestCharDedup_Process(t *testing.T) {
	tests := []struct {
		name         string
		charsToDedup []string
		input        string
		expected     string
	}{
		{
			name:         "dedup tabs",
			charsToDedup: []string{"\t"},
			input:        "hello\t\t\t\tworld",
			expected:     "hello\tworld",
		},
		{
			name:         "dedup spaces",
			charsToDedup: []string{" "},
			input:        "hello    world",
			expected:     "hello world",
		},
		{
			name:         "dedup newlines",
			charsToDedup: []string{"\n"},
			input:        "hello\n\n\n\nworld",
			expected:     "hello\nworld",
		},
		{
			name:         "dedup multiple characters",
			charsToDedup: []string{"\t", " ", "\n"},
			input:        "hello\t\t\tworld   foo\n\n\nbar",
			expected:     "hello\tworld foo\nbar",
		},
		{
			name:         "no consecutive chars",
			charsToDedup: []string{"\t"},
			input:        "hello\tworld",
			expected:     "hello\tworld",
		},
		{
			name:         "empty input",
			charsToDedup: []string{"\t"},
			input:        "",
			expected:     "",
		},
		{
			name:         "no chars to dedup",
			charsToDedup: []string{},
			input:        "hello\t\t\tworld",
			expected:     "hello\t\t\tworld",
		},
		{
			name:         "dedup double newlines",
			charsToDedup: []string{"\n\n"},
			input:        "hello\n\n\n\n\n\nworld",
			expected:     "hello\n\nworld",
		},
		{
			name:         "special regex chars",
			charsToDedup: []string{"."},
			input:        "hello....world",
			expected:     "hello.world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dedup := New(tt.charsToDedup...)
			doc := &document.Document{
				Content: tt.input,
			}

			result, err := dedup.Process(doc)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result.Content)
		})
	}
}

func TestCharDedup_ProcessNilDoc(t *testing.T) {
	dedup := New("\t")
	result, err := dedup.Process(nil)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestCharDedup_Name(t *testing.T) {
	dedup := New("\t")
	assert.Equal(t, "CharDedup", dedup.Name())
}

func TestCharDedup_PreservesMetadata(t *testing.T) {
	dedup := New("\t")
	doc := &document.Document{
		ID:      "test-id",
		Name:    "test-name",
		Content: "hello\t\t\tworld",
		Metadata: map[string]any{
			"key1": "value1",
			"key2": 123,
		},
	}

	result, err := dedup.Process(doc)
	require.NoError(t, err)
	assert.Equal(t, "test-id", result.ID)
	assert.Equal(t, "test-name", result.Name)
	assert.Equal(t, "hello\tworld", result.Content)
	assert.Equal(t, "value1", result.Metadata["key1"])
	assert.Equal(t, 123, result.Metadata["key2"])
}
