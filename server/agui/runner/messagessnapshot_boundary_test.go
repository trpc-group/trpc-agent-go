//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/track"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestMessagesSnapshotFollowPreservesEqualTimeBatch(t *testing.T) {
	for _, terminal := range []string{"none", "finished", "error"} {
		t.Run(terminal, func(t *testing.T) {
			ctx := context.Background()
			svc := inmemory.NewSessionService()
			defer svc.Close()
			key := session.Key{AppName: "demo", UserID: "user", SessionID: "thread"}
			sess, err := svc.CreateSession(ctx, key, session.StateMap{})
			require.NoError(t, err)
			base := time.Now().Add(-time.Second)
			boundary := newTrackEventAt(t, aguievents.NewCustomEvent("already-seen"), base.Add(-time.Nanosecond))
			require.NoError(t, svc.AppendTrackEvent(ctx, sess, &boundary))
			// Identical payloads may be separate persisted events.
			repeated := newTrackEventAt(t, aguievents.NewCustomEvent("same-payload"), base)
			for i := 0; i < 2; i++ {
				require.NoError(t, svc.AppendTrackEvent(ctx, sess, &repeated))
			}
			if terminal != "none" {
				var evt aguievents.Event = aguievents.NewRunFinishedEvent("thread", "owner")
				if terminal == "error" {
					evt = aguievents.NewRunErrorEvent("failed", aguievents.WithRunID("owner"))
				}
				stored := newTrackEventAt(t, evt, base)
				require.NoError(t, svc.AppendTrackEvent(ctx, sess, &stored))
			}
			tracker, err := track.New(svc)
			require.NoError(t, err)
			r := &runner{tracker: tracker}
			input := &runInput{key: key, threadID: "thread", runID: "follower"}
			cursor := boundary.Timestamp
			output := make(chan aguievents.Event, 8)
			keepFollowing := r.handleMessagesSnapshotFollowTick(ctx, input, output, &cursor)
			require.Equal(t, terminal == "none", keepFollowing)
			require.Equal(t, base, cursor)
			expected := 2
			if terminal != "none" {
				expected++
			}
			require.Len(t, output, expected)
			for i := 0; i < 2; i++ {
				evt, ok := (<-output).(*aguievents.CustomEvent)
				require.True(t, ok)
				require.Equal(t, "same-payload", evt.Name)
			}
			switch terminal {
			case "none":
				// The backend returns the boundary again; a subsequent poll must
				// not replay the batch that was just forwarded.
				require.True(t, r.handleMessagesSnapshotFollowTick(ctx, input, output, &cursor))
				require.Empty(t, output)
			case "finished":
				evt, ok := (<-output).(*aguievents.RunFinishedEvent)
				require.True(t, ok)
				require.Equal(t, "follower", evt.RunID())
			case "error":
				evt, ok := (<-output).(*aguievents.RunErrorEvent)
				require.True(t, ok)
				require.Equal(t, "failed", evt.Message)
				require.Equal(t, "follower", evt.RunID())
			}
		})
	}
}
