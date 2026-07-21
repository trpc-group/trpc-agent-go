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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// scriptedRunner is a deterministic commandRunner used for unit tests.
type scriptedRunner struct {
	mu     sync.Mutex
	calls  []command
	run    func(command) ([]byte, []byte, error)
	stream func(command, commandOutputHandler) commandResult
}

// Run implements commandRunner.
func (r *scriptedRunner) Run(ctx context.Context, cmd command, onStdoutLine commandOutputHandler) commandResult {
	r.mu.Lock()
	r.calls = append(r.calls, cmd)
	run := r.run
	stream := r.stream
	r.mu.Unlock()
	if stream != nil {
		return stream(cmd, onStdoutLine)
	}
	if run == nil {
		return commandResult{}
	}
	stdout, stderr, runErr := run(cmd)
	outputErr := emitScriptedStdout(stdout, onStdoutLine)
	return commandResult{
		stdout:    stdout,
		stderr:    stderr,
		runErr:    runErr,
		outputErr: outputErr,
	}
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

// emitScriptedStdout forwards captured stdout to the command output handler.
func emitScriptedStdout(stdout []byte, onStdoutLine commandOutputHandler) error {
	if onStdoutLine == nil {
		return nil
	}
	for len(stdout) > 0 {
		idx := bytes.IndexByte(stdout, '\n')
		if idx < 0 {
			return onStdoutLine(stdout)
		}
		line := stdout[:idx+1]
		stdout = stdout[idx+1:]
		if err := onStdoutLine(line); err != nil {
			return err
		}
	}
	return nil
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
	require.Len(t, events, 4)
	require.True(t, events[0].IsToolCallResponse())
	require.True(t, events[1].IsToolResultResponse())
	require.False(t, events[2].IsFinalResponse())
	require.True(t, events[2].IsPartial)
	require.Equal(t, model.ObjectTypeChatCompletionChunk, events[2].Object)
	require.True(t, events[3].IsFinalResponse())
	require.Equal(t, "command_execution", events[0].Choices[0].Message.ToolCalls[0].Function.Name)
	require.Equal(t, `{"command":"/usr/bin/bash -lc 'printf hi'"}`, string(events[0].Choices[0].Message.ToolCalls[0].Function.Arguments))
	require.Equal(t, "hi", events[1].Choices[0].Message.Content)
	require.Equal(t, "done", events[2].Choices[0].Delta.Content)
	require.Equal(t, "item_1", events[2].Response.ID)
	require.Equal(t, "done", events[3].Choices[0].Message.Content)
	require.Equal(t, "item_1", events[3].Response.ID)
	require.Equal(t, 10, events[3].Usage.PromptTokens)
	require.Equal(t, 3, events[3].Usage.CompletionTokens)
	require.Equal(t, 13, events[3].Usage.TotalTokens)
	require.Equal(t, 2, events[3].Usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 1, events[3].Usage.CompletionTokensDetails.ReasoningTokens)
	require.Equal(t, []byte("thread-1"), events[3].StateDelta[StateKeyThreadID])
	calls := runner.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "codex", calls[0].bin)
	require.Equal(t, []string{"exec", "--json"}, calls[0].args)
	require.Equal(t, "Run printf.", string(calls[0].stdin))
}

