//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package croncmd

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
)

func TestParse(t *testing.T) {
	t.Parallel()

	cmd, err := Parse("")
	require.NoError(t, err)
	require.Equal(t, ActionList, cmd.Action)

	cmd, err = Parse("status 2")
	require.NoError(t, err)
	require.Equal(t, ActionStatus, cmd.Action)
	require.Equal(t, "2", cmd.Selector)

	cmd, err = Parse("remove cpu-report")
	require.NoError(t, err)
	require.Equal(t, ActionRemove, cmd.Action)
	require.Equal(t, "cpu-report", cmd.Selector)

	_, err = Parse("unknown 1")
	require.ErrorIs(t, err, ErrUnknownAction)
}

func TestResolveSelector(t *testing.T) {
	t.Parallel()

	jobs := []gwclient.ScheduledJobSummary{
		{ID: "aabbccdd-1", Name: "cpu report"},
		{ID: "bbccddee-2", Name: "memory report"},
	}

	job, err := ResolveSelector(jobs, "1")
	require.NoError(t, err)
	require.Equal(t, "aabbccdd-1", job.ID)

	job, err = ResolveSelector(jobs, "bbccddee")
	require.NoError(t, err)
	require.Equal(t, "bbccddee-2", job.ID)

	job, err = ResolveSelector(jobs, "memory report")
	require.NoError(t, err)
	require.Equal(t, "bbccddee-2", job.ID)

	_, err = ResolveSelector(jobs, "")
	require.ErrorIs(t, err, ErrSelectorEmpty)

	_, err = ResolveSelector(jobs, "missing")
	require.ErrorIs(t, err, ErrSelectorMiss)
}

func TestResolveSelectorRejectsAmbiguousPrefix(t *testing.T) {
	t.Parallel()

	jobs := []gwclient.ScheduledJobSummary{
		{ID: "abc11111-1"},
		{ID: "abc22222-2"},
	}

	_, err := ResolveSelector(jobs, "abc")
	require.ErrorIs(t, err, ErrSelectorMany)
}

func TestResolveSelectorRejectsAmbiguousName(t *testing.T) {
	t.Parallel()

	jobs := []gwclient.ScheduledJobSummary{
		{ID: "a1", Name: "cpu"},
		{ID: "b2", Name: "CPU"},
	}

	_, err := ResolveSelector(jobs, "cpu")
	require.ErrorIs(t, err, ErrSelectorMany)
}

func TestNeedsSelector(t *testing.T) {
	t.Parallel()

	require.True(t, NeedsSelector(ActionStatus))
	require.True(t, NeedsSelector(ActionStop))
	require.True(t, NeedsSelector(ActionResume))
	require.True(t, NeedsSelector(ActionRemove))
	require.False(t, NeedsSelector(ActionList))
	require.False(t, NeedsSelector(ActionClear))
}

func TestShortID(t *testing.T) {
	t.Parallel()

	require.Equal(t, "12345678", ShortID("12345678-rest"))
	require.Equal(t, "short", ShortID("short"))
	require.Equal(t, "", ShortID(" "))
}
