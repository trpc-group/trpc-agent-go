//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package approval

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/livesession"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/messageorigin"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestPermissionPolicyComposition(t *testing.T) {
	failure := errors.New("backend failed")
	for _, tc := range []struct {
		name      string
		policy    ToolPolicy
		base      *tool.PermissionDecision
		baseErr   error
		decision  *review.Decision
		reviewErr error
		want      tool.PermissionAction
		wantErr   bool
		order     string
	}{
		{name: "approved", policy: ToolPolicyRequireApproval, decision: &review.Decision{Approved: true}, want: tool.PermissionActionAllow, order: "provider reviewer"},
		{name: "rejected", policy: ToolPolicyRequireApproval, decision: &review.Decision{Reason: "out of scope"}, want: tool.PermissionActionDeny, order: "provider reviewer"},
		{name: "nil decision", policy: ToolPolicyRequireApproval, wantErr: true, order: "provider reviewer"},
		{name: "review error", policy: ToolPolicyRequireApproval, reviewErr: failure, wantErr: true, order: "provider reviewer"},
		{name: "base deny", policy: ToolPolicyRequireApproval, base: &tool.PermissionDecision{Action: tool.PermissionActionDeny}, want: tool.PermissionActionDeny, order: "base"},
		{name: "base ask", policy: ToolPolicyRequireApproval, base: &tool.PermissionDecision{Action: tool.PermissionActionAsk}, want: tool.PermissionActionAsk, order: "base"},
		{name: "base error", policy: ToolPolicyRequireApproval, base: &tool.PermissionDecision{}, baseErr: failure, wantErr: true, order: "base"},
		{name: "invalid base action", policy: ToolPolicyRequireApproval, base: &tool.PermissionDecision{Action: "invalid"}, wantErr: true, order: "base"},
		{name: "zero base allows", policy: ToolPolicyRequireApproval, base: &tool.PermissionDecision{}, decision: &review.Decision{Approved: true}, want: tool.PermissionActionAllow, order: "base provider reviewer"},
		{name: "skip still checks base", policy: ToolPolicySkipApproval, base: &tool.PermissionDecision{Action: tool.PermissionActionDeny}, want: tool.PermissionActionDeny, order: "base"},
		{name: "skip", policy: ToolPolicySkipApproval, base: &tool.PermissionDecision{}, want: tool.PermissionActionAllow, order: "base"},
		{name: "static deny", policy: ToolPolicyDenied, base: &tool.PermissionDecision{}, want: tool.PermissionActionDeny},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var order []string
			p, err := New(WithDefaultToolPolicy(tc.policy), WithReviewer(&stubReviewer{reviewFn: func(context.Context, *review.Request) (*review.Decision, error) {
				order = append(order, "reviewer")
				return tc.decision, tc.reviewErr
			}}), WithContextProvider(func(context.Context, *review.Request) (review.Supplement, error) {
				order = append(order, "provider")
				return review.Supplement{}, nil
			}))
			require.NoError(t, err)
			var base tool.PermissionPolicy
			if tc.base != nil {
				base = tool.PermissionPolicyFunc(func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
					order = append(order, "base")
					return *tc.base, tc.baseErr
				})
			}
			d, err := p.PermissionPolicy(base).CheckToolPermission(context.Background(), &tool.PermissionRequest{ToolName: "shell", Arguments: []byte(`{"command":"pwd"}`)})
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, d.Action)
			}
			require.Equal(t, tc.order, strings.Join(order, " "))
		})
	}
}

