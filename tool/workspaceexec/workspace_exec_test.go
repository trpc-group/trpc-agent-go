//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspaceexec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/internal/programsession"
	"trpc.group/trpc-go/trpc-agent-go/internal/skillstage"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	toolskill "trpc.group/trpc-go/trpc-agent-go/tool/skill"
)

const (
	testSkillName   = "echoer"
	timeoutSecSmall = 5
)

func writeSkill(t *testing.T, root, name string) {
	t.Helper()
	sdir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(sdir, 0o755))
	data := "---\nname: " + name + "\n" +
		"description: simple echo skill\n---\nbody\n"
	err := os.WriteFile(filepath.Join(sdir, "SKILL.md"), []byte(data), 0o644)
	require.NoError(t, err)
}

func TestExecTool_ExecutesWithoutSkillsRepo(t *testing.T) {
	exec := localexec.New()
	tl := NewExecTool(exec)

	args := execInput{
		Command: "mkdir -p work/demo && printf hello > work/demo/a.txt && cat work/demo/a.txt",
		Timeout: timeoutSecSmall,
	}
	enc, err := json.Marshal(args)
	require.NoError(t, err)

	res, err := tl.Call(context.Background(), enc)
	require.NoError(t, err)

	out := res.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusExited, out.Status)
	require.NotNil(t, out.ExitCode)
	require.Equal(t, 0, *out.ExitCode)
	require.Contains(t, out.Output, "hello")
	require.Empty(t, out.SessionID)
}

func TestExecTool_Declaration_DescribesGeneralShellUsage(t *testing.T) {
	tl := NewExecTool(localexec.New())

	decl := tl.Declaration()
	require.NotNil(t, decl)
	require.Contains(t, decl.Description, "general command runner")
	require.Contains(t, decl.Description, "curl")
	require.Contains(t, decl.Description, "current executor environment allows them")
}

func TestExecTool_UsesExistingStagedSkillFromCWD(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	reg := codeexecutor.NewWorkspaceRegistry()
	stager := skillstage.New()
	tl := NewExecTool(exec, WithWorkspaceRegistry(reg))
	ctx := context.Background()

	eng := tl.resolver.EnsureEngine()
	ws, err := tl.resolver.CreateWorkspace(ctx, eng, "workspace")
	require.NoError(t, err)
	skillRoot, err := repo.Path(testSkillName)
	require.NoError(t, err)
	require.NoError(t, stager.StageSkill(ctx, eng, ws, skillRoot, testSkillName))

	args := execInput{
		Command: "test -f SKILL.md && printf ok",
		Cwd:     "skills/" + testSkillName,
		Timeout: timeoutSecSmall,
	}
	enc, err := json.Marshal(args)
	require.NoError(t, err)

	res, err := tl.Call(context.Background(), enc)
	require.NoError(t, err)

	out := res.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusExited, out.Status)
	require.NotNil(t, out.ExitCode)
	require.Equal(t, 0, *out.ExitCode)
	require.Equal(t, "ok", out.Output)
}

func TestExecTool_DoesNotAutoStageSkillFromCWD(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	exec := localexec.New()
	tl := NewExecTool(exec)

	args := execInput{
		Command: "test ! -f SKILL.md && printf empty",
		Cwd:     "skills/" + testSkillName,
		Timeout: timeoutSecSmall,
	}
	enc, err := json.Marshal(args)
	require.NoError(t, err)

	res, err := tl.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusExited, out.Status)
	require.NotNil(t, out.ExitCode)
	require.Equal(t, 0, *out.ExitCode)
	require.Equal(t, "empty", out.Output)
}

