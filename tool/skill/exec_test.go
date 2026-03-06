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
	"testing"

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
