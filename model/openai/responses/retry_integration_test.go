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
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3/option"
	api "github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type retryCapturedRequest struct {
	body       json.RawMessage
	retryCount string
}

func retryResponse(req *http.Request, status int, data, retryDelay string) *http.Response {
	header := http.Header{"Content-Type": []string{"application/json"}}
	if retryDelay != "" {
		header.Set("Retry-After-Ms", retryDelay)
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(bytes.NewBufferString(data)), Request: req}
}

func TestSDKDefaultRetryPreservesNativeHistoryAcrossSQLiteReopen(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, connectionFailure := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/connection_failure=%t", streaming, connectionFailure), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				requests := make(chan retryCapturedRequest, 5)
				var httpCalls, toolCalls, requestCallbacks atomic.Int32
				transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
					defer req.Body.Close()
					call := httpCalls.Add(1)
					if call > 5 {
						return nil, fmt.Errorf("retry fixture request budget exceeded")
					}
					body, err := io.ReadAll(req.Body)
					if err != nil {
						return nil, err
					}
					select {
					case requests <- retryCapturedRequest{body: body, retryCount: req.Header.Get("X-Stainless-Retry-Count")}:
					default:
						return nil, fmt.Errorf("retry fixture request capture is full")
					}
					if call == 2 {
						if connectionFailure {
							return nil, errors.New("fixture connection reset before response headers")
						}
						return retryResponse(req, http.StatusTooManyRequests, `{"error":{"message":"fixture rate limit","type":"rate_limit_error"}}`, "1"), nil
					}
					if call == 3 {
						return retryResponse(req, http.StatusServiceUnavailable, `{"error":{"message":"fixture unavailable","type":"server_error"}}`, "1"), nil
					}
					output := nativeToolOutput
					if call != 1 {
						output = fmt.Sprintf(`[{"type":"message","id":"msg_retry_%d","status":"completed","role":"assistant","content":[{"type":"output_text","text":"2"}]}]`, call)
					}
					response := fmt.Sprintf(`{"id":"resp_retry_%d","status":"completed","output":%s}`, call, output)
					if !streaming {
						return retryResponse(req, http.StatusOK, response, ""), nil
					}
					event, err := json.Marshal(map[string]any{"type": "response.completed", "response": json.RawMessage(response)})
					if err != nil {
						return nil, err
					}
					result := retryResponse(req, http.StatusOK, "data: "+string(event)+"\n\n", "")
					result.Header.Set("Content-Type", "text/event-stream")
					return result, nil
				})
				// Leave MaxRetries unset to exercise the SDK's production default.
				m := New("test-model", WithAPIKey("test"), WithBaseURL("http://fixture.invalid/v1"),
					WithOpenAIOptions(option.WithHTTPClient(&http.Client{Transport: transport})),
					WithRequestCallback(func(context.Context, *api.ResponseNewParams) { requestCallbacks.Add(1) }))
				a := llmagent.New("native", llmagent.WithModel(m),
					llmagent.WithGenerationConfig(model.GenerationConfig{Stream: streaming}),
					llmagent.WithTools([]tool.Tool{integrationCalculator(&toolCalls)}))
				path := filepath.Join(t.TempDir(), "retry.db")
				open := func() *sqlite.Service {
					db, err := sql.Open("sqlite3", path)
					require.NoError(t, err)
					svc, err := sqlite.NewService(db, sqlite.WithEnableAsyncPersist(false))
					require.NoError(t, err)
					t.Cleanup(func() { _ = svc.Close() })
					return svc
				}
				svc := open()
				r := runner.NewRunner("native", a, runner.WithSessionService(svc))
				t.Cleanup(func() { _ = r.Close() })
				for _, evt := range runIntegration(t, r, ctx, "add one") {
					require.Nil(t, evt.Error)
				}
				require.EqualValues(t, 4, httpCalls.Load())
				require.EqualValues(t, 2, requestCallbacks.Load(), "HTTP retries must not rerun the logical request callback")
				require.EqualValues(t, 1, toolCalls.Load())
				var captured []retryCapturedRequest
				for range 4 {
					captured = append(captured, takeRetryRequest(t, requests))
				}
				require.Equal(t, captured[1].body, captured[2].body, "the SDK must resend the complete request unchanged")
				require.Equal(t, captured[1].body, captured[3].body)
				for i, expected := range []string{"0", "0", "1", "2"} {
					require.Equal(t, expected, captured[i].retryCount)
					var body struct {
						Stream bool            `json:"stream"`
						Input  json.RawMessage `json:"input"`
					}
					require.NoError(t, json.Unmarshal(captured[i].body, &body))
					require.Equal(t, streaming, body.Stream)
					if i > 0 {
						assertToolReplayInput(t, body.Input)
					}
				}
				require.NoError(t, r.Close())
				require.NoError(t, svc.Close())
				svc = open()
				sess, err := svc.GetSession(ctx, session.Key{AppName: "native", UserID: "user", SessionID: "session"})
				require.NoError(t, err)
				var nativeCalls int
				for _, evt := range sess.Events {
					if evt.Response == nil {
						continue
					}
					for _, choice := range evt.Response.Choices {
						msg := choice.Message
						if len(msg.ToolCalls) == 0 {
							continue
						}
						nativeCalls++
						raw, err := msg.ReplayDataFor(m.Info().ReplayProtocol, m.Info().Name)
						require.NoError(t, err)
						require.JSONEq(t, nativeToolOutput, string(raw))
					}
				}
				require.Equal(t, 1, nativeCalls)
				r = runner.NewRunner("native", a, runner.WithSessionService(svc))
				t.Cleanup(func() { _ = r.Close() })
				for _, evt := range runIntegration(t, r, ctx, "continue") {
					require.Nil(t, evt.Error)
				}
				require.EqualValues(t, 5, httpCalls.Load())
				require.EqualValues(t, 3, requestCallbacks.Load())
				require.EqualValues(t, 1, toolCalls.Load(), "completed tools must not run again after reopening SQLite")
				last := takeRetryRequest(t, requests)
				var body map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(last.body, &body))
				assertToolReplayInput(t, body["input"])
				require.Equal(t, "0", last.retryCount)
			})
		}
	}
}

