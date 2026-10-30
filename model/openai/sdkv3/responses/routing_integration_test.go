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
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/failover"
	"trpc.group/trpc-go/trpc-agent-go/model/hedge"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type nativeRoutingRequest struct {
	body map[string]json.RawMessage
	err  error
}

func captureNativeRoutingRequest(r *http.Request) nativeRoutingRequest {
	defer r.Body.Close()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nativeRoutingRequest{err: err}
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nativeRoutingRequest{err: err}
	}
	return nativeRoutingRequest{body: body}
}

func takeNativeRoutingRequest(t *testing.T, requests <-chan nativeRoutingRequest) map[string]json.RawMessage {
	t.Helper()
	select {
	case got := <-requests:
		require.NoError(t, got.err)
		return got.body
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Responses request")
		return nil
	}
}

func recordNativeRoutingRequest(
	w http.ResponseWriter,
	r *http.Request,
	requests chan<- nativeRoutingRequest,
	calls *atomic.Int32,
	budget int32,
) (int32, bool) {
	call := calls.Add(1)
	if call > budget {
		http.Error(w, "native routing fixture request budget exceeded", http.StatusInternalServerError)
		return call, false
	}
	captured := captureNativeRoutingRequest(r)
	if captured.err != nil && r.Context().Err() != nil {
		// A hedge loser can be canceled while its HTTP request body is still
		// arriving. Ignore only that canceled, incomplete request; keep parse
		// errors visible for every request whose context is still live.
		return call, false
	}
	select {
	case requests <- captured:
		return call, true
	case <-r.Context().Done():
		return call, false
	default:
		http.Error(w, "native routing fixture request capture buffer is full", http.StatusInternalServerError)
		return call, false
	}
}

func drainNativeRoutingRequests(t *testing.T, requests <-chan nativeRoutingRequest) {
	t.Helper()
	for {
		select {
		case got := <-requests:
			require.NoError(t, got.err)
		default:
			return
		}
	}
}

func nativeRoutingToolItem(id, callID, name, arguments string) string {
	item, err := json.Marshal(map[string]any{
		"type":      "function_call",
		"id":        id,
		"call_id":   callID,
		"name":      name,
		"arguments": arguments,
		"status":    "completed",
	})
	if err != nil {
		panic(err)
	}
	return string(item)
}

func writeNativeRoutingToolRound(w http.ResponseWriter, responseID, id, callID, arguments string) {
	item := nativeRoutingToolItem(id, callID, "calculator", arguments)
	writeSSE(w,
		`{"type":"response.output_item.done","output_index":0,"item":`+item+`}`,
		`{"type":"response.completed","response":{"id":"`+responseID+`","status":"completed","output":[`+item+`]}}`,
	)
}

func writeNativeRoutingAnswer(w http.ResponseWriter, responseID, text string) {
	output, err := json.Marshal([]map[string]any{{
		"type": "message", "id": "msg_" + responseID, "status": "completed", "role": "assistant",
		"phase": "final_answer", "content": []map[string]string{{"type": "output_text", "text": text}},
	}})
	if err != nil {
		panic(err)
	}
	writeSSE(w, `{"type":"response.completed","response":{"id":"`+responseID+`","status":"completed","output":`+string(output)+`}}`)
}

func openNativeRoutingSQLite(t *testing.T, path string) *sqlite.Service {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	svc, err := sqlite.NewService(db, sqlite.WithEnableAsyncPersist(false))
	require.NoError(t, err)
	return svc
}

func newNativeRoutingRunner(m model.Model, calls *atomic.Int32, svc session.Service) runner.Runner {
	a := llmagent.New("native",
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
		llmagent.WithTools([]tool.Tool{integrationCalculator(calls)}),
	)
	return runner.NewRunner("native", a, runner.WithSessionService(svc))
}

