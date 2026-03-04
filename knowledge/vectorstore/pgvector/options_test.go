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
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// mockRow implements pgx.Row for testing
type mockRow struct {
	scanFunc func(...any) error
}

func (m *mockRow) Scan(dest ...any) error {
	return m.scanFunc(dest...)
}

// TestDefaultDocBuilder tests the default document builder function
func TestDefaultDocBuilder(t *testing.T) {
	tests := []struct {
		name        string
		setupMock   func() pgx.Row
		expectError bool
		validate    func(*testing.T, *vectorstore.ScoredDocument, []float64)
	}{
		{
			name: "successful_scan_with_valid_data",
			setupMock: func() pgx.Row {
				return &mockRow{
					scanFunc: func(dest ...any) error {
						// Simulate successful scan with valid data
						*(dest[0].(*string)) = "doc-1"
						*(dest[1].(*string)) = "test-name"
						*(dest[2].(*string)) = "test-content"
						*(dest[3].(*pgvector.Vector)) = pgvector.NewVector([]float32{0.1, 0.2, 0.3})
						*(dest[4].(*string)) = `{"key":"value"}`
						*(dest[5].(*int64)) = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
						*(dest[6].(*int64)) = time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC).Unix()
						*(dest[7].(*float64)) = 0.85
						*(dest[8].(*float64)) = 0.10
						*(dest[9].(*float64)) = 0.95
						return nil
					},
				}
			},
			expectError: false,
			validate: func(t *testing.T, doc *vectorstore.ScoredDocument, vector []float64) {
				assert.NotNil(t, doc)
				assert.NotNil(t, doc.Document)
				assert.Equal(t, "doc-1", doc.Document.ID)
				assert.Equal(t, "test-name", doc.Document.Name)
				assert.Equal(t, "test-content", doc.Document.Content)
				assert.Equal(t, 0.95, doc.Score)
				assert.Equal(t, "value", doc.Document.Metadata["key"])
				assert.Equal(t, 0.85, doc.Document.Metadata["trpc_agent_go_dense_score"])
				assert.Equal(t, 0.10, doc.Document.Metadata["trpc_agent_go_sparse_score"])
				// Use InDelta for float comparison due to float32->float64 precision loss
				assert.Len(t, vector, 3)
				assert.InDelta(t, 0.1, vector[0], 0.0001)
				assert.InDelta(t, 0.2, vector[1], 0.0001)
				assert.InDelta(t, 0.3, vector[2], 0.0001)
				assert.Equal(t, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix(), doc.Document.CreatedAt.Unix())
				assert.Equal(t, time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC).Unix(), doc.Document.UpdatedAt.Unix())
			},
		},
		{
			name: "successful_scan_with_empty_metadata",
			setupMock: func() pgx.Row {
				return &mockRow{
					scanFunc: func(dest ...any) error {
						*(dest[0].(*string)) = "doc-2"
						*(dest[1].(*string)) = "name-2"
						*(dest[2].(*string)) = "content-2"
						*(dest[3].(*pgvector.Vector)) = pgvector.NewVector([]float32{0.5})
						*(dest[4].(*string)) = "{}"
						*(dest[5].(*int64)) = 0
						*(dest[6].(*int64)) = 0
						*(dest[7].(*float64)) = 0.8
						*(dest[8].(*float64)) = 0.0
						*(dest[9].(*float64)) = 0.8
						return nil
					},
				}
			},
			expectError: false,
			validate: func(t *testing.T, doc *vectorstore.ScoredDocument, vector []float64) {
				assert.NotNil(t, doc)
				assert.Equal(t, "doc-2", doc.Document.ID)
				assert.Equal(t, 0.8, doc.Score)
				assert.Equal(t, 0.8, doc.Document.Metadata["trpc_agent_go_dense_score"])
				assert.Equal(t, 0.0, doc.Document.Metadata["trpc_agent_go_sparse_score"])
				assert.Len(t, vector, 1)
				assert.InDelta(t, 0.5, vector[0], 0.0001)
			},
		},
		{
			name: "scan_error",
			setupMock: func() pgx.Row {
				return &mockRow{
					scanFunc: func(dest ...any) error {
						return pgx.ErrNoRows
					},
				}
			},
			expectError: true,
		},
		{
			name: "invalid_json_metadata",
			setupMock: func() pgx.Row {
				return &mockRow{
					scanFunc: func(dest ...any) error {
						*(dest[0].(*string)) = "doc-3"
						*(dest[1].(*string)) = "name-3"
						*(dest[2].(*string)) = "content-3"
						*(dest[3].(*pgvector.Vector)) = pgvector.NewVector([]float32{0.1})
						*(dest[4].(*string)) = `{invalid json}`
						*(dest[5].(*int64)) = 0
						*(dest[6].(*int64)) = 0
						*(dest[7].(*float64)) = 0.5
						*(dest[8].(*float64)) = 0.0
						*(dest[9].(*float64)) = 0.5
						return nil
					},
				}
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := tt.setupMock()
			doc, vector, err := defaultDocBuilder(row)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, doc)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, doc)
				assert.NotNil(t, vector)
				tt.validate(t, doc, vector)
			}
		})
	}
}

