//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
)

func TestManagerSaveGetList(t *testing.T) {
	ctx := context.Background()
	mgr := New().(*manager)

	_, err := mgr.Save(ctx, "", &evalresult.EvalSetResult{EvalSetID: "set"})
	assert.EqualError(t, err, "app name is empty")

	_, err = mgr.Save(ctx, "app", nil)
	assert.EqualError(t, err, "eval set result is nil")

	_, err = mgr.Save(ctx, "app", &evalresult.EvalSetResult{})
	assert.EqualError(t, err, "eval set id is empty")

	id, err := mgr.Save(ctx, "app", &evalresult.EvalSetResult{EvalSetID: "set"})
	assert.NoError(t, err)
	assert.True(t, strings.HasPrefix(id, "set_"))

	// Ensure value stored under generated ID.
	stored := mgr.evalSetResults["app"][id]
	assert.Equal(t, id, stored.EvalSetResultID)

	// Subsequent Save with explicit ID should override that entry.
	withID := &evalresult.EvalSetResult{
		EvalSetResultID: "manual-id",
		EvalSetID:       "set",
	}
	explicitID, err := mgr.Save(ctx, "app", withID)
	assert.NoError(t, err)
	assert.Equal(t, "manual-id", explicitID)
	assert.Equal(t, explicitID, mgr.evalSetResults["app"][explicitID].EvalSetResultID)

	// Get returns a clone.
	result, err := mgr.Get(ctx, "app", explicitID)
	assert.NoError(t, err)
	assert.NotSame(t, result, mgr.evalSetResults["app"][explicitID])
	result.EvalSetResultName = "mutated"
	fresh, err := mgr.Get(ctx, "app", explicitID)
	assert.NoError(t, err)
	assert.Empty(t, fresh.EvalSetResultName)

	ids, err := mgr.List(ctx, "app")
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{id, explicitID}, ids)

	ids, err = mgr.List(ctx, "missing")
	assert.NoError(t, err)
	assert.Empty(t, ids)
}

func TestManagerGetErrors(t *testing.T) {
	ctx := context.Background()
	mgr := New().(*manager)

	_, err := mgr.Get(ctx, "app", "unknown")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))

	_, err = mgr.Save(ctx, "app", &evalresult.EvalSetResult{EvalSetID: "set"})
	assert.NoError(t, err)

	_, err = mgr.Get(ctx, "app", "missing")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
}