func TestExecTool_SharesWorkspaceWithSkillRun(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	reg := codeexecutor.NewWorkspaceRegistry()
	runTool := toolskill.NewRunTool(
		repo,
		exec,
		toolskill.WithWorkspaceRegistry(reg),
	)
	execTool := NewExecTool(exec, WithWorkspaceRegistry(reg))

	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{ID: "sess-workspace-exec"}),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	runArgs := map[string]any{
		"skill":   testSkillName,
		"command": "mkdir -p out && printf hello > out/a.txt",
		"timeout": timeoutSecSmall,
	}
	runEnc, err := json.Marshal(runArgs)
	require.NoError(t, err)
	_, err = runTool.Call(ctx, runEnc)
	require.NoError(t, err)

	execArgs := execInput{
		Command: "cat out/a.txt",
		Timeout: timeoutSecSmall,
	}
	execEnc, err := json.Marshal(execArgs)
	require.NoError(t, err)

	res, err := execTool.Call(ctx, execEnc)
	require.NoError(t, err)

	out := res.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusExited, out.Status)
	require.NotNil(t, out.ExitCode)
	require.Equal(t, 0, *out.ExitCode)
	require.Equal(t, "hello", out.Output)
}

func TestExecTool_SkillsCWDDoesNotRequireRepo(t *testing.T) {
	exec := localexec.New()
	tl := NewExecTool(exec)

	args := execInput{
		Command: "test ! -f SKILL.md && printf empty",
		Cwd:     "skills/demo",
		Timeout: timeoutSecSmall,
	}
	enc, err := json.Marshal(args)
	require.NoError(t, err)

	res, err := tl.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusExited, out.Status)
	require.NotNil(t, out.ExitCode)
	require.Equal(t, 0, *out.ExitCode)
	require.Equal(t, "empty", out.Output)
}

func TestExecTool_BackgroundAndWriteStdin(t *testing.T) {
	exec := localexec.New()
	execTool := NewExecTool(exec)
	writeTool := NewWriteStdinTool(execTool)

	startArgs := execInput{
		Command:     "printf 'ready\\n'; read v; echo out:$v; echo err:$v >&2",
		Cwd:         "work",
		Background:  true,
		YieldTimeMS: intPtr(100),
		Timeout:     timeoutSecSmall,
	}
	startEnc, err := json.Marshal(startArgs)
	require.NoError(t, err)

	startRes, err := execTool.Call(context.Background(), startEnc)
	require.NoError(t, err)
	started := startRes.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusRunning, started.Status)
	require.NotEmpty(t, started.SessionID)
	require.Contains(t, started.Output, "ready")

	writeArgs := writeInput{
		SessionID:     started.SessionID,
		Chars:         "hello",
		AppendNewline: boolPtr(true),
		YieldTimeMS:   intPtr(100),
	}
	writeEnc, err := json.Marshal(writeArgs)
	require.NoError(t, err)

	var out execOutput
	require.Eventually(t, func() bool {
		res, err := writeTool.Call(context.Background(), writeEnc)
		if err != nil {
			return false
		}
		out = res.(execOutput)
		if out.Status == codeexecutor.ProgramStatusExited {
			return true
		}
		pollEnc, err := json.Marshal(writeInput{
			SessionID:   started.SessionID,
			YieldTimeMS: intPtr(50),
		})
		require.NoError(t, err)
		res, err = writeTool.Call(context.Background(), pollEnc)
		if err != nil {
			return false
		}
		out = res.(execOutput)
		return out.Status == codeexecutor.ProgramStatusExited
	}, 3*time.Second, 20*time.Millisecond)
	require.NotNil(t, out.ExitCode)
	require.Equal(t, 0, *out.ExitCode)
	require.Contains(t, out.Output, "out:hello")
}

