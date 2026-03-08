//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skill

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	skillrepo "trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func drainExecStream(
	t *testing.T,
	reader *tool.StreamReader,
) (string, execOutput) {
	t.Helper()

	var text strings.Builder
	var out execOutput
	for {
		chunk, err := reader.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)

		switch v := chunk.Content.(type) {
		case string:
			text.WriteString(v)
		case tool.FinalResultChunk:
			switch got := v.Result.(type) {
			case execOutput:
				out = got
			default:
				b, err := json.Marshal(v.Result)
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(b, &out))
			}
		default:
			t.Fatalf("unexpected chunk type %T", v)
		}
	}
	return text.String(), out
}

func TestExecTool_StartAndWriteStdin(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skillrepo.NewFSRepository(root)
	require.NoError(t, err)

	runTool := NewRunTool(repo, localexec.New())
	execTool := NewExecTool(runTool)
	writeTool := NewWriteStdinTool(execTool)

	startArgs, err := jsonMarshal(execInput{
		runInput: runInput{
			Skill:   testSkillName,
			Command: "read value; echo got:$value",
			Timeout: timeoutSecSmall,
		},
		YieldMS: 10,
	})
	require.NoError(t, err)

	reader, err := execTool.StreamableCall(context.Background(), startArgs)
	require.NoError(t, err)
	_, started := drainExecStream(t, reader)
	require.Equal(t, codeexecutor.ProgramStatusRunning, started.Status)
	require.NotEmpty(t, started.SessionID)
	require.Nil(t, started.Result)

	writeArgs, err := jsonMarshal(sessionWriteInput{
		SessionID: started.SessionID,
		Chars:     "hello",
		Submit:    true,
		YieldMS:   200,
	})
	require.NoError(t, err)

	reader, err = writeTool.StreamableCall(context.Background(), writeArgs)
	require.NoError(t, err)
	streamText, finished := drainExecStream(t, reader)
	require.Equal(t, codeexecutor.ProgramStatusExited, finished.Status)
	require.NotNil(t, finished.Result)
	require.Equal(t, 0, finished.Result.ExitCode)
	require.Contains(t, streamText, "got:hello")
	require.Contains(t, finished.Result.Stdout, "got:hello")
}

func TestExecTool_SelectionPromptWithoutTrailingNewline(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skillrepo.NewFSRepository(root)
	require.NoError(t, err)

	runTool := NewRunTool(repo, localexec.New())
	execTool := NewExecTool(runTool)
	writeTool := NewWriteStdinTool(execTool)

	startArgs, err := jsonMarshal(execInput{
		runInput: runInput{
			Skill: testSkillName,
			Command: "printf '1) one\\n2) two\\nChoose: '; " +
				"read value; echo pick:$value",
			Timeout: timeoutSecSmall,
		},
		YieldMS: 150,
	})
	require.NoError(t, err)

	reader, err := execTool.StreamableCall(context.Background(), startArgs)
	require.NoError(t, err)
	streamText, started := drainExecStream(t, reader)
	require.Equal(t, codeexecutor.ProgramStatusRunning, started.Status)
	require.Contains(t, streamText, "Choose:")
	require.NotNil(t, started.Interaction)
	require.True(t, started.Interaction.NeedsInput)
	require.Equal(t, interactionKindSelection, started.Interaction.Kind)

	writeArgs, err := jsonMarshal(sessionWriteInput{
		SessionID: started.SessionID,
		Chars:     "2",
		Submit:    true,
		YieldMS:   200,
	})
	require.NoError(t, err)

	reader, err = writeTool.StreamableCall(context.Background(), writeArgs)
	require.NoError(t, err)
	_, finished := drainExecStream(t, reader)
	require.Equal(t, codeexecutor.ProgramStatusExited, finished.Status)
	require.NotNil(t, finished.Result)
	require.Contains(t, finished.Result.Stdout, "pick:2")
}

