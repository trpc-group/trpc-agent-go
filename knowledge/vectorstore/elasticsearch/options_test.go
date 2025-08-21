package elasticsearch

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultOptionsValues(t *testing.T) {
	// Basic sanity on a few key defaults
	assert.NotNil(t, defaultOptions.addresses)
	assert.Equal(t, 3, defaultOptions.maxRetries)
	assert.True(t, defaultOptions.compressRequestBody)
	assert.Equal(t, []int{502, 503, 504, 429}, defaultOptions.retryOnStatus)
	assert.Equal(t, defaultIndexName, defaultOptions.indexName)
	assert.Equal(t, defaultVectorField, defaultOptions.vectorField)
	assert.Equal(t, defaultContentField, defaultOptions.contentField)
	assert.Equal(t, defaultMetadataField, defaultOptions.metadataField)
	assert.Equal(t, defaultScoreThreshold, defaultOptions.scoreThreshold)
	assert.Equal(t, defaultMaxResults, defaultOptions.maxResults)
	assert.Equal(t, defaultVectorDimension, defaultOptions.vectorDimension)
	assert.True(t, defaultOptions.enableTSVector)
	assert.Equal(t, "english", defaultOptions.language)
}

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
	WithVectorField("vf")(&opt)
	WithContentField("cf")(&opt)
	WithMetadataField("mf")(&opt)
	WithScoreThreshold(0.12)(&opt)
	WithMaxResults(5)(&opt)
	WithVectorDimension(123)(&opt)
	WithEnableTSVector(false)(&opt)
	WithLanguage("chinese")(&opt)

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
	assert.Equal(t, "vf", opt.vectorField)
	assert.Equal(t, "cf", opt.contentField)
	assert.Equal(t, "mf", opt.metadataField)
	assert.InDelta(t, 0.12, opt.scoreThreshold, 1e-9)
	assert.Equal(t, 5, opt.maxResults)
	assert.Equal(t, 123, opt.vectorDimension)
	assert.False(t, opt.enableTSVector)
	assert.Equal(t, "chinese", opt.language)
}
