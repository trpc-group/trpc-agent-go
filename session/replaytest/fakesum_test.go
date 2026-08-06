//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

// TestFakeSummarizer exercises the deterministic summarizer: per-session
// call counter, event count in the output, fixed metadata and the no-op
// setters.
func TestFakeSummarizer(t *testing.T) {
	f := replaytest.NewFakeSummarizer()

	sess := &session.Session{ID: "s1", Events: make([]event.Event, 2)}
	assert.True(t, f.ShouldSummarize(sess))

	// No-op setters must not panic.
	f.SetPrompt("custom prompt")
	f.SetModel(nil)

	md := f.Metadata()
	require.NotNil(t, md)
	assert.Equal(t, "replaytest-fake-summarizer", md["name"])

	text, err := f.Summarize(context.Background(), sess)
	require.NoError(t, err)
	assert.Contains(t, text, "FAKE-SUMMARY[s1]#1")
	assert.Contains(t, text, "events=2")

	text, err = f.Summarize(context.Background(), sess)
	require.NoError(t, err)
	assert.Contains(t, text, "FAKE-SUMMARY[s1]#2")

	// The counter is per session ID.
	other := &session.Session{ID: "s2"}
	text, err = f.Summarize(context.Background(), other)
	require.NoError(t, err)
	assert.Contains(t, text, "FAKE-SUMMARY[s2]#1")
	assert.Contains(t, text, "events=0")
}
