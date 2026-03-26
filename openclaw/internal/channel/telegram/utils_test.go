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
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/croncmd"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
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

func TestBuildLaneKey(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		"telegram:dm:u1",
		buildLaneKey("u1", ""),
	)
	require.Equal(
		t,
		"telegram:thread:10:topic:2",
		buildLaneKey("u1", "10:topic:2"),
	)
}

func TestParseDMPolicy_DefaultAndInvalid(t *testing.T) {
	t.Parallel()

	got, err := parseDMPolicy("")
	require.NoError(t, err)
	require.Equal(t, defaultDMPolicy, got)

	_, err = parseDMPolicy("nope")
	require.Error(t, err)
}

func TestParseGroupPolicy_DefaultAndInvalid(t *testing.T) {
	t.Parallel()

	got, err := parseGroupPolicy("")
	require.NoError(t, err)
	require.Equal(t, defaultGroupPolicy, got)

	_, err = parseGroupPolicy("nope")
	require.Error(t, err)
}

func TestParseDMBlockCleanup_DefaultAndInvalid(t *testing.T) {
	t.Parallel()

	got, err := parseDMBlockCleanup("")
	require.NoError(t, err)
	require.Equal(t, defaultDMBlockCleanup, got)

	_, err = parseDMBlockCleanup("nope")
	require.Error(t, err)
}

func TestWithAllowUsers_EmptyResetsNil(t *testing.T) {
	t.Parallel()

	cfg := &config{
		allowUsers: map[string]struct{}{
			"u1": {},
		},
	}

	WithAllowUsers()(cfg)
	require.Nil(t, cfg.allowUsers)
}

func TestWithAllowUsers_TrimsAndSkipsEmpty(t *testing.T) {
	t.Parallel()

	cfg := &config{}
	WithAllowUsers(" u1 ", "", "  ")(cfg)

	require.Contains(t, cfg.allowUsers, "u1")
	require.Len(t, cfg.allowUsers, 1)
}

func TestWithAllowThreads_EmptyResetsNil(t *testing.T) {
	t.Parallel()

	cfg := &config{
		allowThreads: map[string]struct{}{
			"t1": {},
		},
	}

	WithAllowThreads()(cfg)
	require.Nil(t, cfg.allowThreads)
}

