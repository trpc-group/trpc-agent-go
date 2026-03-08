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
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/persona"
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

type stubGatewayWithPersona struct {
	*stubGateway

	presets      []persona.Preset
	current      persona.Preset
	setErr       error
	getErr       error
	lastScopeKey string
	lastPresetID string
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

func (g *stubGatewayWithPersona) ListPresetPersonas() []persona.Preset {
	out := make([]persona.Preset, len(g.presets))
	copy(out, g.presets)
	return out
}

func (g *stubGatewayWithPersona) GetPresetPersona(
	_ context.Context,
	scopeKey string,
) (persona.Preset, error) {
	g.lastScopeKey = scopeKey
	if g.getErr != nil {
		return persona.Preset{}, g.getErr
	}
	return g.current, nil
}

func (g *stubGatewayWithPersona) SetPresetPersona(
	_ context.Context,
	scopeKey string,
	presetID string,
) (persona.Preset, error) {
	g.lastScopeKey = scopeKey
	g.lastPresetID = presetID
	if g.setErr != nil {
		return persona.Preset{}, g.setErr
	}
	for _, preset := range g.presets {
		if preset.ID == presetID {
			g.current = preset
			return preset, nil
		}
	}
	return persona.Preset{}, persona.ErrUnknownPreset
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

func TestChannel_HandleMessage_CommandPersonas(t *testing.T) {
	t.Parallel()

	gw := &stubGatewayWithPersona{
		stubGateway: &stubGateway{},
		presets:     persona.List(),
		current:     persona.DefaultPreset(),
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
		MessageID: 6,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "/personas",
	})
	require.NoError(t, err)

	require.Equal(t, "telegram:dm:2", gw.lastScopeKey)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Contains(t, bot.sent[0].Text, personaMessageHeader)
	require.Contains(t, bot.sent[0].Text, "girlfriend")
	require.Contains(t, bot.sent[0].Text, "(active)")
	require.NotNil(t, bot.sent[0].ReplyMarkup)
	require.NotEmpty(
		t,
		bot.sent[0].ReplyMarkup.InlineKeyboard,
	)
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_CommandPersona_Set(t *testing.T) {
	t.Parallel()

	gw := &stubGatewayWithPersona{
		stubGateway: &stubGateway{},
		presets:     persona.List(),
		current:     persona.DefaultPreset(),
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
		MessageID: 7,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "/persona girlfriend",
	})
	require.NoError(t, err)

	require.Equal(t, "telegram:dm:2", gw.lastScopeKey)
	require.Equal(t, persona.PresetGirlfriend, gw.lastPresetID)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(
		t,
		"Persona set to girlfriend.",
		bot.sent[0].Text,
	)
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_CommandPersona_Unknown(t *testing.T) {
	t.Parallel()

	gw := &stubGatewayWithPersona{
		stubGateway: &stubGateway{},
		presets:     persona.List(),
		current:     persona.DefaultPreset(),
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
		MessageID: 8,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "/persona missing",
	})
	require.NoError(t, err)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Contains(t, bot.sent[0].Text, "Unknown persona preset.")
	require.Contains(t, bot.sent[0].Text, "personas list command")
	bot.mu.Unlock()
}

func TestChannel_HandleCallbackQuery_Persona(t *testing.T) {
	t.Parallel()

	gw := &stubGatewayWithPersona{
		stubGateway: &stubGateway{},
		presets:     persona.List(),
		current:     persona.DefaultPreset(),
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

	err = ch.handleCallbackQuery(context.Background(), tgapi.CallbackQuery{
		ID:   "cb-1",
		From: &tgapi.User{ID: 2},
		Message: &tgapi.Message{
			MessageID: 17,
			Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		},
		Data: personaCallbackPrefix + persona.PresetCoach,
	})
	require.NoError(t, err)

	require.Equal(t, "telegram:dm:2", gw.lastScopeKey)
	require.Equal(t, persona.PresetCoach, gw.lastPresetID)

	bot.mu.Lock()
	require.Len(t, bot.edits, 1)
	require.Contains(t, bot.edits[0].Text, "coach")
	require.NotNil(t, bot.edits[0].ReplyMarkup)
	require.Len(t, bot.callbacks, 1)
	require.Equal(
		t,
		"cb-1",
		bot.callbacks[0].CallbackQueryID,
	)
	require.Equal(
		t,
		"Persona set to coach.",
		bot.callbacks[0].Text,
	)
	bot.mu.Unlock()
}