func TestExecTool_EditorText(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skillrepo.NewFSRepository(root)
	require.NoError(t, err)

	runTool := NewRunTool(repo, localexec.New())
	execTool := NewExecTool(runTool)
	pollTool := NewPollSessionTool(execTool)

	args, err := jsonMarshal(execInput{
		runInput: runInput{
			Skill: testSkillName,
			Command: "mkdir -p out; $EDITOR out/note.txt; " +
				"cat out/note.txt",
			EditorText: "note body",
			Timeout:    timeoutSecSmall,
		},
		YieldMS: 1_000,
	})
	require.NoError(t, err)

	reader, err := execTool.StreamableCall(context.Background(), args)
	require.NoError(t, err)
	streamText, out := drainExecStream(t, reader)
	for attempt := 0; attempt < 5 &&
		out.Status == codeexecutor.ProgramStatusRunning; attempt++ {
		pollArgs, err := jsonMarshal(sessionPollInput{
			SessionID: out.SessionID,
			YieldMS:   500,
		})
		require.NoError(t, err)
		reader, err = pollTool.StreamableCall(
			context.Background(),
			pollArgs,
		)
		require.NoError(t, err)
		pollText, polled := drainExecStream(t, reader)
		streamText += pollText
		out = polled
	}
	require.Equal(t, codeexecutor.ProgramStatusExited, out.Status)
	require.NotNil(t, out.Result)
	require.Contains(t, streamText, "note body")
	require.Contains(t, out.Result.Stdout, "note body")
}

func TestKillSessionTool_RemovesSession(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skillrepo.NewFSRepository(root)
	require.NoError(t, err)

	runTool := NewRunTool(repo, localexec.New())
	execTool := NewExecTool(runTool)
	killTool := NewKillSessionTool(execTool)
	pollTool := NewPollSessionTool(execTool)

	startArgs, err := jsonMarshal(execInput{
		runInput: runInput{
			Skill:   testSkillName,
			Command: "sleep 5",
			Timeout: timeoutSecSmall,
		},
		YieldMS: 10,
	})
	require.NoError(t, err)

	reader, err := execTool.StreamableCall(context.Background(), startArgs)
	require.NoError(t, err)
	_, started := drainExecStream(t, reader)
	require.Equal(t, codeexecutor.ProgramStatusRunning, started.Status)

	killArgs, err := jsonMarshal(sessionKillInput{
		SessionID: started.SessionID,
	})
	require.NoError(t, err)
	res, err := killTool.Call(context.Background(), killArgs)
	require.NoError(t, err)

	out := res.(sessionKillOutput)
	require.True(t, out.OK)
	require.Equal(t, "killed", out.Status)

	pollArgs, err := jsonMarshal(sessionPollInput{
		SessionID: started.SessionID,
	})
	require.NoError(t, err)
	_, err = pollTool.StreamableCall(context.Background(), pollArgs)
	require.Error(t, err)
}

func TestExecArtifactsStateDelta(t *testing.T) {
	resultJSON, err := json.Marshal(execOutput{
		Status:    codeexecutor.ProgramStatusExited,
		SessionID: "s1",
		Result: &runOutput{
			ArtifactFiles: []artifactRef{{
				Name:    "a.txt",
				Version: 3,
			}},
		},
	})
	require.NoError(t, err)

	delta := execArtifactsStateDelta("call-1", resultJSON)
	require.NotNil(t, delta)
	raw, ok := delta[skillrepo.StateKeyArtifacts]
	require.True(t, ok)

	var got skillRunArtifactsDelta
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Len(t, got.Artifacts, 1)
	require.Equal(t, "artifact://a.txt@3", got.Artifacts[0].Ref)
}

type stubProgramSession struct {
	mu         sync.Mutex
	id         string
	polls      []codeexecutor.ProgramPoll
	logOutput  string
	runResult  codeexecutor.RunResult
	writes     []string
	killErr    error
	killCalled bool
	closeCount int
}

func (s *stubProgramSession) ID() string { return s.id }

func (s *stubProgramSession) Poll(limit *int) codeexecutor.ProgramPoll {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.polls) == 0 {
		return codeexecutor.ProgramPoll{
			Status: codeexecutor.ProgramStatusExited,
		}
	}
	poll := s.polls[0]
	if len(s.polls) > 1 {
		s.polls = s.polls[1:]
	}
	if limit != nil && *limit > 0 && poll.Output != "" {
		lines := strings.Split(poll.Output, "\n")
		if len(lines) > *limit {
			poll.Output = strings.Join(lines[:*limit], "\n")
		}
	}
	return poll
}

func (s *stubProgramSession) Log(
	offset *int,
	limit *int,
) codeexecutor.ProgramLog {
	output := s.logOutput
	if limit != nil && *limit > 0 && output != "" {
		lines := strings.Split(output, "\n")
		if len(lines) > *limit {
			output = strings.Join(lines[:*limit], "\n")
		}
	}
	out := codeexecutor.ProgramLog{
		Output:     output,
		Offset:     0,
		NextOffset: len(strings.Split(output, "\n")),
	}
	if offset != nil {
		out.Offset = *offset
	}
	return out
}