func TestCodexAgent_Run_StreamsToolEventBeforeCommandReturns(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-stream-1")
	inv := newTestInvocation("inv-stream-1", sess, "Run slow command.")
	release := make(chan struct{})
	returned := make(chan struct{})
	firstLine := []byte(`{"type":"item.started","item":{"id":"item_0","type":"command_execution","command":"sleep 1","status":"in_progress"}}` + "\n")
	rest := []byte(`{"type":"item.completed","item":{"id":"item_0","type":"command_execution","command":"sleep 1","aggregated_output":"done","exit_code":0,"status":"completed"}}` + "\n" +
		`{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"finished"}}` + "\n" +
		`{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":2}}` + "\n")
	runner := &scriptedRunner{
		stream: func(cmd command, onStdoutLine commandOutputHandler) commandResult {
			stdout := append([]byte(nil), firstLine...)
			if err := onStdoutLine(firstLine); err != nil {
				close(returned)
				return commandResult{stdout: stdout, outputErr: err}
			}
			<-release
			stdout = append(stdout, rest...)
			outputErr := emitScriptedStdout(rest, onStdoutLine)
			close(returned)
			return commandResult{stdout: stdout, outputErr: outputErr}
		},
	}
	ag, err := New(withCommandRunner(runner))
	require.NoError(t, err)
	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	var first *event.Event
	select {
	case first = <-ch:
	case <-time.After(time.Second):
		require.FailNow(t, "timed out waiting for streamed tool event")
	}
	require.True(t, first.IsToolCallResponse())
	require.Equal(t, "sleep 1", commandArg(t, first))
	select {
	case <-returned:
		require.FailNow(t, "command returned before the test released it")
	default:
	}
	close(release)
	events := append([]*event.Event{first}, drainEvents(ch)...)
	require.Len(t, events, 4)
	require.True(t, events[1].IsToolResultResponse())
	require.Equal(t, "done", events[1].Choices[0].Message.Content)
	require.False(t, events[2].IsFinalResponse())
	require.True(t, events[2].IsPartial)
	require.Equal(t, "finished", events[2].Choices[0].Delta.Content)
	require.True(t, events[3].IsFinalResponse())
	require.Equal(t, "finished", events[3].Choices[0].Message.Content)
	require.Equal(t, 3, events[3].Usage.TotalTokens)
}

func TestCodexAgent_Run_StreamsAssistantMessagesAroundToolEvents(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-stream-2")
	inv := newTestInvocation("inv-stream-2", sess, "Say, run, say.")
	release := make(chan struct{})
	returned := make(chan struct{})
	firstLine := []byte(`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"good luck"}}` + "\n")
	rest := []byte(`{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"sleep 1","status":"in_progress"}}` + "\n" +
		`{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"sleep 1","aggregated_output":"done","exit_code":0,"status":"completed"}}` + "\n" +
		`{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"practice makes perfect"}}` + "\n" +
		`{"type":"turn.completed","usage":{"input_tokens":3,"output_tokens":4}}` + "\n")
	runner := &scriptedRunner{
		stream: func(cmd command, onStdoutLine commandOutputHandler) commandResult {
			stdout := append([]byte(nil), firstLine...)
			if err := onStdoutLine(firstLine); err != nil {
				close(returned)
				return commandResult{stdout: stdout, outputErr: err}
			}
			<-release
			stdout = append(stdout, rest...)
			outputErr := emitScriptedStdout(rest, onStdoutLine)
			close(returned)
			return commandResult{stdout: stdout, outputErr: outputErr}
		},
	}
	ag, err := New(withCommandRunner(runner))
	require.NoError(t, err)
	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	var first *event.Event
	select {
	case first = <-ch:
	case <-time.After(time.Second):
		require.FailNow(t, "timed out waiting for streamed assistant event")
	}
	require.False(t, first.IsFinalResponse())
	require.True(t, first.IsPartial)
	require.Equal(t, model.ObjectTypeChatCompletionChunk, first.Object)
	require.Equal(t, "item_0", first.Response.ID)
	require.Equal(t, "good luck", first.Choices[0].Delta.Content)
	select {
	case <-returned:
		require.FailNow(t, "command returned before the test released it")
	default:
	}
	close(release)
	events := append([]*event.Event{first}, drainEvents(ch)...)
	require.Len(t, events, 5)
	require.True(t, events[1].IsToolCallResponse())
	require.True(t, events[2].IsToolResultResponse())
	require.False(t, events[3].IsFinalResponse())
	require.True(t, events[3].IsPartial)
	require.Equal(t, "practice makes perfect", events[3].Choices[0].Delta.Content)
	require.Equal(t, "item_2", events[3].Response.ID)
	require.True(t, events[4].IsFinalResponse())
	require.Equal(t, "practice makes perfect", events[4].Choices[0].Message.Content)
	require.Equal(t, "item_2", events[4].Response.ID)
	require.Equal(t, 7, events[4].Usage.TotalTokens)
}

