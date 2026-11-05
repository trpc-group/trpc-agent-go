//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package langfuse

import (
	"compress/gzip"
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	coreevaluation "trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	langfusetelemetry "trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// TestExperimentIDMatchesLangfuseDerivation pins the id to vectors computed with
// Langfuse's Python SDK (sha256 of the compact, non-ASCII-preserving JSON array,
// first 16 hex characters), including characters Go's encoder would escape.
func TestExperimentIDMatchesLangfuseDerivation(t *testing.T) {
	for _, tc := range []struct{ project, dataset, run, want string }{
		{"proj-1", "ds-1", "nightly-run", "8cc2bfb4cabd9c52"},
		{"p", "d", "run <a> & é 日本 x", "baf1657d0400a18e"},
		{"cm0abc", "cm0def", "", "6f7f881763098a69"},
		{"p", "d", `\u2028`, "3b9a4b4ed47da837"},
		{"p", "d", `\u2029`, "7f7d8c1fb439f380"},
	} {
		got, err := experimentID(tc.project, tc.dataset, tc.run)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got, "run %q", tc.run)
	}
}

func TestWithAPIVersionValidatesAndDefaultsToV3(t *testing.T) {
	newHandler := func(opts ...Option) (*Handler, error) {
		evaluator := &fakeAgentEvaluator{}
		return New("demo-app", evaluator, newTestEvalSetManager(t), newTestMetricManager(t), newTestEvalResultManager(t),
			append([]Option{WithBaseURL("http://localhost"), WithPublicKey("pk"), WithSecretKey("sk")}, opts...)...)
	}
	h, err := newHandler()
	require.NoError(t, err)
	assert.Equal(t, APIVersionV3, h.apiVersion)
	h, err = newHandler(WithAPIVersion(APIVersionV4))
	require.NoError(t, err)
	assert.Equal(t, APIVersionV4, h.apiVersion)
	_, err = newHandler(WithAPIVersion("v5"))
	require.ErrorContains(t, err, `unsupported API version "v5"`)
}

func TestV4CaseAttributesCarryTheFieldsTheTraceUpsertSent(t *testing.T) {
	h := &Handler{appName: "demo-app", apiVersion: APIVersionV4}
	attrs, err := h.v4CaseAttributes(&CaseSpec{
		DatasetItemID: "item-1",
		TraceInput:    map[string]any{"q": "hi"},
		TraceMetadata: map[string]any{"b": 2, "a": "x"},
	}, executionOptions{
		runName: "run", runDescription: "nightly", experimentID: "exp-1", datasetID: "ds-1",
		traceTags: []string{"t1", "t2"},
	}, "session-1")
	require.NoError(t, err)
	got := map[string]attribute.Value{}
	order := []string{}
	for _, kv := range attrs {
		got[string(kv.Key)] = kv.Value
		order = append(order, string(kv.Key))
	}
	assert.Equal(t, "exp-1", got[attrExperimentID].AsString())
	assert.Equal(t, "run", got[attrExperimentName].AsString())
	assert.NotContains(t, got, attrExperimentDesc, "the description belongs on the item span only")
	assert.Equal(t, "ds-1", got[attrExperimentDataset].AsString())
	assert.Equal(t, "item-1", got[attrExperimentItemID].AsString())
	assert.JSONEq(t, `{"appName":"demo-app","runName":"run"}`, got[attrExperimentMetadata].AsString())
	assert.Equal(t, "session-1", got[attrSessionID].AsString())
	assert.Equal(t, []string{"t1", "t2"}, got[attrTraceTags].AsStringSlice())
	assert.JSONEq(t, `{"q":"hi"}`, got[attrTraceInput].AsString())
	assert.Equal(t, "x", got["langfuse.trace.metadata.a"].AsString())
	assert.Equal(t, "2", got["langfuse.trace.metadata.b"].AsString())
	// Deterministic metadata order.
	assert.Less(t, indexOf(order, "langfuse.trace.metadata.a"), indexOf(order, "langfuse.trace.metadata.b"))
}

func TestV4CaseAttributesRejectUnserializableInput(t *testing.T) {
	h := &Handler{appName: "demo-app"}
	_, err := h.v4CaseAttributes(&CaseSpec{TraceInput: make(chan int)}, executionOptions{}, "")
	require.ErrorContains(t, err, "trace input must be JSON serializable")
}

