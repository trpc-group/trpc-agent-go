//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package debug

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/debug/internal/schema"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockAgent is a simple mock agent for testing.
type mockAgent struct {
	name        string
	description string
}

func (m *mockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: m.description,
	}
}

func (m *mockAgent) Tools() []tool.Tool { return nil }

func (m *mockAgent) SubAgents() []agent.Agent { return nil }

func (m *mockAgent) FindSubAgent(name string) agent.Agent { return nil }

func (m *mockAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	// Return a simple event channel for testing.
	events := make(chan *event.Event, 1)
	go func() {
		defer close(events)
		events <- &event.Event{
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "test response",
					},
				}},
			},
		}
	}()
	return events, nil
}

type fakeRunner struct {
	events []*event.Event
	err    error
}

type ctxCapturingRunner struct {
	ctx context.Context
}

func (f *fakeRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	ch := make(chan *event.Event, len(f.events))
	go func() {
		for _, evt := range f.events {
			ch <- evt
		}
		close(ch)
	}()
	return ch, nil
}

func (f *fakeRunner) Close() error { return nil }

func (f *ctxCapturingRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	runOpts ...agent.RunOption,
) (<-chan *event.Event, error) {
	f.ctx = ctx
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (f *ctxCapturingRunner) Close() error {
	return nil
}

type flushRecorder struct {
	*httptest.ResponseRecorder
}

func (f *flushRecorder) Flush() {}

type noFlusherRecorder struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newNoFlusherRecorder() *noFlusherRecorder {
	return &noFlusherRecorder{header: make(http.Header)}
}

func (r *noFlusherRecorder) Header() http.Header {
	return r.header
}

func (r *noFlusherRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(b)
}

func (r *noFlusherRecorder) WriteHeader(status int) {
	r.status = status
}

func (r *noFlusherRecorder) StatusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

func (r *noFlusherRecorder) BodyString() string {
	return r.body.String()
}

func TestNew(t *testing.T) {
	agents := map[string]agent.Agent{
		"test-agent": &mockAgent{name: "test-agent", description: "test description"},
	}

	server := New(agents)
	assert.NotNilf(t, server, "New() returned nil")

	assert.Equal(t, 1, len(server.agents), "expected 1 agent, got %d", len(server.agents))

	assert.NotNilf(t, server.agents["test-agent"], "agent not found in server")
}

func TestNew_WithOptions(t *testing.T) {
	agents := map[string]agent.Agent{
		"test-agent": &mockAgent{name: "test-agent", description: "test description"},
	}

	// Test with custom session service.
	customSessionSvc := &mockSessionService{}
	server := New(agents, WithSessionService(customSessionSvc))

	assert.Equal(t, customSessionSvc, server.sessionSvc, "custom session service not set")
}

func TestServer_Handler(t *testing.T) {
	agents := map[string]agent.Agent{
		"test-agent": &mockAgent{name: "test-agent", description: "test description"},
	}

	server := New(agents)
	handler := server.Handler()

	assert.NotNilf(t, handler, "Handler() returned nil")
}

func TestServer_handleListApps(t *testing.T) {
	agents := map[string]agent.Agent{
		"agent1": &mockAgent{name: "agent1", description: "first agent"},
		"agent2": &mockAgent{name: "agent2", description: "second agent"},
	}

	server := New(agents)
	req := httptest.NewRequest(http.MethodGet, "/list-apps", nil)
	w := httptest.NewRecorder()

	server.handleListApps(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "expected status 200, got %d", w.Code)

	var apps []string
	if err := json.Unmarshal(w.Body.Bytes(), &apps); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	assert.Equal(t, 2, len(apps), "expected 2 apps, got %d", len(apps))

	// Check that both agent names are present.
	found := make(map[string]bool)
	for _, app := range apps {
		found[app] = true
	}

	assert.True(t, found["agent1"] && found["agent2"], "expected agent names not found in response")
}

func TestServer_handleCreateSession(t *testing.T) {
	agents := map[string]agent.Agent{
		"test-agent": &mockAgent{name: "test-agent", description: "test description"},
	}

	server := New(agents)
	req := httptest.NewRequest(http.MethodPost, "/apps/test-agent/users/test-user/sessions", nil)
	w := httptest.NewRecorder()

	// Set up the route variables that gorilla/mux would normally set.
	req = mux.SetURLVars(req, map[string]string{
		"appName": "test-agent",
		"userId":  "test-user",
	})

	server.handleCreateSession(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "expected status 200, got %d", w.Code)

	var session schema.ADKSession
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	assert.Equal(t, "test-agent", session.AppName, "expected appName 'test-agent', got '%s'", session.AppName)

	assert.Equal(t, "test-user", session.UserID, "expected userId 'test-user', got '%s'", session.UserID)

	assert.NotEmpty(t, session.ID, "expected non-empty session ID")
}

func TestServer_handleRun(t *testing.T) {
	// Create a real LLM agent for this test.
	modelInstance := openai.New("test-model")
	llmAgent := llmagent.New(
		"test-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("test agent"),
	)

	agents := map[string]agent.Agent{
		"test-agent": llmAgent,
	}

	server := New(agents)

	// Create a test request.
	requestBody := schema.AgentRunRequest{
		AppName:   "test-agent",
		UserID:    "test-user",
		SessionID: "test-session",
		NewMessage: schema.Content{
			Role: "user",
			Parts: []schema.Part{
				{Text: "Hello, world!"},
			},
		},
		Streaming: false,
	}

	bodyBytes, _ := json.Marshal(requestBody)
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleRun(w, req)

	// The request should fail because the model is not properly configured,
	// but we can verify the request was processed.
	if w.Code == http.StatusOK {
		// If it succeeded, verify the response structure.
		var response map[string]any
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response), "failed to unmarshal response: %v")
	} else {
		// Expected to fail due to model configuration, but should not be 500.
		assert.Equal(t, http.StatusInternalServerError, w.Code, "expected status 500, got %d", w.Code)
	}
}

