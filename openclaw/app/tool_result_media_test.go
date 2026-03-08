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
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
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

func TestToolResultImageMessages_Guards(t *testing.T) {
	t.Parallel()

	out, err := toolResultImageMessages(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, out)

	out, err = openClawToolResultMessages(
		context.Background(),
		&tool.ToolResultMessagesInput{
			DefaultToolMessage: "not-a-model-message",
			Result: map[string]any{
				"media_files": []string{"/tmp/missing.png"},
			},
		},
	)
	require.NoError(t, err)
	require.Nil(t, out)
}

func TestCollectToolResultImagePaths_WalksDirsAndDeduplicates(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := filepath.Join(root, "frames")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	pathA := writeTestFile(t, nested, "frame.png", []byte("png"))
	pathB := writeTestFile(t, root, "cover.jpg", []byte("jpg"))
	writeTestFile(t, root, "notes.txt", []byte("note"))

	got := collectToolResultImagePaths(map[string]any{
		"media_files": []string{
			uploads.HostRef(pathA),
			pathA,
		},
		"media_dirs": []string{root},
		"output": "MEDIA: " + pathB + "\n" +
			"MEDIA_DIR: " + uploads.HostRef(root),
	})

	require.Equal(t, []string{pathA, pathB}, got)
}

func TestLoadToolResultImages_FiltersBySizeAndFormat(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestFile(t, root, "frame.webp", []byte("webp"))
	writeTestFile(t, root, "notes.txt", []byte("note"))

	largePath := filepath.Join(root, "large.png")
	require.NoError(
		t,
		os.WriteFile(
			largePath,
			make([]byte, maxToolResultImageBytes+1),
			0o600,
		),
	)

	got := loadToolResultImages(map[string]any{
		"media_dirs": []string{root},
	})

	require.Len(t, got, 1)
	require.Equal(t, "frame.webp", got[0].Name)
	require.Equal(t, "webp", got[0].Format)
	require.Equal(t, []byte("webp"), got[0].Data)
}

func TestLoadToolResultImages_SkipsMissingAndTrimmedPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	goodPath := writeTestFile(t, root, "good.png", []byte("png"))
	nestedDir := filepath.Join(root, "nested")
	require.NoError(t, os.MkdirAll(nestedDir, 0o755))
	nestedPath := writeTestFile(t, nestedDir, "nested.jpg", []byte("jpg"))
	writeTestFile(t, root, "notes.txt", []byte("note"))

	got := loadToolResultImages(map[string]any{
		"media_files": []string{
			" `" + goodPath + "` ",
			filepath.Join(root, "missing.png"),
		},
		"media_dirs": []string{root},
	})

	require.Len(t, got, 2)
	require.Equal(t, "good.png", got[0].Name)
	require.Equal(t, []byte("png"), got[0].Data)
	require.Equal(t, "nested.jpg", got[1].Name)
	require.Equal(t, []byte("jpg"), got[1].Data)

	paths := appendToolResultImagePath(
		nil,
		map[string]struct{}{},
		"relative.png",
	)
	require.Nil(t, paths)

	seen := make(map[string]struct{})
	paths = appendToolResultImagePath(nil, seen, goodPath)
	paths = appendToolResultImagePath(paths, seen, goodPath)
	require.Equal(t, []string{goodPath}, paths)

	gotPath, ok := resolveToolResultPath(" `" + nestedPath + "` ")
	require.True(t, ok)
	require.Equal(t, nestedPath, gotPath)
}

func TestCollectToolResultImagePaths_StopsAtLimit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for i := 0; i < maxToolResultImages+2; i++ {
		name := filepath.Join("frames", "image-"+string(rune('a'+i))+".png")
		writeTestFile(t, root, name, []byte("png"))
	}

	got := collectToolResultImagePaths(map[string]any{
		"media_dirs": []string{root},
	})
	require.Len(t, got, maxToolResultImages)
}

func TestParseToolResultMediaPayload_RejectsUnsupportedInput(t *testing.T) {
	t.Parallel()

	_, ok := parseToolResultMediaPayload(nil)
	require.False(t, ok)

	_, ok = parseToolResultMediaPayload(map[string]any{
		"bad": func() {},
	})
	require.False(t, ok)
}

func TestToolResultPathHelpers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := writeTestFile(t, root, "clip.gif", []byte("gif"))
	hostRef := uploads.HostRef(path)

	got, ok := toolResultPathFromLine("MEDIA: `" + hostRef + "`")
	require.True(t, ok)
	require.Equal(t, path, got)

	got, ok = toolResultPathFromLine(path)
	require.True(t, ok)
	require.Equal(t, path, got)

	_, ok = toolResultPathFromLine("MEDIA: relative.png")
	require.False(t, ok)

	require.Equal(
		t,
		[]string{path},
		toolResultOutputPaths("noise\nMEDIA: "+hostRef+"\n"),
	)

	got, ok = resolveToolResultPath(hostRef)
	require.True(t, ok)
	require.Equal(t, path, got)

	_, ok = resolveToolResultPath("relative.png")
	require.False(t, ok)

	require.Equal(t, "gif", mustToolResultImageFormat(t, path))
	require.Equal(
		t,
		"Generated image attached for direct inspection.",
		toolResultImageMessageText([]toolResultImage{{}}),
	)
	require.Equal(
		t,
		"Generated images attached for direct inspection: "+
			"one.png, two.jpg.",
		toolResultImageMessageText([]toolResultImage{
			{Name: "one.png"},
			{Name: "two.jpg"},
		}),
	)
}

func writeTestImage(t *testing.T, name string) string {
	t.Helper()

	return writeTestFile(t, t.TempDir(), name, []byte("fake-image"))
}

func writeTestFile(
	t *testing.T,
	dir string,
	name string,
	data []byte,
) string {
	t.Helper()

	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func mustToolResultImageFormat(t *testing.T, path string) string {
	t.Helper()

	format, ok := toolResultImageFormat(path)
	require.True(t, ok)
	return format
}