func TestWithAllowThreads_TrimsAndSkipsEmpty(t *testing.T) {
	t.Parallel()

	cfg := &config{}
	WithAllowThreads(" t1 ", "", "  ")(cfg)

	require.Contains(t, cfg.allowThreads, "t1")
	require.Len(t, cfg.allowThreads, 1)
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

func TestSplitIndex_MinAndFallbacks(t *testing.T) {
	t.Parallel()

	require.Equal(t, 1, splitIndex([]rune("a"), 4))
	require.Equal(t, 2, splitIndex([]rune("a "), 4))
	require.Equal(t, 3, splitIndex([]rune("a\n\n"), 4))
	require.Equal(t, 2, splitIndex([]rune("a\n"), 4))
	require.Equal(t, 4, splitIndex([]rune("abcd"), 4))
}

func TestParseCommand(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", parseCommand("hi", BotInfo{}))
	require.Equal(t, "help", parseCommand("/help", BotInfo{}))
	require.Equal(t, "help", parseCommand(" /help ", BotInfo{}))
	require.Equal(t, "help", parseCommand("/help@bot", BotInfo{Username: "bot"}))
	require.Equal(t, "", parseCommand("/help@x", BotInfo{Username: "bot"}))

	call := parseCommandCall(
		"/persona@bot girlfriend  ",
		BotInfo{Username: "bot"},
	)
	require.Equal(t, commandPersona, call.Name)
	require.Equal(t, "girlfriend", call.Args)
}

func TestCurrentChatTargetAndRunCountFormatting(t *testing.T) {
	t.Parallel()

	require.Equal(t, "123", currentChatTarget(123, 0))
	require.Equal(
		t,
		"123"+threadTopicSep+"7",
		currentChatTarget(123, 7),
	)

	require.Equal(
		t,
		"runs 2/5",
		formatScheduledJobRunCount(
			gwclient.ScheduledJobSummary{
				RunCount: 2,
				MaxRuns:  5,
			},
		),
	)
	require.Equal(
		t,
		"runs 2",
		formatScheduledJobRunCount(
			gwclient.ScheduledJobSummary{
				RunCount: 2,
			},
		),
	)
	require.Empty(
		t,
		formatScheduledJobRunCount(gwclient.ScheduledJobSummary{}),
	)
}

func TestReplySelectedJobAndMutationHelpers(t *testing.T) {
	t.Parallel()

	bot := &stubBot{}
	ch := &Channel{bot: bot}
	ctx := context.Background()
	jobs := []gwclient.ScheduledJobSummary{{
		ID:      "job-1",
		Name:    "daily",
		Enabled: true,
	}}

	require.NoError(
		t,
		ch.replySelectedJob(ctx, 1, 0, 2, jobs, "missing"),
	)
	require.NoError(
		t,
		ch.replySelectedJob(ctx, 1, 0, 2, jobs, "job-1"),
	)

	manager := &stubGatewayWithJobs{
		stubGateway: &stubGateway{},
		jobs:        append([]gwclient.ScheduledJobSummary(nil), jobs...),
	}
	require.NoError(
		t,
		ch.setSelectedJobEnabled(
			ctx,
			1,
			0,
			2,
			manager,
			"user-1",
			"123",
			jobs,
			croncmd.ActionStop,
			"job-1",
			false,
		),
	)
	require.NotNil(t, manager.lastEnabled)
	require.False(t, *manager.lastEnabled)

	manager.updateErr = errors.New("boom")
	require.NoError(
		t,
		ch.setSelectedJobEnabled(
			ctx,
			1,
			0,
			2,
			manager,
			"user-1",
			"123",
			jobs,
			croncmd.ActionResume,
			"job-1",
			true,
		),
	)

	manager.removeSuccess = true
	manager.removeErr = nil
	require.NoError(
		t,
		ch.removeSelectedJob(
			ctx,
			1,
			0,
			2,
			manager,
			"user-1",
			"123",
			jobs,
			"job-1",
		),
	)

	manager.removeErr = errors.New("boom")
	require.NoError(
		t,
		ch.removeSelectedJob(
			ctx,
			1,
			0,
			2,
			manager,
			"user-1",
			"123",
			jobs,
			"job-1",
		),
	)

	bot.mu.Lock()
	require.Len(t, bot.sent, 6)
	require.Contains(t, bot.sent[0].Text, "cron list")
	require.Contains(t, bot.sent[0].Text, "cron status")
	require.Contains(t, bot.sent[1].Text, "daily")
	require.Contains(t, bot.sent[2].Text, "Stopped")
	require.Contains(t, bot.sent[3].Text, jobsUpdateFailedMessage)
	require.Contains(t, bot.sent[4].Text, "Removed")
	require.Contains(t, bot.sent[5].Text, jobsRemoveFailedMessage)
	bot.mu.Unlock()
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

func TestPairingStorePath_StateDirEmpty(t *testing.T) {
	t.Parallel()

	_, err := PairingStorePath("", BotInfo{})
	require.Error(t, err)
}

func TestSanitizeFileToken_EmptyUsesDefault(t *testing.T) {
	t.Parallel()

	require.Equal(t, defaultOffsetKey, sanitizeFileToken("   "))
}

func TestNew_OptionValidationErrors(t *testing.T) {
	t.Parallel()

	_, err := New(
		testToken,
		BotInfo{Username: "bot"},
		&stubGateway{},
		WithStateDir(t.TempDir()),
		WithDMPolicy("nope"),
	)
	require.Error(t, err)

	_, err = New(
		testToken,
		BotInfo{Username: "bot"},
		&stubGateway{},
		WithStateDir(t.TempDir()),
		WithGroupPolicy("nope"),
	)
	require.Error(t, err)

	_, err = New(
		testToken,
		BotInfo{Username: "bot"},
		&stubGateway{},
		WithStateDir(t.TempDir()),
		WithPairingTTL(0),
	)
	require.Error(t, err)

	_, err = New(
		testToken,
		BotInfo{Username: "bot"},
		&stubGateway{},
		WithStateDir(t.TempDir()),
		WithStreamingMode("nope"),
	)
	require.Error(t, err)

	_, err = New(
		testToken,
		BotInfo{Username: "bot"},
		&stubGateway{},
		WithStateDir(t.TempDir()),
		WithAPIOptions(tgapi.WithBaseURL("")),
	)
	require.Error(t, err)
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

func TestChannel_CancelInflight_CancelErrorDoesNotClear(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		cancelOK:  true,
		cancelErr: errors.New("boom"),
	}
	ch := &Channel{
		gw:       gw,
		inflight: newInflightRequests(),
	}
	const (
		laneKey   = "lane"
		requestID = "req-1"
	)
	ch.inflight.Set(laneKey, requestID)

	canceled := ch.cancelInflight(context.Background(), laneKey)
	require.False(t, canceled)
	require.Equal(t, requestID, ch.inflight.Get(laneKey))
}

func TestChannel_HandleResetCommand_NoDMSessionsRepliesFailed(t *testing.T) {
	t.Parallel()

	bot := &stubBot{}
	ch := &Channel{
		bot:      bot,
		gw:       &stubGateway{},
		inflight: newInflightRequests(),
	}

	err := ch.handleResetCommand(
		context.Background(),
		1,
		0,
		2,
		"lane",
		"u1",
	)
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, resetFailedMessage, bot.sent[0].Text)
	bot.mu.Unlock()
}

func TestChannel_HandleResetCommand_RotateErrorRepliesFailed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path, err := dmSessionStorePath(dir, BotInfo{Username: "bot"})
	require.NoError(t, err)
	store, err := newDMSessionStore(path)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	bot := &stubBot{}
	ch := &Channel{
		bot:        bot,
		gw:         &stubGateway{},
		inflight:   newInflightRequests(),
		dmSessions: store,
	}

	err = ch.handleResetCommand(ctx, 1, 0, 2, "lane", "u1")
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, resetFailedMessage, bot.sent[0].Text)
	bot.mu.Unlock()
}

func TestChannel_HandleForgetCommand_SessionErrorRepliesOK(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path, err := dmSessionStorePath(dir, BotInfo{Username: "bot"})
	require.NoError(t, err)
	store, err := newDMSessionStore(path)
	require.NoError(t, err)

	gw := &stubGatewayWithForget{stubGateway: &stubGateway{}}
	bot := &stubBot{}
	ch := &Channel{
		bot:        bot,
		gw:         gw,
		inflight:   newInflightRequests(),
		dmSessions: store,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = ch.handleForgetCommand(ctx, 1, 0, 2, "lane", "u1")
	require.NoError(t, err)

	gw.mu.Lock()
	require.Equal(t, []string{"u1"}, gw.forgetCalls)
	gw.mu.Unlock()

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, forgetOKMessage, bot.sent[0].Text)
	bot.mu.Unlock()
}