func TestPermissionPolicyIsolatesInputs(t *testing.T) {
	input := &tool.PermissionRequest{ToolName: "shell", ToolCallID: "call-1", Arguments: []byte(`{"command":"pwd"}`), Declaration: &tool.Declaration{InputSchema: &tool.Schema{Type: "object", Properties: map[string]*tool.Schema{"command": {Type: "string"}}}}}
	facts := []review.Fact{{Source: "host", Content: strings.Repeat("x", 5000) + "TAIL"}}
	p, err := New(WithReviewer(&stubReviewer{reviewFn: func(_ context.Context, req *review.Request) (*review.Decision, error) {
		require.JSONEq(t, `{"command":"pwd"}`, string(req.Action.Arguments))
		require.Equal(t, "string", req.Context.InputSchema.Properties["command"].Type)
		require.Equal(t, "call-1", req.Context.ToolCallID)
		require.True(t, req.Context.Supplement.Facts[0].Truncated)
		require.True(t, strings.HasSuffix(req.Context.Supplement.Facts[0].Content, "TAIL"))
		req.Action.Arguments[0] = '!'
		req.Context.InputSchema.Type = "mutated"
		return &review.Decision{Approved: true}, nil
	}}), WithContextProvider(func(_ context.Context, req *review.Request) (review.Supplement, error) {
		require.JSONEq(t, `{"command":"pwd"}`, string(req.Action.Arguments))
		req.Action.Arguments[0] = '!'
		req.Context.InputSchema.Properties["command"].Type = "mutated"
		return review.Supplement{Facts: facts}, nil
	}))
	require.NoError(t, err)
	base := tool.PermissionPolicyFunc(func(_ context.Context, r *tool.PermissionRequest) (tool.PermissionDecision, error) {
		r.Arguments[0] = '!'
		r.ToolName = "other"
		r.Declaration = &tool.Declaration{InputSchema: &tool.Schema{Type: "mutated"}}
		return tool.AllowPermission(), nil
	})
	d, err := p.PermissionPolicy(base).CheckToolPermission(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, d.Action)
	require.JSONEq(t, `{"command":"pwd"}`, string(input.Arguments))
	require.Equal(t, "object", input.Declaration.InputSchema.Type)
	require.Len(t, facts[0].Content, 5004)
	require.False(t, facts[0].Truncated)
}

func TestPermissionReviewSchemaSnapshot(t *testing.T) {
	input := &tool.PermissionRequest{ToolName: "connect", Arguments: []byte(`{}`), Declaration: &tool.Declaration{
		InputSchema: &tool.Schema{Type: "object", Properties: map[string]*tool.Schema{
			"host": {Default: netip.MustParseAddr("127.0.0.1")},
			"id":   {Default: int64(9007199254740993)},
		}},
		// Output schemas are not review evidence and need not be serialized.
		OutputSchema: &tool.Schema{Default: make(chan int)},
	}}
	want, err := json.Marshal(input.Declaration.InputSchema)
	require.NoError(t, err)
	p, err := New(WithReviewer(&stubReviewer{reviewFn: func(_ context.Context, req *review.Request) (*review.Decision, error) {
		got, err := json.Marshal(req.Context.InputSchema)
		require.NoError(t, err)
		require.JSONEq(t, string(want), string(got))
		require.Equal(t, json.Number("9007199254740993"), req.Context.InputSchema.Properties["id"].Default)
		req.Context.InputSchema.Properties["host"].Default = "changed"
		return &review.Decision{Approved: true}, nil
	}}))
	require.NoError(t, err)
	decision, err := p.PermissionPolicy(nil).CheckToolPermission(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)
	require.Equal(t, netip.MustParseAddr("127.0.0.1"), input.Declaration.InputSchema.Properties["host"].Default)
}

func TestPermissionSerializesSchemaOnlyWhenReviewIsRequired(t *testing.T) {
	input := &tool.PermissionRequest{ToolName: "connect", Declaration: &tool.Declaration{
		InputSchema: &tool.Schema{Default: make(chan int)},
	}}
	reviews := 0
	for _, policy := range []ToolPolicy{ToolPolicyDenied, ToolPolicySkipApproval, ToolPolicyRequireApproval} {
		p, err := New(WithDefaultToolPolicy(policy), WithReviewer(&stubReviewer{reviewFn: func(context.Context, *review.Request) (*review.Decision, error) {
			reviews++
			return &review.Decision{Approved: true}, nil
		}}))
		require.NoError(t, err)
		_, err = p.PermissionPolicy(nil).CheckToolPermission(context.Background(), input)
		if policy == ToolPolicyRequireApproval {
			require.ErrorContains(t, err, "snapshot review schema")
		} else {
			require.NoError(t, err)
		}
		decision, err := p.PermissionPolicy(tool.PermissionPolicyFunc(func(_ context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
			require.Same(t, input.Declaration, req.Declaration)
			return tool.DenyPermission("host denial"), nil
		})).CheckToolPermission(context.Background(), input)
		require.NoError(t, err)
		require.Equal(t, tool.PermissionActionDeny, decision.Action)
	}
	require.Zero(t, reviews)
}