func TestCodexAgent_Run_StreamsErrorsAsNonTerminalUntilCommandFinishes(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-stream-error-1")
	inv := newTestInvocation("inv-stream-error-1", sess, "Fail twice.")
	returned := make(chan struct{})
	transcript := []byte(`{"type":"turn.failed","error":{"message":"first failure","code":"first"}}
{"type":"error","message":"second failure"}` + "\n")
	runner := &scriptedRunner{
		stream: func(cmd command, onStdoutLine commandOutputHandler) commandResult {
			outputErr := emitScriptedStdout(transcript, onStdoutLine)
			close(returned)
			return commandResult{stdout: transcript, outputErr: outputErr}
		},
	}
	ag, err := New(withCommandRunner(runner))
	require.NoError(t, err)
	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	doneEvent := make(chan *event.Event, 1)
	go func() {
		for evt := range ch {
			if evt != nil && evt.Done {
				doneEvent <- evt
				return
			}
		}
		doneEvent <- nil
	}()
	var terminal *event.Event
	select {
	case terminal = <-doneEvent:
	case <-time.After(time.Second):
		require.FailNow(t, "timed out waiting for terminal error")
	}
	select {
	case <-returned:
	case <-time.After(time.Second):
		require.FailNow(t, "command blocked after streamed error observations")
	}
	require.NotNil(t, terminal)
	require.True(t, terminal.IsTerminalError())
	require.Equal(t, model.ObjectTypeError, terminal.Object)
	require.Equal(t, "second failure", terminal.Error.Message)
}

func TestCodexAgent_Run_StreamParseErrorEmitsFlowError(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-stream-parse-error")
	inv := newTestInvocation("inv-stream-parse-error", sess, "Hi.")
	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			return []byte("{not-json}\n"), nil, nil
		},
	}
	ag, err := New(withCommandRunner(runner))
	require.NoError(t, err)
	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	events := drainEvents(ch)
	require.Len(t, events, 1)
	require.Equal(t, model.ObjectTypeError, events[0].Object)
	require.NotNil(t, events[0].Error)
	require.Equal(t, model.ErrorTypeFlowError, events[0].Error.Type)
	require.Contains(t, events[0].Error.Message, "stream codex transcript")
	require.Equal(t, "{not-json}", events[0].Choices[0].Message.Content)
}

func TestCodexAgent_Run_CommandErrorAfterCodexFailureEmitsTerminalCodexError(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-codex-error-run-error")
	inv := newTestInvocation("inv-codex-error-run-error", sess, "Hi.")
	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			transcript := []byte(`{"type":"turn.failed","error":{"message":"codex failed","code":"bad_turn"}}` + "\n")
			return transcript, []byte("process failed"), errors.New("exit 1")
		},
	}
	ag, err := New(withCommandRunner(runner))
	require.NoError(t, err)
	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	events := drainEvents(ch)
	require.Len(t, events, 2)
	require.False(t, events[0].Done)
	require.True(t, events[0].IsPartial)
	require.Nil(t, events[0].Error)
	require.True(t, events[1].IsTerminalError())
	require.Equal(t, "codex failed", events[1].Error.Message)
	require.NotNil(t, events[1].Error.Code)
	require.Equal(t, "bad_turn", *events[1].Error.Code)
	require.Equal(t, "codex failed", events[1].Choices[0].Message.Content)
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
	require.Len(t, events, 2)
	require.False(t, events[0].IsFinalResponse())
	require.True(t, events[0].IsPartial)
	require.Equal(t, "hello", events[0].Choices[0].Delta.Content)
	require.True(t, events[1].IsFinalResponse())
	require.Equal(t, "hello", events[1].Choices[0].Message.Content)
	require.Empty(t, events[1].StateDelta)
	calls := runner.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, []string{"exec", "resume", "--json", "thread-1"}, calls[0].args)
	require.Equal(t, "Hi.", string(calls[0].stdin))
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
	require.Len(t, events, 2)
	require.False(t, events[0].IsFinalResponse())
	require.True(t, events[0].IsPartial)
	require.Equal(t, "fresh", events[0].Choices[0].Delta.Content)
	require.True(t, events[1].IsFinalResponse())
	require.Equal(t, "fresh", events[1].Choices[0].Message.Content)
	require.Equal(t, []byte("thread-2"), events[1].StateDelta[StateKeyThreadID])
	calls := runner.Calls()
	require.Len(t, calls, 2)
	require.Equal(t, []string{"exec", "resume", "--json", "stale-thread"}, calls[0].args)
	require.Equal(t, "Hi.", string(calls[0].stdin))
	require.Equal(t, []string{"exec", "--json"}, calls[1].args)
	require.Equal(t, "Hi.", string(calls[1].stdin))
}

