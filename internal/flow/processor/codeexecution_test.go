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
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/calllimit"
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
	cleared := proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, ch)

	// The shared response must stay intact: it may already be published in an
	// emitted event and read by telemetry trackers. The cleared replacement is
	// returned for downstream processing stages instead.
	assert.Equal(t, "```bash\necho hello\n```", rsp.Choices[0].Message.Content)
	if assert.NotNil(t, cleared) && assert.NotEmpty(t, cleared.Choices) {
		assert.Equal(t, "", cleared.Choices[0].Message.Content)
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

func TestCodeExecutionResponseProcessor_SkipsCallLimitFinalization(
	t *testing.T,
) {
	instruction := "return the final answer without executing code"
	tests := []struct {
		name     string
		activate func(*testing.T, *agent.Invocation)
	}{
		{
			name: "llm call limit",
			activate: func(t *testing.T, inv *agent.Invocation) {
				inv.MaxLLMCalls = 1
				calllimit.Configure(inv, &instruction, nil)
				require.True(t,
					calllimit.RecordLLMCall(inv, inv.MaxLLMCalls))
				_, active := calllimit.ActivateForLLM(inv, true)
				require.True(t, active)
			},
		},
		{
			name: "tool iteration limit",
			activate: func(t *testing.T, inv *agent.Invocation) {
				inv.MaxToolIterations = 1
				calllimit.Configure(inv, nil, &instruction)
				require.True(t, calllimit.RecordToolIteration(
					inv, inv.MaxToolIterations))
				calllimit.ScheduleToolFinalization(inv)
				_, active := calllimit.ActivateForLLM(inv, false)
				require.True(t, active)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			proc := iprocessor.NewCodeExecutionResponseProcessor()
			exec := &stubExec{}
			inv := &agent.Invocation{
				Agent:     &testAgent{exec: exec},
				Session:   &session.Session{ID: "test-session"},
				AgentName: "test-agent",
			}
			tt.activate(t, inv)
			content := "```bash\necho hello\n```"
			rsp := &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: content,
					},
				}},
			}
			ch := make(chan *event.Event, 4)

			proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, ch)

			require.Zero(t, exec.calls)
			require.Len(t, ch, 0)
			require.Equal(t, content, rsp.Choices[0].Message.Content)
		})
	}
}

func TestCodeExecutionResponseProcessor_SkipsNonExecutableBlocks(
	t *testing.T,
) {
	t.Parallel()

	ctx := context.Background()
	proc := iprocessor.NewCodeExecutionResponseProcessor()

	cases := []struct {
		name    string
		content string
	}{
		{
			name: "text around block",
			content: "Here you go:\n```bash\n" +
				"echo hello\n```",
		},
		{
			name: "markdown block",
			content: "```markdown\n" +
				"# title\n```",
		},
		{
			name:    "plain unlabeled block",
			content: "```\nhello\n```",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			inv := &agent.Invocation{
				Agent:     &testAgent{exec: &stubExec{}},
				Session:   &session.Session{ID: "test-session"},
				AgentName: "test-agent",
			}
			rsp := &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: tc.content,
					},
				}},
			}

			ch := make(chan *event.Event, 4)
			proc.ProcessResponse(
				ctx,
				inv,
				&model.Request{},
				rsp,
				ch,
			)

			require.Len(t, ch, 0)
			require.Len(t, rsp.Choices, 1)
			require.Equal(
				t,
				tc.content,
				rsp.Choices[0].Message.Content,
			)
		})
	}
}

func TestCodeExecutionResponseProcessor_UsesRunCodeExecutorOverride(
	t *testing.T,
) {
	ctx := context.Background()
	proc := iprocessor.NewCodeExecutionResponseProcessor()

	inv := &agent.Invocation{
		Agent: &testAgent{
			exec: &stubExec{output: "static"},
		},
		Session:   &session.Session{ID: "test-session"},
		AgentName: "test-agent",
		RunOptions: agent.RunOptions{
			CodeExecutor: &stubExec{output: "override"},
		},
	}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "```bash\necho hello\n```",
			}},
		},
	}

	ch := make(chan *event.Event, 4)
	proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, ch)

	require.Len(t, ch, 2)
	<-ch
	result := <-ch
	require.NotNil(t, result)
	require.NotNil(t, result.Response)
	require.Len(t, result.Response.Choices, 1)
	require.Contains(t, result.Response.Choices[0].Message.Content, "override")
}

