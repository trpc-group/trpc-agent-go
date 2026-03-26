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
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval"
	approvalreview "trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
)

type stubReviewer struct{}

func (s *stubReviewer) Review(ctx context.Context, req *approvalreview.Request) (*approvalreview.Decision, error) {
	return &approvalreview.Decision{Approved: true}, nil
}

func TestNew_RequiresCapability(t *testing.T) {
	_, err := New()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no guardrail capability configured")
}

func TestNew_WithName(t *testing.T) {
	approvalPlugin, err := approval.New(&stubReviewer{})
	require.NoError(t, err)
	p, err := New(WithName("custom-guardrail"), WithApproval(approvalPlugin))
	require.NoError(t, err)
	require.Equal(t, "custom-guardrail", p.Name())
}

func TestRegister_ForwardsCapabilityRegistration(t *testing.T) {
	approvalPlugin, err := approval.New(&stubReviewer{}, approval.WithToolPolicy("shell", approval.ToolPolicyDenied))
	require.NoError(t, err)
	p, err := New(WithApproval(approvalPlugin))
	require.NoError(t, err)
	manager, err := plugin.NewManager(p)
	require.NoError(t, err)
	require.NotNil(t, manager.ToolCallbacks())
}