func TestCodexAgent_Run_ResumeFailureAfterStreamedEventDoesNotFallback(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-resume-stream-error")
	sess.SetState(StateKeyThreadID, []byte("thread-1"))
	inv := newTestInvocation("inv-resume-stream-error", sess, "Hi.")
	transcript := `{"type":"thread.started","thread_id":"thread-1"}
{"type":"item.started","item":{"id":"item_0","type":"command_execution","command":"sleep 1","status":"in_progress"}}`
	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			if len(cmd.args) > 1 && cmd.args[1] == "resume" {
				return []byte(transcript), []byte("resume crashed"), errors.New("resume exit 1")
			}
			return []byte(`{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"fresh"}}`), nil, nil
		},
	}
	ag, err := New(withCommandRunner(runner))
	require.NoError(t, err)
	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	events := drainEvents(ch)
	require.Len(t, events, 2)
	require.True(t, events[0].IsToolCallResponse())
	require.True(t, events[1].IsTerminalError())
	require.Equal(t, model.ErrorTypeRunError, events[1].Error.Type)
	require.Contains(t, events[1].Error.Message, "resume crashed")
	require.Empty(t, events[1].StateDelta)
	calls := runner.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, []string{"exec", "resume", "--json", "thread-1"}, calls[0].args)
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
	require.Equal(t, []string{"exec", "resume", "--json", "thread-1"}, calls[0].args)
	require.Equal(t, "Hi.", string(calls[0].stdin))
	require.Equal(t, []string{"exec", "--json"}, calls[1].args)
	require.Equal(t, "Hi.", string(calls[1].stdin))
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

