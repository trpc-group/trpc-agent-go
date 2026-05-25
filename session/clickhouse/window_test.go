//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package clickhouse

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestService_GetEventWindow(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	base := time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC)
	mockCli := &mockClient{}
	svc := &Service{
		chClient:           mockCli,
		tableSessionStates: "session_states",
		tableSessionEvents: "session_events",
	}

	mockCli.queryFunc = func(
		ctx context.Context,
		query string,
		args ...any,
	) (driver.Rows, error) {
		switch {
		case strings.Contains(query, "SELECT created_at FROM"):
			require.Equal(t, []any{key.AppName, key.UserID, key.SessionID}, args[:3])
			return newMockRows([][]any{{base}}), nil
		case strings.Contains(query, "SELECT event, created_at FROM"):
			require.Equal(t, []any{key.AppName, key.UserID, key.SessionID, base}, args)
			return newMockRows([][]any{
				{clickhouseWindowEventJSON(t, "u1", model.RoleUser, "one"), base},
				{clickhouseWindowEventJSON(t, "a1", model.RoleAssistant, "two"), base.Add(time.Minute)},
				{clickhouseWindowEventJSON(t, "u2", model.RoleUser, "three"), base.Add(2 * time.Minute)},
				{clickhouseWindowToolEventJSON(t, "t1", "calc", "four"), base.Add(3 * time.Minute)},
				{clickhouseWindowEventJSON(t, "u3", model.RoleUser, "five"), base.Add(4 * time.Minute)},
			}), nil
		default:
			t.Fatalf("unexpected query: %s", query)
			return nil, nil
		}
	}

	got, err := svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "u2",
		Before:        1,
		After:         1,
		Roles:         []model.Role{model.RoleUser},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"u1", "u2", "u3"}, clickhouseWindowIDs(got))
}

func TestService_GetEventWindowAnchorNotFound(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	base := time.Date(2025, 4, 7, 9, 0, 0, 0, time.UTC)
	mockCli := &mockClient{}
	svc := &Service{
		chClient:           mockCli,
		tableSessionStates: "session_states",
		tableSessionEvents: "session_events",
	}

	mockCli.queryFunc = func(
		ctx context.Context,
		query string,
		args ...any,
	) (driver.Rows, error) {
		switch {
		case strings.Contains(query, "SELECT created_at FROM"):
			return newMockRows([][]any{{base}}), nil
		case strings.Contains(query, "SELECT event, created_at FROM"):
			return newMockRows([][]any{
				{clickhouseWindowEventJSON(t, "u1", model.RoleUser, "one"), base},
			}), nil
		default:
			t.Fatalf("unexpected query: %s", query)
			return nil, nil
		}
	}

	_, err := svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "missing",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event not found")
}

func clickhouseWindowEventJSON(
	t *testing.T,
	id string,
	role model.Role,
	content string,
) string {
	t.Helper()
	evt := event.Event{
		ID:        id,
		Timestamp: time.Now().UTC(),
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    role,
					Content: content,
				},
			}},
		},
	}
	data, err := json.Marshal(evt)
	require.NoError(t, err)
	return string(data)
}

func clickhouseWindowToolEventJSON(
	t *testing.T,
	id string,
	toolName string,
	content string,
) string {
	t.Helper()
	evt := event.Event{
		ID:        id,
		Timestamp: time.Now().UTC(),
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:     model.RoleTool,
					Content:  content,
					ToolID:   "call-" + id,
					ToolName: toolName,
				},
			}},
		},
	}
	data, err := json.Marshal(evt)
	require.NoError(t, err)
	return string(data)
}

func clickhouseWindowIDs(window *session.EventWindow) []string {
	ids := make([]string, 0, len(window.Entries))
	for _, entry := range window.Entries {
		ids = append(ids, entry.Event.ID)
	}
	return ids
}
