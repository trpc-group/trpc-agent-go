//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/backends"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/harness"
)

func TestRunSingleTurn(t *testing.T) {
	bs, err := backends.EnabledBackends(harness.NewMockSummarizer())
	require.NoError(t, err)
	defer func() {
		for _, b := range bs {
			_ = b.Close()
		}
	}()

	c := &harness.ReplayCase{
		Name: "t",
		Key:  harness.CaseKey{AppName: "a", UserID: "u", SessionID: "s"},
		Operations: []harness.Operation{
			{Type: "append_event", Event: &harness.EventSpec{Author: "user", Role: "user", Content: "hi"}},
			{Type: "append_event", Event: &harness.EventSpec{Author: "assistant", Role: "assistant", Content: "hello"}},
		},
	}
	for _, b := range bs {
		snap, err := harness.Run(context.Background(), b, c)
		require.NoError(t, err, b.Name)
		require.Len(t, snap.Events, 2, b.Name)
		require.Equal(t, "hi", snap.Events[0].Content, b.Name)
		require.Equal(t, "hello", snap.Events[1].Content, b.Name)
	}
}
