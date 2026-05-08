//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package recall

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

type mockSessionService struct {
	session.Service
	searchResults  []session.EventSearchResult
	searchErr      error
	searchFunc     func(session.EventSearchRequest) ([]session.EventSearchResult, error)
	searchReqs     []session.EventSearchRequest
	getSessionFunc func(session.Key, ...session.Option) (*session.Session, error)
	lastSearchReq  session.EventSearchRequest
	window         *session.EventWindow
	windowErr      error
	windowFunc     func(session.EventWindowRequest) (*session.EventWindow, error)
	lastWindowReq  session.EventWindowRequest
}

func (m *mockSessionService) SearchEvents(
	_ context.Context,
	req session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	m.searchReqs = append(m.searchReqs, req)
	m.lastSearchReq = req
	if m.searchFunc != nil {
		return m.searchFunc(req)
	}
	return m.searchResults, m.searchErr
}

func (m *mockSessionService) GetSession(
	_ context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	if m.getSessionFunc != nil {
		return m.getSessionFunc(key, opts...)
	}
	return m.Service.GetSession(context.Background(), key, opts...)
}

func (m *mockSessionService) GetEventWindow(
	_ context.Context,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	m.lastWindowReq = req
	if m.windowFunc != nil {
		return m.windowFunc(req)
	}
	return m.window, m.windowErr
}

func TestSearchTool_CurrentHidden(t *testing.T) {
	summaryUpdatedAt := time.Date(2025, 4, 7, 10, 0, 0, 0, time.UTC)
	createdAt := summaryUpdatedAt.Add(-time.Minute)
	svc := &mockSessionService{
		Service: sessioninmemory.NewSessionService(),
		searchResults: []session.EventSearchResult{
			{
				SessionKey: session.Key{
					AppName:   "app",
					UserID:    "user",
					SessionID: "sess",
				},
				EventCreatedAt: createdAt,
				Event: event.Event{
					ID: "evt-1",
					Response: &model.Response{
						Choices: []model.Choice{
							{
								Message: model.Message{
									Role:    model.RoleAssistant,
									Content: "remembered detail",
								},
							},
						},
					},
				},
				Role:  model.RoleAssistant,
				Text:  "assistant: remembered detail",
				Score: 0.91,
			},
		},
		window: &session.EventWindow{
			SessionKey: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			AnchorEventID: "evt-1",
			Entries: []session.EventWindowEntry{
				{
					Event: event.Event{
						ID: "evt-0",
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										Role:    model.RoleUser,
										Content: "what happened earlier?",
									},
								},
							},
						},
					},
					CreatedAt: createdAt.Add(-time.Minute),
				},
				{
					Event: event.Event{
						ID: "evt-1",
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										Role:    model.RoleAssistant,
										Content: "remembered detail",
									},
								},
							},
						},
					},
					CreatedAt: createdAt,
				},
				{
					Event: event.Event{
						ID: "evt-2",
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										Role:    model.RoleUser,
										Content: "thanks, that helps",
									},
								},
							},
						},
					},
					CreatedAt: createdAt.Add(time.Minute),
				},
			},
		},
	}

	inv := &agent.Invocation{
		Session: session.NewSession(
			"app",
			"user",
			"sess",
			session.WithSessionSummaries(map[string]*session.Summary{
				"": {
					Summary:   "summary",
					UpdatedAt: summaryUpdatedAt,
				},
			}),
		),
		SessionService: svc,
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	args, err := json.Marshal(&SearchSessionRequest{
		Query: "remembered detail",
		Scope: ScopeCurrentHidden,
	})
	require.NoError(t, err)

	result, err := NewSearchTool().Call(ctx, args)
	require.NoError(t, err)

	resp, ok := result.(*SearchSessionResponse)
	require.True(t, ok)
	require.Len(t, resp.Results, 1)
	assert.Equal(t, "evt-1", resp.Results[0].EventID)
	assert.Equal(t, ScopeCurrentHidden, resp.Results[0].Scope)
	assert.Equal(
		t,
		"user: what happened earlier?\n[match] assistant: remembered detail\nuser: thanks, that helps",
		resp.Results[0].Snippet,
	)
	require.Len(t, resp.Results[0].Context, 3)
	assert.Equal(t, "evt-0", resp.Results[0].Context[0].EventID)
	assert.Equal(t, model.RoleAssistant, resp.Results[0].Context[1].Role)
	assert.Equal(t, "remembered detail", resp.Results[0].Context[1].Content)
	assert.Equal(t, session.SearchModeHybrid, svc.lastSearchReq.SearchMode)
	assert.Equal(t, []string{"sess"}, svc.lastSearchReq.SessionIDs)
	require.NotNil(t, svc.lastSearchReq.CreatedBefore)
	assert.Equal(t, summaryUpdatedAt, *svc.lastSearchReq.CreatedBefore)
	assert.Equal(t, "evt-1", svc.lastWindowReq.AnchorEventID)
	assert.Equal(t, 2, svc.lastWindowReq.Before)
	assert.Equal(t, 2, svc.lastWindowReq.After)
	assert.ElementsMatch(
		t,
		[]model.Role{model.RoleUser, model.RoleAssistant, model.RoleTool},
		svc.lastSearchReq.Roles,
	)
}

