//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package source

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// mockSource implements the Source interface for testing
type mockSource struct {
	name     string
	srcType  string
	metadata map[string]any
}

func (m *mockSource) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	return []*document.Document{}, nil
}

func (m *mockSource) Name() string {
	return m.name
}

func (m *mockSource) Type() string {
	return m.srcType
}

func (m *mockSource) GetMetadata() map[string]any {
	return m.metadata
}

func TestGetAllMetadata(t *testing.T) {
	tests := []struct {
		name     string
		sources  []Source
		expected map[string][]any
	}{
		{
			name:     "empty sources",
			sources:  []Source{},
			expected: map[string][]any{},
		},
		{
			name: "single source",
			sources: []Source{
				&mockSource{
					name:    "test1",
					srcType: "file",
					metadata: map[string]any{
						"category": "docs",
						"version":  "1.0",
					},
				},
			},
			expected: map[string][]any{
				"metadata.category": {"docs"},
				"metadata.version":  {"1.0"},
			},
		},
		{
			name: "multiple sources with different metadata",
			sources: []Source{
				&mockSource{
					name:    "test1",
					srcType: "file",
					metadata: map[string]any{
						"category": "docs",
						"version":  "1.0",
					},
				},
				&mockSource{
					name:    "test2",
					srcType: "url",
					metadata: map[string]any{
						"category": "api",
						"version":  "2.0",
					},
				},
			},
			expected: map[string][]any{
				"metadata.category": {"docs", "api"},
				"metadata.version":  {"1.0", "2.0"},
			},
		},
		{
			name: "multiple sources with duplicate values",
			sources: []Source{
				&mockSource{
					name:    "test1",
					srcType: "file",
					metadata: map[string]any{
						"category": "docs",
						"version":  "1.0",
					},
				},
				&mockSource{
					name:    "test2",
					srcType: "file",
					metadata: map[string]any{
						"category": "docs", // duplicate value
						"version":  "2.0",
					},
				},
			},
			expected: map[string][]any{
				"metadata.category": {"docs"}, // should be deduplicated
				"metadata.version":  {"1.0", "2.0"},
			},
		},
		{
			name: "sources with different types but same values",
			sources: []Source{
				&mockSource{
					name:    "test1",
					srcType: "file",
					metadata: map[string]any{
						"count": 5,
					},
				},
				&mockSource{
					name:    "test2",
					srcType: "url",
					metadata: map[string]any{
						"count": "5", // different type, should not be deduplicated
					},
				},
			},
			expected: map[string][]any{
				"metadata.count": {5, "5"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetAllMetadata(tt.sources)

			// Check that all expected keys are present
			require.Equal(t, len(tt.expected), len(result), "number of keys should match")

			for key, expectedValues := range tt.expected {
				actualValues, exists := result[key]
				require.True(t, exists, "key %s should exist", key)
				require.Equal(t, len(expectedValues), len(actualValues), "number of values for key %s should match", key)

				// Check that all expected values are present (order might be different)
				for _, expectedValue := range expectedValues {
					found := false
					for _, actualValue := range actualValues {
						if fmt.Sprintf("%v", expectedValue) == fmt.Sprintf("%v", actualValue) {
							found = true
							break
						}
					}
					require.True(t, found, "value %v should be present for key %s", expectedValue, key)
				}
			}
		})
	}
}

func TestGetAllMetadataWithoutValues(t *testing.T) {
	tests := []struct {
		name     string
		sources  []Source
		expected map[string][]any
	}{
		{
			name:     "empty sources",
			sources:  []Source{},
			expected: map[string][]any{},
		},
		{
			name: "single source",
			sources: []Source{
				&mockSource{
					name:    "test1",
					srcType: "file",
					metadata: map[string]any{
						"category": "docs",
						"version":  "1.0",
					},
				},
			},
			expected: map[string][]any{
				"metadata.category": {},
				"metadata.version":  {},
			},
		},
		{
			name: "multiple sources",
			sources: []Source{
				&mockSource{
					name:    "test1",
					srcType: "file",
					metadata: map[string]any{
						"category": "docs",
						"version":  "1.0",
					},
				},
				&mockSource{
					name:    "test2",
					srcType: "url",
					metadata: map[string]any{
						"category": "api",
						"author":   "john",
					},
				},
			},
			expected: map[string][]any{
				"metadata.category": {},
				"metadata.version":  {},
				"metadata.author":   {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetAllMetadataWithoutValues(tt.sources)

			require.Equal(t, len(tt.expected), len(result), "number of keys should match")

			for key := range tt.expected {
				actualValues, exists := result[key]
				require.True(t, exists, "key %s should exist", key)
				require.Empty(t, actualValues, "values should be empty for key %s", key)
			}
		})
	}
}

func TestGetAllMetadataKeys(t *testing.T) {
	tests := []struct {
		name     string
		sources  []Source
		expected []string
	}{
		{
			name:     "empty sources",
			sources:  []Source{},
			expected: []string{},
		},
		{
			name: "single source",
			sources: []Source{
				&mockSource{
					name:    "test1",
					srcType: "file",
					metadata: map[string]any{
						"category": "docs",
						"version":  "1.0",
					},
				},
			},
			expected: []string{"metadata.category", "metadata.version"},
		},
		{
			name: "multiple sources with overlapping keys",
			sources: []Source{
				&mockSource{
					name:    "test1",
					srcType: "file",
					metadata: map[string]any{
						"category": "docs",
						"version":  "1.0",
					},
				},
				&mockSource{
					name:    "test2",
					srcType: "url",
					metadata: map[string]any{
						"category": "api",
						"author":   "john",
					},
				},
			},
			expected: []string{"metadata.category", "metadata.version", "metadata.author"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetAllMetadataKeys(tt.sources)

			require.Equal(t, len(tt.expected), len(result), "number of keys should match")

			// Convert to map for easier comparison (order doesn't matter)
			resultMap := make(map[string]bool)
			for _, key := range result {
				resultMap[key] = true
			}

			for _, expectedKey := range tt.expected {
				require.True(t, resultMap[expectedKey], "key %s should be present", expectedKey)
			}
		})
	}
}

func TestConstants(t *testing.T) {
	// Test source types
	require.Equal(t, "auto", TypeAuto)
	require.Equal(t, "file", TypeFile)
	require.Equal(t, "dir", TypeDir)
	require.Equal(t, "url", TypeURL)

	// Test metadata keys have correct prefix
	expectedPrefix := "trpc_agent_go"
	require.Contains(t, MetaSource, expectedPrefix)
	require.Contains(t, MetaFilePath, expectedPrefix)
	require.Contains(t, MetaFileName, expectedPrefix)
	require.Contains(t, MetaFileExt, expectedPrefix)
	require.Contains(t, MetaFileSize, expectedPrefix)
	require.Contains(t, MetaFileMode, expectedPrefix)
	require.Contains(t, MetaModifiedAt, expectedPrefix)
	require.Contains(t, MetaContentLength, expectedPrefix)
	require.Contains(t, MetaFileCount, expectedPrefix)
	require.Contains(t, MetaFilePaths, expectedPrefix)
	require.Contains(t, MetaURL, expectedPrefix)
	require.Contains(t, MetaURLHost, expectedPrefix)
	require.Contains(t, MetaURLPath, expectedPrefix)
	require.Contains(t, MetaURLScheme, expectedPrefix)
	require.Contains(t, MetaInputCount, expectedPrefix)
	require.Contains(t, MetaInputs, expectedPrefix)
}
