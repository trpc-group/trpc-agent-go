//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summaryrestore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilterKeyContext(t *testing.T) {
	filterKey, ok := FilterKeyFromContext(nil)
	assert.False(t, ok)
	assert.Empty(t, filterKey)

	ctx := ContextWithFilterKey(nil, "")
	filterKey, ok = FilterKeyFromContext(ctx)
	assert.False(t, ok)
	assert.Empty(t, filterKey)

	ctx = ContextWithFilterKey(context.Background(), "app/branch")
	filterKey, ok = FilterKeyFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, "app/branch", filterKey)
}
