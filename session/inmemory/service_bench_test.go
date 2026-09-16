//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inmemory

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var appendEventBenchmarkSink int

func BenchmarkAppendEvent(b *testing.B) {
	for _, tc := range []struct {
		historySize     int
		stateDeltaBytes int
	}{
		{historySize: 16},
		{historySize: 256, stateDeltaBytes: 1024},
		{historySize: 1024, stateDeltaBytes: 64 * 1024},
	} {
		name := fmt.Sprintf(
			"history=%d/state_delta_bytes=%d",
			tc.historySize,
			tc.stateDeltaBytes,
		)
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			service := NewSessionService(WithSessionEventLimit(tc.historySize))
			b.Cleanup(func() {
				_ = service.Close()
			})
			key := session.Key{
				AppName:   "benchmark-app",
				UserID:    "benchmark-user",
				SessionID: "benchmark-session",
			}
			sess, err := service.CreateSession(ctx, key, nil)
			if err != nil {
				b.Fatal(err)
			}
			app, ok := service.getAppSessions(key.AppName)
			if !ok {
				b.Fatal("benchmark app is missing")
			}
			stored := app.sessions[key.UserID][key.SessionID].session
			baseEvents := appendEventBenchmarkHistory(tc.historySize)
			sess.Events = append([]event.Event(nil), baseEvents...)
			stored.Events = append([]event.Event(nil), baseEvents...)
			evt := &event.Event{
				ID:           "benchmark-event",
				InvocationID: "benchmark-invocation",
				Author:       "benchmark-agent",
				StateDelta: map[string][]byte{
					"benchmark": bytes.Repeat([]byte{'s'}, tc.stateDeltaBytes),
				},
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewUserMessage("benchmark event"),
					}},
				},
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := service.AppendEvent(
					ctx,
					sess,
					evt,
					session.WithEventNum(tc.historySize),
				); err != nil {
					b.Fatal(err)
				}
				appendEventBenchmarkSink = len(sess.Events) + len(stored.Events)
			}
		})
	}
}

func appendEventBenchmarkHistory(historySize int) []event.Event {
	events := make([]event.Event, historySize)
	for i := range events {
		events[i] = event.Event{
			ID: fmt.Sprintf("history-event-%d", i),
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewUserMessage(fmt.Sprintf("history-%d", i)),
				}},
			},
		}
	}
	return events
}