func TestSearchTool_CurrentHiddenUsesLastIncludedTimestamp(
	t *testing.T,
) {
	summaryUpdatedAt := time.Date(2025, 4, 7, 10, 0, 0, 0, time.UTC)
	lastIncludedAt := summaryUpdatedAt.Add(-3 * time.Minute)
	svc := &mockSessionService{
		Service: sessioninmemory.NewSessionService(),
		searchResults: []session.EventSearchResult{
			{
				SessionKey: session.Key{
					AppName:   "app",
					UserID:    "user",
					SessionID: "sess",
				},
				EventCreatedAt: lastIncludedAt.Add(-time.Minute),
				Event: event.Event{
					ID: "evt-1",
					Response: &model.Response{
						Choices: []model.Choice{{
							Message: model.Message{
								Role:    model.RoleAssistant,
								Content: "remembered detail",
							},
						}},
					},
				},
				Role:  model.RoleAssistant,
				Text:  "remembered detail",
				Score: 0.91,
			},
		},
	}

	sess := session.NewSession(
		"app",
		"user",
		"sess",
		session.WithSessionSummaries(map[string]*session.Summary{
			"": {
				Summary:   "summary",
				UpdatedAt: summaryUpdatedAt,
			},
		}),
	)
	sess.SetState(summaryLastIncludedTsKey, []byte(lastIncludedAt.Format(time.RFC3339Nano)))

	inv := &agent.Invocation{
		Session:        sess,
		SessionService: svc,
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	args, err := json.Marshal(&SearchSessionRequest{
		Query: "remembered detail",
		Scope: ScopeCurrentHidden,
	})
	require.NoError(t, err)

	result, err := NewSearchTool().Call(ctx, args)
	require.NoError(t, err)

	resp, ok := result.(*SearchSessionResponse)
	require.True(t, ok)
	require.Len(t, resp.Results, 1)
	require.NotNil(t, svc.lastSearchReq.CreatedBefore)
	assert.Equal(t, lastIncludedAt, *svc.lastSearchReq.CreatedBefore)
}

func TestSearchTool_FallbackQuery(t *testing.T) {
	const originalQuery = "What did Alice say about budget planning for April?"
	const fallbackQuery = "alice budget planning april"

	createdAt := time.Date(2025, 4, 7, 10, 30, 0, 0, time.UTC)
	svc := &mockSessionService{
		Service: sessioninmemory.NewSessionService(),
		searchFunc: func(
			req session.EventSearchRequest,
		) ([]session.EventSearchResult, error) {
			switch req.Query {
			case originalQuery:
				return nil, nil
			case fallbackQuery:
				return []session.EventSearchResult{
					{
						SessionKey: session.Key{
							AppName:   "app",
							UserID:    "user",
							SessionID: "sess-old",
						},
						EventCreatedAt: createdAt,
						Event: event.Event{
							ID: "evt-budget",
							Response: &model.Response{
								Choices: []model.Choice{
									{
										Message: model.Message{
											Role:    model.RoleAssistant,
											Content: "Alice said the April budget review had to move up by one week.",
										},
									},
								},
							},
						},
						Role:  model.RoleAssistant,
						Score: 0.88,
					},
				}, nil
			default:
				return nil, nil
			}
		},
	}

	inv := &agent.Invocation{
		Session:        session.NewSession("app", "user", "sess-now"),
		SessionService: svc,
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	args, err := json.Marshal(&SearchSessionRequest{
		Query: originalQuery,
		Scope: ScopeOtherSessions,
	})
	require.NoError(t, err)

	result, err := NewSearchTool().Call(ctx, args)
	require.NoError(t, err)

	resp, ok := result.(*SearchSessionResponse)
	require.True(t, ok)
	require.Len(t, resp.Results, 1)
	assert.Equal(t, fallbackQuery, svc.searchReqs[1].Query)
	assert.Equal(t, "sess-old", resp.Results[0].SessionID)
	assert.Len(t, svc.searchReqs, 2)
}

func TestSearchTool_CurrentHiddenSessionScanFallback(t *testing.T) {
	summaryUpdatedAt := time.Date(2025, 4, 7, 10, 0, 0, 0, time.UTC)
	createdAt := summaryUpdatedAt.Add(-2 * time.Minute)

	hiddenSession := session.NewSession("app", "user", "sess")
	hiddenSession.Events = []event.Event{
		{
			ID:        "evt-before",
			Timestamp: createdAt,
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "[Turn 031] David Hopkins: I agree the new Act will put more pressure on schools.",
					},
				}},
			},
		},
		{
			ID:        "evt-after",
			Timestamp: summaryUpdatedAt.Add(time.Minute),
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "[Turn 220] Later speaker: unrelated follow-up",
					},
				}},
			},
		},
	}

	svc := &mockSessionService{
		Service: sessioninmemory.NewSessionService(),
		searchFunc: func(
			req session.EventSearchRequest,
		) ([]session.EventSearchResult, error) {
			return nil, nil
		},
		getSessionFunc: func(
			key session.Key,
			_ ...session.Option,
		) (*session.Session, error) {
			return hiddenSession, nil
		},
		window: &session.EventWindow{
			SessionKey: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			AnchorEventID: "evt-before",
			Entries: []session.EventWindowEntry{
				{
					Event:     hiddenSession.Events[0],
					CreatedAt: createdAt,
				},
			},
		},
	}

	inv := &agent.Invocation{
		Session: session.NewSession(
			"app",
			"user",
			"sess",
			session.WithSessionSummaries(map[string]*session.Summary{
				"": {
					Summary:   "summary",
					UpdatedAt: summaryUpdatedAt,
				},
			}),
		),
		SessionService: svc,
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	args, err := json.Marshal(&SearchSessionRequest{
		Query: "What did David Hopkins think of the new Act?",
		Scope: ScopeCurrentHidden,
	})
	require.NoError(t, err)

	result, err := NewSearchTool().Call(ctx, args)
	require.NoError(t, err)

	resp, ok := result.(*SearchSessionResponse)
	require.True(t, ok)
	require.Len(t, resp.Results, 1)
	assert.Equal(t, "evt-before", resp.Results[0].EventID)
	assert.Contains(t, resp.Results[0].Snippet, "David Hopkins")
	assert.GreaterOrEqual(t, len(svc.searchReqs), 2)
}

