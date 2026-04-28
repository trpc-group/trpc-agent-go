//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sessionrecall

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

func TestSupportChecks(t *testing.T) {
	require.False(t, SupportsSearch(nil))
	require.False(t, SupportsLoad(nil))
	require.False(t, SupportsOnDemandSession(nil))

	inv := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "sess")),
		agent.WithInvocationSessionService(&mockSessionService{
			Service: sessioninmemory.NewSessionService(),
		}),
	)
	require.True(t, SupportsSearch(inv))
	require.True(t, SupportsLoad(inv))
	require.True(t, SupportsOnDemandSession(inv))
}

func TestInvocationHelpers(t *testing.T) {
	_, err := invocationFromContext(context.Background())
	require.ErrorIs(t, err, errInvocationContextRequired)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "sess")),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	_, _, err = searchableServiceFromContext(ctx)
	require.ErrorIs(t, err, errSearchUnavailable)
	_, _, err = windowServiceFromContext(ctx)
	require.ErrorIs(t, err, errWindowUnavailable)
	assert.Nil(t, optionalWindowServiceFromInvocation(nil))

	userKey, err := currentUserKey(inv)
	require.NoError(t, err)
	assert.Equal(t, "app", userKey.AppName)

	sessionKey, err := currentSessionKey(inv, "")
	require.NoError(t, err)
	assert.Equal(t, "sess", sessionKey.SessionID)
	sessionKey, err = currentSessionKey(inv, "other")
	require.NoError(t, err)
	assert.Equal(t, "other", sessionKey.SessionID)

	_, err = currentUserKey(nil)
	require.Error(t, err)
	_, err = currentSessionKey(nil, "")
	require.Error(t, err)
}

func TestCurrentSummaryCutoff_StateAndSummaryFallbacks(t *testing.T) {
	require.True(t, currentSummaryCutoff(nil).IsZero())

	updatedAt := time.Date(2025, 4, 7, 11, 0, 0, 0, time.UTC)
	childUpdatedAt := updatedAt.Add(2 * time.Minute)
	sess := session.NewSession(
		"app",
		"user",
		"sess",
		session.WithSessionSummaries(map[string]*session.Summary{
			"team/child": {
				Summary:   "team child summary",
				UpdatedAt: childUpdatedAt,
			},
			session.SummaryFilterKeyAllContents: {
				Summary:   "full summary",
				UpdatedAt: updatedAt,
			},
		}),
	)
	sess.SetState(summaryLastIncludedTsKey, []byte("not-a-timestamp"))

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("team"),
	)
	assert.Equal(t, childUpdatedAt, currentSummaryCutoff(inv))

	exactUpdatedAt := updatedAt.Add(4 * time.Minute)
	sess.Summaries["team"] = &session.Summary{
		Summary:   "team summary",
		UpdatedAt: exactUpdatedAt,
	}
	assert.Equal(t, exactUpdatedAt, currentSummaryCutoff(inv))

	lastIncludedAt := updatedAt.Add(-time.Minute)
	sess.SetState(
		summaryLastIncludedTsKey,
		[]byte(lastIncludedAt.Format(time.RFC3339Nano)),
	)
	assert.Equal(t, lastIncludedAt, currentSummaryCutoff(inv))
}

func TestExtractSessionMessageText(t *testing.T) {
	toolText, toolRole, ok := extractSessionMessageText(event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:     model.RoleTool,
					ToolID:   "call-1",
					ToolName: "web_fetch",
					Content:  "HTTP 200",
				},
			}},
		},
	})
	require.True(t, ok)
	assert.Equal(t, model.RoleTool, toolRole)
	assert.Equal(t, "web_fetch: HTTP 200", toolText)

	part1 := "alpha"
	part2 := "beta"
	text, role, ok := extractSessionMessageText(event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					ContentParts: []model.ContentPart{
						{Text: &part1},
						{Text: &part2},
					},
				},
			}},
		},
	})
	require.True(t, ok)
	assert.Equal(t, model.RoleAssistant, role)
	assert.Equal(t, "alpha\nbeta", text)

	_, _, ok = extractSessionMessageText(event.Event{
		Response: &model.Response{
			IsPartial: true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "partial",
				},
			}},
		},
	})
	assert.False(t, ok)

	_, _, ok = extractSessionMessageText(event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{ID: "call-1", Type: "function"},
					},
				},
			}},
		},
	})
	assert.False(t, ok)
}

