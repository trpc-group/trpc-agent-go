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
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestLocalInferenceTraceModeRejectsMismatchedConversationLength(t *testing.T) {
	ctx := context.Background()
	appName := "trace-app"
	evalSetID := "trace-set"
	caseID := "case-trace"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	traceCase := &evalset.EvalCase{
		EvalID:   caseID,
		EvalMode: evalset.EvalModeTrace,
		Conversation: []*evalset.Invocation{
			makeInvocation("exp-1", "prompt-1"),
			makeInvocation("exp-2", "prompt-2"),
		},
		ActualConversation: []*evalset.Invocation{
			makeActualInvocation("act-1", "prompt-1", "answer-1"),
		},
		SessionInput: &evalset.SessionInput{AppName: appName, UserID: "demo-user", State: map[string]any{}},
	}
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, traceCase))

	runnerStub := &fakeRunner{err: errors.New("runner should not be called in trace mode")}
	reg := registry.New()
	svc := newLocalService(t, runnerStub, mgr, reg, "session-trace")

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, status.EvalStatusFailed, results[0].Status)
	assert.Contains(t, results[0].ErrorMessage, "actual conversation length")
	assert.Contains(t, results[0].ErrorMessage, "does not match conversation length")

	runnerStub.mu.Lock()
	callCount := len(runnerStub.calls)
	runnerStub.mu.Unlock()
	assert.Zero(t, callCount)
}

func TestLocalInferenceTraceModeRejectsNilActualInvocation(t *testing.T) {
	ctx := context.Background()
	appName := "trace-app"
	evalSetID := "trace-set"
	baseDir := t.TempDir()

	path := filepath.Join(baseDir, appName, evalSetID+".evalset.json")
	assert.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NoError(t, os.WriteFile(path, []byte(`{
  "evalSetId": "trace-set",
  "name": "trace-set",
  "evalCases": [
    {
      "evalId": "case-trace",
      "evalMode": "trace",
      "actualConversation": [
        null
      ],
      "sessionInput": {
        "appName": "trace-app",
        "userId": "demo-user"
      }
    }
  ]
}`), 0o644))

	runnerStub := &fakeRunner{err: errors.New("runner should not be called in trace mode")}
	reg := registry.New()
	mgr := evalsetlocal.New(evalset.WithBaseDir(baseDir))
	svc := newLocalService(t, runnerStub, mgr, reg, "session-trace")

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, status.EvalStatusFailed, results[0].Status)
	assert.Contains(t, results[0].ErrorMessage, "actual invocation is nil at index 0")
}

func TestLocalInferenceTraceModeRejectsNilUserContentInActualInvocation(t *testing.T) {
	ctx := context.Background()
	appName := "trace-app"
	evalSetID := "trace-set"
	caseID := "case-trace"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	traceCase := &evalset.EvalCase{
		EvalID:   caseID,
		EvalMode: evalset.EvalModeTrace,
		ActualConversation: []*evalset.Invocation{
			{
				InvocationID:  "act-1",
				UserContent:   nil,
				FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "answer"},
			},
		},
		SessionInput: &evalset.SessionInput{AppName: appName, UserID: "demo-user", State: map[string]any{}},
	}
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, traceCase))

	runnerStub := &fakeRunner{err: errors.New("runner should not be called in trace mode")}
	reg := registry.New()
	svc := newLocalService(t, runnerStub, mgr, reg, "session-trace")

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, status.EvalStatusFailed, results[0].Status)
	assert.Contains(t, results[0].ErrorMessage, "actual invocation user content is nil")
}

func TestLocalInferenceTraceModeRejectsEmptyInvocationsWhenBothConversationsEmpty(t *testing.T) {
	ctx := context.Background()
	appName := "trace-app"
	evalSetID := "trace-set"
	caseID := "case-trace"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	traceCase := &evalset.EvalCase{
		EvalID:       caseID,
		EvalMode:     evalset.EvalModeTrace,
		Conversation: []*evalset.Invocation{},
		SessionInput: &evalset.SessionInput{AppName: appName, UserID: "demo-user", State: map[string]any{}},
	}
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, traceCase))

	runnerStub := &fakeRunner{err: errors.New("runner should not be called in trace mode")}
	reg := registry.New()
	svc := newLocalService(t, runnerStub, mgr, reg, "session-trace")

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, status.EvalStatusFailed, results[0].Status)
	assert.Contains(t, results[0].ErrorMessage, "invocations are empty")
}

func TestBuildExpectedsForEvalTraceModeRejectsEmptyConversations(t *testing.T) {
	expecteds, err := buildExpectedsForEval(&evalset.EvalCase{EvalMode: evalset.EvalModeTrace})
	assert.Error(t, err)
	assert.Nil(t, expecteds)
}

func TestBuildExpectedsForEvalDefaultModeRejectsEmptyConversation(t *testing.T) {
	expecteds, err := buildExpectedsForEval(&evalset.EvalCase{EvalMode: evalset.EvalModeDefault})
	assert.Error(t, err)
	assert.Nil(t, expecteds)
}

func TestTraceExpectedsForEvalPreservesUserContentAndHandlesNilInvocation(t *testing.T) {
	user := &model.Message{Role: model.RoleUser, Content: "prompt"}
	expecteds := traceExpectedsForEval([]*evalset.Invocation{
		{InvocationID: "inv-1", UserContent: user, FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "answer"}},
		nil,
	})
	assert.Len(t, expecteds, 2)
	assert.NotNil(t, expecteds[0])
	assert.Equal(t, "inv-1", expecteds[0].InvocationID)
	assert.Same(t, user, expecteds[0].UserContent)
	assert.Nil(t, expecteds[0].FinalResponse)
	assert.NotNil(t, expecteds[1])
	assert.Empty(t, expecteds[1].InvocationID)
	assert.Nil(t, expecteds[1].UserContent)
	assert.Nil(t, expecteds[1].FinalResponse)
}
