//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codex

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// scriptedRunner is a deterministic commandRunner used for unit tests.
type scriptedRunner struct {
	mu    sync.Mutex
	calls []command
	run   func(command) ([]byte, []byte, error)
}

// Run implements commandRunner.
func (r *scriptedRunner) Run(ctx context.Context, cmd command) ([]byte, []byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, cmd)
	run := r.run
	r.mu.Unlock()
	if run == nil {
		return nil, nil, nil
	}
	return run(cmd)
}

// Calls returns a snapshot of captured command invocations.
func (r *scriptedRunner) Calls() []command {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]command, len(r.calls))
	copy(out, r.calls)
	return out
}

// drainEvents collects all events from ch until it is closed.
func drainEvents(ch <-chan *event.Event) []*event.Event {
	var events []*event.Event
	for e := range ch {
		events = append(events, e)
	}
	return events
}

// newTestInvocation creates a test invocation with a user prompt.
func newTestInvocation(invocationID string, sess *session.Session, prompt string) *agent.Invocation {
	return &agent.Invocation{
		InvocationID: invocationID,
		Session:      sess,
		Message: model.Message{
			Role:    model.RoleUser,
			Content: prompt,
		},
	}
}

// codexTranscript returns a basic Codex JSONL transcript.
func codexTranscript(threadID string, final string) string {
	return `{"type":"thread.started","thread_id":"` + threadID + `"}
{"type":"turn.started"}
{"type":"item.started","item":{"id":"item_0","type":"command_execution","command":"/usr/bin/bash -lc 'printf hi'","aggregated_output":"","exit_code":null,"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_0","type":"command_execution","command":"/usr/bin/bash -lc 'printf hi'","aggregated_output":"hi","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"` + final + `"}}
{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":2,"output_tokens":3,"reasoning_output_tokens":1}}`
}

func TestCodexAgent_Run_CreateParsesEventsAndStoresThread(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-1")
	inv := newTestInvocation("inv-1", sess, "Run printf.")
	transcript := codexTranscript("thread-1", "done")
	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			return []byte(transcript), nil, nil
		},
	}
	ag, err := New(WithBin("codex"), withCommandRunner(runner))
	require.NoError(t, err)
	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	events := drainEvents(ch)
	require.Len(t, events, 3)
	require.True(t, events[0].IsToolCallResponse())
	require.True(t, events[1].IsToolResultResponse())
	require.True(t, events[2].IsFinalResponse())
	require.Equal(t, "command_execution", events[0].Choices[0].Message.ToolCalls[0].Function.Name)
	require.Equal(t, `{"command":"/usr/bin/bash -lc 'printf hi'"}`, string(events[0].Choices[0].Message.ToolCalls[0].Function.Arguments))
	require.Equal(t, "hi", events[1].Choices[0].Message.Content)
	require.Equal(t, "done", events[2].Choices[0].Message.Content)
	require.Equal(t, 10, events[2].Usage.PromptTokens)
	require.Equal(t, 3, events[2].Usage.CompletionTokens)
	require.Equal(t, 13, events[2].Usage.TotalTokens)
	require.Equal(t, 2, events[2].Usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 1, events[2].Usage.CompletionTokensDetails.ReasoningTokens)
	require.Equal(t, []byte("thread-1"), events[2].StateDelta[StateKeyThreadID])
	calls := runner.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "codex", calls[0].bin)
	require.Equal(t, []string{"exec", "--json", "Run printf."}, calls[0].args)
}

func TestCodexAgent_Run_ResumeFromSessionState(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-2")
	sess.SetState(StateKeyThreadID, []byte("thread-1"))
	inv := newTestInvocation("inv-2", sess, "Hi.")
	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			return []byte(`{"type":"thread.started","thread_id":"thread-1"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"hello"}}`), nil, nil
		},
	}
	ag, err := New(withCommandRunner(runner))
	require.NoError(t, err)
	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	events := drainEvents(ch)
	require.Len(t, events, 1)
	require.Equal(t, "hello", events[0].Choices[0].Message.Content)
	require.Empty(t, events[0].StateDelta)
	calls := runner.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, []string{"exec", "resume", "--json", "thread-1", "Hi."}, calls[0].args)
}

