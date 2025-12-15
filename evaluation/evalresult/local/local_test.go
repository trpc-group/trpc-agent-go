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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
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

func TestLocalManagerLegacyLoad(t *testing.T) {
	dir := t.TempDir()
	mgr := New(evalresult.WithBaseDir(dir)).(*manager)
	id := "legacy-id"
	legacyResult := &evalresult.EvalSetResult{
		EvalSetID:       "set",
		EvalSetResultID: id,
	}
	payload, err := json.Marshal(legacyResult)
	require.NoError(t, err)
	legacyWrapper, err := json.Marshal(string(payload))
	require.NoError(t, err)

	path := mgr.evalSetResultPath("app", id)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, legacyWrapper, 0o644))

	loaded, err := mgr.Get(context.Background(), "app", id)
	require.NoError(t, err)
	assert.Equal(t, id, loaded.EvalSetResultID)
	assert.Equal(t, "set", loaded.EvalSetID)
}

func TestLocalManagerSaveUsesProvidedID(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	mgr := New(evalresult.WithBaseDir(dir)).(*manager)
	provided := &evalresult.EvalSetResult{
		EvalSetID:         "set",
		EvalSetResultID:   "custom-id",
		EvalSetResultName: "provided-name",
		CreationTimestamp: &epochtime.EpochTime{Time: time.Now()},
	}
	id, err := mgr.Save(ctx, "app", provided)
	require.NoError(t, err)
	assert.Equal(t, "custom-id", id)

	loaded, err := mgr.Get(ctx, "app", "custom-id")
	require.NoError(t, err)
	assert.Equal(t, "provided-name", loaded.EvalSetResultName)
}

func TestLocalManagerGetInvalidContent(t *testing.T) {
	dir := t.TempDir()
	mgr := New(evalresult.WithBaseDir(dir)).(*manager)
	path := mgr.evalSetResultPath("app", "bad")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("{bad json"), 0o644))

	_, err := mgr.Get(context.Background(), "app", "bad")
	assert.Error(t, err)
}

type failingLocator struct{}

func (f failingLocator) Build(baseDir, appName, evalSetResultID string) string {
	return filepath.Join(baseDir, appName, evalSetResultID+".json")
}

func (f failingLocator) List(baseDir, appName string) ([]string, error) {
	return nil, assert.AnError
}

func TestLocalManagerListLocatorError(t *testing.T) {
	dir := t.TempDir()
	mgr := New(evalresult.WithBaseDir(dir)).(*manager)
	mgr.locator = failingLocator{}

	_, err := mgr.List(context.Background(), "app")
	assert.Error(t, err)
}