func TestConvertContentToMessage(t *testing.T) {
	content := schema.Content{
		Role: "user",
		Parts: []schema.Part{
			{Text: "Hello, world!"},
		},
	}

	message := convertContentToMessage(content)

	assert.Equal(t, model.RoleUser, message.Role, "expected role 'user', got '%s'", message.Role)

	assert.Equal(t, "Hello, world!", message.Content, "expected content 'Hello, world!', got '%s'", message.Content)
}

func TestConvertContentToMessage_Func(t *testing.T) {
	content := schema.Content{
		Role: "assistant",
		Parts: []schema.Part{
			{
				FunctionCall: &schema.FunctionCall{
					Name: "test_function",
					Args: map[string]any{
						"param1": "value1",
						"param2": 42,
					},
				},
			},
		},
	}

	message := convertContentToMessage(content)

	assert.Equal(t, model.RoleAssistant, message.Role, "expected role 'assistant', got '%s'", message.Role)

	assert.Equal(t, 1, len(message.ToolCalls), "expected 1 tool call, got %d", len(message.ToolCalls))

	toolCall := message.ToolCalls[0]
	assert.Equal(t, "function", toolCall.Type, "expected type 'function', got '%s'", toolCall.Type)

	assert.Equal(t, "test_function", toolCall.Function.Name, "expected function name 'test_function', got '%s'", toolCall.Function.Name)
}

func TestConvertSessionToADKFormat(t *testing.T) {
	now := time.Now()
	sess := &session.Session{
		ID:        "test-session-id",
		AppName:   "test-app",
		UserID:    "test-user",
		CreatedAt: now,
		UpdatedAt: now,
		State:     map[string][]byte{"key1": []byte("value1")},
	}

	adkSession := convertSessionToADKFormat(sess)

	assert.Equal(t, "test-session-id", adkSession.ID, "expected ID 'test-session-id', got '%s'", adkSession.ID)

	assert.Equal(t, "test-app", adkSession.AppName, "expected AppName 'test-app', got '%s'", adkSession.AppName)

	assert.Equal(t, "test-user", adkSession.UserID, "expected UserID 'test-user', got '%s'", adkSession.UserID)

	assert.NotZero(t, adkSession.CreateTime, "expected non-zero CreateTime")

	assert.NotZero(t, adkSession.LastUpdateTime, "expected non-zero LastUpdateTime")

	assert.Equal(t, 1, len(adkSession.State), "expected 1 state entry, got %d", len(adkSession.State))
}

func TestServer_convertSessionToEvalInvocations(t *testing.T) {
	sess := &session.Session{}
	sess.Events = append(sess.Events,
		*newUserMessageEvent("invocation-1", "calc add 1 2"),
		*newToolCallEvent("invocation-1"),
		*newAssistantFinalEvent("invocation-1", "calc result: 3"),
	)

	srv := &Server{}
	invocations := srv.convertSessionToEvalInvocations(sess)

	require.Len(t, invocations, 1)
	inv := invocations[0]
	require.NotNil(t, inv.UserContent)
	require.NotNil(t, inv.FinalResponse)
	assert.Equal(t, "invocation-1", inv.InvocationID)
	assert.Equal(t, "calc add 1 2", inv.UserContent.Parts[0].Text)
	assert.Equal(t, "calc result: 3", inv.FinalResponse.Parts[0].Text)
	require.NotNil(t, inv.IntermediateData)
	require.Len(t, inv.IntermediateData.ToolUses, 1)
	assert.Equal(t, "calculator", inv.IntermediateData.ToolUses[0].Name)
	assert.Equal(t, "add", inv.IntermediateData.ToolUses[0].Args["operation"])
}

func TestServer_handleAddSessionToEvalSet(t *testing.T) {
	appName := "assistant"
	srv := New(map[string]agent.Agent{
		appName: &mockAgent{name: appName},
	})

	ctx := context.Background()
	_, err := srv.evalSetManager.Create(ctx, appName, "eval-1")
	require.NoError(t, err)

	sess := recordEvalSession(t, srv, appName, "user-1", "session-1")

	body := schema.AddSessionToEvalSetRequest{
		EvalId:    "case-1",
		SessionId: sess.ID,
		UserId:    sess.UserID,
	}
	payload, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/apps/assistant/eval-sets/eval-1/add-session", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req = mux.SetURLVars(req, map[string]string{
		"appName":   appName,
		"evalSetId": "eval-1",
	})
	w := httptest.NewRecorder()

	srv.handleAddSessionToEvalSet(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	evalCase, err := srv.evalSetManager.GetCase(ctx, appName, "eval-1", "case-1")
	require.NoError(t, err)
	require.NotNil(t, evalCase)
	require.Len(t, evalCase.Conversation, 1)
	assert.Equal(t, "calc add 1 2", evalCase.Conversation[0].UserContent.Parts[0].Text)
	assert.Equal(t, "calc result: 3", evalCase.Conversation[0].FinalResponse.Parts[0].Text)
	require.NotNil(t, evalCase.Conversation[0].IntermediateData)
	require.Len(t, evalCase.Conversation[0].IntermediateData.ToolUses, 1)
	assert.Equal(t, "tool-call-1", evalCase.Conversation[0].IntermediateData.ToolUses[0].ID)
	assert.Equal(t, "user-1", evalCase.SessionInput.UserID)
}

