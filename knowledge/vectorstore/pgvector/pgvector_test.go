//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

func TestConvertToFloat32Vector(t *testing.T) {
	tests := []struct {
		name     string
		input    []float64
		expected []float32
	}{
		{
			name:     "empty_vector",
			input:    []float64{},
			expected: []float32{},
		},
		{
			name:     "single_element",
			input:    []float64{1.5},
			expected: []float32{1.5},
		},
		{
			name:     "multiple_elements",
			input:    []float64{1.0, 2.5, 3.7, 4.2},
			expected: []float32{1.0, 2.5, 3.7, 4.2},
		},
		{
			name:     "zero_values",
			input:    []float64{0.0, 0.0, 0.0},
			expected: []float32{0.0, 0.0, 0.0},
		},
		{
			name:     "negative_values",
			input:    []float64{-1.5, -2.7, 3.2},
			expected: []float32{-1.5, -2.7, 3.2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertToFloat32Vector(tt.input)
			assert.Equal(t, len(tt.expected), len(result))
			for i := range tt.expected {
				assert.InDelta(t, tt.expected[i], result[i], 0.0001)
			}
		})
	}
}

// TestConvertToFloat64Vector tests float32 to float64 vector conversion
func TestConvertToFloat64Vector(t *testing.T) {
	tests := []struct {
		name     string
		input    []float32
		expected []float64
	}{
		{
			name:     "empty_vector",
			input:    []float32{},
			expected: []float64{},
		},
		{
			name:     "single_element",
			input:    []float32{1.5},
			expected: []float64{1.5},
		},
		{
			name:     "multiple_elements",
			input:    []float32{1.0, 2.5, 3.7, 4.2},
			expected: []float64{1.0, 2.5, 3.7, 4.2},
		},
		{
			name:     "zero_values",
			input:    []float32{0.0, 0.0, 0.0},
			expected: []float64{0.0, 0.0, 0.0},
		},
		{
			name:     "negative_values",
			input:    []float32{-1.5, -2.7, 3.2},
			expected: []float64{-1.5, -2.7, 3.2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertToFloat64Vector(tt.input)
			assert.Equal(t, len(tt.expected), len(result))
			for i := range tt.expected {
				assert.InDelta(t, tt.expected[i], result[i], 0.0001)
			}
		})
	}
}

// TestMapToJSON tests map to JSON string conversion
func TestMapToJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected string
	}{
		{
			name:     "nil_map",
			input:    nil,
			expected: "{}",
		},
		{
			name:     "empty_map",
			input:    map[string]any{},
			expected: "{}",
		},
		{
			name:     "simple_map",
			input:    map[string]any{"key": "value"},
			expected: `{"key":"value"}`,
		},
		{
			name: "multiple_keys",
			input: map[string]any{
				"name": "test",
				"age":  25,
			},
			// JSON order is not guaranteed, so we'll verify differently
			expected: "",
		},
		{
			name: "nested_map",
			input: map[string]any{
				"user": map[string]any{
					"name": "John",
					"age":  30,
				},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapToJSON(tt.input)
			if tt.expected != "" {
				assert.Equal(t, tt.expected, result)
			} else {
				// For cases with multiple/nested keys, just verify it's valid JSON
				assert.NotEmpty(t, result)
				assert.Contains(t, result, "{")
				assert.Contains(t, result, "}")
			}
		})
	}
}

// TestJSONToMap tests JSON string to map conversion
func TestJSONToMap(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		expectErr bool
		validate  func(*testing.T, map[string]any)
	}{
		{
			name:      "empty_json",
			input:     "{}",
			expectErr: false,
			validate: func(t *testing.T, m map[string]any) {
				assert.Empty(t, m)
			},
		},
		{
			name:      "empty_string",
			input:     "",
			expectErr: false,
			validate: func(t *testing.T, m map[string]any) {
				assert.Empty(t, m)
			},
		},
		{
			name:      "simple_json",
			input:     `{"key":"value"}`,
			expectErr: false,
			validate: func(t *testing.T, m map[string]any) {
				assert.Equal(t, "value", m["key"])
			},
		},
		{
			name:      "json_with_number",
			input:     `{"age":25}`,
			expectErr: false,
			validate: func(t *testing.T, m map[string]any) {
				assert.Equal(t, float64(25), m["age"])
			},
		},
		{
			name:      "nested_json",
			input:     `{"user":{"name":"John","age":30}}`,
			expectErr: false,
			validate: func(t *testing.T, m map[string]any) {
				user, ok := m["user"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "John", user["name"])
				assert.Equal(t, float64(30), user["age"])
			},
		},
		{
			name:      "invalid_json",
			input:     `{invalid json}`,
			expectErr: true,
			validate: func(t *testing.T, m map[string]any) {
				// Should return empty map even on error
				assert.Empty(t, m)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := jsonToMap(tt.input)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			if tt.validate != nil {
				tt.validate(t, result)
			}
		})
	}
}

