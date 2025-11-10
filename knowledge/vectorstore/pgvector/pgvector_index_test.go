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
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

// mockClientForIndex is a mock postgres client for index creation testing
type mockClientForIndex struct {
	execContextFunc func(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (m *mockClientForIndex) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if m.execContextFunc != nil {
		return m.execContextFunc(ctx, query, args...)
	}
	return nil, nil
}

func (m *mockClientForIndex) Query(ctx context.Context, callback postgres.HandlerFunc, query string, args ...any) error {
	return nil
}

func (m *mockClientForIndex) Transaction(ctx context.Context, fn postgres.TxFunc) error {
	// For testing purposes, we just execute the function directly
	// In real implementation, it would wrap with transaction
	return nil
}

func (m *mockClientForIndex) Close() error {
	return nil
}

// TestCreateVectorIndex_HNSW tests HNSW index creation
func TestCreateVectorIndex_HNSW(t *testing.T) {
	tests := []struct {
		name            string
		hnswParams      *HNSWIndexParams
		expectedM       int
		expectedEfConst int
	}{
		{
			name:            "HNSW with default params",
			hnswParams:      nil,
			expectedM:       defaultHNSWM,
			expectedEfConst: defaultHNSWEfConstruction,
		},
		{
			name: "HNSW with custom params",
			hnswParams: &HNSWIndexParams{
				M:              32,
				EfConstruction: 200,
			},
			expectedM:       32,
			expectedEfConst: 200,
		},
		{
			name: "HNSW with zero M uses default",
			hnswParams: &HNSWIndexParams{
				M:              0,
				EfConstruction: 200,
			},
			expectedM:       defaultHNSWM,
			expectedEfConst: 200,
		},
		{
			name: "HNSW with zero EfConstruction uses default",
			hnswParams: &HNSWIndexParams{
				M:              32,
				EfConstruction: 0,
			},
			expectedM:       32,
			expectedEfConst: defaultHNSWEfConstruction,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedSQL string
			mockClient := &mockClientForIndex{
				execContextFunc: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
					capturedSQL = query
					return nil, nil
				},
			}

			vs := &VectorStore{
				client: mockClient,
				option: options{
					table:              "test_table",
					embeddingFieldName: "embedding",
					vectorIndexType:    VectorIndexHNSW,
					hnswParams:         tt.hnswParams,
				},
			}

			err := vs.createVectorIndex(context.Background())
			require.NoError(t, err)

			// Verify SQL contains expected parameters
			assert.Contains(t, capturedSQL, "USING hnsw")
			assert.Contains(t, capturedSQL, "test_table")
			assert.Contains(t, capturedSQL, "embedding")
			assert.Contains(t, capturedSQL, fmt.Sprintf("m = %d", tt.expectedM))
			assert.Contains(t, capturedSQL, fmt.Sprintf("ef_construction = %d", tt.expectedEfConst))
		})
	}
}

// TestCreateVectorIndex_IVFFlat tests IVFFlat index creation
func TestCreateVectorIndex_IVFFlat(t *testing.T) {
	tests := []struct {
		name          string
		ivfflatParams *IVFFlatIndexParams
		expectedLists int
	}{
		{
			name:          "IVFFlat with default params",
			ivfflatParams: nil,
			expectedLists: defaultIVFFlatLists,
		},
		{
			name: "IVFFlat with custom params",
			ivfflatParams: &IVFFlatIndexParams{
				Lists: 1000,
			},
			expectedLists: 1000,
		},
		{
			name: "IVFFlat with zero lists uses default",
			ivfflatParams: &IVFFlatIndexParams{
				Lists: 0,
			},
			expectedLists: defaultIVFFlatLists,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedSQL string
			mockClient := &mockClientForIndex{
				execContextFunc: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
					capturedSQL = query
					return nil, nil
				},
			}

			vs := &VectorStore{
				client: mockClient,
				option: options{
					table:              "test_table",
					embeddingFieldName: "embedding",
					vectorIndexType:    VectorIndexIVFFlat,
					ivfflatParams:      tt.ivfflatParams,
				},
			}

			err := vs.createVectorIndex(context.Background())
			require.NoError(t, err)

			// Verify SQL contains expected parameters
			assert.Contains(t, capturedSQL, "USING ivfflat")
			assert.Contains(t, capturedSQL, "test_table")
			assert.Contains(t, capturedSQL, "embedding")
			assert.Contains(t, capturedSQL, fmt.Sprintf("lists = %d", tt.expectedLists))
		})
	}
}

