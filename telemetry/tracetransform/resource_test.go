//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Copyright The OpenTelemetry Authors
// Copyright (C) 2025 Tencent. All rights reserved.
// SPDX-License-Identifier: Apache-2.0
//

package tracetransform

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

func TestNilResource(t *testing.T) {
	assert.Empty(t, Resource(nil))
}

func TestEmptyResource(t *testing.T) {
	assert.Empty(t, Resource(&resource.Resource{}))
}

/*
* This does not include any testing on the ordering of Resource Attributes.
* They are stored as a map internally to the Resource and their order is not
* guaranteed.
 */

func TestResourceAttributes(t *testing.T) {
	attrs := []attribute.KeyValue{attribute.Int("one", 1), attribute.Int("two", 2)}

	got := Resource(resource.NewSchemaless(attrs...)).GetAttributes()
	if !assert.Len(t, attrs, 2) {
		return
	}
	assert.ElementsMatch(t, KeyValues(attrs), got)
}