func TestServer_EvaluationEndpoints(t *testing.T) {
	appName := "assistant"
	srv := New(map[string]agent.Agent{
		appName: &mockAgent{name: appName},
	})
	require.NoError(t, srv.metricRegistry.Register("fake_metric", &fakeEvaluatorImpl{name: "fake_metric"}))
	srv.runners[appName] = &fakeEvalRunner{events: []*event.Event{
		newRunnerToolCallEvent("eval-run-1"),
		newRunnerFinalEvent("eval-run-1", "calc result: 3"),
	}}

	sessionObj := recordEvalSession(t, srv, appName, "user-http", "session-http")

	newSetBody := map[string]any{
		"evalSet": map[string]any{
			"evalSetId": "eval-http",
		},
	}
	resp := performJSONRequest(t, srv.Handler(), http.MethodPost, "/apps/assistant/eval-sets", newSetBody)
	assert.Equal(t, http.StatusOK, resp.Code)

	resp = performJSONRequest(t, srv.Handler(), http.MethodPost, "/apps/assistant/eval_sets/eval-legacy", nil)
	assert.Equal(t, http.StatusOK, resp.Code)

	resp = performJSONRequest(t, srv.Handler(), http.MethodGet, "/apps/assistant/eval-sets", nil)
	var listResp schema.ListEvalSetsResponse
	decodeBody(t, resp.Body, &listResp)
	assert.ElementsMatch(t, []string{"eval-http", "eval-legacy"}, listResp.EvalSetIds)

	resp = performJSONRequest(t, srv.Handler(), http.MethodGet, "/apps/assistant/eval_sets", nil)
	var legacyList []string
	decodeBody(t, resp.Body, &legacyList)
	assert.ElementsMatch(t, []string{"eval-http", "eval-legacy"}, legacyList)

	addBody := schema.AddSessionToEvalSetRequest{
		EvalId:    "case-http",
		SessionId: sessionObj.ID,
		UserId:    sessionObj.UserID,
	}
	resp = performJSONRequest(t, srv.Handler(), http.MethodPost, "/apps/assistant/eval-sets/eval-http/add-session", addBody)
	assert.Equal(t, http.StatusOK, resp.Code)

	resp = performJSONRequest(t, srv.Handler(), http.MethodGet, "/apps/assistant/eval_sets/eval-http/evals", nil)
	var caseIDs []string
	decodeBody(t, resp.Body, &caseIDs)
	assert.Equal(t, []string{"case-http"}, caseIDs)

	resp = performJSONRequest(t, srv.Handler(), http.MethodGet, "/apps/assistant/eval-sets/eval-http/eval-cases/case-http", nil)
	var fetchedCase evalset.EvalCase
	decodeBody(t, resp.Body, &fetchedCase)
	assert.Equal(t, "case-http", fetchedCase.EvalID)

	resp = performJSONRequest(t, srv.Handler(), http.MethodGet, "/apps/assistant/eval_sets/eval-http/evals/case-http", nil)
	var legacyFetched evalset.EvalCase
	decodeBody(t, resp.Body, &legacyFetched)
	assert.Equal(t, "case-http", legacyFetched.EvalID)

	fetchedCase.SessionInput.State = map[string]any{"channel": "test"}
	updateBody, err := json.Marshal(fetchedCase)
	require.NoError(t, err)
	resp = performRequest(t, srv.Handler(), http.MethodPut, "/apps/assistant/eval-sets/eval-http/eval-cases/case-http", bytes.NewReader(updateBody))
	assert.Equal(t, http.StatusOK, resp.Code)

	resp = performJSONRequest(t, srv.Handler(), http.MethodGet, "/apps/assistant/metrics-info", nil)
	var metricsInfo schema.ListMetricsInfoResponse
	decodeBody(t, resp.Body, &metricsInfo)
	assert.NotEmpty(t, metricsInfo.MetricsInfo)

	runReq := schema.RunEvalRequest{
		EvalCaseIds: []string{"case-http"},
		EvalMetrics: []metric.EvalMetric{{MetricName: "fake_metric", Threshold: 0.5}},
	}
	resp = performJSONRequest(t, srv.Handler(), http.MethodPost, "/apps/assistant/eval-sets/eval-http/run", runReq)
	assert.Equal(t, http.StatusOK, resp.Code)
	var runResp schema.RunEvalResponse
	decodeBody(t, resp.Body, &runResp)
	require.Len(t, runResp.RunEvalResults, 1)
	assert.Equal(t, "case-http", runResp.RunEvalResults[0].EvalId)

	resp = performJSONRequest(t, srv.Handler(), http.MethodPost, "/apps/assistant/eval_sets/eval-http/run_eval", runReq)
	assert.Equal(t, http.StatusOK, resp.Code)
	var legacyRun []*schema.RunEvalResult
	decodeBody(t, resp.Body, &legacyRun)
	require.Len(t, legacyRun, 1)

	resp = performJSONRequest(t, srv.Handler(), http.MethodGet, "/apps/assistant/eval-results", nil)
	var evalResultIDs struct {
		EvalResultIds []string `json:"evalResultIds"`
	}
	decodeBody(t, resp.Body, &evalResultIDs)
	require.NotEmpty(t, evalResultIDs.EvalResultIds)

	resp = performJSONRequest(t, srv.Handler(), http.MethodGet, "/apps/assistant/eval_results", nil)
	var legacyResultIDs []string
	decodeBody(t, resp.Body, &legacyResultIDs)
	require.NotEmpty(t, legacyResultIDs)

	targetID := evalResultIDs.EvalResultIds[0]
	path := "/apps/assistant/eval-results/" + targetID
	resp = performJSONRequest(t, srv.Handler(), http.MethodGet, path, nil)
	var evalResult evalresult.EvalSetResult
	decodeBody(t, resp.Body, &evalResult)
	assert.Equal(t, targetID, evalResult.EvalSetResultID)

	resp = performJSONRequest(t, srv.Handler(), http.MethodGet, "/apps/assistant/eval_results/"+targetID, nil)
	var legacyEvalResult evalresult.EvalSetResult
	decodeBody(t, resp.Body, &legacyEvalResult)
	assert.Equal(t, targetID, legacyEvalResult.EvalSetResultID)

	resp = performRequest(t, srv.Handler(), http.MethodDelete, "/apps/assistant/eval-sets/eval-http/eval-cases/case-http", nil)
	assert.Equal(t, http.StatusOK, resp.Code)

	resp = performJSONRequest(t, srv.Handler(), http.MethodGet, "/apps/assistant/eval_sets/eval-http/evals", nil)
	decodeBody(t, resp.Body, &caseIDs)
	assert.Empty(t, caseIDs)
}

