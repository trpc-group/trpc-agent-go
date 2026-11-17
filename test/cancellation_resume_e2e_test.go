//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/tooljournal"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type cancellationRestartModel struct{ response *model.Response }

func (m *cancellationRestartModel) Info() model.Info { return model.Info{Name: "restart-model"} }
func (m *cancellationRestartModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- m.response.Clone()
	close(ch)
	return ch, nil
}

func TestCancellationResumeSQLiteRestart(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed-and-not-started", true: "intent-without-result"}[uncertain], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.db")
			open := func() *sqlite.Service {
				db, err := sql.Open("sqlite3", path)
				require.NoError(t, err)
				svc, err := sqlite.NewService(db, sqlite.WithEnableAsyncPersist(false))
				require.NoError(t, err)
				return svc
			}
			svc := open()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var callsA, callsB atomic.Int32
			a := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { callsA.Add(1); cancel(); return "persisted-a", nil }, function.WithName("a"))
			b := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { callsB.Add(1); return "persisted-b", nil }, function.WithName("b"))
			calls := []model.ToolCall{}
			for _, name := range []string{"a", "b"} {
				calls = append(calls, model.ToolCall{ID: name, Type: "function", Function: model.FunctionDefinitionParam{Name: name, Arguments: []byte(`{}`)}})
			}
			first := &cancellationRestartModel{response: &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, ToolCalls: calls}}}}}
			seed := runner.NewRunner("restart", llmagent.New("agent", llmagent.WithModel(first), llmagent.WithTools([]tool.Tool{a, b})), runner.WithSessionService(svc), runner.WithToolExecutionTracking(true))
			ch, err := seed.Run(ctx, "user", "session", model.NewUserMessage("write"), agent.WithRequestID("seed"))
			require.NoError(t, err)
			for range ch {
			}
			waitCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			_, err = seed.(runner.ExecutionObserver).WaitExecution(waitCtx, "seed")
			require.NoError(t, err)
			require.EqualValues(t, 1, callsA.Load())
			require.Zero(t, callsB.Load())
			key := session.Key{AppName: "restart", UserID: "user", SessionID: "session"}
			if uncertain {
				sess, err := svc.GetSession(context.Background(), key)
				require.NoError(t, err)
				records, err := tooljournal.Records(sess)
				require.NoError(t, err)
				require.Len(t, records, 1)
				record := records[0]
				record.Calls[0].Status = "dispatched"
				record.Calls[0].Choices = nil
				data, err := json.Marshal(record)
				require.NoError(t, err)
				e := event.New("seed", "agent")
				e.StateDelta = map[string][]byte{tooljournal.Prefix + record.ID: data}
				require.NoError(t, svc.AppendEvent(context.Background(), sess, e))
			}
			require.NoError(t, seed.Close())
			require.NoError(t, svc.Close())
			// Close the database and discard the Runner; no live coordinator survives.
			restored := open()
			defer restored.Close()
			final := &cancellationRestartModel{response: &model.Response{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("done")}}}}
			resumed := runner.NewRunner("restart", llmagent.New("agent", llmagent.WithModel(final), llmagent.WithTools([]tool.Tool{a, b})), runner.WithSessionService(restored), runner.WithToolExecutionTracking(true))
			defer resumed.Close()
			ch, err = resumed.Run(context.Background(), "user", "session", model.Message{}, agent.WithResume(true), agent.WithStrictToolResume(true))
			require.NoError(t, err)
			rejected := false
			for e := range ch {
				if e.Response != nil && e.Error != nil {
					rejected = strings.Contains(e.Error.Message, "verify external effects")
					if !uncertain {
						t.Error(e.Error.Message)
					}
				}
			}
			require.EqualValues(t, 1, callsA.Load(), "completed or uncertain write must not repeat")
			if uncertain {
				require.True(t, rejected)
				require.Zero(t, callsB.Load())
			} else {
				require.EqualValues(t, 1, callsB.Load())
			}
		})
	}
}
