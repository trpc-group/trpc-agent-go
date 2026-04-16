//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package langfuse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	coreevaluation "trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	criterion "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	finalresponsecriterion "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	textcriterion "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	serverevaluation "trpc.group/trpc-go/trpc-agent-go/server/evaluation"
)

type fakeRunner struct {
	events []*event.Event
	err    error
}

func (f *fakeRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	ch := make(chan *event.Event, len(f.events))
	for _, evt := range f.events {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

func (f *fakeRunner) Close() error {
	return nil
}

type recordedAPIServer struct {
	mu             sync.Mutex
	dataset        *dataset
	traceRequests  []traceCreateRequest
	runItemRequest []datasetRunItemCreateRequest
	scoreRequests  []scoreCreateRequest
}

func (s *recordedAPIServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/api/public/datasets/demo-dataset":
		writeJSON(writer, request, http.StatusOK, s.dataset)
	case request.Method == http.MethodPost && request.URL.Path == "/api/public/traces":
		var req traceCreateRequest
		_ = json.NewDecoder(request.Body).Decode(&req)
		s.mu.Lock()
		s.traceRequests = append(s.traceRequests, req)
		s.mu.Unlock()
		writeJSON(writer, request, http.StatusOK, map[string]string{"id": req.ID})
	case request.Method == http.MethodPost && request.URL.Path == "/api/public/dataset-run-items":
		var req datasetRunItemCreateRequest
		_ = json.NewDecoder(request.Body).Decode(&req)
		s.mu.Lock()
		s.runItemRequest = append(s.runItemRequest, req)
		s.mu.Unlock()
		writeJSON(writer, request, http.StatusOK, map[string]any{
			"id":             "run-item-1",
			"datasetRunId":   "dataset-run-1",
			"datasetRunName": req.RunName,
			"datasetItemId":  req.DatasetItemID,
			"traceId":        req.TraceID,
		})
	case request.Method == http.MethodPost && request.URL.Path == "/api/public/scores":
		var req scoreCreateRequest
		_ = json.NewDecoder(request.Body).Decode(&req)
		s.mu.Lock()
		s.scoreRequests = append(s.scoreRequests, req)
		s.mu.Unlock()
		writeJSON(writer, request, http.StatusOK, map[string]string{"id": "score-1"})
	default:
		http.NotFound(writer, request)
	}
}

func makeFinalEvent(text string) *event.Event {
	return &event.Event{
		InvocationID: "generated-invocation",
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: text},
			}},
		},
	}
}

func newTestRuntime(
	t *testing.T,
	appName string,
	agentRunner runner.Runner,
) coreevaluation.AgentEvaluator {
	t.Helper()
	evalSetManager := evalsetinmemory.New()
	metricManager := metricinmemory.New()
	evalResultManager := evalresultinmemory.New()
	agentEvaluator, err := coreevaluation.New(
		appName,
		agentRunner,
		coreevaluation.WithEvalSetManager(evalSetManager),
		coreevaluation.WithMetricManager(metricManager),
		coreevaluation.WithEvalResultManager(evalResultManager),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, agentEvaluator.Close())
	})
	return agentEvaluator
}

func buildStringCaseSpec(ctx context.Context, item *DatasetItem) (*CaseSpec, error) {
	_ = ctx
	prompt, ok := item.Input.(string)
	if !ok {
		return nil, errors.New("input must be a string")
	}
	expectedOutput, ok := item.ExpectedOutput.(string)
	if !ok {
		return nil, errors.New("expected output must be a string")
	}
	return &CaseSpec{
		TraceInput: prompt,
		EvalCase: &evalset.EvalCase{
			EvalID: item.ID,
			Conversation: []*evalset.Invocation{{
				UserContent: &model.Message{
					Role:    model.RoleUser,
					Content: prompt,
				},
				FinalResponse: &model.Message{
					Role:    model.RoleAssistant,
					Content: expectedOutput,
				},
			}},
		},
	}, nil
}

