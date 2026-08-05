//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/awaitreply"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

// safeStubTool has no IsConcurrencySafe method, so it takes the default.
type safeStubTool struct{ name string }

func (s safeStubTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: s.name} }

// unsafeStubTool objects to sharing a turn through the narrow
// tool.ConcurrencyAware interface, as the transfer and await_user_reply tools do.
type unsafeStubTool struct{ name string }

func (u unsafeStubTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: u.name} }
func (u unsafeStubTool) IsConcurrencySafe() bool        { return false }

// metadataStubTool publishes ToolMetadata but implements no concurrency
// interface, which is what an external tool written against the descriptive
// metadata contract looks like.
type metadataStubTool struct {
	name string
	safe bool
}

func (m metadataStubTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: m.name} }
func (m metadataStubTool) ToolMetadata() tool.ToolMetadata {
	return tool.ToolMetadata{ReadOnly: true, ConcurrencySafe: m.safe}
}

func callsFor(names ...string) []model.ToolCall {
	calls := make([]model.ToolCall, 0, len(names))
	for _, n := range names {
		calls = append(calls, model.ToolCall{
			Function: model.FunctionDefinitionParam{Name: n},
		})
	}
	return calls
}

func TestHasConcurrentBatch(t *testing.T) {
	// The invocation carries a sub-agent named "child" and the request declares
	// transfer_to_agent, which is what makes findCompatibleTool map a bare
	// "child" call onto the transfer tool.
	child := &mockAgent{name: "child"}
	invocation := &agent.Invocation{Agent: &parentAgent{child: child}}

	tools := map[string]tool.Tool{
		"read":  safeStubTool{name: "read"},
		"grep":  safeStubTool{name: "grep"},
		"stop":  unsafeStubTool{name: "stop"},
		"meta":  metadataStubTool{name: "meta", safe: true},
		"metaX": metadataStubTool{name: "metaX", safe: false},
		transfer.TransferToolName: itool.ApplyDeclarations(
			[]tool.Tool{transfer.New([]agent.Info{child.Info()})},
			[]tool.Declaration{{
				Name:        transfer.TransferToolName,
				Description: "patched by a host surface",
			}},
		)[0],
	}

	tests := []struct {
		name      string
		toolCalls []model.ToolCall
		// tools overrides the shared request surface; nil means use it as is.
		tools map[string]tool.Tool
		want  bool
	}{
		{
			name:      "several admissible calls batch",
			toolCalls: callsFor("read", "grep"),
			want:      true,
		},
		{
			// One objection sends the whole turn sequential rather than splitting
			// it, so the model's requested ordering is preserved.
			name:      "one objecting call disqualifies the batch",
			toolCalls: callsFor("read", "stop"),
			want:      false,
		},
		{
			name:      "objecting call first also disqualifies",
			toolCalls: callsFor("stop", "read"),
			want:      false,
		},
		{
			// A lone call has nothing to run beside it.
			name:      "single call is not a batch",
			toolCalls: callsFor("read"),
			want:      false,
		},
		{
			name:      "no calls",
			toolCalls: nil,
			want:      false,
		},
		{
			// Undeclared tools never execute; they produce a terminal error result
			// and must not constrain their siblings.
			name:      "undeclared tool does not disqualify",
			toolCalls: callsFor("read", "missing"),
			want:      true,
		},
		{
			// ToolMetadata is descriptive and its zero value is indistinguishable
			// from an unset one, so a tool publishing only metadata never objects —
			// otherwise every external tool that publishes a ReadOnly hint would
			// silently take its turn off the parallel path.
			name:      "metadata alone never objects",
			toolCalls: callsFor("meta", "metaX"),
			want:      true,
		},
		{
			// An exact transfer_to_agent call must be caught even though a host
			// surface patched its declaration: the overlay wrapper implements no
			// optional interfaces of its own.
			name:      "patched transfer tool still objects",
			toolCalls: callsFor("read", transfer.TransferToolName),
			want:      false,
		},
		{
			// The model may name a sub-agent directly. Execution maps that onto
			// transfer_to_agent, so admission has to resolve the same mapping or it
			// checks a name no tool answers to.
			name:      "sub-agent name resolves to the transfer tool",
			toolCalls: callsFor("read", "child"),
			want:      false,
		},
		{
			// Same call, but with no transfer tool in the request there is nothing
			// to map onto and nothing that can object.
			name:      "sub-agent name without a transfer tool is undeclared",
			toolCalls: callsFor("read", "child"),
			tools:     map[string]tool.Tool{"read": safeStubTool{name: "read"}},
			want:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			batchTools := tt.tools
			if batchTools == nil {
				batchTools = tools
			}
			if got := hasConcurrentBatch(tt.toolCalls, batchTools, invocation); got != tt.want {
				t.Errorf("hasConcurrentBatch() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A batched transfer must still hand off.
//
// The transfer tool records the handoff by assigning Invocation.TransferInfo, and
// TransferResponseProcessor reads that field off the base invocation afterwards.
// The parallel path gives each call its own invocation view and never syncs one
// back, so admitting a transfer there loses the assignment: the tool returns
// Success: true and the conversation stays with the current agent. Admission has
// to keep the turn sequential for the field to survive.
func TestBatchedTransferReachesBaseInvocation(t *testing.T) {
	const (
		agentName  = "tester"
		plainName  = "read"
		transferID = "call-transfer"
		plainID    = "call-read"
	)
	child := &mockAgent{name: "child"}
	p := NewFunctionCallResponseProcessor(true, nil)
	inv := &agent.Invocation{
		AgentName: agentName,
		Agent:     &parentAgent{child: child},
		Model:     &mockModel{},
		Session:   &session.Session{},
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)
	tools := map[string]tool.Tool{
		plainName:                 safeStubTool{name: plainName},
		transfer.TransferToolName: transfer.New([]agent.Info{child.Info()}),
	}

	for _, tt := range []struct {
		name         string
		transferCall model.ToolCall
	}{
		{
			name: "exact transfer_to_agent call",
			transferCall: model.ToolCall{
				ID: transferID,
				Function: model.FunctionDefinitionParam{
					Name:      transfer.TransferToolName,
					Arguments: []byte(`{"agent_name":"child"}`),
				},
			},
		},
		{
			// The model may name the sub-agent directly; execution maps that onto
			// transfer_to_agent, so admission must resolve the same mapping.
			name: "sub-agent name mapped to the transfer tool",
			transferCall: model.ToolCall{
				ID: transferID,
				Function: model.FunctionDefinitionParam{
					Name:      "child",
					Arguments: []byte(`{"request":"take over"}`),
				},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			inv.TransferInfo = nil
			toolCalls := []model.ToolCall{
				{
					ID: plainID,
					Function: model.FunctionDefinitionParam{
						Name:      plainName,
						Arguments: []byte(`{}`),
					},
				},
				tt.transferCall,
			}
			_, err := p.handleFunctionCalls(
				ctx,
				inv,
				newToolCallResponseWithCalls(toolCalls),
				tools,
				nil,
			)
			require.NoError(t, err)
			require.NotNil(
				t,
				inv.TransferInfo,
				"the handoff was recorded on a discarded invocation view",
			)
			require.Equal(t, "child", inv.TransferInfo.TargetAgentName)
			require.False(
				t,
				hasConcurrentBatch(toolCalls, tools, inv),
				"a turn containing a transfer must not be admitted to the parallel path",
			)
		})
	}
}

// A batched await_user_reply must still stage its resume route, for the same
// reason: MarkAwaitingUserReply writes to the invocation's own state, and a
// worker's cloned state is discarded when the worker finishes.
func TestBatchedAwaitUserReplyReachesBaseInvocation(t *testing.T) {
	const (
		agentName = "tester"
		plainName = "read"
	)
	p := NewFunctionCallResponseProcessor(true, nil)
	inv := &agent.Invocation{
		AgentName: agentName,
		Model:     &mockModel{},
		Session:   &session.Session{},
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)
	tools := map[string]tool.Tool{
		plainName:           safeStubTool{name: plainName},
		awaitreply.ToolName: awaitreply.New(),
	}
	toolCalls := []model.ToolCall{
		{
			ID: "call-read",
			Function: model.FunctionDefinitionParam{
				Name:      plainName,
				Arguments: []byte(`{}`),
			},
		},
		{
			ID: "call-await",
			Function: model.FunctionDefinitionParam{
				Name:      awaitreply.ToolName,
				Arguments: []byte(`{}`),
			},
		},
	}

	_, err := p.handleFunctionCalls(
		ctx,
		inv,
		newToolCallResponseWithCalls(toolCalls),
		tools,
		nil,
	)
	require.NoError(t, err)
	route, ok := agent.CurrentAwaitUserReplyRoute(inv)
	require.True(t, ok, "the resume route was staged on a discarded invocation view")
	require.Equal(t, agentName, route.AgentName)
	require.False(
		t,
		hasConcurrentBatch(toolCalls, tools, inv),
		"a turn containing await_user_reply must not be admitted to the parallel path",
	)
}