func TestCodexAgent_Run_RawOutputHookReceivesCommandError(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-hook-err-1")
	inv := newTestInvocation("inv-hook-err-1", sess, "--help")
	runErr := errors.New("exit 1")
	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			return nil, []byte("stderr text"), runErr
		},
	}
	var got RawOutputHookArgs
	ag, err := New(
		withCommandRunner(runner),
		WithRawOutputHook(func(_ context.Context, args *RawOutputHookArgs) error {
			got = *args
			return nil
		}),
	)
	require.NoError(t, err)
	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	events := drainEvents(ch)
	require.Len(t, events, 1)
	require.ErrorIs(t, got.Error, runErr)
	require.Equal(t, "--help", got.Prompt)
	require.Empty(t, string(got.Stdout))
	require.Equal(t, "stderr text", string(got.Stderr))
	require.NotNil(t, events[0].Error)
	require.Equal(t, model.ErrorTypeRunError, events[0].Error.Type)
	require.Equal(t, "stderr text", events[0].Error.Message)
	require.Equal(t, "stderr text", events[0].Choices[0].Message.Content)
	calls := runner.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, []string{"exec", "--json"}, calls[0].args)
	require.Equal(t, "--help", string(calls[0].stdin))
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
	require.Len(t, events, 4)
	require.True(t, events[0].IsToolCallResponse())
	require.True(t, events[1].IsToolResultResponse())
	require.True(t, events[2].IsPartial)
	require.Equal(t, "hello", events[2].Choices[0].Delta.Content)
	require.True(t, events[3].IsFinalResponse())
	require.Equal(t, model.ObjectTypeError, events[3].Object)
	require.NotNil(t, events[3].Error)
	require.Equal(t, model.ErrorTypeFlowError, events[3].Error.Type)
	require.Contains(t, events[3].Error.Message, "raw output hook")
	require.Contains(t, events[3].Error.Message, "hook failed")
	require.Contains(t, events[3].Choices[0].Message.Content, "thread-hook-2")
	require.Contains(t, events[3].Choices[0].Message.Content, "warn")
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
	require.Equal(t, []string{"--ask-for-approval", "never", "exec", "--sandbox", "read-only", "--json"}, calls[0].args)
	require.Equal(t, "Hi.", string(calls[0].stdin))
}

func TestNew_ValidationErrors(t *testing.T) {
	_, err := New(WithBin(""))
	require.Error(t, err)
	_, err = New(withCommandRunner(nil))
	require.Error(t, err)
}

func TestCodexAgent_Run_ValidationErrors(t *testing.T) {
	ctx := context.Background()
	ag, err := New()
	require.NoError(t, err)
	_, err = ag.Run(ctx, nil)
	require.Error(t, err)
	_, err = ag.Run(ctx, &agent.Invocation{})
	require.Error(t, err)
	_, err = ag.Run(ctx, &agent.Invocation{Session: &session.Session{}})
	require.Error(t, err)
	_, err = ag.Run(ctx, newTestInvocation("inv-empty-prompt", session.NewSession("app", "user", "sess-empty"), ""))
	require.Error(t, err)
}

func TestExecCommandRunner_Run(t *testing.T) {
	tmp := t.TempDir()
	runner := execCommandRunner{}
	result := runner.Run(context.Background(), command{
		bin:  "sh",
		args: []string{"-c", "printf \"$FOO\""},
		env:  []string{"FOO=bar"},
	}, nil)
	require.NoError(t, result.err())
	require.Equal(t, "bar", string(result.stdout))
	require.Empty(t, string(result.stderr))
	result = runner.Run(context.Background(), command{
		bin:   "sh",
		args:  []string{"-c", "cat"},
		stdin: []byte("--help"),
	}, nil)
	require.NoError(t, result.err())
	require.Equal(t, "--help", string(result.stdout))
	require.Empty(t, string(result.stderr))
	result = runner.Run(context.Background(), command{
		bin:  "sh",
		args: []string{"-c", "pwd"},
		dir:  tmp,
	}, nil)
	require.NoError(t, result.err())
	got := strings.TrimSpace(string(result.stdout))
	gotResolved, err := filepath.EvalSymlinks(got)
	require.NoError(t, err)
	wantResolved, err := filepath.EvalSymlinks(tmp)
	require.NoError(t, err)
	require.Equal(t, wantResolved, gotResolved)
	require.Empty(t, string(result.stderr))
}

func TestExecCommandRunner_Run_StreamsStdoutLines(t *testing.T) {
	runner := execCommandRunner{}
	var lines []string
	result := runner.Run(context.Background(), command{
		bin:  "sh",
		args: []string{"-c", "printf 'one\\n'; printf 'two'"},
	}, func(line []byte) error {
		lines = append(lines, string(line))
		return nil
	})
	require.NoError(t, result.err())
	require.Equal(t, "one\ntwo", string(result.stdout))
	require.Empty(t, string(result.stderr))
	require.Equal(t, []string{"one\n", "two"}, lines)
}

