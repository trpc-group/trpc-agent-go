//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package telegram

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	artifactinmemory "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcache"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestResolveTextTargetFromSessionID(t *testing.T) {
	target, ok := ResolveTextTargetFromSessionID("telegram:dm:123")
	require.True(t, ok)
	require.Equal(t, "123", target)

	target, ok = ResolveTextTargetFromSessionID(
		"telegram:thread:100:topic:7",
	)
	require.True(t, ok)
	require.Equal(t, "100:topic:7", target)

	target, ok = ResolveTextTargetFromSessionID(
		"telegram:dm:123:session-abc",
	)
	require.True(t, ok)
	require.Equal(t, "123", target)
}

func TestParseTextTarget(t *testing.T) {
	chatID, threadID, err := parseTextTarget("telegram:thread:100:topic:7")
	require.NoError(t, err)
	require.EqualValues(t, 100, chatID)
	require.Equal(t, 7, threadID)
}

func TestChannel_SendText_SplitsLongMessages(t *testing.T) {
	bot := &stubBot{}
	ch := &Channel{bot: bot}

	text := strings.Repeat("a", maxReplyRunes+5)
	err := ch.SendText(context.Background(), "100", text)
	require.NoError(t, err)

	require.Len(t, bot.sent, 2)
	require.EqualValues(t, 100, bot.sent[0].ChatID)
	require.Len(t, bot.sent[0].Text, maxReplyRunes)
	require.Len(t, bot.sent[1].Text, 5)
}

func TestChannel_SendMessage_SendsDocumentAndPhoto(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	docPath := filepath.Join(root, "report.pdf")
	photoPath := filepath.Join(root, "frame.png")
	require.NoError(t, os.WriteFile(docPath, []byte("%PDF-1.4"), 0o600))
	require.NoError(
		t,
		os.WriteFile(
			photoPath,
			[]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'},
			0o600,
		),
	)

	bot := &stubBot{}
	ch := &Channel{bot: bot}

	err := ch.SendMessage(
		context.Background(),
		"telegram:dm:100:session-abc",
		channel.OutboundMessage{
			Text: "done",
			Files: []channel.OutboundFile{
				{Path: docPath},
				{Path: photoPath},
			},
		},
	)
	require.NoError(t, err)

	require.Len(t, bot.sent, 1)
	require.Equal(t, "done", bot.sent[0].Text)
	require.Len(t, bot.docs, 1)
	require.Equal(t, "report.pdf", bot.docs[0].FileName)
	require.Len(t, bot.photos, 1)
	require.Equal(t, "frame.png", bot.photos[0].FileName)
}

func TestChannel_SendMessage_SendsAudioVoiceAndVideo(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	audioPath := filepath.Join(root, "note.mp3")
	voicePath := filepath.Join(root, "voice.oga")
	videoPath := filepath.Join(root, "clip.mp4")
	require.NoError(t, os.WriteFile(audioPath, []byte("mp3"), 0o600))
	require.NoError(t, os.WriteFile(voicePath, []byte("ogg"), 0o600))
	require.NoError(t, os.WriteFile(videoPath, []byte("mp4"), 0o600))

	bot := &stubBot{}
	ch := &Channel{bot: bot}

	err := ch.SendMessage(
		context.Background(),
		"100",
		channel.OutboundMessage{
			Files: []channel.OutboundFile{
				{Path: audioPath},
				{Path: voicePath},
				{Path: videoPath},
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, bot.audios, 1)
	require.Equal(t, "note.mp3", bot.audios[0].FileName)
	require.Len(t, bot.voices, 1)
	require.Equal(t, "voice.oga", bot.voices[0].FileName)
	require.Len(t, bot.videos, 1)
	require.Equal(t, "clip.mp4", bot.videos[0].FileName)
}

func TestChannel_SendMessage_SendsArtifactAndWorkspaceRefs(t *testing.T) {
	t.Parallel()

	artifactSvc := artifactinmemory.NewService()
	info := artifact.SessionInfo{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	_, err := artifactSvc.SaveArtifact(
		context.Background(),
		info,
		"reports/result.pdf",
		&artifact.Artifact{
			Data: []byte("%PDF-1.4"),
		},
	)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(
			session.NewSession("app", "user", "telegram:dm:100"),
		),
	)
	var ctx context.Context
	ctx = agent.NewInvocationContext(context.Background(), inv)
	ctx = codeexecutor.WithArtifactService(ctx, artifactSvc)
	ctx = codeexecutor.WithArtifactSession(ctx, info)
	toolcache.StoreSkillRunOutputFilesFromContext(
		ctx,
		[]codeexecutor.File{
			{
				Name:     "notes/summary.txt",
				Content:  "hello",
				MIMEType: "text/plain",
			},
		},
	)

	bot := &stubBot{}
	ch := &Channel{bot: bot}

	err = ch.SendMessage(
		ctx,
		"100",
		channel.OutboundMessage{
			Files: []channel.OutboundFile{
				{Path: "artifact://reports/result.pdf@0"},
				{Path: "workspace://notes/summary.txt"},
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, bot.docs, 2)
	require.Equal(t, "result.pdf", bot.docs[0].FileName)
	require.Equal(t, "%PDF-1.4", string(bot.docs[0].Data))
	require.Equal(t, "summary.txt", bot.docs[1].FileName)
	require.Equal(t, "hello", string(bot.docs[1].Data))
}

func TestResolveOutboundFilePath_SupportsHostRef(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "x.pdf")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))

	got, err := resolveOutboundFilePath("host://" + path)
	require.NoError(t, err)
	require.Equal(t, path, got)
}

func TestResolveOutboundFilePath_ExpandsHome(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	got, err := resolveOutboundFilePath("~/x.pdf")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, "x.pdf"), got)
}