func indexOf(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}

func TestV4RequiresProjectIDForTheExperimentID(t *testing.T) {
	h := &Handler{apiVersion: APIVersionV4}
	_, err := h.executeRemoteExperiment(context.Background(), &remoteExperimentRequest{DatasetID: "set"},
		executionOptions{runName: "run"}, []*CaseSpec{buildTestCaseSpec("one")})
	require.ErrorContains(t, err, "projectId is required")
}

// TestV4WritesNoLegacyEndpointsAndAttachesRunScoresToTheExperiment drives the
// real evaluator. The Langfuse API fake records any POST to the endpoints v4
// removes (traces) or deprecates (dataset-run-items); there must be none.
func TestV4WritesNoLegacyEndpointsAndAttachesRunScoresToTheExperiment(t *testing.T) {
	recording := startV4Telemetry(t)
	ctx := context.Background()
	actual, judge := &roleTraceRunner{role: 'a'}, &roleTraceRunner{role: 'j'}
	evaluator, err := coreevaluation.New("demo-app", actual,
		coreevaluation.WithJudgeRunner(judge), coreevaluation.WithJudgeRunnerNumSamples(1))
	require.NoError(t, err)
	defer evaluator.Close()
	api := &recordedAPIServer{}
	server := httptest.NewServer(api)
	defer server.Close()
	metrics := newTestMetricManager(t)
	require.NoError(t, metrics.Add(ctx, "demo-app", "set", &metric.EvalMetric{
		MetricName: "llm_final_response", Threshold: 1, Criterion: &criterion.Criterion{LLMJudge: &llm.LLMCriterion{}},
	}))
	h, err := New("demo-app", evaluator, newTestEvalSetManager(t), metrics, newTestEvalResultManager(t),
		WithBaseURL(server.URL), WithPublicKey("pk"), WithSecretKey("sk"), WithAPIVersion(APIVersionV4), WithEnvironment("ci"))
	require.NoError(t, err)
	specs := []*CaseSpec{buildTestCaseSpec("one"), buildTestCaseSpec("two")}
	for i, spec := range specs {
		spec.DatasetItemID = spec.EvalCase.EvalID
		spec.TraceName = "run/" + spec.EvalCase.EvalID
		spec.TraceMetadata = map[string]any{"index": i}
		spec.EvalCase.SessionInput = &evalset.SessionInput{AppName: "demo-app", UserID: "user"}
	}
	require.NoError(t, h.syncEvalSet(ctx, "set", specs))

	response, err := h.executeRemoteExperiment(ctx,
		&remoteExperimentRequest{ProjectID: "proj-1", DatasetID: "set"},
		executionOptions{runName: "nightly-run", runDescription: "desc", traceTags: []string{"tag-a"}}, specs)
	require.NoError(t, err)

	wantID, err := experimentID("proj-1", "set", "nightly-run")
	require.NoError(t, err)
	assert.Equal(t, wantID, response.DatasetRunID)
	assert.Equal(t, 2, response.ProcessedCases)

	api.mu.Lock()
	defer api.mu.Unlock()
	assert.Empty(t, api.traceRequests, "POST /api/public/traces is removed in Langfuse v4")
	assert.Empty(t, api.runItemRequest, "POST /api/public/dataset-run-items is deprecated in Langfuse v4")
	roots := map[string]string{}
	recording.mu.Lock()
	for _, span := range recording.spans {
		if span.Name == "experiment-item-task" {
			roots[hex.EncodeToString(span.TraceId)] = hex.EncodeToString(span.SpanId)
		}
	}
	recording.mu.Unlock()
	var runLevel, caseLevel int
	for _, score := range api.scoreRequests {
		if score.DatasetRunID != "" {
			runLevel++
			assert.Equal(t, wantID, score.DatasetRunID)
			assert.Empty(t, score.TraceID)
		} else {
			caseLevel++
			assert.NotEmpty(t, score.TraceID)
			require.NotEmpty(t, score.ObservationID)
			assert.Equal(t, roots[score.TraceID], score.ObservationID)
		}
	}
	assert.Equal(t, 2, runLevel, "pass_rate and one metric mean")
	assert.Equal(t, 2, caseLevel)

	require.Len(t, actual.observations, 2)
	for _, call := range actual.observations {
		got := map[string]string{}
		for _, kv := range call.options.SpanAttributes {
			got[string(kv.Key)] = kv.Value.Emit()
		}
		assert.Equal(t, wantID, got[attrExperimentID])
		assert.Equal(t, "nightly-run", got[attrExperimentName])
		assert.Empty(t, got[attrExperimentDesc])
		assert.Equal(t, "set", got[attrExperimentDataset])
		assert.Contains(t, []string{"one", "two"}, got[attrExperimentItemID])
		assert.Equal(t, call.session, got[attrSessionID])
		assert.Contains(t, got[attrTraceTags], "tag-a")
		// The trace attributes the runner already set are untouched.
		assert.Equal(t, "run/"+got[attrExperimentItemID], got["langfuse.trace.name"])
		assert.Equal(t, "ci", got["langfuse.environment"])
	}
}

