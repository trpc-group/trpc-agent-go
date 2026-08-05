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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/programsession"
	"trpc.group/trpc-go/trpc-agent-go/internal/skillstage"
	"trpc.group/trpc-go/trpc-agent-go/internal/workspaceprep"
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
	require.Contains(t, decl.Description, "Execute a shell command in the current workspace.")
	require.NotContains(t, decl.Description, "curl")
	require.NotContains(t, decl.Description, "network")
	require.NotContains(t, decl.Description, "git")
	require.Contains(t, decl.OutputSchema.Properties, "truncated")
	require.Contains(t, decl.OutputSchema.Properties, "total_bytes")
}

func TestExecTool_OutputLimitsApplyBeforeReturn(t *testing.T) {
	const maxBytes = 40
	exec := localexec.New()
	tl := NewExecTool(exec, WithOutputLimits(OutputLimits{
		MaxOutputBytes: maxBytes,
	}))

	original := "HEAD!" + strings.Repeat("x", 80) + "!TAIL"
	args, err := json.Marshal(execInput{
		Command: "printf '" + original + "'",
		Timeout: timeoutSecSmall,
	})
	require.NoError(t, err)

	res, err := tl.Call(context.Background(), args)
	require.NoError(t, err)
	out := res.(execOutput)
	require.True(t, out.Truncated)
	require.Equal(t, len(original), out.TotalBytes)
	require.LessOrEqual(t, len(out.Output), maxBytes)
	require.Contains(t, out.Output, outputTruncatedMarker)
	require.True(t, strings.HasPrefix(out.Output, "HEAD!"))
	require.True(t, strings.HasSuffix(out.Output, "!TAIL"))
}

func TestExecTool_OutputLimitsSanitizeInvalidUTF8BeforeReturn(t *testing.T) {
	const maxBytes = 4
	exec := localexec.New()
	tl := NewExecTool(exec, WithOutputLimits(OutputLimits{
		MaxOutputBytes: maxBytes,
	}))

	args, err := json.Marshal(execInput{
		Command: "printf '\\377abc'",
		Timeout: timeoutSecSmall,
	})
	require.NoError(t, err)

	res, err := tl.Call(context.Background(), args)
	require.NoError(t, err)
	out := res.(execOutput)
	require.True(t, out.Truncated)
	require.Equal(t, maxBytes, out.TotalBytes)
	require.Equal(t, "abc", out.Output)
	require.True(t, utf8.ValidString(out.Output))
	require.LessOrEqual(t, len(out.Output), maxBytes)
}

func TestExecTool_OutputLimitsDisabledByDefault(t *testing.T) {
	original := strings.Repeat("x", 128)
	out := (&ExecTool{}).limitOutput(execOutput{Output: original})
	require.Equal(t, original, out.Output)
	require.False(t, out.Truncated)
	require.Zero(t, out.TotalBytes)
}

func TestExecTool_OutputLimitsLeaveBoundedOutputUnchanged(t *testing.T) {
	original := execOutput{
		Status: codeexecutor.ProgramStatusExited,
		Output: "short",
	}
	out := (&ExecTool{outputLimit: len(original.Output)}).limitOutput(original)
	require.Equal(t, original, out)
}

func TestExecTool_OutputLimitsAllowNilReceiver(t *testing.T) {
	original := execOutput{Output: strings.Repeat("x", 128)}
	var execTool *ExecTool
	require.Equal(t, original, execTool.limitOutput(original))
}

func TestExecTool_OutputLimitsPreserveUTF8(t *testing.T) {
	limit := len(outputTruncatedMarker) + 6
	original := "你" + strings.Repeat("x", 80) + "好"
	out := (&ExecTool{outputLimit: limit}).limitOutput(execOutput{
		Output: original,
	})
	require.True(t, out.Truncated)
	require.Equal(t, len(original), out.TotalBytes)
	require.LessOrEqual(t, len(out.Output), limit)
	require.True(t, strings.HasPrefix(out.Output, "你"))
	require.True(t, strings.HasSuffix(out.Output, "好"))
	require.True(t, utf8.ValidString(out.Output))
}

func TestExecTool_OutputLimitsTinyBudgetUsesUTF8Prefix(t *testing.T) {
	tests := []struct {
		name     string
		limit    int
		expected string
	}{
		{name: "partial multibyte rune", limit: 2, expected: ""},
		{name: "complete rune and ASCII", limit: 4, expected: "你a"},
		{name: "marker does not fit", limit: len(outputTruncatedMarker), expected: "你abcdefghijklmnopqrstuvw"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := "你abcdefghijklmnopqrstuvwxyz"
			out := (&ExecTool{outputLimit: tc.limit}).limitOutput(execOutput{
				Output: original,
			})
			require.True(t, out.Truncated)
			require.Equal(t, len(original), out.TotalBytes)
			require.Equal(t, tc.expected, out.Output)
			require.LessOrEqual(t, len(out.Output), tc.limit)
			require.True(t, utf8.ValidString(out.Output))
			require.NotContains(t, out.Output, outputTruncatedMarker)
		})
	}
}

func TestWindowOutputLeavesBoundedOutputUnchanged(t *testing.T) {
	require.Equal(t, "output", windowOutput("output", 0))
	require.Equal(t, "output", windowOutput("output", len("output")))
}

