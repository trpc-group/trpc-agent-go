//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package taskrun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStatusIsTerminal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		status   Status
		terminal bool
	}{
		{name: "queued", status: StatusQueued},
		{name: "running", status: StatusRunning},
		{name: "finalizing", status: StatusFinalizing},
		{name: "canceling", status: StatusCanceling},
		{name: "completed", status: StatusCompleted, terminal: true},
		{name: "failed", status: StatusFailed, terminal: true},
		{name: "canceled", status: StatusCanceled, terminal: true},
		{name: "unknown", status: Status("unknown")},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.terminal, tc.status.IsTerminal())
		})
	}
}

func TestObserverFunc(t *testing.T) {
	t.Parallel()

	var got Run
	observer := ObserverFunc(func(ctx context.Context, run Run) {
		got = run
	})
	observer.OnRunUpdate(context.Background(), Run{ID: "run-1"})
	require.Equal(t, "run-1", got.ID)

	var nilObserver ObserverFunc
	require.NotPanics(t, func() {
		nilObserver.OnRunUpdate(context.Background(), Run{ID: "run-2"})
	})
}
