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
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

func TestLocalManager(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	manager := New(evalset.WithBaseDir(dir)).(*manager)

	results, err := manager.List(ctx, "app")
	assert.NoError(t, err)
	assert.Empty(t, results)

	_, err = manager.Get(ctx, "app", "missing")
	assert.Error(t, err)

	evalSet, err := manager.Create(ctx, "app", "set1")
	assert.NoError(t, err)
	assert.Equal(t, "set1", evalSet.EvalSetID)
	assert.FileExists(t, manager.evalSetPath("app", "set1"))

	err = manager.AddCase(ctx, "app", "set1", nil)
	assert.EqualError(t, err, "evalCase is nil")
	err = manager.AddCase(ctx, "app", "set1", &evalset.EvalCase{})
	assert.EqualError(t, err, "evalCase.EvalID is empty")

	caseInput := &evalset.EvalCase{EvalID: "case1"}
	err = manager.AddCase(ctx, "app", "set1", caseInput)
	assert.NoError(t, err)
	evalSet, err = manager.Get(ctx, "app", "set1")
	assert.NoError(t, err)
	assert.Len(t, evalSet.EvalCases, 1)

	gotCase, err := manager.GetCase(ctx, "app", "set1", "case1")
	assert.NoError(t, err)
	assert.Equal(t, "case1", gotCase.EvalID)

	update := &evalset.EvalCase{EvalID: "case1", SessionInput: &evalset.SessionInput{AppName: "updated"}}
	err = manager.UpdateCase(ctx, "app", "set1", update)
	assert.NoError(t, err)

	evalSet, err = manager.Get(ctx, "app", "set1")
	assert.NoError(t, err)
	assert.Equal(t, "updated", evalSet.EvalCases[0].SessionInput.AppName)

	err = manager.DeleteCase(ctx, "app", "set1", "missing")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))

	err = manager.DeleteCase(ctx, "app", "set1", "case1")
	assert.NoError(t, err)

	evalSet, err = manager.Get(ctx, "app", "set1")
	assert.NoError(t, err)
	assert.Empty(t, evalSet.EvalCases)

	_, err = manager.GetCase(ctx, "app", "set1", "case1")
	assert.Error(t, err)

	results, err = manager.List(ctx, "app")
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"set1"}, results)
}

func TestLocalManagerStoreValidation(t *testing.T) {
	dir := t.TempDir()
	manager := New(evalset.WithBaseDir(dir)).(*manager)
	err := manager.store("app", nil)
	assert.Error(t, err)
}
