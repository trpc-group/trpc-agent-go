//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package local

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/usersimulation"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// TestExpectedDriverCaseOptions protects role isolation and context seeding
// when the expected runner generates the conversation before the actual runner.
func TestExpectedDriverCaseOptions(t *testing.T) {
	actual := &fakeRunner{events: []*event.Event{makeFinalEvent("actual")}}
	expected := &fakeRunner{events: []*event.Event{makeFinalEvent("expected")}}
	conversation := &scenarioTestConversation{decisions: []*usersimulation.Decision{
		{Message: &model.Message{Role: model.RoleUser, Content: "hello"}}, {Stop: true},
	}}
	callbacks := service.NewCallbacks().RegisterBeforeInferenceCase("actual-only", func(ctx context.Context, args *service.BeforeInferenceCaseArgs) (*service.BeforeInferenceCaseResult, error) {
		// Replacing an existing entry must not mutate the service's option slice.
		args.RunOptions[0] = agent.WithRequestID(args.EvalCaseID)
		return nil, nil
	})
	opts := &service.Options{
		SessionIDSupplier: func(context.Context) string { return "session" },
		RunOptions:        []agent.RunOption{agent.WithRequestID("shared")},
		ExpectedRunner:    expected,
		UserSimulator:     &scenarioTestSimulator{conversation: conversation},
		Callbacks:         callbacks,
	}
	c := makeScenarioEvalCase("app", "case")
	c.ConversationScenario.Driver = evalset.ConversationScenarioDriverExpected
	c.ExpectedRunnerEnabled = true
	c.ContextMessages = []*model.Message{{Role: model.RoleSystem, Content: "seed"}}
	result := (&local{runner: actual}).inferenceEvalCase(context.Background(), &service.InferenceRequest{AppName: "app", EvalSetID: "set"}, c, opts)
	require.Equal(t, status.EvalStatusPassed, result.Status, result.ErrorMessage)
	require.Len(t, result.Inferences, 1)
	require.Len(t, result.ExpectedInferences, 1)
	require.True(t, conversation.closed)
	require.Equal(t, "case", actual.runOptions.RequestID)
	require.Equal(t, "shared", expected.runOptions.RequestID)
	require.Equal(t, "shared", agent.NewRunOptions(opts.RunOptions...).RequestID)
	seed := []model.Message{{Role: model.RoleSystem, Content: "seed"}}
	require.Equal(t, seed, actual.runOptions.InjectedContextMessages)
	require.Equal(t, seed, expected.runOptions.InjectedContextMessages)
	require.Equal(t, []string{"session"}, actual.sessionIDs)
	require.Equal(t, []string{"session-expected"}, expected.sessionIDs)
}
