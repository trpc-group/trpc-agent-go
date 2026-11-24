//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"context"
	"errors"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestPermissionCancelledWithoutActiveTurn(t *testing.T) {
	t.Parallel()

	client := &protocolClient{activeTurns: newActiveTurnRegistry()}
	response, err := client.RequestPermission(
		context.Background(),
		permissionTestRequest("missing"),
	)
	require.NoError(t, err)
	requirePermissionCancelled(t, response)
}

func TestPermissionRejectsWithoutPolicy(t *testing.T) {
	t.Parallel()

	turns := newActiveTurnRegistry()
	turns.set("session-1", &activeTurn{
		ctx:        context.Background(),
		invocation: &agent.Invocation{},
	})
	client := &protocolClient{activeTurns: turns}

	response, err := client.RequestPermission(
		context.Background(),
		permissionTestRequest("session-1"),
	)
	require.NoError(t, err)
	requirePermissionSelected(t, response, "reject-once")
}

func TestPermissionProjectsACPRequestAndAllowsOnce(t *testing.T) {
	t.Parallel()

	var captured *tool.PermissionRequest
	turns := newActiveTurnRegistry()
	turns.set("session-1", activeTurnWithPolicy(
		tool.PermissionPolicyFunc(func(
			_ context.Context,
			request *tool.PermissionRequest,
		) (tool.PermissionDecision, error) {
			captured = request
			return tool.AllowPermission(), nil
		},
		)))
	client := &protocolClient{activeTurns: turns}

	response, err := client.RequestPermission(
		context.Background(),
		permissionTestRequest("session-1"),
	)
	require.NoError(t, err)
	requirePermissionSelected(t, response, "allow-once")
	require.NotNil(t, captured)
	require.Equal(t, "write_file", captured.ToolName)
	require.Equal(t, "call-1", captured.ToolCallID)
	require.JSONEq(t, `{"path":"file.txt"}`, string(captured.Arguments))
	require.Equal(t, tool.ToolMetadata{Destructive: true}, captured.Metadata)
	require.Equal(t, "write_file", captured.Declaration.Name)
	require.Equal(t, "Edit file", captured.Declaration.Description)
	require.Same(t, captured.Declaration, captured.Tool.Declaration())
	require.Equal(t, captured.Metadata, tool.MetadataOf(captured.Tool))
}

func TestPermissionPolicyDecisionsFailClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		policy    tool.PermissionPolicy
		options   []acpsdk.PermissionOption
		optionID  string
		cancelled bool
	}{
		{
			name: "deny",
			policy: tool.PermissionPolicyFunc(func(
				context.Context,
				*tool.PermissionRequest,
			) (tool.PermissionDecision, error) {
				return tool.DenyPermission("denied"), nil
			}),
			optionID: "reject-once",
		},
		{
			name: "ask",
			policy: tool.PermissionPolicyFunc(func(
				context.Context,
				*tool.PermissionRequest,
			) (tool.PermissionDecision, error) {
				return tool.AskPermission("ask"), nil
			}),
			optionID: "reject-once",
		},
		{
			name: "policy error",
			policy: tool.PermissionPolicyFunc(func(
				context.Context,
				*tool.PermissionRequest,
			) (tool.PermissionDecision, error) {
				return tool.PermissionDecision{}, errors.New("policy failed")
			}),
			optionID: "reject-once",
		},
		{
			name: "invalid decision",
			policy: tool.PermissionPolicyFunc(func(
				context.Context,
				*tool.PermissionRequest,
			) (tool.PermissionDecision, error) {
				return tool.PermissionDecision{
					Action: tool.PermissionAction("invalid"),
				}, nil
			}),
			optionID: "reject-once",
		},
		{
			name: "policy canceled",
			policy: tool.PermissionPolicyFunc(func(
				context.Context,
				*tool.PermissionRequest,
			) (tool.PermissionDecision, error) {
				return tool.PermissionDecision{}, context.Canceled
			}),
			cancelled: true,
		},
		{
			name: "deny without reject option",
			policy: tool.PermissionPolicyFunc(func(
				context.Context,
				*tool.PermissionRequest,
			) (tool.PermissionDecision, error) {
				return tool.DenyPermission("denied"), nil
			}),
			options: []acpsdk.PermissionOption{
				permissionOption("allow-once", acpsdk.PermissionOptionKindAllowOnce),
			},
			cancelled: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			turns := newActiveTurnRegistry()
			turns.set("session-1", activeTurnWithPolicy(test.policy))
			client := &protocolClient{activeTurns: turns}
			request := permissionTestRequest("session-1")
			if test.options != nil {
				request.Options = test.options
			}
			response, err := client.RequestPermission(context.Background(), request)
			require.NoError(t, err)
			if test.cancelled {
				requirePermissionCancelled(t, response)
				return
			}
			requirePermissionSelected(t, response, test.optionID)
		})
	}
}

func TestPermissionRoutesBySession(t *testing.T) {
	t.Parallel()

	turns := newActiveTurnRegistry()
	turns.set("allow-session", activeTurnWithPolicy(tool.PermissionPolicyFunc(func(
		context.Context,
		*tool.PermissionRequest,
	) (tool.PermissionDecision, error) {
		return tool.AllowPermission(), nil
	})))
	turns.set("deny-session", activeTurnWithPolicy(tool.PermissionPolicyFunc(func(
		context.Context,
		*tool.PermissionRequest,
	) (tool.PermissionDecision, error) {
		return tool.DenyPermission("denied"), nil
	})))
	client := &protocolClient{activeTurns: turns}

	allowed, err := client.RequestPermission(
		context.Background(),
		permissionTestRequest("allow-session"),
	)
	require.NoError(t, err)
	requirePermissionSelected(t, allowed, "allow-once")

	denied, err := client.RequestPermission(
		context.Background(),
		permissionTestRequest("deny-session"),
	)
	require.NoError(t, err)
	requirePermissionSelected(t, denied, "reject-once")
}

