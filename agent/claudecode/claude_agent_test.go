//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package claudecode

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

// TestClaudeCodeAgent_Run_ResumeThenCreate verifies the agent retries with --session-id when --resume has no conversation.
func TestClaudeCodeAgent_Run_ResumeThenCreate(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-1")
	inv := &agent.Invocation{
		InvocationID: "inv-1",
		Session:      sess,
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Calculate the sum of 1 and 2.",
		},
	}

	transcript := `[{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call_1","name":"calculator","input":{"operation":"add","a":1,"b":2}}]}},{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"text","text":"{\"operation\":\"add\",\"a\":1,\"b\":2,\"result\":3}"}]}]}},{"type":"result","result":"ok"}]`

	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			if strings.Contains(strings.Join(cmd.args, " "), "--resume") {
				return nil, []byte("No conversation found with session ID: sess-1\n"), errors.New("exit 1")
			}
			if strings.Contains(strings.Join(cmd.args, " "), "--session-id") {
				return []byte(transcript), nil, nil
			}
			return nil, nil, errors.New("unexpected args")
		},
	}

	ag, err := New(
		WithBin("claude"),
		withCommandRunner(runner),
	)
	require.NoError(t, err)

	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	events := drainEvents(ch)
	require.Len(t, events, 3)

	require.True(t, events[0].IsToolCallResponse())
	require.True(t, events[1].IsToolResultResponse())
	require.True(t, events[2].IsFinalResponse())
	require.Equal(t, "ok", events[2].Choices[0].Message.Content)

	calls := runner.Calls()
	require.Len(t, calls, 2)
	require.Contains(t, strings.Join(calls[0].args, " "), "--resume")
	require.Contains(t, strings.Join(calls[1].args, " "), "--session-id")
}

func TestClaudeCodeAgent_RunWithSession_DoesNotMutateArgsOnFallback(t *testing.T) {
	ctx := context.Background()
	callCount := 0
	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			callCount++
			if callCount == 1 {
				return nil, nil, errors.New("resume failed")
			}
			return []byte(`[{"type":"result","result":"ok"}]`), nil, nil
		},
	}

	baseArgs := make([]string, 0, 32)
	baseArgs = append(baseArgs, "-p", "--verbose", "--output-format", "json")
	baseLen := len(baseArgs)

	ag := &claudeCodeAgent{
		bin:           "claude",
		args:          baseArgs,
		commandRunner: runner,
	}

	_, _, err := ag.runWithSession(ctx, "session-1", "Hi.")
	require.NoError(t, err)

	calls := runner.Calls()
	require.Len(t, calls, 2)
	require.GreaterOrEqual(t, len(calls[0].args), baseLen+1)
	require.GreaterOrEqual(t, len(calls[1].args), baseLen+1)
	require.Equal(t, "--resume", calls[0].args[baseLen])
	require.Equal(t, "--session-id", calls[1].args[baseLen])
}

// TestClaudeCodeAgent_Run_ResumeSuccess verifies the agent uses --resume when the session is available.
func TestClaudeCodeAgent_Run_ResumeSuccess(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-2")
	inv := &agent.Invocation{
		InvocationID: "inv-2",
		Session:      sess,
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Hi.",
		},
	}

	transcript := `[{"type":"result","result":"hello"}]`
	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			return []byte(transcript), nil, nil
		},
	}

	ag, err := New(
		WithBin("claude"),
		withCommandRunner(runner),
	)
	require.NoError(t, err)

	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	events := drainEvents(ch)
	require.Len(t, events, 1)
	require.True(t, events[0].IsFinalResponse())
	require.Equal(t, "hello", events[0].Choices[0].Message.Content)

	calls := runner.Calls()
	require.Len(t, calls, 1)
	require.Contains(t, strings.Join(calls[0].args, " "), "--resume")
}

