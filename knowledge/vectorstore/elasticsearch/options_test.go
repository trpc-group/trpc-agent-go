//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package elasticsearch

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/storage/elasticsearch"
)

func TestOptionSettersOverrideValues(t *testing.T) {
	opt := defaultOptions

	WithAddresses([]string{"http://example:9200"})(&opt)
	WithUsername("user")(&opt)
	WithPassword("pass")(&opt)
	WithAPIKey("apikey")(&opt)
	WithCertificateFingerprint("fp")(&opt)
	WithCompressRequestBody(false)(&opt)
	WithEnableMetrics(true)(&opt)
	WithEnableDebugLogger(true)(&opt)
	WithRetryOnStatus([]int{500, 408})(&opt)
	WithMaxRetries(7)(&opt)
	WithIndexName("idx")(&opt)
	WithScoreThreshold(0.12)(&opt)
	WithMaxResults(5)(&opt)
	WithVectorDimension(123)(&opt)
	WithEnableTSVector(false)(&opt)
	WithVersion(string(elasticsearch.ESVersionV8))(&opt)
	WithIDField("doc_id")(&opt)
	WithNameField("title")(&opt)
	WithContentField("body")(&opt)
	WithEmbeddingField("vec")(&opt)

	assert.Equal(t, []string{"http://example:9200"}, opt.addresses)
	assert.Equal(t, "user", opt.username)
	assert.Equal(t, "pass", opt.password)
	assert.Equal(t, "apikey", opt.apiKey)
	assert.Equal(t, "fp", opt.certificateFingerprint)
	assert.False(t, opt.compressRequestBody)
	assert.True(t, opt.enableMetrics)
	assert.True(t, opt.enableDebugLogger)
	assert.Equal(t, []int{500, 408}, opt.retryOnStatus)
	assert.Equal(t, 7, opt.maxRetries)
	assert.Equal(t, "idx", opt.indexName)
	assert.InDelta(t, 0.12, opt.scoreThreshold, 1e-9)
	assert.Equal(t, 5, opt.maxResults)
	assert.Equal(t, 123, opt.vectorDimension)
	assert.False(t, opt.enableTSVector)
	assert.Equal(t, elasticsearch.ESVersionV8, opt.version)
	assert.Equal(t, "doc_id", opt.idFieldName)
	assert.Equal(t, "title", opt.nameFieldName)
	assert.Equal(t, "body", opt.contentFieldName)
	assert.Equal(t, "vec", opt.embeddingFieldName)
}

func TestWithExtraOptions(t *testing.T) {
	t.Run("accumulation", func(t *testing.T) {
		opt := defaultOptions

		WithExtraOptions("first")(&opt)
		WithExtraOptions(2, true)(&opt)

		assert.Equal(t, []any{"first", 2, true}, opt.extraOptions)
	})

	t.Run("empty noop", func(t *testing.T) {
		opt := defaultOptions

		WithExtraOptions()(&opt)
		assert.Nil(t, opt.extraOptions)
	})
}

// TestAdditionalOptions tests options with 0% coverage
func TestAdditionalOptions(t *testing.T) {
	tests := []struct {
		name     string
		option   Option
		validate func(*testing.T, *options)
	}{
		{
			name:   "WithMetadataField",
			option: WithMetadataField("custom_meta"),
			validate: func(t *testing.T, opt *options) {
				assert.Equal(t, "custom_meta", opt.metadataFieldName)
			},
		},
		{
			name:   "WithCreatedAtField",
			option: WithCreatedAtField("created_time"),
			validate: func(t *testing.T, opt *options) {
				assert.Equal(t, "created_time", opt.createdAtFieldName)
			},
		},
		{
			name:   "WithUpdatedAtField",
			option: WithUpdatedAtField("updated_time"),
			validate: func(t *testing.T, opt *options) {
				assert.Equal(t, "updated_time", opt.updatedAtFieldName)
			},
		},
		{
			name: "WithDocBuilder",
			option: WithDocBuilder(func(json.RawMessage) (*document.Document, []float64, error) {
				return &document.Document{}, []float64{}, nil
			}),
			validate: func(t *testing.T, opt *options) {
				assert.NotNil(t, opt.docBuilder)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := defaultOptions
			tt.option(&opt)
			tt.validate(t, &opt)
		})
	}
}
