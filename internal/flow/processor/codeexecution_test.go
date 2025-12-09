//
//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	iprocessor "trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestCodeExecutionResponseProcessor_EmitsCodeAndResultEvents(t *testing.T) {
	ctx := context.Background()
	proc := iprocessor.NewCodeExecutionResponseProcessor()

	inv := &agent.Invocation{
		Agent:     &testAgent{exec: &stubExec{}},
		Session:   &session.Session{ID: "test-session"},
		AgentName: "test-agent",
	}

	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{Message: model.Message{Role: model.RoleAssistant,
				Content: "```bash\necho hello\n```"}},
		},
	}

	ch := make(chan *event.Event, 4)
	proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, ch)

	if assert.NotEmpty(t, rsp.Choices) {
		assert.Equal(t, "", rsp.Choices[0].Message.Content)
	}
	var evts []*event.Event
	for len(ch) > 0 {
		evts = append(evts, <-ch)
	}
	if assert.Len(t, evts, 2) {
		// Both events have the same Object type (code execution)
		assert.Equal(t, model.ObjectTypePostprocessingCodeExecution,
			evts[0].Response.Object)
		assert.Equal(t, model.ObjectTypePostprocessingCodeExecution,
			evts[1].Response.Object)
		// The distinction is made via the Tag field
		assert.Contains(t, evts[0].Tag, event.CodeExecutionTag)       // code execution event has "code" tag
		assert.Contains(t, evts[1].Tag, event.CodeExecutionResultTag) // result event has "code_execution_result" tag
		codeMsg := evts[0].Response.Choices[0].Message.Content
		assert.Contains(t, codeMsg, "```bash")
		resultMsg := evts[1].Response.Choices[0].Message.Content
		assert.True(t, strings.Contains(resultMsg,
			"Code execution result:") || strings.Contains(resultMsg, "OK"))
	}
}

// stubExec is a simple CodeExecutor stub returning a fixed output
type stubExec struct{}

func (s *stubExec) ExecuteCode(
	ctx context.Context, input codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{Output: "OK"}, nil
}
func (s *stubExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

// testAgent implements agent.Agent and agent.CodeExecutor
type testAgent struct{ exec codeexecutor.CodeExecutor }

// agent.Agent
func (a *testAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	return nil, nil
}
func (a *testAgent) Tools() []tool.Tool                   { return nil }
func (a *testAgent) Info() agent.Info                     { return agent.Info{Name: "test-agent"} }
func (a *testAgent) SubAgents() []agent.Agent             { return nil }
func (a *testAgent) FindSubAgent(name string) agent.Agent { return nil }

func (a *testAgent) CodeExecutor() codeexecutor.CodeExecutor { return a.exec }
