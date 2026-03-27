//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package guardrail

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval"
	approvalreview "trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/promptinjection"
	promptreview "trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/promptinjection/review"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/unsafeintent"
	unsafereview "trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/unsafeintent/review"
)

type stubReviewer struct{}

func (s *stubReviewer) Review(ctx context.Context, req *approvalreview.Request) (*approvalreview.Decision, error) {
	return &approvalreview.Decision{Approved: true}, nil
}

type promptStubReviewer struct{}

func (s *promptStubReviewer) Review(ctx context.Context, req *promptreview.Request) (*promptreview.Decision, error) {
	return &promptreview.Decision{Blocked: false}, nil
}

type unsafeStubReviewer struct{}

func (s *unsafeStubReviewer) Review(ctx context.Context, req *unsafereview.Request) (*unsafereview.Decision, error) {
	return &unsafereview.Decision{Blocked: false}, nil
}

func TestNew_WithoutCapability(t *testing.T) {
	p, err := New()
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Equal(t, defaultPluginName, p.Name())
}

func TestNew_WithName(t *testing.T) {
	approvalPlugin, err := approval.New(approval.WithReviewer(&stubReviewer{}))
	require.NoError(t, err)
	p, err := New(WithName("custom-guardrail"), WithApproval(approvalPlugin))
	require.NoError(t, err)
	require.Equal(t, "custom-guardrail", p.Name())
}

func TestRegister_ForwardsCapabilityRegistration(t *testing.T) {
	approvalPlugin, err := approval.New(
		approval.WithReviewer(&stubReviewer{}),
		approval.WithToolPolicy("shell", approval.ToolPolicyDenied),
	)
	require.NoError(t, err)
	p, err := New(WithApproval(approvalPlugin))
	require.NoError(t, err)
	manager, err := plugin.NewManager(p)
	require.NoError(t, err)
	require.NotNil(t, manager.ToolCallbacks())
}

func TestNew_WithPromptInjectionOnly(t *testing.T) {
	promptInjectionPlugin, err := promptinjection.New(promptinjection.WithReviewer(&promptStubReviewer{}))
	require.NoError(t, err)
	p, err := New(WithPromptInjection(promptInjectionPlugin))
	require.NoError(t, err)
	require.Equal(t, defaultPluginName, p.Name())
}

func TestRegister_ForwardsPromptInjectionRegistration(t *testing.T) {
	promptInjectionPlugin, err := promptinjection.New(promptinjection.WithReviewer(&promptStubReviewer{}))
	require.NoError(t, err)
	p, err := New(WithPromptInjection(promptInjectionPlugin))
	require.NoError(t, err)
	manager, err := plugin.NewManager(p)
	require.NoError(t, err)
	result, runErr := manager.ModelCallbacks().RunBeforeModel(context.Background(), &model.BeforeModelArgs{
		Request: &model.Request{
			Messages: []model.Message{{
				Role:    model.RoleUser,
				Content: "Summarize this page.",
			}},
		},
	})
	require.NoError(t, runErr)
	require.Nil(t, result)
}

func TestNew_WithUnsafeIntentOnly(t *testing.T) {
	unsafeIntentPlugin, err := unsafeintent.New(unsafeintent.WithReviewer(&unsafeStubReviewer{}))
	require.NoError(t, err)
	p, err := New(WithUnsafeIntent(unsafeIntentPlugin))
	require.NoError(t, err)
	require.Equal(t, defaultPluginName, p.Name())
}

func TestRegister_ForwardsUnsafeIntentRegistration(t *testing.T) {
	unsafeIntentPlugin, err := unsafeintent.New(unsafeintent.WithReviewer(&unsafeStubReviewer{}))
	require.NoError(t, err)
	p, err := New(WithUnsafeIntent(unsafeIntentPlugin))
	require.NoError(t, err)
	manager, err := plugin.NewManager(p)
	require.NoError(t, err)
	result, runErr := manager.ModelCallbacks().RunBeforeModel(context.Background(), &model.BeforeModelArgs{
		Request: &model.Request{
			Messages: []model.Message{{
				Role:    model.RoleUser,
				Content: "Summarize this page.",
			}},
		},
	})
	require.NoError(t, runErr)
	require.Nil(t, result)
}