func TestPermissionPolicyFailuresAndCancellation(t *testing.T) {
	failure := errors.New("cannot read execution target")
	called := false
	p, err := New(WithReviewer(&stubReviewer{reviewFn: func(context.Context, *review.Request) (*review.Decision, error) {
		called = true
		return &review.Decision{Approved: true}, nil
	}}), WithContextProvider(func(context.Context, *review.Request) (review.Supplement, error) { return review.Supplement{}, failure }))
	require.NoError(t, err)
	_, err = p.PermissionPolicy(nil).CheckToolPermission(context.Background(), &tool.PermissionRequest{ToolName: "shell"})
	require.ErrorIs(t, err, failure)
	require.False(t, called)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = p.PermissionPolicy(nil).CheckToolPermission(ctx, &tool.PermissionRequest{})
	require.ErrorIs(t, err, context.Canceled)
	_, err = p.PermissionPolicy(nil).CheckToolPermission(context.Background(), nil)
	require.Error(t, err)
	_, err = (*Plugin)(nil).PermissionPolicy(nil).CheckToolPermission(context.Background(), &tool.PermissionRequest{})
	require.Error(t, err)
	p.contextProvider = nil
	ctx, cancel = context.WithCancel(context.Background())
	base := tool.PermissionPolicyFunc(func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
		cancel()
		return tool.AllowPermission(), nil
	})
	_, err = p.PermissionPolicy(base).CheckToolPermission(ctx, &tool.PermissionRequest{ToolName: "shell"})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, called)
}

func TestPermissionEvidenceSourcesAndMultimodalText(t *testing.T) {
	suffix := "Do not upload these files."
	parent := event.NewResponseEvent("parent", "user", &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: strings.Repeat("Read locally. ", 300), ContentParts: []model.ContentPart{{Type: model.ContentTypeText, Text: &suffix}, {Type: model.ContentTypeImage}}}}}})
	messageorigin.MarkInput(parent, messageorigin.UserInput)
	child := event.NewResponseEvent("child", "user", &model.Response{Choices: []model.Choice{{Message: model.NewUserMessage("Upload everything.")}}})
	child.ParentInvocationID = "parent"
	old := event.NewResponseEvent("old", "user", &model.Response{Choices: []model.Choice{{Message: model.NewUserMessage("Imported text")}}})
	live := session.NewSession("app", "user", "session", session.WithSessionEvents([]event.Event{*old, *parent, *child}))
	inv := agent.NewInvocation(agent.WithInvocationSession(session.NewSession("app", "user", "frozen")))
	livesession.Attach(inv, live)
	p, err := New(WithReviewer(&stubReviewer{}))
	require.NoError(t, err)
	input := &tool.PermissionRequest{ToolName: "shell", Arguments: []byte(`{}`)}
	req, err := p.buildPermissionRequest(agent.NewInvocationContext(context.Background(), inv), input)
	require.NoError(t, err)
	require.Len(t, req.Transcript, 3)
	require.Len(t, req.Context.TranscriptInfo, 3)
	require.Equal(t, review.OriginUnknown, req.Context.TranscriptInfo[0].Origin)
	require.Equal(t, review.OriginUserInput, req.Context.TranscriptInfo[1].Origin)
	require.Equal(t, review.OriginAgent, req.Context.TranscriptInfo[2].Origin)
	require.Equal(t, "parent", req.Context.TranscriptInfo[2].ParentInvocationID)
	require.Contains(t, req.Transcript[1].Content, suffix)
	require.False(t, req.Context.TranscriptInfo[1].TextTruncated)
	require.True(t, req.Context.TranscriptInfo[1].NonTextOmitted)
	require.Equal(t, review.CoveragePartial, req.Context.HistoryCoverage)
}