func assertNativeRoutingText(t *testing.T, events []*event.Event, want string) {
	t.Helper()
	for _, evt := range events {
		if evt == nil {
			continue
		}
		choices := evt.Choices
		if evt.Response != nil {
			choices = evt.Response.Choices
		}
		for _, choice := range choices {
			if choice.Message.Content == want || choice.Delta.Content == want {
				return
			}
		}
	}
	t.Fatalf("did not observe expected text %q", want)
}

func assertNativeRoutingReplayInput(t *testing.T, raw json.RawMessage, itemID, callID, arguments, toolOutput string) {
	t.Helper()
	var input []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &input))
	var calls, outputs int
	for _, item := range input {
		var itemType, id string
		_ = json.Unmarshal(item["type"], &itemType)
		_ = json.Unmarshal(item["id"], &id)
		if itemType == "function_call" && id == itemID {
			calls++
			require.Equal(t, `"calculator"`, string(item["name"]))
			require.Equal(t, `"`+callID+`"`, string(item["call_id"]))
			var gotArguments string
			require.NoError(t, json.Unmarshal(item["arguments"], &gotArguments))
			require.Equal(t, arguments, gotArguments)
		}
		if itemType == "function_call_output" {
			var gotCallID string
			_ = json.Unmarshal(item["call_id"], &gotCallID)
			if gotCallID == callID {
				outputs++
				require.Equal(t, `"`+toolOutput+`"`, string(item["output"]))
			}
		}
	}
	require.Equal(t, 1, calls, "the original native function_call must appear once")
	require.Equal(t, 1, outputs, "the matching native function_call_output must appear once")
}

func assertNativeRoutingSessionContainsOnlyCall(t *testing.T, sess *session.Session, itemID, callID string) {
	t.Helper()
	encoded, err := json.Marshal(sess.Events)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "fc_loser", "a losing candidate's native item must not be persisted")
	require.NotContains(t, string(encoded), "call_loser", "a losing candidate's tool call must not be persisted")
	var replayCount, toolCallCount, toolResultCount int
	for _, evt := range sess.Events {
		choices := evt.Choices
		if evt.Response != nil {
			choices = evt.Response.Choices
		}
		for _, choice := range choices {
			msg := choice.Message
			delta := choice.Delta
			require.Empty(t, delta.ToolCalls, "native function calls are committed only in the winner's terminal message")
			require.Nil(t, delta.Replay, "partial deltas must not persist native replay")
			for _, call := range msg.ToolCalls {
				toolCallCount++
				require.Equal(t, callID, call.ID)
			}
			if msg.Role == model.RoleTool {
				toolResultCount++
				require.Equal(t, callID, msg.ToolID)
			}
			if msg.Replay != nil {
				payload, replayErr := msg.ReplayDataFor(responseProvider, "gpt-5")
				require.NoError(t, replayErr)
				require.NotContains(t, string(payload), "fc_loser")
				if bytes.Contains(payload, []byte(itemID)) {
					replayCount++
					require.Contains(t, string(payload), callID)
				}
			}
		}
	}
	require.Equal(t, 1, replayCount)
	require.Equal(t, 1, toolCallCount)
	require.Equal(t, 1, toolResultCount)
}

