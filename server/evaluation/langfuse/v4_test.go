//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package langfuse

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	coreevaluation "trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
)

// TestExperimentIDMatchesLangfuseDerivation pins the id to vectors computed with
// Langfuse's Python SDK (sha256 of the compact, non-ASCII-preserving JSON array,
// first 16 hex characters), including characters Go's encoder would escape.
func TestExperimentIDMatchesLangfuseDerivation(t *testing.T) {
	for _, tc := range []struct{ project, dataset, run, want string }{
		{"proj-1", "ds-1", "nightly-run", "8cc2bfb4cabd9c52"},
		{"p", "d", "run <a> & é 日本 x", "baf1657d0400a18e"},
		{"cm0abc", "cm0def", "", "6f7f881763098a69"},
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
	assert.Equal(t, "nightly", got[attrExperimentDesc].AsString())
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
	var runLevel, caseLevel int
	for _, score := range api.scoreRequests {
		if score.DatasetRunID != "" {
			runLevel++
			assert.Equal(t, wantID, score.DatasetRunID)
			assert.Empty(t, score.TraceID)
		} else {
			caseLevel++
			assert.NotEmpty(t, score.TraceID)
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
		assert.Equal(t, "desc", got[attrExperimentDesc])
		assert.Equal(t, "set", got[attrExperimentDataset])
		assert.Contains(t, []string{"one", "two"}, got[attrExperimentItemID])
		assert.Equal(t, call.session, got[attrSessionID])
		assert.Contains(t, got[attrTraceTags], "tag-a")
		// The trace attributes the runner already set are untouched.
		assert.Equal(t, "run/"+got[attrExperimentItemID], got["langfuse.trace.name"])
		assert.Equal(t, "ci", got["langfuse.environment"])
	}
}
