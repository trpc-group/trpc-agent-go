//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telegram

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const debugEventsFilePattern = "events.jsonl*"

func TestParseStreamingMode_DefaultAndInvalid(t *testing.T) {
	t.Parallel()

	got, err := parseStreamingMode("")
	require.NoError(t, err)
	require.Equal(t, defaultStreamingMode, got)

	_, err = parseStreamingMode("nope")
	require.Error(t, err)
}

func TestProgressEditInterval(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		progressEditIntervalFast,
		progressEditInterval(progressEditAfterMedium-time.Second),
	)
	require.Equal(
		t,
		progressEditIntervalMedium,
		progressEditInterval(progressEditAfterMedium),
	)
	require.Equal(
		t,
		progressEditIntervalSlow,
		progressEditInterval(progressEditAfterSlow),
	)
	require.Equal(
		t,
		progressEditIntervalVerySlow,
		progressEditInterval(progressEditAfterVerySlow),
	)
}

func TestChannel_SendPreviewMessage_ModeOffOrNoBot(t *testing.T) {
	t.Parallel()

	ch := &Channel{}
	_, ok := ch.sendPreviewMessage(
		context.Background(),
		1,
		0,
		0,
		streamingBlock,
	)
	require.False(t, ok)

	ch.bot = &stubBot{}
	_, ok = ch.sendPreviewMessage(
		context.Background(),
		1,
		0,
		0,
		streamingOff,
	)
	require.False(t, ok)
}

func TestChannel_SendPreviewMessage_SendFails(t *testing.T) {
	t.Parallel()

	bot := &stubBot{sendErr: errors.New("send failed")}
	ch := &Channel{bot: bot}
	_, ok := ch.sendPreviewMessage(
		context.Background(),
		1,
		0,
		0,
		streamingBlock,
	)
	require.False(t, ok)
}

func TestChannel_CallGatewayAndReply_4xxEditsPreviewAndDrops(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusBadRequest,
			Error: &gwclient.APIError{
				Type:    "bad_request",
				Message: "nope",
			},
		},
		err: errors.New("boom"),
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:           bot,
		gw:            gw,
		streamingMode: streamingBlock,
	}

	err := ch.callGatewayAndReply(
		context.Background(),
		1,
		0,
		2,
		"u1",
		"",
		buildLaneKey("u1", ""),
		"rid",
		tgapi.Message{MessageID: 2, Text: "hi"},
	)
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, processingMessage, bot.sent[0].Text)
	require.Len(t, bot.edits, 1)
	require.Equal(t, "nope", bot.edits[0].Text)
	bot.mu.Unlock()
}

func TestChannel_CallGatewayAndReply_RecorderCreatesTrace(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:           bot,
		gw:            gw,
		streamingMode: streamingOff,
	}

	mode, err := debugrecorder.ParseMode("safe")
	require.NoError(t, err)

	rec, err := debugrecorder.New(t.TempDir(), mode)
	require.NoError(t, err)

	ctx := debugrecorder.WithRecorder(context.Background(), rec)
	err = ch.callGatewayAndReply(
		ctx,
		1,
		0,
		2,
		"u1",
		"",
		buildLaneKey("u1", ""),
		"rid",
		tgapi.Message{MessageID: 2, Text: "hi"},
	)
	require.NoError(t, err)

	matches, err := filepath.Glob(
		filepath.Join(rec.Dir(), "*", "*", debugEventsFilePattern),
	)
	require.NoError(t, err)
	require.Len(t, matches, 1)

	raw, err := debugrecorder.ReadEventsFile(filepath.Dir(matches[0]))
	require.NoError(t, err)
	require.Contains(t, string(raw), debugrecorder.KindTelegramMessage)
}

func TestChannel_CallGatewayAndReply_5xxReturnsError(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusInternalServerError,
		},
		err: errors.New("boom"),
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:           bot,
		gw:            gw,
		streamingMode: streamingBlock,
	}

	err := ch.callGatewayAndReply(
		context.Background(),
		1,
		0,
		2,
		"u1",
		"",
		buildLaneKey("u1", ""),
		"rid",
		tgapi.Message{MessageID: 2, Text: "hi"},
	)
	require.Error(t, err)

	bot.mu.Lock()
	require.Len(t, bot.edits, 1)
	require.Equal(t, "Failed to process message.", bot.edits[0].Text)
	bot.mu.Unlock()
}