func TestSearchTool_BroadensSparsePrimaryResults(t *testing.T) {
	const originalQuery = "What did David Hopkins think of the new Act?"
	const fallbackQuery = "david hopkins think new act"

	svc := &mockSessionService{
		Service: sessioninmemory.NewSessionService(),
		searchFunc: func(
			req session.EventSearchRequest,
		) ([]session.EventSearchResult, error) {
			switch req.Query {
			case originalQuery:
				return []session.EventSearchResult{
					{
						SessionKey: session.Key{
							AppName:   "app",
							UserID:    "user",
							SessionID: "sess-a",
						},
						Event: event.Event{ID: "evt-a"},
						Score: 0.60,
					},
				}, nil
			case fallbackQuery:
				return []session.EventSearchResult{
					{
						SessionKey: session.Key{
							AppName:   "app",
							UserID:    "user",
							SessionID: "sess-b",
						},
						Event: event.Event{ID: "evt-b"},
						Score: 0.83,
					},
				}, nil
			default:
				return nil, nil
			}
		},
	}

	inv := &agent.Invocation{
		Session:        session.NewSession("app", "user", "sess-now"),
		SessionService: svc,
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	args, err := json.Marshal(&SearchSessionRequest{
		Query: originalQuery,
		Scope: ScopeOtherSessions,
	})
	require.NoError(t, err)

	result, err := NewSearchTool().Call(ctx, args)
	require.NoError(t, err)

	resp, ok := result.(*SearchSessionResponse)
	require.True(t, ok)
	require.Len(t, resp.Results, 2)
	assert.Equal(t, fallbackQuery, svc.searchReqs[1].Query)
	assert.Equal(t, "evt-b", resp.Results[0].EventID)
	assert.Equal(t, "evt-a", resp.Results[1].EventID)
}

func TestSearchTool_CurrentSessionIncludesToolResults(
	t *testing.T,
) {
	createdAt := time.Date(2025, 4, 7, 10, 30, 0, 0, time.UTC)
	svc := &mockSessionService{
		Service: sessioninmemory.NewSessionService(),
		searchResults: []session.EventSearchResult{
			{
				SessionKey: session.Key{
					AppName:   "app",
					UserID:    "user",
					SessionID: "sess",
				},
				EventCreatedAt: createdAt,
				Event: event.Event{
					ID: "evt-tool",
					Response: &model.Response{
						Choices: []model.Choice{{
							Message: model.Message{
								Role:     model.RoleTool,
								ToolID:   "call-1",
								ToolName: "web_fetch",
								Content:  "HTTP 200 with product details",
							},
						}},
					},
				},
				Role:  model.RoleTool,
				Text:  "web_fetch: HTTP 200 with product details",
				Score: 0.88,
			},
		},
		window: &session.EventWindow{
			SessionKey: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			AnchorEventID: "evt-tool",
			Entries: []session.EventWindowEntry{
				{
					Event: event.Event{
						ID: "evt-user",
						Response: &model.Response{
							Choices: []model.Choice{{
								Message: model.Message{
									Role:    model.RoleUser,
									Content: "fetch the product page",
								},
							}},
						},
					},
					CreatedAt: createdAt.Add(-time.Minute),
				},
				{
					Event: event.Event{
						ID: "evt-tool",
						Response: &model.Response{
							Choices: []model.Choice{{
								Message: model.Message{
									Role:     model.RoleTool,
									ToolID:   "call-1",
									ToolName: "web_fetch",
									Content:  "HTTP 200 with product details",
								},
							}},
						},
					},
					CreatedAt: createdAt,
				},
			},
		},
	}

	inv := &agent.Invocation{
		Session:        session.NewSession("app", "user", "sess"),
		SessionService: svc,
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	args, err := json.Marshal(&SearchSessionRequest{
		Query: "product details",
		Scope: ScopeCurrentSession,
	})
	require.NoError(t, err)

	result, err := NewSearchTool().Call(ctx, args)
	require.NoError(t, err)

	resp, ok := result.(*SearchSessionResponse)
	require.True(t, ok)
	require.Len(t, resp.Results, 1)
	assert.Equal(t, ScopeCurrentSession, resp.Results[0].Scope)
	assert.Equal(t, model.RoleTool, resp.Results[0].Role)
	assert.Contains(t, resp.Results[0].Snippet, "tool: web_fetch: HTTP 200 with product details")
	require.Len(t, resp.Results[0].Context, 2)
	assert.Equal(t, model.RoleTool, resp.Results[0].Context[1].Role)
	assert.Equal(t, "web_fetch: HTTP 200 with product details", resp.Results[0].Context[1].Content)
	assert.Nil(t, svc.lastSearchReq.CreatedBefore)
	assert.ElementsMatch(
		t,
		[]model.Role{model.RoleUser, model.RoleAssistant, model.RoleTool},
		svc.lastSearchReq.Roles,
	)
}

func TestLoadTool(t *testing.T) {
	createdAt := time.Date(2025, 4, 7, 11, 0, 0, 0, time.UTC)
	svc := &mockSessionService{
		Service: sessioninmemory.NewSessionService(),
		window: &session.EventWindow{
			SessionKey: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			AnchorEventID: "evt-2",
			Entries: []session.EventWindowEntry{
				{
					Event: event.Event{
						ID: "evt-1",
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										Role:    model.RoleUser,
										Content: "first question",
									},
								},
							},
						},
					},
					CreatedAt: createdAt,
				},
				{
					Event: event.Event{
						ID: "evt-2",
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										Role:    model.RoleAssistant,
										Content: "second answer",
									},
								},
							},
						},
					},
					CreatedAt: createdAt.Add(time.Minute),
				},
			},
		},
	}

	inv := &agent.Invocation{
		Session:        session.NewSession("app", "user", "sess"),
		SessionService: svc,
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	args, err := json.Marshal(&LoadSessionRequest{
		EventID: "evt-2",
		Before:  4,
		After:   4,
	})
	require.NoError(t, err)

	result, err := NewLoadTool().Call(ctx, args)
	require.NoError(t, err)

	resp, ok := result.(*LoadSessionResponse)
	require.True(t, ok)
	assert.Equal(t, "sess", resp.SessionID)
	assert.Equal(t, "evt-2", resp.EventID)
	assert.Equal(t, 2, resp.Before)
	assert.Equal(t, 2, resp.After)
	assert.Equal(t, loadContextNote, resp.Note)
	require.Len(t, resp.Messages, 2)
	assert.Equal(t, "evt-1", resp.Messages[0].EventID)
	assert.Equal(t, model.RoleUser, resp.Messages[0].Role)
	assert.Equal(t, "first question", resp.Messages[0].Content)
	assert.Equal(t, "sess", svc.lastWindowReq.Key.SessionID)
	assert.Equal(t, "evt-2", svc.lastWindowReq.AnchorEventID)
	assert.ElementsMatch(
		t,
		[]model.Role{model.RoleUser, model.RoleAssistant, model.RoleTool},
		svc.lastWindowReq.Roles,
	)
}

