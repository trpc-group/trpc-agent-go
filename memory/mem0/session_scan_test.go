//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func mkMsg(role model.Role, content string) model.Message {
	return model.Message{Role: role, Content: content}
}

func mkEvent(ts time.Time, msg model.Message) event.Event {
	return event.Event{
		Timestamp: ts,
		Response: &model.Response{
			Choices: []model.Choice{{Message: msg}},
		},
	}
}

func TestReadLastExtractAt_NilSession(t *testing.T) {
	assert.True(t, readLastExtractAt(nil).IsZero())
}

func TestReadLastExtractAt_MissingKeyReturnsZero(t *testing.T) {
	sess := &session.Session{State: session.StateMap{}}
	assert.True(t, readLastExtractAt(sess).IsZero())
}

func TestReadLastExtractAt_InvalidValueReturnsZero(t *testing.T) {
	sess := &session.Session{}
	sess.SetState(memory.SessionStateKeyAutoMemoryLastExtractAt, []byte("not-a-timestamp"))
	assert.True(t, readLastExtractAt(sess).IsZero())
}

func TestReadLastExtractAt_EmptyValueReturnsZero(t *testing.T) {
	sess := &session.Session{State: session.StateMap{
		memory.SessionStateKeyAutoMemoryLastExtractAt: []byte{},
	}}
	assert.True(t, readLastExtractAt(sess).IsZero())
}

func TestReadWriteLastExtractAtRoundTrip(t *testing.T) {
	sess := &session.Session{}
	now := time.Date(2024, 5, 7, 12, 34, 56, 0, time.UTC)
	writeLastExtractAt(sess, now)
	assert.True(t, readLastExtractAt(sess).Equal(now))
}

func TestWriteLastExtractAt_NilSessionIsSafe(t *testing.T) {
	assert.NotPanics(t, func() { writeLastExtractAt(nil, time.Now()) })
}

func TestScanDeltaSince_NilSession(t *testing.T) {
	ts, msgs := scanDeltaSince(nil, time.Time{})
	assert.True(t, ts.IsZero())
	assert.Nil(t, msgs)
}

func TestScanDeltaSince_FiltersBySinceAndSkipsNonChat(t *testing.T) {
	sess := &session.Session{}
	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)

	sess.Events = []event.Event{
		// Before since - ignored.
		mkEvent(old, mkMsg(model.RoleUser, "way before")),
		// Tool role - skipped.
		mkEvent(newer, mkMsg(model.RoleTool, "tool-output")),
		// Event with tool calls - skipped.
		{
			Timestamp: newer,
			Response: &model.Response{
				Choices: []model.Choice{{Message: model.Message{
					Role:      model.RoleAssistant,
					ToolCalls: []model.ToolCall{{ID: "x"}},
				}}},
			},
		},
		// Event with ToolID set - skipped.
		{
			Timestamp: newer,
			Response: &model.Response{
				Choices: []model.Choice{{Message: model.Message{
					Role:   model.RoleAssistant,
					ToolID: "t1",
				}}},
			},
		},
		// Non-chat role (system) - skipped.
		mkEvent(newer, mkMsg(model.RoleSystem, "sys")),
		// Empty content and empty parts - skipped.
		mkEvent(newer, mkMsg(model.RoleUser, "")),
		// Event with nil Response - latest ts still updates but no msgs.
		{Timestamp: newer.Add(time.Second), Response: nil},
		// Valid user message at `mid`.
		mkEvent(mid, mkMsg(model.RoleUser, "hello")),
		// Valid assistant message at `newer`.
		mkEvent(newer, mkMsg(model.RoleAssistant, "hi there")),
	}

	latest, msgs := scanDeltaSince(sess, old)
	require.Len(t, msgs, 2)
	assert.Equal(t, "hello", msgs[0].Content)
	assert.Equal(t, "hi there", msgs[1].Content)
	// latest ts must advance to the highest event timestamp encountered,
	// which is `newer.Add(time.Second)` (the nil-response event).
	assert.True(t, latest.Equal(newer.Add(time.Second)), "latest ts = %v", latest)
}

func TestScanDeltaSince_ZeroSinceIncludesAll(t *testing.T) {
	sess := &session.Session{}
	t1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	sess.Events = []event.Event{mkEvent(t1, mkMsg(model.RoleUser, "msg"))}
	latest, msgs := scanDeltaSince(sess, time.Time{})
	require.Len(t, msgs, 1)
	assert.True(t, latest.Equal(t1))
}
