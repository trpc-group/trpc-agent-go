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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gwclient"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

func TestProbeBotInfo_EmptyToken(t *testing.T) {
	t.Parallel()

	info, err := ProbeBotInfo(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, BotInfo{}, info)
}

func TestProbeBotInfo_HTTP(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/bot"+testToken+"/getMe", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(
			w,
			`{"ok":true,"result":{"id":123,"username":" my_bot "}}`,
		)
	}))
	t.Cleanup(srv.Close)

	info, err := ProbeBotInfo(
		context.Background(),
		testToken,
		tgapi.WithBaseURL(srv.URL),
		tgapi.WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)
	require.Equal(t, int64(123), info.ID)
	require.Equal(t, "my_bot", info.Username)
	require.Equal(t, "@my_bot", info.Mention)
}

func TestProbeBotInfo_HTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/bot"+testToken+"/getMe", r.URL.Path)

		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	_, err := ProbeBotInfo(
		context.Background(),
		testToken,
		tgapi.WithBaseURL(srv.URL),
		tgapi.WithHTTPClient(srv.Client()),
	)
	require.Error(t, err)
}

func TestMentionFromUsername(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", mentionFromUsername(""))
	require.Equal(t, "@bot", mentionFromUsername(" bot "))
}

func TestOffsetKey(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		"default",
		offsetKey(BotInfo{}),
	)
	require.Equal(
		t,
		"123",
		offsetKey(BotInfo{ID: 123}),
	)
	require.Equal(
		t,
		"my_bot",
		offsetKey(BotInfo{Username: "my bot"}),
	)
}

func TestBuildRequestID(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		"telegram:1:2",
		buildRequestID(1, 0, 2),
	)
	require.Equal(
		t,
		"telegram:1:9:2",
		buildRequestID(1, 9, 2),
	)
}

func TestSplitRunes(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		[]string{"hello"},
		splitRunes("hello", 0),
	)
	require.Equal(
		t,
		[]string{"he", "ll", "o"},
		splitRunes("hello", 2),
	)
	require.Equal(
		t,
		[]string{"a\n\n", "b"},
		splitRunes("a\n\nb", 3),
	)
}

func TestParseCommand(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", parseCommand("hi", BotInfo{}))
	require.Equal(t, "help", parseCommand("/help", BotInfo{}))
	require.Equal(t, "help", parseCommand(" /help ", BotInfo{}))
	require.Equal(t, "help", parseCommand("/help@bot", BotInfo{Username: "bot"}))
	require.Equal(t, "", parseCommand("/help@x", BotInfo{Username: "bot"}))
}

func TestResolveStateDir_Default(t *testing.T) {
	t.Parallel()

	got, err := resolveStateDir("")
	require.NoError(t, err)

	suffix := filepath.Join(defaultStateRootDir, defaultStateAppName)
	require.True(t, strings.HasSuffix(got, suffix))
}

func TestNewOffsetStore_StateDirEmpty(t *testing.T) {
	t.Parallel()

	_, err := newOffsetStore("", BotInfo{})
	require.Error(t, err)
}

func TestNewOffsetStore_WritesToExpectedPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	bot := BotInfo{Username: "my bot"}

	store, err := newOffsetStore(dir, bot)
	require.NoError(t, err)

	require.NoError(t, store.Write(context.Background(), 10))

	filename := offsetStoreFilePrefix +
		offsetKey(bot) +
		offsetStoreFileSuffix
	path := filepath.Join(dir, offsetStoreDir, filename)

	_, err = os.Stat(path)
	require.NoError(t, err)
}

func TestChannel_HandleMessage_ReplySplit(t *testing.T) {
	t.Parallel()

	reply := strings.Repeat("a", maxReplyRunes+1)
	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      reply,
		},
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
	)
	require.NoError(t, err)

	bot := &stubBot{}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 5,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 3, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.sent, 2)
	first := bot.sent[0]
	second := bot.sent[1]
	bot.mu.Unlock()

	require.Equal(t, 5, first.ReplyToMessageID)
	require.Equal(t, 0, second.ReplyToMessageID)
	require.Len(t, first.Text, maxReplyRunes)
	require.Len(t, second.Text, 1)
}
