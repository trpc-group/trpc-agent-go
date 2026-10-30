//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responses

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// legacyChatModel stands in for an existing Chat Completions model: it calls
// the calculator once and then answers in plain text.
type legacyChatModel struct {
	mu       sync.Mutex
	requests []*model.Request
}

func (m *legacyChatModel) Info() model.Info { return model.Info{Name: "chat-model"} }

func (m *legacyChatModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	call := len(m.requests)
	m.mu.Unlock()
	msg := model.NewAssistantMessage("2")
	if call == 1 {
		msg = model.Message{
			Role:             model.RoleAssistant,
			ReasoningContent: "chat reasoning",
			ToolCalls: []model.ToolCall{{
				Type: "function",
				ID:   "call_chat",
				Function: model.FunctionDefinitionParam{
					Name:      "calculator",
					Arguments: []byte(`{"a":1}`),
				},
			}},
		}
	}
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done:    true,
		Object:  model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{Index: 0, Message: msg}},
	}
	close(ch)
	return ch, nil
}

func TestRunnerUnusedResponsesModelKeepsLegacySession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var responsesCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		responsesCalls.Add(1)
		http.Error(w, "an unused Responses model must not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()
	chat := &legacyChatModel{}
	var toolCalls atomic.Int32
	a := llmagent.New("assistant",
		llmagent.WithModel(chat),
		llmagent.WithModels(map[string]model.Model{
			"chat":      chat,
			"responses": New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL)),
		}),
		llmagent.WithTools([]tool.Tool{integrationCalculator(&toolCalls)}),
	)
	path := filepath.Join(t.TempDir(), "legacy.db")
	openService := func() *sqlite.Service {
		db, err := sql.Open("sqlite3", path)
		require.NoError(t, err)
		svc, err := sqlite.NewService(db)
		require.NoError(t, err)
		return svc
	}

	for _, text := range []string{"add one", "explain the previous answer"} {
		svc := openService()
		r := runner.NewRunner("legacy", a, runner.WithSessionService(svc))
		for _, evt := range runIntegration(t, r, ctx, text) {
			require.Nil(t, evt.Error)
		}
		require.NoError(t, r.Close())
		require.NoError(t, svc.Close())
	}

	require.Zero(t, responsesCalls.Load())
	require.EqualValues(t, 1, toolCalls.Load())
	require.Len(t, chat.requests, 3)
	last := chat.requests[2].Messages
	var sawCall, sawResult bool
	for _, msg := range last {
		require.Nil(t, msg.Replay)
		sawCall = sawCall || len(msg.ToolCalls) == 1 && msg.ToolCalls[0].ID == "call_chat"
		sawResult = sawResult || msg.Role == model.RoleTool && msg.ToolID == "call_chat"
	}
	require.True(t, sawCall && sawResult, "the reopened legacy history keeps its tool round")

	svc := openService()
	defer svc.Close()
	sess, err := svc.GetSession(ctx, session.Key{AppName: "legacy", UserID: "user", SessionID: "session"})
	require.NoError(t, err)
	require.NotNil(t, sess)
	data, err := json.Marshal(sess.Events)
	require.NoError(t, err)
	require.False(t, strings.Contains(string(data), `"replay"`),
		"a legacy session must not persist native replay fields")
}