func (s *stubProgramSession) Write(
	data string,
	newline bool,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if newline {
		data += "\n"
	}
	s.writes = append(s.writes, data)
	return nil
}

func (s *stubProgramSession) Kill(grace time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = grace
	s.killCalled = true
	return s.killErr
}

func (s *stubProgramSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeCount++
	return nil
}

func (s *stubProgramSession) RunResult() codeexecutor.RunResult {
	return s.runResult
}

type logOnlyProgramSession struct {
	id        string
	logOutput string
}

func (s *logOnlyProgramSession) ID() string { return s.id }

func (s *logOnlyProgramSession) Poll(limit *int) codeexecutor.ProgramPoll {
	_ = limit
	return codeexecutor.ProgramPoll{
		Status: codeexecutor.ProgramStatusExited,
	}
}

func (s *logOnlyProgramSession) Log(
	offset *int,
	limit *int,
) codeexecutor.ProgramLog {
	output := s.logOutput
	if limit != nil && *limit > 0 {
		lines := strings.Split(output, "\n")
		if len(lines) > *limit {
			output = strings.Join(lines[:*limit], "\n")
		}
	}
	start := 0
	if offset != nil {
		start = *offset
	}
	return codeexecutor.ProgramLog{
		Output:     output,
		Offset:     start,
		NextOffset: len(strings.Split(output, "\n")),
	}
}

func (s *logOnlyProgramSession) Write(
	data string,
	newline bool,
) error {
	_ = data
	_ = newline
	return nil
}

func (s *logOnlyProgramSession) Kill(grace time.Duration) error {
	_ = grace
	return nil
}

func (s *logOnlyProgramSession) Close() error { return nil }

func TestExecTool_DeclarationsAndHelpers(t *testing.T) {
	runTool := NewRunTool(nil, nil)
	execTool := NewExecTool(runTool)
	writeTool := NewWriteStdinTool(execTool)
	pollTool := NewPollSessionTool(execTool)
	killTool := NewKillSessionTool(execTool)

	require.Equal(t, "skill_exec", execTool.Declaration().Name)
	require.Equal(t, "skill_write_stdin", writeTool.Declaration().Name)
	require.Equal(t, "skill_poll_session", pollTool.Declaration().Name)
	require.Equal(t, "skill_kill_session", killTool.Declaration().Name)

	require.Equal(
		t,
		250*time.Millisecond,
		yieldDuration(250, defaultExecYieldMS),
	)
	require.Equal(
		t,
		time.Duration(defaultExecYieldMS)*time.Millisecond,
		yieldDuration(0, defaultExecYieldMS),
	)
	require.Equal(t, 40, *pollLineLimit(40))
	require.Equal(t, defaultPollLines, *pollLineLimit(0))

	require.Equal(t, "prompt", lastNonEmptyLine("a\n\nprompt"))
	require.True(t, hasSelectionItems("1) one\n2) two"))
	require.False(t, hasSelectionItems("1) only"))

	require.Equal(
		t,
		&sessionInteraction{
			NeedsInput: true,
			Kind:       interactionKindSelection,
			Hint:       "Enter the number:",
		},
		detectInteraction(codeexecutor.ProgramPoll{
			Status: codeexecutor.ProgramStatusRunning,
			Output: "1) one\n2) two\nEnter the number:",
		}),
	)
	require.Equal(
		t,
		&sessionInteraction{
			NeedsInput: true,
			Kind:       interactionKindPrompt,
			Hint:       "Continue?",
		},
		detectInteraction(codeexecutor.ProgramPoll{
			Status: codeexecutor.ProgramStatusRunning,
			Output: "Continue?",
		}),
	)
	require.Nil(t, detectInteraction(codeexecutor.ProgramPoll{
		Status: codeexecutor.ProgramStatusExited,
		Output: "Done",
	}))
}

func TestExecTool_ParseAndStateDeltaEdges(t *testing.T) {
	_, err := parseExecArgs([]byte("{"))
	require.Error(t, err)

	_, err = parseExecArgs([]byte(`{"skill":"","command":"x"}`))
	require.Error(t, err)

	in, err := parseExecArgs([]byte(
		`{"skill":" demo ","command":"echo hi"}`,
	))
	require.NoError(t, err)
	require.Equal(t, " demo ", in.Skill)

	_, err = parseSessionWriteArgs([]byte(`{"chars":"x"}`))
	require.Error(t, err)
	_, err = parseSessionPollArgs([]byte(`{"yield_ms":1}`))
	require.Error(t, err)

	require.Nil(t, execArtifactsStateDelta("", []byte(`{}`)))
	require.Nil(t, execArtifactsStateDelta("call", []byte(`nope`)))
	require.Nil(
		t,
		execArtifactsStateDelta("call", []byte(`{"status":"running"}`)),
	)
}

