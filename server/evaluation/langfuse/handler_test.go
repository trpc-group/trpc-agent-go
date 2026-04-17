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
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
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
	evalstatus "trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	serverevaluation "trpc.group/trpc-go/trpc-agent-go/server/evaluation"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
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

func TestBuildCaseSpecRejectsNilItem(t *testing.T) {
	spec, err := buildCaseSpec(context.Background(), nil)
	require.Error(t, err)
	assert.Nil(t, spec)
}

func TestBuildCaseSpecRejectsNonSerializableInput(t *testing.T) {
	spec, err := buildCaseSpec(context.Background(), &DatasetItem{
		ID:    "item-5",
		Input: map[string]any{"unsupported": make(chan int)},
	})
	require.Error(t, err)
	assert.Nil(t, spec)
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

func TestResolveSessionIDFallsBackToInferenceAndEmpty(t *testing.T) {
	sessionID := resolveSessionID(&coreevaluation.EvaluationInferenceDetails{
		SessionID: "inference-session",
	}, &CaseSpec{})
	assert.Equal(t, "inference-session", sessionID)
	assert.Equal(t, "", resolveSessionID(nil, &CaseSpec{}))
}

func TestResolveUserIDPrefersInferenceDetail(t *testing.T) {
	userID := resolveUserID(&coreevaluation.EvaluationInferenceDetails{
		UserID: "inference-user",
	}, &CaseSpec{
		UserID: "case-user",
	}, executionOptions{userID: "default-user"})
	assert.Equal(t, "inference-user", userID)
}

func TestResolveUserIDFallsBackToCaseSpecAndDefault(t *testing.T) {
	userID := resolveUserID(nil, &CaseSpec{UserID: "case-user"}, executionOptions{userID: "default-user"})
	assert.Equal(t, "case-user", userID)
	userID = resolveUserID(nil, &CaseSpec{}, executionOptions{userID: "default-user"})
	assert.Equal(t, "default-user", userID)
}

func TestResolveMetricReasonFallsBackToDefaultMessage(t *testing.T) {
	reason := resolveMetricReason(&evalresult.EvalMetricResult{
		MetricName: "final_response_avg_score",
		EvalStatus: evalstatus.EvalStatusPassed,
		Score:      1,
		Threshold:  0.8,
	})
	assert.Contains(t, reason, "final_response_avg_score")
	assert.Contains(t, reason, "1.00")
}

func TestResolveMetricReasonUsesExplicitReason(t *testing.T) {
	reason := resolveMetricReason(&evalresult.EvalMetricResult{
		MetricName: "final_response_avg_score",
		Details: &evalresult.EvalMetricResultDetails{
			Reason: "explicit reason",
		},
	})
	assert.Equal(t, "explicit reason", reason)
}

func TestExtractFinalOutputValidatesInferenceDetails(t *testing.T) {
	_, err := extractFinalOutput(nil)
	require.Error(t, err)
	_, err = extractFinalOutput(&coreevaluation.EvaluationInferenceDetails{})
	require.Error(t, err)
	_, err = extractFinalOutput(&coreevaluation.EvaluationInferenceDetails{
		Inferences: []*evalset.Invocation{{}},
	})
	require.Error(t, err)
	output, err := extractFinalOutput(&coreevaluation.EvaluationInferenceDetails{
		Inferences: []*evalset.Invocation{{
			FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "hello"},
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, "hello", output)
}

func TestResolveRemoteExperimentOptionsSupportsPayloadForms(t *testing.T) {
	handler := &Handler{
		traceTags: []string{"framework-tag"},
		userIDSupplier: func(_ context.Context) string {
			return "framework-user"
		},
	}
	ctx := context.Background()
	opts, err := handler.resolveRemoteExperimentOptions(ctx, nil, "demo-dataset")
	require.NoError(t, err)
	assert.Equal(t, "framework-user", opts.userID)
	assert.Equal(t, []string{"framework-tag"}, opts.traceTags)
	assert.Contains(t, opts.runName, "demo-dataset")
	opts, err = handler.resolveRemoteExperimentOptions(ctx, "nightly-run", "demo-dataset")
	require.NoError(t, err)
	assert.Equal(t, "nightly-run", opts.runName)
	opts, err = handler.resolveRemoteExperimentOptions(ctx, `{"runName":"json-run","userId":"payload-user","traceTags":["payload-tag"]}`, "demo-dataset")
	require.NoError(t, err)
	assert.Equal(t, "json-run", opts.runName)
	assert.Equal(t, "payload-user", opts.userID)
	assert.Equal(t, []string{"payload-tag"}, opts.traceTags)
}

func TestResolveRemoteExperimentOptionsRejectsUnmarshallablePayload(t *testing.T) {
	handler := &Handler{
		traceTags: []string{"framework-tag"},
		userIDSupplier: func(_ context.Context) string {
			return "framework-user"
		},
	}
	_, err := handler.resolveRemoteExperimentOptions(context.Background(), map[string]any{
		"runName": make(chan int),
	}, "demo-dataset")
	require.Error(t, err)
}

func TestRegisterRoutesValidatesArguments(t *testing.T) {
	handler := &Handler{path: "/langfuse/remote-experiment"}
	err := handler.RegisterRoutes(nil, &serverevaluation.Server{})
	require.Error(t, err)
	err = handler.RegisterRoutes(http.NewServeMux(), nil)
	require.Error(t, err)
}

func TestServeHTTPValidatesMethodAndRequestBody(t *testing.T) {
	handler := &Handler{timeout: time.Second}
	methodRequest := httptest.NewRequest(http.MethodGet, "/langfuse/remote-experiment", nil)
	methodRecorder := httptest.NewRecorder()
	handler.ServeHTTP(methodRecorder, methodRequest)
	assert.Equal(t, http.StatusMethodNotAllowed, methodRecorder.Code)
	decodeRequest := httptest.NewRequest(http.MethodPost, "/langfuse/remote-experiment", bytes.NewBufferString("{"))
	decodeRecorder := httptest.NewRecorder()
	handler.ServeHTTP(decodeRecorder, decodeRequest)
	assert.Equal(t, http.StatusBadRequest, decodeRecorder.Code)
}

func TestServeHTTPValidatesDatasetNameAndDatasetFetch(t *testing.T) {
	handler := &Handler{timeout: time.Second}
	missingDatasetRequest := httptest.NewRequest(http.MethodPost, "/langfuse/remote-experiment", bytes.NewBufferString(`{"projectId":"project-1"}`))
	missingDatasetRecorder := httptest.NewRecorder()
	handler.ServeHTTP(missingDatasetRecorder, missingDatasetRequest)
	assert.Equal(t, http.StatusBadRequest, missingDatasetRecorder.Code)
	apiServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "upstream failed", http.StatusBadGateway)
	}))
	defer apiServer.Close()
	handler = &Handler{
		timeout:     time.Second,
		client:      newClient(apiServer.URL, "pk", "sk", apiServer.Client()),
		caseBuilder: buildCaseSpec,
		traceTags:   []string{"framework-tag"},
		userIDSupplier: func(_ context.Context) string {
			return "framework-user"
		},
	}
	fetchRequest := httptest.NewRequest(http.MethodPost, "/langfuse/remote-experiment", bytes.NewBufferString(`{"projectId":"project-1","datasetName":"demo-dataset"}`))
	fetchRecorder := httptest.NewRecorder()
	handler.ServeHTTP(fetchRecorder, fetchRequest)
	assert.Equal(t, http.StatusBadGateway, fetchRecorder.Code)
}