func TestLoadedMessagesFromWindow_SkipsUnusableEntries(t *testing.T) {
	require.Nil(t, loadedMessagesFromWindow(nil))

	window := &session.EventWindow{
		Entries: []session.EventWindowEntry{
			{
				Event: event.Event{
					ID: "evt-user",
					Response: &model.Response{
						Choices: []model.Choice{{
							Message: model.Message{
								Role:    model.RoleUser,
								Content: "hello",
							},
						}},
					},
				},
				CreatedAt: time.Date(2025, 4, 7, 11, 0, 0, 0, time.UTC),
			},
			{
				Event: event.Event{
					ID: "evt-toolcall",
					Response: &model.Response{
						Choices: []model.Choice{{
							Message: model.Message{
								ToolCalls: []model.ToolCall{
									{ID: "call-1", Type: "function"},
								},
							},
						}},
					},
				},
				CreatedAt: time.Date(2025, 4, 7, 11, 1, 0, 0, time.UTC),
			},
		},
	}

	messages := loadedMessagesFromWindow(window)
	require.Len(t, messages, 1)
	assert.Equal(t, "evt-user", messages[0].EventID)
	assert.Equal(t, "hello", messages[0].Content)
}

func TestSearchHelperFunctions(t *testing.T) {
	assert.Equal(t, ScopeCurrentHidden, normalizeScope("unknown"))
	assert.Equal(t, ScopeCurrentSession, normalizeScope(" current_session "))
	assert.Equal(t, session.SearchModeHybrid, normalizeSearchMode(""))
	assert.Equal(t, session.SearchModeDense, normalizeSearchMode(session.SearchModeDense))
	assert.Equal(t, defaultSearchTopK, normalizeTopK(0))
	assert.Equal(t, maxSearchTopK, normalizeTopK(maxSearchTopK+3))

	queries := searchQueries("What did Alice say about budgets, timeline and risks?")
	require.NotEmpty(t, queries)
	assert.Contains(t, queries, "What did Alice say about budgets, timeline and risks?")
	assert.Contains(t, queries, "Alice say about budgets")

	assert.True(t, shouldBroadenSearch(
		"Alice and Bob",
		[]session.EventSearchResult{{Score: 0.9}},
		3,
	))
	assert.False(t, shouldBroadenSearch(
		"Alice",
		[]session.EventSearchResult{{Score: 0.9}, {Score: 0.8}},
		2,
	))
	assert.True(t, hasCompoundSearchIntent("Alice, Bob and Carol"))
	clauses := splitSearchClauses("Alice, Bob and Carol")
	require.Len(t, clauses, 3)
	assert.Equal(t, "Alice", clauses[0])
	assert.Equal(t, "Bob", trimSearchLeadIn(clauses[1]))
	assert.Equal(t, "Carol", clauses[2])
	assert.Equal(
		t,
		"Alice say about budgets",
		trimSearchLeadIn("What did Alice say about budgets"),
	)
	assert.NotEmpty(t, fallbackSearchQueries("What did Alice say about budget planning for April?"))
	assert.Equal(t, "budget planning april", keywordSearchQuery("budget planning for April"))
	assert.Equal(t, []string{"roadmap", "2025"}, keywordTokens("the roadmap for 2025"))
	assert.Equal(t, []string{"Budget", "2025"}, tokenizeSearchQuery("Budget/2025"))
	assert.True(t, containsDigit("v2"))
	assert.False(t, containsDigit("beta"))
	assert.Equal(t, 2, utf8Len("你好"))
	assert.Equal(t, []string{"alpha", "beta"}, dedupeStrings([]string{" alpha ", "beta", "alpha"}))
}

