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
	"path/filepath"
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
	assert.Error(t, err)
	err = manager.AddCase(ctx, "app", "set1", &evalset.EvalCase{})
	assert.Error(t, err)

	caseInput := &evalset.EvalCase{
		EvalID: "case1",
		Conversation: []*evalset.Invocation{
			{InvocationID: "inv1"},
		},
	}
	err = manager.AddCase(ctx, "app", "set1", caseInput)
	assert.NoError(t, err)
	err = manager.AddCase(ctx, "app", "set1", &evalset.EvalCase{EvalID: "case1"})
	assert.Error(t, err)
	evalSet, err = manager.Get(ctx, "app", "set1")
	assert.NoError(t, err)
	assert.Len(t, evalSet.EvalCases, 1)

	gotCase, err := manager.GetCase(ctx, "app", "set1", "case1")
	assert.NoError(t, err)
	assert.Equal(t, "case1", gotCase.EvalID)
	assert.NotNil(t, gotCase.CreationTimestamp)
	assert.Len(t, gotCase.Conversation, 1)
	assert.NotNil(t, gotCase.Conversation[0].CreationTimestamp)

	update := &evalset.EvalCase{EvalID: "case1", SessionInput: &evalset.SessionInput{AppName: "updated"}}
	err = manager.UpdateCase(ctx, "app", "set1", update)
	assert.NoError(t, err)

	evalSet, err = manager.Get(ctx, "app", "set1")
	assert.NoError(t, err)
	assert.Equal(t, "updated", evalSet.EvalCases[0].SessionInput.AppName)

	second := &evalset.EvalCase{EvalID: "case2", SessionInput: &evalset.SessionInput{AppName: "second"}}
	err = manager.AddCase(ctx, "app", "set1", second)
	assert.NoError(t, err)

	err = manager.DeleteCase(ctx, "app", "set1", "missing")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))

	err = manager.DeleteCase(ctx, "app", "set1", "case1")
	assert.NoError(t, err)

	remaining, err := manager.GetCase(ctx, "app", "set1", "case2")
	assert.NoError(t, err)
	assert.Equal(t, "case2", remaining.EvalID)

	evalSet, err = manager.Get(ctx, "app", "set1")
	assert.NoError(t, err)
	assert.Len(t, evalSet.EvalCases, 1)

	_, err = manager.GetCase(ctx, "app", "set1", "case1")
	assert.Error(t, err)

	err = manager.DeleteCase(ctx, "app", "set1", "case2")
	assert.NoError(t, err)

	evalSet, err = manager.Get(ctx, "app", "set1")
	assert.NoError(t, err)
	assert.Empty(t, evalSet.EvalCases)

	_, err = manager.Create(ctx, "app", "set1")
	assert.Error(t, err)

	results, err = manager.List(ctx, "app")
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"set1"}, results)
}

func TestLocalManagerUpdateValidation(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	manager := New(evalset.WithBaseDir(dir)).(*manager)

	err := manager.UpdateCase(ctx, "app", "set1", nil)
	assert.Error(t, err)

	err = manager.UpdateCase(ctx, "app", "set1", &evalset.EvalCase{})
	assert.Error(t, err)

	_, err = manager.Create(ctx, "app", "set1")
	assert.NoError(t, err)

	err = manager.UpdateCase(ctx, "app", "set1", &evalset.EvalCase{EvalID: "missing"})
	assert.Error(t, err)
}

func TestLocalManagerAddCaseValidation(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	manager := New(evalset.WithBaseDir(dir)).(*manager)

	err := manager.AddCase(ctx, "app", "missing", &evalset.EvalCase{EvalID: "case"})
	assert.Error(t, err)

	_, err = manager.Create(ctx, "app", "set1")
	assert.NoError(t, err)

	err = manager.AddCase(ctx, "app", "set1", nil)
	assert.Error(t, err)

	err = manager.AddCase(ctx, "app", "set1", &evalset.EvalCase{})
	assert.Error(t, err)
}

func TestLocalManagerLoadInvalidData(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	manager := New(evalset.WithBaseDir(dir)).(*manager)

	path := manager.evalSetPath("app", "broken")
	err := os.MkdirAll(filepath.Dir(path), 0o755)
	assert.NoError(t, err)
	err = os.WriteFile(path, []byte("invalid-json"), 0o644)
	assert.NoError(t, err)

	_, err = manager.Get(ctx, "app", "broken")
	assert.Error(t, err)
}

func TestLocalManagerStoreValidation(t *testing.T) {
	dir := t.TempDir()
	manager := New(evalset.WithBaseDir(dir)).(*manager)
	err := manager.store("app", nil)
	assert.Error(t, err)
}