func TestUTF8WindowHelpersBoundaryCases(t *testing.T) {
	t.Run("empty budget", func(t *testing.T) {
		require.Empty(t, utf8Prefix("你", 0))
		require.Empty(t, utf8Suffix("你", 0))
	})
	t.Run("input fits budget", func(t *testing.T) {
		require.Equal(t, "你", utf8Prefix("你", len("你")))
		require.Equal(t, "你", utf8Suffix("你", len("你")))
	})
	t.Run("suffix starts inside multibyte rune", func(t *testing.T) {
		require.Equal(t, "好", utf8Suffix("a你好", 4))
	})
}

func TestExecTool_AutoStagesInvocationMessageFiles(t *testing.T) {
	exec := localexec.New()
	tl := NewExecTool(exec)

	msg := model.NewUserMessage("upload")
	msg.AddFileData("notes.txt", []byte("hello from upload"), "text/plain")
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(msg),
		agent.WithInvocationSession(&session.Session{ID: "sess-upload"}),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	args := execInput{
		Command: "cat work/inputs/notes.txt",
		Timeout: timeoutSecSmall,
	}
	enc, err := json.Marshal(args)
	require.NoError(t, err)

	res, err := tl.Call(ctx, enc)
	require.NoError(t, err)

	out := res.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusExited, out.Status)
	require.NotNil(t, out.ExitCode)
	require.Equal(t, 0, *out.ExitCode)
	require.Equal(t, "hello from upload", out.Output)
}

func TestExecTool_AutoStagesSessionFilesAcrossTurns(t *testing.T) {
	exec := localexec.New()
	tl := NewExecTool(exec)

	prior := model.NewUserMessage("uploaded earlier")
	prior.AddFileData("history.txt", []byte("session upload"), "text/plain")
	sess := &session.Session{
		ID: "sess-history",
		Events: []event.Event{{
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: prior,
				}},
			},
		}},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("use previous upload")),
		agent.WithInvocationSession(sess),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	args := execInput{
		Command: "cat work/inputs/history.txt",
		Timeout: timeoutSecSmall,
	}
	enc, err := json.Marshal(args)
	require.NoError(t, err)

	res, err := tl.Call(ctx, enc)
	require.NoError(t, err)

	out := res.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusExited, out.Status)
	require.NotNil(t, out.ExitCode)
	require.Equal(t, 0, *out.ExitCode)
	require.Equal(t, "session upload", out.Output)
}

func TestExecTool_AutoStageFailureDoesNotBlockCommand(t *testing.T) {
	exec := localexec.New()
	tl := NewExecTool(exec)

	msg := model.NewUserMessage("upload")
	msg.AddFileIDWithName("provider-file-1", "missing.txt")
	inv := agent.NewInvocation(agent.WithInvocationMessage(msg))
	ctx := agent.NewInvocationContext(context.Background(), inv)

	args := execInput{
		Command: "printf ok",
		Timeout: timeoutSecSmall,
	}
	enc, err := json.Marshal(args)
	require.NoError(t, err)

	res, err := tl.Call(ctx, enc)
	require.NoError(t, err)

	out := res.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusExited, out.Status)
	require.NotNil(t, out.ExitCode)
	require.Equal(t, 0, *out.ExitCode)
	require.Equal(t, "ok", out.Output)
}