func TestServeHTTPReturnsBuildCaseErrors(t *testing.T) {
	apiServer := &recordedAPIServer{
		dataset: &dataset{
			ID:        "dataset-1",
			ProjectID: "project-1",
			Name:      "demo-dataset",
			Items: []*DatasetItem{{
				ID:        "item-1",
				DatasetID: "dataset-1",
				Input:     123,
			}},
		},
	}
	testServer := httptest.NewServer(apiServer)
	defer testServer.Close()
	handler, err := New(
		"demo-app",
		&fakeAgentEvaluator{},
		newTestEvalSetManager(t),
		newTestMetricManager(t),
		newTestEvalResultManager(t),
		WithBaseURL(testServer.URL),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
		WithCaseBuilder(buildStringCaseSpec),
	)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, handler.Path(), bytes.NewBufferString(`{"projectId":"project-1","datasetName":"demo-dataset"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestBuildCaseSpecsValidatesDatasetShapes(t *testing.T) {
	handler := &Handler{caseBuilder: buildStringCaseSpec}
	_, err := handler.buildCaseSpecs(context.Background(), &remoteExperimentRequest{}, executionOptions{}, nil)
	require.Error(t, err)
	_, err = handler.buildCaseSpecs(context.Background(), &remoteExperimentRequest{}, executionOptions{}, &dataset{Name: "demo-dataset"})
	require.Error(t, err)
	_, err = handler.buildCaseSpecs(context.Background(), &remoteExperimentRequest{}, executionOptions{}, &dataset{
		Name:  "demo-dataset",
		Items: []*DatasetItem{nil},
	})
	require.Error(t, err)
	_, err = handler.buildCaseSpecs(context.Background(), &remoteExperimentRequest{}, executionOptions{}, &dataset{
		Name: "demo-dataset",
		Items: []*DatasetItem{{
			ID: "item-1",
		}},
	})
	require.Error(t, err)
}

func TestResolveCaseArtifactsReturnsResolvedValues(t *testing.T) {
	handler := &Handler{}
	evaluationResult := buildEvaluationResult("item-1", []*evalresult.EvalMetricResult{{
		MetricName: "final_response_avg_score",
		Score:      1,
		Threshold:  1,
	}})
	caseAggregate, runCaseResult, inferenceDetail, err := handler.resolveCaseArtifacts(evaluationResult, "item-1")
	require.NoError(t, err)
	require.NotNil(t, caseAggregate)
	require.NotNil(t, runCaseResult)
	require.NotNil(t, inferenceDetail)
	assert.Equal(t, "item-1", caseAggregate.EvalCaseID)
	assert.Equal(t, "item-1", runCaseResult.EvalID)
	assert.Equal(t, "session-1", inferenceDetail.SessionID)
}

func TestResolveCaseArtifactsReturnsAggregateErrors(t *testing.T) {
	handler := &Handler{}
	_, _, _, err := handler.resolveCaseArtifacts(&coreevaluation.EvaluationResult{}, "item-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "eval case item-1 not found")
}

func TestResolveCaseArtifactsReturnsRunResultErrors(t *testing.T) {
	handler := &Handler{}
	evaluationResult := &coreevaluation.EvaluationResult{
		EvalCases: []*coreevaluation.EvaluationCaseResult{{
			EvalCaseID: "item-1",
		}},
	}
	_, _, _, err := handler.resolveCaseArtifacts(evaluationResult, "item-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not contain an eval set result")
}

func TestResolveCaseArtifactsReturnsInferenceErrors(t *testing.T) {
	handler := &Handler{}
	evaluationResult := &coreevaluation.EvaluationResult{
		EvalCases: []*coreevaluation.EvaluationCaseResult{{
			EvalCaseID: "item-1",
		}},
		EvalResult: &evalresult.EvalSetResult{
			EvalCaseResults: []*evalresult.EvalCaseResult{{
				EvalID: "item-1",
			}},
		},
	}
	_, _, _, err := handler.resolveCaseArtifacts(evaluationResult, "item-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "do not contain inference details")
}

func TestFindHelpersValidateMissingArtifacts(t *testing.T) {
	_, err := findEvaluationCaseResult(nil, "case-1")
	require.Error(t, err)
	_, err = findEvalCaseResult(&coreevaluation.EvaluationResult{}, "case-1")
	require.Error(t, err)
	_, err = findInferenceDetail(&coreevaluation.EvaluationCaseResult{EvalCaseID: "case-1"})
	require.Error(t, err)
}

func TestFindHelpersValidateNotFoundBranches(t *testing.T) {
	_, err := findEvaluationCaseResult(&coreevaluation.EvaluationResult{
		EvalCases: []*coreevaluation.EvaluationCaseResult{{
			EvalCaseID: "case-2",
		}},
	}, "case-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "eval case case-1 not found")
	_, err = findEvalCaseResult(&coreevaluation.EvaluationResult{
		EvalResult: &evalresult.EvalSetResult{
			EvalCaseResults: []*evalresult.EvalCaseResult{{
				EvalID: "case-2",
			}},
		},
	}, "case-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "eval case case-1 not found")
	_, err = findInferenceDetail(&coreevaluation.EvaluationCaseResult{
		EvalCaseID: "case-1",
		RunDetails: []*coreevaluation.EvaluationCaseRunDetails{{
			Inference: nil,
		}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "do not contain inference details")
}

func TestInjectRemoteTraceParentCreatesTraceContext(t *testing.T) {
	ctx, traceID, err := injectRemoteTraceParent(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, traceID)
	spanContext := oteltrace.SpanContextFromContext(ctx)
	assert.True(t, spanContext.IsValid())
	assert.True(t, spanContext.IsRemote())
}

func TestNewExecutionContextRespectsExistingDeadline(t *testing.T) {
	parent, cancelParent := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelParent()
	ctx, cancel := newExecutionContext(parent, time.Hour)
	defer cancel()
	parentDeadline, ok := parent.Deadline()
	require.True(t, ok)
	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	assert.WithinDuration(t, parentDeadline, deadline, 5*time.Millisecond)
}

func TestForceFlushTelemetryReturnsNilWithDefaultProvider(t *testing.T) {
	assert.NoError(t, forceFlushTelemetry(context.Background()))
}

func TestForceFlushTelemetryFlushesSDKProvider(t *testing.T) {
	previousProvider := atrace.TracerProvider
	provider := sdktrace.NewTracerProvider()
	atrace.TracerProvider = provider
	t.Cleanup(func() {
		assert.NoError(t, provider.Shutdown(context.Background()))
		atrace.TracerProvider = previousProvider
	})
	assert.NoError(t, forceFlushTelemetry(context.Background()))
}

func TestDefaultRunNameFallsBackToTimestamp(t *testing.T) {
	runName := defaultRunName(" ")
	assert.NotEmpty(t, runName)
	assert.NotContains(t, runName, " ")
}

func TestAverageFloat64HandlesEmptyAndMultipleValues(t *testing.T) {
	assert.Equal(t, 0.0, averageFloat64(nil))
	assert.Equal(t, 2.0, averageFloat64([]float64{1, 2, 3}))
}

func TestLogResponseWriteErrorAcceptsRequest(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/langfuse/remote-experiment", nil)
	logResponseWriteError(nil, errors.New("nil request"))
	logResponseWriteError(request, errors.New("request error"))
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

func TestHandlerServeHTTPExecutesRemoteExperimentForMultipleItems(t *testing.T) {
	apiServer := &recordedAPIServer{
		dataset: &dataset{
			ID:        "dataset-1",
			ProjectID: "project-1",
			Name:      "demo-dataset",
			Items: []*DatasetItem{
				{
					ID:             "item-1",
					DatasetID:      "dataset-1",
					Input:          "say hello",
					ExpectedOutput: "hello",
				},
				{
					ID:             "item-2",
					DatasetID:      "dataset-1",
					Input:          "say hello again",
					ExpectedOutput: "hello",
				},
			},
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
	)
	require.NoError(t, err)
	requestBody, err := json.Marshal(remoteExperimentRequest{
		ProjectID:   "project-1",
		DatasetID:   "dataset-1",
		DatasetName: "demo-dataset",
		Payload: map[string]any{
			"runName": "nightly-run",
		},
	})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, handler.Path(), bytes.NewReader(requestBody))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response remoteExperimentResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, 2, response.ProcessedCases)
	assert.Equal(t, 2, response.TraceCount)
	assert.Equal(t, 4, response.ScoreCount)
	apiServer.mu.Lock()
	defer apiServer.mu.Unlock()
	require.Len(t, apiServer.traceRequests, 2)
	require.Len(t, apiServer.runItemRequest, 2)
	require.Len(t, apiServer.scoreRequests, 4)
	assert.Equal(t, "nightly-run/item-1", apiServer.traceRequests[0].Name)
	assert.Equal(t, "nightly-run/item-2", apiServer.traceRequests[1].Name)
}

func TestProcessCaseReturnsTraceCreationErrors(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/public/traces":
			writeJSON(writer, request, http.StatusBadGateway, errorResponse{Message: "trace failed"})
		case request.Method == http.MethodPost && request.URL.Path == "/api/public/dataset-run-items":
			writeJSON(writer, request, http.StatusOK, map[string]any{
				"id":             "run-item-1",
				"datasetRunId":   "dataset-run-1",
				"datasetRunName": "nightly-run",
				"datasetItemId":  "item-1",
				"traceId":        "trace-1",
			})
		case request.Method == http.MethodPost && request.URL.Path == "/api/public/scores":
			writeJSON(writer, request, http.StatusOK, map[string]string{"id": "score-1"})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer apiServer.Close()
	agentEvaluator := &fakeAgentEvaluator{
		result: &coreevaluation.EvaluationResult{
			EvalCases: []*coreevaluation.EvaluationCaseResult{{
				EvalCaseID:    "item-1",
				OverallStatus: evalstatus.EvalStatusPassed,
				RunDetails: []*coreevaluation.EvaluationCaseRunDetails{{
					Inference: &coreevaluation.EvaluationInferenceDetails{
						SessionID: "session-1",
						UserID:    "demo-user",
						Inferences: []*evalset.Invocation{{
							FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "hello"},
						}},
					},
				}},
			}},
			EvalResult: &evalresult.EvalSetResult{
				EvalCaseResults: []*evalresult.EvalCaseResult{{
					EvalID:          "item-1",
					FinalEvalStatus: evalstatus.EvalStatusPassed,
				}},
			},
		},
	}
	evalSetManager := newTestEvalSetManager(t)
	metricManager := newTestMetricManager(t)
	require.NoError(t, metricManager.Add(context.Background(), "demo-app", "dataset-1", buildFinalResponseMetric()))
	handler, err := New(
		"demo-app",
		agentEvaluator,
		evalSetManager,
		metricManager,
		newTestEvalResultManager(t),
		WithBaseURL(apiServer.URL),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
	)
	require.NoError(t, err)
	require.NoError(t, handler.syncEvalSet(context.Background(), "dataset-1", []*CaseSpec{{
		DatasetItemID: "item-1",
		TraceInput:    "say hello",
		EvalCase: &evalset.EvalCase{
			EvalID: "item-1",
			Conversation: []*evalset.Invocation{{
				UserContent:   &model.Message{Role: model.RoleUser, Content: "say hello"},
				FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "hello"},
			}},
		},
	}}))
	_, _, _, err = handler.processCase(context.Background(), "dataset-1", executionOptions{
		runName:   "nightly-run",
		userID:    "demo-user",
		traceTags: []string{"framework-tag"},
	}, &CaseSpec{
		DatasetItemID: "item-1",
		TraceName:     "nightly-run/item-1",
		TraceInput:    "say hello",
		UserID:        "demo-user",
		EvalCase: &evalset.EvalCase{
			EvalID: "item-1",
			Conversation: []*evalset.Invocation{{
				UserContent:   &model.Message{Role: model.RoleUser, Content: "say hello"},
				FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "hello"},
			}},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create trace")
}

func TestProcessCaseReturnsDatasetRunItemCreationErrors(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/public/traces":
			writeJSON(writer, request, http.StatusOK, map[string]string{"id": "trace-1"})
		case request.Method == http.MethodPost && request.URL.Path == "/api/public/dataset-run-items":
			http.Error(writer, "run item failed", http.StatusBadGateway)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer apiServer.Close()
	handler, err := New(
		"demo-app",
		&fakeAgentEvaluator{
			result: buildEvaluationResult("item-1", nil),
		},
		newTestEvalSetManager(t),
		newTestMetricManager(t),
		newTestEvalResultManager(t),
		WithBaseURL(apiServer.URL),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
	)
	require.NoError(t, err)
	_, _, _, err = handler.processCase(context.Background(), "dataset-1", executionOptions{
		runName: "nightly-run",
		userID:  "demo-user",
	}, buildTestCaseSpec("item-1"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create dataset run item")
}

func TestProcessCaseReturnsScoreCreationErrors(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/public/traces":
			writeJSON(writer, request, http.StatusOK, map[string]string{"id": "trace-1"})
		case request.Method == http.MethodPost && request.URL.Path == "/api/public/dataset-run-items":
			writeJSON(writer, request, http.StatusOK, map[string]any{
				"id":             "run-item-1",
				"datasetRunId":   "dataset-run-1",
				"datasetRunName": "nightly-run",
				"datasetItemId":  "item-1",
				"traceId":        "trace-1",
			})
		case request.Method == http.MethodPost && request.URL.Path == "/api/public/scores":
			http.Error(writer, "score failed", http.StatusBadGateway)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer apiServer.Close()
	handler, err := New(
		"demo-app",
		&fakeAgentEvaluator{
			result: buildEvaluationResult("item-1", []*evalresult.EvalMetricResult{{
				MetricName: "final_response_avg_score",
				Score:      1,
				Threshold:  1,
			}}),
		},
		newTestEvalSetManager(t),
		newTestMetricManager(t),
		newTestEvalResultManager(t),
		WithBaseURL(apiServer.URL),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
	)
	require.NoError(t, err)
	_, _, scoreCount, err := handler.processCase(context.Background(), "dataset-1", executionOptions{
		runName: "nightly-run",
		userID:  "demo-user",
	}, buildTestCaseSpec("item-1"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create score final_response_avg_score")
	assert.Equal(t, 0, scoreCount)
}

func TestProcessCaseReturnsSummaryWithoutMetricReasons(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/public/traces":
			writeJSON(writer, request, http.StatusOK, map[string]string{"id": "trace-1"})
		case request.Method == http.MethodPost && request.URL.Path == "/api/public/dataset-run-items":
			writeJSON(writer, request, http.StatusOK, map[string]any{
				"id":             "run-item-1",
				"datasetRunId":   "dataset-run-1",
				"datasetRunName": "nightly-run",
				"datasetItemId":  "item-1",
				"traceId":        "trace-1",
			})
		case request.Method == http.MethodPost && request.URL.Path == "/api/public/scores":
			writeJSON(writer, request, http.StatusOK, map[string]string{"id": "score-1"})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer apiServer.Close()
	handler, err := New(
		"demo-app",
		&fakeAgentEvaluator{
			result: buildEvaluationResult("item-1", nil),
		},
		newTestEvalSetManager(t),
		newTestMetricManager(t),
		newTestEvalResultManager(t),
		WithBaseURL(apiServer.URL),
		WithPublicKey("pk"),
		WithSecretKey("sk"),
	)
	require.NoError(t, err)
	summary, datasetRunID, scoreCount, err := handler.processCase(context.Background(), "dataset-1", executionOptions{
		runName: "nightly-run",
		userID:  "demo-user",
	}, buildTestCaseSpec("item-1"))
	require.NoError(t, err)
	require.NotNil(t, summary)
	assert.Equal(t, "dataset-run-1", datasetRunID)
	assert.Equal(t, 0, scoreCount)
	assert.Empty(t, summary.MetricScores)
	assert.Nil(t, summary.MetricReasons)
}

func TestSyncEvalSetReturnsDeleteErrorsForExistingEvalSet(t *testing.T) {
	handler := &Handler{
		appName:        "demo-app",
		evalSetManager: &stubEvalSetManager{delegate: newTestEvalSetManager(t), deleteErr: errors.New("delete failed")},
	}
	_, err := handler.evalSetManager.Create(context.Background(), "demo-app", "dataset-1")
	require.NoError(t, err)
	err = handler.syncEvalSet(context.Background(), "dataset-1", []*CaseSpec{buildTestCaseSpec("item-1")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete eval set")
}

func TestSyncEvalSetReturnsCreateErrors(t *testing.T) {
	handler := &Handler{
		appName:        "demo-app",
		evalSetManager: &stubEvalSetManager{delegate: newTestEvalSetManager(t), createErr: errors.New("create failed")},
	}
	err := handler.syncEvalSet(context.Background(), "dataset-1", []*CaseSpec{buildTestCaseSpec("item-1")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create eval set")
}

func TestSyncEvalSetReturnsAddCaseErrors(t *testing.T) {
	handler := &Handler{
		appName: "demo-app",
		evalSetManager: &stubEvalSetManager{
			delegate:   newTestEvalSetManager(t),
			addCaseErr: errors.New("add case failed"),
		},
	}
	err := handler.syncEvalSet(context.Background(), "dataset-1", []*CaseSpec{buildTestCaseSpec("item-1")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "add eval case item-1")
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

type fakeAgentEvaluator struct {
	result *coreevaluation.EvaluationResult
	err    error
}

func (f *fakeAgentEvaluator) Evaluate(ctx context.Context, evalSetID string, opt ...coreevaluation.Option) (*coreevaluation.EvaluationResult, error) {
	return f.result, f.err
}

func (f *fakeAgentEvaluator) Close() error {
	return nil
}

func buildTestCaseSpec(evalCaseID string) *CaseSpec {
	return &CaseSpec{
		DatasetItemID: evalCaseID,
		TraceName:     "nightly-run/" + evalCaseID,
		TraceInput:    "say hello",
		UserID:        "demo-user",
		EvalCase: &evalset.EvalCase{
			EvalID: evalCaseID,
			Conversation: []*evalset.Invocation{{
				UserContent:   &model.Message{Role: model.RoleUser, Content: "say hello"},
				FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "hello"},
			}},
		},
	}
}

func buildEvaluationResult(
	evalCaseID string,
	metricResults []*evalresult.EvalMetricResult,
) *coreevaluation.EvaluationResult {
	return &coreevaluation.EvaluationResult{
		EvalCases: []*coreevaluation.EvaluationCaseResult{{
			EvalCaseID:    evalCaseID,
			OverallStatus: evalstatus.EvalStatusPassed,
			RunDetails: []*coreevaluation.EvaluationCaseRunDetails{{
				Inference: &coreevaluation.EvaluationInferenceDetails{
					SessionID: "session-1",
					UserID:    "demo-user",
					Inferences: []*evalset.Invocation{{
						FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "hello"},
					}},
				},
			}},
		}},
		EvalResult: &evalresult.EvalSetResult{
			EvalCaseResults: []*evalresult.EvalCaseResult{{
				EvalID:                   evalCaseID,
				FinalEvalStatus:          evalstatus.EvalStatusPassed,
				OverallEvalMetricResults: metricResults,
				SessionID:                "session-1",
				UserID:                   "demo-user",
			}},
		},
	}
}

type stubEvalSetManager struct {
	delegate   evalset.Manager
	deleteErr  error
	createErr  error
	addCaseErr error
}

func (m *stubEvalSetManager) Get(ctx context.Context, appName string, evalSetID string) (*evalset.EvalSet, error) {
	return m.delegate.Get(ctx, appName, evalSetID)
}

func (m *stubEvalSetManager) Create(ctx context.Context, appName string, evalSetID string) (*evalset.EvalSet, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	return m.delegate.Create(ctx, appName, evalSetID)
}

func (m *stubEvalSetManager) List(ctx context.Context, appName string) ([]string, error) {
	return m.delegate.List(ctx, appName)
}

func (m *stubEvalSetManager) Delete(ctx context.Context, appName string, evalSetID string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	return m.delegate.Delete(ctx, appName, evalSetID)
}

func (m *stubEvalSetManager) GetCase(ctx context.Context, appName string, evalSetID string, evalCaseID string) (*evalset.EvalCase, error) {
	return m.delegate.GetCase(ctx, appName, evalSetID, evalCaseID)
}

func (m *stubEvalSetManager) AddCase(ctx context.Context, appName string, evalSetID string, evalCase *evalset.EvalCase) error {
	if m.addCaseErr != nil {
		return m.addCaseErr
	}
	return m.delegate.AddCase(ctx, appName, evalSetID, evalCase)
}

func (m *stubEvalSetManager) UpdateCase(ctx context.Context, appName string, evalSetID string, evalCase *evalset.EvalCase) error {
	return m.delegate.UpdateCase(ctx, appName, evalSetID, evalCase)
}

func (m *stubEvalSetManager) DeleteCase(ctx context.Context, appName string, evalSetID string, evalCaseID string) error {
	return m.delegate.DeleteCase(ctx, appName, evalSetID, evalCaseID)
}

func (m *stubEvalSetManager) Close() error {
	return nil
}