func recordEvalSession(t *testing.T, srv *Server, appName, userID, sessionID string) *session.Session {
	ctx := context.Background()
	sess, err := srv.sessionSvc.CreateSession(ctx, session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	}, session.StateMap{})
	require.NoError(t, err)

	require.NoError(t, srv.sessionSvc.AppendEvent(ctx, sess, newUserMessageEvent("invocation-1", "calc add 1 2")))
	require.NoError(t, srv.sessionSvc.AppendEvent(ctx, sess, newToolCallEvent("invocation-1")))
	require.NoError(t, srv.sessionSvc.AppendEvent(ctx, sess, newAssistantFinalEvent("invocation-1", "calc result: 3")))
	return sess
}

func newUserMessageEvent(invocationID, content string) *event.Event {
	rsp := &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: content,
			},
		}},
		Done: true,
	}
	return event.NewResponseEvent(invocationID, string(model.RoleUser), rsp)
}

func newToolCallEvent(invocationID string) *event.Event {
	args := json.RawMessage(`{"operation":"add","a":1,"b":2}`)
	rsp := &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{
						ID: "tool-call-1",
						Function: model.FunctionDefinitionParam{
							Name:      "calculator",
							Arguments: args,
						},
					},
				},
			},
		}},
	}
	return event.NewResponseEvent(invocationID, string(model.RoleAssistant), rsp)
}

func newAssistantFinalEvent(invocationID, content string) *event.Event {
	rsp := &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: content,
			},
		}},
		Done: true,
	}
	return event.NewResponseEvent(invocationID, string(model.RoleAssistant), rsp)
}

type fakeEvalRunner struct {
	events []*event.Event
}

func (f *fakeEvalRunner) Run(ctx context.Context, userID, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, len(f.events))
	for _, evt := range f.events {
		e := *evt
		ch <- &e
	}
	close(ch)
	return ch, nil
}

func (f *fakeEvalRunner) Close() error {
	return nil
}

type fakeEvaluatorImpl struct {
	name string
}

func (f *fakeEvaluatorImpl) Name() string        { return f.name }
func (f *fakeEvaluatorImpl) Description() string { return "fake evaluator" }
func (f *fakeEvaluatorImpl) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation, evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	result := &evaluator.EvaluateResult{
		OverallScore:         1,
		OverallStatus:        status.EvalStatusPassed,
		PerInvocationResults: make([]evaluator.PerInvocationResult, len(actuals)),
	}
	for i := range actuals {
		result.PerInvocationResults[i] = evaluator.PerInvocationResult{
			ActualInvocation:   actuals[i],
			ExpectedInvocation: expecteds[i],
			Score:              1,
			Status:             status.EvalStatusPassed,
		}
	}
	return result, nil
}

func performJSONRequest(t *testing.T, handler http.Handler, method, path string, payload any) *httptest.ResponseRecorder {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		require.NoError(t, err)
		body = bytes.NewReader(data)
	}
	return performRequest(t, handler, method, path, body)
}

func performRequest(t *testing.T, handler http.Handler, method, path string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}

func decodeBody(t *testing.T, r io.Reader, v any) {
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, v))
}

func newRunnerToolCallEvent(invocationID string) *event.Event {
	args := json.RawMessage(`{"operation":"add","a":1,"b":2}`)
	resp := &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:       "runner-tool",
					Function: model.FunctionDefinitionParam{Name: "calculator", Arguments: args},
				}},
			},
		}},
	}
	return &event.Event{Response: resp, InvocationID: invocationID}
}

func newRunnerFinalEvent(invocationID, content string) *event.Event {
	resp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: content,
			},
		}},
	}
	return &event.Event{Response: resp, InvocationID: invocationID}
}

// mockSessionService is a simple mock session service for testing.
type mockSessionService struct {
	sessions           map[string]*session.Session
	listSessionsResult []*session.Session
	listSessionsErr    error
	getSessionResult   *session.Session
	getSessionErr      error
}

func (m *mockSessionService) CreateSession(ctx context.Context, key session.Key, state session.StateMap, options ...session.Option) (*session.Session, error) {
	now := time.Now()
	sess := &session.Session{
		ID:        "mock-session-id",
		AppName:   key.AppName,
		UserID:    key.UserID,
		CreatedAt: now,
		UpdatedAt: now,
		State:     state,
	}
	return sess, nil
}

func (m *mockSessionService) GetSession(ctx context.Context, key session.Key, options ...session.Option) (*session.Session, error) {
	return m.getSessionResult, m.getSessionErr
}

func (m *mockSessionService) ListSessions(ctx context.Context, userKey session.UserKey, options ...session.Option) ([]*session.Session, error) {
	if m.listSessionsResult != nil {
		return m.listSessionsResult, m.listSessionsErr
	}
	return []*session.Session{}, m.listSessionsErr
}

func (m *mockSessionService) DeleteSession(ctx context.Context, key session.Key, options ...session.Option) error {
	return nil
}

func (m *mockSessionService) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	return nil
}

func (m *mockSessionService) DeleteAppState(ctx context.Context, appName string, key string) error {
	return nil
}

func (m *mockSessionService) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	return session.StateMap{}, nil
}

func (m *mockSessionService) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	return nil
}

func (m *mockSessionService) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	return session.StateMap{}, nil
}

func (m *mockSessionService) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	return nil
}

func (m *mockSessionService) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	return nil
}

func (m *mockSessionService) AppendEvent(ctx context.Context, session *session.Session, event *event.Event, options ...session.Option) error {
	return nil
}

func (m *mockSessionService) Close() error {
	return nil
}

// Implement new session.Service summary methods.
func (m *mockSessionService) CreateSessionSummary(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	return nil
}

func (m *mockSessionService) EnqueueSummaryJob(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	return nil
}

func (m *mockSessionService) GetSessionSummaryText(ctx context.Context, sess *session.Session) (string, bool) {
	return "", false
}