func TestExecTool_WriteStdin_AliasFieldsAndSubmit(t *testing.T) {
	exec := localexec.New()
	execTool := NewExecTool(exec)
	writeTool := NewWriteStdinTool(execTool)

	startArgs := execInput{
		Command:    "printf 'ready\\n'; read v; echo out:$v",
		Background: true,
		YieldMs:    intPtr(50),
		TimeoutSec: intPtr(timeoutSecSmall),
	}
	startEnc, err := json.Marshal(startArgs)
	require.NoError(t, err)

	startRes, err := execTool.Call(context.Background(), startEnc)
	require.NoError(t, err)
	started := startRes.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusRunning, started.Status)
	require.NotEmpty(t, started.SessionID)

	writeEnc, err := json.Marshal(writeInput{
		SessionIDOld: started.SessionID,
		Chars:        "hello",
		Submit:       boolPtr(true),
		YieldMs:      intPtr(100),
	})
	require.NoError(t, err)

	var out execOutput
	require.Eventually(t, func() bool {
		res, err := writeTool.Call(context.Background(), writeEnc)
		if err != nil {
			return false
		}
		out = res.(execOutput)
		return out.Status == codeexecutor.ProgramStatusExited
	}, 3*time.Second, 20*time.Millisecond)
	require.NotNil(t, out.ExitCode)
	require.Equal(t, 0, *out.ExitCode)
	require.Contains(t, out.Output, "out:hello")
}

func TestExecTool_NonInteractiveExecutorIgnoresYieldTimeMS(t *testing.T) {
	exec := &nonInteractiveExec{}
	tl := NewExecTool(exec)

	args := execInput{
		Command:     "echo hello",
		YieldTimeMS: intPtr(100),
		Timeout:     timeoutSecSmall,
	}
	enc, err := json.Marshal(args)
	require.NoError(t, err)

	res, err := tl.Call(context.Background(), enc)
	require.NoError(t, err)

	out := res.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusExited, out.Status)
	require.NotNil(t, out.ExitCode)
	require.Equal(t, 0, *out.ExitCode)
	require.Equal(t, "hello", out.Output)
}

func TestExecTool_NonInteractiveExecutorRejectsInteractiveFlags(t *testing.T) {
	exec := &nonInteractiveExec{}
	tl := NewExecTool(exec)

	for _, args := range []execInput{
		{Command: "echo hello", Background: true, Timeout: timeoutSecSmall},
		{Command: "echo hello", TTY: boolPtr(true), Timeout: timeoutSecSmall},
		{Command: "echo hello", PTY: boolPtr(true), Timeout: timeoutSecSmall},
	} {
		enc, err := json.Marshal(args)
		require.NoError(t, err)

		_, err = tl.Call(context.Background(), enc)
		require.Error(t, err)
		require.Contains(t, err.Error(), "interactive sessions are not supported")
	}
}

func TestExecTool_KillSession(t *testing.T) {
	exec := localexec.New()
	execTool := NewExecTool(exec)
	killTool := NewKillSessionTool(execTool)

	startEnc, err := json.Marshal(execInput{
		Command:    "sleep 30",
		Background: true,
		Timeout:    timeoutSecSmall,
	})
	require.NoError(t, err)

	startRes, err := execTool.Call(context.Background(), startEnc)
	require.NoError(t, err)
	started := startRes.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusRunning, started.Status)
	require.NotEmpty(t, started.SessionID)

	killEnc, err := json.Marshal(killInput{
		SessionID: started.SessionID,
	})
	require.NoError(t, err)
	res, err := killTool.Call(context.Background(), killEnc)
	require.NoError(t, err)

	out := res.(killOutput)
	require.True(t, out.OK)
	require.Equal(t, started.SessionID, out.SessionID)
	require.Equal(t, "killed", out.Status)
}

func TestExecTool_KillSession_AliasSessionID(t *testing.T) {
	exec := localexec.New()
	execTool := NewExecTool(exec)
	killTool := NewKillSessionTool(execTool)

	startEnc, err := json.Marshal(execInput{
		Command:    "sleep 30",
		Background: true,
		Timeout:    timeoutSecSmall,
	})
	require.NoError(t, err)

	startRes, err := execTool.Call(context.Background(), startEnc)
	require.NoError(t, err)
	started := startRes.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusRunning, started.Status)

	killEnc, err := json.Marshal(killInput{SessionIDOld: started.SessionID})
	require.NoError(t, err)
	res, err := killTool.Call(context.Background(), killEnc)
	require.NoError(t, err)

	out := res.(killOutput)
	require.True(t, out.OK)
	require.Equal(t, started.SessionID, out.SessionID)
}

