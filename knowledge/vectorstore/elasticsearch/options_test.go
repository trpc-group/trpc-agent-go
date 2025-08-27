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
	"testing"

	"github.com/stretchr/testify/assert"
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
}
