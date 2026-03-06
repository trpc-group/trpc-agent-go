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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

type stubGatewayWithForget struct {
	*stubGateway

	forgetCalls []string
	forgetErr   error
}

type stubGatewayWithJobs struct {
	*stubGateway

	jobs       []gwclient.ScheduledJobSummary
	jobsErr    error
	clearCnt   int
	clearErr   error
	lastUser   string
	lastTarget string
}

func (g *stubGatewayWithForget) ForgetUser(
	_ context.Context,
	_ string,
	userID string,
) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.forgetCalls = append(g.forgetCalls, userID)
	return g.forgetErr
}

func (g *stubGatewayWithJobs) ListScheduledJobs(
	_ context.Context,
	_ string,
	userID string,
	target string,
) ([]gwclient.ScheduledJobSummary, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lastUser = userID
	g.lastTarget = target
	if g.jobsErr != nil {
		return nil, g.jobsErr
	}
	out := make([]gwclient.ScheduledJobSummary, len(g.jobs))
	copy(out, g.jobs)
	return out, nil
}

func (g *stubGatewayWithJobs) ClearScheduledJobs(
	_ context.Context,
	_ string,
	userID string,
	target string,
) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lastUser = userID
	g.lastTarget = target
	if g.clearErr != nil {
		return 0, g.clearErr
	}
	return g.clearCnt, nil
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

func TestChannel_HandleMessage_CommandHelp_FromCaption(t *testing.T) {
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
		Caption:   "/help",
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

func TestChannel_HandleMessage_CommandForget_CallsGateway(t *testing.T) {
	t.Parallel()

	gw := &stubGatewayWithForget{
		stubGateway: &stubGateway{},
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
		Text:      "/reset",
	})
	require.NoError(t, err)

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 4,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "/forget",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Equal(t, []string{"2"}, gw.forgetCalls)
	gw.mu.Unlock()
}

func TestChannel_HandleMessage_CommandForget_Unsupported(t *testing.T) {
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
		Text:      "/forget",
	})
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, forgetUnsupportedMessage, bot.sent[0].Text)
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_CommandForget_Error(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("forget failed")
	gw := &stubGatewayWithForget{
		stubGateway: &stubGateway{},
		forgetErr:   wantErr,
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
		MessageID: 4,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "/forget",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Equal(t, []string{"2"}, gw.forgetCalls)
	gw.mu.Unlock()

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, forgetFailedMessage, bot.sent[0].Text)
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_CommandJobs(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 6, 16, 45, 0, 0, time.UTC)
	gw := &stubGatewayWithJobs{
		stubGateway: &stubGateway{},
		jobs: []gwclient.ScheduledJobSummary{
			{
				ID:         "job-1",
				Name:       "cpu report",
				Schedule:   "every 1m",
				NextRunAt:  &now,
				LastStatus: "succeeded",
			},
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
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "/jobs",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Equal(t, "2", gw.lastUser)
	require.Equal(t, "1", gw.lastTarget)
	gw.mu.Unlock()

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Contains(t, bot.sent[0].Text, jobsMessageHeader)
	require.Contains(t, bot.sent[0].Text, "cpu report")
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_CommandJobsClear(t *testing.T) {
	t.Parallel()

	gw := &stubGatewayWithJobs{
		stubGateway: &stubGateway{},
		clearCnt:    3,
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
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "/jobs_clear",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Equal(t, "2", gw.lastUser)
	require.Equal(t, "1", gw.lastTarget)
	gw.mu.Unlock()

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(
		t,
		"Cleared 3 scheduled job(s) for this chat.",
		bot.sent[0].Text,
	)
	bot.mu.Unlock()
}
