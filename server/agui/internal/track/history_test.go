//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package track

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestAdmitHistoryRunWhenAnotherWriterCreatesSession(t *testing.T) {
	const runID = "run-id"
	ctx := WithHistoryRunID(context.Background(), runID)
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	svc := newHookSessionService()
	svc.createSessionFn = func(ctx context.Context, key session.Key, _ session.StateMap, _ ...session.Option) (*session.Session, error) {
		// The other writer creates the session after the first lookup.
		_, err := svc.SessionService.CreateSession(ctx, key, session.StateMap{"existing": []byte("value")})
		if err != nil {
			return nil, err
		}
		return nil, errors.New("session already exists")
	}
	tr, err := New(svc)
	require.NoError(t, err)
	history := tr.(*tracker)
	require.NoError(t, history.AdmitHistoryRun(ctx, key))
	require.NoError(t, history.AppendEvent(ctx, key, aguievents.NewRunStartedEvent("thread", "wire-run")))
	require.NoError(t, history.Flush(ctx, key))
	events, err := history.GetEvents(ctx, key)
	require.NoError(t, err)
	require.Len(t, events.Events, 1)
	storedRunID, order := HistoryRun(events.Events[0].Payload)
	require.Equal(t, runID, storedRunID)
	require.NotEmpty(t, order.Epoch)
	require.EqualValues(t, 1, order.Sequence)
	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	persisted, ok := sess.GetState(historyRunOrderKey)
	require.True(t, ok)
	var persistedOrder HistoryRunOrder
	require.NoError(t, json.Unmarshal(persisted, &persistedOrder))
	require.Equal(t, order, persistedOrder)
	value, _ := sess.GetState("existing")
	require.Equal(t, []byte("value"), value)
}

func TestAdmitHistoryRunOrdersConcurrentTrackers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	svc := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	_, err := svc.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	ids := []string{"first-writer", "second-writer"}
	writers := make([]*tracker, len(ids))
	start := make(chan struct{})
	admitted := make(chan error, len(ids))
	for i, id := range ids {
		tr, err := New(svc)
		require.NoError(t, err)
		writers[i] = tr.(*tracker)
		go func(writer *tracker, id string) {
			<-start
			admitted <- writer.AdmitHistoryRun(WithHistoryRunID(ctx, id), key)
		}(writers[i], id)
	}
	close(start)
	for range writers {
		select {
		case err := <-admitted:
			require.NoError(t, err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	for i, writer := range writers {
		require.NoError(t, writer.AppendEvent(ctx, key, aguievents.NewRunStartedEvent("thread", ids[i])))
		require.NoError(t, writer.Flush(ctx, key))
	}

	events, err := writers[0].GetEvents(ctx, key)
	require.NoError(t, err)
	require.Len(t, events.Events, len(ids))
	orders := make(map[string]HistoryRunOrder, len(ids))
	for _, event := range events.Events {
		id, order := HistoryRun(event.Payload)
		orders[id] = order
	}
	first, second := orders[ids[0]], orders[ids[1]]
	require.NotEmpty(t, first.Epoch)
	require.Equal(t, first.Epoch, second.Epoch)
	require.ElementsMatch(t, []uint64{1, 2}, []uint64{first.Sequence, second.Sequence})
}

func TestAdmitHistoryRunUsesStateHiddenByGetSessionHook(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "thread"}
	svc := inmemory.NewSessionService(inmemory.WithGetSessionHook(
		func(_ *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
			sess, err := next()
			if err != nil || sess == nil {
				return sess, err
			}
			for key := range sess.SnapshotState() {
				if strings.HasPrefix(key, session.StateTempPrefix) {
					sess.DeleteState(key)
				}
			}
			return sess, nil
		},
	))
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	var history *tracker
	for _, id := range []string{"first-writer", "second-writer"} {
		tr, err := New(svc)
		require.NoError(t, err)
		history = tr.(*tracker)
		runCtx := WithHistoryRunID(ctx, id)
		require.NoError(t, history.AdmitHistoryRun(runCtx, key))
		require.NoError(t, history.AppendEvent(runCtx, key, aguievents.NewRunStartedEvent("thread", id)))
		require.NoError(t, history.Flush(runCtx, key))
	}
	events, err := history.GetEvents(ctx, key)
	require.NoError(t, err)
	require.Len(t, events.Events, 2)
	orders := make(map[string]HistoryRunOrder, len(events.Events))
	for _, event := range events.Events {
		id, order := HistoryRun(event.Payload)
		orders[id] = order
	}
	first, second := orders["first-writer"], orders["second-writer"]
	require.NotEmpty(t, first.Epoch)
	require.Equal(t, first.Epoch, second.Epoch)
	require.EqualValues(t, 1, first.Sequence)
	require.EqualValues(t, 2, second.Sequence)
}
