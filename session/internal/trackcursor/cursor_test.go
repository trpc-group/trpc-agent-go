//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package trackcursor

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestCursorBindings(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	c, err := Decode("", "backend", key, "track")
	require.NoError(t, err)
	c.Generation = "generation"
	c.Position = 5
	raw := Encode(c)
	decoded, err := Decode(raw, "backend", key, "track")
	require.NoError(t, err)
	require.Equal(t, c, decoded)
	for _, invalid := range []string{"not-base64!", base64.RawURLEncoding.EncodeToString([]byte(`{}`)), strings.Repeat("x", 8193)} {
		_, err := Decode(invalid, "backend", key, "track")
		require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
	}
	_, err = Decode(raw, "other", key, "track")
	require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
	_, err = Decode(raw, "backend", key, "other")
	require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
	c.Generation = ""
	_, err = Decode(Encode(c), "backend", key, "track")
	require.ErrorIs(t, err, session.ErrTrackCursorInvalid)
}
