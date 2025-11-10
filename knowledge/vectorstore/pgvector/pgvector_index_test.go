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
)

func TestVectorIndexType(t *testing.T) {
	tests := []struct {
		name     string
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