func TestChannel_CallGatewayAndReply_IgnoredEditsPreview(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Ignored:    true,
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:           bot,
		gw:            gw,
		streamingMode: streamingBlock,
	}

	err := ch.callGatewayAndReply(
		context.Background(),
		1,
		0,
		2,
		"u1",
		"",
		buildLaneKey("u1", ""),
		"rid",
		tgapi.Message{MessageID: 2, Text: "hi"},
	)
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, processingMessage, bot.sent[0].Text)
	require.Len(t, bot.edits, 1)
	require.Equal(t, "Ignored.", bot.edits[0].Text)
	bot.mu.Unlock()
}

func TestChannel_CallGatewayAndReply_NoReplyEditsPreview(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "  ",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:           bot,
		gw:            gw,
		streamingMode: streamingBlock,
	}

	err := ch.callGatewayAndReply(
		context.Background(),
		1,
		0,
		2,
		"u1",
		"",
		buildLaneKey("u1", ""),
		"rid",
		tgapi.Message{MessageID: 2, Text: "hi"},
	)
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.edits, 1)
	require.Equal(t, "No reply.", bot.edits[0].Text)
	bot.mu.Unlock()
}

func TestChannel_CallGatewayAndReply_StreamingOff(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:           bot,
		gw:            gw,
		streamingMode: streamingOff,
	}

	err := ch.callGatewayAndReply(
		context.Background(),
		1,
		0,
		2,
		"u1",
		"",
		buildLaneKey("u1", ""),
		"rid",
		tgapi.Message{MessageID: 2, Text: "hi"},
	)
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, "ok", bot.sent[0].Text)
	require.Empty(t, bot.edits)
	bot.mu.Unlock()
}

func TestChannel_CallGatewayAndReply_AutoSendsDerivedFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cwdMu.Lock()
	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	defer func() {
		require.NoError(t, os.Chdir(oldWD))
		cwdMu.Unlock()
	}()

	framesDir := filepath.Join(root, "frames")
	require.NoError(t, os.MkdirAll(framesDir, 0o755))
	frameA := filepath.Join(framesDir, "frame-1.png")
	frameB := filepath.Join(framesDir, "frame-2.png")
	require.NoError(
		t,
		os.WriteFile(frameA, []byte("png-a"), 0o600),
	)
	require.NoError(
		t,
		os.WriteFile(frameB, []byte("png-b"), 0o600),
	)

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply: "已导出两张图片：`frames/frame-1.png` 和 " +
				"`frames/frame-2.png`。",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:           bot,
		gw:            gw,
		state:         filepath.Join(root, "state"),
		streamingMode: streamingOff,
	}

	err = ch.callGatewayAndReply(
		context.Background(),
		1,
		0,
		2,
		"u1",
		"",
		buildLaneKey("u1", ""),
		"rid",
		tgapi.Message{MessageID: 2, Text: "hi"},
	)
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Contains(t, bot.sent[0].Text, "<code>frame-1.png</code>")
	require.Contains(t, bot.sent[0].Text, "<code>frame-2.png</code>")
	require.Len(t, bot.photos, 2)
	require.Equal(t, "frame-1.png", bot.photos[0].FileName)
	require.Equal(t, "frame-2.png", bot.photos[1].FileName)
	bot.mu.Unlock()
}

func TestChannel_CallGatewayAndReply_AutoSendsBareOutputFilenames(
	t *testing.T,
) {
	t.Parallel()

	root := t.TempDir()
	cwdMu.Lock()
	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	defer func() {
		require.NoError(t, os.Chdir(oldWD))
		cwdMu.Unlock()
	}()

	frameA := filepath.Join(root, "frame-1.png")
	frameB := filepath.Join(root, "frame-2.png")
	require.NoError(
		t,
		os.WriteFile(frameA, []byte("png-a"), 0o600),
	)
	require.NoError(
		t,
		os.WriteFile(frameB, []byte("png-b"), 0o600),
	)

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply: "已提取两张图片：`frame-1.png` 和 " +
				"`frame-2.png`。",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:           bot,
		gw:            gw,
		state:         filepath.Join(root, "state"),
		streamingMode: streamingOff,
	}

	err = ch.callGatewayAndReply(
		context.Background(),
		1,
		0,
		2,
		"u1",
		"",
		buildLaneKey("u1", ""),
		"rid",
		tgapi.Message{MessageID: 2, Text: "hi"},
	)
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Contains(t, bot.sent[0].Text, "<code>frame-1.png</code>")
	require.Contains(t, bot.sent[0].Text, "<code>frame-2.png</code>")
	require.Len(t, bot.photos, 2)
	require.Equal(t, "frame-1.png", bot.photos[0].FileName)
	require.Equal(t, "frame-2.png", bot.photos[1].FileName)
	require.Empty(t, bot.docs)
	require.Empty(t, bot.audios)
	require.Empty(t, bot.voices)
	require.Empty(t, bot.videos)
	bot.mu.Unlock()
}