func TestPermissionCancellationReturnsCancelled(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	requestCanceled := make(chan struct{})
	turns := newActiveTurnRegistry()
	turns.set("session-1", activeTurnWithPolicy(tool.PermissionPolicyFunc(func(
		ctx context.Context,
		_ *tool.PermissionRequest,
	) (tool.PermissionDecision, error) {
		close(started)
		<-ctx.Done()
		close(requestCanceled)
		return tool.AllowPermission(), nil
	})))
	client := &protocolClient{activeTurns: turns}
	ctx, cancel := context.WithCancel(context.Background())
	response := make(chan acpsdk.RequestPermissionResponse, 1)
	go func() {
		got, _ := client.RequestPermission(ctx, permissionTestRequest("session-1"))
		response <- got
	}()
	<-started
	cancel()
	<-requestCanceled
	requirePermissionCancelled(t, <-response)
}

func TestPermissionInvalidRawInputFailsClosed(t *testing.T) {
	t.Parallel()

	policyCalled := false
	turns := newActiveTurnRegistry()
	turns.set("session-1", activeTurnWithPolicy(tool.PermissionPolicyFunc(func(
		context.Context,
		*tool.PermissionRequest,
	) (tool.PermissionDecision, error) {
		policyCalled = true
		return tool.AllowPermission(), nil
	})))
	client := &protocolClient{activeTurns: turns}
	request := permissionTestRequest("session-1")
	request.ToolCall.RawInput = make(chan int)

	response, err := client.RequestPermission(context.Background(), request)
	require.NoError(t, err)
	require.False(t, policyCalled)
	requirePermissionSelected(t, response, "reject-once")
}

func TestPermissionToolNameFallsBackToTitle(t *testing.T) {
	t.Parallel()

	request := permissionTestRequest("session-1")
	request.ToolCall.Meta = nil
	projected, err := permissionRequestFromACP(request.ToolCall)
	require.NoError(t, err)
	require.Equal(t, "Edit file", projected.ToolName)
}

func activeTurnWithPolicy(policy tool.PermissionPolicy) *activeTurn {
	return &activeTurn{
		ctx: context.Background(),
		invocation: &agent.Invocation{
			RunOptions: agent.RunOptions{
				ToolPermissionPolicy: policy,
			},
		},
	}
}

func permissionTestRequest(sessionID acpsdk.SessionId) acpsdk.RequestPermissionRequest {
	title := "Edit file"
	kind := acpsdk.ToolKindEdit
	return acpsdk.RequestPermissionRequest{
		SessionId: sessionID,
		Options: []acpsdk.PermissionOption{
			permissionOption("allow-always", acpsdk.PermissionOptionKindAllowAlways),
			permissionOption("allow-once", acpsdk.PermissionOptionKindAllowOnce),
			permissionOption("reject-always", acpsdk.PermissionOptionKindRejectAlways),
			permissionOption("reject-once", acpsdk.PermissionOptionKindRejectOnce),
		},
		ToolCall: acpsdk.ToolCallUpdate{
			Meta: map[string]any{
				permissionMetaToolName: "write_file",
			},
			ToolCallId: "call-1",
			Title:      &title,
			Kind:       &kind,
			RawInput: map[string]any{
				"path": "file.txt",
			},
		},
	}
}

func permissionOption(
	id string,
	kind acpsdk.PermissionOptionKind,
) acpsdk.PermissionOption {
	return acpsdk.PermissionOption{
		OptionId: acpsdk.PermissionOptionId(id),
		Kind:     kind,
		Name:     string(kind),
	}
}

func requirePermissionSelected(
	t *testing.T,
	response acpsdk.RequestPermissionResponse,
	optionID string,
) {
	t.Helper()
	require.NotNil(t, response.Outcome.Selected)
	require.Equal(t, acpsdk.PermissionOptionId(optionID), response.Outcome.Selected.OptionId)
	require.Nil(t, response.Outcome.Cancelled)
}

func requirePermissionCancelled(
	t *testing.T,
	response acpsdk.RequestPermissionResponse,
) {
	t.Helper()
	require.NotNil(t, response.Outcome.Cancelled)
	require.Nil(t, response.Outcome.Selected)
}

func TestPermissionContextWaitBoundsPolicy(t *testing.T) {
	t.Parallel()

	deadline := make(chan struct{})
	turns := newActiveTurnRegistry()
	turn := activeTurnWithPolicy(tool.PermissionPolicyFunc(func(
		ctx context.Context,
		_ *tool.PermissionRequest,
	) (tool.PermissionDecision, error) {
		<-ctx.Done()
		close(deadline)
		return tool.AllowPermission(), nil
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	turn.ctx = ctx
	turns.set("session-1", turn)
	client := &protocolClient{activeTurns: turns}

	response, err := client.RequestPermission(
		context.Background(),
		permissionTestRequest("session-1"),
	)
	require.NoError(t, err)
	<-deadline
	requirePermissionCancelled(t, response)
}