func TestLoadTool_IncludesToolResults(t *testing.T) {
	createdAt := time.Date(2025, 4, 7, 11, 0, 0, 0, time.UTC)
	svc := &mockSessionService{
		Service: sessioninmemory.NewSessionService(),
		window: &session.EventWindow{
			SessionKey: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			AnchorEventID: "evt-tool",
			Entries: []session.EventWindowEntry{
				{
					Event: event.Event{
						ID: "evt-user",
						Response: &model.Response{
							Choices: []model.Choice{{
								Message: model.Message{
									Role:    model.RoleUser,
									Content: "run the lookup",
								},
							}},
						},
					},
					CreatedAt: createdAt,
				},
				{
					Event: event.Event{
						ID: "evt-tool",
						Response: &model.Response{
							Choices: []model.Choice{{
								Message: model.Message{
									Role:     model.RoleTool,
									ToolID:   "call-1",
									ToolName: "db_query",
									Content:  "row_count=42",
								},
							}},
						},
					},
					CreatedAt: createdAt.Add(time.Minute),
				},
			},
		},
	}

	inv := &agent.Invocation{
		Session:        session.NewSession("app", "user", "sess"),
		SessionService: svc,
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	args, err := json.Marshal(&LoadSessionRequest{
		EventID: "evt-tool",
		Before:  1,
		After:   0,
	})
	require.NoError(t, err)

	result, err := NewLoadTool().Call(ctx, args)
	require.NoError(t, err)

	resp, ok := result.(*LoadSessionResponse)
	require.True(t, ok)
	require.Len(t, resp.Messages, 2)
	assert.Equal(t, model.RoleTool, resp.Messages[1].Role)
	assert.Equal(t, "db_query: row_count=42", resp.Messages[1].Content)
}

func TestNormalizeWindowSize_PreservesExplicitZeroSide(t *testing.T) {
	before, after := normalizeWindowSize(0, 3)
	assert.Equal(t, 0, before)
	assert.Equal(t, 3, after)

	before, after = normalizeWindowSize(3, 0)
	assert.Equal(t, 3, before)
	assert.Equal(t, 0, after)
}