func TestHandleEventTrace_LLMRequestFormatted(t *testing.T) {
	reqModel := model.Request{
		Messages: []model.Message{
			{
				Role:    model.RoleUser,
				Content: "hello world",
			},
		},
	}
	payload, err := json.Marshal(reqModel)
	assert.NoError(t, err)

	attrs := attribute.NewSet(
		attribute.String("trace_id", "trace-1"),
		attribute.String("span_id", "span-1"),
		attribute.String(keyEventID, "event-1"),
		attribute.String(keyLLMRequest, string(payload)),
	)

	server := &Server{
		agents:         map[string]agent.Agent{},
		router:         mux.NewRouter(),
		runners:        map[string]runner.Runner{},
		sessionSvc:     sessioninmemory.NewSessionService(),
		traces:         map[string]attribute.Set{"event-1": attrs},
		memoryExporter: newInMemoryExporter(),
	}

	req := httptest.NewRequest(http.MethodGet, "/debug/trace/event-1", nil)
	req = mux.SetURLVars(req, map[string]string{"event_id": "event-1"})
	w := httptest.NewRecorder()

	server.handleEventTrace(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	traceID, ok := resp["trace_id"].(string)
	assert.True(t, ok)
	assert.Equal(t, "trace-1", traceID)

	llmJSON, ok := resp[keyLLMRequest].(string)
	assert.True(t, ok)

	var traceReq schema.TraceLLMRequest
	assert.NoError(t, json.Unmarshal([]byte(llmJSON), &traceReq))
	assert.Equal(t, 1, len(traceReq.Contents))
	assert.Equal(t, "user", traceReq.Contents[0].Role)
	assert.Equal(t, "hello world", traceReq.Contents[0].Parts[0].Text)
}

func TestHandleEventTrace_NotFound(t *testing.T) {
	server := &Server{
		agents:         map[string]agent.Agent{},
		router:         mux.NewRouter(),
		runners:        map[string]runner.Runner{},
		sessionSvc:     sessioninmemory.NewSessionService(),
		traces:         map[string]attribute.Set{},
		memoryExporter: newInMemoryExporter(),
	}

	req := httptest.NewRequest(http.MethodGet, "/debug/trace/missing", nil)
	req = mux.SetURLVars(req, map[string]string{"event_id": "missing"})
	w := httptest.NewRecorder()

	server.handleEventTrace(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleSessionTrace_ReturnsSpans(t *testing.T) {
	server := &Server{
		agents:         map[string]agent.Agent{},
		router:         mux.NewRouter(),
		runners:        map[string]runner.Runner{},
		sessionSvc:     sessioninmemory.NewSessionService(),
		traces:         map[string]attribute.Set{},
		memoryExporter: newInMemoryExporter(),
	}

	sessionID := "session-1"
	traceID, err := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	assert.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("0102030405060708")
	assert.NoError(t, err)

	span := tracetest.SpanStub{
		Name: "chat.completion",
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: traceID,
			SpanID:  spanID,
		}),
		StartTime: time.Unix(100, 0),
		EndTime:   time.Unix(101, 0),
		Attributes: []attribute.KeyValue{
			attribute.String(keySessionID, sessionID),
			attribute.String(keyEventID, "event-1"),
		},
	}.Snapshot()

	server.memoryExporter.sessionTraces[sessionID] = map[string]struct{}{
		traceID.String(): {},
	}
	server.memoryExporter.spans = []sdktrace.ReadOnlySpan{span}

	req := httptest.NewRequest(http.MethodGet, "/debug/trace/session/"+sessionID, nil)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	w := httptest.NewRecorder()

	server.handleSessionTrace(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var spans []schema.Span
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &spans))
	assert.Equal(t, 1, len(spans))
	assert.Equal(t, traceID.String(), spans[0].TraceID)
	assert.Equal(t, spanID.String(), spans[0].SpanID)
	assert.Equal(t, "event-1", spans[0].Attributes[keyEventID])
	assert.Equal(t, sessionID, spans[0].Attributes[keySessionID])
}

func TestHandleListSessions_FiltersEvalSessions(t *testing.T) {
	now := time.Now()
	customSvc := &mockSessionService{
		listSessionsResult: []*session.Session{
			{ID: "sess-1", AppName: "app", UserID: "user", CreatedAt: now, UpdatedAt: now},
			{ID: "eval-123", AppName: "app", UserID: "user", CreatedAt: now, UpdatedAt: now},
			{ID: "sess-2", AppName: "app", UserID: "user", CreatedAt: now, UpdatedAt: now},
		},
	}

	server := New(map[string]agent.Agent{}, WithSessionService(customSvc))

	req := httptest.NewRequest(http.MethodGet, "/apps/app/users/user/sessions", nil)
	req = mux.SetURLVars(req, map[string]string{"appName": "app", "userId": "user"})
	w := httptest.NewRecorder()

	server.handleListSessions(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var sessionsResp []schema.ADKSession
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &sessionsResp))
	assert.Equal(t, 2, len(sessionsResp))
	ids := []string{sessionsResp[0].ID, sessionsResp[1].ID}
	assert.Contains(t, ids, "sess-1")
	assert.Contains(t, ids, "sess-2")
}