type otlpRecording struct {
	mu      sync.Mutex
	spans   []*tracepb.Span
	headers []http.Header
}

func startV4Telemetry(t *testing.T) *otlpRecording {
	t.Helper()
	recording := &otlpRecording{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/public/otel/v1/traces", r.URL.Path)
		var body io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			reader, err := gzip.NewReader(r.Body)
			if !assert.NoError(t, err) {
				w.WriteHeader(400)
				return
			}
			defer reader.Close()
			body = reader
		}
		data, err := io.ReadAll(body)
		if !assert.NoError(t, err) {
			w.WriteHeader(400)
			return
		}
		var request collectortracepb.ExportTraceServiceRequest
		if !assert.NoError(t, proto.Unmarshal(data, &request)) {
			w.WriteHeader(400)
			return
		}
		recording.mu.Lock()
		recording.headers = append(recording.headers, r.Header.Clone())
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				recording.spans = append(recording.spans, scope.Spans...)
			}
		}
		recording.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	oldProvider, oldTracer := atrace.TracerProvider, atrace.Tracer
	atrace.TracerProvider = noop.NewTracerProvider()
	cleanup, err := langfusetelemetry.Start(context.Background(),
		langfusetelemetry.WithHost(strings.TrimPrefix(server.URL, "http://")),
		langfusetelemetry.WithPublicKey("pk"), langfusetelemetry.WithSecretKey("sk"),
		langfusetelemetry.WithInsecure(), langfusetelemetry.WithIngestionVersion("4"))
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, cleanup(context.Background()))
		atrace.TracerProvider, atrace.Tracer = oldProvider, oldTracer
		server.Close()
	})
	return recording
}

type childSpanRunner struct{}

func (*childSpanRunner) Run(ctx context.Context, _, _ string, _ model.Message, options ...agent.RunOption) (<-chan *event.Event, error) {
	opts := agent.NewRunOptions(options...)
	ctx, span := atrace.Tracer.Start(ctx, "agent", oteltrace.WithAttributes(opts.SpanAttributes...))
	for _, cb := range opts.TraceStartedCallbacks {
		cb(span.SpanContext())
	}
	_, child := atrace.Tracer.Start(ctx, "child-tool")
	child.End()
	span.End()
	events := make(chan *event.Event, 1)
	events <- makeFinalEvent("hello")
	close(events)
	return events, nil
}
func (*childSpanRunner) Close() error { return nil }