func TestExecTool_KillSession_KillFailurePreservesSession(t *testing.T) {
	execTool := &ExecTool{
		sessions: map[string]*execSession{},
		ttl:      programsession.DefaultSessionTTL,
		clock:    time.Now,
	}
	killTool := NewKillSessionTool(execTool)

	const sessionID = "sess-fail"
	execTool.putSession(sessionID, &execSession{
		proc: failingProgramSession{
			poll: codeexecutor.ProgramPoll{Status: codeexecutor.ProgramStatusRunning},
			err:  errors.New("kill failed"),
		},
	})

	enc, err := json.Marshal(killInput{SessionID: sessionID})
	require.NoError(t, err)

	_, err = killTool.Call(context.Background(), enc)
	require.Error(t, err)
	require.Contains(t, err.Error(), "kill failed")

	_, err = execTool.getSession(sessionID)
	require.NoError(t, err)
}

func TestExecTool_FinalizeAndRemoveSession_CloseFailurePreservesSession(t *testing.T) {
	execTool := &ExecTool{
		sessions: map[string]*execSession{},
		ttl:      programsession.DefaultSessionTTL,
		clock:    time.Now,
	}

	const sessionID = "sess-close-fail"
	execTool.putSession(sessionID, &execSession{
		proc: failingProgramSession{
			poll:     codeexecutor.ProgramPoll{Status: codeexecutor.ProgramStatusExited},
			closeErr: errors.New("close failed"),
		},
	})

	err := execTool.finalizeAndRemoveSession(sessionID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "close failed")

	sess, err := execTool.getSession(sessionID)
	require.NoError(t, err)
	require.True(t, sess.finalized)
	require.False(t, sess.finalizedAt.IsZero())
	require.False(t, sess.exitedAt.IsZero())
}

func TestExecTool_WriteStdin_CloseFailurePreservesSessionID(t *testing.T) {
	execTool := &ExecTool{
		sessions: map[string]*execSession{},
		ttl:      programsession.DefaultSessionTTL,
		clock:    time.Now,
	}
	writeTool := NewWriteStdinTool(execTool)

	const sessionID = "sess-write-close-fail"
	execTool.putSession(sessionID, &execSession{
		proc: failingProgramSession{
			poll: codeexecutor.ProgramPoll{
				Status:   codeexecutor.ProgramStatusExited,
				Output:   "done",
				ExitCode: intPtr(0),
			},
			closeErr: errors.New("close failed"),
		},
	})

	enc, err := json.Marshal(writeInput{SessionID: sessionID})
	require.NoError(t, err)

	res, err := writeTool.Call(context.Background(), enc)
	require.NoError(t, err)

	out := res.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusExited, out.Status)
	require.Equal(t, sessionID, out.SessionID)
	require.Equal(t, "done", out.Output)

	_, err = execTool.getSession(sessionID)
	require.NoError(t, err)
}

