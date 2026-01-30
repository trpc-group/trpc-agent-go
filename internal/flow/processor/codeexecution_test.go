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

const payloadStateKey = "processor:code_execution_payload"

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
	iprocessor.PrepareCodeExecutionResponse(inv, rsp)
	proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, ch)

	if assert.NotEmpty(t, rsp.Choices) {
		assert.Equal(t, "", rsp.Choices[0].Message.Content)
	}
	var evts []*event.Event
	for len(ch) > 0 {
		evts = append(evts, <-ch)
	}
	if assert.Len(t, evts, 2) {
		// Both events have the same Object type (code execution).
		assert.Equal(t, model.ObjectTypePostprocessingCodeExecution,
			evts[0].Response.Object)
		assert.Equal(t, model.ObjectTypePostprocessingCodeExecution,
			evts[1].Response.Object)
		// The distinction is made via the Tag field.
		assert.Contains(t, evts[0].Tag, event.CodeExecutionTag)       // The code execution event has the "code" tag.
		assert.Contains(t, evts[1].Tag, event.CodeExecutionResultTag) // The result event has the "code_execution_result" tag.
		codeMsg := evts[0].Response.Choices[0].Message.Content
		assert.Contains(t, codeMsg, "```bash")
		resultMsg := evts[1].Response.Choices[0].Message.Content
		assert.True(t, strings.Contains(resultMsg,
			"Code execution result:") || strings.Contains(resultMsg, "OK"))
	}
}

func TestCodeExecutionResponseProcessor_NoPayload_NoEvents(t *testing.T) {
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

	assert.Empty(t, ch)
	assert.Equal(t, "```bash\necho hello\n```", rsp.Choices[0].Message.Content)
}

func TestCodeExecutionResponseProcessor_ExecuteError_EmitsErrorResultEvent(t *testing.T) {
	ctx := context.Background()
	proc := iprocessor.NewCodeExecutionResponseProcessor()

	inv := &agent.Invocation{
		Agent:     &testAgent{exec: &failingExec{err: assert.AnError}},
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
	iprocessor.PrepareCodeExecutionResponse(inv, rsp)
	proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, ch)

	assert.Equal(t, "", rsp.Choices[0].Message.Content)

	var evts []*event.Event
	for len(ch) > 0 {
		evts = append(evts, <-ch)
	}
	if assert.Len(t, evts, 2) {
		assert.Contains(t, evts[0].Tag, event.CodeExecutionTag)
		assert.Contains(t, evts[1].Tag, event.CodeExecutionResultTag)
		assert.Contains(t, evts[1].Response.Choices[0].Message.Content, "Code execution failed:")
	}
}

func TestCodeExecutionResponseProcessor_NoCodeBlocks_NoEvents(t *testing.T) {
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
			{Message: model.Message{Role: model.RoleAssistant, Content: "hello"}},
		},
	}

	ch := make(chan *event.Event, 4)
	iprocessor.PrepareCodeExecutionResponse(inv, rsp)
	proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, ch)

	assert.Equal(t, "hello", rsp.Choices[0].Message.Content)
	assert.Empty(t, ch)
}

func TestPrepareCodeExecutionResponse_NilInvocation_NoMutation(t *testing.T) {
	content := "```bash\necho hello\n```"
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{Role: model.RoleAssistant, Content: content},
			},
		},
	}

	iprocessor.PrepareCodeExecutionResponse(nil, rsp)
	assert.Equal(t, content, rsp.Choices[0].Message.Content)
}

func TestPrepareCodeExecutionResponse_NonCodeExecutor_NoState(t *testing.T) {
	content := "```bash\necho hello\n```"
	inv := &agent.Invocation{
		Agent: &nonCodeExecAgent{},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{Role: model.RoleAssistant, Content: content},
			},
		},
	}

	iprocessor.PrepareCodeExecutionResponse(inv, rsp)
	assert.Equal(t, content, rsp.Choices[0].Message.Content)
	_, ok := inv.GetState(payloadStateKey)
	assert.False(t, ok)
}

func TestPrepareCodeExecutionResponse_NilExecutor_NoState(t *testing.T) {
	content := "```bash\necho hello\n```"
	inv := &agent.Invocation{
		Agent: &testAgent{exec: nil},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{Role: model.RoleAssistant, Content: content},
			},
		},
	}

	iprocessor.PrepareCodeExecutionResponse(inv, rsp)
	assert.Equal(t, content, rsp.Choices[0].Message.Content)
	_, ok := inv.GetState(payloadStateKey)
	assert.False(t, ok)
}

func TestPrepareCodeExecutionResponse_EmptyChoices_NoState(t *testing.T) {
	inv := &agent.Invocation{
		Agent: &testAgent{exec: &stubExec{}},
	}
	rsp := &model.Response{
		Done:    true,
		Choices: nil,
	}

	iprocessor.PrepareCodeExecutionResponse(inv, rsp)
	_, ok := inv.GetState(payloadStateKey)
	assert.False(t, ok)
}