func TestCodexAgent_Run_ResumeErrorFallsBackToCreate(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-3")
	sess.SetState(StateKeyThreadID, []byte("stale-thread"))
	inv := newTestInvocation("inv-3", sess, "Hi.")
	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			if len(cmd.args) > 1 && cmd.args[1] == "resume" {
				return nil, []byte("resume unavailable"), errors.New("exit 1")
			}
			return []byte(`{"type":"thread.started","thread_id":"thread-2"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"fresh"}}`), nil, nil
		},
	}
	ag, err := New(withCommandRunner(runner))
	require.NoError(t, err)
	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	events := drainEvents(ch)
	require.Len(t, events, 1)
	require.Equal(t, "fresh", events[0].Choices[0].Message.Content)
	require.Equal(t, []byte("thread-2"), events[0].StateDelta[StateKeyThreadID])
	calls := runner.Calls()
	require.Len(t, calls, 2)
	require.Equal(t, []string{"exec", "resume", "--json", "stale-thread", "Hi."}, calls[0].args)
	require.Equal(t, []string{"exec", "--json", "Hi."}, calls[1].args)
}

func TestCodexAgent_Run_ResumeAndCreateErrorsReturnRunError(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-4")
	sess.SetState(StateKeyThreadID, []byte("thread-1"))
	inv := newTestInvocation("inv-4", sess, "Hi.")
	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			if len(cmd.args) > 1 && cmd.args[1] == "resume" {
				return nil, []byte("resume unavailable"), errors.New("resume exit 1")
			}
			return nil, []byte("create unavailable"), errors.New("create exit 1")
		},
	}
	ag, err := New(withCommandRunner(runner))
	require.NoError(t, err)
	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	events := drainEvents(ch)
	require.Len(t, events, 1)
	require.NotNil(t, events[0].Error)
	require.Equal(t, model.ErrorTypeRunError, events[0].Error.Type)
	require.Equal(t, "create unavailable", events[0].Error.Message)
	calls := runner.Calls()
	require.Len(t, calls, 2)
	require.Equal(t, []string{"exec", "resume", "--json", "thread-1", "Hi."}, calls[0].args)
	require.Equal(t, []string{"exec", "--json", "Hi."}, calls[1].args)
}

func TestCodexAgent_Run_RawOutputHook(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-hook-1")
	inv := newTestInvocation("inv-hook-1", sess, "Hi.")
	transcript := `{"type":"thread.started","thread_id":"thread-hook"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"hello"}}`
	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			return []byte(transcript), []byte("warn\n"), nil
		},
	}
	var got RawOutputHookArgs
	var called bool
	ag, err := New(
		withCommandRunner(runner),
		WithRawOutputHook(func(_ context.Context, args *RawOutputHookArgs) error {
			called = true
			if args != nil {
				got = *args
			}
			return nil
		}),
	)
	require.NoError(t, err)
	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	_ = drainEvents(ch)
	require.True(t, called)
	require.Equal(t, "inv-hook-1", got.InvocationID)
	require.Equal(t, "sess-hook-1", got.SessionID)
	require.Empty(t, got.ResumeThreadID)
	require.Equal(t, "thread-hook", got.ThreadID)
	require.Equal(t, "Hi.", got.Prompt)
	require.Equal(t, transcript, string(got.Stdout))
	require.Equal(t, "warn\n", string(got.Stderr))
	require.NoError(t, got.Error)
}

func TestCodexAgent_Run_RawOutputHookError(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-hook-2")
	inv := newTestInvocation("inv-hook-2", sess, "Hi.")
	hookErr := errors.New("hook failed")
	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			return []byte(codexTranscript("thread-hook-2", "hello")), []byte("warn\n"), nil
		},
	}
	ag, err := New(
		withCommandRunner(runner),
		WithRawOutputHook(func(_ context.Context, _ *RawOutputHookArgs) error {
			return hookErr
		}),
	)
	require.NoError(t, err)
	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	events := drainEvents(ch)
	require.Len(t, events, 1)
	require.True(t, events[0].IsFinalResponse())
	require.Equal(t, model.ObjectTypeError, events[0].Object)
	require.NotNil(t, events[0].Error)
	require.Equal(t, model.ErrorTypeFlowError, events[0].Error.Type)
	require.Contains(t, events[0].Error.Message, "raw output hook")
	require.Contains(t, events[0].Error.Message, "hook failed")
	require.Contains(t, events[0].Choices[0].Message.Content, "thread-hook-2")
	require.Contains(t, events[0].Choices[0].Message.Content, "warn")
}

