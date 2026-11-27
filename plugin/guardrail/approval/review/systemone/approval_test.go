//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package systemone_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestApprovalPlugin(t *testing.T) {
	for _, tt := range []struct {
		name      string
		policy    approval.ToolPolicy
		choice    string
		status    int
		wantCalls int
		blocked   bool
	}{
		{"approve", approval.ToolPolicyRequireApproval, "supported", 200, 1, false},
		{"deny", approval.ToolPolicyRequireApproval, "insufficient", 200, 1, true},
		{"service error", approval.ToolPolicyRequireApproval, "supported", 503, 1, true},
		{"skip", approval.ToolPolicySkipApproval, "insufficient", 200, 0, false},
		{"static deny", approval.ToolPolicyDenied, "supported", 200, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls int
			a := defaultAnswer()
			a.choice = tt.choice
			r := newReviewer(t, func(*http.Request) (*http.Response, error) {
				calls++
				return jsonResponse(a.JSON(), tt.status), nil
			})
			p, err := approval.New(approval.WithReviewer(r), approval.WithDefaultToolPolicy(tt.policy))
			require.NoError(t, err)
			evt := event.NewResponseEvent("invocation", "user", &model.Response{
				Choices: []model.Choice{{Message: model.NewUserMessage("Run pwd and print the current directory.")}},
				Done:    true,
			})
			evt.FilterKey = "app"
			sess := session.NewSession("app", "user", "session", session.WithSessionEvents([]event.Event{*evt}))
			invocation := agent.NewInvocation(agent.WithInvocationSession(sess), agent.WithInvocationEventFilterKey("app"))
			ctx := agent.NewInvocationContext(context.Background(), invocation)
			callbacks := plugin.MustNewManager(p).ToolCallbacks()
			result, err := callbacks.RunBeforeTool(ctx, &tool.BeforeToolArgs{
				ToolName: "shell", Arguments: []byte(`{"command":"pwd"}`),
			})
			require.NoError(t, err)
			require.Equal(t, tt.wantCalls, calls)
			if tt.blocked {
				require.NotNil(t, result)
				require.NotEmpty(t, result.CustomResult)
			} else {
				require.Nil(t, result)
			}
		})
	}
}
