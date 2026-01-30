//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestCodeExecutionResponseProcessor_ProcessResponse_EmptyCodeBlocks_NoEvents(t *testing.T) {
	ctx := context.Background()
	proc := NewCodeExecutionResponseProcessor()

	inv := &agent.Invocation{
		Agent:     &testCodeExecAgent{exec: &testCodeExecExecutor{}},
		AgentName: "test-agent",
	}
	inv.SetState(codeExecutionPayloadStateKey, &codeExecutionPayload{
		truncatedContent: "```bash\necho hello\n```",
		codeBlocks:       nil,
	})

	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{Role: model.RoleAssistant, Content: ""},
			},
		},
	}

	ch := make(chan *event.Event, 1)
	proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, ch)

	assert.Empty(t, ch)
}

type testCodeExecExecutor struct{}

func (e *testCodeExecExecutor) ExecuteCode(
	ctx context.Context, input codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{Output: "OK"}, nil
}

func (e *testCodeExecExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

type testCodeExecAgent struct{ exec codeexecutor.CodeExecutor }

func (a *testCodeExecAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	return nil, nil
}
func (a *testCodeExecAgent) Tools() []tool.Tool                   { return nil }
func (a *testCodeExecAgent) Info() agent.Info                     { return agent.Info{Name: "test-agent"} }
func (a *testCodeExecAgent) SubAgents() []agent.Agent             { return nil }
func (a *testCodeExecAgent) FindSubAgent(name string) agent.Agent { return nil }
func (a *testCodeExecAgent) CodeExecutor() codeexecutor.CodeExecutor {
	return a.exec
}
