//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package approval

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	approvallog "trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	approvalreview "trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type stubReviewer struct {
	reviewFn func(ctx context.Context, req *approvalreview.Request) (*approvalreview.Decision, error)
}

func (s *stubReviewer) Review(ctx context.Context, req *approvalreview.Request) (*approvalreview.Decision, error) {
	return s.reviewFn(ctx, req)
}

func TestNew_NilReviewer(t *testing.T) {
	_, err := New(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "reviewer is nil")
}

func TestNew_InvalidToolPolicy(t *testing.T) {
	_, err := New(&stubReviewer{}, WithDefaultToolPolicy(ToolPolicy("bad")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid tool policy")
}

func TestNew_EmptyToolPolicyName(t *testing.T) {
	_, err := New(&stubReviewer{}, WithToolPolicy("", ToolPolicyDenied))
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool policy name is empty")
}

func TestNew_WithName(t *testing.T) {
	p, err := New(&stubReviewer{}, WithName("tool-approval"))
	require.NoError(t, err)
	require.Equal(t, "tool-approval", p.Name())
}

func TestOptionSettersUpdateOptions(t *testing.T) {
	opts := newOptions()
	WithName("tool-approval")(opts)
	WithDefaultToolPolicy(ToolPolicyDenied)(opts)
	require.Equal(t, "tool-approval", opts.name)
	require.Equal(t, ToolPolicyDenied, opts.defaultToolPolicy)
}

func TestRegister_IgnoresNilReceiverAndNilRegistry(t *testing.T) {
	var nilPlugin *Plugin
	require.NotPanics(t, func() {
		nilPlugin.Register(nil)
	})
	p, err := New(&stubReviewer{})
	require.NoError(t, err)
	require.NotPanics(t, func() {
		p.Register(nil)
	})
}

func TestWithToolPolicy_InitializesMap(t *testing.T) {
	opts := &options{}
	WithToolPolicy("shell", ToolPolicyDenied)(opts)
	require.Equal(t, ToolPolicyDenied, opts.toolPolicies["shell"])
}

func TestBeforeTool_DeniedPolicyShortCircuits(t *testing.T) {
	reviewerCalled := false
	p, err := New(
		&stubReviewer{
			reviewFn: func(ctx context.Context, req *approvalreview.Request) (*approvalreview.Decision, error) {
				reviewerCalled = true
				return nil, nil
			},
		},
		WithToolPolicy("shell", ToolPolicyDenied),
	)
	require.NoError(t, err)
	callbacks := registeredToolCallbacks(t, p)
	result, err := callbacks.RunBeforeTool(context.Background(), &tool.BeforeToolArgs{
		ToolName:   "shell",
		ToolCallID: "call-1",
		Arguments:  []byte(`{"command":"pwd"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, reviewerCalled)
	require.Equal(t, `tool "shell" is denied by approval policy`, result.CustomResult)
}

func TestBeforeTool_SkipApprovalBypassesReviewer(t *testing.T) {
	reviewerCalled := false
	p, err := New(
		&stubReviewer{
			reviewFn: func(ctx context.Context, req *approvalreview.Request) (*approvalreview.Decision, error) {
				reviewerCalled = true
				return &approvalreview.Decision{Approved: true}, nil
			},
		},
		WithToolPolicy("search", ToolPolicySkipApproval),
	)
	require.NoError(t, err)
	callbacks := registeredToolCallbacks(t, p)
	result, err := callbacks.RunBeforeTool(context.Background(), &tool.BeforeToolArgs{
		ToolName:   "search",
		ToolCallID: "call-1",
		Arguments:  []byte(`{"query":"guardian"}`),
	})
	require.NoError(t, err)
	require.Nil(t, result)
	require.False(t, reviewerCalled)
}

func TestBeforeTool_NilArgsReturnsNil(t *testing.T) {
	p, err := New(&stubReviewer{})
	require.NoError(t, err)
	result, runErr := p.beforeTool()(context.Background(), nil)
	require.NoError(t, runErr)
	require.Nil(t, result)
}

func TestBeforeTool_RequireApprovalBuildsRequestFromSession(t *testing.T) {
	var captured *approvalreview.Request
	p, err := New(&stubReviewer{
		reviewFn: func(ctx context.Context, req *approvalreview.Request) (*approvalreview.Decision, error) {
			captured = req
			return &approvalreview.Decision{Approved: true, RiskScore: 12, RiskLevel: "low", Reason: "Allowed."}, nil
		},
	})
	require.NoError(t, err)
	callbacks := registeredToolCallbacks(t, p)
	invocation := invocationWithEvents(
		t,
		[]event.Event{
			responseEvent(
				"inv-1",
				"author",
				"app",
				model.Message{Role: model.RoleUser, Content: "Please inspect the workspace."},
			),
			responseEvent(
				"inv-1",
				"author",
				"app",
				model.Message{
					Role:    model.RoleAssistant,
					Content: "I will inspect the workspace first.",
					ToolCalls: []model.ToolCall{{
						Type: "function",
						ID:   "tool-call-1",
						Function: model.FunctionDefinitionParam{
							Name:      "shell",
							Arguments: []byte(`{"command":"ls"}`),
						},
					}},
				},
			),
			responseEvent(
				"inv-1",
				"author",
				"app",
				model.Message{Role: model.RoleTool, ToolID: "tool-call-1", ToolName: "shell", Content: "file-a\nfile-b"},
			),
			responseEvent(
				"inv-2",
				"author",
				"other",
				model.Message{Role: model.RoleUser, Content: "This should be filtered out."},
			),
		},
	)
	ctx := agent.NewInvocationContext(context.Background(), invocation)
	result, err := callbacks.RunBeforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:    "shell",
		ToolCallID:  "call-2",
		Declaration: &tool.Declaration{Name: "shell", Description: "Runs shell commands."},
		Arguments:   []byte(`{"command":"pwd"}`),
	})
	require.NoError(t, err)
	require.Nil(t, result)
	require.NotNil(t, captured)
	require.Equal(t, "shell", captured.Action.ToolName)
	require.Equal(t, "Runs shell commands.", captured.Action.ToolDescription)
	require.JSONEq(t, `{"command":"pwd"}`, string(captured.Action.Arguments))
	require.Len(t, captured.Transcript, 4)
	assert.Equal(t, model.RoleUser, captured.Transcript[0].Role)
	assert.Equal(t, "Please inspect the workspace.", captured.Transcript[0].Content)
	assert.Equal(t, "I will inspect the workspace first.", captured.Transcript[1].Content)
	assert.Equal(t, "tool shell call: {\"command\":\"ls\"}", captured.Transcript[2].Content)
	assert.Equal(t, "tool shell result: file-a\nfile-b", captured.Transcript[3].Content)
}

func TestBeforeTool_ReviewerApprovedLogsInfo(t *testing.T) {
	original := approvallog.InfofContext
	var infoLog string
	approvallog.InfofContext = func(ctx context.Context, format string, args ...any) {
		infoLog = fmt.Sprintf(format, args...)
	}
	defer func() {
		approvallog.InfofContext = original
	}()
	p, err := New(&stubReviewer{
		reviewFn: func(ctx context.Context, req *approvalreview.Request) (*approvalreview.Decision, error) {
			return &approvalreview.Decision{
				Approved:  true,
				RiskScore: 42,
				RiskLevel: "medium",
				Reason:    "The action is scoped and user-authorized.",
			}, nil
		},
	})
	require.NoError(t, err)
	callbacks := registeredToolCallbacks(t, p)
	result, runErr := callbacks.RunBeforeTool(context.Background(), &tool.BeforeToolArgs{
		ToolName:   "shell",
		ToolCallID: "call-1",
		Arguments:  []byte(`{"command":"pwd"}`),
	})
	require.NoError(t, runErr)
	require.Nil(t, result)
	require.Equal(
		t,
		"Automatic approval review approved (risk: medium): The action is scoped and user-authorized.",
		infoLog,
	)
}

func TestBeforeTool_ReviewerErrorFailsClosed(t *testing.T) {
	original := approvallog.ErrorfContext
	var errorLog string
	approvallog.ErrorfContext = func(ctx context.Context, format string, args ...any) {
		errorLog = fmt.Sprintf(format, args...)
	}
	defer func() {
		approvallog.ErrorfContext = original
	}()
	p, err := New(&stubReviewer{
		reviewFn: func(ctx context.Context, req *approvalreview.Request) (*approvalreview.Decision, error) {
			return nil, fmt.Errorf("review backend unavailable")
		},
	})
	require.NoError(t, err)
	callbacks := registeredToolCallbacks(t, p)
	result, err := callbacks.RunBeforeTool(context.Background(), &tool.BeforeToolArgs{
		ToolName:   "shell",
		ToolCallID: "call-1",
		Arguments:  []byte(`{"command":"pwd"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, `approval review failed for tool "shell": review backend unavailable`, result.CustomResult)
	require.Equal(
		t,
		`Automatic approval review denied: approval review failed for tool "shell": review backend unavailable`,
		errorLog,
	)
}

func TestBeforeTool_NilDecisionFailsClosed(t *testing.T) {
	p, err := New(&stubReviewer{
		reviewFn: func(ctx context.Context, req *approvalreview.Request) (*approvalreview.Decision, error) {
			return nil, nil
		},
	})
	require.NoError(t, err)
	callbacks := registeredToolCallbacks(t, p)
	result, runErr := callbacks.RunBeforeTool(context.Background(), &tool.BeforeToolArgs{
		ToolName:   "shell",
		ToolCallID: "call-1",
		Arguments:  []byte(`{"command":"pwd"}`),
	})
	require.NoError(t, runErr)
	require.NotNil(t, result)
	require.Equal(t, `approval review failed for tool "shell": approval reviewer returned nil decision`, result.CustomResult)
}

func TestBeforeTool_EmptyDecisionFieldsDoNotFail(t *testing.T) {
	p, err := New(&stubReviewer{
		reviewFn: func(ctx context.Context, req *approvalreview.Request) (*approvalreview.Decision, error) {
			return &approvalreview.Decision{
				Approved:  false,
				RiskScore: 92,
			}, nil
		},
	})
	require.NoError(t, err)
	callbacks := registeredToolCallbacks(t, p)
	result, runErr := callbacks.RunBeforeTool(context.Background(), &tool.BeforeToolArgs{
		ToolName:   "shell",
		ToolCallID: "call-1",
		Arguments:  []byte(`{"command":"rm -rf /tmp/demo"}`),
	})
	require.NoError(t, runErr)
	require.NotNil(t, result)
	require.Equal(t, "Automatic approval review denied (risk: ): ", result.CustomResult)
}

func TestBeforeTool_ReviewerDeniedLogsWarning(t *testing.T) {
	original := approvallog.WarnContext
	var warning string
	approvallog.WarnContext = func(ctx context.Context, args ...any) {
		warning = fmt.Sprint(args...)
	}
	defer func() {
		approvallog.WarnContext = original
	}()
	p, err := New(&stubReviewer{
		reviewFn: func(ctx context.Context, req *approvalreview.Request) (*approvalreview.Decision, error) {
			return &approvalreview.Decision{
				Approved:  false,
				RiskScore: 92,
				RiskLevel: "high",
				Reason:    "The command is destructive and exceeds safe automatic approval.",
			}, nil
		},
	})
	require.NoError(t, err)
	callbacks := registeredToolCallbacks(t, p)
	result, runErr := callbacks.RunBeforeTool(context.Background(), &tool.BeforeToolArgs{
		ToolName:   "shell",
		ToolCallID: "call-1",
		Arguments:  []byte(`{"command":"rm -rf /tmp/demo"}`),
	})
	require.NoError(t, runErr)
	require.NotNil(t, result)
	require.Equal(
		t,
		"Automatic approval review denied (risk: high): The command is destructive and exceeds safe automatic approval.",
		result.CustomResult,
	)
	require.Equal(
		t,
		"Automatic approval review denied (risk: high): The command is destructive and exceeds safe automatic approval.",
		warning,
	)
}

func TestBeforeTool_UnsupportedPolicyReturnsFailureMessage(t *testing.T) {
	p := &Plugin{
		name:              "approval",
		reviewer:          &stubReviewer{},
		defaultToolPolicy: ToolPolicy("unsupported"),
		toolPolicies:      map[string]ToolPolicy{},
		tokenCounter:      model.NewSimpleTokenCounter(),
	}
	result, runErr := p.beforeTool()(context.Background(), &tool.BeforeToolArgs{
		ToolName:   "shell",
		ToolCallID: "call-1",
		Arguments:  []byte(`{"command":"pwd"}`),
	})
	require.NoError(t, runErr)
	require.NotNil(t, result)
	require.Equal(t, `approval review failed for tool "shell": unsupported tool policy "unsupported"`, result.CustomResult)
}

func TestBuildTranscript_UserOverflowReturnsOmissionOnly(t *testing.T) {
	p, err := New(&stubReviewer{
		reviewFn: func(ctx context.Context, req *approvalreview.Request) (*approvalreview.Decision, error) {
			return &approvalreview.Decision{Approved: true}, nil
		},
	})
	require.NoError(t, err)
	events := make([]event.Event, 0, 25)
	for i := 0; i < 25; i++ {
		events = append(events, responseEvent(
			"inv-1",
			"author",
			"app",
			model.Message{Role: model.RoleUser, Content: stringsRepeat("user ", defaultMessageEntryCap)},
		))
	}
	events = append(events, responseEvent(
		"inv-1",
		"author",
		"app",
		model.Message{Role: model.RoleAssistant, Content: "assistant context"},
	))
	invocation := invocationWithEvents(
		t,
		events,
	)
	transcript := p.buildTranscript(context.Background(), invocation)
	require.Len(t, transcript, 1)
	assert.Equal(t, model.RoleAssistant, transcript[0].Role)
	assert.Equal(t, omissionNote, transcript[0].Content)
}

func TestBuildRequest_WithoutInvocationReturnsActionOnly(t *testing.T) {
	p, err := New(&stubReviewer{})
	require.NoError(t, err)
	req, buildErr := p.buildRequest(context.Background(), &tool.BeforeToolArgs{
		ToolName:    "shell",
		Declaration: &tool.Declaration{Description: "Runs shell commands."},
		Arguments:   []byte(`{"command":"pwd"}`),
	})
	require.NoError(t, buildErr)
	require.NotNil(t, req)
	require.Equal(t, "shell", req.Action.ToolName)
	require.Equal(t, "Runs shell commands.", req.Action.ToolDescription)
	require.JSONEq(t, `{"command":"pwd"}`, string(req.Action.Arguments))
	require.Nil(t, req.Transcript)
}

type errorTokenCounter struct{}

func (errorTokenCounter) CountTokens(ctx context.Context, message model.Message) (int, error) {
	return 0, errors.New("count tokens failed")
}

func (errorTokenCounter) CountTokensRange(
	ctx context.Context,
	messages []model.Message,
	start, end int,
) (int, error) {
	return 0, errors.New("count tokens failed")
}

func TestCountTokens_ReturnsZeroOnCounterError(t *testing.T) {
	p := &Plugin{tokenCounter: errorTokenCounter{}}
	require.Equal(t, defaultMessageTranscriptBudget+1, p.countTokens(context.Background(), approvalreview.TranscriptEntry{
		Role:    model.RoleUser,
		Content: "hello",
	}))
}

func TestBuildTranscript_TokenCounterErrorFailsClosed(t *testing.T) {
	p := &Plugin{tokenCounter: errorTokenCounter{}}
	invocation := invocationWithEvents(t, []event.Event{
		responseEvent(
			"inv-1",
			"author",
			"app",
			model.Message{Role: model.RoleUser, Content: "Please inspect the workspace."},
		),
		responseEvent(
			"inv-1",
			"author",
			"app",
			model.Message{Role: model.RoleAssistant, Content: "I will inspect it."},
		),
	})
	transcript := p.buildTranscript(context.Background(), invocation)
	require.Len(t, transcript, 1)
	require.Equal(t, model.RoleAssistant, transcript[0].Role)
	require.Equal(t, omissionNote, transcript[0].Content)
}

func registeredToolCallbacks(t *testing.T, p *Plugin) *tool.Callbacks {
	t.Helper()
	manager := plugin.MustNewManager(p)
	callbacks := manager.ToolCallbacks()
	require.NotNil(t, callbacks)
	return callbacks
}

func invocationWithEvents(t *testing.T, events []event.Event) *agent.Invocation {
	t.Helper()
	sess := session.NewSession("app", "user", "session", session.WithSessionEvents(events))
	return agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("app"),
	)
}

func responseEvent(invocationID, author, filterKey string, message model.Message) event.Event {
	evt := event.NewResponseEvent(invocationID, author, &model.Response{
		Choices: []model.Choice{{Message: message}},
		Done:    true,
	})
	evt.FilterKey = filterKey
	return *evt
}

func stringsRepeat(value string, n int) string {
	if n <= 0 {
		return ""
	}
	result := make([]byte, 0, len(value)*n)
	for i := 0; i < n; i++ {
		result = append(result, value...)
	}
	return string(result)
}
