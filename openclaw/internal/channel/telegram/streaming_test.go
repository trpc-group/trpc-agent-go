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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

func TestParseStreamingMode_DefaultAndInvalid(t *testing.T) {
	t.Parallel()

	got, err := parseStreamingMode("")
	require.NoError(t, err)
	require.Equal(t, defaultStreamingMode, got)

	_, err = parseStreamingMode("nope")
	require.Error(t, err)
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
		"rid",
		2,
		"hi",
	)
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, processingMessage, bot.sent[0].Text)
	require.Len(t, bot.edits, 1)
	require.Equal(t, "nope", bot.edits[0].Text)
	bot.mu.Unlock()
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
		"rid",
		2,
		"hi",
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
		"rid",
		2,
		"hi",
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
		"rid",
		2,
		"hi",
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
		"rid",
		2,
		"hi",
	)
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, "ok", bot.sent[0].Text)
	require.Empty(t, bot.edits)
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
		"rid",
		2,
		"hi",
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
