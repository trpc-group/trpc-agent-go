//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/admin"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/conversationscope"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

type transcriptErrorSessionService struct {
	session.Service
	getErr     error
	getNil     bool
	listErr    error
	listByUser map[string][]*session.Session
}

func (s transcriptErrorSessionService) GetSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) (*session.Session, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.getNil {
		return nil, nil
	}
	return s.Service.GetSession(ctx, key, options...)
}

func (s transcriptErrorSessionService) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	options ...session.Option,
) ([]*session.Session, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	if s.listByUser != nil {
		return s.listByUser[userKey.UserID], nil
	}
	return s.Service.ListSessions(ctx, userKey, options...)
}

func TestOpenAdminBinding_AutoPortFallback(t *testing.T) {
	t.Parallel()

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = busy.Close()
	})

	binding, err := openAdminBinding(
		busy.Addr().String(),
		true,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = binding.listener.Close()
	})

	require.NotNil(t, binding.listener)
	require.NotEqual(t, busy.Addr().String(), binding.addr)
	require.True(t, binding.relocated)
	require.NotEmpty(t, binding.url)
}

func TestOpenAdminBinding_ExactPortFailure(t *testing.T) {
	t.Parallel()

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = busy.Close()
	})

	_, err = openAdminBinding(
		busy.Addr().String(),
		false,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "listen on")
}

func TestBuildAdminConfig_IncludesBrowserProviders(t *testing.T) {
	t.Parallel()

	cfg := buildAdminConfig(
		runOptions{
			AppName: "openclaw",
			ToolProviders: []pluginSpec{{
				Type: toolProviderBrowser,
				Name: "primary-browser",
				Config: yamlNode(t, `
default_profile: "openclaw"
server_url: "http://127.0.0.1:19790"
sandbox_server_url: "http://127.0.0.1:20790"
allow_loopback: true
profiles:
  - name: "openclaw"
    transport: "stdio"
    command: "npx"
  - name: "chrome"
    browser_server_url: "http://127.0.0.1:19790"
nodes:
  - id: "edge"
    server_url: "http://node.example:7777"
`),
			}},
		},
		agentTypeLLM,
		"instance-1",
		admin.LangfuseStatus{},
		"/tmp/state",
		"/tmp/debug",
		time.Unix(0, 0),
		nil,
		admin.Routes{},
		nil,
		nil,
		nil,
		nil,
		"127.0.0.1:8081",
		"http://127.0.0.1:8081",
		nil,
		nil,
		nil,
		nil,
	)

	require.Len(t, cfg.Browser.Providers, 1)
	require.NotNil(t, cfg.Skills)
	provider := cfg.Browser.Providers[0]
	require.Equal(t, "primary-browser", provider.Name)
	require.Equal(t, "openclaw", provider.DefaultProfile)
	require.Equal(t, "http://127.0.0.1:19790", provider.HostServerURL)
	require.Equal(
		t,
		"http://127.0.0.1:20790",
		provider.SandboxServerURL,
	)
	require.True(t, provider.AllowLoopback)
	require.Len(t, provider.Profiles, 2)
	require.Equal(
		t,
		"http://127.0.0.1:19790",
		provider.Profiles[1].BrowserServerURL,
	)
	require.Len(t, provider.Nodes, 1)
	require.Equal(t, "edge", provider.Nodes[0].ID)
}