// TestWithDocBuilder tests custom document builder option
func TestWithDocBuilder(t *testing.T) {
	t.Run("custom_builder_is_set", func(t *testing.T) {
		called := false
		customBuilder := func(row pgx.Row) (*vectorstore.ScoredDocument, []float64, error) {
			called = true
			return &vectorstore.ScoredDocument{}, []float64{1.0, 2.0}, nil
		}

		opts := defaultOptions
		WithDocBuilder(customBuilder)(&opts)

		assert.NotNil(t, opts.docBuilder)

		// Verify the custom builder is actually called
		doc, vector, err := opts.docBuilder(nil)
		assert.True(t, called)
		assert.NoError(t, err)
		assert.NotNil(t, doc)
		assert.Equal(t, []float64{1.0, 2.0}, vector)
	})

	t.Run("custom_builder_with_error", func(t *testing.T) {
		expectedErr := errors.New("custom error")
		customBuilder := func(row pgx.Row) (*vectorstore.ScoredDocument, []float64, error) {
			return nil, nil, expectedErr
		}

		opts := defaultOptions
		WithDocBuilder(customBuilder)(&opts)

		doc, vector, err := opts.docBuilder(nil)
		assert.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, doc)
		assert.Nil(t, vector)
	})
}

// TestDocBuilderFunc tests the DocBuilderFunc type
func TestDocBuilderFunc(t *testing.T) {
	t.Run("nil_doc_builder", func(t *testing.T) {
		opts := defaultOptions
		assert.NotNil(t, opts.docBuilder)

		// Default doc builder should be set
		row := &mockRow{
			scanFunc: func(dest ...any) error {
				*(dest[0].(*string)) = "test-id"
				*(dest[1].(*string)) = "test-name"
				*(dest[2].(*string)) = "test-content"
				*(dest[3].(*pgvector.Vector)) = pgvector.NewVector([]float32{0.5})
				*(dest[4].(*string)) = `{}`
				*(dest[5].(*int64)) = 0
				*(dest[6].(*int64)) = 0
				*(dest[7].(*float64)) = 0.9
				return nil
			},
		}

		doc, vector, err := opts.docBuilder(row)
		assert.NoError(t, err)
		assert.NotNil(t, doc)
		assert.NotNil(t, vector)
		assert.Equal(t, "test-id", doc.Document.ID)
	})
}

func TestWithPGVectorClientDSN(t *testing.T) {
	opts := defaultOptions
	dsn := "postgres://user:pass@host:5432/db"
	WithPGVectorClientDSN(dsn)(&opts)
	assert.Equal(t, dsn, opts.dsn)
}

// freshOptions returns a copy of defaultOptions with its own RRFParams
// to avoid pointer sharing between tests.
func freshOptions() options {
	o := defaultOptions
	o.rrfParams = &RRFParams{K: 60, CandidateRatio: 3}
	return o
}

func TestWithRRFParams(t *testing.T) {
	t.Run("nil_params_no_change", func(t *testing.T) {
		opts := freshOptions()
		WithRRFParams(nil)(&opts)
		assert.Equal(t, 60, opts.rrfParams.K)
		assert.Equal(t, 3, opts.rrfParams.CandidateRatio)
	})

	t.Run("valid_params", func(t *testing.T) {
		opts := freshOptions()
		WithRRFParams(&RRFParams{K: 30, CandidateRatio: 5})(&opts)
		assert.Equal(t, 30, opts.rrfParams.K)
		assert.Equal(t, 5, opts.rrfParams.CandidateRatio)
	})

	t.Run("zero_values_keep_defaults", func(t *testing.T) {
		opts := freshOptions()
		WithRRFParams(&RRFParams{K: 0, CandidateRatio: 0})(&opts)
		assert.Equal(t, 60, opts.rrfParams.K)
		assert.Equal(t, 3, opts.rrfParams.CandidateRatio)
	})

	t.Run("negative_values_keep_defaults", func(t *testing.T) {
		opts := freshOptions()
		WithRRFParams(&RRFParams{K: -10, CandidateRatio: -5})(&opts)
		assert.Equal(t, 60, opts.rrfParams.K)
		assert.Equal(t, 3, opts.rrfParams.CandidateRatio)
	})

	t.Run("large_values_accepted", func(t *testing.T) {
		opts := freshOptions()
		WithRRFParams(&RRFParams{K: 500, CandidateRatio: 20})(&opts)
		assert.Equal(t, 500, opts.rrfParams.K)
		assert.Equal(t, 20, opts.rrfParams.CandidateRatio)
	})

	t.Run("partial_update_k_only", func(t *testing.T) {
		opts := freshOptions()
		WithRRFParams(&RRFParams{K: 20})(&opts)
		assert.Equal(t, 20, opts.rrfParams.K)
		assert.Equal(t, 3, opts.rrfParams.CandidateRatio)
	})

	t.Run("partial_update_candidate_ratio_only", func(t *testing.T) {
		opts := freshOptions()
		WithRRFParams(&RRFParams{CandidateRatio: 7})(&opts)
		assert.Equal(t, 60, opts.rrfParams.K)
		assert.Equal(t, 7, opts.rrfParams.CandidateRatio)
	})
}

func TestWithHybridFusionMode(t *testing.T) {
	t.Run("set_rrf_mode", func(t *testing.T) {
		opts := defaultOptions
		WithHybridFusionMode(HybridFusionRRF)(&opts)
		assert.Equal(t, HybridFusionRRF, opts.fusionMode)
	})

	t.Run("set_weighted_mode", func(t *testing.T) {
		opts := defaultOptions
		opts.fusionMode = HybridFusionRRF
		WithHybridFusionMode(HybridFusionWeighted)(&opts)
		assert.Equal(t, HybridFusionWeighted, opts.fusionMode)
	})
}