func takeRetryRequest(t *testing.T, requests <-chan retryCapturedRequest) retryCapturedRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(2 * time.Second):
		t.Fatal("retry request was not captured")
		return retryCapturedRequest{}
	}
}

type retryClosedBody struct {
	io.ReadCloser
	closed chan struct{}
	once   sync.Once
}

func (b *retryClosedBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { close(b.closed) })
	return err
}

func TestSDKDefaultRetryCancellationInterruptsBackoff(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", streaming), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			bodyClosed := make(chan struct{})
			var httpCalls atomic.Int32
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				defer req.Body.Close()
				if httpCalls.Add(1) > 1 {
					return nil, fmt.Errorf("request retried after cancellation")
				}
				response := retryResponse(req, http.StatusTooManyRequests, `{"error":{"message":"wait before retrying"}}`, "10000")
				response.Body = &retryClosedBody{ReadCloser: response.Body, closed: bodyClosed}
				return response, nil
			})
			m := New("test-model", WithAPIKey("test"), WithBaseURL("http://fixture.invalid/v1"),
				WithOpenAIOptions(option.WithHTTPClient(&http.Client{Transport: transport})))
			ch, err := m.GenerateContent(ctx, &model.Request{
				Messages: []model.Message{model.NewUserMessage("start")}, GenerationConfig: model.GenerationConfig{Stream: streaming},
			})
			require.NoError(t, err)
			select {
			case <-bodyClosed:
			case <-time.After(2 * time.Second):
				t.Fatal("SDK did not close the failed response before retry backoff")
			}
			cancel()
			select {
			case response, ok := <-ch:
				require.False(t, ok, "cancellation during retry backoff must close the channel without a response: %#v", response)
			case <-time.After(2 * time.Second):
				t.Fatal("cancellation did not interrupt the retry backoff")
			}
			require.EqualValues(t, 1, httpCalls.Load())
		})
	}
}

func TestSDKDefaultRetryExhaustionIsOneTerminalError(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", streaming), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var httpCalls, requestCallbacks atomic.Int32
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				defer req.Body.Close()
				if httpCalls.Add(1) > 3 {
					return nil, fmt.Errorf("SDK exceeded its default retry budget")
				}
				return retryResponse(req, http.StatusTooManyRequests, `{"error":{"message":"fixture rate limit","type":"rate_limit_error"}}`, "1"), nil
			})
			m := New("test-model", WithAPIKey("test"), WithBaseURL("http://fixture.invalid/v1"),
				WithOpenAIOptions(option.WithHTTPClient(&http.Client{Transport: transport})),
				WithRequestCallback(func(context.Context, *api.ResponseNewParams) { requestCallbacks.Add(1) }))
			ch, err := m.GenerateContent(ctx, &model.Request{
				Messages: []model.Message{model.NewUserMessage("start")}, GenerationConfig: model.GenerationConfig{Stream: streaming},
			})
			require.NoError(t, err)
			responses := collect(t, ch)
			require.Len(t, responses, 1)
			requireTerminalError(t, responses)
			for _, choice := range responses[0].Choices {
				require.Empty(t, choice.Delta.ToolCalls)
				require.Nil(t, choice.Delta.Replay)
			}
			require.EqualValues(t, 3, httpCalls.Load())
			require.EqualValues(t, 1, requestCallbacks.Load())
		})
	}
}