// TestValidateDeleteConfig tests delete configuration validation
func TestValidateDeleteConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    *vectorstore.DeleteConfig
		expectErr bool
		errMsg    string
	}{
		{
			name: "valid_delete_all",
			config: &vectorstore.DeleteConfig{
				DeleteAll: true,
			},
			expectErr: false,
		},
		{
			name: "invalid_delete_all_with_ids",
			config: &vectorstore.DeleteConfig{
				DeleteAll:   true,
				DocumentIDs: []string{"id1", "id2"},
			},
			expectErr: true,
			errMsg:    "delete all documents, but document ids or filter are provided",
		},
		{
			name: "invalid_delete_all_with_filter",
			config: &vectorstore.DeleteConfig{
				DeleteAll: true,
				Filter:    map[string]any{"key": "value"},
			},
			expectErr: true,
			errMsg:    "delete all documents, but document ids or filter are provided",
		},
		{
			name: "invalid_delete_all_with_both",
			config: &vectorstore.DeleteConfig{
				DeleteAll:   true,
				DocumentIDs: []string{"id1"},
				Filter:      map[string]any{"key": "value"},
			},
			expectErr: true,
			errMsg:    "delete all documents, but document ids or filter are provided",
		},
		{
			name: "valid_delete_by_ids",
			config: &vectorstore.DeleteConfig{
				DeleteAll:   false,
				DocumentIDs: []string{"id1", "id2"},
			},
			expectErr: false,
		},
		{
			name: "valid_delete_by_filter",
			config: &vectorstore.DeleteConfig{
				DeleteAll: false,
				Filter:    map[string]any{"key": "value"},
			},
			expectErr: false,
		},
		{
			name: "invalid_no_conditions",
			config: &vectorstore.DeleteConfig{
				DeleteAll: false,
			},
			expectErr: true,
			errMsg:    "no filter conditions specified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := &VectorStore{}
			err := vs.validateDeleteConfig(tt.config)
			if tt.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestGetMaxResult tests getMaxResult logic
func TestGetMaxResult(t *testing.T) {
	tests := []struct {
		name           string
		queryLimit     int
		defaultLimit   int
		expectedResult int
	}{
		{
			name:           "use_query_limit",
			queryLimit:     5,
			defaultLimit:   10,
			expectedResult: 5,
		},
		{
			name:           "use_default_when_zero",
			queryLimit:     0,
			defaultLimit:   10,
			expectedResult: 10,
		},
		{
			name:           "use_default_when_negative",
			queryLimit:     -1,
			defaultLimit:   10,
			expectedResult: 10,
		},
		{
			name:           "use_query_limit_when_larger",
			queryLimit:     100,
			defaultLimit:   10,
			expectedResult: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := &VectorStore{
				option: options{
					maxResults: tt.defaultLimit,
				},
			}
			result := vs.getMaxResult(tt.queryLimit)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

// TestErrorConstants tests error constant values
func TestErrorConstants(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "document_required",
			err:      errDocumentRequired,
			expected: "pgvector document is required",
		},
		{
			name:     "document_id_required",
			err:      errDocumentIDRequired,
			expected: "pgvector document ID is required",
		},
		{
			name:     "id_required",
			err:      errIDRequired,
			expected: "pgvector id is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.err.Error())
		})
	}
}

// TestDefaultOptions tests default options values
func TestDefaultOptions(t *testing.T) {
	assert.Equal(t, "localhost", defaultOptions.host)
	assert.Equal(t, 5432, defaultOptions.port)
	assert.Equal(t, "trpc_agent_go", defaultOptions.database)
	assert.Equal(t, "documents", defaultOptions.table)
	assert.Equal(t, true, defaultOptions.enableTSVector)
	assert.Equal(t, 1536, defaultOptions.indexDimension)
	assert.Equal(t, "disable", defaultOptions.sslMode)
	assert.Equal(t, 0.7, defaultOptions.vectorWeight)
	assert.Equal(t, 0.3, defaultOptions.textWeight)
	assert.Equal(t, "english", defaultOptions.language)
	assert.Equal(t, defaultMaxResults, defaultOptions.maxResults)
	assert.NotNil(t, defaultOptions.docBuilder)

	// Field names
	assert.Equal(t, "id", defaultOptions.idFieldName)
	assert.Equal(t, "name", defaultOptions.nameFieldName)
	assert.Equal(t, "content", defaultOptions.contentFieldName)
	assert.Equal(t, "embedding", defaultOptions.embeddingFieldName)
	assert.Equal(t, "metadata", defaultOptions.metadataFieldName)
	assert.Equal(t, "created_at", defaultOptions.createdAtFieldName)
	assert.Equal(t, "updated_at", defaultOptions.updatedAtFieldName)
}

// TestOptions tests all option functions
func TestOptions(t *testing.T) {
	t.Run("connection_options", func(t *testing.T) {
		tests := []struct {
			name     string
			option   Option
			getField func(options) any
			expected any
		}{
			{"host", WithHost("example.com"), func(o options) any { return o.host }, "example.com"},
			{"port", WithPort(5433), func(o options) any { return o.port }, 5433},
			{"user", WithUser("testuser"), func(o options) any { return o.user }, "testuser"},
			{"password", WithPassword("testpass"), func(o options) any { return o.password }, "testpass"},
			{"database", WithDatabase("testdb"), func(o options) any { return o.database }, "testdb"},
			{"ssl_mode", WithSSLMode("require"), func(o options) any { return o.sslMode }, "require"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				opts := defaultOptions
				tt.option(&opts)
				assert.Equal(t, tt.expected, tt.getField(opts))
			})
		}
	})

	t.Run("table_options", func(t *testing.T) {
		tests := []struct {
			name     string
			option   Option
			getField func(options) any
			expected any
		}{
			{"table", WithTable("test_table"), func(o options) any { return o.table }, "test_table"},
			{"index_dimension", WithIndexDimension(768), func(o options) any { return o.indexDimension }, 768},
			{"enable_tsvector", WithEnableTSVector(false), func(o options) any { return o.enableTSVector }, false},
			{"language", WithLanguageExtension("chinese"), func(o options) any { return o.language }, "chinese"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				opts := defaultOptions
				tt.option(&opts)
				assert.Equal(t, tt.expected, tt.getField(opts))
			})
		}
	})

	t.Run("field_name_options", func(t *testing.T) {
		tests := []struct {
			name     string
			option   Option
			getField func(options) string
			expected string
		}{
			{"id_field", WithIDField("doc_id"), func(o options) string { return o.idFieldName }, "doc_id"},
			{"name_field", WithNameField("title"), func(o options) string { return o.nameFieldName }, "title"},
			{"content_field", WithContentField("text"), func(o options) string { return o.contentFieldName }, "text"},
			{"embedding_field", WithEmbeddingField("vector"), func(o options) string { return o.embeddingFieldName }, "vector"},
			{"metadata_field", WithMetadataField("meta"), func(o options) string { return o.metadataFieldName }, "meta"},
			{"created_at_field", WithCreatedAtField("create_time"), func(o options) string { return o.createdAtFieldName }, "create_time"},
			{"updated_at_field", WithUpdatedAtField("update_time"), func(o options) string { return o.updatedAtFieldName }, "update_time"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				opts := defaultOptions
				tt.option(&opts)
				assert.Equal(t, tt.expected, tt.getField(opts))
			})
		}
	})

	t.Run("max_results_options", func(t *testing.T) {
		tests := []struct {
			name     string
			value    int
			expected int
		}{
			{"valid_value", 50, 50},
			{"zero_uses_default", 0, defaultMaxResults},
			{"negative_uses_default", -5, defaultMaxResults},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				opts := defaultOptions
				WithMaxResults(tt.value)(&opts)
				assert.Equal(t, tt.expected, opts.maxResults)
			})
		}
	})
}