// TestClaudeCodeAgent_Run_CommandError verifies CLI failures are surfaced via Response.Error.
func TestClaudeCodeAgent_Run_CommandError(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-4")
	inv := &agent.Invocation{
		InvocationID: "inv-4",
		Session:      sess,
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Hi.",
		},
	}

	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			return nil, []byte("boom\n"), errors.New("exit 2")
		},
	}

	ag, err := New(
		WithBin("claude"),
		withCommandRunner(runner),
	)
	require.NoError(t, err)

	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	events := drainEvents(ch)
	require.Len(t, events, 1)
	require.True(t, events[0].IsFinalResponse())
	require.NotNil(t, events[0].Error)
	require.Equal(t, "boom", events[0].Error.Message)
	require.Equal(t, "boom", events[0].Choices[0].Message.Content)
}

func TestClaudeCodeAgent_Run_RawOutputHook(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-hook-1")
	inv := &agent.Invocation{
		InvocationID: "inv-hook-1",
		Session:      sess,
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Hi.",
		},
	}

	transcript := `[{"type":"result","result":"hello"}]`
	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			return []byte(transcript), []byte("warn\n"), nil
		},
	}

	var got RawOutputHookArgs
	var called bool
	ag, err := New(
		WithBin("claude"),
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
	require.Equal(t, cliSessionID(sess), got.CLISessionID)
	require.Equal(t, "Hi.", got.Prompt)
	require.Equal(t, transcript, string(got.Stdout))
	require.Equal(t, "warn\n", string(got.Stderr))
	require.NoError(t, got.Error)
}

func TestClaudeCodeAgent_Run_RawOutputHookError(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-hook-2")
	inv := &agent.Invocation{
		InvocationID: "inv-hook-2",
		Session:      sess,
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Hi.",
		},
	}

	transcript := `[{"type":"result","result":"hello"}]`
	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			return []byte(transcript), []byte("warn\n"), nil
		},
	}

	hookErr := errors.New("hook failed")
	var called bool
	ag, err := New(
		WithBin("claude"),
		withCommandRunner(runner),
		WithRawOutputHook(func(_ context.Context, _ *RawOutputHookArgs) error {
			called = true
			return hookErr
		}),
	)
	require.NoError(t, err)

	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	events := drainEvents(ch)

	require.True(t, called)
	require.Len(t, events, 1)
	require.True(t, events[0].IsFinalResponse())
	require.Equal(t, model.ObjectTypeError, events[0].Object)
	require.NotNil(t, events[0].Error)
	require.Equal(t, model.ErrorTypeFlowError, events[0].Error.Type)
	require.Contains(t, events[0].Error.Message, "raw output hook")
	require.Contains(t, events[0].Error.Message, "hook failed")
	require.Contains(t, events[0].Choices[0].Message.Content, transcript)
	require.Contains(t, events[0].Choices[0].Message.Content, "warn")
}

func TestClaudeCodeAgent_InfoAndRunnerArgs(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-info-1")
	inv := &agent.Invocation{
		InvocationID: "inv-info-1",
		Session:      sess,
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Hi.",
		},
	}

	transcript := `[{"type":"result","result":"ok"}]`
	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			return []byte(transcript), nil, nil
		},
	}

	ag, err := New(
		WithName("my-claude"),
		WithBin("claude-bin"),
		WithExtraArgs("--permission-mode", "bypassPermissions"),
		WithEnv("CLAUDE_AGENT_TEST=1"),
		WithWorkDir("/tmp"),
		withCommandRunner(runner),
	)
	require.NoError(t, err)

	info := ag.Info()
	require.Equal(t, "my-claude", info.Name)
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
	require.Equal(t, "claude-bin", calls[0].bin)
	require.Equal(t, "/tmp", calls[0].dir)
	require.Contains(t, calls[0].env, "CLAUDE_AGENT_TEST=1")
	require.Contains(t, calls[0].args, "--permission-mode")
	require.Contains(t, calls[0].args, "bypassPermissions")
	require.Contains(t, calls[0].args, "--resume")
	require.Contains(t, calls[0].args, cliSessionID(sess))
	require.Equal(t, "Hi.", calls[0].args[len(calls[0].args)-1])
}