func TestSearchResultHelpers(t *testing.T) {
	inv := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "sess")),
	)
	assert.Equal(t, ScopeCurrentHidden, resultScope(
		session.EventSearchResult{
			SessionKey: session.Key{SessionID: "sess"},
		},
		inv,
		ScopeCurrentHidden,
	))
	assert.Equal(t, ScopeOtherSessions, resultScope(
		session.EventSearchResult{
			SessionKey: session.Key{SessionID: "other"},
		},
		inv,
		ScopeCurrentHidden,
	))

	window := &session.EventWindow{
		AnchorEventID: "evt-2",
		Entries: []session.EventWindowEntry{
			{
				Event: event.Event{
					ID: "evt-1",
					Response: &model.Response{
						Choices: []model.Choice{{
							Message: model.Message{
								Role:    model.RoleUser,
								Content: "first line",
							},
						}},
					},
				},
			},
			{
				Event: event.Event{
					ID: "evt-2",
					Response: &model.Response{
						Choices: []model.Choice{{
							Message: model.Message{
								Role:    model.RoleAssistant,
								Content: "second line",
							},
						}},
					},
				},
			},
		},
	}
	assert.Contains(t, windowSnippet(window), "[match] assistant: second line")
	assert.Equal(t, "a...", compactSnippetText("abcdef", 4))
	assert.Empty(t, compactSnippetText("abcdef", 0))

	result := session.EventSearchResult{
		Event: event.Event{
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "fallback text",
					},
				}},
			},
		},
	}
	assert.Equal(t, "fallback text", resultSnippet(result, nil))
	assert.Equal(t, "<empty>", resultSnippet(session.EventSearchResult{}, nil))

	contextMessages := searchResultContext(window)
	require.Len(t, contextMessages, 2)
	assert.Equal(t, "evt-2", contextMessages[1].EventID)

	windowSvc := &mockSessionService{
		window: &session.EventWindow{
			AnchorEventID: "evt-2",
			Entries:       window.Entries,
		},
	}
	searchWindow := searchResultWindow(
		context.Background(),
		windowSvc,
		session.EventSearchResult{
			SessionKey: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			Event: event.Event{ID: "evt-2"},
		},
		0,
	)
	require.NotNil(t, searchWindow)
	assert.Equal(t, "evt-2", searchWindow.AnchorEventID)
	assert.Nil(t, searchResultWindow(
		context.Background(),
		windowSvc,
		session.EventSearchResult{},
		searchExpandedHits,
	))

	windowSvc.windowErr = assert.AnError
	assert.Nil(t, searchResultWindow(
		context.Background(),
		windowSvc,
		session.EventSearchResult{
			SessionKey: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			Event: event.Event{ID: "evt-2"},
		},
		0,
	))
}

func TestMergeSearchResultsAndLexicalScore(t *testing.T) {
	current := []session.EventSearchResult{
		{
			SessionKey:     session.Key{SessionID: "sess-a"},
			Event:          event.Event{ID: "evt-1"},
			Score:          0.6,
			EventCreatedAt: time.Date(2025, 4, 7, 10, 0, 0, 0, time.UTC),
		},
	}
	incoming := []session.EventSearchResult{
		{
			SessionKey:     session.Key{SessionID: "sess-a"},
			Event:          event.Event{ID: "evt-1"},
			Score:          0.9,
			EventCreatedAt: time.Date(2025, 4, 7, 10, 1, 0, 0, time.UTC),
		},
		{
			SessionKey:     session.Key{SessionID: "sess-b"},
			Event:          event.Event{ID: "evt-2"},
			Score:          0.7,
			EventCreatedAt: time.Date(2025, 4, 7, 10, 2, 0, 0, time.UTC),
		},
	}
	merged := mergeSearchResults(current, incoming)
	require.Len(t, merged, 2)
	assert.Equal(t, "sess-a", merged[0].SessionKey.SessionID)
	assert.Equal(t, 0.9, merged[0].Score)
	assert.Greater(t, lexicalEventScore(
		"budget planning",
		[]string{"budget", "planning"},
		"The budget planning discussion moved to Friday.",
	), 0.0)
	assert.Zero(t, lexicalEventScore(
		"budget planning",
		[]string{"budget", "planning"},
		"Unrelated topic",
	))
}