func TestCodeExecutionResponseProcessor_ProcessResponse_Partial_NoEvents(t *testing.T) {
	ctx := context.Background()
	proc := iprocessor.NewCodeExecutionResponseProcessor()

	inv := &agent.Invocation{
		Agent: &testAgent{exec: &stubExec{}},
	}
	rsp := &model.Response{
		Done:      false,
		IsPartial: true,
		Choices: []model.Choice{
			{
				Message: model.Message{Role: model.RoleAssistant, Content: "```bash\necho hello\n```"},
			},
		},
	}

	ch := make(chan *event.Event, 1)
	proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, ch)
	assert.Empty(t, ch)
}

func TestCodeExecutionResponseProcessor_ProcessResponse_WrongStateType_NoEvents(t *testing.T) {
	ctx := context.Background()
	proc := iprocessor.NewCodeExecutionResponseProcessor()

	inv := &agent.Invocation{
		Agent: &testAgent{exec: &stubExec{}},
	}
	inv.SetState(payloadStateKey, "bad")
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{Role: model.RoleAssistant, Content: "```bash\necho hello\n```"},
			},
		},
	}

	ch := make(chan *event.Event, 1)
	proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, ch)
	assert.Empty(t, ch)
}

func TestCodeExecutionResponseProcessor_ProcessResponse_NonCodeExecutor_NoEvents(t *testing.T) {
	ctx := context.Background()
	proc := iprocessor.NewCodeExecutionResponseProcessor()

	inv := &agent.Invocation{
		Agent: &testAgent{exec: &stubExec{}},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{Role: model.RoleAssistant, Content: "```bash\necho hello\n```"},
			},
		},
	}

	ch := make(chan *event.Event, 1)
	iprocessor.PrepareCodeExecutionResponse(inv, rsp)
	inv.Agent = &nonCodeExecAgent{}
	proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, ch)
	assert.Empty(t, ch)
}

func TestCodeExecutionResponseProcessor_ProcessResponse_NilExecutor_NoEvents(t *testing.T) {
	ctx := context.Background()
	proc := iprocessor.NewCodeExecutionResponseProcessor()

	inv := &agent.Invocation{
		Agent: &testAgent{exec: &stubExec{}},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{Role: model.RoleAssistant, Content: "```bash\necho hello\n```"},
			},
		},
	}

	ch := make(chan *event.Event, 1)
	iprocessor.PrepareCodeExecutionResponse(inv, rsp)
	inv.Agent = &testAgent{exec: nil}
	proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, ch)
	assert.Empty(t, ch)
}

func TestCodeExecutionResponseProcessor_ProcessResponse_EmptyChoices_NoEvents(t *testing.T) {
	ctx := context.Background()
	proc := iprocessor.NewCodeExecutionResponseProcessor()

	inv := &agent.Invocation{
		Agent: &testAgent{exec: &stubExec{}},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{Role: model.RoleAssistant, Content: "```bash\necho hello\n```"},
			},
		},
	}
	ch := make(chan *event.Event, 1)
	iprocessor.PrepareCodeExecutionResponse(inv, rsp)

	rspNoChoices := &model.Response{
		Done:    true,
		Choices: nil,
	}
	proc.ProcessResponse(ctx, inv, &model.Request{}, rspNoChoices, ch)
	assert.Empty(t, ch)
}

// stubExec is a simple CodeExecutor stub returning a fixed output.
type stubExec struct{}

func (s *stubExec) ExecuteCode(
	ctx context.Context, input codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{Output: "OK"}, nil
}
func (s *stubExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

type failingExec struct {
	err error
}

func (f *failingExec) ExecuteCode(
	ctx context.Context, input codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, f.err
}

func (f *failingExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

type testAgent struct{ exec codeexecutor.CodeExecutor }

func (a *testAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	return nil, nil
}
func (a *testAgent) Tools() []tool.Tool                   { return nil }
func (a *testAgent) Info() agent.Info                     { return agent.Info{Name: "test-agent"} }
func (a *testAgent) SubAgents() []agent.Agent             { return nil }
func (a *testAgent) FindSubAgent(name string) agent.Agent { return nil }

func (a *testAgent) CodeExecutor() codeexecutor.CodeExecutor { return a.exec }

type nonCodeExecAgent struct{}

func (a *nonCodeExecAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	return nil, nil
}
func (a *nonCodeExecAgent) Tools() []tool.Tool                   { return nil }
func (a *nonCodeExecAgent) Info() agent.Info                     { return agent.Info{Name: "non-codeexec-agent"} }
func (a *nonCodeExecAgent) SubAgents() []agent.Agent             { return nil }
func (a *nonCodeExecAgent) FindSubAgent(name string) agent.Agent { return nil }
