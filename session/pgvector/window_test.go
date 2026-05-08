//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package pgvector

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestGetEventWindow_InvalidRequest(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	_, err := s.GetEventWindow(
		context.Background(),
		session.EventWindowRequest{
			Key:           session.Key{AppName: "app", UserID: "user"},
			AnchorEventID: "evt-1",
		},
	)
	require.Error(t, err)

	_, err = s.GetEventWindow(
		context.Background(),
		session.EventWindowRequest{
			Key: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
		},
	)
	require.Error(t, err)

	_, err = s.GetEventWindow(
		context.Background(),
		session.EventWindowRequest{
			Key: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			AnchorEventID: "evt-1",
			Before:        -1,
		},
	)
	require.Error(t, err)
}

func TestGetEventWindow_Success(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(t, nil)
	defer db.Close()

	makeEventBytes := func(
		id string,
		role model.Role,
		content string,
	) []byte {
		evt := event.Event{
			ID: id,
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    role,
							Content: content,
						},
					},
				},
			},
		}
		b, err := json.Marshal(evt)
		require.NoError(t, err)
		return b
	}

	base := time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC)
	anchorRows := sqlmock.NewRows(
		[]string{"id", "event", "created_at"},
	).AddRow(
		int64(22),
		makeEventBytes("evt-2", model.RoleAssistant, "second"),
		base.Add(2*time.Minute),
	)
	beforeRows := sqlmock.NewRows(
		[]string{"event", "created_at"},
	).AddRow(
		makeEventBytes("evt-1", model.RoleUser, "first"),
		base,
	)
	afterRows := sqlmock.NewRows(
		[]string{"event", "created_at"},
	).AddRow(
		makeEventBytes("evt-3", model.RoleUser, "third"),
		base.Add(3*time.Minute),
	)

	mock.ExpectQuery(`SELECT se\.id, se\.event, se\.created_at`).
		WithArgs("app", "user", "sess", "evt-2", []string{"user", "assistant"}).
		WillReturnRows(anchorRows)
	mock.ExpectQuery(`SELECT se\.event, se\.created_at`).
		WithArgs("app", "user", "sess", base.Add(2*time.Minute), int64(22), []string{"user", "assistant"}).
		WillReturnRows(beforeRows)
	mock.ExpectQuery(`SELECT se\.event, se\.created_at`).
		WithArgs("app", "user", "sess", base.Add(2*time.Minute), int64(22), []string{"user", "assistant"}).
		WillReturnRows(afterRows)

	window, err := s.GetEventWindow(
		context.Background(),
		session.EventWindowRequest{
			Key: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			AnchorEventID: "evt-2",
			Before:        1,
			After:         1,
			Roles: []model.Role{
				model.RoleUser,
				model.RoleAssistant,
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, window)
	require.Len(t, window.Entries, 3)
	assert.Equal(t, "evt-1", window.Entries[0].Event.ID)
	assert.Equal(t, "evt-2", window.Entries[1].Event.ID)
	assert.Equal(t, "evt-3", window.Entries[2].Event.ID)
	assert.Equal(t, base.Add(2*time.Minute), window.Entries[1].CreatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetEventWindow_AnchorNotFound(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(t, nil)
	defer db.Close()

	rows := sqlmock.NewRows(
		[]string{"id", "event", "created_at"},
	)

	mock.ExpectQuery(`SELECT se\.id, se\.event, se\.created_at`).
		WithArgs("app", "user", "sess", "evt-tool", []string{"user", "assistant"}).
		WillReturnRows(rows)

	_, err := s.GetEventWindow(
		context.Background(),
		session.EventWindowRequest{
			Key: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			AnchorEventID: "evt-tool",
			Before:        1,
			After:         1,
			Roles: []model.Role{
				model.RoleUser,
				model.RoleAssistant,
			},
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "anchor event not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetEventWindow_IncludesToolResultsWhenRequested(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(t, nil)
	defer db.Close()

	makeEventBytes := func(id string, role model.Role, content string) []byte {
		evt := event.Event{
			ID: id,
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    role,
						Content: content,
					},
				}},
			},
		}
		b, err := json.Marshal(evt)
		require.NoError(t, err)
		return b
	}

	makeToolEventBytes := func(id string) []byte {
		evt := event.Event{
			ID: id,
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
		}
		b, err := json.Marshal(evt)
		require.NoError(t, err)
		return b
	}

	base := time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC)
	anchorRows := sqlmock.NewRows(
		[]string{"id", "event", "created_at"},
	).AddRow(
		int64(11),
		makeToolEventBytes("evt-tool"),
		base.Add(time.Minute),
	)
	beforeRows := sqlmock.NewRows(
		[]string{"event", "created_at"},
	).AddRow(
		makeEventBytes("evt-1", model.RoleUser, "first"),
		base,
	)
	afterRows := sqlmock.NewRows(
		[]string{"event", "created_at"},
	).AddRow(
		makeEventBytes("evt-2", model.RoleAssistant, "second"),
		base.Add(2*time.Minute),
	)

	mock.ExpectQuery(`SELECT se\.id, se\.event, se\.created_at`).
		WithArgs("app", "user", "sess", "evt-tool", []string{"user", "assistant", "tool"}).
		WillReturnRows(anchorRows)
	mock.ExpectQuery(`SELECT se\.event, se\.created_at`).
		WithArgs("app", "user", "sess", base.Add(time.Minute), int64(11), []string{"user", "assistant", "tool"}).
		WillReturnRows(beforeRows)
	mock.ExpectQuery(`SELECT se\.event, se\.created_at`).
		WithArgs("app", "user", "sess", base.Add(time.Minute), int64(11), []string{"user", "assistant", "tool"}).
		WillReturnRows(afterRows)

	window, err := s.GetEventWindow(
		context.Background(),
		session.EventWindowRequest{
			Key: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			AnchorEventID: "evt-tool",
			Before:        1,
			After:         1,
			Roles: []model.Role{
				model.RoleUser,
				model.RoleAssistant,
				model.RoleTool,
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, window.Entries, 3)
	assert.Equal(t, "evt-tool", window.Entries[1].Event.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetEventWindow_QueryError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	mock.ExpectQuery(`SELECT se\.id, se\.event, se\.created_at`).
		WithArgs("app", "user", "sess", "evt-1").
		WillReturnError(assert.AnError)

	_, err := s.GetEventWindow(
		context.Background(),
		session.EventWindowRequest{
			Key: session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: "sess",
			},
			AnchorEventID: "evt-1",
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load event window")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractWindowEventText_UsesContentParts(t *testing.T) {
	text1 := "first"
	text2 := "second"
	text, role, ok := extractWindowEventText(&event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ContentParts: []model.ContentPart{
						{Text: &text1},
						{Text: &text2},
					},
				},
			}},
		},
	})
	require.True(t, ok)
	assert.Equal(t, model.RoleAssistant, role)
	assert.Equal(t, "first\nsecond", text)
}

