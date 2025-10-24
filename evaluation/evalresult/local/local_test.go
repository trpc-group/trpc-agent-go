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

	var err error
	_, err = mgr.Save(ctx, "", &evalresult.EvalSetResult{EvalSetID: "set"})
	assert.Error(t, err)

	_, err = mgr.Save(ctx, "app", nil)
	assert.Error(t, err)

	_, err = mgr.Save(ctx, "app", &evalresult.EvalSetResult{})
	assert.Error(t, err)

	_, err = mgr.Get(ctx, "", "id")
	assert.Error(t, err)

	_, err = mgr.Get(ctx, "app", "")
	assert.Error(t, err)

	result := &evalresult.EvalSetResult{EvalSetID: "set"}
	var id string
	id, err = mgr.Save(ctx, "app", result)
	assert.NoError(t, err)
	assert.True(t, strings.HasPrefix(id, "app_set_"))
	assert.FileExists(t, mgr.evalSetResultPath("app", id))

	retrieved, err := mgr.Get(ctx, "app", id)
	assert.NoError(t, err)
	assert.Equal(t, "set", retrieved.EvalSetID)
	assert.Equal(t, id, retrieved.EvalSetResultName)
	assert.NotNil(t, retrieved.CreationTimestamp)

	ids, err := mgr.List(ctx, "app")
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{id}, ids)

	_, err = mgr.List(ctx, "")
	assert.Error(t, err)

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
	assert.Error(t, err)
}
