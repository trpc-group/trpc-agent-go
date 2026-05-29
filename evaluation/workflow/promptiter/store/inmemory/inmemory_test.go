//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inmemory

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	promptiter "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestInMemoryStoreCreateGetUpdate(t *testing.T) {
	ctx := context.Background()
	store := New().(*inMemoryStore)
	t.Cleanup(func() {
		assert.NoError(t, store.Close())
	})
	run := &engine.RunResult{
		AppName: "demo-app",
		ID:      "run-1",
		Status:  engine.RunStatusQueued,
		AcceptedProfile: &promptiter.Profile{
			StructureID: "structure-1",
			Overrides: []promptiter.SurfaceOverride{
				{SurfaceID: "candidate#instruction"},
			},
		},
		Rounds: []engine.RoundResult{
			{
				Round: 1,
				Acceptance: &engine.AcceptanceDecision{
					Accepted: true,
					Reason:   "accepted",
				},
			},
		},
	}
	require.NoError(t, store.Create(ctx, "demo-app", run))
	run.Status = engine.RunStatusFailed
	run.AcceptedProfile.StructureID = "mutated"
	run.Rounds[0].Acceptance.Reason = "mutated"
	loaded, err := store.Get(ctx, "demo-app", "run-1")
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, "demo-app", loaded.AppName)
	assert.Equal(t, engine.RunStatusQueued, loaded.Status)
	require.NotNil(t, loaded.AcceptedProfile)
	assert.Equal(t, "structure-1", loaded.AcceptedProfile.StructureID)
	require.Len(t, loaded.Rounds, 1)
	require.NotNil(t, loaded.Rounds[0].Acceptance)
	assert.Equal(t, "accepted", loaded.Rounds[0].Acceptance.Reason)
	loaded.Status = engine.RunStatusSucceeded
	loaded.AcceptedProfile.StructureID = "loaded-mutated"
	require.NoError(t, store.Update(ctx, "demo-app", loaded))
	loadedAgain, err := store.Get(ctx, "demo-app", "run-1")
	require.NoError(t, err)
	require.NotNil(t, loadedAgain)
	assert.Equal(t, engine.RunStatusSucceeded, loadedAgain.Status)
	require.NotNil(t, loadedAgain.AcceptedProfile)
	assert.Equal(t, "loaded-mutated", loadedAgain.AcceptedProfile.StructureID)
	loadedAgain.AcceptedProfile.StructureID = "second-mutation"
	loadedOnceMore, err := store.Get(ctx, "demo-app", "run-1")
	require.NoError(t, err)
	require.NotNil(t, loadedOnceMore)
	require.NotNil(t, loadedOnceMore.AcceptedProfile)
	assert.Equal(t, "loaded-mutated", loadedOnceMore.AcceptedProfile.StructureID)
}

func TestInMemoryStoreValidationAndNotFoundErrors(t *testing.T) {
	ctx := context.Background()
	store := New().(*inMemoryStore)
	t.Cleanup(func() {
		assert.NoError(t, store.Close())
	})
	assert.EqualError(t, store.Create(ctx, "demo-app", nil), "promptiter run is nil")
	assert.EqualError(t, store.Create(ctx, "", &engine.RunResult{ID: "run-1"}), "promptiter run app name is empty")
	assert.EqualError(t, store.Create(ctx, "demo-app", &engine.RunResult{}), "promptiter run id is empty")
	assert.EqualError(t, store.Create(ctx, "demo-app", &engine.RunResult{AppName: "other-app", ID: "run-1"}), `promptiter run app name "other-app" does not match "demo-app"`)
}

func TestInMemoryStoreGetMissingRun(t *testing.T) {
	ctx := context.Background()
	store := New().(*inMemoryStore)
	t.Cleanup(func() {
		assert.NoError(t, store.Close())
	})
	run, err := store.Get(ctx, "demo-app", "missing")
	assert.Nil(t, run)
	assert.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestInMemoryStoreCreateDuplicateAndUpdateMissing(t *testing.T) {
	ctx := context.Background()
	store := New().(*inMemoryStore)
	t.Cleanup(func() {
		assert.NoError(t, store.Close())
	})
	run := &engine.RunResult{AppName: "demo-app", ID: "run-1", Status: engine.RunStatusQueued}
	require.NoError(t, store.Create(ctx, "demo-app", run))
	err := store.Create(ctx, "demo-app", run)
	assert.EqualError(t, err, `run "run-1" for app "demo-app" already exists`)
	err = store.Update(ctx, "demo-app", nil)
	assert.EqualError(t, err, "promptiter run is nil")
	err = store.Update(ctx, "", &engine.RunResult{ID: "run-1"})
	assert.EqualError(t, err, "promptiter run app name is empty")
	err = store.Update(ctx, "demo-app", &engine.RunResult{})
	assert.EqualError(t, err, "promptiter run id is empty")
	err = store.Update(ctx, "demo-app", &engine.RunResult{AppName: "other-app", ID: "run-1"})
	assert.EqualError(t, err, `promptiter run app name "other-app" does not match "demo-app"`)
	err = store.Update(ctx, "demo-app", &engine.RunResult{AppName: "demo-app", ID: "missing", Status: engine.RunStatusQueued})
	assert.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestInMemoryStoreIsolatesRunsByAppName(t *testing.T) {
	ctx := context.Background()
	store := New().(*inMemoryStore)
	t.Cleanup(func() {
		assert.NoError(t, store.Close())
	})
	require.NoError(t, store.Create(ctx, "app-a", &engine.RunResult{
		AppName: "app-a",
		ID:      "run-1",
		Status:  engine.RunStatusQueued,
	}))
	require.NoError(t, store.Create(ctx, "app-b", &engine.RunResult{
		AppName: "app-b",
		ID:      "run-1",
		Status:  engine.RunStatusSucceeded,
	}))
	appA, err := store.Get(ctx, "app-a", "run-1")
	require.NoError(t, err)
	appB, err := store.Get(ctx, "app-b", "run-1")
	require.NoError(t, err)
	assert.Equal(t, engine.RunStatusQueued, appA.Status)
	assert.Equal(t, engine.RunStatusSucceeded, appB.Status)
}

func TestInMemoryStoreCloneErrors(t *testing.T) {
	ctx := context.Background()
	store := New().(*inMemoryStore)
	t.Cleanup(func() {
		assert.NoError(t, store.Close())
	})
	badRun := &engine.RunResult{
		AppName: "demo-app",
		ID:      "run-1",
		BaselineValidation: &engine.EvaluationResult{
			OverallScore: math.NaN(),
		},
	}
	err := store.Create(ctx, "demo-app", badRun)
	assert.ErrorContains(t, err, "clone promptiter run")
	store.runs["demo-app"] = map[string]*engine.RunResult{"run-1": badRun}
	loaded, err := store.Get(ctx, "demo-app", "run-1")
	assert.Nil(t, loaded)
	assert.ErrorContains(t, err, "clone promptiter run")
	err = store.Update(ctx, "demo-app", badRun)
	assert.ErrorContains(t, err, "clone promptiter run")
}

func TestCloneRunNil(t *testing.T) {
	cloned, err := cloneRun(nil)
	assert.NoError(t, err)
	assert.Nil(t, cloned)
}