func TestCodexAgent_InfoAndRunnerArgs(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-info-1")
	inv := newTestInvocation("inv-info-1", sess, "Hi.")
	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			return []byte(`{"type":"thread.started","thread_id":"thread-info"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"ok"}}`), nil, nil
		},
	}
	ag, err := New(
		WithName("my-codex"),
		WithBin("codex-bin"),
		WithGlobalArgs("--ask-for-approval", "never"),
		WithExtraArgs("--sandbox", "read-only"),
		WithEnv("CODEX_AGENT_TEST=1"),
		WithWorkDir("/tmp"),
		withCommandRunner(runner),
	)
	require.NoError(t, err)
	info := ag.Info()
	require.Equal(t, "my-codex", info.Name)
	require.NotEmpty(t, info.Description)
	require.Nil(t, ag.Tools())
	require.Nil(t, ag.SubAgents())
	require.Nil(t, ag.FindSubAgent("anything"))
	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	events := drainEvents(ch)
	require.NotEmpty(t, events)
	require.True(t, events[len(events)-1].IsFinalResponse())
	calls := runner.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "codex-bin", calls[0].bin)
	require.Equal(t, "/tmp", calls[0].dir)
	require.Contains(t, calls[0].env, "CODEX_AGENT_TEST=1")
	require.Equal(t, []string{"--ask-for-approval", "never", "exec", "--sandbox", "read-only", "--json", "Hi."}, calls[0].args)
}

func TestNew_ValidationErrors(t *testing.T) {
	_, err := New(WithBin(""))
	require.Error(t, err)
	_, err = New(withCommandRunner(nil))
	require.Error(t, err)
}

func TestExecCommandRunner_Run(t *testing.T) {
	tmp := t.TempDir()
	runner := execCommandRunner{}
	stdout, stderr, err := runner.Run(context.Background(), command{
		bin:  "sh",
		args: []string{"-c", "printf \"$FOO\""},
		env:  []string{"FOO=bar"},
	})
	require.NoError(t, err)
	require.Equal(t, "bar", string(stdout))
	require.Empty(t, string(stderr))
	stdout, stderr, err = runner.Run(context.Background(), command{
		bin:  "sh",
		args: []string{"-c", "pwd"},
		dir:  tmp,
	})
	require.NoError(t, err)
	got := strings.TrimSpace(string(stdout))
	gotResolved, err := filepath.EvalSymlinks(got)
	require.NoError(t, err)
	wantResolved, err := filepath.EvalSymlinks(tmp)
	require.NoError(t, err)
	require.Equal(t, wantResolved, gotResolved)
	require.Empty(t, string(stderr))
}

func TestParseTranscriptEvents_CompletedToolWithoutStarted(t *testing.T) {
	transcript := `{"type":"thread.started","thread_id":"thread-parse"}
{"type":"item.completed","item":{"id":"item_0","type":"command_execution","command":"pwd","aggregated_output":"/tmp\n","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"done"}}`
	result, err := parseTranscriptEvents([]byte(transcript), "inv-parse-1", "codex")
	require.NoError(t, err)
	require.Equal(t, "thread-parse", result.ThreadID)
	require.Equal(t, "done", result.FinalMessage)
	require.Len(t, result.Events, 2)
	require.True(t, result.Events[0].IsToolCallResponse())
	require.True(t, result.Events[1].IsToolResultResponse())
	require.Equal(t, "pwd", commandArg(t, result.Events[0]))
	require.Equal(t, "/tmp\n", result.Events[1].Choices[0].Message.Content)
}

func TestParseTranscriptEvents_MCPToolCallMapping(t *testing.T) {
	transcript := `{"type":"item.started","item":{"id":"item_mcp","type":"mcp_tool_call","server":"calc","tool":"add","arguments":{"a":1,"b":2}}}
{"type":"item.completed","item":{"id":"item_mcp","type":"mcp_tool_call","server":"calc","tool":"add","arguments":{"a":1,"b":2},"result":"3","status":"completed"}}
{"type":"turn.completed","usage":{"input_tokens":4,"output_tokens":5}}`
	result, err := parseTranscriptEvents([]byte(transcript), "inv-parse-2", "codex")
	require.NoError(t, err)
	require.Len(t, result.Events, 2)
	require.Equal(t, "mcp__calc__add", result.Events[0].Choices[0].Message.ToolCalls[0].Function.Name)
	require.Equal(t, "mcp__calc__add", result.Events[1].Choices[0].Message.ToolName)
	require.JSONEq(t, `{"a":1,"b":2}`, string(result.Events[0].Choices[0].Message.ToolCalls[0].Function.Arguments))
	require.Equal(t, "3", result.Events[1].Choices[0].Message.Content)
	require.Equal(t, 9, result.Usage.TotalTokens)
}