func TestSearchWithFallback_UsesSecondaryQueries(t *testing.T) {
	svc := &mockSessionService{
		Service: sessioninmemory.NewSessionService(),
		searchFunc: func(
			req session.EventSearchRequest,
		) ([]session.EventSearchResult, error) {
			if req.Query == "alice budget planning april" {
				return []session.EventSearchResult{{
					SessionKey: session.Key{SessionID: "sess-a"},
					Event:      event.Event{ID: "evt-1"},
					Score:      0.9,
				}}, nil
			}
			return nil, nil
		},
	}

	results, err := searchWithFallback(
		context.Background(),
		svc,
		session.EventSearchRequest{
			Query:      "What did Alice say about budget planning for April?",
			MaxResults: 5,
		},
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.GreaterOrEqual(t, len(svc.searchReqs), 2)
}

func TestLexicalScanSessionEvents_HonorsCutoffAndTopK(t *testing.T) {
	base := time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC)
	sess := session.NewSession("app", "user", "sess")
	sess.Events = []event.Event{
		{
			ID:        "evt-1",
			Timestamp: base,
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Budget planning moved to Friday.",
					},
				}},
			},
		},
		{
			ID:        "evt-2",
			Timestamp: base.Add(time.Minute),
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Budget planning moved to Thursday.",
					},
				}},
			},
		},
		{
			ID:        "evt-3",
			Timestamp: base.Add(2 * time.Minute),
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Unrelated topic.",
					},
				}},
			},
		},
	}

	results := lexicalScanSessionEvents(
		sess,
		session.Key{
			AppName:   "app",
			UserID:    "user",
			SessionID: "sess",
		},
		"budget planning friday",
		base.Add(90*time.Second),
		1,
	)
	require.Len(t, results, 1)
	assert.Equal(t, "evt-1", results[0].Event.ID)
}

func TestSearchCurrentSession_UsesBackendResults(t *testing.T) {
	svc := &mockSessionService{
		Service: sessioninmemory.NewSessionService(),
		searchResults: []session.EventSearchResult{{
			SessionKey: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			Event:          event.Event{ID: "evt-1"},
			EventCreatedAt: time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC),
			Score:          0.9,
		}},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "sess")),
		agent.WithInvocationSessionService(svc),
	)

	results, err := searchCurrentSession(
		context.Background(),
		svc,
		inv,
		&SearchSessionRequest{
			Query:      "budget planning",
			TopK:       3,
			SearchMode: session.SearchModeDense,
		},
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, session.SearchModeDense, svc.lastSearchReq.SearchMode)
}

func TestSearchCurrentSession_FallsBackToScan(t *testing.T) {
	createdAt := time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC)
	storedSession := session.NewSession("app", "user", "sess")
	storedSession.Events = []event.Event{
		{
			ID:        "evt-scan",
			Timestamp: createdAt,
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Budget planning moved to Friday.",
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
			return storedSession, nil
		},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "sess")),
		agent.WithInvocationSessionService(svc),
	)

	results, err := searchCurrentSession(
		context.Background(),
		svc,
		inv,
		&SearchSessionRequest{Query: "budget planning friday"},
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "evt-scan", results[0].Event.ID)
}

