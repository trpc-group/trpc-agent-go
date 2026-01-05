//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package s3

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRegistryOperations(t *testing.T) {
	// Clean up after test
	defer func() {
		UnregisterS3Instance("test-instance")
		UnregisterS3Instance("another-instance")
	}()

	// Test RegisterS3Instance and GetS3Instance
	RegisterS3Instance("test-instance",
		WithBucket("my-bucket"),
		WithRegion("us-west-2"),
	)

	opts, ok := GetS3Instance("test-instance")
	assert.True(t, ok)
	assert.Len(t, opts, 2)

	// Test non-existent instance
	opts, ok = GetS3Instance("non-existent")
	assert.False(t, ok)
	assert.Nil(t, opts)

	// Test ListS3Instances
	RegisterS3Instance("another-instance", WithBucket("other-bucket"))
	instances := ListS3Instances()
	assert.Len(t, instances, 2)
	assert.Contains(t, instances, "test-instance")
	assert.Contains(t, instances, "another-instance")

	// Test UnregisterS3Instance
	UnregisterS3Instance("test-instance")
	_, ok = GetS3Instance("test-instance")
	assert.False(t, ok)

	instances = ListS3Instances()
	assert.Len(t, instances, 1)
	assert.Contains(t, instances, "another-instance")
}

func TestGetS3Instance_ReturnsCopy(t *testing.T) {
	defer UnregisterS3Instance("copy-test")

	RegisterS3Instance("copy-test", WithBucket("bucket1"))

	opts1, _ := GetS3Instance("copy-test")
	opts2, _ := GetS3Instance("copy-test")

	// Modify opts1 and ensure opts2 is not affected
	opts1[0] = WithBucket("modified")

	// Get fresh copy
	opts3, _ := GetS3Instance("copy-test")

	// Apply options and verify
	builderOpts := &ClientBuilderOpts{}
	for _, opt := range opts3 {
		opt(builderOpts)
	}
	assert.Equal(t, "bucket1", builderOpts.Bucket)

	_ = opts2 // Use opts2 to avoid unused variable warning
}
