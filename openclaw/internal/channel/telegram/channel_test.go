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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gwclient"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

const (
	testToken = "token"

	chatTypePrivate    = "private"
	chatTypeSuperGroup = "supergroup"
)

type stubGateway struct {
	mu    sync.Mutex
	reqs  []gwclient.MessageRequest
	rsp   gwclient.MessageResponse
	err   error
	delay time.Duration

	onSend func()
}

func (g *stubGateway) SendMessage(
	ctx context.Context,
	req gwclient.MessageRequest,
) (gwclient.MessageResponse, error) {
	if g.delay > 0 {
		select {
		case <-ctx.Done():
			return gwclient.MessageResponse{}, ctx.Err()
		case <-time.After(g.delay):
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.reqs = append(g.reqs, req)
	if g.onSend != nil {
		g.onSend()
	}
	return g.rsp, g.err
}

type stubBot struct {
	mu       sync.Mutex
	sent     []tgapi.SendMessageParams
	sendErr  error
	updates  [][]tgapi.Update
	getError error
}

func (b *stubBot) GetUpdates(
	_ context.Context,
	_ int,
	_ time.Duration,
) ([]tgapi.Update, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.getError != nil {
		return nil, b.getError
	}
	if len(b.updates) == 0 {
		return nil, nil
	}
	out := b.updates[0]
	b.updates = b.updates[1:]
	return out, nil
}

func (b *stubBot) SendMessage(
	_ context.Context,
	params tgapi.SendMessageParams,
) (tgapi.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, params)
	return tgapi.Message{}, b.sendErr
}

type stubOffsetStore struct {
	mu     sync.Mutex
	offset int
	ok     bool
	err    error
	writes []int
}

func (s *stubOffsetStore) Read(
	_ context.Context,
) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.offset, s.ok, s.err
}

func (s *stubOffsetStore) Write(
	_ context.Context,
	offset int,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, offset)
	return nil
}

func TestProbeBotInfo_EmptyToken(t *testing.T) {
	t.Parallel()

	info, err := ProbeBotInfo(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, BotInfo{}, info)
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
}

func TestNew_Errors(t *testing.T) {
	t.Parallel()

	_, err := New("", BotInfo{}, &stubGateway{})
	require.Error(t, err)

	_, err = New(testToken, BotInfo{}, nil)
	require.Error(t, err)
}

func TestChannel_ID(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
	)
	require.NoError(t, err)
	require.Equal(t, "telegram", ch.ID())
}

func TestChannel_Run_Nil(t *testing.T) {
	t.Parallel()

	var ch *Channel
	err := ch.Run(context.Background())
	require.Error(t, err)
}

func TestChannel_HandleMessage_PrivateChat(t *testing.T) {
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

	gw.mu.Lock()
	require.Len(t, gw.reqs, 1)
	req := gw.reqs[0]
	gw.mu.Unlock()

	require.Equal(t, "telegram", req.Channel)
	require.Equal(t, "2", req.From)
	require.Equal(t, "", req.Thread)
	require.Equal(t, "3", req.MessageID)
	require.Equal(t, "hi", req.Text)
	require.Equal(t, "2", req.UserID)
	require.Equal(t, "telegram:1:3", req.RequestID)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	sent := bot.sent[0]
	bot.mu.Unlock()

	require.Equal(t, int64(1), sent.ChatID)
	require.Equal(t, 0, sent.MessageThreadID)
	require.Equal(t, 3, sent.ReplyToMessageID)
	require.Equal(t, "ok", sent.Text)
}

func TestChannel_HandleMessage_GroupTopic(t *testing.T) {
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
	)
	require.NoError(t, err)

	bot := &stubBot{}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID:       7,
		MessageThreadID: 99,
		From:            &tgapi.User{ID: 8},
		Chat: &tgapi.Chat{
			ID:   10,
			Type: chatTypeSuperGroup,
		},
		Text: "hi",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Len(t, gw.reqs, 1)
	req := gw.reqs[0]
	gw.mu.Unlock()

	require.Equal(t, "10:topic:99", req.Thread)
	require.Equal(t, "telegram:10:99:7", req.RequestID)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	sent := bot.sent[0]
	bot.mu.Unlock()

	require.Equal(t, int64(10), sent.ChatID)
	require.Equal(t, 99, sent.MessageThreadID)
	require.Equal(t, 7, sent.ReplyToMessageID)
}

func TestChannel_HandleMessage_Gateway4xx_Drop(t *testing.T) {
	t.Parallel()

	gwErr := errors.New("bad request")
	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusBadRequest,
		},
		err: gwErr,
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
	)
	require.NoError(t, err)

	bot := &stubBot{}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 1,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 3, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.NoError(t, err)

	bot.mu.Lock()
	require.Empty(t, bot.sent)
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_Gateway5xx_Retry(t *testing.T) {
	t.Parallel()

	gwErr := errors.New("server error")
	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusInternalServerError,
		},
		err: gwErr,
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
	)
	require.NoError(t, err)

	bot := &stubBot{}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 1,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 3, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.ErrorIs(t, err, gwErr)
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

func TestChannel_Run_OneMessage(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
		onSend: cancel,
	}
	gw.delay = 0

	bot := &stubBot{
		updates: [][]tgapi.Update{
			{
				{
					UpdateID: 1,
					Message: &tgapi.Message{
						MessageID: 1,
						From:      &tgapi.User{ID: 2},
						Chat: &tgapi.Chat{
							ID:   3,
							Type: chatTypePrivate,
						},
						Text: "hi",
					},
				},
			},
		},
	}

	store := &stubOffsetStore{}
	ch := &Channel{
		bot:             bot,
		gw:              gw,
		store:           store,
		startFromLatest: false,
		pollTimeout:     0,
		errorBackoff:    0,
	}

	require.NoError(t, ch.Run(ctx))
}

func TestChannel_HandleMessage_Ignored(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Ignored:    true,
			Reply:      "ok",
		},
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
	)
	require.NoError(t, err)

	bot := &stubBot{}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 1,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 3, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.NoError(t, err)

	bot.mu.Lock()
	require.Empty(t, bot.sent)
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_SendError_Drops(t *testing.T) {
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
	)
	require.NoError(t, err)

	botErr := errors.New("send failed")
	bot := &stubBot{sendErr: botErr}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 1,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 3, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.NoError(t, err)
}