func TestHandleGetSession_NotFound(t *testing.T) {
	customSvc := &mockSessionService{}
	server := New(map[string]agent.Agent{}, WithSessionService(customSvc))

	req := httptest.NewRequest(http.MethodGet, "/apps/app/users/user/sessions/unknown", nil)
	req = mux.SetURLVars(req, map[string]string{"appName": "app", "userId": "user", "sessionId": "unknown"})
	w := httptest.NewRecorder()

	server.handleGetSession(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleGetSession_Success(t *testing.T) {
	now := time.Now()
	sessionObj := &session.Session{
		ID:        "sess-1",
		AppName:   "app",
		UserID:    "user",
		CreatedAt: now,
		UpdatedAt: now,
		State:     session.StateMap{"key": []byte("value")},
	}

	customSvc := &mockSessionService{
		getSessionResult: sessionObj,
	}
	server := New(map[string]agent.Agent{}, WithSessionService(customSvc))

	req := httptest.NewRequest(http.MethodGet, "/apps/app/users/user/sessions/sess-1", nil)
	req = mux.SetURLVars(req, map[string]string{"appName": "app", "userId": "user", "sessionId": "sess-1"})
	w := httptest.NewRecorder()

	server.handleGetSession(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp schema.ADKSession
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "sess-1", resp.ID)
	assert.Equal(t, "app", resp.AppName)
	assert.Equal(t, "user", resp.UserID)
}

func TestConvertContentToMessage_InlineDataAndFunctionResponse(t *testing.T) {
	content := schema.Content{
		Role: "assistant",
		Parts: []schema.Part{
			{
				InlineData: &schema.InlineData{
					MimeType:    "image/png",
					DisplayName: "diagram.png",
				},
			},
			{
				InlineData: &schema.InlineData{
					MimeType: "audio/mpeg",
				},
			},
			{
				FunctionResponse: &schema.FunctionResponse{
					Name:     "tool",
					Response: map[string]any{"ok": true},
				},
			},
		},
	}

	msg := convertContentToMessage(content)

	assert.Equal(t, model.RoleAssistant, msg.Role)
	expectedSnippets := []string{
		"[image: diagram.png (image/png)]",
		"[audio: attachment (audio/mpeg)]",
		`[Function tool responded: {"ok":true}]`,
	}
	for _, snippet := range expectedSnippets {
		assert.True(t, strings.Contains(msg.Content, snippet))
	}
}

func TestBuildFunctionCallPart_InvalidJSON(t *testing.T) {
	part := buildFunctionCallPart(model.ToolCall{
		ID: "tool-1",
		Function: model.FunctionDefinitionParam{
			Name:      "fn",
			Arguments: []byte("invalid"),
		},
	})

	call, ok := part[keyFunctionCall].(map[string]any)
	assert.True(t, ok)

	args, ok := call["args"].(map[string]any)
	assert.True(t, ok)

	assert.Equal(t, "invalid", args["raw"])
}

func TestConvertEventToADKFormat_StreamingSkipsDoneEvent(t *testing.T) {
	evt := &event.Event{
		InvocationID: "inv",
		Author:       "assistant",
		ID:           "event-id",
		Timestamp:    time.Unix(0, 0),
		Response: &model.Response{
			Done:      true,
			IsPartial: false,
			Choices: []model.Choice{
				{
					Message: model.Message{
						Content: "final output",
						Role:    model.RoleAssistant,
					},
				},
			},
		},
	}

	res := convertEventToADKFormat(evt, true)
	assert.Nil(t, res)
}

func TestConvertEventToADKFormat_NonStreamingKeepsToolCall(t *testing.T) {
	evt := &event.Event{
		InvocationID: "inv",
		Author:       "assistant",
		ID:           "event-id",
		Timestamp:    time.Unix(0, 0),
		Response: &model.Response{
			Done: false,
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{
								ID: "tool-1",
								Function: model.FunctionDefinitionParam{
									Name:      "fn",
									Arguments: []byte(`{"x":1}`),
								},
							},
						},
					},
				},
			},
		},
	}

	res := convertEventToADKFormat(evt, false)
	assert.NotNil(t, res)

	content, ok := res["content"].(map[string]any)
	assert.True(t, ok)
	parts, ok := content["parts"].([]map[string]any)
	assert.True(t, ok)
	assert.Len(t, parts, 1)
	call, ok := parts[0][keyFunctionCall].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "fn", call["name"])
}

func TestConvertEventToADKFormat_ToolResponseIncludesMetadata(t *testing.T) {
	evt := &event.Event{
		InvocationID: "inv",
		Author:       "assistant",
		ID:           "event-id",
		Timestamp:    time.Unix(0, 0),
		Response: &model.Response{
			Done:   true,
			Object: model.ObjectTypeToolResponse,
			Model:  "test-model",
			Usage: &model.Usage{
				PromptTokens:     10,
				CompletionTokens: 20,
				TotalTokens:      30,
			},
			Choices: []model.Choice{
				{
					Message: model.Message{
						Content:  `{"result":"ok"}`,
						ToolID:   "tool-1",
						ToolName: "tool",
					},
				},
			},
		},
	}

	res := convertEventToADKFormat(evt, false)
	assert.NotNil(t, res)
	assert.Equal(t, true, res["done"])
	assert.Equal(t, "tool.response", res["object"])
	assert.Equal(t, "test-model", res["model"])
	usage, ok := res["usageMetadata"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, 10, usage["promptTokenCount"])

	content, ok := res["content"].(map[string]any)
	assert.True(t, ok)
	parts, ok := content["parts"].([]map[string]any)
	assert.True(t, ok)
	assert.Len(t, parts, 1)
	respPart, ok := parts[0][keyFunctionResponse].(map[string]any)
	assert.True(t, ok)
	responsePayload, ok := respPart["response"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "ok", responsePayload["result"])
}

func TestBuildGraphEventParts_ToolPhases(t *testing.T) {
	metadataComplete := map[string]string{
		"toolName": "tool",
		"toolId":   "tool-1",
		"phase":    "complete",
		"output":   `{"answer":42}`,
	}
	completeBytes, err := json.Marshal(metadataComplete)
	assert.NoError(t, err)

	eComplete := &event.Event{
		Response: &model.Response{
			Object: graph.ObjectTypeGraphNodeExecution,
		},
		StateDelta: map[string][]byte{
			graph.MetadataKeyTool: completeBytes,
		},
	}

	partsComplete := buildGraphEventParts(eComplete)
	assert.Len(t, partsComplete, 1)
	compResp, ok := partsComplete[0][keyFunctionResponse].(map[string]any)
	assert.True(t, ok)
	compData, ok := compResp["response"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, float64(42), compData["answer"])

	metadataError := map[string]string{
		"toolName": "tool",
		"toolId":   "tool-1",
		"phase":    "error",
		"output":   "failed",
	}
	errorBytes, err := json.Marshal(metadataError)
	assert.NoError(t, err)

	eError := &event.Event{
		Response: &model.Response{
			Object: graph.ObjectTypeGraphNodeExecution,
		},
		StateDelta: map[string][]byte{
			graph.MetadataKeyTool: errorBytes,
		},
	}

	partsError := buildGraphEventParts(eError)
	assert.Len(t, partsError, 1)
	errResp, ok := partsError[0][keyFunctionResponse].(map[string]any)
	assert.True(t, ok)
	errData, ok := errResp["response"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "failed", errData["error"])

	eStart := &event.Event{
		Response: &model.Response{
			Object: graph.ObjectTypeGraphNodeExecution,
		},
		StateDelta: map[string][]byte{
			graph.MetadataKeyTool: []byte(`{"phase":"start"}`),
		},
	}
	partsStart := buildGraphEventParts(eStart)
	assert.Len(t, partsStart, 0)
}