func TestNativeRoutingFailoverBeforePreviewPreservesToolReplayAcrossReopen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	primaryRequests := make(chan nativeRoutingRequest, 4)
	backupRequests := make(chan nativeRoutingRequest, 8)
	var primaryCalls, backupCalls, toolCalls atomic.Int32
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := recordNativeRoutingRequest(w, r, primaryRequests, &primaryCalls, 3); !ok {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"rate limited before preview","type":"rate_limit_error"}}`)
	}))
	defer primaryServer.Close()
	backupServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call, ok := recordNativeRoutingRequest(w, r, backupRequests, &backupCalls, 3)
		if !ok {
			return
		}
		if call == 1 {
			writeNativeRoutingToolRound(w, "resp_failover_tool", "fc_failover", "call_failover", `{"a":4}`)
			return
		}
		writeNativeRoutingAnswer(w, fmt.Sprintf("resp_failover_answer_%d", call), "5")
	}))
	defer backupServer.Close()

	primary := New("gpt-5", WithAPIKey("test"), WithBaseURL(primaryServer.URL), WithOpenAIOptions(option.WithMaxRetries(0)))
	backup := New("gpt-5", WithAPIKey("test"), WithBaseURL(backupServer.URL), WithOpenAIOptions(option.WithMaxRetries(0)))
	routed, err := failover.New(failover.WithCandidates(primary, backup))
	require.NoError(t, err)
	require.Equal(t, responseProvider, routed.Info().ReplayProtocol)

	path := filepath.Join(t.TempDir(), "native-failover.db")
	svc := openNativeRoutingSQLite(t, path)
	var r runner.Runner
	defer func() {
		if r != nil {
			_ = r.Close()
		}
		if svc != nil {
			_ = svc.Close()
		}
	}()
	r = newNativeRoutingRunner(routed, &toolCalls, svc)
	firstRunEvents := runIntegration(t, r, ctx, "calculate")
	for _, evt := range firstRunEvents {
		require.Nil(t, evt.Error)
	}
	assertNativeRoutingText(t, firstRunEvents, "5")
	require.EqualValues(t, 1, toolCalls.Load())
	require.EqualValues(t, 2, primaryCalls.Load(), "each model turn starts at the primary and fails before yielding a preview")
	require.EqualValues(t, 2, backupCalls.Load(), "the backup must complete the native tool round")
	primaryInitial := takeNativeRoutingRequest(t, primaryRequests)
	require.Equal(t, `true`, string(primaryInitial["stream"]))
	primaryToolContinuation := takeNativeRoutingRequest(t, primaryRequests)
	assertNativeRoutingReplayInput(t, primaryToolContinuation["input"], "fc_failover", "call_failover", `{"a":4}`, "5")
	backupInitial := takeNativeRoutingRequest(t, backupRequests)
	require.Equal(t, `true`, string(backupInitial["stream"]))
	backupToolContinuation := takeNativeRoutingRequest(t, backupRequests)
	assertNativeRoutingReplayInput(t, backupToolContinuation["input"], "fc_failover", "call_failover", `{"a":4}`, "5")

	require.NoError(t, r.Close())
	r = nil
	require.NoError(t, svc.Close())
	svc = nil
	svc = openNativeRoutingSQLite(t, path)
	r = newNativeRoutingRunner(routed, &toolCalls, svc)
	continuedEvents := runIntegration(t, r, ctx, "continue after reopen")
	for _, evt := range continuedEvents {
		require.Nil(t, evt.Error)
	}
	assertNativeRoutingText(t, continuedEvents, "5")
	require.EqualValues(t, 1, toolCalls.Load(), "reloaded native history must not execute the completed tool again")
	require.EqualValues(t, 3, primaryCalls.Load())
	require.EqualValues(t, 3, backupCalls.Load())
	primaryAfterReopen := takeNativeRoutingRequest(t, primaryRequests)
	assertNativeRoutingReplayInput(t, primaryAfterReopen["input"], "fc_failover", "call_failover", `{"a":4}`, "5")
	backupAfterReopen := takeNativeRoutingRequest(t, backupRequests)
	assertNativeRoutingReplayInput(t, backupAfterReopen["input"], "fc_failover", "call_failover", `{"a":4}`, "5")
}

func TestNativeRoutingFailoverAfterPreviewDoesNotSwitchOrExecuteTool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var primaryCalls, backupCalls, toolCalls atomic.Int32
	primaryRequests := make(chan nativeRoutingRequest, 2)
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := recordNativeRoutingRequest(w, r, primaryRequests, &primaryCalls, 1); !ok {
			return
		}
		item := nativeRoutingToolItem("fc_partial", "call_partial", "calculator", `{"a":90}`)
		writeSSE(w,
			`{"type":"response.created","response":{"id":"resp_partial","status":"in_progress"}}`,
			`{"type":"response.output_text.delta","delta":"preview"}`,
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_partial","call_id":"call_partial","name":"calculator","arguments":"","status":"in_progress"}}`,
			`{"type":"response.output_item.done","output_index":0,"item":`+item+`}`,
			`{"type":"response.failed","response":{"id":"resp_partial","status":"failed","error":{"message":"upstream failed after preview","type":"server_error"}}}`,
		)
	}))
	defer primaryServer.Close()
	backupRequests := make(chan nativeRoutingRequest, 1)
	backupServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := recordNativeRoutingRequest(w, r, backupRequests, &backupCalls, 0); !ok {
			return
		}
		writeNativeRoutingToolRound(w, "resp_unexpected_backup", "fc_backup", "call_backup", `{"a":1}`)
	}))
	defer backupServer.Close()
	primary := New("gpt-5", WithAPIKey("test"), WithBaseURL(primaryServer.URL), WithOpenAIOptions(option.WithMaxRetries(0)))
	backup := New("gpt-5", WithAPIKey("test"), WithBaseURL(backupServer.URL), WithOpenAIOptions(option.WithMaxRetries(0)))
	routed, err := failover.New(failover.WithCandidates(primary, backup))
	require.NoError(t, err)
	svc := inmemory.NewSessionService()
	defer svc.Close()
	r := newNativeRoutingRunner(routed, &toolCalls, svc)
	defer r.Close()
	events := runIntegration(t, r, ctx, "calculate")
	var sawError bool
	for _, evt := range events {
		if evt.Error != nil {
			sawError = true
		}
	}
	assertNativeRoutingText(t, events, "preview")
	require.True(t, sawError, "the error after a published preview must remain visible")
	require.EqualValues(t, 1, primaryCalls.Load())
	primaryBody := takeNativeRoutingRequest(t, primaryRequests)
	require.Equal(t, `true`, string(primaryBody["stream"]))
	require.Zero(t, backupCalls.Load(), "failover must not switch after a non-error preview")
	require.Zero(t, toolCalls.Load(), "an uncommitted streamed tool call must not execute")
	sess, err := svc.GetSession(ctx, session.Key{AppName: "native", UserID: "user", SessionID: "session"})
	require.NoError(t, err)
	for _, evt := range sess.Events {
		choices := evt.Choices
		if evt.Response != nil {
			choices = evt.Response.Choices
		}
		for _, choice := range choices {
			require.Empty(t, choice.Message.ToolCalls)
			require.Empty(t, choice.Delta.ToolCalls)
			require.Nil(t, choice.Message.Replay)
			require.Nil(t, choice.Delta.Replay)
		}
	}
}

