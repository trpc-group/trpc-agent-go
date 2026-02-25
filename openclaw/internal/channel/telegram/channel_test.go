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
	"io"
	"net/http"
	"net/http/httptest"
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

	canceled  []string
	cancelOK  bool
	cancelErr error
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

func (g *stubGateway) Cancel(
	_ context.Context,
	requestID string,
) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.canceled = append(g.canceled, requestID)
	return g.cancelOK, g.cancelErr
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

type stubPairingStore struct {
	approved bool
	code     string

	isApprovedErr error
	requestErr    error

	requested []string
}

func (s *stubPairingStore) IsApproved(
	_ context.Context,
	_ string,
) (bool, error) {
	if s.isApprovedErr != nil {
		return false, s.isApprovedErr
	}
	return s.approved, nil
}

func (s *stubPairingStore) Request(
	_ context.Context,
	userID string,
) (string, bool, error) {
	if s.requestErr != nil {
		return "", false, s.requestErr
	}
	s.requested = append(s.requested, userID)
	return s.code, false, nil
}

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
}

func TestParseCommand(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", parseCommand("hi", BotInfo{}))
	require.Equal(t, "help", parseCommand("/help", BotInfo{}))
	require.Equal(t, "help", parseCommand(" /help ", BotInfo{}))
	require.Equal(t, "help", parseCommand("/help@bot", BotInfo{Username: "bot"}))
	require.Equal(t, "", parseCommand("/help@x", BotInfo{Username: "bot"}))
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

func TestNew_OptionsApplied(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{}
	dir := t.TempDir()

	pollTimeout := 5 * time.Second
	errorBackoff := 7 * time.Second
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithStartFromLatest(false),
		WithPollTimeout(pollTimeout),
		WithErrorBackoff(errorBackoff),
	)
	require.NoError(t, err)

	require.False(t, ch.startFromLatest)
	require.Equal(t, pollTimeout, ch.pollTimeout)
	require.Equal(t, errorBackoff, ch.errorBackoff)
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
		WithDMPolicy(dmPolicyOpen),
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

func TestChannel_HandleMessage_CommandHelp(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{}
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
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "/help",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Empty(t, gw.reqs)
	gw.mu.Unlock()

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Contains(t, bot.sent[0].Text, "Commands:")
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_CommandCancel_NoInflight(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{cancelOK: true}
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
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "/cancel",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Empty(t, gw.canceled)
	gw.mu.Unlock()

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, cancelNoopMessage, bot.sent[0].Text)
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_CommandCancel_Inflight(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{cancelOK: true}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
	)
	require.NoError(t, err)

	ch.inflight.Set("telegram:dm:2", "req-1")

	bot := &stubBot{}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "/cancel",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Equal(t, []string{"req-1"}, gw.canceled)
	gw.mu.Unlock()

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, cancelOKMessage, bot.sent[0].Text)
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_DMPolicyAllowlist_NoAllowUsers(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:        bot,
		gw:         gw,
		dmPolicy:   dmPolicyAllowlist,
		allowUsers: nil,
	}

	err := ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Empty(t, gw.reqs)
	gw.mu.Unlock()

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, notAllowedMessage, bot.sent[0].Text)
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_DMPolicyPairing_Unapproved(t *testing.T) {
	t.Parallel()

	p := &stubPairingStore{approved: false, code: "123456"}
	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:      bot,
		gw:       gw,
		dmPolicy: dmPolicyPairing,
		pairing:  p,
	}

	err := ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Empty(t, gw.reqs)
	gw.mu.Unlock()

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Contains(t, bot.sent[0].Text, "Pairing required")
	require.Contains(t, bot.sent[0].Text, "123456")
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_DMPolicyPairing_Approved(t *testing.T) {
	t.Parallel()

	p := &stubPairingStore{approved: true, code: "123456"}
	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:      bot,
		gw:       gw,
		dmPolicy: dmPolicyPairing,
		pairing:  p,
	}

	err := ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Len(t, gw.reqs, 1)
	gw.mu.Unlock()

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, "ok", bot.sent[0].Text)
	bot.mu.Unlock()
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
		WithGroupPolicy(groupPolicyOpen),
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

func TestChannel_HandleMessage_GroupPolicyAllowlist_Drops(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:         bot,
		gw:          gw,
		groupPolicy: groupPolicyAllowlist,
		allowThreads: map[string]struct{}{
			"11": {},
		},
	}

	err := ch.handleMessage(context.Background(), tgapi.Message{
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
	require.Empty(t, gw.reqs)
	gw.mu.Unlock()
}

func TestChannel_HandleMessage_GroupPolicyAllowlist_AllowsChatID(
	t *testing.T,
) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:         bot,
		gw:          gw,
		groupPolicy: groupPolicyAllowlist,
		allowThreads: map[string]struct{}{
			"10": {},
		},
	}

	err := ch.handleMessage(context.Background(), tgapi.Message{
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
	gw.mu.Unlock()
}

func TestChannel_HandleMessage_GroupPolicyAllowlist_AllowsTopic(
	t *testing.T,
) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:         bot,
		gw:          gw,
		groupPolicy: groupPolicyAllowlist,
		allowThreads: map[string]struct{}{
			"10:topic:99": {},
		},
	}

	err := ch.handleMessage(context.Background(), tgapi.Message{
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
	gw.mu.Unlock()
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
		WithDMPolicy(dmPolicyOpen),
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
		WithDMPolicy(dmPolicyOpen),
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
		dmPolicy:        dmPolicyOpen,
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
		WithDMPolicy(dmPolicyOpen),
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
		WithDMPolicy(dmPolicyOpen),
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