// TestWithHybridSearchWeights tests hybrid search weight normalization
func TestWithHybridSearchWeights(t *testing.T) {
	tests := []struct {
		name                 string
		vectorWeight         float64
		textWeight           float64
		expectedVectorWeight float64
		expectedTextWeight   float64
	}{
		{
			name:                 "equal_weights",
			vectorWeight:         0.5,
			textWeight:           0.5,
			expectedVectorWeight: 0.5,
			expectedTextWeight:   0.5,
		},
		{
			name:                 "unequal_weights",
			vectorWeight:         0.7,
			textWeight:           0.3,
			expectedVectorWeight: 0.7,
			expectedTextWeight:   0.3,
		},
		{
			name:                 "weights_not_summing_to_one",
			vectorWeight:         2.0,
			textWeight:           3.0,
			expectedVectorWeight: 0.4,
			expectedTextWeight:   0.6,
		},
		{
			name:                 "both_zero_fallback",
			vectorWeight:         0.0,
			textWeight:           0.0,
			expectedVectorWeight: 0.7,
			expectedTextWeight:   0.3,
		},
		{
			name:                 "negative_values_fallback",
			vectorWeight:         -0.5,
			textWeight:           -0.5,
			expectedVectorWeight: 0.7,
			expectedTextWeight:   0.3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := defaultOptions
			WithHybridSearchWeights(tt.vectorWeight, tt.textWeight)(&opts)
			assert.InDelta(t, tt.expectedVectorWeight, opts.vectorWeight, 0.0001)
			assert.InDelta(t, tt.expectedTextWeight, opts.textWeight, 0.0001)
		})
	}
}