func TestCodeExecutionResponseProcessor_UsesSharedWorkspaceSessionKey(
	t *testing.T,
) {
	ctx := context.Background()
	proc := iprocessor.NewCodeExecutionResponseProcessor()

	cases := []struct {
		name string
		sess *session.Session
		want string
	}{
		{
			name: "session id only",
			sess: &session.Session{ID: "test-session"},
			want: "test-session",
		},
		{
			name: "full session key",
			sess: &session.Session{
				AppName: "test-app",
				UserID:  "test-user",
				ID:      "test-session",
			},
			want: "test-app/test-user/test-session",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exec := &stubExec{}
			inv := &agent.Invocation{
				Agent:     &testAgent{exec: exec},
				Session:   tc.sess,
				AgentName: "test-agent",
			}
			rsp := &model.Response{
				Done: true,
				Choices: []model.Choice{
					{Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "```bash\necho hello\n```",
					}},
				},
			}

			ch := make(chan *event.Event, 4)
			proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, ch)

			require.Equal(t, tc.want, exec.lastInput.ExecutionID)
		})
	}
}

// stubExec is a simple CodeExecutor stub returning a fixed output
type stubExec struct {
	output    string
	lastInput codeexecutor.CodeExecutionInput
	calls     int
}

func (s *stubExec) ExecuteCode(
	ctx context.Context, input codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	s.calls++
	s.lastInput = input
	output := s.output
	if output == "" {
		output = "OK"
	}
	return codeexecutor.CodeExecutionResult{Output: output}, nil
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

type errExec struct{ calls int }

func (e *errExec) ExecuteCode(
	ctx context.Context, input codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	e.calls++
	return codeexecutor.CodeExecutionResult{}, errors.New("exec failed")
}

func (e *errExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func TestCodeExecutionResponseProcessor_NilInvocation(t *testing.T) {
	proc := iprocessor.NewCodeExecutionResponseProcessor()
	rsp := &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "x"}}}}
	got := proc.ProcessResponse(context.Background(), nil, nil, rsp, make(chan *event.Event, 1))
	require.Same(t, rsp, got)
}

func TestCodeExecutionResponseProcessor_NoExecutorConfigured(t *testing.T) {
	proc := iprocessor.NewCodeExecutionResponseProcessor()
	inv := &agent.Invocation{
		Agent:     &testAgent{exec: nil},
		Session:   &session.Session{ID: "s"},
		AgentName: "test-agent",
	}
	rsp := &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "```bash\necho hi\n```"}}}}
	got := proc.ProcessResponse(context.Background(), inv, &model.Request{}, rsp, make(chan *event.Event, 1))
	require.Same(t, rsp, got)
}

func TestCodeExecutionResponseProcessor_EmptyChoices(t *testing.T) {
	proc := iprocessor.NewCodeExecutionResponseProcessor()
	exec := &stubExec{}
	inv := &agent.Invocation{
		Agent:     &testAgent{exec: exec},
		Session:   &session.Session{ID: "s"},
		AgentName: "test-agent",
	}
	rsp := &model.Response{}
	got := proc.ProcessResponse(context.Background(), inv, &model.Request{}, rsp, make(chan *event.Event, 1))
	require.Same(t, rsp, got)
	require.Zero(t, exec.calls)
}

func TestCodeExecutionResponseProcessor_ExecutionErrorReturnsOriginal(t *testing.T) {
	proc := iprocessor.NewCodeExecutionResponseProcessor()
	exec := &errExec{}
	inv := &agent.Invocation{
		Agent:     &testAgent{exec: exec},
		Session:   &session.Session{ID: "s"},
		AgentName: "test-agent",
	}
	content := "```bash\necho hi\n```"
	rsp := &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: content}}}}
	got := proc.ProcessResponse(context.Background(), inv, &model.Request{}, rsp, make(chan *event.Event, 2))
	require.Same(t, rsp, got)
	require.Equal(t, content, rsp.Choices[0].Message.Content)
	require.Equal(t, 1, exec.calls)
}
