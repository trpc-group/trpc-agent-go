//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
)

func TestOpenClawToolResultMessages_AttachesMediaFiles(t *testing.T) {
	t.Parallel()

	path := writeTestImage(
		t,
		"00ed39bb50144ce9ebef3aab-frame.png",
	)
	defaultMsg := model.Message{
		Role:     model.RoleTool,
		ToolID:   "tool-1",
		ToolName: "exec_command",
		Content:  "{}",
	}

	got, err := openClawToolResultMessages(
		context.Background(),
		&tool.ToolResultMessagesInput{
			ToolName:           "exec_command",
			DefaultToolMessage: defaultMsg,
			Result: map[string]any{
				"media_files": []string{path},
			},
		},
	)
	require.NoError(t, err)

	msgs, ok := got.([]model.Message)
	require.True(t, ok)
	require.Len(t, msgs, 2)
	require.Equal(t, defaultMsg, msgs[0])
	require.Contains(t, msgs[1].Content, "frame.png")
	require.Len(t, msgs[1].ContentParts, 1)
	require.NotNil(t, msgs[1].ContentParts[0].Image)
	require.Equal(
		t,
		[]byte("fake-image"),
		msgs[1].ContentParts[0].Image.Data,
	)
}

func TestOpenClawToolResultMessages_AttachesOutputImagePaths(t *testing.T) {
	t.Parallel()

	path := writeTestImage(t, "video-note_last-frame.png")
	defaultMsg := model.Message{
		Role:    model.RoleTool,
		ToolID:  "tool-1",
		Content: "{}",
	}

	got, err := openClawToolResultMessages(
		context.Background(),
		&tool.ToolResultMessagesInput{
			DefaultToolMessage: defaultMsg,
			Result: map[string]any{
				"output": path + "\n",
			},
		},
	)
	require.NoError(t, err)

	msgs, ok := got.([]model.Message)
	require.True(t, ok)
	require.Len(t, msgs, 2)
	require.Contains(
		t,
		msgs[1].Content,
		"video-note_last-frame.png",
	)
	require.Len(t, msgs[1].ContentParts, 1)
}

func TestOpenClawToolResultMessages_RecordsTraceEvent(t *testing.T) {
	t.Parallel()

	path := writeTestImage(t, "frame.png")
	mode, err := debugrecorder.ParseMode("full")
	require.NoError(t, err)
	rec, err := debugrecorder.New(t.TempDir(), mode)
	require.NoError(t, err)

	trace, err := rec.Start(debugrecorder.TraceStart{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "s1",
		MessageID: "m1",
		RequestID: "r1",
		Source:    "test",
	})
	require.NoError(t, err)

	ctx := debugrecorder.WithTrace(context.Background(), trace)
	_, err = openClawToolResultMessages(
		ctx,
		&tool.ToolResultMessagesInput{
			DefaultToolMessage: model.Message{
				Role: model.RoleTool,
			},
			Result: map[string]any{
				"media_files": []string{path},
			},
		},
	)
	require.NoError(t, err)
	require.NoError(t, trace.Close(
		debugrecorder.TraceEnd{Status: "ok"},
	))

	data, err := os.ReadFile(
		filepath.Join(trace.Dir(), "events.jsonl"),
	)
	require.NoError(t, err)
	require.Contains(t, string(data), toolResultImagesTraceKind)
	require.Contains(t, string(data), "frame.png")
}

func writeTestImage(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(
		t,
		os.WriteFile(path, []byte("fake-image"), 0o600),
	)
	return path
}