func TestExecCommandRunner_Run_StopsProcessOnStreamHandlerError(t *testing.T) {
	runner := execCommandRunner{}
	handlerErr := errors.New("stop streaming")
	result := runner.Run(context.Background(), command{
		bin:  "sh",
		args: []string{"-c", "printf 'one\\n'; sleep 5; printf 'two\\n'"},
	}, func(line []byte) error {
		require.Equal(t, "one\n", string(line))
		return handlerErr
	})
	require.ErrorIs(t, result.outputErr, handlerErr)
	require.ErrorIs(t, result.err(), handlerErr)
	require.Error(t, result.runErr)
	require.Equal(t, "one\n", string(result.stdout))
}

func TestExecCommandRunner_Run_ReportsStartErrorWithStreaming(t *testing.T) {
	runner := execCommandRunner{}
	result := runner.Run(context.Background(), command{
		bin: filepath.Join(t.TempDir(), "missing-codex"),
	}, func(line []byte) error {
		require.Empty(t, line)
		return nil
	})
	require.Error(t, result.runErr)
	require.NoError(t, result.outputErr)
	require.Empty(t, result.stdout)
	require.Empty(t, result.stderr)
}

func TestReadStdoutLines_ReportsReaderErrorAfterCapturedLine(t *testing.T) {
	reader, writer := io.Pipe()
	readErr := errors.New("read failed")
	go func() {
		_, _ = writer.Write([]byte("one\n"))
		_ = writer.CloseWithError(readErr)
	}()
	var capture bytes.Buffer
	var lines []string
	err := readStdoutLines(reader, &capture, func(line []byte) error {
		lines = append(lines, string(line))
		return nil
	})
	require.ErrorIs(t, err, readErr)
	require.Equal(t, "one\n", capture.String())
	require.Equal(t, []string{"one\n"}, lines)
}