func TestV4ExportsExperimentAssociationOnRootAndChildren(t *testing.T) {
	recording := startV4Telemetry(t)
	ctx := context.Background()
	evaluator, err := coreevaluation.New("demo-app", &childSpanRunner{})
	require.NoError(t, err)
	defer evaluator.Close()
	api := &recordedAPIServer{}
	server := httptest.NewServer(api)
	defer server.Close()
	h, err := New("demo-app", evaluator, newTestEvalSetManager(t), newTestMetricManager(t), newTestEvalResultManager(t),
		WithBaseURL(server.URL), WithPublicKey("pk"), WithSecretKey("sk"), WithAPIVersion(APIVersionV4))
	require.NoError(t, err)
	spec := buildTestCaseSpec("one")
	spec.EvalCase.SessionInput = &evalset.SessionInput{AppName: "demo-app", UserID: "demo-user"}
	spec.TraceInput = "question"
	spec.TraceMetadata = map[string]any{"datasetItemMetadata": map[string]any{"category": "billing"}}
	require.NoError(t, h.syncEvalSet(ctx, "set", []*CaseSpec{spec}))
	response, err := h.executeRemoteExperiment(ctx, &remoteExperimentRequest{ProjectID: "project", DatasetID: "set"}, executionOptions{runName: "run", runDescription: "nightly"}, []*CaseSpec{spec})
	require.NoError(t, err)
	recording.mu.Lock()
	defer recording.mu.Unlock()
	require.Len(t, recording.spans, 3)
	var rootID string
	for _, span := range recording.spans {
		if span.Name == "experiment-item-task" {
			rootID = hex.EncodeToString(span.SpanId)
			assert.Empty(t, span.ParentSpanId)
		}
	}
	require.NotEmpty(t, rootID)
	for _, span := range recording.spans {
		attrs := map[string]string{}
		for _, attr := range span.Attributes {
			attrs[attr.Key] = attr.Value.GetStringValue()
		}
		assert.Equal(t, response.DatasetRunID, attrs[attrExperimentID], span.Name)
		assert.Equal(t, rootID, attrs[attrExperimentRootID], span.Name)
		assert.Equal(t, spec.DatasetItemID, attrs[attrExperimentItemID], span.Name)
		assert.JSONEq(t, `{"category":"billing"}`, attrs[attrExperimentItemMetadata], span.Name)
		assert.Equal(t, response.Cases[0].TraceID, hex.EncodeToString(span.TraceId), span.Name)
		if span.Name == "experiment-item-task" {
			assert.Equal(t, "hello", attrs["langfuse.observation.output"])
			assert.Equal(t, "nightly", attrs[attrExperimentDesc])
		} else {
			assert.Empty(t, attrs[attrExperimentDesc], span.Name)
		}
	}
	require.NotEmpty(t, recording.headers)
	for _, header := range recording.headers {
		assert.Equal(t, "4", header.Get("x-langfuse-ingestion-version"))
		assert.Equal(t, "Basic cGs6c2s=", header.Get("Authorization"))
	}
}

func TestV4FailsWithoutRecordingTelemetry(t *testing.T) {
	oldTracer := atrace.Tracer
	atrace.Tracer = noop.NewTracerProvider().Tracer("test")
	defer func() { atrace.Tracer = oldTracer }()
	h := &Handler{}
	_, _, err := h.startExperimentItem(context.Background(), &CaseSpec{}, nil, "")
	require.ErrorContains(t, err, "recording OpenTelemetry exporter")
}

func TestV4DoesNotPublishScoresWithoutAnExperimentItemSpan(t *testing.T) {
	oldTracer := atrace.Tracer
	atrace.Tracer = noop.NewTracerProvider().Tracer("test")
	defer func() { atrace.Tracer = oldTracer }()
	ctx := context.Background()
	evaluator, err := coreevaluation.New("demo-app", &childSpanRunner{})
	require.NoError(t, err)
	defer evaluator.Close()
	api := &recordedAPIServer{}
	server := httptest.NewServer(api)
	defer server.Close()
	h, err := New("demo-app", evaluator, newTestEvalSetManager(t), newTestMetricManager(t), newTestEvalResultManager(t),
		WithBaseURL(server.URL), WithPublicKey("pk"), WithSecretKey("sk"), WithAPIVersion(APIVersionV4))
	require.NoError(t, err)
	spec := buildTestCaseSpec("one")
	spec.EvalCase.SessionInput = &evalset.SessionInput{AppName: "demo-app", UserID: "demo-user"}
	require.NoError(t, h.syncEvalSet(ctx, "set", []*CaseSpec{spec}))
	_, err = h.executeRemoteExperiment(ctx, &remoteExperimentRequest{ProjectID: "project", DatasetID: "set"}, executionOptions{runName: "run"}, []*CaseSpec{spec})
	require.Error(t, err)
	api.mu.Lock()
	defer api.mu.Unlock()
	assert.Empty(t, api.scoreRequests)
}