func TestSearchCurrentHidden_WithoutSummaryReturnsNil(t *testing.T) {
	svc := &mockSessionService{Service: sessioninmemory.NewSessionService()}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "sess")),
		agent.WithInvocationSessionService(svc),
	)
	results, err := searchCurrentHidden(
		context.Background(),
		svc,
		inv,
		&SearchSessionRequest{Query: "budget planning"},
	)
	require.NoError(t, err)
	assert.Nil(t, results)
	assert.Empty(t, svc.searchReqs)
}

func TestSearchAllSessions_DedupesAndRespectsTopK(t *testing.T) {
	summaryUpdatedAt := time.Date(2025, 4, 7, 11, 0, 0, 0, time.UTC)
	sess := session.NewSession(
		"app",
		"user",
		"sess",
		session.WithSessionSummaries(map[string]*session.Summary{
			"": {Summary: "summary", UpdatedAt: summaryUpdatedAt},
		}),
	)
	sess.SetState(
		summaryLastIncludedTsKey,
		[]byte(summaryUpdatedAt.Add(-time.Minute).Format(time.RFC3339Nano)),
	)
	svc := &mockSessionService{
		Service: sessioninmemory.NewSessionService(),
		searchFunc: func(
			req session.EventSearchRequest,
		) ([]session.EventSearchResult, error) {
			if len(req.ExcludeSessionIDs) > 0 {
				return []session.EventSearchResult{
					{
						SessionKey: session.Key{SessionID: "sess"},
						Event:      event.Event{ID: "evt-dup"},
						Score:      0.91,
					},
					{
						SessionKey: session.Key{SessionID: "other"},
						Event:      event.Event{ID: "evt-other"},
						Score:      0.89,
					},
				}, nil
			}
			return []session.EventSearchResult{{
				SessionKey: session.Key{SessionID: "sess"},
				Event:      event.Event{ID: "evt-dup"},
				Score:      0.95,
			}}, nil
		},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(svc),
	)

	results, err := searchAllSessions(
		context.Background(),
		svc,
		inv,
		&SearchSessionRequest{
			Query: "budget planning",
			TopK:  1,
		},
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "evt-dup", results[0].Event.ID)
}