func TestNew_ValidationErrors(t *testing.T) {
	_, err := New(WithBin(""))
	require.Error(t, err)

	_, err = New(withCommandRunner(nil))
	require.Error(t, err)

	_, err = New(WithOutputFormat(OutputFormat("yaml")))
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

func TestCliSessionID_EdgeCases(t *testing.T) {
	require.Equal(t, "", cliSessionID(nil))
	require.Equal(t, "", cliSessionID(&session.Session{}))
	require.NotEmpty(t, cliSessionID(&session.Session{ID: "session-1"}))
}

// TestParseTranscriptToolEvents_ToolResultStringContent verifies tool_result blocks can carry plain string content.
func TestParseTranscriptToolEvents_ToolResultStringContent(t *testing.T) {
	transcript := `[{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call_1","name":"Bash","input":{"command":"ls"}}]}},{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call_1","content":"No files found"}]}},{"type":"result","result":"ok"}]`

	events, result, err := parseTranscriptToolEvents([]byte(transcript), "inv-parse-1", "claude")
	require.NoError(t, err)
	require.Equal(t, "ok", result)
	require.Len(t, events, 2)
	require.True(t, events[0].IsToolCallResponse())
	require.True(t, events[1].IsToolResultResponse())
	require.Equal(t, "No files found", events[1].Choices[0].Message.Content)
}

func TestParseTranscriptToolEvents_EmptyOutput(t *testing.T) {
	events, result, err := parseTranscriptToolEvents(nil, "inv-empty-1", "claude")
	require.NoError(t, err)
	require.Empty(t, result)
	require.Nil(t, events)
}

func TestParseTranscriptToolEvents_ToolUseNullInput(t *testing.T) {
	transcript := `[{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call_1","name":"Bash","input":null}]}},{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call_1","content":"ok"}]}},{"type":"result","result":"done"}]`

	events, result, err := parseTranscriptToolEvents([]byte(transcript), "inv-null-input-1", "claude")
	require.NoError(t, err)
	require.Equal(t, "done", result)
	require.Len(t, events, 2)
	require.True(t, events[0].IsToolCallResponse())
	require.Equal(t, "{}", string(events[0].Choices[0].Message.ToolCalls[0].Function.Arguments))
	require.True(t, events[1].IsToolResultResponse())
}

func TestParseTranscriptToolEvents_TaskInvalidInputNoTransfer(t *testing.T) {
	transcript := `[{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call_task","name":"Task","input":"not-json"}]}},{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call_task","content":"ok"}]}},{"type":"result","result":"done"}]`

	events, result, err := parseTranscriptToolEvents([]byte(transcript), "inv-task-invalid-1", "claude")
	require.NoError(t, err)
	require.Equal(t, "done", result)
	require.Len(t, events, 2)
	require.True(t, events[0].IsToolCallResponse())
	require.True(t, events[1].IsToolResultResponse())
	require.Equal(t, "Task", events[0].Choices[0].Message.ToolCalls[0].Function.Name)
}

func TestDecodeToolResultContent_TextBlocks(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"one"},{"type":"text","text":""},{"type":"text","text":"two"}]`)
	require.Equal(t, "one\ntwo", decodeToolResultContent(raw))
}

func TestClaudeCodeAgent_Run_OutputFormatOverride(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-output-format-1")
	inv := &agent.Invocation{
		InvocationID: "inv-output-format-1",
		Session:      sess,
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Hi.",
		},
	}

	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			return []byte(`[{"type":"result","result":"ok"}]`), nil, nil
		},
	}

	ag, err := New(
		WithBin("claude"),
		WithOutputFormat(OutputFormatStreamJSON),
		withCommandRunner(runner),
	)
	require.NoError(t, err)

	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	_ = drainEvents(ch)

	calls := runner.Calls()
	require.Len(t, calls, 1)
	argv := calls[0].args
	var positions []int
	for i, arg := range argv {
		if arg == "--output-format" {
			positions = append(positions, i)
		}
	}
	require.Len(t, positions, 1)
	require.Greater(t, len(argv), positions[0]+1)
	require.Equal(t, "stream-json", argv[positions[0]+1])
}

// TestParseTranscriptToolEvents_JSONL verifies the transcript parser supports stream-json (JSONL) output.
func TestParseTranscriptToolEvents_JSONL(t *testing.T) {
	transcript := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call_1","name":"Bash","input":{"command":"ls"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call_1","content":"ok"}]}}
{"type":"result","result":"done"}`

	events, result, err := parseTranscriptToolEvents([]byte(transcript), "inv-parse-jsonl-1", "claude")
	require.NoError(t, err)
	require.Equal(t, "done", result)
	require.Len(t, events, 2)
	require.True(t, events[0].IsToolCallResponse())
	require.True(t, events[1].IsToolResultResponse())
}