func TestNativeRoutingHedgeCancelsLoserAndPersistsOnlyWinnerReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	loserRequests := make(chan nativeRoutingRequest, 8)
	winnerRequests := make(chan nativeRoutingRequest, 8)
	loserFlushed := make(chan struct{})
	loserCancelled := make(chan struct{})
	loserToolRequestRead := make(chan struct{})
	loserReopenRequestRead := make(chan struct{})
	var loserCalls, winnerCalls, toolCalls atomic.Int32
	loserServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call, ok := recordNativeRoutingRequest(w, r, loserRequests, &loserCalls, 3)
		if !ok {
			return
		}
		if call == 1 {
			loserItem := nativeRoutingToolItem("fc_loser", "call_loser", "calculator", `{"a":99}`)
			writeSSE(w,
				`{"type":"response.created","response":{"id":"resp_loser","status":"in_progress"}}`,
				`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_loser","call_id":"call_loser","name":"calculator","arguments":"","status":"in_progress"}}`,
				`{"type":"response.output_item.done","output_index":0,"item":`+loserItem+`}`,
			)
			close(loserFlushed)
			<-r.Context().Done()
			close(loserCancelled)
			return
		}
		if call == 2 {
			close(loserToolRequestRead)
		}
		if call == 3 {
			close(loserReopenRequestRead)
		}
		writeNativeRoutingAnswer(w, fmt.Sprintf("resp_loser_answer_%d", call), "5")
	}))
	defer loserServer.Close()
	winnerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call, ok := recordNativeRoutingRequest(w, r, winnerRequests, &winnerCalls, 3)
		if !ok {
			return
		}
		if call == 1 {
			select {
			case <-loserFlushed:
			case <-r.Context().Done():
				return
			}
			writeNativeRoutingToolRound(w, "resp_winner_tool", "fc_winner", "call_winner", `{"a":4}`)
			return
		}
		if call == 2 {
			select {
			case <-loserToolRequestRead:
			case <-r.Context().Done():
				return
			}
		}
		if call == 3 {
			select {
			case <-loserReopenRequestRead:
			case <-r.Context().Done():
				return
			}
		}
		writeNativeRoutingAnswer(w, fmt.Sprintf("resp_winner_answer_%d", call), "5")
	}))
	defer winnerServer.Close()

	loser := New("gpt-5", WithAPIKey("test"), WithBaseURL(loserServer.URL), WithOpenAIOptions(option.WithMaxRetries(0)))
	winner := New("gpt-5", WithAPIKey("test"), WithBaseURL(winnerServer.URL), WithOpenAIOptions(option.WithMaxRetries(0)))
	routed, err := hedge.New(hedge.WithCandidates(loser, winner), hedge.WithDelays(0))
	require.NoError(t, err)
	require.Equal(t, responseProvider, routed.Info().ReplayProtocol)
	path := filepath.Join(t.TempDir(), "native-hedge.db")
	svc := openNativeRoutingSQLite(t, path)
	var r runner.Runner
	defer func() {
		if r != nil {
			_ = r.Close()
		}
		if svc != nil {
			_ = svc.Close()
		}
	}()
	r = newNativeRoutingRunner(routed, &toolCalls, svc)
	firstRunEvents := runIntegration(t, r, ctx, "calculate")
	for _, evt := range firstRunEvents {
		require.Nil(t, evt.Error)
	}
	assertNativeRoutingText(t, firstRunEvents, "5")
	select {
	case <-loserCancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the losing Responses request was not cancelled")
	}
	require.EqualValues(t, 1, toolCalls.Load(), "only the winning native function_call may execute")
	require.GreaterOrEqual(t, loserCalls.Load(), int32(2), "the candidate may participate in later independent model turns")
	loserInitial := takeNativeRoutingRequest(t, loserRequests)
	initialWinner := takeNativeRoutingRequest(t, winnerRequests)
	require.Equal(t, `true`, string(loserInitial["stream"]))
	require.Equal(t, `true`, string(initialWinner["stream"]))
	drainNativeRoutingRequests(t, loserRequests)
	drainNativeRoutingRequests(t, winnerRequests)

	sess, err := svc.GetSession(ctx, session.Key{AppName: "native", UserID: "user", SessionID: "session"})
	require.NoError(t, err)
	assertNativeRoutingSessionContainsOnlyCall(t, sess, "fc_winner", "call_winner")

	require.NoError(t, r.Close())
	r = nil
	require.NoError(t, svc.Close())
	svc = nil
	svc = openNativeRoutingSQLite(t, path)
	r = newNativeRoutingRunner(routed, &toolCalls, svc)
	continuedEvents := runIntegration(t, r, ctx, "continue after reopen")
	for _, evt := range continuedEvents {
		require.Nil(t, evt.Error)
	}
	assertNativeRoutingText(t, continuedEvents, "5")
	require.EqualValues(t, 1, toolCalls.Load(), "reopened native history must not execute the winner tool again")
	continued := takeNativeRoutingRequest(t, loserRequests)
	assertNativeRoutingReplayInput(t, continued["input"], "fc_winner", "call_winner", `{"a":4}`, "5")
	sess, err = svc.GetSession(ctx, session.Key{AppName: "native", UserID: "user", SessionID: "session"})
	require.NoError(t, err)
	assertNativeRoutingSessionContainsOnlyCall(t, sess, "fc_winner", "call_winner")
}