func TestExecTool_SessionHelpers(t *testing.T) {
	execTool := NewExecTool(NewRunTool(nil, nil))
	now := time.Date(2026, 3, 6, 18, 0, 0, 0, time.UTC)
	execTool.clock = func() time.Time { return now }
	execTool.ttl = time.Minute

	expired := &execSession{
		proc:        &stubProgramSession{id: "expired"},
		finalized:   true,
		finalizedAt: now.Add(-2 * time.Minute),
	}
	fresh := &execSession{
		proc: &stubProgramSession{id: "fresh"},
	}

	execTool.putSession("expired", expired)
	execTool.putSession("fresh", fresh)

	_, err := execTool.getSession("expired")
	require.Error(t, err)

	got, err := execTool.getSession("fresh")
	require.NoError(t, err)
	require.Equal(t, fresh, got)

	removed, err := execTool.removeSession("fresh")
	require.NoError(t, err)
	require.Equal(t, fresh, removed)

	_, err = execTool.removeSession("fresh")
	require.Error(t, err)
}

func TestExecTool_WaitForProgramOutputAndSessionRunResult(t *testing.T) {
	session := &stubProgramSession{
		id: "s1",
		polls: []codeexecutor.ProgramPoll{
			{
				Status:     codeexecutor.ProgramStatusRunning,
				Output:     "line1",
				Offset:     0,
				NextOffset: 1,
			},
			{
				Status:     codeexecutor.ProgramStatusRunning,
				Output:     "\nline2",
				Offset:     1,
				NextOffset: 2,
			},
			{
				Status:     codeexecutor.ProgramStatusExited,
				Output:     "\nline3",
				Offset:     2,
				NextOffset: 3,
				ExitCode:   intPtr(7),
			},
		},
		runResult: codeexecutor.RunResult{
			Stdout:   "line1\nline2\nline3",
			Stderr:   "warn",
			ExitCode: 7,
		},
	}

	poll := waitForProgramOutput(
		session,
		200*time.Millisecond,
		pollLineLimit(10),
	)
	require.Equal(t, codeexecutor.ProgramStatusExited, poll.Status)
	require.Equal(t, "line1\nline2\nline3", poll.Output)
	require.Equal(t, 0, poll.Offset)
	require.Equal(t, 3, poll.NextOffset)

	res := sessionRunResult(session, poll)
	require.Equal(t, "warn", res.Stderr)
	require.Equal(t, 7, res.ExitCode)

	fallback := sessionRunResult(
		&logOnlyProgramSession{
			id:        "s2",
			logOutput: "fallback output",
		},
		codeexecutor.ProgramPoll{
			ExitCode: intPtr(3),
		},
	)
	require.Equal(t, "fallback output", fallback.Stdout)
	require.Equal(t, 3, fallback.ExitCode)
}

func TestWritePollAndKillTools_ErrorPaths(t *testing.T) {
	writeTool := NewWriteStdinTool(nil)
	pollTool := NewPollSessionTool(nil)
	killTool := NewKillSessionTool(nil)

	_, err := writeTool.StreamableCall(context.Background(), []byte(`{}`))
	require.Error(t, err)

	_, err = pollTool.StreamableCall(context.Background(), []byte(`{}`))
	require.Error(t, err)

	_, err = killTool.Call(context.Background(), []byte(`{}`))
	require.Error(t, err)

	execTool := NewExecTool(NewRunTool(nil, nil))
	exited := &stubProgramSession{
		id: "done",
		polls: []codeexecutor.ProgramPoll{{
			Status: codeexecutor.ProgramStatusExited,
		}},
	}
	execTool.putSession("done", &execSession{proc: exited})

	res, err := NewKillSessionTool(execTool).Call(
		context.Background(),
		[]byte(`{"session_id":"done"}`),
	)
	require.NoError(t, err)
	require.Equal(
		t,
		codeexecutor.ProgramStatusExited,
		res.(sessionKillOutput).Status,
	)
}

func intPtr(v int) *int {
	return &v
}
