//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package util

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestTrackCursorRoundTripAndScope(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	initial, err := DecodeTrackCursor("", "backend:prefix", key, "track")
	require.NoError(t, err)
	require.Equal(t, "0", initial.Position)
	result, err := TrackCursorResult([]string{`{"Generation":"generation","Epoch":"epoch","Position":"42"}`, `{"track":"track","payload":{"value":1}}`}, initial, "track")
	require.NoError(t, err)
	require.Equal(t, session.Track("track"), result.Track)
	require.Len(t, result.Events, 1)
	decoded, err := DecodeTrackCursor(result.Cursor, "backend:prefix", key, "track")
	require.NoError(t, err)
	require.Equal(t, "42", decoded.Position)
	require.Equal(t, "generation", decoded.Generation)
	require.Equal(t, "epoch", decoded.Epoch)
	for _, altered := range []session.Key{{AppName: "other", UserID: key.UserID, SessionID: key.SessionID}, {AppName: key.AppName, UserID: "other", SessionID: key.SessionID}, {AppName: key.AppName, UserID: key.UserID, SessionID: "other"}} {
		_, err := DecodeTrackCursor(result.Cursor, "backend:prefix", altered, "track")
		require.Error(t, err)
	}
	_, err = DecodeTrackCursor(result.Cursor, "backend:other-prefix", key, "track")
	require.Error(t, err)
	_, err = DecodeTrackCursor(result.Cursor, "backend:prefix", key, "other-track")
	require.Error(t, err)
	for _, raw := range []string{"{", `{}`, `{"Generation":"g","Position":"1"}`} {
		_, err := DecodeTrackCursor(base64.RawURLEncoding.EncodeToString([]byte(raw)), "backend:prefix", key, "track")
		require.Error(t, err)
	}
	_, err = DecodeTrackCursor("!", "backend:prefix", key, "track")
	require.Error(t, err)
	for _, field := range []string{"Generation", "Position"} {
		fields := map[string]string{"Scope": decoded.Scope, "Generation": "g", "Position": "1"}
		delete(fields, field)
		raw, err := json.Marshal(fields)
		require.NoError(t, err)
		_, err = DecodeTrackCursor(base64.RawURLEncoding.EncodeToString(raw), "backend:prefix", key, "track")
		require.Error(t, err)
	}
}

func TestTrackCursorResultRejectsCorruption(t *testing.T) {
	for _, raw := range [][]string{nil, {"{"}, {`{"Generation":"g","Position":"1"}`, "{"}} {
		_, err := TrackCursorResult(raw, TrackCursor{}, "track")
		require.Error(t, err)
	}
}