func buildFinalResponseMetric() *metric.EvalMetric {
	return &metric.EvalMetric{
		MetricName: "final_response_avg_score",
		Threshold:  1,
		Criterion: criterion.New(
			criterion.WithFinalResponse(
				finalresponsecriterion.New(
					finalresponsecriterion.WithTextCriterion(textcriterion.New()),
				),
			),
		),
	}
}

func newTestMetricManager(t *testing.T) metric.Manager {
	t.Helper()
	metricManager := metricinmemory.New()
	t.Cleanup(func() {
		assert.NoError(t, metricManager.Close())
	})
	return metricManager
}

func newTestEvalSetManager(t *testing.T) evalset.Manager {
	t.Helper()
	evalSetManager := evalsetinmemory.New()
	t.Cleanup(func() {
		assert.NoError(t, evalSetManager.Close())
	})
	return evalSetManager
}

func newTestEvalResultManager(t *testing.T) evalresult.Manager {
	t.Helper()
	resultManager := evalresultinmemory.New()
	t.Cleanup(func() {
		assert.NoError(t, resultManager.Close())
	})
	return resultManager
}

func TestBuildCaseSpecBuildsFinalResponseCase(t *testing.T) {
	spec, err := buildCaseSpec(context.Background(), &DatasetItem{
		ID:             "item-1",
		Input:          "Say hello.",
		ExpectedOutput: "hello",
	})
	require.NoError(t, err)
	require.NotNil(t, spec)
	require.NotNil(t, spec.EvalCase)
	require.Len(t, spec.EvalCase.Conversation, 1)
	assert.Equal(t, "Say hello.", spec.EvalCase.Conversation[0].UserContent.Content)
	assert.Equal(t, "hello", spec.EvalCase.Conversation[0].FinalResponse.Content)
}

func TestBuildCaseSpecPreservesStringWhitespace(t *testing.T) {
	spec, err := buildCaseSpec(context.Background(), &DatasetItem{
		ID:             "item-1",
		Input:          "  Say hello.  ",
		ExpectedOutput: "  hello  ",
	})
	require.NoError(t, err)
	require.NotNil(t, spec)
	assert.Equal(t, "  Say hello.  ", spec.EvalCase.Conversation[0].UserContent.Content)
	assert.Equal(t, "  hello  ", spec.EvalCase.Conversation[0].FinalResponse.Content)
}

func TestBuildCaseSpecStringifiesObjectInput(t *testing.T) {
	spec, err := buildCaseSpec(context.Background(), &DatasetItem{
		ID:             "item-2",
		Input:          map[string]any{"question": "Say hello."},
		ExpectedOutput: "hello",
	})
	require.NoError(t, err)
	require.NotNil(t, spec)
	assert.Equal(t, "{\"question\":\"Say hello.\"}", spec.EvalCase.Conversation[0].UserContent.Content)
}

func TestBuildCaseSpecStringifiesObjectExpectedOutput(t *testing.T) {
	spec, err := buildCaseSpec(context.Background(), &DatasetItem{
		ID:             "item-3",
		Input:          "Say hello.",
		ExpectedOutput: map[string]any{"content": "hello"},
	})
	require.NoError(t, err)
	require.NotNil(t, spec)
	assert.Equal(t, "{\"content\":\"hello\"}", spec.EvalCase.Conversation[0].FinalResponse.Content)
}

func TestBuildCaseSpecAllowsEmptyExpectedOutput(t *testing.T) {
	spec, err := buildCaseSpec(context.Background(), &DatasetItem{
		ID:             "item-4",
		Input:          "Say hello.",
		ExpectedOutput: nil,
	})
	require.NoError(t, err)
	require.NotNil(t, spec)
	assert.Equal(t, "", spec.EvalCase.Conversation[0].FinalResponse.Content)
}