func TestBuildBrowserAdminConfig_SkipsInvalidSpecs(t *testing.T) {
	t.Parallel()

	evaluateEnabled := true
	allowLoopback := true
	allowPrivateNet := true
	allowFileURLs := true

	cfg := buildBrowserAdminConfig(
		[]pluginSpec{
			{Type: "search", Name: "web"},
			{
				Type: toolProviderBrowser,
				Name: "broken",
				Config: yamlNode(t, `
unknown_field: true
`),
			},
			{
				Type: toolProviderBrowser,
				Name: "primary-browser",
				Config: yamlNode(t, `
default_profile: "openclaw"
server_url: "http://127.0.0.1:19790"
evaluate_enabled: true
allow_loopback: true
allow_private_networks: true
allow_file_urls: true
profiles:
  - name: "openclaw"
    transport: "stdio"
    command: "npx"
nodes:
  - id: "edge"
    server_url: "http://node.example:7777"
`),
			},
		},
		nil,
	)

	require.Len(t, cfg.Providers, 1)
	provider := cfg.Providers[0]
	require.Equal(t, "primary-browser", provider.Name)
	require.Equal(t, "openclaw", provider.DefaultProfile)
	require.Equal(t, "http://127.0.0.1:19790", provider.HostServerURL)
	require.Equal(t, evaluateEnabled, provider.EvaluateEnabled)
	require.Equal(t, allowLoopback, provider.AllowLoopback)
	require.Equal(t, allowPrivateNet, provider.AllowPrivateNet)
	require.Equal(t, allowFileURLs, provider.AllowFileURLs)
	require.Len(t, provider.Profiles, 1)
	require.Equal(t, "openclaw", provider.Profiles[0].Name)
	require.Len(t, provider.Nodes, 1)
	require.Equal(t, "edge", provider.Nodes[0].ID)
}

func TestBuildAdminConfig_IncludesIdentityAndChatsProviders(
	t *testing.T,
) {
	t.Parallel()

	stateDir := t.TempDir()
	ctx := context.Background()
	baseSessions := sessioninmemory.NewSessionService()
	wrappedSessions := conversationscope.WrapSessionService(baseSessions)
	_, err := wrappedSessions.CreateSession(
		ctx,
		session.Key{
			AppName:   "openclaw",
			UserID:    "chat-scope",
			SessionID: "sess-1",
		},
		nil,
	)
	require.NoError(t, err)
	sess, err := wrappedSessions.GetSession(
		ctx,
		session.Key{
			AppName:   "openclaw",
			UserID:    "chat-scope",
			SessionID: "sess-1",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	userEvt := event.NewResponseEvent(
		"inv",
		"user",
		&model.Response{
			Choices: []model.Choice{{
				Message: model.NewUserMessage("hello"),
			}},
		},
	)
	userEvt.Timestamp = time.Unix(1700000000, 0)
	require.NoError(t, wrappedSessions.AppendEvent(ctx, sess, userEvt))
	replyEvt := event.NewResponseEvent(
		"inv",
		"assistant",
		&model.Response{
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("hi"),
			}},
		},
	)
	replyEvt.Timestamp = time.Unix(1700000010, 0)
	require.NoError(t, wrappedSessions.AppendEvent(ctx, sess, replyEvt))

	cfg := buildAdminConfig(
		runOptions{
			AppName: "openclaw",
		},
		agentTypeLLM,
		"instance-1",
		admin.LangfuseStatus{},
		stateDir,
		filepath.Join(stateDir, "debug"),
		time.Unix(0, 0),
		nil,
		admin.Routes{},
		nil,
		nil,
		nil,
		nil,
		"127.0.0.1:8081",
		"http://127.0.0.1:8081",
		nil,
		nil,
		nil,
		wrappedSessions,
	)

	require.NotNil(t, cfg.Identity)
	require.NotNil(t, cfg.Chats)

	identityStatus, err := cfg.Identity.IdentityStatus()
	require.NoError(t, err)
	require.Equal(t, "openclaw", identityStatus.EffectiveName)
	require.Equal(t, "openclaw", identityStatus.RuntimeProduct)
	require.Equal(
		t,
		filepath.Join(stateDir, adminIdentityFileName),
		identityStatus.SourcePath,
	)
	require.Equal(
		t,
		adminIdentityFallbackRuntime,
		identityStatus.FallbackSource,
	)

	err = cfg.Identity.SaveAssistantName("  Nora   Claw  ")
	require.NoError(t, err)

	identityStatus, err = cfg.Identity.IdentityStatus()
	require.NoError(t, err)
	require.Equal(t, "Nora Claw", identityStatus.ConfiguredName)
	require.Equal(t, "Nora Claw", identityStatus.EffectiveName)
	require.Empty(t, identityStatus.FallbackSource)

	chatsStatus, err := cfg.Chats.ChatsStatus()
	require.NoError(t, err)
	require.True(t, chatsStatus.Enabled)
	require.Equal(t, "Nora Claw", chatsStatus.GlobalAssistantName)
	require.Equal(t, "openclaw", chatsStatus.RuntimeAssistantName)
	require.Equal(
		t,
		adminDefaultNameSourceFile,
		chatsStatus.GlobalAssistantSource,
	)
	require.Contains(t, chatsStatus.ChatOverrideHelp, "default name")
	require.Len(t, chatsStatus.Chats, 1)
	require.Equal(t, "chat-scope", chatsStatus.Chats[0].BaseSessionID)
	require.Equal(t, "sess-1", chatsStatus.Chats[0].CurrentSessionID)

	detailProvider, ok := cfg.Chats.(admin.ChatDetailProvider)
	require.True(t, ok)
	detail, err := detailProvider.ChatDetail("chat-scope")
	require.NoError(t, err)
	require.Len(t, detail.Transcript, 1)
	require.Len(t, detail.Transcript[0].Turns, 2)
	require.Equal(t, "hello", detail.Transcript[0].Turns[0].Text)
	require.Equal(t, "hi", detail.Transcript[0].Turns[1].Text)

	cfg = buildAdminConfig(
		runOptions{
			AppName: "openclaw",
		},
		agentTypeLLM,
		"instance-1",
		admin.LangfuseStatus{},
		stateDir,
		filepath.Join(stateDir, "debug"),
		time.Unix(0, 0),
		nil,
		admin.Routes{},
		nil,
		nil,
		nil,
		nil,
		"127.0.0.1:8081",
		"http://127.0.0.1:8081",
		nil,
		nil,
		nil,
		transcriptErrorSessionService{
			Service: wrappedSessions,
			getErr:  errors.New("transcript boom"),
		},
	)
	detailProvider, ok = cfg.Chats.(admin.ChatDetailProvider)
	require.True(t, ok)
	_, err = detailProvider.ChatDetail("chat-scope")
	require.Error(t, err)
	require.Contains(t, err.Error(), "transcript boom")
}