// TestParseTranscriptToolEvents_SkillToolMapping verifies Skill tool calls are mapped to framework skill_run.
func TestParseTranscriptToolEvents_SkillToolMapping(t *testing.T) {
	transcript := `[{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call_1","name":"Skill","input":{"skill":"debug"}}]}},{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call_1","content":"ok"}]}},{"type":"result","result":"done"}]`

	events, result, err := parseTranscriptToolEvents([]byte(transcript), "inv-parse-skill-1", "claude")
	require.NoError(t, err)
	require.Equal(t, "done", result)
	require.Len(t, events, 2)
	require.True(t, events[0].IsToolCallResponse())
	require.True(t, events[1].IsToolResultResponse())
	require.Equal(t, "skill_run", events[0].Choices[0].Message.ToolCalls[0].Function.Name)
	require.Equal(t, "skill_run", events[1].Choices[0].Message.ToolName)

	var args skillRunArgs
	require.NoError(t, json.Unmarshal(events[0].Choices[0].Message.ToolCalls[0].Function.Arguments, &args))
	require.Equal(t, "debug", args.Skill)
	require.Equal(t, "", args.Command)
}

// TestParseTranscriptToolEvents_TaskTransfer verifies Task tool calls emit an agent.transfer event.
func TestParseTranscriptToolEvents_TaskTransfer(t *testing.T) {
	transcript := `[{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call_task","name":"Task","input":{"subagent_type":"Bash","prompt":"do it"}}]}},{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call_task","content":[{"type":"text","text":"done"}]}]}},{"type":"result","result":"ok"}]`

	events, result, err := parseTranscriptToolEvents([]byte(transcript), "inv-parse-task-1", "claude")
	require.NoError(t, err)
	require.Equal(t, "ok", result)
	require.Len(t, events, 3)
	require.True(t, events[0].IsToolCallResponse())
	require.Equal(t, model.ObjectTypeTransfer, events[1].Object)
	require.True(t, events[1].ContainsTag(event.TransferTag))
	require.Contains(t, events[1].Choices[0].Message.Content, "Bash")
	require.True(t, events[2].IsToolResultResponse())
	require.Equal(t, "done", events[2].Choices[0].Message.Content)
}

