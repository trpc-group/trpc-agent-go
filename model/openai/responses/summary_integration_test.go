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
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const multiCallOutput = `[
	{"type":"reasoning","id":"rs_multi","summary":[],"encrypted_content":"opaque-multi-state"},
	{"type":"function_call","id":"fc_a","status":"completed","call_id":"call_a","name":"calculator","arguments":"{\"a\":1}"},
	{"type":"function_call","id":"fc_b","status":"completed","call_id":"call_b","name":"calculator","arguments":"{\"a\":2}"}
]`

func messageOutput(id, text string) string {
	return `[{"type":"message","id":"` + id + `","status":"completed","role":"assistant",` +
		`"content":[{"type":"output_text","text":"` + text + `"}]}]`
}

func TestSummaryCutoffInsideNativeMultiCallRoundRestoresTheRound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	outputs := []string{
		messageOutput("msg_first", "first answer"),
		multiCallOutput,
		messageOutput("msg_done", "both done"),
		messageOutput("msg_next", "next answer"),
	}
	var mu sync.Mutex
	var inputs []json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		inputs = append(inputs, body["input"])
		call := len(inputs)
		mu.Unlock()
		if call > len(outputs) {
			http.Error(w, "unexpected extra model call", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"resp_%d","status":"completed","output":%s}`, call, outputs[call-1])
	}))
	defer srv.Close()
	recorded := func() []json.RawMessage {
		mu.Lock()
		defer mu.Unlock()
		return append([]json.RawMessage(nil), inputs...)
	}

	var toolCalls atomic.Int32
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	a := llmagent.New("native", llmagent.WithModel(m),
		llmagent.WithTools([]tool.Tool{integrationCalculator(&toolCalls)}))
	path := filepath.Join(t.TempDir(), "summary.db")
	openService := func() *sqlite.Service {
		db, err := sql.Open("sqlite3", path)
		require.NoError(t, err)
		svc, err := sqlite.NewService(db)
		require.NoError(t, err)
		return svc
	}
	svc := openService()
	r := runner.NewRunner("native", a, runner.WithSessionService(svc))
	for _, text := range []string{"first question", "compute both"} {
		for _, evt := range runIntegration(t, r, ctx, text) {
			require.Nil(t, evt.Error)
		}
	}
	require.EqualValues(t, 2, toolCalls.Load())
	require.Len(t, recorded(), 3)
	require.NoError(t, r.Close())
	require.NoError(t, svc.Close())

	svc = openService()
	t.Cleanup(func() { _ = svc.Close() })
	sess, err := svc.GetSession(ctx, session.Key{AppName: "native", UserID: "user", SessionID: "session"})
	require.NoError(t, err)
	require.NotNil(t, sess)
	var round *event.Event
	var results []*event.Event
	for i := range sess.Events {
		evt := &sess.Events[i]
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		msg := evt.Response.Choices[0].Message
		if len(msg.ToolCalls) == 2 && msg.Replay != nil {
			round = evt
		}
		if msg.Role == model.RoleTool {
			results = append(results, evt)
		}
	}
	require.NotNil(t, round, "the reloaded session keeps the native round")
	require.NotEmpty(t, results)
	for _, result := range results {
		require.True(t, result.Timestamp.After(round.Timestamp),
			"the summary cutoff must fall inside the native round")
	}
	// The summary covers the native call event, but not its tool results.
	const summary = "Earlier turns were summarized."
	sess.Summaries = map[string]*session.Summary{round.FilterKey: {
		Summary:   summary,
		UpdatedAt: round.Timestamp,
		Boundary:  session.NewSummaryBoundaryWithEventID(round.FilterKey, round.Timestamp, round.ID),
	}}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationModel(m),
		agent.WithInvocationMessage(model.NewUserMessage("third question")),
		agent.WithInvocationEventFilterKey(round.FilterKey),
	)
	inv.AgentName = "native"
	req := &model.Request{}
	processor.NewContentRequestProcessor(processor.WithAddSessionSummary(true)).
		ProcessRequest(ctx, inv, req, make(chan *event.Event, 16))
	ch, err := m.GenerateContent(ctx, req)
	require.NoError(t, err)
	for resp := range ch {
		require.Nil(t, resp.Error)
	}
	sent := recorded()
	require.Len(t, sent, 4)
	next := sent[3]
	for _, summarized := range []string{"first question", "first answer"} {
		require.NotContains(t, string(next), summarized, "the summarized prefix must be omitted")
	}

	var items []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(next, &items))
	var native []map[string]json.RawMessage
	results2 := map[string]string{}
	summaryIndex, anchorIndex, nativeIndex, doneIndex, questionIndex := -1, -1, -1, -1, -1
	var outputIndexes []int
	for i, item := range items {
		id := string(item["id"])
		switch {
		case strings.Contains(string(item["content"]), summary):
			summaryIndex = i
		case string(item["content"]) == `"compute both"`:
			anchorIndex = i
		case id == `"rs_multi"` || id == `"fc_a"` || id == `"fc_b"`:
			if nativeIndex < 0 {
				nativeIndex = i
			}
			native = append(native, item)
		case string(item["type"]) == `"function_call_output"`:
			var callID, output string
			require.NoError(t, json.Unmarshal(item["call_id"], &callID))
			require.NoError(t, json.Unmarshal(item["output"], &output))
			results2[callID] = output
			outputIndexes = append(outputIndexes, i)
		case id == `"msg_done"`:
			doneIndex = i
		case strings.Contains(string(item["content"]), "third question"):
			questionIndex = i
		}
	}
	encoded, err := json.Marshal(native)
	require.NoError(t, err)
	require.JSONEq(t, multiCallOutput, string(encoded), "the round is restored with its complete native items")
	require.Equal(t, map[string]string{"call_a": "2", "call_b": "3"}, results2)
	require.Equal(t, []int{nativeIndex + 3, nativeIndex + 4}, outputIndexes,
		"both call outputs directly follow the restored native items")
	require.GreaterOrEqual(t, summaryIndex, 0)
	// The summarized user request of the cut turn stays as its anchor.
	require.Equal(t, []int{summaryIndex + 1, summaryIndex + 2}, []int{anchorIndex, nativeIndex})
	require.Greater(t, doneIndex, outputIndexes[1])
	require.Greater(t, questionIndex, doneIndex)
}