func TestBuildGraphEventParts_GraphExecution(t *testing.T) {
	e := &event.Event{
		Response: &model.Response{
			Object: graph.ObjectTypeGraphExecution,
			Choices: []model.Choice{
				{Message: model.Message{Content: "graph final"}},
			},
		},
	}

	parts := buildGraphEventParts(e)
	assert.Len(t, parts, 1)
	assert.Equal(t, "graph final", parts[0][keyText])
}

func TestBuildGraphEventParts_InvalidMetadata(t *testing.T) {
	e := &event.Event{
		Response: &model.Response{
			Object: graph.ObjectTypeGraphNodeExecution,
		},
		StateDelta: map[string][]byte{
			graph.MetadataKeyTool: []byte(`not-json`),
		},
	}

	parts := buildGraphEventParts(e)
	assert.Len(t, parts, 0)
}

func TestHandleRunSSE_StreamingWritesEvents(t *testing.T) {
	e := &event.Event{
		InvocationID: "inv",
		Author:       "assistant",
		ID:           "event-id",
		Timestamp:    time.Unix(0, 0),
		Response: &model.Response{
			IsPartial: true,
			Done:      false,
			Choices: []model.Choice{
				{
					Delta: model.Message{
						Content: "partial",
						Role:    model.RoleAssistant,
					},
				},
			},
		},
	}

	server := &Server{
		agents:         map[string]agent.Agent{},
		router:         mux.NewRouter(),
		runners:        map[string]runner.Runner{"app": &fakeRunner{events: []*event.Event{e}}},
		sessionSvc:     sessioninmemory.NewSessionService(),
		traces:         map[string]attribute.Set{},
		memoryExporter: newInMemoryExporter(),
	}

	reqBody := schema.AgentRunRequest{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
		NewMessage: schema.Content{
			Role: "user",
			Parts: []schema.Part{
				{Text: "hi"},
			},
		},
		Streaming: true,
	}
	bodyBytes, err := json.Marshal(reqBody)
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/run_sse", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}

	server.handleRunSSE(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "data: ")
}

