//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package trackpage

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestCursorDoesNotBindSessionGeneration(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)

	raw, err := CursorFor("test", key, "alpha", createdAt, "event-1")
	require.NoError(t, err)

	data, err := base64.RawURLEncoding.DecodeString(raw)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))
	assert.NotContains(t, payload, "sessionCreatedAt")

	cursor, err := Decode(raw)
	require.NoError(t, err)
	assert.NoError(t, ValidateBinding(cursor, "test", key, "alpha"))
	assert.Equal(t, TimeToUnixNano(createdAt), cursor.CreatedAt)
	assert.Equal(t, "event-1", cursor.ID)
}

func TestValidateBindingRejectsWrongRequestBinding(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	cursor := Cursor{
		Version:   version,
		Kind:      "test",
		AppName:   key.AppName,
		UserID:    key.UserID,
		SessionID: key.SessionID,
		Track:     "alpha",
		CreatedAt: 1,
		ID:        "event-1",
	}

	assert.Error(t, ValidateBinding(cursor, "other", key, "alpha"))
	assert.Error(t, ValidateBinding(cursor, "test", session.Key{
		AppName:   key.AppName,
		UserID:    key.UserID,
		SessionID: "other-session",
	}, "alpha"))
	assert.Error(t, ValidateBinding(cursor, "test", key, "beta"))
}