func TestExecTool_ReapsExitedSessionAfterTTL(t *testing.T) {
	now := time.Date(2026, 3, 19, 10, 0, 0, 0, time.UTC)
	execTool := &ExecTool{
		sessions: map[string]*execSession{},
		ttl:      time.Minute,
	}
	execTool.clock = func() time.Time { return now }

	const sessionID = "sess-exited"
	execTool.putSession(sessionID, &execSession{
		proc: failingProgramSession{
			poll: codeexecutor.ProgramPoll{
				Status: codeexecutor.ProgramStatusExited,
			},
		},
	})

	_, err := execTool.getSession(sessionID)
	require.NoError(t, err)

	now = now.Add(2 * time.Minute)
	_, err = execTool.getSession(sessionID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown session_id")
}

func TestExecTool_DoesNotDropExpiredSessionWhenCloseFails(t *testing.T) {
	now := time.Date(2026, 3, 19, 10, 0, 0, 0, time.UTC)
	execTool := &ExecTool{
		sessions: map[string]*execSession{},
		ttl:      time.Minute,
	}
	execTool.clock = func() time.Time { return now }

	const sessionID = "sess-expired-close-fail"
	execTool.putSession(sessionID, &execSession{
		proc: failingProgramSession{
			poll: codeexecutor.ProgramPoll{
				Status: codeexecutor.ProgramStatusExited,
			},
			closeErr: errors.New("close failed"),
		},
	})

	_, err := execTool.getSession(sessionID)
	require.NoError(t, err)

	now = now.Add(2 * time.Minute)
	_, err = execTool.getSession(sessionID)
	require.NoError(t, err)
}

type failingProgramSession struct {
	poll     codeexecutor.ProgramPoll
	err      error
	closeErr error
}

func (p failingProgramSession) ID() string                           { return "failing" }
func (p failingProgramSession) Poll(_ *int) codeexecutor.ProgramPoll { return p.poll }
func (p failingProgramSession) State() codeexecutor.ProgramState {
	state := codeexecutor.ProgramState{Status: p.poll.Status}
	if p.poll.ExitCode != nil {
		code := *p.poll.ExitCode
		state.ExitCode = &code
	}
	return state
}
func (p failingProgramSession) Log(_, _ *int) codeexecutor.ProgramLog {
	return codeexecutor.ProgramLog{}
}
func (p failingProgramSession) Write(string, bool) error { return nil }
func (p failingProgramSession) Kill(time.Duration) error { return p.err }
func (p failingProgramSession) Close() error             { return p.closeErr }

type nonInteractiveExec struct{}

func (e *nonInteractiveExec) ExecuteCode(
	context.Context,
	codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (e *nonInteractiveExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func (e *nonInteractiveExec) Engine() codeexecutor.Engine {
	return codeexecutor.NewEngine(
		&nonInteractiveMgr{},
		&nonInteractiveFS{},
		&nonInteractiveRunner{},
	)
}

type nonInteractiveMgr struct{}

func (m *nonInteractiveMgr) CreateWorkspace(
	context.Context,
	string,
	codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{ID: "ws", Path: "/tmp/ws"}, nil
}

func (m *nonInteractiveMgr) Cleanup(context.Context, codeexecutor.Workspace) error {
	return nil
}

type nonInteractiveFS struct{}

func (f *nonInteractiveFS) PutFiles(
	context.Context,
	codeexecutor.Workspace,
	[]codeexecutor.PutFile,
) error {
	return nil
}

func (f *nonInteractiveFS) StageDirectory(
	context.Context,
	codeexecutor.Workspace,
	string,
	string,
	codeexecutor.StageOptions,
) error {
	return nil
}

func (f *nonInteractiveFS) Collect(
	context.Context,
	codeexecutor.Workspace,
	[]string,
) ([]codeexecutor.File, error) {
	return nil, nil
}

func (f *nonInteractiveFS) StageInputs(
	context.Context,
	codeexecutor.Workspace,
	[]codeexecutor.InputSpec,
) error {
	return nil
}

func (f *nonInteractiveFS) CollectOutputs(
	context.Context,
	codeexecutor.Workspace,
	codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}

type nonInteractiveRunner struct{}

func (r *nonInteractiveRunner) RunProgram(
	context.Context,
	codeexecutor.Workspace,
	codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	return codeexecutor.RunResult{
		Stdout:   "hello",
		ExitCode: 0,
	}, nil
}

func boolPtr(v bool) *bool { return &v }

func intPtr(v int) *int { return &v }