func TestExtractWindowEventText_RejectsPartialResponses(t *testing.T) {
	_, _, ok := extractWindowEventText(&event.Event{
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
}

func TestWindowHelpers(t *testing.T) {
	filter := makeRoleFilter([]model.Role{"", " user ", model.RoleAssistant})
	require.Len(t, filter, 2)
	assert.Nil(t, makeRoleFilter(nil))
	assert.Nil(t, makeRoleFilter([]model.Role{""}))

	assert.True(t, eventAllowedInWindow(nil, nil))
	assert.False(t, eventAllowedInWindow(&event.Event{}, filter))

	entries := []session.EventWindowEntry{
		{Event: event.Event{ID: "evt-1"}},
		{Event: event.Event{ID: "evt-2"}},
		{Event: event.Event{ID: "evt-3"}},
	}
	reverseWindowEntries(entries)
	assert.Equal(t, []string{"evt-3", "evt-2", "evt-1"}, []string{
		entries[0].Event.ID,
		entries[1].Event.ID,
		entries[2].Event.ID,
	})
}

func TestDecodeWindowEntry_AndExtractWindowEventText_EdgeBranches(t *testing.T) {
	_, ok, err := decodeWindowEntry([]byte("not-json"), time.Now(), nil)
	require.Error(t, err)
	assert.False(t, ok)

	toolCallEvent := event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					ToolCalls: []model.ToolCall{{ID: "call-1", Type: "function"}},
				},
			}},
		},
	}
	bytes, err := json.Marshal(toolCallEvent)
	require.NoError(t, err)

	_, ok, err = decodeWindowEntry(bytes, time.Now(), map[model.Role]struct{}{
		model.RoleAssistant: {},
	})
	require.NoError(t, err)
	assert.False(t, ok)

	text, role, ok := extractWindowEventText(&event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Content: "assistant default role",
				},
			}},
		},
	})
	require.True(t, ok)
	assert.Equal(t, model.RoleAssistant, role)
	assert.Equal(t, "assistant default role", text)

	text, role, ok = extractWindowEventText(&event.Event{
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
	})
	require.True(t, ok)
	assert.Equal(t, model.RoleTool, role)
	assert.Equal(t, "db_query: row_count=42", text)

	_, _, ok = extractWindowEventText(&event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleSystem,
					Content: "system message",
				},
			}},
		},
	})
	assert.False(t, ok)

	_, _, ok = extractWindowEventText(&event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
				},
			}},
		},
	})
	assert.False(t, ok)
}

func TestLoadWindowHelpers_EdgeBranches(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	entries, err := s.loadWindowNeighbors(
		context.Background(),
		session.Key{
			AppName:   "app",
			UserID:    "user",
			SessionID: "sess",
		},
		time.Now(),
		12,
		0,
		nil,
		nil,
		true,
	)
	require.NoError(t, err)
	assert.Nil(t, entries)

	anchorRows := sqlmock.NewRows(
		[]string{"id", "event", "created_at"},
	).AddRow(
		int64(22),
		[]byte("not-json"),
		time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC),
	)

	mock.ExpectQuery(`SELECT se\.id, se\.event, se\.created_at`).
		WithArgs("app", "user", "sess", "evt-1").
		WillReturnRows(anchorRows)

	_, err = s.loadWindowAnchor(
		context.Background(),
		session.Key{
			AppName:   "app",
			UserID:    "user",
			SessionID: "sess",
		},
		"evt-1",
		nil,
		nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal event window row")
	require.NoError(t, mock.ExpectationsWereMet())
}