func TestHandleRunSSE_NoFlusher(t *testing.T) {
	server := &Server{
		agents:         map[string]agent.Agent{},
		router:         mux.NewRouter(),
		runners:        map[string]runner.Runner{"app": &fakeRunner{}},
		sessionSvc:     sessioninmemory.NewSessionService(),
		traces:         map[string]attribute.Set{},
		memoryExporter: newInMemoryExporter(),
	}

	reqBody := schema.AgentRunRequest{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
		NewMessage: schema.Content{
			Role:  "user",
			Parts: []schema.Part{{Text: "hi"}},
		},
		Streaming: true,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/run_sse", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := newNoFlusherRecorder()

	server.handleRunSSE(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.StatusCode())
	assert.Contains(t, w.BodyString(), "Streaming unsupported")
}

func TestHandleRunSSE_GetRunnerError(t *testing.T) {
	server := &Server{
		agents:         map[string]agent.Agent{},
		router:         mux.NewRouter(),
		runners:        map[string]runner.Runner{},
		sessionSvc:     sessioninmemory.NewSessionService(),
		traces:         map[string]attribute.Set{},
		memoryExporter: newInMemoryExporter(),
	}

	reqBody := schema.AgentRunRequest{
		AppName:   "missing",
		UserID:    "user",
		SessionID: "sess",
		NewMessage: schema.Content{
			Role:  "user",
			Parts: []schema.Part{{Text: "hi"}},
		},
		Streaming: true,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/run_sse", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}

	server.handleRunSSE(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleRunSSE_RunError(t *testing.T) {
	server := &Server{
		agents:         map[string]agent.Agent{},
		router:         mux.NewRouter(),
		runners:        map[string]runner.Runner{"app": &fakeRunner{err: errors.New("run failed")}},
		sessionSvc:     sessioninmemory.NewSessionService(),
		traces:         map[string]attribute.Set{},
		memoryExporter: newInMemoryExporter(),
	}

	reqBody := schema.AgentRunRequest{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
		NewMessage: schema.Content{
			Role:  "user",
			Parts: []schema.Part{{Text: "hi"}},
		},
		Streaming: true,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/run_sse", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}

	server.handleRunSSE(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleRunSSE_NonStreaming(t *testing.T) {
	e := &event.Event{
		InvocationID: "inv",
		Author:       "assistant",
		ID:           "event-id",
		Timestamp:    time.Unix(0, 0),
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{
				{Message: model.Message{Content: "final", Role: model.RoleAssistant}},
			},
		},
	}

	server := &Server{
		agents:         map[string]agent.Agent{},
		router:         mux.NewRouter(),
		runners:        map[string]runner.Runner{"app": &fakeRunner{events: []*event.Event{e}}},
		sessionSvc:     sessioninmemory.NewSessionService(),
		traces:         map[string]attribute.Set{},
		memoryExporter: newInMemoryExporter(),
	}

	reqBody := schema.AgentRunRequest{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
		NewMessage: schema.Content{
			Role:  "user",
			Parts: []schema.Part{{Text: "hi"}},
		},
		Streaming: false,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/run_sse", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}

	server.handleRunSSE(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "data: ")
}

func TestNewDetachedContextPreservesValues(t *testing.T) {
	type ctxKey struct{}
	key := ctxKey{}
	parent, cancel := context.WithCancel(
		context.WithValue(context.Background(), key, "trace-id"),
	)
	cancel()

	ctx := newDetachedContext(parent)

	assert.Equal(t, "trace-id", ctx.Value(key))
	_, ok := ctx.Deadline()
	assert.False(t, ok)
	assert.Nil(t, ctx.Done())
	assert.Nil(t, ctx.Err())

	err := agent.CheckContextCancelled(ctx)
	assert.NoError(t, err)
}

func TestHandleRunSSE_UsesDetachedContext(t *testing.T) {
	type ctxKey struct{}
	key := ctxKey{}

	ctxRunner := &ctxCapturingRunner{}
	server := &Server{
		agents:     map[string]agent.Agent{},
		router:     mux.NewRouter(),
		runners:    map[string]runner.Runner{"app": ctxRunner},
		sessionSvc: sessioninmemory.NewSessionService(),
		traces:     map[string]attribute.Set{},

		memoryExporter: newInMemoryExporter(),
	}

	reqBody := schema.AgentRunRequest{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
		NewMessage: schema.Content{
			Role:  "user",
			Parts: []schema.Part{{Text: "hi"}},
		},
		Streaming: true,
	}
	body, err := json.Marshal(reqBody)
	require.NoError(t, err)

	baseCtx, cancel := context.WithCancel(
		context.WithValue(context.Background(), key, "trace-id"),
	)
	cancel()

	req := httptest.NewRequest(
		http.MethodPost,
		"/run_sse",
		bytes.NewReader(body),
	).WithContext(baseCtx)
	req.Header.Set("Content-Type", "application/json")
	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}

	server.handleRunSSE(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	if assert.NotNil(t, ctxRunner.ctx) {
		assert.Equal(t, "trace-id", ctxRunner.ctx.Value(key))
		_, ok := ctxRunner.ctx.Deadline()
		assert.False(t, ok)
		assert.Nil(t, ctxRunner.ctx.Done())
		assert.Nil(t, ctxRunner.ctx.Err())

		err = agent.CheckContextCancelled(ctxRunner.ctx)
		assert.NoError(t, err)
	}
}

func TestServerGetRunnerCache(t *testing.T) {
	server := New(map[string]agent.Agent{
		"app": &mockAgent{name: "app"},
	})

	first, err := server.getRunner("app")
	assert.NoError(t, err)

	second, err := server.getRunner("app")
	assert.NoError(t, err)
	assert.Equal(t, first, second)
}

func TestServerGetRunnerMissingAgent(t *testing.T) {
	server := New(map[string]agent.Agent{})

	r, err := server.getRunner("missing")
	assert.Nil(t, r)
	assert.EqualError(t, err, "agent not found")
}

func TestWithRunnerOptionsAppends(t *testing.T) {
	flag := false
	opt := WithRunnerOptions(func(o *runner.Options) {
		flag = true
	})
	server := New(map[string]agent.Agent{}, opt)

	assert.Len(t, server.runnerOpts, 1)
	server.runnerOpts[0](&runner.Options{})
	assert.True(t, flag)
}

func TestInMemoryExporterClearAndShutdown(t *testing.T) {
	exp := newInMemoryExporter()
	sessionID := "s1"
	traceID, err := trace.TraceIDFromHex("11111111111111111111111111111111")
	assert.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("1111111111111111")
	assert.NoError(t, err)
	exp.sessionTraces[sessionID] = map[string]struct{}{
		traceID.String(): {},
	}
	exp.spans = []sdktrace.ReadOnlySpan{
		tracetest.SpanStub{
			Name: "chat.completion",
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: traceID,
				SpanID:  spanID,
			}),
		}.Snapshot(),
	}

	exp.clear()
	assert.Empty(t, exp.spans)
	assert.NoError(t, exp.Shutdown(context.Background()))
}

func TestFilterGraphEventPartsVariants(t *testing.T) {
	parts := []map[string]any{{"key": "value"}}
	toolEvent := &event.Event{
		Response: &model.Response{Object: graph.ObjectTypeGraphNodeExecution},
		StateDelta: map[string][]byte{
			graph.MetadataKeyTool: []byte(`{}`),
		},
	}
	result := filterGraphEventParts(toolEvent, parts, true)
	assert.Equal(t, parts, result)

	execEvent := &event.Event{
		Response: &model.Response{Object: graph.ObjectTypeGraphExecution},
	}
	result = filterGraphEventParts(execEvent, parts, false)
	assert.Equal(t, parts, result)

	otherEvent := &event.Event{
		Response: &model.Response{Object: graph.ObjectTypeGraphNodeExecution},
	}
	result = filterGraphEventParts(otherEvent, parts, true)
	assert.Nil(t, result)
}

func TestIsGraphToolEventBranches(t *testing.T) {
	meta := map[string][]byte{
		graph.MetadataKeyTool: []byte(`{}`),
	}
	eventWithTool := &event.Event{
		Response:   &model.Response{Object: graph.ObjectTypeGraphNodeExecution},
		StateDelta: meta,
	}
	assert.True(t, isGraphToolEvent(eventWithTool))

	eventWithoutTool := &event.Event{
		Response: &model.Response{Object: graph.ObjectTypeGraphNodeExecution},
	}
	assert.False(t, isGraphToolEvent(eventWithoutTool))

	eventWrongType := &event.Event{
		Response: &model.Response{Object: graph.ObjectTypeGraphExecution},
		StateDelta: map[string][]byte{
			graph.MetadataKeyTool: []byte(`{}`),
		},
	}
	assert.False(t, isGraphToolEvent(eventWrongType))
}

func TestApiServerSpanExporterShutdown(t *testing.T) {
	exp := newApiServerSpanExporter(map[string]attribute.Set{})
	assert.NoError(t, exp.Shutdown(context.Background()))
}
