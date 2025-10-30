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
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

// TestNew tests the New function for creating a VectorStore
func TestNew(t *testing.T) {
	t.Run("new_with_invalid_instance_name", func(t *testing.T) {
		_, err := New(WithPostgresInstance("non-existent-instance"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "instance")
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("new_with_instance_name_builder_error", func(t *testing.T) {
		// Save old builder
		oldBuilder := postgres.GetClientBuilder()
		defer func() { postgres.SetClientBuilder(oldBuilder) }()

		// Register a test instance
		postgres.RegisterPostgresInstance("test-instance",
			postgres.WithClientConnString("postgres://test:test@localhost:5432/test"))

		// Set up a mock builder that returns an error
		postgres.SetClientBuilder(func(ctx context.Context, opts ...postgres.ClientBuilderOpt) (postgres.Client, error) {
			return nil, errors.New("builder error from instance")
		})

		_, err := New(WithPostgresInstance("test-instance"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "create postgres client from instance name failed")
		assert.Contains(t, err.Error(), "builder error from instance")
	})

	t.Run("new_with_direct_connection_params", func(t *testing.T) {
		// Save old builder
		oldBuilder := postgres.GetClientBuilder()
		defer func() { postgres.SetClientBuilder(oldBuilder) }()

		// Set up a mock builder that returns an error on connection
		postgres.SetClientBuilder(func(ctx context.Context, opts ...postgres.ClientBuilderOpt) (postgres.Client, error) {
			return nil, errors.New("connection failed")
		})

		_, err := New(
			WithHost("localhost"),
			WithPort(5432),
			WithUser("test"),
			WithPassword("test"),
			WithDatabase("test"),
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "connection failed")
	})

	t.Run("new_with_init_db_extension_error", func(t *testing.T) {
		// Save old builder
		oldBuilder := postgres.GetClientBuilder()
		defer func() { postgres.SetClientBuilder(oldBuilder) }()

		// Create a mock client that will fail on ExecContext (during initDB)
		tc := newTestClient(t)
		defer tc.Close()

		tc.mock.ExpectExec("CREATE EXTENSION").
			WillReturnError(errors.New("extension creation failed"))

		postgres.SetClientBuilder(func(ctx context.Context, opts ...postgres.ClientBuilderOpt) (postgres.Client, error) {
			return tc.client, nil
		})

		_, err := New(
			WithHost("localhost"),
			WithPort(5432),
			WithUser("test"),
			WithPassword("test"),
			WithDatabase("test"),
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "extension creation failed")
	})

	t.Run("new_with_init_db_table_error", func(t *testing.T) {
		oldBuilder := postgres.GetClientBuilder()
		defer func() { postgres.SetClientBuilder(oldBuilder) }()

		tc := newTestClient(t)
		defer tc.Close()

		tc.mock.ExpectExec("CREATE EXTENSION").
			WillReturnResult(sqlmock.NewResult(0, 0))
		tc.mock.ExpectExec("CREATE TABLE").
			WillReturnError(errors.New("table creation failed"))

		postgres.SetClientBuilder(func(ctx context.Context, opts ...postgres.ClientBuilderOpt) (postgres.Client, error) {
			return tc.client, nil
		})

		_, err := New(
			WithHost("localhost"),
			WithPort(5432),
			WithUser("test"),
			WithPassword("test"),
			WithDatabase("test"),
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "table creation failed")
	})

	t.Run("new_with_init_db_vector_index_error", func(t *testing.T) {
		oldBuilder := postgres.GetClientBuilder()
		defer func() { postgres.SetClientBuilder(oldBuilder) }()

		tc := newTestClient(t)
		defer tc.Close()

		tc.mock.ExpectExec("CREATE EXTENSION").
			WillReturnResult(sqlmock.NewResult(0, 0))
		tc.mock.ExpectExec("CREATE TABLE").
			WillReturnResult(sqlmock.NewResult(0, 0))
		tc.mock.ExpectExec("CREATE INDEX IF NOT EXISTS (.+)_embedding_idx").
			WillReturnError(errors.New("vector index creation failed"))

		postgres.SetClientBuilder(func(ctx context.Context, opts ...postgres.ClientBuilderOpt) (postgres.Client, error) {
			return tc.client, nil
		})

		_, err := New(
			WithHost("localhost"),
			WithPort(5432),
			WithUser("test"),
			WithPassword("test"),
			WithDatabase("test"),
			WithEnableTSVector(false),
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "vector index creation failed")
	})

	t.Run("new_with_init_db_text_index_error", func(t *testing.T) {
		oldBuilder := postgres.GetClientBuilder()
		defer func() { postgres.SetClientBuilder(oldBuilder) }()

		tc := newTestClient(t)
		defer tc.Close()

		tc.mock.ExpectExec("CREATE EXTENSION").
			WillReturnResult(sqlmock.NewResult(0, 0))
		tc.mock.ExpectExec("CREATE TABLE").
			WillReturnResult(sqlmock.NewResult(0, 0))
		tc.mock.ExpectExec("CREATE INDEX IF NOT EXISTS (.+)_embedding_idx").
			WillReturnResult(sqlmock.NewResult(0, 0))
		tc.mock.ExpectExec("CREATE INDEX IF NOT EXISTS (.+)_content_fts_idx").
			WillReturnError(errors.New("text index creation failed"))

		postgres.SetClientBuilder(func(ctx context.Context, opts ...postgres.ClientBuilderOpt) (postgres.Client, error) {
			return tc.client, nil
		})

		_, err := New(
			WithHost("localhost"),
			WithPort(5432),
			WithUser("test"),
			WithPassword("test"),
			WithDatabase("test"),
			WithEnableTSVector(true),
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "text index creation failed")
	})

	t.Run("new_success_without_tsvector", func(t *testing.T) {
		oldBuilder := postgres.GetClientBuilder()
		defer func() { postgres.SetClientBuilder(oldBuilder) }()

		tc := newTestClient(t)
		defer tc.Close()

		tc.mock.ExpectExec("CREATE EXTENSION").
			WillReturnResult(sqlmock.NewResult(0, 0))
		tc.mock.ExpectExec("CREATE TABLE").
			WillReturnResult(sqlmock.NewResult(0, 0))
		tc.mock.ExpectExec("CREATE INDEX IF NOT EXISTS (.+)_embedding_idx").
			WillReturnResult(sqlmock.NewResult(0, 0))

		postgres.SetClientBuilder(func(ctx context.Context, opts ...postgres.ClientBuilderOpt) (postgres.Client, error) {
			return tc.client, nil
		})

		vs, err := New(
			WithHost("localhost"),
			WithPort(5432),
			WithUser("test"),
			WithPassword("test"),
			WithDatabase("test"),
			WithEnableTSVector(false),
		)
		require.NoError(t, err)
		require.NotNil(t, vs)
		assert.False(t, vs.option.enableTSVector)
	})

	t.Run("new_success_with_tsvector", func(t *testing.T) {
		oldBuilder := postgres.GetClientBuilder()
		defer func() { postgres.SetClientBuilder(oldBuilder) }()

		tc := newTestClient(t)
		defer tc.Close()

		tc.mock.ExpectExec("CREATE EXTENSION").
			WillReturnResult(sqlmock.NewResult(0, 0))
		tc.mock.ExpectExec("CREATE TABLE").
			WillReturnResult(sqlmock.NewResult(0, 0))
		tc.mock.ExpectExec("CREATE INDEX IF NOT EXISTS (.+)_embedding_idx").
			WillReturnResult(sqlmock.NewResult(0, 0))
		tc.mock.ExpectExec("CREATE INDEX IF NOT EXISTS (.+)_content_fts_idx").
			WillReturnResult(sqlmock.NewResult(0, 0))

		postgres.SetClientBuilder(func(ctx context.Context, opts ...postgres.ClientBuilderOpt) (postgres.Client, error) {
			return tc.client, nil
		})

		vs, err := New(
			WithHost("localhost"),
			WithPort(5432),
			WithUser("test"),
			WithPassword("test"),
			WithDatabase("test"),
			WithEnableTSVector(true),
		)
		require.NoError(t, err)
		require.NotNil(t, vs)
		assert.True(t, vs.option.enableTSVector)
	})

	t.Run("new_with_extra_options", func(t *testing.T) {
		oldBuilder := postgres.GetClientBuilder()
		defer func() { postgres.SetClientBuilder(oldBuilder) }()

		tc := newTestClient(t)
		defer tc.Close()

		var receivedOpts *postgres.ClientBuilderOpts

		// Set up a builder that captures the options
		postgres.SetClientBuilder(func(ctx context.Context, opts ...postgres.ClientBuilderOpt) (postgres.Client, error) {
			builderOpts := &postgres.ClientBuilderOpts{}
			for _, opt := range opts {
				opt(builderOpts)
			}
			receivedOpts = builderOpts
			return tc.client, nil
		})

		tc.mock.ExpectExec("CREATE EXTENSION").
			WillReturnResult(sqlmock.NewResult(0, 0))
		tc.mock.ExpectExec("CREATE TABLE").
			WillReturnResult(sqlmock.NewResult(0, 0))
		tc.mock.ExpectExec("CREATE INDEX IF NOT EXISTS (.+)_embedding_idx").
			WillReturnResult(sqlmock.NewResult(0, 0))

		extraOpt1 := "option1"
		extraOpt2 := 42

		vs, err := New(
			WithHost("localhost"),
			WithPort(5432),
			WithUser("test"),
			WithPassword("test"),
			WithDatabase("test"),
			WithEnableTSVector(false),
			WithExtraOptions(extraOpt1, extraOpt2),
		)
		require.NoError(t, err)
		require.NotNil(t, vs)

		// Verify that extra options were passed to the builder
		require.NotNil(t, receivedOpts)
		require.Len(t, receivedOpts.ExtraOptions, 2)
		assert.Equal(t, extraOpt1, receivedOpts.ExtraOptions[0])
		assert.Equal(t, extraOpt2, receivedOpts.ExtraOptions[1])
	})
}

// TestVectorStore_Close tests the Close method
func TestVectorStore_Close(t *testing.T) {
	t.Run("close_with_nil_client", func(t *testing.T) {
		vs := &VectorStore{
			client: nil,
		}
		err := vs.Close()
		require.NoError(t, err)
	})

	t.Run("close_success", func(t *testing.T) {
		tc := newTestClient(t)
		vs := &VectorStore{
			client: tc.client,
		}

		// Expect the close call
		tc.mock.ExpectClose()

		// Close the vector store
		err := vs.Close()
		require.NoError(t, err)

		// Verify expectations were met
		tc.AssertExpectations(t)
	})
}

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
		validate func(*testing.T, string)
	}{
		{
			name:  "nil_map",
			input: nil,
			validate: func(t *testing.T, result string) {
				assert.Equal(t, "{}", result)
			},
		},
		{
			name:  "empty_map",
			input: map[string]any{},
			validate: func(t *testing.T, result string) {
				assert.Equal(t, "{}", result)
			},
		},
		{
			name:  "simple_map",
			input: map[string]any{"key": "value"},
			validate: func(t *testing.T, result string) {
				assert.Equal(t, `{"key":"value"}`, result)
			},
		},
		{
			name: "multiple_keys",
			input: map[string]any{
				"name": "test",
				"age":  25,
			},
			validate: func(t *testing.T, result string) {
				// Verify by unmarshalling and checking values
				m, err := jsonToMap(result)
				require.NoError(t, err)
				assert.NotEmpty(t, m)

				// Alternative: just verify valid JSON structure
				assert.Contains(t, result, "name")
				assert.Contains(t, result, "test")
				assert.Contains(t, result, "age")
			},
		},
		{
			name: "nested_map",
			input: map[string]any{
				"user": map[string]any{
					"name": "John",
					"age":  30,
				},
			},
			validate: func(t *testing.T, result string) {
				// Verify it's valid JSON and contains expected values
				m, err := jsonToMap(result)
				require.NoError(t, err)
				assert.NotEmpty(t, m)

				assert.Contains(t, result, "user")
				assert.Contains(t, result, "name")
				assert.Contains(t, result, "John")
			},
		},
		{
			name: "map_with_boolean",
			input: map[string]any{
				"active": true,
				"admin":  false,
			},
			validate: func(t *testing.T, result string) {
				assert.Contains(t, result, "active")
				assert.Contains(t, result, "true")
				assert.Contains(t, result, "admin")
				assert.Contains(t, result, "false")
			},
		},
		{
			name: "map_with_null",
			input: map[string]any{
				"value": nil,
			},
			validate: func(t *testing.T, result string) {
				assert.Contains(t, result, "value")
				assert.Contains(t, result, "null")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapToJSON(tt.input)
			tt.validate(t, result)
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

	t.Run("extra_options", func(t *testing.T) {
		opts := defaultOptions
		extraOpt1 := "option1"
		extraOpt2 := 42
		WithExtraOptions(extraOpt1, extraOpt2)(&opts)
		require.Len(t, opts.extraOptions, 2)
		assert.Equal(t, extraOpt1, opts.extraOptions[0])
		assert.Equal(t, extraOpt2, opts.extraOptions[1])
	})

	t.Run("postgres_instance_option", func(t *testing.T) {
		opts := defaultOptions
		WithPostgresInstance("test-instance")(&opts)
		assert.Equal(t, "test-instance", opts.instanceName)
	})
}

// TestBuildQueryFilter tests the buildQueryFilter method
func TestBuildQueryFilter(t *testing.T) {
	vs := &VectorStore{
		filterConverter: &pgVectorConverter{},
	}

	t.Run("nil_filter", func(t *testing.T) {
		mockBuilder := &mockQueryFilterBuilder{}
		err := vs.buildQueryFilter(mockBuilder, nil)
		require.NoError(t, err)
		assert.False(t, mockBuilder.idFilterCalled)
		assert.False(t, mockBuilder.metadataFilterCalled)
		assert.False(t, mockBuilder.filterConditionCalled)
	})

	t.Run("with_ids_only", func(t *testing.T) {
		mockBuilder := &mockQueryFilterBuilder{}
		filter := &vectorstore.SearchFilter{
			IDs: []string{"id1", "id2"},
		}
		err := vs.buildQueryFilter(mockBuilder, filter)
		require.NoError(t, err)
		assert.True(t, mockBuilder.idFilterCalled)
		assert.Equal(t, []string{"id1", "id2"}, mockBuilder.ids)
		assert.False(t, mockBuilder.metadataFilterCalled)
		assert.False(t, mockBuilder.filterConditionCalled)
	})

	t.Run("with_metadata_only", func(t *testing.T) {
		mockBuilder := &mockQueryFilterBuilder{}
		filter := &vectorstore.SearchFilter{
			Metadata: map[string]any{"key": "value"},
		}
		err := vs.buildQueryFilter(mockBuilder, filter)
		require.NoError(t, err)
		assert.False(t, mockBuilder.idFilterCalled)
		assert.True(t, mockBuilder.metadataFilterCalled)
		assert.Equal(t, map[string]any{"key": "value"}, mockBuilder.metadata)
		assert.False(t, mockBuilder.filterConditionCalled)
	})

	t.Run("with_ids_and_metadata", func(t *testing.T) {
		mockBuilder := &mockQueryFilterBuilder{}
		filter := &vectorstore.SearchFilter{
			IDs:      []string{"id1"},
			Metadata: map[string]any{"key": "value"},
		}
		err := vs.buildQueryFilter(mockBuilder, filter)
		require.NoError(t, err)
		assert.True(t, mockBuilder.idFilterCalled)
		assert.True(t, mockBuilder.metadataFilterCalled)
	})

	t.Run("empty_filter", func(t *testing.T) {
		mockBuilder := &mockQueryFilterBuilder{}
		filter := &vectorstore.SearchFilter{}
		err := vs.buildQueryFilter(mockBuilder, filter)
		require.NoError(t, err)
		assert.False(t, mockBuilder.idFilterCalled)
		assert.False(t, mockBuilder.metadataFilterCalled)
		assert.False(t, mockBuilder.filterConditionCalled)
	})

	t.Run("with_filter_condition_success", func(t *testing.T) {
		mockBuilder := &mockQueryFilterBuilder{}
		filter := &vectorstore.SearchFilter{
			FilterCondition: &searchfilter.UniversalFilterCondition{
				Field:    "status",
				Operator: searchfilter.OperatorEqual,
				Value:    "active",
			},
		}
		err := vs.buildQueryFilter(mockBuilder, filter)
		require.NoError(t, err)
		assert.True(t, mockBuilder.filterConditionCalled)
		assert.NotNil(t, mockBuilder.filterCondition)
		assert.Contains(t, mockBuilder.filterCondition.cond, "status")
	})

	t.Run("with_filter_condition_nil", func(t *testing.T) {
		mockBuilder := &mockQueryFilterBuilder{}
		filter := &vectorstore.SearchFilter{
			IDs:             []string{"id1"},
			FilterCondition: nil, // Explicitly nil
		}
		err := vs.buildQueryFilter(mockBuilder, filter)
		require.NoError(t, err)
		assert.True(t, mockBuilder.idFilterCalled)
		assert.False(t, mockBuilder.filterConditionCalled)
	})

	t.Run("with_filter_condition_error", func(t *testing.T) {
		// Create a mock converter that returns an error
		mockConverter := &mockFilterConverter{
			shouldError: true,
		}
		vsWithMockConverter := &VectorStore{
			filterConverter: mockConverter,
		}

		mockBuilder := &mockQueryFilterBuilder{}
		filter := &vectorstore.SearchFilter{
			FilterCondition: &searchfilter.UniversalFilterCondition{
				Field:    "invalid",
				Operator: "INVALID_OP",
				Value:    "test",
			},
		}
		err := vsWithMockConverter.buildQueryFilter(mockBuilder, filter)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "converter error")
		assert.False(t, mockBuilder.filterConditionCalled) // Should not be called if conversion fails
	})
}

// mockQueryFilterBuilder is a mock implementation of queryFilterBuilder
type mockQueryFilterBuilder struct {
	idFilterCalled        bool
	metadataFilterCalled  bool
	filterConditionCalled bool
	ids                   []string
	metadata              map[string]any
	filterCondition       *condConvertResult
}

func (m *mockQueryFilterBuilder) addIDFilter(ids []string) {
	m.idFilterCalled = true
	m.ids = ids
}

func (m *mockQueryFilterBuilder) addMetadataFilter(metadata map[string]any) {
	m.metadataFilterCalled = true
	m.metadata = metadata
}

func (m *mockQueryFilterBuilder) addFilterCondition(condition *condConvertResult) {
	m.filterConditionCalled = true
	m.filterCondition = condition
}

// mockFilterConverter is a mock implementation of filter converter
type mockFilterConverter struct {
	shouldError bool
}

func (m *mockFilterConverter) Convert(cond *searchfilter.UniversalFilterCondition) (*condConvertResult, error) {
	if m.shouldError {
		return nil, errors.New("converter error")
	}
	return &condConvertResult{
		cond: "status = $1",
		args: []any{"active"},
	}, nil
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