func TestChannel_CallGatewayAndReply_SkipsFilesAlreadySentByTool(
	t *testing.T,
) {
	t.Parallel()

	root := t.TempDir()
	cwdMu.Lock()
	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	defer func() {
		require.NoError(t, os.Chdir(oldWD))
		cwdMu.Unlock()
	}()

	frameA := filepath.Join(root, "frame-1.png")
	frameB := filepath.Join(root, "frame-2.png")
	require.NoError(
		t,
		os.WriteFile(frameA, []byte("png-a"), 0o600),
	)
	require.NoError(
		t,
		os.WriteFile(frameB, []byte("png-b"), 0o600),
	)

	bot := &stubBot{}
	ch := &Channel{
		bot:           bot,
		gw:            &stubGateway{},
		state:         filepath.Join(root, "state"),
		streamingMode: streamingOff,
		sentFiles:     newSentFileTracker(),
	}

	sessionID := buildLaneKey("100", "")
	inv := agent.NewInvocation(
		agent.WithInvocationSession(
			session.NewSession("app", "100", sessionID),
		),
		agent.WithInvocationRunOptions(
			agent.RunOptions{RequestID: "rid"},
		),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	err = ch.SendMessage(
		ctx,
		"1",
		channel.OutboundMessage{
			Files: []channel.OutboundFile{
				{Path: frameA},
				{Path: frameB},
			},
		},
	)
	require.NoError(t, err)

	ch.gw = &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "已发出 `frame-1.png` 和 `frame-2.png`。",
		},
	}
	err = ch.callGatewayAndReply(
		context.Background(),
		1,
		0,
		2,
		"100",
		"",
		sessionID,
		"rid",
		tgapi.Message{MessageID: 2, Text: "hi"},
	)
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.photos, 2)
	bot.mu.Unlock()
}

func TestChannel_CallGatewayAndReply_AutoSendsDirectoryCueFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cwdMu.Lock()
	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	defer func() {
		require.NoError(t, os.Chdir(oldWD))
		cwdMu.Unlock()
	}()

	outDir := filepath.Join(root, "out_pdf_split")
	require.NoError(t, os.MkdirAll(outDir, 0o755))
	fileA := filepath.Join(outDir, "page-3.pdf")
	fileB := filepath.Join(outDir, "page-4.pdf")
	require.NoError(t, os.WriteFile(fileA, []byte("a"), 0o600))
	require.NoError(t, os.WriteFile(fileB, []byte("b"), 0o600))

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "已拆分完成，文件在目录 out_pdf_split 里。",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:           bot,
		gw:            gw,
		state:         filepath.Join(root, "state"),
		streamingMode: streamingOff,
	}

	err = ch.callGatewayAndReply(
		context.Background(),
		1,
		0,
		2,
		"u1",
		"",
		buildLaneKey("u1", ""),
		"rid",
		tgapi.Message{MessageID: 2, Text: "hi"},
	)
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.docs, 2)
	require.Equal(t, "page-3.pdf", bot.docs[0].FileName)
	require.Equal(t, "page-4.pdf", bot.docs[1].FileName)
	bot.mu.Unlock()
}

func TestChannel_CallGatewayAndReply_DoesNotAutoSendOutsideRoots(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "report.pdf")
	require.NoError(
		t,
		os.WriteFile(outside, []byte("%PDF-1.4"), 0o600),
	)

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "结果在 `" + outside + "`。",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:           bot,
		gw:            gw,
		state:         filepath.Join(root, "state"),
		streamingMode: streamingOff,
	}

	err := ch.callGatewayAndReply(
		context.Background(),
		1,
		0,
		2,
		"u1",
		"",
		buildLaneKey("u1", ""),
		"rid",
		tgapi.Message{MessageID: 2, Text: "hi"},
	)
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Contains(t, bot.sent[0].Text, "<code>report.pdf</code>")
	require.Empty(t, bot.docs)
	require.Empty(t, bot.photos)
	require.Empty(t, bot.audios)
	require.Empty(t, bot.voices)
	require.Empty(t, bot.videos)
	bot.mu.Unlock()
}

func TestChannel_CallGatewayAndReply_SplitsReplyForPreview(t *testing.T) {
	t.Parallel()

	reply := strings.Repeat("a", maxReplyRunes*2+1)
	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      reply,
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:           bot,
		gw:            gw,
		streamingMode: streamingBlock,
	}

	err := ch.callGatewayAndReply(
		context.Background(),
		1,
		0,
		2,
		"u1",
		"",
		buildLaneKey("u1", ""),
		"rid",
		tgapi.Message{MessageID: 2, Text: "hi"},
	)
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.sent, 3)
	require.Len(t, bot.edits, 1)
	require.Len(t, bot.edits[0].Text, maxReplyRunes)
	require.Len(t, bot.sent[1].Text, maxReplyRunes)
	require.Len(t, bot.sent[2].Text, 1)
	bot.mu.Unlock()
}

