//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package backends

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestLocalBackendsConstruct(t *testing.T) {
	bs, err := EnabledBackends(testSummarizer{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(bs), 2)
	require.Equal(t, "inmemory", bs[0].Name)
	require.Equal(t, "sqlite", bs[1].Name)
	defer func() {
		for _, b := range bs {
			_ = b.Close()
		}
	}()
	// Each backend can create a session.
	for _, b := range bs {
		key := session.Key{AppName: "a", UserID: "u", SessionID: "s"}
		_, err := b.Session.CreateSession(context.Background(), key, session.StateMap{})
		require.NoError(t, err, b.Name)
	}
}