func TestNormalizeCaseSpecUsesExplicitUserIDForSessionInput(t *testing.T) {
	handler := &Handler{appName: "demo-app"}
	spec, err := handler.normalizeCaseSpec(
		&remoteExperimentRequest{
			ProjectID:   "project-1",
			DatasetID:   "dataset-1",
			DatasetName: "demo-dataset",
		},
		executionOptions{
			runName: "nightly-run",
			userID:  "default-user",
		},
		&DatasetItem{ID: "item-1"},
		&CaseSpec{
			UserID: "case-user",
			EvalCase: &evalset.EvalCase{
				EvalID: "item-1",
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, spec)
	require.NotNil(t, spec.EvalCase)
	require.NotNil(t, spec.EvalCase.SessionInput)
	assert.Equal(t, "case-user", spec.UserID)
	assert.Equal(t, "case-user", spec.EvalCase.SessionInput.UserID)
}

func TestResolveSessionIDPrefersCaseSpec(t *testing.T) {
	sessionID := resolveSessionID(&coreevaluation.EvaluationInferenceDetails{
		SessionID: "inference-session",
	}, &CaseSpec{
		SessionID: "case-session",
	})
	assert.Equal(t, "case-session", sessionID)
}

func TestNewValidatesRequiredOptions(t *testing.T) {
	t.Setenv("LANGFUSE_BASE_URL", "")
	t.Setenv("LANGFUSE_PUBLIC_KEY", "")
	t.Setenv("LANGFUSE_SECRET_KEY", "")
	t.Setenv("LANGFUSE_HOST", "")
	evalSetManager := newTestEvalSetManager(t)
	metricManager := newTestMetricManager(t)
	resultManager := newTestEvalResultManager(t)
	_, err := New(
		"",
		&fakeAgentEvaluator{},
		evalSetManager,
		metricManager,
		resultManager,
		WithBaseURL("http://example.com"),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "app name")
	_, err = New(
		"demo-app",
		&fakeAgentEvaluator{},
		evalSetManager,
		metricManager,
		resultManager,
		WithPublicKey("pk"),
		WithSecretKey("sk"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base URL")
	_, err = New(
		"demo-app",
		nil,
		evalSetManager,
		metricManager,
		resultManager,
		WithBaseURL("http://example.com"),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent evaluator")
	_, err = New(
		"demo-app",
		&fakeAgentEvaluator{},
		nil,
		metricManager,
		resultManager,
		WithBaseURL("http://example.com"),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "eval set manager")
	_, err = New(
		"demo-app",
		&fakeAgentEvaluator{},
		evalSetManager,
		nil,
		resultManager,
		WithBaseURL("http://example.com"),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metric manager")
	_, err = New(
		"demo-app",
		&fakeAgentEvaluator{},
		evalSetManager,
		metricManager,
		nil,
		WithBaseURL("http://example.com"),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "eval result manager")
	_, err = New(
		"demo-app",
		&fakeAgentEvaluator{},
		evalSetManager,
		metricManager,
		resultManager,
		WithBaseURL("http://example.com"),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
		WithUserIDSupplier(nil),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user id supplier")
	_, err = New(
		"demo-app",
		&fakeAgentEvaluator{},
		evalSetManager,
		metricManager,
		resultManager,
		WithBaseURL("http://example.com"),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
		WithHTTPClient(nil),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http client")
	_, err = New(
		"demo-app",
		&fakeAgentEvaluator{},
		evalSetManager,
		metricManager,
		resultManager,
		WithBaseURL("http://example.com"),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
		WithCaseBuilder(nil),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "case builder")
	_, err = New(
		"demo-app",
		&fakeAgentEvaluator{},
		evalSetManager,
		metricManager,
		resultManager,
		WithBaseURL("http://example.com"),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
		WithEnvironment(""),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "environment")
	_, err = New(
		"demo-app",
		&fakeAgentEvaluator{},
		evalSetManager,
		metricManager,
		resultManager,
		WithBaseURL("http://example.com"),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
		WithTimeout(-time.Second),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
	_, err = New(
		"demo-app",
		&fakeAgentEvaluator{},
		evalSetManager,
		metricManager,
		resultManager,
		WithBaseURL("http://example.com"),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
		WithPath(""),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path")
	_, err = New(
		"demo-app",
		&fakeAgentEvaluator{},
		evalSetManager,
		metricManager,
		resultManager,
		WithBaseURL("http://example.com"),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
	)
	require.NoError(t, err)
}

func TestSyncEvalSetUpsertsAndDeletesCases(t *testing.T) {
	evalSetManager := evalsetinmemory.New()
	t.Cleanup(func() {
		assert.NoError(t, evalSetManager.Close())
	})
	handler, err := New(
		"demo-app",
		&fakeAgentEvaluator{},
		evalSetManager,
		newTestMetricManager(t),
		newTestEvalResultManager(t),
		WithBaseURL("http://example.com"),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
	)
	require.NoError(t, err)
	ctx := context.Background()
	_, err = evalSetManager.Create(ctx, "demo-app", "dataset-1")
	require.NoError(t, err)
	require.NoError(t, evalSetManager.AddCase(ctx, "demo-app", "dataset-1", &evalset.EvalCase{
		EvalID: "stale-case",
		Conversation: []*evalset.Invocation{{
			UserContent:   &model.Message{Role: model.RoleUser, Content: "old"},
			FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "old"},
		}},
	}))
	require.NoError(t, handler.syncEvalSet(ctx, "dataset-1", []*CaseSpec{
		{
			EvalCase: &evalset.EvalCase{
				EvalID: "item-1",
				Conversation: []*evalset.Invocation{{
					UserContent:   &model.Message{Role: model.RoleUser, Content: "new"},
					FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "answer"},
				}},
			},
		},
	}))
	evalSet, err := evalSetManager.Get(ctx, "demo-app", "dataset-1")
	require.NoError(t, err)
	require.Len(t, evalSet.EvalCases, 1)
	assert.Equal(t, "item-1", evalSet.EvalCases[0].EvalID)
	assert.Equal(t, "new", evalSet.EvalCases[0].Conversation[0].UserContent.Content)
}

func TestNewUsesLangfuseEnvDefaults(t *testing.T) {
	t.Setenv("LANGFUSE_BASE_URL", "http://env.langfuse.local:3000")
	t.Setenv("LANGFUSE_PUBLIC_KEY", "env-public-key")
	t.Setenv("LANGFUSE_SECRET_KEY", "env-secret-key")
	t.Setenv("LANGFUSE_HOST", "")
	handler, err := New(
		"demo-app",
		&fakeAgentEvaluator{},
		newTestEvalSetManager(t),
		newTestMetricManager(t),
		newTestEvalResultManager(t),
	)
	require.NoError(t, err)
	require.NotNil(t, handler)
}

func TestHandlerServeHTTPExecutesRemoteExperiment(t *testing.T) {
	apiServer := &recordedAPIServer{
		dataset: &dataset{
			ID:        "dataset-1",
			ProjectID: "project-1",
			Name:      "demo-dataset",
			Items: []*DatasetItem{{
				ID:             "item-1",
				DatasetID:      "dataset-1",
				Input:          "say hello",
				ExpectedOutput: "hello",
				Metadata:       map[string]any{"suite": "demo"},
			}},
		},
	}
	testServer := httptest.NewServer(apiServer)
	defer testServer.Close()
	agentEvaluator := newTestRuntime(
		t,
		"demo-app",
		&fakeRunner{events: []*event.Event{makeFinalEvent("hello")}},
	)
	metricManager := metricinmemory.New()
	t.Cleanup(func() {
		assert.NoError(t, metricManager.Close())
	})
	require.NoError(t, metricManager.Add(context.Background(), "demo-app", "dataset-1", buildFinalResponseMetric()))
	handler, err := New(
		"demo-app",
		agentEvaluator,
		newTestEvalSetManager(t),
		metricManager,
		newTestEvalResultManager(t),
		WithBaseURL(testServer.URL),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
		WithCaseBuilder(buildStringCaseSpec),
		WithTraceTags("framework-tag"),
		WithUserIDSupplier(func(_ context.Context) string {
			return "demo-user"
		}),
		WithEnvironment("staging"),
	)
	require.NoError(t, err)
	requestBody, err := json.Marshal(remoteExperimentRequest{
		ProjectID:   "project-1",
		DatasetID:   "dataset-1",
		DatasetName: "demo-dataset",
		Payload: map[string]any{
			"runName":        "nightly-run",
			"runDescription": "Nightly regression run",
		},
	})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, handler.Path(), bytes.NewReader(requestBody))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response remoteExperimentResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "dataset-run-1", response.DatasetRunID)
	assert.Equal(t, "nightly-run", response.RunName)
	assert.Equal(t, 1, response.ProcessedCases)
	assert.Equal(t, 1, response.TraceCount)
	assert.Equal(t, 3, response.ScoreCount)
	assert.Equal(t, 1.0, response.AggregateScores["pass_rate"])
	assert.Equal(t, 1.0, response.AggregateScores["final_response_avg_score_mean"])
	assert.Equal(t, "1/1 cases passed.", response.AggregateReasons["pass_rate"])
	apiServer.mu.Lock()
	defer apiServer.mu.Unlock()
	require.Len(t, apiServer.traceRequests, 1)
	require.Len(t, apiServer.runItemRequest, 1)
	require.Len(t, apiServer.scoreRequests, 3)
	assert.Equal(t, "nightly-run/item-1", apiServer.traceRequests[0].Name)
	assert.Equal(t, "demo-user", apiServer.traceRequests[0].UserID)
	assert.Equal(t, "staging", apiServer.traceRequests[0].Environment)
	assert.Equal(t, []string{"remote-experiment", "trpc-agent-go", "framework-tag"}, apiServer.traceRequests[0].Tags)
	assert.Equal(t, "Nightly regression run", apiServer.runItemRequest[0].RunDescription)
	assert.Equal(t, "dataset-run-1", apiServer.scoreRequests[1].DatasetRunID)
	assert.Equal(t, "staging", apiServer.scoreRequests[0].Environment)
	assert.NotEmpty(t, apiServer.scoreRequests[0].Comment)
}

func TestHandlerServeHTTPPayloadOverridesFrameworkDefaults(t *testing.T) {
	apiServer := &recordedAPIServer{
		dataset: &dataset{
			ID:        "dataset-1",
			ProjectID: "project-1",
			Name:      "demo-dataset",
			Items: []*DatasetItem{{
				ID:             "item-1",
				DatasetID:      "dataset-1",
				Input:          "say hello",
				ExpectedOutput: "hello",
			}},
		},
	}
	testServer := httptest.NewServer(apiServer)
	defer testServer.Close()
	agentEvaluator := newTestRuntime(
		t,
		"demo-app",
		&fakeRunner{events: []*event.Event{makeFinalEvent("hello")}},
	)
	metricManager := metricinmemory.New()
	t.Cleanup(func() {
		assert.NoError(t, metricManager.Close())
	})
	require.NoError(t, metricManager.Add(context.Background(), "demo-app", "dataset-1", buildFinalResponseMetric()))
	handler, err := New(
		"demo-app",
		agentEvaluator,
		newTestEvalSetManager(t),
		metricManager,
		newTestEvalResultManager(t),
		WithBaseURL(testServer.URL),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
		WithCaseBuilder(buildStringCaseSpec),
		WithTraceTags("framework-tag"),
		WithUserIDSupplier(func(_ context.Context) string {
			return "framework-user"
		}),
	)
	require.NoError(t, err)
	requestBody, err := json.Marshal(remoteExperimentRequest{
		ProjectID:   "project-1",
		DatasetID:   "dataset-1",
		DatasetName: "demo-dataset",
		Payload: map[string]any{
			"runName":   "nightly-run",
			"userId":    "payload-user",
			"traceTags": []string{"payload-tag-a", "payload-tag-b"},
		},
	})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, handler.Path(), bytes.NewReader(requestBody))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	apiServer.mu.Lock()
	defer apiServer.mu.Unlock()
	require.Len(t, apiServer.traceRequests, 1)
	assert.Equal(t, "payload-user", apiServer.traceRequests[0].UserID)
	assert.Equal(t, []string{"payload-tag-a", "payload-tag-b"}, apiServer.traceRequests[0].Tags)
}

func TestHandlerServeHTTPUsesConfiguredManagers(t *testing.T) {
	apiServer := &recordedAPIServer{
		dataset: &dataset{
			ID:        "dataset-1",
			ProjectID: "project-1",
			Name:      "demo-dataset",
			Items: []*DatasetItem{{
				ID:             "item-1",
				DatasetID:      "dataset-1",
				Input:          "say hello",
				ExpectedOutput: "hello",
			}},
		},
	}
	testServer := httptest.NewServer(apiServer)
	defer testServer.Close()
	agentEvaluator := newTestRuntime(
		t,
		"demo-app",
		&fakeRunner{events: []*event.Event{makeFinalEvent("hello")}},
	)
	evalSetManager := evalsetinmemory.New()
	metricManager := metricinmemory.New()
	resultManager := evalresultinmemory.New()
	t.Cleanup(func() {
		assert.NoError(t, evalSetManager.Close())
		assert.NoError(t, metricManager.Close())
		assert.NoError(t, resultManager.Close())
	})
	require.NoError(t, metricManager.Add(context.Background(), "demo-app", "dataset-1", buildFinalResponseMetric()))
	handler, err := New(
		"demo-app",
		agentEvaluator,
		evalSetManager,
		metricManager,
		resultManager,
		WithBaseURL(testServer.URL),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
		WithCaseBuilder(buildStringCaseSpec),
	)
	require.NoError(t, err)
	requestBody, err := json.Marshal(remoteExperimentRequest{
		ProjectID:   "project-1",
		DatasetID:   "dataset-1",
		DatasetName: "demo-dataset",
		Payload: map[string]any{
			"runName": "custom-manager-run",
		},
	})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, handler.Path(), bytes.NewReader(requestBody))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	resultIDs, err := resultManager.List(context.Background(), "demo-app")
	require.NoError(t, err)
	assert.Len(t, resultIDs, 1)
	evalSetIDs, err := evalSetManager.List(context.Background(), "demo-app")
	require.NoError(t, err)
	assert.Equal(t, []string{"dataset-1"}, evalSetIDs)
	evalSet, err := evalSetManager.Get(context.Background(), "demo-app", "dataset-1")
	require.NoError(t, err)
	require.Len(t, evalSet.EvalCases, 1)
	assert.Equal(t, "item-1", evalSet.EvalCases[0].EvalID)
}

func TestHandlerRegisterRoutesMountsUnderEvaluationBasePath(t *testing.T) {
	agentEvaluator := newTestRuntime(t, "demo-app", &fakeRunner{})
	evalSetManager := evalsetinmemory.New()
	resultManager := evalresultinmemory.New()
	t.Cleanup(func() {
		assert.NoError(t, evalSetManager.Close())
		assert.NoError(t, resultManager.Close())
	})
	handler, err := New(
		"demo-app",
		agentEvaluator,
		evalSetManager,
		newTestMetricManager(t),
		resultManager,
		WithBaseURL("http://example.com"),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
		WithCaseBuilder(func(ctx context.Context, item *DatasetItem) (*CaseSpec, error) {
			return nil, errors.New("not used")
		}),
	)
	require.NoError(t, err)
	server, err := serverevaluation.New(
		serverevaluation.WithAppName("demo-app"),
		serverevaluation.WithAgentEvaluator(agentEvaluator),
		serverevaluation.WithEvalSetManager(evalSetManager),
		serverevaluation.WithEvalResultManager(resultManager),
		serverevaluation.WithRouteRegistrar(handler),
	)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodGet, "/evaluation/langfuse/remote-experiment", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
}

type fakeAgentEvaluator struct{}

func (f *fakeAgentEvaluator) Evaluate(ctx context.Context, evalSetID string, opt ...coreevaluation.Option) (*coreevaluation.EvaluationResult, error) {
	return &coreevaluation.EvaluationResult{}, nil
}

func (f *fakeAgentEvaluator) Close() error {
	return nil
}
