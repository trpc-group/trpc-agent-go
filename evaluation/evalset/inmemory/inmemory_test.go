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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/epochtime"
)

func TestManager(t *testing.T) {
	ctx := context.Background()
	mgr := New().(*manager)

	ids, err := mgr.List(ctx, "app")
	assert.NoError(t, err)
	assert.Empty(t, ids)

	created, err := mgr.Create(ctx, "app", "set1")
	assert.NoError(t, err)
	assert.Equal(t, "set1", created.EvalSetID)
	assert.Equal(t, "set1", created.Name)
	created.Name = "mutated"

	ids, err = mgr.List(ctx, "app")
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"set1"}, ids)

	loaded, err := mgr.Get(ctx, "app", "set1")
	assert.NoError(t, err)
	assert.Equal(t, "set1", loaded.Name)
	loaded.Name = "mutation"
	loaded.EvalCases = append(loaded.EvalCases, &evalset.EvalCase{EvalID: "temp"})

	refreshed, err := mgr.Get(ctx, "app", "set1")
	assert.NoError(t, err)
	assert.Equal(t, "set1", refreshed.Name)
	assert.Empty(t, refreshed.EvalCases)

	caseInput := &evalset.EvalCase{
		EvalID: "case1",
		SessionInput: &evalset.SessionInput{
			AppName: "app",
			UserID:  "user1",
			State:   map[string]interface{}{"premium": true},
		},
		Conversation: []*evalset.Invocation{
			{InvocationID: "inv1"},
		},
		CreationTimestamp: &epochtime.EpochTime{Time: time.Unix(1700, 0).UTC()},
	}
	err = mgr.AddCase(ctx, "app", "set1", caseInput)
	assert.NoError(t, err)

	err = mgr.AddCase(ctx, "app", "set1", caseInput)
	assert.Error(t, err)

	caseInput.SessionInput.AppName = "changed"

	storedCase, err := mgr.GetCase(ctx, "app", "set1", "case1")
	assert.NoError(t, err)
	assert.Equal(t, "app", storedCase.SessionInput.AppName)
	storedCase.SessionInput.AppName = "local-mutation"

	refetchedCase, err := mgr.GetCase(ctx, "app", "set1", "case1")
	assert.NoError(t, err)
	assert.Equal(t, "app", refetchedCase.SessionInput.AppName)
	assert.Len(t, refetchedCase.Conversation, 1)

	update := &evalset.EvalCase{
		EvalID: "case1",
		SessionInput: &evalset.SessionInput{
			AppName: "app-updated",
			State:   map[string]interface{}{"level": 2},
		},
		Conversation: []*evalset.Invocation{
			{InvocationID: "inv1"},
			{InvocationID: "inv2"},
		},
	}
	err = mgr.UpdateCase(ctx, "app", "set1", update)
	assert.NoError(t, err)

	update.SessionInput.AppName = "mutated-after-update"

	updatedCase, err := mgr.GetCase(ctx, "app", "set1", "case1")
	assert.NoError(t, err)
	assert.Equal(t, "app-updated", updatedCase.SessionInput.AppName)
	assert.Equal(t, map[string]interface{}{"level": 2}, updatedCase.SessionInput.State)
	assert.Len(t, updatedCase.Conversation, 2)

	evalSetAfterUpdate, err := mgr.Get(ctx, "app", "set1")
	assert.NoError(t, err)
	assert.Len(t, evalSetAfterUpdate.EvalCases, 1)
	assert.Len(t, evalSetAfterUpdate.EvalCases[0].Conversation, 2)

	secondCase := &evalset.EvalCase{
		EvalID: "case2",
		SessionInput: &evalset.SessionInput{
			AppName: "app",
			State:   map[string]interface{}{"role": "tester"},
		},
	}
	err = mgr.AddCase(ctx, "app", "set1", secondCase)
	assert.NoError(t, err)

	evalSetWithTwoCases, err := mgr.Get(ctx, "app", "set1")
	assert.NoError(t, err)
	assert.Len(t, evalSetWithTwoCases.EvalCases, 2)

	err = mgr.DeleteCase(ctx, "app", "set1", "case1")
	assert.NoError(t, err)

	_, err = mgr.GetCase(ctx, "app", "set1", "case1")
	assert.Error(t, err)

	remainingCase, err := mgr.GetCase(ctx, "app", "set1", "case2")
	assert.NoError(t, err)
	assert.Equal(t, "case2", remainingCase.EvalID)

	err = mgr.DeleteCase(ctx, "app", "set1", "case1")
	assert.Error(t, err)

	err = mgr.DeleteCase(ctx, "app", "set1", "case2")
	assert.NoError(t, err)

	evalSetAfterDelete, err := mgr.Get(ctx, "app", "set1")
	assert.NoError(t, err)
	assert.Empty(t, evalSetAfterDelete.EvalCases)

	err = mgr.UpdateCase(ctx, "app", "set1", &evalset.EvalCase{EvalID: "missing"})
	assert.Error(t, err)

	_, err = mgr.Create(ctx, "app", "set1")
	assert.Error(t, err)

	_, err = mgr.Get(ctx, "app", "missing")
	assert.Error(t, err)

	err = mgr.Delete(ctx, "", "set1")
	assert.Error(t, err)

	err = mgr.Delete(ctx, "app", "")
	assert.Error(t, err)

	err = mgr.Delete(ctx, "app", "set1")
	assert.NoError(t, err)

	_, err = mgr.Get(ctx, "app", "set1")
	assert.Error(t, err)
}

