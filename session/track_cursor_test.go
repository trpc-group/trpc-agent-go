//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package session

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTrackCursorMetadataIsLocalAndCloned(t *testing.T) {
	option := WithTrackCursor("original")
	a, b := &Options{}, &Options{}
	option(a)
	option(b)
	*a.TrackCursor = "changed"
	require.Equal(t, "original", *b.TrackCursor)
	optionsJSON, err := json.Marshal(b)
	require.NoError(t, err)
	require.NotContains(t, string(optionsJSON), "TrackCursor")
	require.NotContains(t, string(optionsJSON), "original")
	sess := NewSession("app", "user", "session")
	sess.Tracks = map[Track]*TrackEvents{"track": {Track: "track", Cursor: b.TrackCursor}}
	clone := sess.Clone()
	*clone.Tracks["track"].Cursor = "clone"
	require.Equal(t, "original", *sess.Tracks["track"].Cursor)
	events, err := sess.GetTrackEvents("track")
	require.NoError(t, err)
	*events.Cursor = "read"
	require.Equal(t, "original", *sess.Tracks["track"].Cursor)
	data, err := json.Marshal(sess.Tracks["track"])
	require.NoError(t, err)
	require.NotContains(t, string(data), "original")
	require.NotContains(t, string(data), "Cursor")
}
