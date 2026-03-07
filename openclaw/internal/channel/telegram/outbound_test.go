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
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
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

	got, err := resolveOutboundFilePath(
		context.Background(),
		"",
		"host://"+path,
	)
	require.NoError(t, err)
	require.Equal(t, path, got)
}

func TestResolveOutboundFilePath_ExpandsHome(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	got, err := resolveOutboundFilePath(context.Background(), "", "~/x.pdf")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, "x.pdf"), got)
}

func TestResolveOutboundFilePath_FileURL(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "frame.png")
	require.NoError(
		t,
		os.WriteFile(path, []byte("png"), 0o600),
	)

	got, err := resolveOutboundFilePath(
		context.Background(),
		"",
		"file://"+path,
	)
	require.NoError(t, err)
	require.Equal(t, path, got)
}

func TestResolveOutboundFilePath_InvalidFileURL(t *testing.T) {
	t.Parallel()

	_, err := resolveOutboundFilePath(context.Background(), "", "file://")
	require.Error(t, err)
}

func TestDetectUploadMode(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		uploadModePhoto,
		detectUploadMode("frame.png", []byte("png")),
	)
	require.Equal(
		t,
		uploadModeAudio,
		detectUploadMode("note.mp3", []byte("mp3")),
	)
	require.Equal(
		t,
		uploadModeVoice,
		detectUploadMode("voice.oga", []byte("ogg")),
	)
	require.Equal(
		t,
		uploadModeVideo,
		detectUploadMode("clip.mp4", []byte("mp4")),
	)
	require.Equal(
		t,
		uploadModeDocument,
		detectUploadMode("report.pdf", []byte("%PDF-1.4")),
	)
}

func TestParseTextTargetErrors(t *testing.T) {
	t.Parallel()

	_, _, err := parseTextTarget("")
	require.Error(t, err)

	_, _, err = parseTextTarget("abc")
	require.Error(t, err)

	_, _, err = parseTextTarget("100:topic:")
	require.Error(t, err)
}

func TestResolveOutboundFile_EmptyPath(t *testing.T) {
	t.Parallel()

	_, err := resolveOutboundFile(
		context.Background(),
		"",
		channel.OutboundFile{},
	)
	require.Error(t, err)
}

func TestExpandHomePath_ResolvesBareHome(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	got, err := expandHomePath("~")
	require.NoError(t, err)
	require.Equal(t, home, got)
}

func TestTypeFromExtensionAndVoiceDetection(t *testing.T) {
	t.Parallel()

	require.Equal(t, "video/quicktime", typeFromExtension(".mov"))
	require.Equal(t, "image/gif", typeFromExtension(".gif"))
	require.Equal(t, "", typeFromExtension(".unknown"))
	require.True(t, isVoiceMedia(mimeVoiceOGG, "voice.oga"))
	require.False(t, isVoiceMedia("audio/mpeg", "voice.mp3"))
}

func TestSendTextAndSendMessage_Errors(t *testing.T) {
	t.Parallel()

	var ch *Channel
	err := ch.SendText(context.Background(), "100", "hi")
	require.Error(t, err)

	ch = &Channel{bot: &stubBot{}}
	err = ch.SendMessage(
		context.Background(),
		"bad-target",
		channel.OutboundMessage{Text: "hi"},
	)
	require.Error(t, err)
}

func TestResolveOutboundFilePath_Relative(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cwdMu.Lock()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(cwd))
		cwdMu.Unlock()
	})

	got, err := resolveOutboundFilePath(
		context.Background(),
		"",
		"report.pdf",
	)
	require.NoError(t, err)
	require.Equal(t, "report.pdf", filepath.Base(got))
	require.True(t, strings.HasSuffix(got, filepath.Join("001", "report.pdf")))
}

func TestResolveOutboundFilePath_SessionUploadsFallback(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	scope := uploads.Scope{
		Channel:   channelID,
		UserID:    "u1",
		SessionID: "telegram:dm:u1:s1",
	}
	saved, err := store.Save(
		context.Background(),
		scope,
		"split.pdf",
		[]byte("%PDF-1.4"),
	)
	require.NoError(t, err)

	ctx := agent.NewInvocationContext(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationSession(
				session.NewSession("app", "u1", "telegram:dm:u1:s1"),
			),
		),
	)

	got, err := resolveOutboundFilePath(ctx, stateDir, "split.pdf")
	require.NoError(t, err)
	require.Equal(t, saved.Path, got)
}

func TestResolveHostOutboundFile_NameHintAndReadError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "report.pdf")
	require.NoError(t, os.WriteFile(path, []byte("pdf"), 0o600))

	got, err := resolveHostOutboundFile(
		context.Background(),
		"",
		path,
		"renamed.pdf",
	)
	require.NoError(t, err)
	require.Equal(t, "renamed.pdf", got.Name)
	require.Equal(t, []byte("pdf"), got.Data)

	_, err = resolveHostOutboundFile(
		context.Background(),
		"",
		filepath.Join(root, "missing.pdf"),
		"",
	)
	require.Error(t, err)
}

func TestDetectMediaType_PrefersExtensionFallback(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		"application/pdf",
		detectMediaType("report.pdf", []byte("plain text")),
	)
	require.Equal(
		t,
		"image/png",
		detectMediaType("frame.png", []byte("not real image")),
	)
}

func TestParseSessionTargets_Invalid(t *testing.T) {
	t.Parallel()

	_, ok := parseDMSessionTarget("")
	require.False(t, ok)

	_, ok = parseThreadSessionTarget("100")
	require.False(t, ok)

	require.Equal(t, "100", leadingSessionToken("100:session-1"))
	require.Equal(
		t,
		"100",
		mustParseLegacyDMTarget(t, "100:session-1"),
	)
	_, ok = parseLegacyDMSessionTarget("100:123")
	require.False(t, ok)
}

func TestResolveTextTargetFromSessionID_BareSuffix(t *testing.T) {
	t.Parallel()

	target, ok := ResolveTextTargetFromSessionID("100:session-abc")
	require.True(t, ok)
	require.Equal(t, "100", target)

	target, ok = ResolveTextTargetFromSessionID(
		"-100123:topic:7:session-abc",
	)
	require.True(t, ok)
	require.Equal(t, "-100123:topic:7", target)
}

func TestParseTextTarget_BareSessionSuffix(t *testing.T) {
	t.Parallel()

	chatID, threadID, err := parseTextTarget("100:session-abc")
	require.NoError(t, err)
	require.EqualValues(t, 100, chatID)
	require.Zero(t, threadID)
}

func mustParseLegacyDMTarget(t *testing.T, raw string) string {
	t.Helper()

	target, ok := parseLegacyDMSessionTarget(raw)
	require.True(t, ok)
	return target
}