// TestCreateVectorIndex_UnsupportedType tests error handling for unsupported index types
func TestCreateVectorIndex_UnsupportedType(t *testing.T) {
	mockClient := &mockClientForIndex{}

	vs := &VectorStore{
		client: mockClient,
		option: options{
			vectorIndexType: VectorIndexType("invalid"),
		},
	}

	err := vs.createVectorIndex(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported vector index type")
}

// TestCreateVectorIndex_ExecError tests error handling when ExecContext fails
func TestCreateVectorIndex_ExecError(t *testing.T) {
	expectedErr := fmt.Errorf("database error")
	mockClient := &mockClientForIndex{
		execContextFunc: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, expectedErr
		},
	}

	vs := &VectorStore{
		client: mockClient,
		option: options{
			table:              "test_table",
			embeddingFieldName: "embedding",
			vectorIndexType:    VectorIndexHNSW,
			hnswParams: &HNSWIndexParams{
				M:              16,
				EfConstruction: 64,
			},
		},
	}

	err := vs.createVectorIndex(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create vector index")
	assert.Contains(t, err.Error(), "hnsw")
}

// TestCreateVectorIndex_SQLFormat tests the exact SQL format
func TestCreateVectorIndex_SQLFormat(t *testing.T) {
	tests := []struct {
		name        string
		indexType   VectorIndexType
		table       string
		field       string
		hnswParams  *HNSWIndexParams
		ivfParams   *IVFFlatIndexParams
		expectedSQL string
	}{
		{
			name:      "HNSW SQL format",
			indexType: VectorIndexHNSW,
			table:     "docs",
			field:     "vec",
			hnswParams: &HNSWIndexParams{
				M:              24,
				EfConstruction: 100,
			},
			expectedSQL: `CREATE INDEX IF NOT EXISTS docs_embedding_idx ON docs USING hnsw (vec vector_cosine_ops) WITH (m = 24, ef_construction = 100)`,
		},
		{
			name:      "IVFFlat SQL format",
			indexType: VectorIndexIVFFlat,
			table:     "docs",
			field:     "vec",
			ivfParams: &IVFFlatIndexParams{
				Lists: 500,
			},
			expectedSQL: `CREATE INDEX IF NOT EXISTS docs_embedding_idx ON docs USING ivfflat (vec vector_cosine_ops) WITH (lists = 500)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedSQL string
			mockClient := &mockClientForIndex{
				execContextFunc: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
					capturedSQL = query
					return nil, nil
				},
			}

			vs := &VectorStore{
				client: mockClient,
				option: options{
					table:              tt.table,
					embeddingFieldName: tt.field,
					vectorIndexType:    tt.indexType,
					hnswParams:         tt.hnswParams,
					ivfflatParams:      tt.ivfParams,
				},
			}

			err := vs.createVectorIndex(context.Background())
			require.NoError(t, err)

			// Normalize whitespace for comparison
			normalizeSpace := func(s string) string {
				s = strings.TrimSpace(s)
				re := regexp.MustCompile(`\s+`)
				return re.ReplaceAllString(s, " ")
			}

			assert.Equal(t, normalizeSpace(tt.expectedSQL), normalizeSpace(capturedSQL))
		})
	}
}

func TestVectorIndexType(t *testing.T) {
	tests := []struct {
		name      string
		indexType VectorIndexType
		expected  VectorIndexType
	}{
		{
			name:      "HNSW index type",
			indexType: VectorIndexHNSW,
			expected:  "hnsw",
		},
		{
			name:      "IVFFlat index type",
			indexType: VectorIndexIVFFlat,
			expected:  "ivfflat",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.indexType)
		})
	}
}

func TestWithVectorIndexType(t *testing.T) {
	tests := []struct {
		name      string
		indexType VectorIndexType
	}{
		{
			name:      "Set HNSW index",
			indexType: VectorIndexHNSW,
		},
		{
			name:      "Set IVFFlat index",
			indexType: VectorIndexIVFFlat,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := defaultOptions
			WithVectorIndexType(tt.indexType)(&opts)
			assert.Equal(t, tt.indexType, opts.vectorIndexType)
		})
	}
}

func TestWithHNSWIndexParams(t *testing.T) {
	tests := []struct {
		name       string
		params     *HNSWIndexParams
		expectedM  int
		expectedEf int
	}{
		{
			name: "Valid HNSW parameters",
			params: &HNSWIndexParams{
				M:              32,
				EfConstruction: 200,
			},
			expectedM:  32,
			expectedEf: 200,
		},
		{
			name:       "Nil params keeps defaults",
			params:     nil,
			expectedM:  defaultHNSWM,
			expectedEf: defaultHNSWEfConstruction,
		},
		{
			name: "Only m set",
			params: &HNSWIndexParams{
				M:              64,
				EfConstruction: 0,
			},
			expectedM:  64,
			expectedEf: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := defaultOptions
			WithHNSWIndexParams(tt.params)(&opts)
			if tt.params != nil {
				assert.Equal(t, tt.expectedM, opts.hnswParams.M)
				assert.Equal(t, tt.expectedEf, opts.hnswParams.EfConstruction)
			} else {
				// nil params should not change defaults
				assert.Equal(t, defaultHNSWM, opts.hnswParams.M)
				assert.Equal(t, defaultHNSWEfConstruction, opts.hnswParams.EfConstruction)
			}
		})
	}
}

func TestWithIVFFlatIndexParams(t *testing.T) {
	tests := []struct {
		name          string
		params        *IVFFlatIndexParams
		expectedLists int
	}{
		{
			name: "Valid IVFFlat parameters",
			params: &IVFFlatIndexParams{
				Lists: 1000,
			},
			expectedLists: 1000,
		},
		{
			name:          "Nil params keeps default",
			params:        nil,
			expectedLists: defaultIVFFlatLists,
		},
		{
			name: "Zero value",
			params: &IVFFlatIndexParams{
				Lists: 0,
			},
			expectedLists: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := defaultOptions
			WithIVFFlatIndexParams(tt.params)(&opts)
			if tt.params != nil {
				assert.Equal(t, tt.expectedLists, opts.ivfflatParams.Lists)
			} else {
				// nil params should not change defaults
				assert.Equal(t, defaultIVFFlatLists, opts.ivfflatParams.Lists)
			}
		})
	}
}

func TestVectorIndexDefaultOptions(t *testing.T) {
	assert.Equal(t, VectorIndexHNSW, defaultOptions.vectorIndexType)
	assert.NotNil(t, defaultOptions.hnswParams)
	assert.Equal(t, defaultHNSWM, defaultOptions.hnswParams.M)
	assert.Equal(t, defaultHNSWEfConstruction, defaultOptions.hnswParams.EfConstruction)
	assert.NotNil(t, defaultOptions.ivfflatParams)
	assert.Equal(t, defaultIVFFlatLists, defaultOptions.ivfflatParams.Lists)
}
