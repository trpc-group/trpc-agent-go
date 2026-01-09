//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tcvector

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAllOptions tests all configuration option functions
func TestAllOptions(t *testing.T) {
	opt := defaultOptions

	// Test WithURL
	WithURL("http://test.com")(&opt)
	assert.Equal(t, "http://test.com", opt.url)

	// Test WithUsername
	WithUsername("testuser")(&opt)
	assert.Equal(t, "testuser", opt.username)

	// Test WithPassword
	WithPassword("testpass")(&opt)
	assert.Equal(t, "testpass", opt.password)

	// Test WithDatabase
	WithDatabase("test_db")(&opt)
	assert.Equal(t, "test_db", opt.database)

	// Test WithCollection
	WithCollection("test_col")(&opt)
	assert.Equal(t, "test_col", opt.collection)

	// Test WithIndexDimension
	WithIndexDimension(768)(&opt)
	assert.Equal(t, uint32(768), opt.indexDimension)

	// Test WithReplicas
	WithReplicas(3)(&opt)
	assert.Equal(t, uint32(3), opt.replicas)

	// Test WithSharding
	WithSharding(5)(&opt)
	assert.Equal(t, uint32(5), opt.sharding)

	// Test WithEnableTSVector
	WithEnableTSVector(true)(&opt)
	assert.True(t, opt.enableTSVector)

	// Test WithHybridSearchWeights
	WithHybridSearchWeights(0.7, 0.3)(&opt)
	assert.Equal(t, 0.7, opt.vectorWeight)
	assert.Equal(t, 0.3, opt.textWeight)

	// Test WithLanguage
	WithLanguage("zh")(&opt)
	assert.Equal(t, "zh", opt.language)

	// Test WithTCVectorInstance
	WithTCVectorInstance("instance1")(&opt)
	assert.Equal(t, "instance1", opt.instanceName)

	// Test WithExtraOptions
	WithExtraOptions("opt1", 2)(&opt)
	assert.Contains(t, opt.extraOptions, "opt1")
	assert.Contains(t, opt.extraOptions, 2)

	// Test WithFilterIndexFields
	WithFilterIndexFields([]string{"field1", "field2"})(&opt)
	assert.Contains(t, opt.filterFields, "field1")
	assert.Contains(t, opt.filterFields, "field2")

	// Test WithMaxResults
	WithMaxResults(100)(&opt)
	assert.Equal(t, 100, opt.maxResults)

	// Test WithIDField
	WithIDField("custom_id")(&opt)
	assert.Equal(t, "custom_id", opt.idFieldName)

	// Test WithNameField
	WithNameField("custom_name")(&opt)
	assert.Equal(t, "custom_name", opt.nameFieldName)

	// Test WithContentField
	WithContentField("custom_content")(&opt)
	assert.Equal(t, "custom_content", opt.contentFieldName)

	// Test WithEmbeddingField
	WithEmbeddingField("custom_embedding")(&opt)
	assert.Equal(t, "custom_embedding", opt.embeddingFieldName)

	// Test WithMetadataField
	WithMetadataField("custom_metadata")(&opt)
	assert.Equal(t, "custom_metadata", opt.metadataFieldName)

	// Test WithCreatedAtField
	WithCreatedAtField("custom_created")(&opt)
	assert.Equal(t, "custom_created", opt.createdAtFieldName)

	// Test WithUpdatedAtField
	WithUpdatedAtField("custom_updated")(&opt)
	assert.Equal(t, "custom_updated", opt.updatedAtFieldName)

	// Test WithSparseVectorField
	WithSparseVectorField("custom_sparse")(&opt)
	assert.Equal(t, "custom_sparse", opt.sparseVectorFieldName)
}

// TestOptionsDefaults verifies default option values
func TestOptionsDefaults(t *testing.T) {
	opt := defaultOptions

	assert.Equal(t, "trpc-agent-go", opt.database)
	assert.Equal(t, "documents", opt.collection)
	assert.Equal(t, uint32(1536), opt.indexDimension)
	assert.Equal(t, 10, opt.maxResults)
	assert.Equal(t, 0.7, opt.vectorWeight)
	assert.Equal(t, 0.3, opt.textWeight)
	assert.True(t, opt.enableTSVector)
	assert.Equal(t, "vector", opt.embeddingFieldName)
	assert.Len(t, opt.filterFields, 2) // Default has 2 filter fields
}

// TestOptionIndependence verifies options don't interfere
func TestOptionIndependence(t *testing.T) {
	opt1 := defaultOptions
	opt2 := defaultOptions

	WithDatabase("db1")(&opt1)
	WithDatabase("db2")(&opt2)

	assert.Equal(t, "db1", opt1.database)
	assert.Equal(t, "db2", opt2.database)
	assert.NotEqual(t, opt1.database, opt2.database)
}