func TestLocalManagerDeleteEvalSet(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	manager := New(evalset.WithBaseDir(dir)).(*manager)

	_, err := manager.Create(ctx, "app", "set1")
	assert.NoError(t, err)
	assert.FileExists(t, manager.evalSetPath("app", "set1"))

	err = manager.Delete(ctx, "app", "set1")
	assert.NoError(t, err)
	assert.NoFileExists(t, manager.evalSetPath("app", "set1"))

	_, err = manager.Get(ctx, "app", "set1")
	assert.Error(t, err)
}

type failingLocator struct {
}

func (f failingLocator) Build(baseDir, appName, evalSetID string) string {
	return filepath.Join(baseDir, appName, evalSetID+".evalset.json")
}

func (f failingLocator) List(baseDir, appName string) ([]string, error) {
	return nil, errors.New("locator error")
}

func TestLocalManagerListError(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	manager := New(evalset.WithBaseDir(dir), evalset.WithLocator(failingLocator{})).(*manager)

	_, err := manager.List(ctx, "app")
	assert.Error(t, err)
}

func TestLocalManagerLoadFailures(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	manager := New(evalset.WithBaseDir(dir)).(*manager)

	_, err := manager.Create(ctx, "app", "set1")
	assert.NoError(t, err)

	err = manager.AddCase(ctx, "app", "set1", &evalset.EvalCase{EvalID: "case1"})
	assert.NoError(t, err)

	path := manager.evalSetPath("app", "set1")
	err = os.WriteFile(path, []byte("not-json"), 0o644)
	assert.NoError(t, err)

	_, err = manager.GetCase(ctx, "app", "set1", "case1")
	assert.Error(t, err)

	err = manager.UpdateCase(ctx, "app", "set1", &evalset.EvalCase{EvalID: "case1"})
	assert.Error(t, err)

	err = manager.DeleteCase(ctx, "app", "set1", "case1")
	assert.Error(t, err)
}

func TestLocalManagerLoadNilEvalCases(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	manager := New(evalset.WithBaseDir(dir)).(*manager)

	path := manager.evalSetPath("app", "empty")
	err := os.MkdirAll(filepath.Dir(path), 0o755)
	assert.NoError(t, err)

	payload := `{
  "evalSetId": "empty",
  "name": "empty",
  "description": "nil cases",
  "evalCases": null,
  "creationTimestamp": 0
}`
	err = os.WriteFile(path, []byte(payload), 0o644)
	assert.NoError(t, err)

	evalSet, err := manager.Get(ctx, "app", "empty")
	assert.NoError(t, err)
	assert.NotNil(t, evalSet.EvalCases)
	assert.Empty(t, evalSet.EvalCases)
}

func TestLocalManagerEmptyInputs(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	manager := New(evalset.WithBaseDir(dir)).(*manager)

	_, err := manager.Get(ctx, "", "set")
	assert.Error(t, err)

	_, err = manager.Get(ctx, "app", "")
	assert.Error(t, err)

	_, err = manager.Create(ctx, "", "set")
	assert.Error(t, err)

	_, err = manager.Create(ctx, "app", "")
	assert.Error(t, err)

	_, err = manager.List(ctx, "")
	assert.Error(t, err)

	err = manager.Delete(ctx, "", "set")
	assert.Error(t, err)

	err = manager.Delete(ctx, "app", "")
	assert.Error(t, err)

	_, err = manager.GetCase(ctx, "", "set", "case")
	assert.Error(t, err)

	_, err = manager.GetCase(ctx, "app", "", "case")
	assert.Error(t, err)

	_, err = manager.GetCase(ctx, "app", "set", "")
	assert.Error(t, err)

	err = manager.AddCase(ctx, "", "set", &evalset.EvalCase{EvalID: "case"})
	assert.Error(t, err)

	err = manager.AddCase(ctx, "app", "", &evalset.EvalCase{EvalID: "case"})
	assert.Error(t, err)

	err = manager.AddCase(ctx, "app", "set", &evalset.EvalCase{})
	assert.Error(t, err)

	err = manager.UpdateCase(ctx, "", "set", &evalset.EvalCase{EvalID: "case"})
	assert.Error(t, err)

	err = manager.UpdateCase(ctx, "app", "", &evalset.EvalCase{EvalID: "case"})
	assert.Error(t, err)

	err = manager.UpdateCase(ctx, "app", "set", nil)
	assert.Error(t, err)

	err = manager.DeleteCase(ctx, "", "set", "case")
	assert.Error(t, err)

	err = manager.DeleteCase(ctx, "app", "", "case")
	assert.Error(t, err)

	err = manager.DeleteCase(ctx, "app", "set", "")
	assert.Error(t, err)
}
