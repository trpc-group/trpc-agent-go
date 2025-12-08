//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package milvus

import (
	"testing"

	client "github.com/milvus-io/milvus/client/v2/milvusclient"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
)

// TestAllOptions tests all configuration option functions
func TestAllOptions(t *testing.T) {
	opt := defaultOptions

	WithCollectionName("test_collection")(&opt)
	assert.Equal(t, "test_collection", opt.collectionName)

	WithDimension(768)(&opt)
	assert.Equal(t, 768, opt.dimension)

	WithAddress("localhost:19530")(&opt)
	assert.Equal(t, "localhost:19530", opt.address)

	WithUsername("testuser")(&opt)
	assert.Equal(t, "testuser", opt.username)

	WithPassword("testpass")(&opt)
	assert.Equal(t, "testpass", opt.password)

	WithDBName("test_db")(&opt)
	assert.Equal(t, "test_db", opt.dbName)

	WithAPIKey("test_api_key")(&opt)
	assert.Equal(t, "test_api_key", opt.apiKey)

	WithMaxResults(100)(&opt)
	assert.Equal(t, 100, opt.maxResults)

	WithHNSWParams(32, 256)(&opt)
	assert.True(t, opt.enableHNSW)
	assert.Equal(t, 32, opt.hnswM)
	assert.Equal(t, 256, opt.hnswEfConstruction)

	WithIDField("custom_id")(&opt)
	assert.Equal(t, "custom_id", opt.idField)

	WithNameField("custom_name")(&opt)
	assert.Equal(t, "custom_name", opt.nameField)

	WithContentField("custom_content")(&opt)
	assert.Equal(t, "custom_content", opt.contentField)

	WithVectorField("custom_vector")(&opt)
	assert.Equal(t, "custom_vector", opt.vectorField)

	WithMetadataField("custom_metadata")(&opt)
	assert.Equal(t, "custom_metadata", opt.metadataField)

	WithCreatedAtField("custom_created")(&opt)
	assert.Equal(t, "custom_created", opt.createdAtField)

	WithUpdatedAtField("custom_updated")(&opt)
	assert.Equal(t, "custom_updated", opt.updatedAtField)

	WithDialOptions(grpc.WithDefaultCallOptions())(&opt)
	assert.Len(t, opt.dialOpts, 1)

	WithReranker(client.NewRRFReranker())(&opt)
	assert.NotNil(t, opt.reranker)
}

// TestOptionsDefaults verifies default option values
func TestOptionsDefaults(t *testing.T) {
	opt := defaultOptions

	assert.Equal(t, "trpc_agent_documents", opt.collectionName)
	assert.Equal(t, 1536, opt.dimension)
	assert.Equal(t, 10, opt.maxResults)
	assert.True(t, opt.enableHNSW)
	assert.Equal(t, 16, opt.hnswM)
	assert.Equal(t, 128, opt.hnswEfConstruction)
	assert.Equal(t, "id", opt.idField)
	assert.Equal(t, "name", opt.nameField)
	assert.Equal(t, "content", opt.contentField)
	assert.Equal(t, "vector", opt.vectorField)
	assert.Equal(t, "metadata", opt.metadataField)
	assert.Equal(t, "created_at", opt.createdAtField)
	assert.Equal(t, "updated_at", opt.updatedAtField)
}

// TestOptionIndependence verifies options don't interfere
func TestOptionIndependence(t *testing.T) {
	opt1 := defaultOptions
	opt2 := defaultOptions

	WithCollectionName("collection1")(&opt1)
	WithCollectionName("collection2")(&opt2)

	assert.Equal(t, "collection1", opt1.collectionName)
	assert.Equal(t, "collection2", opt2.collectionName)
	assert.NotEqual(t, opt1.collectionName, opt2.collectionName)
}

// TestMultipleOptions tests applying multiple options together
func TestMultipleOptions(t *testing.T) {
	opt := defaultOptions

	opts := []Option{
		WithCollectionName("test_collection"),
		WithDimension(512),
		WithMaxResults(50),
		WithAddress("localhost:19530"),
		WithUsername("admin"),
		WithPassword("password"),
	}

	for _, o := range opts {
		o(&opt)
	}

	assert.Equal(t, "test_collection", opt.collectionName)
	assert.Equal(t, 512, opt.dimension)
	assert.Equal(t, 50, opt.maxResults)
	assert.Equal(t, "localhost:19530", opt.address)
	assert.Equal(t, "admin", opt.username)
	assert.Equal(t, "password", opt.password)
}