func TestClaudeCodeAgent_Run_TranscriptFixture(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "sess-fixture-1")
	inv := &agent.Invocation{
		InvocationID: "inv-fixture-1",
		Session:      sess,
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "fixture",
		},
	}

	transcript := `[{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_bash","name":"Bash","input":{"command":"ls","description":"List files in current directory"}}]}},{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_bash","content":"README.md\nagent\nexamples\n"}]}},{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_mcp","name":"mcp__eva_cli_log_kodbey__calculator","input":{"operation":"add","a":1,"b":2}}]}},{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_mcp","content":[{"type":"text","text":"{\"operation\":\"add\",\"a\":1,\"b\":2,\"result\":3}"}]}]}},{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_skill","name":"Skill","input":{"skill":"debug"}}]}},{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_skill","content":"<tool_use_error>Skill debug cannot be used with Skill tool due to disable-model-invocation</tool_use_error>"}]}},{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_task","name":"Task","input":{"description":"Respond with OK","prompt":"Please respond with exactly: OK","subagent_type":"general-purpose"}}]}},{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_task","content":[{"type":"text","text":"OK"},{"type":"text","text":"agentId: a880ff9 (for resuming to continue this agent's work if needed)\n<usage>total_tokens: 12847\ntool_uses: 0\nduration_ms: 2627</usage>"}]}]}},{"type":"result","result":"## Tool Usage Demonstration Complete\n\nBash ls and MCP calculator demonstrated."}]`

	runner := &scriptedRunner{
		run: func(cmd command) ([]byte, []byte, error) {
			return []byte(transcript), nil, nil
		},
	}

	ag, err := New(
		WithBin("claude"),
		withCommandRunner(runner),
	)
	require.NoError(t, err)

	ch, err := ag.Run(ctx, inv)
	require.NoError(t, err)
	events := drainEvents(ch)
	require.NotEmpty(t, events)
	require.True(t, events[len(events)-1].IsFinalResponse())
	require.Contains(t, events[len(events)-1].Choices[0].Message.Content, "Tool Usage Demonstration Complete")

	type bashArgs struct {
		Command string `json:"command"`
	}
	type mcpArgs struct {
		Operation string  `json:"operation"`
		A         float64 `json:"a"`
		B         float64 `json:"b"`
	}
	type mcpResult struct {
		Operation string  `json:"operation"`
		A         float64 `json:"a"`
		B         float64 `json:"b"`
		Result    float64 `json:"result"`
	}

	var sawBashCall bool
	var sawBashResult bool
	var sawMCPCalcCall bool
	var sawMCPCalcResult bool
	var sawSkillDebugCall bool
	var sawSkillDebugResult bool
	var sawTaskCall bool
	var sawTaskResult bool
	var sawTransfer bool

	for _, evt := range events {
		if evt.Object == model.ObjectTypeTransfer && evt.ContainsTag(event.TransferTag) {
			sawTransfer = true
			require.Contains(t, evt.Choices[0].Message.Content, "general-purpose")
			continue
		}
		if evt.IsToolCallResponse() {
			toolCall := evt.Choices[0].Message.ToolCalls[0]
			switch toolCall.Function.Name {
			case "Bash":
				var args bashArgs
				require.NoError(t, json.Unmarshal(toolCall.Function.Arguments, &args))
				require.Equal(t, "ls", args.Command)
				sawBashCall = true
			case "mcp__eva_cli_log_kodbey__calculator":
				var args mcpArgs
				require.NoError(t, json.Unmarshal(toolCall.Function.Arguments, &args))
				require.Equal(t, "add", args.Operation)
				require.Equal(t, float64(1), args.A)
				require.Equal(t, float64(2), args.B)
				sawMCPCalcCall = true
			case "skill_run":
				var args skillRunArgs
				require.NoError(t, json.Unmarshal(toolCall.Function.Arguments, &args))
				if args.Skill == "debug" {
					sawSkillDebugCall = true
				}
			case "Task":
				var args taskToolInput
				require.NoError(t, json.Unmarshal(toolCall.Function.Arguments, &args))
				require.Equal(t, "general-purpose", args.SubagentType)
				sawTaskCall = true
			}
			continue
		}
		if evt.IsToolResultResponse() {
			switch evt.Choices[0].Message.ToolName {
			case "Bash":
				sawBashResult = true
				require.Contains(t, evt.Choices[0].Message.Content, "README.md")
			case "mcp__eva_cli_log_kodbey__calculator":
				var res mcpResult
				require.NoError(t, json.Unmarshal([]byte(evt.Choices[0].Message.Content), &res))
				require.Equal(t, "add", res.Operation)
				require.Equal(t, float64(1), res.A)
				require.Equal(t, float64(2), res.B)
				require.Equal(t, float64(3), res.Result)
				sawMCPCalcResult = true
			case "skill_run":
				if strings.Contains(evt.Choices[0].Message.Content, "disable-model-invocation") {
					sawSkillDebugResult = true
				}
			case "Task":
				sawTaskResult = true
				require.Contains(t, evt.Choices[0].Message.Content, "OK")
				require.Contains(t, evt.Choices[0].Message.Content, "agentId")
			}
		}
	}

	require.True(t, sawBashCall)
	require.True(t, sawBashResult)
	require.True(t, sawMCPCalcCall)
	require.True(t, sawMCPCalcResult)
	require.True(t, sawSkillDebugCall)
	require.True(t, sawSkillDebugResult)
	require.True(t, sawTaskCall)
	require.True(t, sawTaskResult)
	require.True(t, sawTransfer)
}