func TestSearchCurrentSessionByScanAndAllSessions(t *testing.T) {
	summaryUpdatedAt := time.Date(2025, 4, 7, 11, 0, 0, 0, time.UTC)
	hiddenCreatedAt := summaryUpdatedAt.Add(-time.Minute)
	otherCreatedAt := summaryUpdatedAt.Add(-2 * time.Minute)

	storedSession := session.NewSession("app", "user", "sess")
	storedSession.Events = []event.Event{
		{
			ID:        "evt-hidden",
			Timestamp: hiddenCreatedAt,
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Budget planning moved to Friday.",
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
			if len(req.ExcludeSessionIDs) > 0 {
				return []session.EventSearchResult{{
					SessionKey: session.Key{
						AppName:   "app",
						UserID:    "user",
						SessionID: "other-sess",
					},
					EventCreatedAt: otherCreatedAt,
					Event:          event.Event{ID: "evt-other"},
					Score:          0.82,
				}}, nil
			}
			return nil, nil
		},
		getSessionFunc: func(
			key session.Key,
			_ ...session.Option,
		) (*session.Session, error) {
			require.Equal(t, "sess", key.SessionID)
			return storedSession, nil
		},
	}

	currentSession := session.NewSession(
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
	inv := agent.NewInvocation(
		agent.WithInvocationSession(currentSession),
		agent.WithInvocationSessionService(svc),
	)

	currentResults, err := searchCurrentSessionByScan(
		context.Background(),
		inv,
		&SearchSessionRequest{
			Query: "budget planning friday",
			TopK:  2,
		},
	)
	require.NoError(t, err)
	require.Len(t, currentResults, 1)
	assert.Equal(t, "evt-hidden", currentResults[0].Event.ID)

	allResults, err := searchAllSessions(
		context.Background(),
		svc,
		inv,
		&SearchSessionRequest{
			Query: "budget planning friday",
			TopK:  3,
		},
	)
	require.NoError(t, err)
	require.Len(t, allResults, 2)
	assert.ElementsMatch(
		t,
		[]string{"sess", "other-sess"},
		[]string{
			allResults[0].SessionKey.SessionID,
			allResults[1].SessionKey.SessionID,
		},
	)
}

func TestSearchHelpers_EdgeBranches(t *testing.T) {
	assert.Equal(t, session.SearchModeHybrid, normalizeSearchMode("invalid"))
	assert.Equal(t, "current_hidden", normalizeScope(""))
	assert.Equal(t, []string{"alpha", "beta"}, dedupeStrings([]string{"", "alpha", "beta", "alpha"}))
	assert.Nil(t, dedupeStrings(nil))

	before, after := normalizeWindowSize(-2, -3)
	assert.Equal(t, defaultWindowBefore, before)
	assert.Equal(t, defaultWindowAfter, after)

	before, after = normalizeWindowSize(4, 4)
	assert.Equal(t, 2, before)
	assert.Equal(t, 2, after)

	assert.Nil(t, searchQueries(""))
	assert.Nil(t, fallbackSearchQueries("the and or"))
	assert.NotEmpty(
		t,
		fallbackSearchQueries("alpha beta gamma delta epsilon zeta eta theta iota"),
	)
	assert.Empty(t, keywordSearchQuery("the and or"))
	assert.Nil(t, keywordTokens("the and or"))

	assert.Nil(t, lexicalScanSessionEvents(nil, session.Key{}, "alpha", time.Time{}, 1))
	assert.Nil(t, lexicalScanSessionEvents(session.NewSession("app", "user", "sess"), session.Key{}, "", time.Time{}, 1))

	assert.Zero(
		t,
		lexicalEventScore(
			"alpha beta gamma",
			[]string{"alpha", "beta", "gamma"},
			"alpha only",
		),
	)

	assert.Equal(t, "", compactSnippetText("abcdef", -1))
	assert.Equal(t, "abc", compactSnippetText("abcdef", 3))
	assert.Equal(t, "", windowSnippet(nil))
	assert.Equal(t, "", windowSnippet(&session.EventWindow{}))
}

func TestSearchSessionHistory_AndScanErrorBranches(t *testing.T) {
	summaryUpdatedAt := time.Date(2025, 4, 7, 11, 0, 0, 0, time.UTC)
	sess := session.NewSession(
		"app",
		"user",
		"sess",
		session.WithSessionSummaries(map[string]*session.Summary{
			"": {Summary: "summary", UpdatedAt: summaryUpdatedAt},
		}),
	)
	sess.SetState(
		summaryLastIncludedTsKey,
		[]byte(summaryUpdatedAt.Add(-time.Minute).Format(time.RFC3339Nano)),
	)

	svc := &mockSessionService{
		Service: sessioninmemory.NewSessionService(),
		searchFunc: func(
			req session.EventSearchRequest,
		) ([]session.EventSearchResult, error) {
			switch {
			case len(req.SessionIDs) > 0:
				return []session.EventSearchResult{{
					SessionKey: session.Key{SessionID: req.SessionIDs[0]},
					Event:      event.Event{ID: "evt-current"},
					Score:      0.9,
				}}, nil
			case len(req.ExcludeSessionIDs) > 0:
				return []session.EventSearchResult{{
					SessionKey: session.Key{SessionID: "evt-other-sess"},
					Event:      event.Event{ID: "evt-other"},
					Score:      0.8,
				}}, nil
			default:
				return nil, assert.AnError
			}
		},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(svc),
	)

	results, err := searchSessionHistory(
		context.Background(),
		svc,
		inv,
		ScopeCurrentSession,
		&SearchSessionRequest{Query: "alpha"},
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "evt-current", results[0].Event.ID)

	results, err = searchSessionHistory(
		context.Background(),
		svc,
		inv,
		ScopeOtherSessions,
		&SearchSessionRequest{Query: "alpha"},
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "evt-other", results[0].Event.ID)

	results, err = searchSessionHistory(
		context.Background(),
		svc,
		inv,
		ScopeAllSessions,
		&SearchSessionRequest{Query: "alpha", TopK: 2},
	)
	require.NoError(t, err)
	require.Len(t, results, 2)

	_, err = searchAllSessions(
		context.Background(),
		svc,
		agent.NewInvocation(
			agent.WithInvocationSession(session.NewSession("", "user", "sess")),
		),
		&SearchSessionRequest{Query: "alpha"},
	)
	require.Error(t, err)

	_, err = searchCurrentSession(
		context.Background(),
		svc,
		nil,
		&SearchSessionRequest{Query: "alpha"},
	)
	require.Error(t, err)

	_, err = searchCurrentSessionScan(
		context.Background(),
		&agent.Invocation{
			Session: session.NewSession("", "user", "sess"),
			SessionService: &mockSessionService{
				Service: sessioninmemory.NewSessionService(),
			},
		},
		&SearchSessionRequest{Query: "alpha"},
		time.Time{},
	)
	require.Error(t, err)

	_, err = searchCurrentSessionScan(
		context.Background(),
		&agent.Invocation{
			Session: session.NewSession("app", "user", "sess"),
			SessionService: &mockSessionService{
				Service: sessioninmemory.NewSessionService(),
				getSessionFunc: func(
					session.Key,
					...session.Option,
				) (*session.Session, error) {
					return nil, assert.AnError
				},
			},
		},
		&SearchSessionRequest{Query: "alpha"},
		time.Time{},
	)
	require.Error(t, err)

	results, err = searchCurrentSessionScan(
		context.Background(),
		&agent.Invocation{
			Session: session.NewSession("app", "user", "sess"),
			SessionService: &mockSessionService{
				Service: sessioninmemory.NewSessionService(),
				getSessionFunc: func(
					session.Key,
					...session.Option,
				) (*session.Session, error) {
					return nil, nil
				},
			},
		},
		&SearchSessionRequest{Query: "alpha"},
		time.Time{},
	)
	require.NoError(t, err)
	assert.Nil(t, results)
}

func TestNewSearchAndLoadTool_NilPayloads(t *testing.T) {
	inv := &agent.Invocation{
		Session: session.NewSession("app", "user", "sess"),
		SessionService: &mockSessionService{
			Service: sessioninmemory.NewSessionService(),
			window:  &session.EventWindow{},
		},
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	searchResult, err := NewSearchTool().Call(ctx, []byte("null"))
	require.NoError(t, err)
	searchResp, ok := searchResult.(*SearchSessionResponse)
	require.True(t, ok)
	assert.Empty(t, searchResp.Query)
	assert.Empty(t, searchResp.Results)

	_, err = NewLoadTool().Call(ctx, []byte("null"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "event_id is required")
}

func TestLoadTool_ErrorPaths(t *testing.T) {
	inv := &agent.Invocation{
		Session:        session.NewSession("app", "user", "sess"),
		SessionService: &mockSessionService{Service: sessioninmemory.NewSessionService()},
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	args, err := json.Marshal(&LoadSessionRequest{})
	require.NoError(t, err)
	_, err = NewLoadTool().Call(ctx, args)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "event_id is required")

	svc := &mockSessionService{
		Service:   sessioninmemory.NewSessionService(),
		windowErr: assert.AnError,
	}
	ctx = agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session:        session.NewSession("app", "user", "sess"),
		SessionService: svc,
	})
	args, err = json.Marshal(&LoadSessionRequest{EventID: "evt-1"})
	require.NoError(t, err)
	_, err = NewLoadTool().Call(ctx, args)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session load tool")
}