func TestExecTool_Declaration_NonInteractiveOmitsSessionFields(t *testing.T) {
	tl := NewExecTool(&nonInteractiveExec{})

	decl := tl.Declaration()
	require.NotNil(t, decl)
	require.NotContains(t, decl.Description, "workspace_write_stdin")
	require.NotContains(t, decl.Description, "background=true")
	_, hasBackground := decl.InputSchema.Properties["background"]
	require.False(t, hasBackground)
	_, hasYield := decl.InputSchema.Properties["yield_time_ms"]
	require.False(t, hasYield)
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
	const maxBytes = 40
	initialOutput := "HEAD!" + strings.Repeat("x", 80) + "!TAIL"
	exec := localexec.New()
	execTool := NewExecTool(exec, WithOutputLimits(OutputLimits{
		MaxOutputBytes: maxBytes,
	}))
	writeTool := NewWriteStdinTool(execTool)

	startArgs := execInput{
		Command:     "printf '" + initialOutput + "\\n'; read v; echo out:$v; echo err:$v >&2",
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
	require.True(t, started.Truncated)
	require.Equal(t, len(initialOutput), started.TotalBytes)
	require.LessOrEqual(t, len(started.Output), maxBytes)
	require.Contains(t, started.Output, outputTruncatedMarker)
	require.True(t, strings.HasPrefix(started.Output, "HEAD!"))
	require.True(t, strings.HasSuffix(started.Output, "!TAIL"))

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

func TestWriteStdinTool_OutputLimitsApplyToPolls(t *testing.T) {
	const (
		sessionID = "limited-poll"
		maxBytes  = 40
	)
	original := string([]byte{0xff}) +
		"HEAD!" + strings.Repeat("x", 80) + "!TAIL" +
		string([]byte{0xfe})
	exitCode := 0
	execTool := &ExecTool{
		outputLimit: maxBytes,
		sessions: map[string]*execSession{
			sessionID: {
				proc: failingProgramSession{poll: codeexecutor.ProgramPoll{
					Status:     codeexecutor.ProgramStatusExited,
					Output:     original,
					ExitCode:   &exitCode,
					Offset:     12,
					NextOffset: 34,
				}},
			},
		},
		ttl:   time.Minute,
		clock: time.Now,
	}
	writeTool := NewWriteStdinTool(execTool)
	args, err := json.Marshal(writeInput{SessionID: sessionID})
	require.NoError(t, err)

	res, err := writeTool.Call(context.Background(), args)
	require.NoError(t, err)
	out := res.(execOutput)
	require.True(t, out.Truncated)
	require.Equal(t, len(original), out.TotalBytes)
	require.LessOrEqual(t, len(out.Output), maxBytes)
	require.Contains(t, out.Output, outputTruncatedMarker)
	require.True(t, utf8.ValidString(out.Output))
	require.True(t, strings.HasPrefix(out.Output, "HEAD!"))
	require.True(t, strings.HasSuffix(out.Output, "!TAIL"))
	require.Equal(t, codeexecutor.ProgramStatusExited, out.Status)
	require.Equal(t, 12, out.Offset)
	require.Equal(t, 34, out.NextOffset)
}

func TestExecTool_OutputLimitsPreservePollMetadata(t *testing.T) {
	exitCode := 7
	original := execOutput{
		Status:     codeexecutor.ProgramStatusRunning,
		Output:     strings.Repeat("x", 80),
		ExitCode:   &exitCode,
		SessionID:  "session-1",
		Offset:     12,
		NextOffset: 34,
	}
	out := (&ExecTool{outputLimit: 40}).limitOutput(original)
	require.True(t, out.Truncated)
	require.Equal(t, original.Status, out.Status)
	require.Same(t, original.ExitCode, out.ExitCode)
	require.Equal(t, original.SessionID, out.SessionID)
	require.Equal(t, original.Offset, out.Offset)
	require.Equal(t, original.NextOffset, out.NextOffset)
}

func TestExecTool_ParseExecInput_Validation(t *testing.T) {
	_, err := parseExecInput([]byte(`{`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid args")

	_, err = parseExecInput([]byte(`{"command":"   "}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "command is required")
}

func TestExecTool_NormalizeCWD(t *testing.T) {
	cwd, err := normalizeCWD("")
	require.NoError(t, err)
	require.Equal(t, ".", cwd)

	cwd, err = normalizeCWD("/")
	require.NoError(t, err)
	require.Equal(t, ".", cwd)

	cwd, err = normalizeCWD("/out/demo")
	require.NoError(t, err)
	require.Equal(t, "out/demo", cwd)

	cwd, err = normalizeCWD("${OUTPUT_DIR}/demo")
	require.NoError(t, err)
	require.Equal(t, "out/demo", cwd)

	cwd, err = normalizeCWD("skills/demo")
	require.NoError(t, err)
	require.Equal(t, "skills/demo", cwd)

	_, err = normalizeCWD("out/*.zip")
	require.Error(t, err)
	require.Contains(t, err.Error(), "glob patterns")

	_, err = normalizeCWD("../secret")
	require.Error(t, err)
	require.Contains(t, err.Error(), "stay within the workspace")

	_, err = normalizeCWD("tmp/demo")
	require.Error(t, err)
	require.Contains(t, err.Error(), "supported workspace roots")
}

func TestExecTool_HelperFunctions(t *testing.T) {
	require.Equal(t, 5*time.Second, execTimeout(5))
	require.Equal(t, defaultWorkspaceExecTimeout, execTimeout(0))

	require.Equal(t, 0*time.Millisecond, execYield(true, nil))
	require.Equal(t, 120*time.Millisecond, execYield(true, intPtr(120)))
	require.Equal(
		t,
		programsession.YieldDuration(0, programsession.DefaultExecYieldMS),
		execYield(false, nil),
	)
	require.Equal(
		t,
		programsession.YieldDuration(75, programsession.DefaultExecYieldMS),
		execYield(false, intPtr(75)),
	)

	require.Equal(
		t,
		time.Duration(defaultWorkspaceWriteYield)*time.Millisecond,
		writeYield(nil),
	)
	require.Equal(t, 0*time.Millisecond, writeYield(intPtr(0)))
	require.Equal(t, 25*time.Millisecond, writeYield(intPtr(25)))

	require.Equal(t, "stderr", combineOutput("", "stderr"))
	require.Equal(t, "stdout", combineOutput("stdout", ""))
	require.Equal(t, "stdoutstderr", combineOutput("stdout", "stderr"))

	require.Nil(t, firstIntPtr(nil, nil))
	require.Equal(t, 7, *firstIntPtr(nil, intPtr(7)))
	require.Equal(t, 0, firstIntValue(nil, nil))
	require.Equal(t, 8, firstIntValue(nil, intPtr(8)))
	require.False(t, firstBoolValue(nil, nil))
	require.True(t, firstBoolValue(nil, boolPtr(true)))
	require.Equal(t, "", firstNonEmpty("", "   "))
	require.Equal(t, "abc", firstNonEmpty("", " abc "))

	require.True(t, isAllowedWorkspacePath("skills/demo"))
	require.True(t, isAllowedWorkspacePath("work/demo"))
	require.True(t, isAllowedWorkspacePath("out/demo"))
	require.True(t, isAllowedWorkspacePath("runs/demo"))
	require.False(t, isAllowedWorkspacePath("tmp/demo"))
}

func TestExecTool_LiveEngine_Errors(t *testing.T) {
	var nilTool *ExecTool
	_, err := nilTool.liveEngine()
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires an executor")

	_, err = (&ExecTool{exec: &noEngineExec{}}).liveEngine()
	require.Error(t, err)
	require.Contains(t, err.Error(), "EngineProvider")

	_, err = (&ExecTool{exec: &badEngineExec{}}).liveEngine()
	require.Error(t, err)
	require.Contains(t, err.Error(), "live workspace support")
}

func TestExecTool_Call_NotConfigured(t *testing.T) {
	_, err := (&ExecTool{}).Call(context.Background(), []byte(`{"command":"echo hi"}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "workspace_exec is not configured")
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

func TestExecTool_WriteStdin_ValidationErrors(t *testing.T) {
	t.Run("tool not configured", func(t *testing.T) {
		_, err := (&WriteStdinTool{}).Call(context.Background(), []byte(`{}`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "workspace_write_stdin is not configured")
	})

	t.Run("invalid args", func(t *testing.T) {
		_, err := NewWriteStdinTool(NewExecTool(localexec.New())).Call(context.Background(), []byte(`{`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid args")
	})

	t.Run("missing session id", func(t *testing.T) {
		_, err := NewWriteStdinTool(NewExecTool(localexec.New())).Call(context.Background(), []byte(`{}`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "session_id is required")
	})

	t.Run("unknown session", func(t *testing.T) {
		enc, err := json.Marshal(writeInput{SessionID: "missing"})
		require.NoError(t, err)
		_, err = NewWriteStdinTool(NewExecTool(localexec.New())).Call(context.Background(), enc)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unknown session_id")
	})

	t.Run("write failure", func(t *testing.T) {
		execTool := &ExecTool{
			sessions: map[string]*execSession{},
			ttl:      programsession.DefaultSessionTTL,
			clock:    time.Now,
		}
		execTool.putSession("sess-write-fail", &execSession{
			proc: writeFailProgramSession{
				poll: codeexecutor.ProgramPoll{Status: codeexecutor.ProgramStatusRunning},
				err:  errors.New("write failed"),
			},
		})
		writeTool := NewWriteStdinTool(execTool)
		enc, err := json.Marshal(writeInput{SessionID: "sess-write-fail", Chars: "hi"})
		require.NoError(t, err)

		_, err = writeTool.Call(context.Background(), enc)
		require.Error(t, err)
		require.Contains(t, err.Error(), "write failed")
	})
}

func TestExecTool_KillSession_ValidationAndExitedStatus(t *testing.T) {
	t.Run("tool not configured", func(t *testing.T) {
		_, err := (&KillSessionTool{}).Call(context.Background(), []byte(`{}`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "workspace_kill_session is not configured")
	})

	t.Run("invalid args", func(t *testing.T) {
		_, err := NewKillSessionTool(NewExecTool(localexec.New())).Call(context.Background(), []byte(`{`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid args")
	})

	t.Run("missing session id", func(t *testing.T) {
		_, err := NewKillSessionTool(NewExecTool(localexec.New())).Call(context.Background(), []byte(`{}`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "session_id is required")
	})

	t.Run("unknown session", func(t *testing.T) {
		enc, err := json.Marshal(killInput{SessionID: "missing"})
		require.NoError(t, err)
		_, err = NewKillSessionTool(NewExecTool(localexec.New())).Call(context.Background(), enc)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unknown session_id")
	})

	t.Run("already exited", func(t *testing.T) {
		execTool := &ExecTool{
			sessions: map[string]*execSession{},
			ttl:      programsession.DefaultSessionTTL,
			clock:    time.Now,
		}
		execTool.putSession("sess-exited", &execSession{
			proc: failingProgramSession{
				poll: codeexecutor.ProgramPoll{
					Status:   codeexecutor.ProgramStatusExited,
					ExitCode: intPtr(0),
				},
			},
		})
		killTool := NewKillSessionTool(execTool)
		enc, err := json.Marshal(killInput{SessionID: "sess-exited"})
		require.NoError(t, err)

		res, err := killTool.Call(context.Background(), enc)
		require.NoError(t, err)
		out := res.(killOutput)
		require.True(t, out.OK)
		require.Equal(t, "exited", out.Status)
		_, err = execTool.getSession("sess-exited")
		require.Error(t, err)
	})
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

type writeFailProgramSession struct {
	poll codeexecutor.ProgramPoll
	err  error
}

func (p writeFailProgramSession) ID() string                           { return "write-fail" }
func (p writeFailProgramSession) Poll(_ *int) codeexecutor.ProgramPoll { return p.poll }
func (p writeFailProgramSession) State() codeexecutor.ProgramState {
	return codeexecutor.ProgramState{Status: p.poll.Status}
}
func (p writeFailProgramSession) Log(_, _ *int) codeexecutor.ProgramLog {
	return codeexecutor.ProgramLog{}
}
func (p writeFailProgramSession) Write(string, bool) error { return p.err }
func (p writeFailProgramSession) Kill(time.Duration) error { return nil }
func (p writeFailProgramSession) Close() error             { return nil }

type staleWriteRunner struct{}

func (staleWriteRunner) RunProgram(
	context.Context,
	codeexecutor.Workspace,
	codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	return codeexecutor.RunResult{Stdout: "ok"}, nil
}

func (staleWriteRunner) StartProgram(
	context.Context,
	codeexecutor.Workspace,
	codeexecutor.InteractiveProgramSpec,
) (codeexecutor.ProgramSession, error) {
	return writeFailProgramSession{
		poll: codeexecutor.ProgramPoll{
			Status: codeexecutor.ProgramStatusRunning,
		},
		err: fmt.Errorf(
			"old session backend is gone: %w",
			codeexecutor.ErrWorkspaceStale,
		),
	}, nil
}

type staleRetryManager struct {
	mu         sync.Mutex
	instance   int
	createRuns int
}

func (m *staleRetryManager) CreateWorkspace(
	_ context.Context,
	id string,
	_ codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createRuns++
	return codeexecutor.Workspace{
		ID:   id,
		Path: "/tmp/" + id,
	}, nil
}

func (*staleRetryManager) Cleanup(
	context.Context,
	codeexecutor.Workspace,
) error {
	return nil
}

func (m *staleRetryManager) InstanceID(
	context.Context,
) (codeexecutor.WorkspaceInstanceID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return codeexecutor.WorkspaceInstanceID(
		fmt.Sprintf("instance-%d", m.instance),
	), nil
}

func (m *staleRetryManager) rotate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instance++
}

func (m *staleRetryManager) createCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createRuns
}

type legacyABAManager struct {
	mu      sync.Mutex
	creates int
}

func (m *legacyABAManager) CreateWorkspace(
	_ context.Context,
	id string,
	_ codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creates++
	return codeexecutor.Workspace{
		ID:   id,
		Path: "/tmp/" + id,
	}, nil
}

func (*legacyABAManager) Cleanup(
	context.Context,
	codeexecutor.Workspace,
) error {
	return nil
}

func (m *legacyABAManager) createCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.creates
}

type legacyABARunner struct {
	mu sync.Mutex

	calls       int
	starts      int
	lateEntered chan struct{}
	releaseLate chan struct{}
}

func (r *legacyABARunner) RunProgram(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()

	switch call {
	case 1:
		close(r.lateEntered)
		<-r.releaseLate
		return codeexecutor.RunResult{}, fmt.Errorf(
			"late stale result: %w",
			codeexecutor.ErrWorkspaceStale,
		)
	case 2:
		return codeexecutor.RunResult{}, fmt.Errorf(
			"refreshing stale result: %w",
			codeexecutor.ErrWorkspaceStale,
		)
	default:
		r.mu.Lock()
		r.starts++
		r.mu.Unlock()
		return codeexecutor.RunResult{Stdout: "ok", ExitCode: 0}, nil
	}
}

func (r *legacyABARunner) counts() (calls int, starts int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls, r.starts
}

type staleRetryRunner struct {
	mu sync.Mutex

	manager         *staleRetryManager
	userErrors      []error
	bootstrapErrors []error
	metadataErrors  []error
	userAttempts    int
	userStarts      int
	bootstrapStarts int
}

func (r *staleRetryRunner) RunProgram(
	_ context.Context,
	_ codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	if spec.Cmd != "sh" {
		r.mu.Lock()
		if spec.Cmd == "bash" && len(r.metadataErrors) > 0 {
			err := r.metadataErrors[0]
			r.metadataErrors = r.metadataErrors[1:]
			r.mu.Unlock()
			if errors.Is(err, codeexecutor.ErrWorkspaceStale) {
				r.manager.rotate()
			}
			return codeexecutor.RunResult{}, err
		}
		var err error
		if spec.Cmd != "bash" {
			r.bootstrapStarts++
			if len(r.bootstrapErrors) > 0 {
				err = r.bootstrapErrors[0]
				r.bootstrapErrors = r.bootstrapErrors[1:]
			}
		}
		r.mu.Unlock()
		if errors.Is(err, codeexecutor.ErrWorkspaceStale) {
			r.manager.rotate()
		}
		if err != nil {
			return codeexecutor.RunResult{}, err
		}
		return codeexecutor.RunResult{ExitCode: 0}, nil
	}
	r.mu.Lock()
	r.userAttempts++
	var err error
	if len(r.userErrors) > 0 {
		err = r.userErrors[0]
		r.userErrors = r.userErrors[1:]
	}
	if !errors.Is(err, codeexecutor.ErrWorkspaceStale) {
		r.userStarts++
	}
	r.mu.Unlock()
	if errors.Is(err, codeexecutor.ErrWorkspaceStale) {
		r.manager.rotate()
	}
	if err != nil {
		return codeexecutor.RunResult{}, err
	}
	return codeexecutor.RunResult{
		Stdout:   "ok",
		ExitCode: 0,
	}, nil
}

func (r *staleRetryRunner) counts() (attempts int, starts int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.userAttempts, r.userStarts
}

func (r *staleRetryRunner) bootstrapCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bootstrapStarts
}

type staleRetryFS struct {
	nonInteractiveFS

	mu        sync.Mutex
	manager   *staleRetryManager
	staleNext bool
}

func (f *staleRetryFS) PutFiles(
	context.Context,
	codeexecutor.Workspace,
	[]codeexecutor.PutFile,
) error {
	f.mu.Lock()
	stale := f.staleNext
	f.staleNext = false
	f.mu.Unlock()
	if stale {
		f.manager.rotate()
		return fmt.Errorf(
			"workspace changed before file write: %w",
			codeexecutor.ErrWorkspaceStale,
		)
	}
	return nil
}

type staleRetryExec struct {
	eng codeexecutor.Engine
}

func (*staleRetryExec) ExecuteCode(
	context.Context,
	codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (*staleRetryExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func (e *staleRetryExec) Engine() codeexecutor.Engine {
	return e.eng
}

func newStaleRetryExec(
	manager *staleRetryManager,
	fs codeexecutor.WorkspaceFS,
	runner *staleRetryRunner,
) *staleRetryExec {
	return &staleRetryExec{
		eng: codeexecutor.NewEngine(manager, fs, runner),
	}
}

type staleInteractiveRetryRunner struct {
	*staleRetryRunner

	mu            sync.Mutex
	startAttempts int
	starts        int
}

func (r *staleInteractiveRetryRunner) StartProgram(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ codeexecutor.InteractiveProgramSpec,
) (codeexecutor.ProgramSession, error) {
	r.mu.Lock()
	r.startAttempts++
	attempt := r.startAttempts
	if attempt > 1 {
		r.starts++
	}
	r.mu.Unlock()
	if attempt == 1 {
		r.manager.rotate()
		return nil, fmt.Errorf(
			"before process start: %w",
			codeexecutor.ErrWorkspaceStale,
		)
	}
	exitCode := 0
	return failingProgramSession{
		poll: codeexecutor.ProgramPoll{
			Status:   codeexecutor.ProgramStatusExited,
			ExitCode: &exitCode,
		},
	}, nil
}

func (r *staleInteractiveRetryRunner) startCounts() (
	attempts int,
	starts int,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.startAttempts, r.starts
}

func TestExecTool_WorkspaceStaleBeforeRunRetriesOnce(t *testing.T) {
	manager := &staleRetryManager{instance: 1}
	runner := &staleRetryRunner{
		manager: manager,
		userErrors: []error{
			fmt.Errorf("before submit: %w", codeexecutor.ErrWorkspaceStale),
		},
	}
	exec := newStaleRetryExec(
		manager,
		&nonInteractiveFS{},
		runner,
	)
	tl := NewExecTool(exec)
	args, err := json.Marshal(execInput{Command: "printf ok"})
	require.NoError(t, err)

	got, err := tl.Call(context.Background(), args)
	require.NoError(t, err)
	require.Equal(t, "ok", got.(execOutput).Output)
	require.Equal(t, 2, manager.createCount())
	attempts, starts := runner.counts()
	require.Equal(t, 2, attempts)
	require.Equal(t, 1, starts, "the user command must start only once")
}

func TestExecTool_UnsafeStaleInvalidatesWithoutReplay(t *testing.T) {
	manager := &staleRetryManager{instance: 1}
	runner := &staleRetryRunner{
		manager: manager,
		userErrors: []error{errors.Join(
			codeexecutor.ErrWorkspaceStale,
			codeexecutor.ErrWorkspaceRetryUnsafe,
		)},
	}
	exec := newStaleRetryExec(
		manager,
		&nonInteractiveFS{},
		runner,
	)
	tl := NewExecTool(exec)
	args, err := json.Marshal(execInput{Command: "side-effect"})
	require.NoError(t, err)

	_, err = tl.Call(context.Background(), args)
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceRetryUnsafe)
	require.Equal(t, 1, manager.createCount())
	attempts, starts := runner.counts()
	require.Equal(t, 1, attempts, "unsafe stale must not replay")
	require.Zero(t, starts)

	got, err := tl.Call(context.Background(), args)
	require.NoError(t, err)
	require.Equal(t, "ok", got.(execOutput).Output)
	require.Equal(t, 2, manager.createCount(),
		"the next call must rebuild the invalidated workspace once")
	attempts, starts = runner.counts()
	require.Equal(t, 2, attempts)
	require.Equal(t, 1, starts)
}

func TestExecTool_WorkspaceStaleBeforeInteractiveStartRetriesOnce(
	t *testing.T,
) {
	manager := &staleRetryManager{instance: 1}
	runner := &staleInteractiveRetryRunner{
		staleRetryRunner: &staleRetryRunner{manager: manager},
	}
	exec := &staleRetryExec{
		eng: codeexecutor.NewEngine(
			manager,
			&nonInteractiveFS{},
			runner,
		),
	}
	tl := NewExecTool(exec)
	args, err := json.Marshal(execInput{
		Command:    "interactive",
		Background: true,
	})
	require.NoError(t, err)

	got, err := tl.Call(context.Background(), args)
	require.NoError(t, err)
	require.Equal(
		t,
		codeexecutor.ProgramStatusExited,
		got.(execOutput).Status,
	)
	require.Equal(t, 2, manager.createCount())
	attempts, starts := runner.startCounts()
	require.Equal(t, 2, attempts)
	require.Equal(t, 1, starts)
}

func TestExecTool_StaleSessionWriteInvalidatesLegacyWorkspace(
	t *testing.T,
) {
	manager := &legacyABAManager{}
	exec := &staleRetryExec{
		eng: codeexecutor.NewEngine(
			manager,
			&nonInteractiveFS{},
			staleWriteRunner{},
		),
	}
	execTool := NewExecTool(exec)
	startArgs, err := json.Marshal(execInput{
		Command:    "interactive",
		Background: true,
	})
	require.NoError(t, err)

	started, err := execTool.Call(context.Background(), startArgs)
	require.NoError(t, err)
	require.Equal(t, "write-fail", started.(execOutput).SessionID)
	require.Equal(t, 1, manager.createCount())

	writeArgs, err := json.Marshal(writeInput{
		SessionID: "write-fail",
		Chars:     "must-not-replay",
	})
	require.NoError(t, err)
	_, err = NewWriteStdinTool(execTool).Call(
		context.Background(),
		writeArgs,
	)
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)

	nextArgs, err := json.Marshal(execInput{Command: "printf ok"})
	require.NoError(t, err)
	got, err := execTool.Call(context.Background(), nextArgs)
	require.NoError(t, err)
	require.Equal(t, "ok", got.(execOutput).Output)
	require.Equal(t, 2, manager.createCount(),
		"the next call must rebuild exactly once")
}

func TestExecTool_WorkspaceStaleTwiceStops(t *testing.T) {
	manager := &staleRetryManager{instance: 1}
	runner := &staleRetryRunner{
		manager: manager,
		userErrors: []error{
			codeexecutor.ErrWorkspaceStale,
			fmt.Errorf("still stale: %w", codeexecutor.ErrWorkspaceStale),
		},
	}
	exec := newStaleRetryExec(
		manager,
		&nonInteractiveFS{},
		runner,
	)
	tl := NewExecTool(exec)
	args, err := json.Marshal(execInput{Command: "printf ok"})
	require.NoError(t, err)

	_, err = tl.Call(context.Background(), args)
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
	require.Equal(t, 2, manager.createCount())
	attempts, starts := runner.counts()
	require.Equal(t, 2, attempts)
	require.Zero(t, starts)
}

func TestExecTool_NonStaleRunErrorDoesNotRetry(t *testing.T) {
	manager := &staleRetryManager{instance: 1}
	uncertain := errors.New("transport timeout after request submission")
	runner := &staleRetryRunner{
		manager:    manager,
		userErrors: []error{uncertain},
	}
	exec := newStaleRetryExec(
		manager,
		&nonInteractiveFS{},
		runner,
	)
	tl := NewExecTool(exec)
	args, err := json.Marshal(execInput{Command: "side-effect"})
	require.NoError(t, err)

	_, err = tl.Call(context.Background(), args)
	require.ErrorIs(t, err, uncertain)
	require.Equal(t, 1, manager.createCount())
	attempts, starts := runner.counts()
	require.Equal(t, 1, attempts)
	require.Equal(t, 1, starts)
}

func TestExecTool_WorkspaceStaleDuringReconcileRetriesBeforeCommand(
	t *testing.T,
) {
	manager := &staleRetryManager{instance: 1}
	runner := &staleRetryRunner{manager: manager}
	fs := &staleRetryFS{
		manager:   manager,
		staleNext: true,
	}
	exec := newStaleRetryExec(manager, fs, runner)
	tl := NewExecTool(
		exec,
		WithWorkspaceBootstrap(codeexecutor.WorkspaceBootstrapSpec{
			Files: []codeexecutor.WorkspaceFile{{
				Target:  "work/seed.txt",
				Content: []byte("seed"),
			}},
		}),
	)
	args, err := json.Marshal(execInput{Command: "printf ok"})
	require.NoError(t, err)

	got, err := tl.Call(context.Background(), args)
	require.NoError(t, err)
	require.Equal(t, "ok", got.(execOutput).Output)
	require.Equal(t, 2, manager.createCount())
	attempts, starts := runner.counts()
	require.Equal(t, 1, attempts)
	require.Equal(t, 1, starts)
}

func TestExecTool_UnsafeReconcileStaleInvalidatesWithoutReplay(
	t *testing.T,
) {
	manager := &staleRetryManager{instance: 1}
	runner := &staleRetryRunner{
		manager: manager,
		metadataErrors: []error{
			fmt.Errorf(
				"metadata commit after command: %w",
				codeexecutor.ErrWorkspaceStale,
			),
		},
	}
	exec := newStaleRetryExec(manager, &nonInteractiveFS{}, runner)
	tl := NewExecTool(
		exec,
		WithWorkspaceBootstrap(codeexecutor.WorkspaceBootstrapSpec{
			Commands: []codeexecutor.WorkspaceCommand{{
				Cmd: "setup-with-side-effect",
			}},
		}),
	)
	args, err := json.Marshal(execInput{Command: "must-not-start"})
	require.NoError(t, err)

	_, err = tl.Call(context.Background(), args)
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
	require.ErrorIs(t, err, workspaceprep.ErrReconcileRetryUnsafe)
	require.Equal(t, 1, manager.createCount(),
		"unsafe reconciliation must not reacquire and replay")
	require.Equal(t, 1, runner.bootstrapCount())
	attempts, starts := runner.counts()
	require.Zero(t, attempts)
	require.Zero(t, starts)
}

func TestExecTool_FailedOptionalCommandPreventsLaterStaleReplay(
	t *testing.T,
) {
	manager := &staleRetryManager{instance: 1}
	runner := &staleRetryRunner{
		manager: manager,
		bootstrapErrors: []error{
			errors.New("timeout after optional command submission"),
			fmt.Errorf(
				"later command did not start: %w",
				codeexecutor.ErrWorkspaceStale,
			),
		},
	}
	exec := newStaleRetryExec(manager, &nonInteractiveFS{}, runner)
	tl := NewExecTool(
		exec,
		WithWorkspaceBootstrap(codeexecutor.WorkspaceBootstrapSpec{
			Commands: []codeexecutor.WorkspaceCommand{
				{
					Cmd:      "optional-side-effect",
					Optional: true,
				},
				{Cmd: "later-stale"},
			},
		}),
	)
	args, err := json.Marshal(execInput{Command: "must-not-start"})
	require.NoError(t, err)

	_, err = tl.Call(context.Background(), args)
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
	require.ErrorIs(t, err, workspaceprep.ErrReconcileRetryUnsafe)
	require.Equal(t, 1, manager.createCount(),
		"an uncertain optional command must prevent reconciliation replay")
	require.Equal(t, 2, runner.bootstrapCount())
	attempts, starts := runner.counts()
	require.Zero(t, attempts)
	require.Zero(t, starts)
}

func TestExecTool_LegacyLateStaleDoesNotEvictRefreshedHandle(
	t *testing.T,
) {
	manager := &legacyABAManager{}
	runner := &legacyABARunner{
		lateEntered: make(chan struct{}),
		releaseLate: make(chan struct{}),
	}
	exec := &staleRetryExec{
		eng: codeexecutor.NewEngine(
			manager,
			&nonInteractiveFS{},
			runner,
		),
	}
	tl := NewExecTool(exec)
	args, err := json.Marshal(execInput{Command: "printf ok"})
	require.NoError(t, err)

	type callResult struct {
		value any
		err   error
	}
	lateResult := make(chan callResult, 1)
	go func() {
		value, err := tl.Call(context.Background(), args)
		lateResult <- callResult{value: value, err: err}
	}()
	select {
	case <-runner.lateEntered:
	case <-time.After(time.Second):
		t.Fatal("late attempt did not reach the runner")
	}

	refreshed, err := tl.Call(context.Background(), args)
	require.NoError(t, err)
	require.Equal(t, "ok", refreshed.(execOutput).Output)

	close(runner.releaseLate)
	var late callResult
	select {
	case late = <-lateResult:
	case <-time.After(time.Second):
		t.Fatal("late attempt did not finish")
	}
	require.NoError(t, late.err)
	require.Equal(t, "ok", late.value.(execOutput).Output)
	require.Equal(t, 2, manager.createCount(),
		"late invalidation must not evict the replacement entry")
	calls, starts := runner.counts()
	require.Equal(t, 4, calls)
	require.Equal(t, 2, starts)
}

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

type noEngineExec struct{}

func (e *noEngineExec) ExecuteCode(
	context.Context,
	codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (e *noEngineExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

type badEngineExec struct{}

func (e *badEngineExec) ExecuteCode(
	context.Context,
	codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (e *badEngineExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func (e *badEngineExec) Engine() codeexecutor.Engine {
	return codeexecutor.NewEngine(nil, nil, nil)
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

func TestExecTool_InstanceRotationReRunsBootstrapCommand(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	rt := localexec.NewRuntimeWithOptions(
		root,
		localexec.WithRuntimeWorkspaceMode(
			localexec.WorkspaceModeTrustedLocal,
		),
		localexec.WithAutoInputs(false),
	)
	manager := &instanceAwareTrustedManager{
		Runtime:    rt,
		instanceID: "instance-1",
	}
	reg := codeexecutor.NewWorkspaceRegistry()
	runner := &bootstrapCountingRunner{inner: rt}
	exec := &instanceAwareExec{
		eng: codeexecutor.NewEngine(manager, rt, runner),
	}
	tl := NewExecTool(
		exec,
		WithWorkspaceRegistry(reg),
		WithWorkspaceBootstrap(codeexecutor.WorkspaceBootstrapSpec{
			Commands: []codeexecutor.WorkspaceCommand{{
				Cmd: "true",
			}},
		}),
	)
	args, err := json.Marshal(execInput{Command: "printf ok"})
	require.NoError(t, err)

	got, err := tl.Call(context.Background(), args)
	require.NoError(t, err)
	require.Equal(t, "ok", got.(execOutput).Output)
	require.Equal(t, 1, runner.bootstrapCount(),
		"bootstrap must run once on first workspace use")
	require.Equal(t, 1, manager.createCount())

	bootstrapBeforeRotate := runner.bootstrapCount()
	manager.setInstanceID("instance-2")

	got, err = tl.Call(context.Background(), args)
	require.NoError(t, err)
	require.Equal(t, "ok", got.(execOutput).Output)
	require.Equal(t, 2, manager.createCount(),
		"instance change must recreate the workspace")
	require.Equal(t, bootstrapBeforeRotate+1, runner.bootstrapCount(),
		"bootstrap must rerun after instance rotation")
}

type instanceAwareTrustedManager struct {
	*localexec.Runtime

	mu         sync.Mutex
	instanceID codeexecutor.WorkspaceInstanceID
	creates    int
}

func (m *instanceAwareTrustedManager) CreateWorkspace(
	ctx context.Context,
	id string,
	pol codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	m.mu.Lock()
	m.creates++
	m.mu.Unlock()
	return m.Runtime.CreateWorkspace(ctx, id, pol)
}

func (m *instanceAwareTrustedManager) InstanceID(
	context.Context,
) (codeexecutor.WorkspaceInstanceID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.instanceID, nil
}

func (m *instanceAwareTrustedManager) setInstanceID(
	id codeexecutor.WorkspaceInstanceID,
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instanceID = id
}

func (m *instanceAwareTrustedManager) createCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.creates
}

type instanceAwareExec struct {
	eng codeexecutor.Engine
}

func (*instanceAwareExec) ExecuteCode(
	context.Context,
	codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (*instanceAwareExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func (e *instanceAwareExec) Engine() codeexecutor.Engine {
	return e.eng
}

type bootstrapCountingRunner struct {
	inner codeexecutor.ProgramRunner

	mu     sync.Mutex
	starts int
}

func (r *bootstrapCountingRunner) RunProgram(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	if spec.Cmd == "true" && len(spec.Args) == 0 {
		r.mu.Lock()
		r.starts++
		r.mu.Unlock()
	}
	if r.inner == nil {
		return codeexecutor.RunResult{ExitCode: 0}, nil
	}
	return r.inner.RunProgram(ctx, ws, spec)
}

func (r *bootstrapCountingRunner) bootstrapCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts
}

func boolPtr(v bool) *bool { return &v }

func intPtr(v int) *int { return &v }
