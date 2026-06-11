//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summary

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSummaryAwareRestoreFilterKeyContext(t *testing.T) {
	filterKey, ok := SummaryAwareRestoreFilterKeyFromContext(nil)
	assert.False(t, ok)
	assert.Empty(t, filterKey)

	ctx := ContextWithSummaryAwareRestoreFilterKey(nil, "")
	filterKey, ok = SummaryAwareRestoreFilterKeyFromContext(ctx)
	assert.False(t, ok)
	assert.Empty(t, filterKey)

	ctx = ContextWithSummaryAwareRestoreFilterKey(context.Background(), "app/branch")
	filterKey, ok = SummaryAwareRestoreFilterKeyFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, "app/branch", filterKey)
}