func TestNormalizeAdminAssistantName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", normalizeAdminAssistantName("   "))
	require.Equal(
		t,
		"Nora Claw",
		normalizeAdminAssistantName(" “Nora   Claw” "),
	)

	raw := "1234567890123456789012345678901234567890"
	got := normalizeAdminAssistantName(raw)
	require.Len(t, []rune(got), adminAssistantNameMaxRunes)
	require.Equal(t, raw[:adminAssistantNameMaxRunes], got)
}

func TestAdminIdentityHelpers(t *testing.T) {
	t.Parallel()

	require.Nil(t, buildAdminChatsProvider(nil, "", nil))
	require.Equal(t, appName, defaultAdminRuntimeProduct(" "))
	require.Equal(
		t,
		"",
		normalizeAdminAssistantName(" 【】 "),
	)

	name, err := readAdminAssistantName("")
	require.NoError(t, err)
	require.Empty(t, name)

	name, err = readAdminAssistantName(
		filepath.Join(t.TempDir(), "IDENTITY.md"),
	)
	require.NoError(t, err)
	require.Empty(t, name)

	err = writeAdminAssistantName("", "Nora")
	require.Error(t, err)

	var nilIdentity *adminIdentityProvider
	status, err := nilIdentity.IdentityStatus()
	require.NoError(t, err)
	require.Equal(t, admin.IdentityStatus{}, status)
	require.Error(t, nilIdentity.SaveAssistantName("Nora"))

	var nilChats *adminChatsProvider
	chatsStatus, err := nilChats.ChatsStatus()
	require.NoError(t, err)
	require.Equal(t, admin.ChatsStatus{}, chatsStatus)

	_, err = nilChats.ChatDetail("chat-scope")
	require.Error(t, err)
	require.Contains(t, err.Error(), "chat provider is unavailable")
}