func TestChannel_CallGatewayAndReply_AttachmentTooLargeEditsPreview(
	t *testing.T,
) {
	t.Parallel()

	gw := &stubGateway{}
	bot := &stubBot{}
	ch := &Channel{
		bot:              bot,
		gw:               gw,
		streamingMode:    streamingBlock,
		maxDownloadBytes: 3,
	}

	err := ch.callGatewayAndReply(
		context.Background(),
		1,
		0,
		2,
		"u1",
		"",
		buildLaneKey("u1", ""),
		"rid",
		tgapi.Message{
			MessageID: 2,
			Photo: []tgapi.PhotoSize{
				{FileID: "p1", FileSize: 4},
			},
		},
	)
	require.NoError(t, err)

	gw.mu.Lock()
	require.Empty(t, gw.reqs)
	gw.mu.Unlock()

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, processingMessage, bot.sent[0].Text)
	require.Len(t, bot.edits, 1)
	require.Equal(
		t,
		attachmentTooLargeMessage(3),
		bot.edits[0].Text,
	)
	bot.mu.Unlock()
}

func TestChannel_CallGatewayAndReply_DownloadFailedEditsPreview(
	t *testing.T,
) {
	t.Parallel()

	gw := &stubGateway{}
	bot := &stubBot{
		downloads: map[string]stubDownload{
			"p1": {err: errors.New("download failed")},
		},
	}
	ch := &Channel{
		bot:              bot,
		gw:               gw,
		streamingMode:    streamingBlock,
		maxDownloadBytes: 10,
	}

	err := ch.callGatewayAndReply(
		context.Background(),
		1,
		0,
		2,
		"u1",
		"",
		buildLaneKey("u1", ""),
		"rid",
		tgapi.Message{
			MessageID: 2,
			Photo: []tgapi.PhotoSize{
				{FileID: "p1"},
			},
		},
	)
	require.NoError(t, err)

	gw.mu.Lock()
	require.Empty(t, gw.reqs)
	gw.mu.Unlock()

	bot.mu.Lock()
	require.Len(t, bot.edits, 1)
	require.Equal(t, downloadFailedMessage, bot.edits[0].Text)
	bot.mu.Unlock()
}

func TestChannel_CallGatewayAndReply_StreamingOffRepliesOnUserError(
	t *testing.T,
) {
	t.Parallel()

	gw := &stubGateway{}
	bot := &stubBot{}
	ch := &Channel{
		bot:              bot,
		gw:               gw,
		streamingMode:    streamingOff,
		maxDownloadBytes: 3,
	}

	err := ch.callGatewayAndReply(
		context.Background(),
		1,
		0,
		2,
		"u1",
		"",
		buildLaneKey("u1", ""),
		"rid",
		tgapi.Message{
			MessageID: 2,
			Photo: []tgapi.PhotoSize{
				{FileID: "p1", FileSize: 4},
			},
		},
	)
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(
		t,
		attachmentTooLargeMessage(3),
		bot.sent[0].Text,
	)
	bot.mu.Unlock()
}

func TestChannel_ProgressLoop_EditsAtLeastOnce(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bot := &stubBot{}
	ch := &Channel{bot: bot}
	cancelLoop, wg := ch.startProgressLoop(
		ctx,
		1,
		2,
		tgapi.Message{MessageID: 3},
		true,
		streamingProgress,
	)
	require.NotNil(t, cancelLoop)
	require.NotNil(t, wg)

	time.Sleep(progressInterval + 200*time.Millisecond)
	cancelLoop()
	wg.Wait()

	bot.mu.Lock()
	require.NotEmpty(t, bot.actions)
	require.NotEmpty(t, bot.edits)
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_StreamingBlock(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
		WithStreamingMode(streamingBlock),
	)
	require.NoError(t, err)

	bot := &stubBot{}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, processingMessage, bot.sent[0].Text)
	require.Len(t, bot.edits, 1)
	require.Equal(t, "ok", bot.edits[0].Text)
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_StreamingBlock_EditFails_Fallback(
	t *testing.T,
) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
		WithStreamingMode(streamingBlock),
	)
	require.NoError(t, err)

	bot := &stubBot{editErr: errors.New("edit failed")}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.sent, 2)
	require.Equal(t, processingMessage, bot.sent[0].Text)
	require.Equal(t, "ok", bot.sent[1].Text)
	require.Len(t, bot.edits, 1)
	bot.mu.Unlock()
}
