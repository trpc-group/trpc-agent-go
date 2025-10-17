//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package local

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
)

func TestLocalManagerSaveGetList(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	mgr := New(evalresult.WithBaseDir(dir)).(*manager)

	_, err := mgr.Save(ctx, "app", nil)
	assert.EqualError(t, err, "eval set result is nil")

	_, err = mgr.Save(ctx, "app", &evalresult.EvalSetResult{})
	assert.EqualError(t, err, "eval set result id is empty")

	result := &evalresult.EvalSetResult{EvalSetID: "set"}
	id, err := mgr.Save(ctx, "app", result)
	assert.NoError(t, err)
	assert.True(t, strings.HasPrefix(id, "set_"))
	assert.FileExists(t, mgr.evalSetResultPath("app", id))

	retrieved, err := mgr.Get(ctx, "app", id)
	assert.NoError(t, err)
	assert.Equal(t, "set", retrieved.EvalSetID)

	ids, err := mgr.List(ctx, "app")
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{id}, ids)

	ids, err = mgr.List(ctx, "missing")
	assert.NoError(t, err)
	assert.Empty(t, ids)

	_, err = mgr.Get(ctx, "app", "unknown")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
}

func TestLocalManagerStoreValidation(t *testing.T) {
	dir := t.TempDir()
	mgr := New(evalresult.WithBaseDir(dir)).(*manager)

	err := mgr.store("app", nil)
	assert.EqualError(t, err, "eval set result is nil")
}