func TestManagerValidationAndErrors(t *testing.T) {

	ctx := context.Background()
	mgr := New().(*manager)

	_, err := mgr.Get(ctx, "ghost", "set")
	assert.Error(t, err)

	err = mgr.AddCase(ctx, "ghost", "set", nil)
	assert.Error(t, err)

	err = mgr.AddCase(ctx, "ghost", "set", &evalset.EvalCase{})
	assert.Error(t, err)

	err = mgr.AddCase(ctx, "ghost", "set", &evalset.EvalCase{EvalID: "case"})
	assert.Error(t, err)

	created, err := mgr.Create(ctx, "ghost", "set")
	assert.NoError(t, err)
	assert.Equal(t, "set", created.EvalSetID)

	_, err = mgr.Create(ctx, "ghost", "set")
	assert.Error(t, err)

	_, err = mgr.Get(ctx, "ghost", "missing")
	assert.Error(t, err)

	_, err = mgr.GetCase(ctx, "ghost", "missing", "case")
	assert.Error(t, err)

	_, err = mgr.GetCase(ctx, "ghost", "set", "case")
	assert.Error(t, err)

	err = mgr.UpdateCase(ctx, "ghost", "set", &evalset.EvalCase{EvalID: "case"})
	assert.Error(t, err)

	err = mgr.DeleteCase(ctx, "ghost", "set", "case")
	assert.Error(t, err)

	err = mgr.AddCase(ctx, "ghost", "set", &evalset.EvalCase{EvalID: "case"})
	assert.NoError(t, err)

	err = mgr.UpdateCase(ctx, "ghost", "set", &evalset.EvalCase{
		EvalID: "case",
		SessionInput: &evalset.SessionInput{
			State: map[string]interface{}{"k": "v"},
		},
	})
	assert.NoError(t, err)

	err = mgr.AddCase(ctx, "ghost", "set", &evalset.EvalCase{EvalID: "case"})
	assert.Error(t, err)

	err = mgr.DeleteCase(ctx, "ghost", "set", "case")
	assert.NoError(t, err)

	_, err = mgr.GetCase(ctx, "ghost", "set", "case")
	assert.Error(t, err)

	err = mgr.UpdateCase(ctx, "ghost", "set", &evalset.EvalCase{EvalID: "case"})
	assert.Error(t, err)

	err = mgr.DeleteCase(ctx, "ghost", "set", "case")
	assert.Error(t, err)

	err = mgr.UpdateCase(ctx, "ghost", "set", nil)
	assert.Error(t, err)

	err = mgr.UpdateCase(ctx, "ghost", "set", &evalset.EvalCase{})
	assert.Error(t, err)

	err = mgr.DeleteCase(ctx, "ghost", "missing-set", "case")
	assert.Error(t, err)

	err = mgr.UpdateCase(ctx, "ghost", "missing-set", &evalset.EvalCase{EvalID: "case"})
	assert.Error(t, err)

	_, err = mgr.GetCase(ctx, "ghost", "missing-set", "case")
	assert.Error(t, err)

	newMgr := New().(*manager)
	_, err = newMgr.GetCase(ctx, "phantom", "set", "case")
	assert.Error(t, err)
}
