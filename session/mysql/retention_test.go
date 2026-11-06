//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysql

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestLimitedSessionEventsReleasesDiscardedPayload(t *testing.T) {
	const payloadSize = 32 << 20
	for _, filterByTime := range []bool{false, true} {
		name := "first user"
		if filterByTime {
			name = "timestamp"
		}
		t.Run(name, func(t *testing.T) {
			before := heapAfterGC()
			events := loadTrimmedHistory(t, payloadSize, filterByTime)
			after := heapAfterGC()
			// Allow ample runtime/test overhead, but not the discarded payload.
			retained := int64(after) - int64(before)
			require.Less(t, retained, int64(payloadSize/2),
				"two small retained events still keep a discarded large message alive")
			runtime.KeepAlive(events)
		})
	}
}

func heapAfterGC() uint64 {
	// The second collection also clears temporary JSON encoder pool entries.
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// Keep the SQL fixtures and decoder temporaries outside the live stack when
// measuring the returned history. Only the retained events escape this helper.
//
//go:noinline
func loadTrimmedHistory(t *testing.T, payloadSize int, filterByTime bool) []event.Event {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	svc := createTestService(t, db)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	created := time.Unix(100, 0)
	afterTime := time.Time{}
	queryAfterTime := created
	firstRole := model.RoleAssistant
	if filterByTime {
		afterTime = created.Add(2 * time.Second)
		queryAfterTime = afterTime
		firstRole = model.RoleUser
	}
	messages := []model.Message{
		{Role: firstRole, Content: strings.Repeat("x", payloadSize)},
		model.NewUserMessage("keep user"),
		model.NewAssistantMessage("keep answer"),
	}
	var refs []eventRef
	var rows []limitedEventRow
	for i, message := range messages {
		evt := event.NewResponseEvent("invocation", "agent", &model.Response{
			Choices: []model.Choice{{Message: message}},
		})
		evt.Timestamp = created.Add(time.Duration(i+1) * time.Second)
		raw, err := json.Marshal(evt)
		require.NoError(t, err)
		// Persistence timestamps can be later than the event timestamps.
		persistedAt := created.Add(time.Duration(i+10) * time.Second)
		id := int64(i + 1)
		refs = append(refs, eventRef{id: id, createdAt: persistedAt})
		rows = append(rows, limitedEventRow{id: id, event: raw, createdAt: persistedAt})
	}
	expectLimitedEventRefs(mock, key, queryAfterTime, len(refs), refs...)
	expectEventsByRefs(mock, key, rows...)
	result, err := svc.getLimitedSessionEvents(
		context.Background(), key, created, len(refs), afterTime, time.Time{},
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Len(t, result, 1)
	require.Len(t, result[0], 2)
	require.Equal(t, "keep user", result[0][0].Choices[0].Message.Content)
	require.Equal(t, "keep answer", result[0][1].Choices[0].Message.Content)
	return result[0]
}