func TestParseTranscriptEvents_BuiltInToolMapping(t *testing.T) {
	transcript := `{"type":"item.started","item":{"id":"item_search","type":"web_search","query":"trpc agent"}}
{"type":"item.completed","item":{"id":"item_search","type":"web_search","query":"trpc agent","result":[{"title":"doc"}],"status":"completed"}}
{"type":"item.started","item":{"id":"item_file","type":"file_change","changes":{"path":"main.go","kind":"update"}}}
{"type":"item.completed","item":{"id":"item_file","type":"file_change","changes":{"path":"main.go","kind":"update"},"status":"completed"}}
{"type":"item.started","item":{"id":"item_image","type":"image_generation","prompt":"draw icon"}}
{"type":"item.completed","item":{"id":"item_image","type":"image_generation","saved_path":"/tmp/icon.png","revised_prompt":"draw a clean icon","status":"completed"}}`
	result, err := parseTranscriptEvents([]byte(transcript), "inv-parse-builtins-1", "codex")
	require.NoError(t, err)
	require.Len(t, result.Events, 6)
	require.Equal(t, "web_search", result.Events[0].Choices[0].Message.ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"query":"trpc agent"}`, string(result.Events[0].Choices[0].Message.ToolCalls[0].Function.Arguments))
	require.JSONEq(t, `[{"title":"doc"}]`, result.Events[1].Choices[0].Message.Content)
	require.Equal(t, "file_change", result.Events[2].Choices[0].Message.ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"path":"main.go","kind":"update"}`, string(result.Events[2].Choices[0].Message.ToolCalls[0].Function.Arguments))
	require.JSONEq(t, `{"path":"main.go","kind":"update"}`, result.Events[3].Choices[0].Message.Content)
	require.Equal(t, "image_generation", result.Events[4].Choices[0].Message.ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"prompt":"draw icon"}`, string(result.Events[4].Choices[0].Message.ToolCalls[0].Function.Arguments))
	require.JSONEq(t, `{"saved_path":"/tmp/icon.png","revised_prompt":"draw a clean icon","status":"completed"}`, result.Events[5].Choices[0].Message.Content)
}

func TestParseTranscriptEvents_SkillToolMapping(t *testing.T) {
	transcript := `{"type":"item.started","item":{"id":"item_skill","type":"skill","skill":"debug"}}
{"type":"item.completed","item":{"id":"item_skill","type":"skill","skill":"debug","result":"ok","status":"completed"}}`
	result, err := parseTranscriptEvents([]byte(transcript), "inv-parse-skill-1", "codex")
	require.NoError(t, err)
	require.Len(t, result.Events, 2)
	require.True(t, result.Events[0].IsToolCallResponse())
	require.True(t, result.Events[1].IsToolResultResponse())
	require.Equal(t, "skill_run", result.Events[0].Choices[0].Message.ToolCalls[0].Function.Name)
	require.Equal(t, "skill_run", result.Events[1].Choices[0].Message.ToolName)
	var directArgs skillRunArgs
	require.NoError(t, json.Unmarshal(result.Events[0].Choices[0].Message.ToolCalls[0].Function.Arguments, &directArgs))
	require.Equal(t, "debug", directArgs.Skill)
	require.Equal(t, "", directArgs.Command)
	require.Equal(t, "ok", result.Events[1].Choices[0].Message.Content)
}

func TestParseTranscriptEvents_EmptyAndInvalidOutput(t *testing.T) {
	result, err := parseTranscriptEvents(nil, "inv-empty", "codex")
	require.NoError(t, err)
	require.Empty(t, result.FinalMessage)
	require.Empty(t, result.Events)
	_, err = parseTranscriptEvents([]byte(`not-json`), "inv-invalid", "codex")
	require.Error(t, err)
}

func commandArg(t *testing.T, evt *event.Event) string {
	t.Helper()
	type args struct {
		Command string `json:"command"`
	}
	var got args
	require.NoError(t, json.Unmarshal(evt.Choices[0].Message.ToolCalls[0].Function.Arguments, &got))
	return got.Command
}
