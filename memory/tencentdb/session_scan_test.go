//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestSessionScanTimestampAndStateEdges(t *testing.T) {
	assert.Empty(t, scanTranscript(nil, time.Time{}).Messages)
	assert.Empty(t, scanTranscript(&session.Session{}, time.Time{}).Messages)

	base := time.Date(2026, 5, 22, 8, 0, 0, 0, time.UTC)
	sess := &session.Session{
		ID:      "s1",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			{ID: "old", Timestamp: base, Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewUserMessage("old"),
			}}}},
			{ID: "nil-response", Timestamp: base.Add(time.Second)},
			{ID: "system", Timestamp: base.Add(2 * time.Second), Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewSystemMessage("system"),
			}}}},
			{ID: "empty", Timestamp: base.Add(3 * time.Second), Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewUserMessage("   "),
			}}}},
			{ID: "user", Timestamp: base.Add(4 * time.Second), Response: &model.Response{Choices: []model.Choice{{
				Index:   2,
				Message: model.NewUserMessage("new"),
			}}}},
		},
	}
	scan := scanTranscript(sess, base)
	require.Len(t, scan.Messages, 1)
	assert.Equal(t, "user:2", scan.Messages[0].ID)
	assert.Equal(t, base.Add(4*time.Second), scan.Latest)

	assert.Nil(t, normalizeGatewayMessageTimestamps(nil, time.Now()))
	normalized := normalizeGatewayMessageTimestamps([]tdaiMessage{{Content: "x"}}, time.Time{})
	require.Len(t, normalized, 1)
	assert.NotZero(t, normalized[0].Timestamp)
	empty, latest := normalizeGatewayMessageTimestampsAfter(nil, time.Now(), 123)
	assert.Nil(t, empty)
	assert.Equal(t, int64(123), latest)
	bumped, latest := normalizeGatewayMessageTimestampsAfter(
		[]tdaiMessage{{Content: "a"}, {Content: "b"}},
		time.UnixMilli(1000),
		5000,
	)
	require.Len(t, bumped, 2)
	assert.Equal(t, int64(5001), bumped[0].Timestamp)
	assert.Equal(t, int64(5002), bumped[1].Timestamp)
	assert.Equal(t, int64(5002), latest)

	stateSess := &session.Session{}
	stateSess.SetState(lastCaptureAtStateKey, []byte("not-a-time"))
	assert.True(t, readBestEffortLastCaptureAt(stateSess).IsZero())
	writeBestEffortSyntheticTimestamp(nil, 10)
	writeBestEffortSyntheticTimestamp(stateSess, 0)
	assert.Zero(t, readBestEffortSyntheticTimestamp(stateSess))
	stateSess.SetState(syntheticTimestampStateKey, []byte("bad"))
	assert.Zero(t, readBestEffortSyntheticTimestamp(stateSess))
	writeBestEffortSyntheticTimestamp(stateSess, 77)
	assert.Equal(t, int64(77), readBestEffortSyntheticTimestamp(stateSess))
	clearBestEffortSyntheticTimestamp(nil)
	clearBestEffortSyntheticTimestamp(stateSess)
	assert.Zero(t, readBestEffortSyntheticTimestamp(stateSess))

	assert.Empty(t, messageText(model.Message{Role: model.RoleUser}))
	assert.Empty(t, messageText(model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{{
			Type: model.ContentTypeText,
		}},
	}))
}