type experimentModel struct{}

func (*experimentModel) Info() model.Info { return model.Info{Name: "scripted-local"} }
func (*experimentModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	responses := make(chan *model.Response, 1)
	responses <- &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "hello"}}}}
	close(responses)
	return responses, nil
}

func TestV4NativeRunnerKeepsPublishedTraceAndItemAssociation(t *testing.T) {
	recording := startV4Telemetry(t)
	ctx := context.Background()
	actual := runner.NewRunner("demo-app", llmagent.New("assistant", llmagent.WithModel(&experimentModel{})))
	evaluator, err := coreevaluation.New("demo-app", actual,
		coreevaluation.WithNumRuns(2), coreevaluation.WithNumRunsParallelEnabled(true),
		coreevaluation.WithEvalCaseParallelism(2), coreevaluation.WithEvalCaseParallelInferenceEnabled(true))
	require.NoError(t, err)
	defer evaluator.Close()
	api := &recordedAPIServer{}
	server := httptest.NewServer(api)
	defer server.Close()
	h, err := New("demo-app", evaluator, newTestEvalSetManager(t), newTestMetricManager(t), newTestEvalResultManager(t),
		WithBaseURL(server.URL), WithPublicKey("pk"), WithSecretKey("sk"), WithAPIVersion(APIVersionV4), WithEnvironment("ci"))
	require.NoError(t, err)
	specs := []*CaseSpec{buildTestCaseSpec("one"), buildTestCaseSpec("two")}
	for _, spec := range specs {
		spec.EvalCase.SessionInput = &evalset.SessionInput{AppName: "demo-app", UserID: "demo-user"}
		spec.TraceName = "trace-" + spec.EvalCase.EvalID
		spec.UserID = "demo-user"
		spec.TraceMetadata = map[string]any{"region": "ro"}
	}
	require.NoError(t, h.syncEvalSet(ctx, "set", specs))
	response, err := h.executeRemoteExperiment(ctx, &remoteExperimentRequest{ProjectID: "project", DatasetID: "set"}, executionOptions{runName: "run", traceTags: []string{"tag-a", "x,y"}}, specs)
	require.NoError(t, err)
	recording.mu.Lock()
	defer recording.mu.Unlock()
	roots := map[string]string{}
	traceIDs := map[string]bool{}
	var children int
	for _, span := range recording.spans {
		id := hex.EncodeToString(span.TraceId)
		traceIDs[id] = true
		if span.Name == "experiment-item-task" {
			roots[id] = hex.EncodeToString(span.SpanId)
		}
	}
	require.Len(t, roots, 4, "two cases with two parallel inference runs")
	for _, span := range recording.spans {
		attrs := map[string]string{}
		for _, attr := range span.Attributes {
			attrs[attr.Key] = attr.Value.GetStringValue()
		}
		id := hex.EncodeToString(span.TraceId)
		assert.Equal(t, response.DatasetRunID, attrs[attrExperimentID], span.Name)
		assert.Equal(t, roots[id], attrs[attrExperimentRootID], span.Name)
		assert.Equal(t, "demo-user", attrs["langfuse.user.id"], span.Name)
		assert.Equal(t, "ci", attrs["langfuse.environment"], span.Name)
		assert.Equal(t, "ro", attrs[attrTraceMetadataPrefix+"region"], span.Name)
		assert.NotEmpty(t, attrs[attrSessionID], span.Name)
		assert.True(t, strings.HasPrefix(attrs["langfuse.trace.name"], "trace-"), span.Name)
		for _, attr := range span.Attributes {
			if attr.Key == attrTraceTags {
				values := attr.Value.GetArrayValue().GetValues()
				require.Len(t, values, 2, span.Name)
				assert.Equal(t, "tag-a", values[0].GetStringValue())
				assert.Equal(t, "x,y", values[1].GetStringValue())
			}
		}
		if span.Name != "experiment-item-task" {
			children++
		}
	}
	assert.GreaterOrEqual(t, children, 8, "native agent and model spans are exported for each run")
	for _, result := range response.Cases {
		assert.True(t, traceIDs[result.TraceID], "published trace is among exported spans")
	}
}