func TestParseTranscriptEvents_CompletedToolWithoutStarted(t *testing.T) {
	transcript := `{"type":"thread.started","thread_id":"thread-parse"}
{"type":"item.completed","item":{"id":"item_0","type":"command_execution","command":"pwd","aggregated_output":"/tmp\n","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"done"}}`
	result, err := parseTranscriptEvents([]byte(transcript), "inv-parse-1", "codex")
	require.NoError(t, err)
	require.Equal(t, "thread-parse", result.ThreadID)
	require.Equal(t, "done", result.FinalMessage)
	require.Equal(t, "item_1", result.FinalMessageID)
	require.Len(t, result.Events, 3)
	require.True(t, result.Events[0].IsToolCallResponse())
	require.True(t, result.Events[1].IsToolResultResponse())
	require.False(t, result.Events[2].IsFinalResponse())
	require.Equal(t, "pwd", commandArg(t, result.Events[0]))
	require.Equal(t, "/tmp\n", result.Events[1].Choices[0].Message.Content)
	require.True(t, result.Events[2].IsPartial)
	require.Equal(t, "done", result.Events[2].Choices[0].Delta.Content)
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
{"type":"item.started","item":{"id":"item_image_view","type":"image_view","path":"/tmp/input.png"}}
{"type":"item.completed","item":{"id":"item_image_view","type":"image_view","result":{"width":64,"height":32},"status":"completed"}}
{"type":"item.started","item":{"id":"item_image","type":"image_generation","prompt":"draw icon"}}
{"type":"item.completed","item":{"id":"item_image","type":"image_generation","saved_path":"/tmp/icon.png","revised_prompt":"draw a clean icon","status":"completed"}}`
	result, err := parseTranscriptEvents([]byte(transcript), "inv-parse-builtins-1", "codex")
	require.NoError(t, err)
	require.Len(t, result.Events, 8)
	require.Equal(t, "web_search", result.Events[0].Choices[0].Message.ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"query":"trpc agent"}`, string(result.Events[0].Choices[0].Message.ToolCalls[0].Function.Arguments))
	require.JSONEq(t, `[{"title":"doc"}]`, result.Events[1].Choices[0].Message.Content)
	require.Equal(t, "file_change", result.Events[2].Choices[0].Message.ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"path":"main.go","kind":"update"}`, string(result.Events[2].Choices[0].Message.ToolCalls[0].Function.Arguments))
	require.JSONEq(t, `{"path":"main.go","kind":"update"}`, result.Events[3].Choices[0].Message.Content)
	require.Equal(t, "image_view", result.Events[4].Choices[0].Message.ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"path":"/tmp/input.png"}`, string(result.Events[4].Choices[0].Message.ToolCalls[0].Function.Arguments))
	require.JSONEq(t, `{"width":64,"height":32}`, result.Events[5].Choices[0].Message.Content)
	require.Equal(t, "image_generation", result.Events[6].Choices[0].Message.ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"prompt":"draw icon"}`, string(result.Events[6].Choices[0].Message.ToolCalls[0].Function.Arguments))
	require.JSONEq(t, `{"saved_path":"/tmp/icon.png","revised_prompt":"draw a clean icon","status":"completed"}`, result.Events[7].Choices[0].Message.Content)
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

func TestParseTranscriptEvents_ErrorEventMapping(t *testing.T) {
	transcript := `{"type":"turn.failed","error":{"message":"turn failed","code":"bad_turn"}}
{"type":"error","message":"top-level error"}`
	result, err := parseTranscriptEvents([]byte(transcript), "inv-parse-error-1", "codex")
	require.NoError(t, err)
	require.Len(t, result.Events, 2)
	require.Equal(t, model.ObjectTypeChatCompletionChunk, result.Events[0].Object)
	require.False(t, result.Events[0].Done)
	require.True(t, result.Events[0].IsPartial)
	require.Nil(t, result.Events[0].Error)
	require.False(t, result.Events[0].IsTerminalError())
	require.Equal(t, "turn failed", result.Events[0].Choices[0].Delta.Content)
	require.False(t, result.Events[1].Done)
	require.True(t, result.Events[1].IsPartial)
	require.Nil(t, result.Events[1].Error)
	require.False(t, result.Events[1].IsTerminalError())
	require.Equal(t, "top-level error", result.Events[1].Choices[0].Delta.Content)
	require.Equal(t, "top-level error", result.Error.Message)
}

func TestParseTranscriptEvents_ErrorPayloadShapes(t *testing.T) {
	transcript := `{"type":"turn.failed","error":"quoted failure"}
{"type":"turn.failed","error":{"type":"api_error","message":"api failed","code":"api_code"}}`
	result, err := parseTranscriptEvents([]byte(transcript), "inv-parse-error-2", "codex")
	require.NoError(t, err)
	require.Len(t, result.Events, 2)
	require.Equal(t, "quoted failure", result.Events[0].Choices[0].Delta.Content)
	require.Equal(t, "api failed", result.Events[1].Choices[0].Delta.Content)
	require.NotNil(t, result.Error)
	require.Equal(t, "api_error", result.Error.Type)
	require.NotNil(t, result.Error.Code)
	require.Equal(t, "api_code", *result.Error.Code)
}

func TestErrorEventFromRecord(t *testing.T) {
	evt := errorEventFromRecord("inv-error-record", "codex", codexEvent{
		Type:  codexEventTurnFailed,
		Error: json.RawMessage(`"quoted failure"`),
	}, false)
	require.NotNil(t, evt)
	require.False(t, evt.Done)
	require.True(t, evt.IsPartial)
	require.Nil(t, evt.Error)
	require.Equal(t, "quoted failure", evt.Choices[0].Delta.Content)
	require.Nil(t, errorEventFromResponseError("inv-error-nil", "codex", nil, false))
}

func TestErrorEventFromResponseErrorClonesTerminalError(t *testing.T) {
	param := "prompt"
	code := "bad_turn"
	responseErr := &model.ResponseError{
		Type:    model.ErrorTypeRunError,
		Message: "original",
		Param:   &param,
		Code:    &code,
	}
	evt := errorEventFromResponseError("inv-clone", "codex", responseErr, true)
	responseErr.Message = "mutated"
	*responseErr.Param = "mutated-param"
	*responseErr.Code = "mutated-code"
	require.Equal(t, "original", evt.Error.Message)
	require.NotNil(t, evt.Error.Param)
	require.Equal(t, "prompt", *evt.Error.Param)
	require.NotNil(t, evt.Error.Code)
	require.Equal(t, "bad_turn", *evt.Error.Code)
}

func TestTranscriptHelpers_Fallbacks(t *testing.T) {
	exitCode := 2
	require.Equal(t, "mcp__server", mcpToolName("server", ""))
	require.Equal(t, "tool", mcpToolName("", "tool"))
	require.Equal(t, "mcp_tool_call", mcpToolName("", ""))
	require.False(t, isToolItem("unknown"))
	require.Equal(t, "custom", toolNameForItem(&codexItem{Type: " custom "}))
	require.JSONEq(t, `{"raw":true}`, string(toolArgumentsFromRawOrFields(&codexItem{Arguments: json.RawMessage(`{"raw":true}`)}, "path")))
	require.JSONEq(t, `{}`, string(toolArgumentsFromRawOrFields(&codexItem{}, "path")))
	require.Equal(t, "aggregated", toolResultFromOutputFields(&codexItem{AggregatedOutput: "aggregated"}))
	require.JSONEq(t, `{"status":"completed"}`, toolResultFromOutputFields(&codexItem{Status: "completed"}))
	require.JSONEq(t, `{"exit_code":2,"status":"failed"}`, commandStatusResult(&codexItem{Status: "failed", ExitCode: &exitCode}))
	require.Empty(t, commandStatusResult(&codexItem{}))
	require.JSONEq(t, `{"skill":"deploy","command":""}`, string(skillArguments(&codexItem{Arguments: json.RawMessage(`"deploy"`)})))
	require.JSONEq(t, `{"skill":"debug","command":"run"}`, string(skillArguments(&codexItem{Arguments: json.RawMessage(`{"skill":"debug","command":"run"}`)})))
	require.JSONEq(t, `[1]`, string(skillArguments(&codexItem{Arguments: json.RawMessage(`[1]`)})))
	require.JSONEq(t, `{}`, string(skillArguments(&codexItem{})))
	require.JSONEq(t, `{}`, string(marshalArgs(func() {})))
	require.Nil(t, (*codexUsage)(nil).toModelUsage())
	require.Nil(t, (&codexUsage{}).toModelUsage())
}

func TestTranscriptStream_HandlesEmptyLinesAndNilState(t *testing.T) {
	require.Empty(t, (*transcriptStream)(nil).Result().Events)
	require.Nil(t, (*transcriptStream)(nil).HandleRecord(codexEvent{Type: codexEventThreadStarted}))
	stream := newTranscriptStream("inv-stream-helper", "codex")
	events, err := stream.HandleLine([]byte(" \n"))
	require.NoError(t, err)
	require.Nil(t, events)
	stream.result = nil
	require.Empty(t, stream.Result().Events)
}

func TestCodexStreamEmitter_HandlesEmptyLinesAndNilState(t *testing.T) {
	ctx := context.Background()
	inv := newTestInvocation("inv-emitter-helper", session.NewSession("app", "user", "sess-emitter-helper"), "Hi.")
	out := make(chan *event.Event, 1)
	rawAgent, err := New()
	require.NoError(t, err)
	ag, ok := rawAgent.(*codexAgent)
	require.True(t, ok)
	emitter := newCodexStreamEmitter(ctx, ag, inv, out)
	require.NoError(t, emitter.HandleLine([]byte("\n")))
	require.False(t, emitter.emitted)
	require.Empty(t, emitter.Result().Events)
	require.Empty(t, (*codexStreamEmitter)(nil).Result().Events)
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