func TestAdminIdentityProvider_FallbackAndErrors(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	identity := buildAdminIdentityProvider(stateDir, "runtime-product")

	status, err := identity.IdentityStatus()
	require.NoError(t, err)
	require.Equal(t, "runtime-product", status.EffectiveName)
	require.Empty(t, status.ConfiguredName)
	require.Equal(t, adminIdentityFallbackRuntime, status.FallbackSource)
	require.Equal(
		t,
		adminDefaultNameSourceApp,
		identityDefaultNameSource(status),
	)

	err = identity.SaveAssistantName("")
	require.NoError(t, err)

	status, err = identity.IdentityStatus()
	require.NoError(t, err)
	require.Equal(t, "runtime-product", status.EffectiveName)
	require.Empty(t, status.ConfiguredName)

	badIdentity := &adminIdentityProvider{
		filePath:       stateDir,
		runtimeProduct: "runtime-product",
	}
	_, err = badIdentity.IdentityStatus()
	require.Error(t, err)

	chats := buildAdminChatsProvider(badIdentity, "openclaw", nil)
	_, err = chats.ChatsStatus()
	require.Error(t, err)
}

func TestAdminChatsProvider_ViewsAndTranscriptHelpers(t *testing.T) {
	t.Parallel()

	baseSvc := sessioninmemory.NewSessionService()
	now := time.Unix(1700000200, 0).UTC()
	tie := time.Unix(1700000100, 0).UTC()

	wrapper := transcriptErrorSessionService{
		Service: baseSvc,
		listByUser: map[string][]*session.Session{
			"chat-new": {{
				ID:        "chat-new:sess",
				UserID:    "chat-new",
				UpdatedAt: now,
			}},
			"chat-a": {{
				ID:        "chat-a:sess",
				UserID:    "chat-a",
				CreatedAt: tie,
			}},
			"chat-b": {{
				ID:        "chat-b:sess",
				UserID:    "chat-b",
				CreatedAt: tie,
			}},
			"chat-zero": {{
				ID:     "chat-zero:sess",
				UserID: "chat-zero",
			}},
			"chat-filter": {
				nil,
				{
					UserID:    "chat-filter",
					UpdatedAt: now,
				},
				{
					ID:        "cron:job-1:123",
					UserID:    "chat-filter",
					UpdatedAt: now,
				},
				{
					ID:        "chat-filter:old",
					UserID:    "chat-filter",
					CreatedAt: tie,
				},
				{
					ID:        "chat-filter:new",
					UserID:    "chat-filter",
					UpdatedAt: now,
				},
			},
			"chat-skip": {{
				ID:     "cron:run:demo",
				UserID: "chat-skip",
			}},
		},
	}

	ctx := context.Background()
	for _, scope := range []string{
		"chat-zero",
		"chat-b",
		"chat-new",
		"chat-a",
		"chat-skip",
	} {
		require.NoError(
			t,
			conversationscope.RememberIndexedStorageScope(
				ctx,
				wrapper,
				"openclaw",
				scope,
			),
		)
	}

	provider := buildAdminChatsProvider(
		buildAdminIdentityProvider(t.TempDir(), "runtime-product"),
		"openclaw",
		wrapper,
	)
	require.NotNil(t, provider)

	chats, err := provider.chatViews("Nora", adminDefaultNameSourceFile)
	require.NoError(t, err)
	require.Len(t, chats, 4)
	require.Equal(t, "chat-new", chats[0].BaseSessionID)
	require.Equal(t, "chat-a", chats[1].BaseSessionID)
	require.Equal(t, "chat-b", chats[2].BaseSessionID)
	require.Equal(t, "chat-zero", chats[3].BaseSessionID)
	require.Equal(t, "Nora", chats[0].EffectiveAssistant)
	require.Equal(t, adminChatKindTracked, chats[0].Kind)
	require.Equal(t, adminChatKindLabel, chats[0].KindLabel)

	emptySessions, err := provider.chatSessions("")
	require.NoError(t, err)
	require.Nil(t, emptySessions)

	provider = buildAdminChatsProvider(
		buildAdminIdentityProvider(t.TempDir(), "runtime-product"),
		"",
		wrapper,
	)
	emptySessions, err = provider.chatSessions("chat-new")
	require.NoError(t, err)
	require.Nil(t, emptySessions)

	filteredSessions, err := buildAdminChatsProvider(
		buildAdminIdentityProvider(t.TempDir(), "runtime-product"),
		"openclaw",
		wrapper,
	).chatSessions("chat-filter")
	require.NoError(t, err)
	require.Len(t, filteredSessions, 2)
	require.Equal(t, "chat-filter:new", filteredSessions[0].ID)
	require.Equal(t, "chat-filter:old", filteredSessions[1].ID)

	errorProvider := buildAdminChatsProvider(
		buildAdminIdentityProvider(t.TempDir(), "runtime-product"),
		"openclaw",
		transcriptErrorSessionService{
			Service: baseSvc,
			listErr: errors.New("list boom"),
		},
	)
	_, err = errorProvider.chatSessions("chat-new")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list boom")
	_, err = errorProvider.chatViews("Nora", adminDefaultNameSourceFile)
	require.Error(t, err)
	require.Contains(t, err.Error(), "list boom")
	_, err = provider.ChatDetail("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "chat_id is required")
	_, err = buildAdminChatsProvider(
		buildAdminIdentityProvider(t.TempDir(), "runtime-product"),
		"openclaw",
		baseSvc,
	).ChatDetail("chat-missing")
	require.Error(t, err)
	require.Contains(t, err.Error(), "tracked chat not found")

	emptyViews, err := buildAdminChatsProvider(
		buildAdminIdentityProvider(t.TempDir(), "runtime-product"),
		"openclaw",
		baseSvc,
	).chatViews("Nora", adminDefaultNameSourceFile)
	require.NoError(t, err)
	require.Nil(t, emptyViews)

	chat := buildAdminTrackedChatView(
		"chat-empty",
		"Nora",
		adminDefaultNameSourceFile,
		nil,
	)
	require.Equal(t, "chat-empty", chat.BaseSessionID)
	require.Empty(t, chat.CurrentSessionID)
	require.True(t, chat.LastActivity.IsZero())
	require.Empty(t, chat.History)
	require.Zero(t, chat.HistoryTotalCount)
	require.False(t, chat.HistoryTruncated)

	manySessions := make([]*session.Session, 0, adminChatHistorySessionLimit+2)
	for i := 0; i < adminChatHistorySessionLimit+2; i++ {
		manySessions = append(manySessions, &session.Session{
			ID:        fmt.Sprintf("chat-many:%02d", i),
			UpdatedAt: time.Unix(int64(1700001000+i), 0).UTC(),
		})
	}
	chat = buildAdminTrackedChatView(
		"chat-many",
		"Nora",
		adminDefaultNameSourceFile,
		manySessions,
	)
	require.Len(t, chat.History, adminChatHistorySessionLimit)
	require.Equal(
		t,
		adminChatHistorySessionLimit+2,
		chat.HistoryTotalCount,
	)
	require.True(t, chat.HistoryTruncated)
	visibleHistory := 0
	for _, item := range chat.History {
		if item.Visible {
			visibleHistory++
		}
	}
	require.Equal(t, adminChatHistoryVisibleCount, visibleHistory)

	require.True(t, sessionActivityTime(nil).IsZero())
	require.Equal(t, time.Time{}, timeZero())
}

func TestAdminChatTranscriptHelpers_EdgeCases(t *testing.T) {
	t.Parallel()

	baseSvc := sessioninmemory.NewSessionService()
	ctx := context.Background()
	const (
		appName = "openclaw"
		baseID  = "chat-scope"
	)

	current, err := baseSvc.CreateSession(
		ctx,
		session.Key{
			AppName:   appName,
			UserID:    baseID,
			SessionID: "sess-current",
		},
		nil,
	)
	require.NoError(t, err)
	current.CreatedAt = time.Unix(1700000000, 0).UTC()
	current.UpdatedAt = time.Unix(1700000300, 0).UTC()

	longText := strings.Repeat("x", adminChatTranscriptTextLimit+5)
	for i := 0; i < adminChatTranscriptTurnLimit+1; i++ {
		evt := event.NewResponseEvent(
			"inv",
			"user",
			&model.Response{
				Choices: []model.Choice{{
					Message: model.NewUserMessage(
						fmt.Sprintf("turn-%02d", i),
					),
				}},
			},
		)
		evt.Timestamp = time.Unix(int64(1700000300+i), 0).UTC()
		if i == adminChatTranscriptTurnLimit {
			evt = event.NewResponseEvent(
				"inv",
				"user",
				&model.Response{
					Choices: []model.Choice{{
						Message: model.NewUserMessage(longText),
					}},
				},
			)
			evt.Timestamp = time.Unix(1700000500, 0).UTC()
		}
		require.NoError(t, baseSvc.AppendEvent(ctx, current, evt))
	}

	middle, err := baseSvc.CreateSession(
		ctx,
		session.Key{
			AppName:   appName,
			UserID:    baseID,
			SessionID: "sess-middle",
		},
		nil,
	)
	require.NoError(t, err)
	middle.CreatedAt = time.Unix(1700000100, 0).UTC()
	middle.UpdatedAt = time.Unix(1700000200, 0).UTC()
	require.NoError(
		t,
		baseSvc.AppendEvent(
			ctx,
			middle,
			event.NewResponseEvent(
				"inv",
				"user",
				&model.Response{
					Choices: []model.Choice{{
						Message: model.NewUserMessage("middle"),
					}},
				},
			),
		),
	)

	older, err := baseSvc.CreateSession(
		ctx,
		session.Key{
			AppName:   appName,
			UserID:    baseID,
			SessionID: "sess-older",
		},
		nil,
	)
	require.NoError(t, err)
	older.CreatedAt = time.Unix(1700000050, 0).UTC()
	require.NoError(
		t,
		baseSvc.AppendEvent(
			ctx,
			older,
			event.NewResponseEvent(
				"inv",
				"user",
				&model.Response{
					Choices: []model.Choice{{
						Message: model.NewUserMessage("older"),
					}},
				},
			),
		),
	)

	oldest, err := baseSvc.CreateSession(
		ctx,
		session.Key{
			AppName:   appName,
			UserID:    baseID,
			SessionID: "sess-oldest",
		},
		nil,
	)
	require.NoError(t, err)
	oldest.CreatedAt = time.Unix(1700000001, 0).UTC()
	require.NoError(
		t,
		baseSvc.AppendEvent(
			ctx,
			oldest,
			event.NewResponseEvent(
				"inv",
				"user",
				&model.Response{
					Choices: []model.Choice{{
						Message: model.NewUserMessage("oldest"),
					}},
				},
			),
		),
	)

	sessions := []*session.Session{current, middle, older, oldest}
	for i := 0; i < adminChatTranscriptSessionLimit; i++ {
		sessionID := fmt.Sprintf("sess-extra-%02d", i)
		extra, err := baseSvc.CreateSession(
			ctx,
			session.Key{
				AppName:   appName,
				UserID:    baseID,
				SessionID: sessionID,
			},
			nil,
		)
		require.NoError(t, err)
		extra.CreatedAt = time.Unix(
			1700000000-int64(i+10),
			0,
		).UTC()
		require.NoError(
			t,
			baseSvc.AppendEvent(
				ctx,
				extra,
				event.NewResponseEvent(
					"inv",
					"user",
					&model.Response{
						Choices: []model.Choice{{
							Message: model.NewUserMessage(sessionID),
						}},
					},
				),
			),
		)
		sessions = append(sessions, extra)
	}

	transcript, truncated, err := buildAdminChatTranscript(
		"",
		baseSvc,
		baseID,
		sessions,
	)
	require.NoError(t, err)
	require.Nil(t, transcript)
	require.False(t, truncated)

	transcript, truncated, err = buildAdminChatTranscript(
		appName,
		nil,
		baseID,
		sessions,
	)
	require.NoError(t, err)
	require.Nil(t, transcript)
	require.False(t, truncated)

	transcript, truncated, err = buildAdminChatTranscript(
		appName,
		baseSvc,
		"",
		sessions,
	)
	require.NoError(t, err)
	require.Nil(t, transcript)
	require.False(t, truncated)

	transcript, truncated, err = buildAdminChatTranscript(
		appName,
		baseSvc,
		baseID,
		nil,
	)
	require.NoError(t, err)
	require.Nil(t, transcript)
	require.False(t, truncated)

	transcript, truncated, err = buildAdminChatTranscript(
		appName,
		baseSvc,
		baseID,
		sessions,
	)
	require.NoError(t, err)
	require.True(t, truncated)
	require.Len(t, transcript, adminChatTranscriptSessionLimit)
	require.True(t, transcript[0].Current)
	require.True(t, transcript[0].Visible)
	require.True(t, transcript[1].Visible)
	require.False(t, transcript[2].Visible)
	require.True(t, transcript[0].Truncated)
	require.Len(t, transcript[0].Turns, adminChatTranscriptTurnLimit)
	visibleTurns := 0
	for _, turn := range transcript[0].Turns {
		if turn.Visible {
			visibleTurns++
		}
	}
	require.Equal(t, adminChatTranscriptTurnVisible, visibleTurns)
	require.Equal(
		t,
		"sess-current",
		transcript[0].SessionID,
	)
	require.True(
		t,
		strings.HasSuffix(
			transcript[0].Turns[len(transcript[0].Turns)-1].Text,
			"...",
		),
	)
	require.Equal(t, "sess-middle", transcript[1].SessionID)
	require.Equal(t, "sess-older", transcript[2].SessionID)

	view, ok, err := buildAdminChatTranscriptView(
		appName,
		baseSvc,
		baseID,
		"sess-current",
		nil,
	)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, admin.ChatTranscriptView{}, view)

	view, ok, err = buildAdminChatTranscriptView(
		appName,
		baseSvc,
		baseID,
		"sess-current",
		&session.Session{},
	)
	require.NoError(t, err)
	require.False(t, ok)

	nilSvc := transcriptErrorSessionService{
		Service: baseSvc,
		getNil:  true,
	}
	_, ok, err = buildAdminChatTranscriptView(
		appName,
		nilSvc,
		baseID,
		"sess-current",
		&session.Session{ID: "sess-missing"},
	)
	require.Error(t, err)
	require.False(t, ok)
	require.Contains(t, err.Error(), "session not found")

	blank, err := baseSvc.CreateSession(
		ctx,
		session.Key{
			AppName:   appName,
			UserID:    baseID,
			SessionID: "sess-blank",
		},
		nil,
	)
	require.NoError(t, err)
	blank.CreatedAt = time.Unix(1700000400, 0).UTC()
	require.NoError(
		t,
		baseSvc.AppendEvent(
			ctx,
			blank,
			event.NewResponseEvent(
				"inv",
				"user",
				&model.Response{
					Choices: []model.Choice{{
						Message: model.NewUserMessage(" "),
					}},
				},
			),
		),
	)

	blankView, ok, err := buildAdminChatTranscriptView(
		appName,
		baseSvc,
		baseID,
		"sess-current",
		blank,
	)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, admin.ChatTranscriptView{}, blankView)

	require.Empty(t, trimAdminChatTranscriptText("   "))
	require.Equal(
		t,
		"hello",
		trimAdminChatTranscriptText("  hello  "),
	)
	require.True(
		t,
		strings.HasSuffix(trimAdminChatTranscriptText(longText), "..."),
	)
}