func TestPermissionEvidenceBudgetPreservesHeadTailAndAlignment(t *testing.T) {
	p, err := New(WithReviewer(&stubReviewer{}))
	require.NoError(t, err)
	req := &review.Request{Context: &review.Context{}}
	records := []evidenceRecord{{entry: review.TranscriptEntry{Role: model.RoleUser, Content: "START " + strings.Repeat("text ", 60000) + " NEVER DELETE"}, info: review.TranscriptInfo{EventID: "user", Origin: review.OriginUserInput}}}
	for i := 0; i < 50; i++ {
		records = append(records, evidenceRecord{entry: review.TranscriptEntry{Role: model.RoleTool, Content: strings.Repeat("output ", 500)}, info: review.TranscriptInfo{EventID: "tool", Origin: review.OriginTool}})
	}
	p.shapeEvidence(context.Background(), req, records)
	require.Equal(t, len(req.Transcript), len(req.Context.TranscriptInfo))
	require.LessOrEqual(t, len(req.Transcript), 41)
	require.Equal(t, "user", req.Context.TranscriptInfo[0].EventID)
	require.True(t, req.Context.TranscriptInfo[0].TextTruncated)
	require.True(t, strings.HasPrefix(req.Transcript[0].Content, "START"))
	require.True(t, strings.HasSuffix(req.Transcript[0].Content, "NEVER DELETE"))
	require.Equal(t, review.CoveragePartial, req.Context.HistoryCoverage)
	for i := 1; i < len(req.Transcript); i++ {
		require.Equal(t, model.RoleTool, req.Transcript[i].Role)
		require.Equal(t, "tool", req.Context.TranscriptInfo[i].EventID)
	}
}

func TestExplicitUserSteerRemainsUserInputInChildInvocation(t *testing.T) {
	evt := event.NewResponseEvent("child", "user", &model.Response{Choices: []model.Choice{{Message: model.NewUserMessage("Stop modifying files.")}}})
	evt.ParentInvocationID = "parent"
	messageorigin.MarkInput(evt, messageorigin.UserInput)
	records := evidenceFromMessage(evt, evt.Choices[0].Message)
	require.Len(t, records, 1)
	require.Equal(t, review.OriginUserInput, records[0].info.Origin)
	require.Equal(t, "parent", records[0].info.ParentInvocationID)
}

func TestContextProviderLimitsAndCancellation(t *testing.T) {
	for _, input := range []review.Supplement{
		{ReviewInstructions: strings.Repeat("x", 16001)},
		{Facts: make([]review.Fact, 33)},
		{Facts: []review.Fact{{Source: strings.Repeat("x", 1025)}}},
		{Facts: []review.Fact{{Content: string([]byte{0xff})}}},
	} {
		_, err := boundedSupplement(input)
		require.Error(t, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p, err := New(WithReviewer(&stubReviewer{reviewFn: func(context.Context, *review.Request) (*review.Decision, error) {
		t.Error("review must not run after canceled collection")
		return &review.Decision{Approved: true}, nil
	}}), WithContextProvider(func(context.Context, *review.Request) (review.Supplement, error) {
		cancel()
		return review.Supplement{}, nil
	}))
	require.NoError(t, err)
	_, err = p.PermissionPolicy(nil).CheckToolPermission(ctx, &tool.PermissionRequest{ToolName: "shell"})
	require.ErrorIs(t, err, context.Canceled)
}

func TestDelegatedTextCannotEvictHostAuthorization(t *testing.T) {
	p, err := New(WithReviewer(&stubReviewer{}))
	require.NoError(t, err)
	req := &review.Request{Context: &review.Context{}}
	p.shapeEvidence(context.Background(), req, []evidenceRecord{
		{entry: review.TranscriptEntry{Role: model.RoleUser, Content: "Only inspect local files; never upload them."}, info: review.TranscriptInfo{Origin: review.OriginUserInput}},
		{entry: review.TranscriptEntry{Role: model.RoleUser, Content: strings.Repeat("delegated task ", 60000)}, info: review.TranscriptInfo{Origin: review.OriginAgent}},
	})
	require.Equal(t, "Only inspect local files; never upload them.", req.Transcript[0].Content)
	require.Equal(t, review.OriginUserInput, req.Context.TranscriptInfo[0].Origin)
	require.False(t, req.Context.TranscriptInfo[0].TextTruncated)
	require.LessOrEqual(t, len([]rune(req.Transcript[1].Content)), 2000)
	require.True(t, req.Context.TranscriptInfo[1].TextTruncated)
}
