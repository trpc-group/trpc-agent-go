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
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	coreevaluation "trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	evalstatus "trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/log"
	serverevaluation "trpc.group/trpc-go/trpc-agent-go/server/evaluation"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

type executionOptions struct {
	runName        string
	runDescription string
	userID         string
	traceTags      []string
}

// Handler serves Langfuse remote experiment webhooks.
type Handler struct {
	path           string
	appName        string
	userIDSupplier UserIDSupplier
	traceTags      []string
	environment    string
	timeout        time.Duration
	client         *client
	agentEvaluator coreevaluation.AgentEvaluator
	caseBuilder    CaseBuilder
	evalSetManager evalset.Manager
	metricManager  metric.Manager
	resultManager  evalresult.Manager
	runOptions     []agent.RunOption
}

// New creates a Langfuse remote experiment handler.
func New(
	appName string,
	agentEvaluator coreevaluation.AgentEvaluator,
	evalSetManager evalset.Manager,
	metricManager metric.Manager,
	resultManager evalresult.Manager,
	opt ...Option,
) (*Handler, error) {
	opts := newOptions(opt...)
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return nil, errors.New("langfuse handler: app name must not be empty")
	}
	if agentEvaluator == nil {
		return nil, errors.New("langfuse handler: agent evaluator must not be nil")
	}
	if strings.TrimSpace(opts.publicKey) == "" {
		return nil, errors.New("langfuse handler: public key must not be empty")
	}
	if strings.TrimSpace(opts.secretKey) == "" {
		return nil, errors.New("langfuse handler: secret key must not be empty")
	}
	if opts.userIDSupplier == nil {
		return nil, errors.New("langfuse handler: user id supplier must not be nil")
	}
	if evalSetManager == nil {
		return nil, errors.New("langfuse handler: eval set manager must not be nil")
	}
	if metricManager == nil {
		return nil, errors.New("langfuse handler: metric manager must not be nil")
	}
	if resultManager == nil {
		return nil, errors.New("langfuse handler: eval result manager must not be nil")
	}
	if opts.httpClient == nil {
		return nil, errors.New("langfuse handler: http client must not be nil")
	}
	if opts.caseBuilder == nil {
		return nil, errors.New("langfuse handler: case builder must not be nil")
	}
	if strings.TrimSpace(opts.environment) == "" {
		return nil, errors.New("langfuse handler: environment must not be empty")
	}
	if opts.timeout < 0 {
		return nil, errors.New("langfuse handler: timeout must not be negative")
	}
	rawPath := strings.TrimSpace(opts.path)
	if rawPath == "" {
		return nil, errors.New("langfuse handler: path must not be empty")
	}
	routePath, err := url.JoinPath("/", rawPath)
	if err != nil {
		return nil, fmt.Errorf("langfuse handler: normalize route path: %w", err)
	}
	baseURL := strings.TrimSpace(opts.baseURL)
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("langfuse handler: parse base URL: %w", err)
	}
	if parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, fmt.Errorf("langfuse handler: base URL must include scheme and host, got %q", baseURL)
	}
	handler := &Handler{
		path:           routePath,
		appName:        appName,
		userIDSupplier: opts.userIDSupplier,
		traceTags:      append([]string(nil), opts.traceTags...),
		environment:    opts.environment,
		timeout:        opts.timeout,
		client:         newClient(baseURL, opts.publicKey, opts.secretKey, opts.httpClient),
		agentEvaluator: agentEvaluator,
		caseBuilder:    opts.caseBuilder,
		evalSetManager: evalSetManager,
		metricManager:  metricManager,
		resultManager:  resultManager,
		runOptions:     append([]agent.RunOption(nil), opts.runOptions...),
	}
	return handler, nil
}

// Path returns the configured route path for standalone mounting.
func (h *Handler) Path() string {
	return h.path
}

// RegisterRoutes mounts the handler under the evaluation server base path.
func (h *Handler) RegisterRoutes(mux *http.ServeMux, server *serverevaluation.Server) error {
	if mux == nil {
		return errors.New("langfuse handler: mux must not be nil")
	}
	if server == nil {
		return errors.New("langfuse handler: server must not be nil")
	}
	routePath, err := url.JoinPath(server.BasePath(), h.path)
	if err != nil {
		return fmt.Errorf("langfuse handler: join route path: %w", err)
	}
	mux.Handle(routePath, h)
	return nil
}

// ServeHTTP handles Langfuse remote experiment webhook requests.
func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeJSONError(writer, request, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	defer request.Body.Close()
	ctx, cancel := newExecutionContext(request.Context(), h.timeout)
	defer cancel()
	var remoteRequest remoteExperimentRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&remoteRequest); err != nil {
		writeJSONError(writer, request, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	if strings.TrimSpace(remoteRequest.DatasetName) == "" {
		writeJSONError(writer, request, http.StatusBadRequest, "datasetName is required")
		return
	}
	opts, err := h.resolveRemoteExperimentOptions(ctx, remoteRequest.Payload, remoteRequest.DatasetName)
	if err != nil {
		writeJSONError(writer, request, http.StatusBadRequest, fmt.Sprintf("resolve payload: %v", err))
		return
	}
	dataset, err := h.client.getDataset(ctx, remoteRequest.DatasetName)
	if err != nil {
		writeJSONError(writer, request, http.StatusBadGateway, fmt.Sprintf("fetch dataset: %v", err))
		return
	}
	if strings.TrimSpace(dataset.ID) != "" {
		remoteRequest.DatasetID = dataset.ID
	}
	caseSpecs, err := h.buildCaseSpecs(ctx, &remoteRequest, opts, dataset)
	if err != nil {
		writeJSONError(writer, request, http.StatusBadRequest, fmt.Sprintf("build case specs: %v", err))
		return
	}
	if strings.TrimSpace(remoteRequest.DatasetID) == "" {
		writeJSONError(writer, request, http.StatusBadGateway, "dataset id is required")
		return
	}
	if err := h.syncEvalSet(ctx, remoteRequest.DatasetID, caseSpecs); err != nil {
		writeJSONError(writer, request, http.StatusBadGateway, fmt.Sprintf("sync eval set: %v", err))
		return
	}
	result, err := h.executeRemoteExperiment(ctx, &remoteRequest, opts, caseSpecs)
	if err != nil {
		writeJSONError(writer, request, http.StatusBadGateway, fmt.Sprintf("execute remote experiment: %v", err))
		return
	}
	writeJSON(writer, request, http.StatusOK, result)
}

func (h *Handler) buildCaseSpecs(
	ctx context.Context,
	remoteRequest *remoteExperimentRequest,
	opts executionOptions,
	dataset *dataset,
) ([]*CaseSpec, error) {
	if dataset == nil {
		return nil, errors.New("dataset is nil")
	}
	if len(dataset.Items) == 0 {
		return nil, fmt.Errorf("dataset %s has no active items", dataset.Name)
	}
	specs := make([]*CaseSpec, 0, len(dataset.Items))
	for _, item := range dataset.Items {
		if item == nil {
			return nil, fmt.Errorf("dataset %s contains a nil item", dataset.Name)
		}
		spec, err := h.caseBuilder(ctx, item)
		if err != nil {
			return nil, fmt.Errorf("build case for dataset item %s: %w", item.ID, err)
		}
		spec, err = h.normalizeCaseSpec(remoteRequest, opts, item, spec)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func (h *Handler) normalizeCaseSpec(
	remoteRequest *remoteExperimentRequest,
	opts executionOptions,
	item *DatasetItem,
	spec *CaseSpec,
) (*CaseSpec, error) {
	if spec == nil {
		return nil, fmt.Errorf("dataset item %s produced a nil case spec", item.ID)
	}
	if spec.EvalCase == nil {
		return nil, fmt.Errorf("dataset item %s produced a nil eval case", item.ID)
	}
	if strings.TrimSpace(spec.DatasetItemID) == "" {
		spec.DatasetItemID = item.ID
	}
	if strings.TrimSpace(spec.EvalCase.EvalID) == "" {
		spec.EvalCase.EvalID = item.ID
	}
	if spec.EvalCase.SessionInput == nil {
		spec.EvalCase.SessionInput = &evalset.SessionInput{}
	}
	if strings.TrimSpace(spec.EvalCase.SessionInput.AppName) == "" {
		spec.EvalCase.SessionInput.AppName = h.appName
	}
	if strings.TrimSpace(spec.UserID) == "" {
		spec.UserID = opts.userID
	}
	if strings.TrimSpace(spec.EvalCase.SessionInput.UserID) == "" {
		spec.EvalCase.SessionInput.UserID = spec.UserID
	}
	if strings.TrimSpace(spec.TraceName) == "" {
		spec.TraceName = fmt.Sprintf("%s/%s", opts.runName, item.ID)
	}
	if strings.TrimSpace(spec.SessionID) == "" {
		spec.SessionID = fmt.Sprintf("%s-%s", opts.runName, item.ID)
		if strings.TrimSpace(spec.SessionID) == "" {
			spec.SessionID = item.ID
		}
	}
	traceMetadata := map[string]any{
		"source":              "langfuse-remote-experiment",
		"projectId":           remoteRequest.ProjectID,
		"datasetId":           remoteRequest.DatasetID,
		"datasetName":         remoteRequest.DatasetName,
		"datasetItemId":       item.ID,
		"appName":             h.appName,
		"runName":             opts.runName,
		"datasetItemMetadata": item.Metadata,
	}
	maps.Copy(traceMetadata, spec.TraceMetadata)
	spec.TraceMetadata = traceMetadata
	return spec, nil
}

func (h *Handler) executeRemoteExperiment(
	ctx context.Context,
	remoteRequest *remoteExperimentRequest,
	opts executionOptions,
	caseSpecs []*CaseSpec,
) (*remoteExperimentResponse, error) {
	response := &remoteExperimentResponse{
		DatasetID:        remoteRequest.DatasetID,
		DatasetName:      remoteRequest.DatasetName,
		RunName:          opts.runName,
		AggregateScores:  make(map[string]float64),
		AggregateReasons: make(map[string]string),
		Cases:            make([]remoteCaseSummary, 0, len(caseSpecs)),
	}
	metricBuckets := make(map[string][]float64)
	datasetRunID := ""
	passedCases := 0
	scoreCount := 0
	for _, spec := range caseSpecs {
		caseSummary, caseRunID, caseScoreCount, err := h.processCase(ctx, remoteRequest.DatasetID, opts, spec)
		if err != nil {
			return nil, err
		}
		if datasetRunID == "" {
			datasetRunID = caseRunID
		} else if caseRunID != "" && datasetRunID != caseRunID {
			return nil, fmt.Errorf("dataset run id mismatch: %s != %s", datasetRunID, caseRunID)
		}
		if caseSummary.Status == string(evalstatus.EvalStatusPassed) {
			passedCases++
		}
		for metricName, score := range caseSummary.MetricScores {
			metricBuckets[metricName] = append(metricBuckets[metricName], score)
		}
		response.Cases = append(response.Cases, *caseSummary)
		response.TraceCount++
		scoreCount += caseScoreCount
	}
	response.DatasetRunID = datasetRunID
	response.ProcessedCases = len(response.Cases)
	if datasetRunID != "" && len(response.Cases) > 0 {
		passRate := float64(passedCases) / float64(len(response.Cases))
		passRateReason := fmt.Sprintf("%d/%d cases passed.", passedCases, len(response.Cases))
		if err := h.client.createScore(ctx, scoreCreateRequest{
			Name:         "pass_rate",
			DatasetRunID: datasetRunID,
			Value:        passRate,
			DataType:     "NUMERIC",
			Environment:  h.environment,
			Comment:      passRateReason,
			Metadata: map[string]any{
				"runName": opts.runName,
				"appName": h.appName,
			},
		}); err != nil {
			return nil, fmt.Errorf("create run score pass_rate: %w", err)
		}
		response.AggregateScores["pass_rate"] = passRate
		response.AggregateReasons["pass_rate"] = passRateReason
		scoreCount++
		for metricName, scores := range metricBuckets {
			meanScore := averageFloat64(scores)
			runMetricName := metricName + "_mean"
			runMetricReason := fmt.Sprintf("Average %s across %d cases.", metricName, len(scores))
			if err := h.client.createScore(ctx, scoreCreateRequest{
				Name:         runMetricName,
				DatasetRunID: datasetRunID,
				Value:        meanScore,
				DataType:     "NUMERIC",
				Environment:  h.environment,
				Comment:      runMetricReason,
				Metadata: map[string]any{
					"metricName": metricName,
					"runName":    opts.runName,
					"appName":    h.appName,
				},
			}); err != nil {
				return nil, fmt.Errorf("create run score %s: %w", runMetricName, err)
			}
			response.AggregateScores[runMetricName] = meanScore
			response.AggregateReasons[runMetricName] = runMetricReason
			scoreCount++
		}
	}
	if len(response.AggregateReasons) == 0 {
		response.AggregateReasons = nil
	}
	response.ScoreCount = scoreCount
	return response, nil
}

func (h *Handler) processCase(
	ctx context.Context,
	datasetID string,
	opts executionOptions,
	spec *CaseSpec,
) (*remoteCaseSummary, string, int, error) {
	caseCtxWithTraceParent, traceID, err := injectRemoteTraceParent(ctx)
	if err != nil {
		return nil, "", 0, fmt.Errorf("inject remote trace parent for case %s: %w", spec.EvalCase.EvalID, err)
	}
	runOptions := append([]agent.RunOption(nil), h.runOptions...)
	runOptions = append(runOptions,
		agent.WithExecutionTraceEnabled(true),
		agent.WithSpanAttributes(
			attribute.String("langfuse.trace.name", spec.TraceName),
			attribute.String("langfuse.user.id", spec.UserID),
			attribute.String("langfuse.environment", h.environment),
		),
		agent.WithTraceStartedCallback(func(spanContext oteltrace.SpanContext) {
			if spanContext.IsValid() {
				traceID = spanContext.TraceID().String()
			}
		}),
	)
	evaluationResult, err := h.agentEvaluator.Evaluate(
		caseCtxWithTraceParent,
		datasetID,
		coreevaluation.WithEvalCaseIDs(spec.EvalCase.EvalID),
		coreevaluation.WithRunDetailsEnabled(true),
		coreevaluation.WithEvalSetManager(h.evalSetManager),
		coreevaluation.WithMetricManager(h.metricManager),
		coreevaluation.WithEvalResultManager(h.resultManager),
		coreevaluation.WithRunOptions(runOptions...),
	)
	if err != nil {
		return nil, "", 0, fmt.Errorf("evaluate case %s: %w", spec.EvalCase.EvalID, err)
	}
	caseAggregate, runCaseResult, inferenceDetail, err := h.resolveCaseArtifacts(evaluationResult, spec.EvalCase.EvalID)
	if err != nil {
		return nil, "", 0, fmt.Errorf("resolve case artifacts for case %s: %w", spec.EvalCase.EvalID, err)
	}
	if traceID == "" {
		return nil, "", 0, fmt.Errorf("trace id was not captured for case %s", spec.EvalCase.EvalID)
	}
	if err := forceFlushTelemetry(caseCtxWithTraceParent); err != nil {
		return nil, "", 0, fmt.Errorf("flush telemetry for case %s: %w", spec.EvalCase.EvalID, err)
	}
	finalOutput, err := extractFinalOutput(inferenceDetail)
	if err != nil {
		return nil, "", 0, fmt.Errorf("extract final output for case %s: %w", spec.EvalCase.EvalID, err)
	}
	if err := h.client.createTrace(caseCtxWithTraceParent, traceCreateRequest{
		ID:          traceID,
		Timestamp:   time.Now().UTC(),
		Name:        spec.TraceName,
		Input:       spec.TraceInput,
		Output:      finalOutput,
		SessionID:   resolveSessionID(inferenceDetail, spec),
		UserID:      resolveUserID(inferenceDetail, spec, opts),
		Environment: h.environment,
		Metadata:    spec.TraceMetadata,
		Tags:        opts.traceTags,
	}); err != nil {
		return nil, "", 0, fmt.Errorf("create trace for case %s: %w", spec.EvalCase.EvalID, err)
	}
	runItem, err := h.client.createDatasetRunItem(caseCtxWithTraceParent, datasetRunItemCreateRequest{
		RunName:        opts.runName,
		RunDescription: opts.runDescription,
		DatasetItemID:  spec.DatasetItemID,
		TraceID:        traceID,
		Metadata: map[string]any{
			"appName": h.appName,
			"runName": opts.runName,
		},
	})
	if err != nil {
		return nil, "", 0, fmt.Errorf("create dataset run item for case %s: %w", spec.EvalCase.EvalID, err)
	}
	summary := &remoteCaseSummary{
		CaseID:        spec.EvalCase.EvalID,
		DatasetItemID: spec.DatasetItemID,
		TraceID:       traceID,
		Status:        string(caseAggregate.OverallStatus),
		MetricScores:  make(map[string]float64),
		MetricReasons: make(map[string]string),
	}
	scoreCount := 0
	for _, metricResult := range runCaseResult.OverallEvalMetricResults {
		comment := resolveMetricReason(metricResult)
		if err := h.client.createScore(caseCtxWithTraceParent, scoreCreateRequest{
			Name:        metricResult.MetricName,
			TraceID:     traceID,
			Value:       metricResult.Score,
			DataType:    "NUMERIC",
			Environment: h.environment,
			Comment:     comment,
			Metadata: map[string]any{
				"datasetItemId": spec.DatasetItemID,
				"evalCaseId":    spec.EvalCase.EvalID,
				"runName":       opts.runName,
				"appName":       h.appName,
			},
		}); err != nil {
			return nil, "", scoreCount, fmt.Errorf("create score %s for case %s: %w", metricResult.MetricName, spec.EvalCase.EvalID, err)
		}
		summary.MetricScores[metricResult.MetricName] = metricResult.Score
		if comment != "" {
			summary.MetricReasons[metricResult.MetricName] = comment
		}
		scoreCount++
	}
	if len(summary.MetricReasons) == 0 {
		summary.MetricReasons = nil
	}
	return summary, runItem.DatasetRunID, scoreCount, nil
}

func (h *Handler) syncEvalSet(
	ctx context.Context,
	evalSetID string,
	caseSpecs []*CaseSpec,
) error {
	if err := h.evalSetManager.Delete(ctx, h.appName, evalSetID); err != nil {
		if _, getErr := h.evalSetManager.Get(ctx, h.appName, evalSetID); getErr == nil {
			return fmt.Errorf("delete eval set: %w", err)
		}
	}
	if _, err := h.evalSetManager.Create(ctx, h.appName, evalSetID); err != nil {
		return fmt.Errorf("create eval set: %w", err)
	}
	for _, spec := range caseSpecs {
		if spec == nil || spec.EvalCase == nil {
			continue
		}
		evalCaseID := strings.TrimSpace(spec.EvalCase.EvalID)
		if evalCaseID == "" {
			continue
		}
		if err := h.evalSetManager.AddCase(ctx, h.appName, evalSetID, spec.EvalCase); err != nil {
			return fmt.Errorf("add eval case %s: %w", evalCaseID, err)
		}
	}
	return nil
}

func (h *Handler) resolveCaseArtifacts(
	evaluationResult *coreevaluation.EvaluationResult,
	evalCaseID string,
) (*coreevaluation.EvaluationCaseResult, *evalresult.EvalCaseResult, *coreevaluation.EvaluationInferenceDetails, error) {
	caseAggregate, err := findEvaluationCaseResult(evaluationResult, evalCaseID)
	if err != nil {
		return nil, nil, nil, err
	}
	runCaseResult, err := findEvalCaseResult(evaluationResult, evalCaseID)
	if err != nil {
		return nil, nil, nil, err
	}
	inferenceDetail, err := findInferenceDetail(caseAggregate)
	if err != nil {
		return nil, nil, nil, err
	}
	return caseAggregate, runCaseResult, inferenceDetail, nil
}

func findEvaluationCaseResult(
	evaluationResult *coreevaluation.EvaluationResult,
	evalCaseID string,
) (*coreevaluation.EvaluationCaseResult, error) {
	if evaluationResult == nil {
		return nil, errors.New("evaluation result is nil")
	}
	for _, caseResult := range evaluationResult.EvalCases {
		if caseResult != nil && caseResult.EvalCaseID == evalCaseID {
			return caseResult, nil
		}
	}
	return nil, fmt.Errorf("eval case %s not found in evaluation result", evalCaseID)
}

func findEvalCaseResult(
	evaluationResult *coreevaluation.EvaluationResult,
	evalCaseID string,
) (*evalresult.EvalCaseResult, error) {
	if evaluationResult == nil {
		return nil, errors.New("evaluation result is nil")
	}
	if evaluationResult.EvalResult == nil {
		return nil, errors.New("evaluation result does not contain an eval set result")
	}
	for _, caseResult := range evaluationResult.EvalResult.EvalCaseResults {
		if caseResult != nil && caseResult.EvalID == evalCaseID {
			return caseResult, nil
		}
	}
	return nil, fmt.Errorf("eval case %s not found in evaluation result", evalCaseID)
}

func findInferenceDetail(caseResult *coreevaluation.EvaluationCaseResult) (*coreevaluation.EvaluationInferenceDetails, error) {
	if caseResult == nil {
		return nil, errors.New("evaluation case result is nil")
	}
	for _, runDetail := range caseResult.RunDetails {
		if runDetail != nil && runDetail.Inference != nil {
			return runDetail.Inference, nil
		}
	}
	return nil, fmt.Errorf("run details for eval case %s do not contain inference details", caseResult.EvalCaseID)
}

func (h *Handler) resolveRemoteExperimentOptions(
	ctx context.Context,
	rawPayload any,
	datasetName string,
) (executionOptions, error) {
	payloadOptions := RemoteExperimentOptions{
		RunName:   defaultRunName(datasetName),
		UserID:    strings.TrimSpace(h.userIDSupplier(ctx)),
		TraceTags: append([]string(nil), h.traceTags...),
	}
	if rawPayload == nil {
		return h.buildExecutionOptions(payloadOptions), nil
	}
	switch value := rawPayload.(type) {
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return h.buildExecutionOptions(payloadOptions), nil
		}
		var objectPayload map[string]any
		if json.Unmarshal([]byte(trimmed), &objectPayload) == nil {
			if err := applyPayloadOverrides(&payloadOptions, objectPayload); err != nil {
				return executionOptions{}, err
			}
		} else {
			payloadOptions.RunName = trimmed
		}
	default:
		if err := applyPayloadOverrides(&payloadOptions, value); err != nil {
			return executionOptions{}, err
		}
	}
	if strings.TrimSpace(payloadOptions.RunName) == "" {
		payloadOptions.RunName = defaultRunName(datasetName)
	}
	return h.buildExecutionOptions(payloadOptions), nil
}

func applyPayloadOverrides(opts *RemoteExperimentOptions, payload any) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	var overrides remoteExperimentPayload
	if err := json.Unmarshal(payloadBytes, &overrides); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}
	if overrides.RunName != nil && strings.TrimSpace(*overrides.RunName) != "" {
		opts.RunName = strings.TrimSpace(*overrides.RunName)
	}
	if overrides.RunDescription != nil {
		opts.RunDescription = strings.TrimSpace(*overrides.RunDescription)
	}
	if overrides.UserID != nil {
		opts.UserID = strings.TrimSpace(*overrides.UserID)
	}
	if overrides.TraceTags != nil {
		opts.TraceTags = append([]string(nil), (*overrides.TraceTags)...)
	}
	return nil
}

func (h *Handler) buildExecutionOptions(payloadOptions RemoteExperimentOptions) executionOptions {
	return executionOptions{
		runName:        payloadOptions.RunName,
		runDescription: payloadOptions.RunDescription,
		userID:         payloadOptions.UserID,
		traceTags:      append([]string(nil), payloadOptions.TraceTags...),
	}
}

func extractFinalOutput(inferenceDetail *coreevaluation.EvaluationInferenceDetails) (string, error) {
	if inferenceDetail == nil {
		return "", errors.New("inference detail is nil")
	}
	if len(inferenceDetail.Inferences) == 0 {
		return "", errors.New("inferences are empty")
	}
	lastInvocation := inferenceDetail.Inferences[len(inferenceDetail.Inferences)-1]
	if lastInvocation == nil || lastInvocation.FinalResponse == nil {
		return "", errors.New("final response is empty")
	}
	return lastInvocation.FinalResponse.Content, nil
}

func resolveSessionID(inferenceDetail *coreevaluation.EvaluationInferenceDetails, spec *CaseSpec) string {
	if spec != nil && strings.TrimSpace(spec.SessionID) != "" {
		return spec.SessionID
	}
	if inferenceDetail != nil && strings.TrimSpace(inferenceDetail.SessionID) != "" {
		return inferenceDetail.SessionID
	}
	return ""
}

func resolveUserID(inferenceDetail *coreevaluation.EvaluationInferenceDetails, spec *CaseSpec, opts executionOptions) string {
	if inferenceDetail != nil && strings.TrimSpace(inferenceDetail.UserID) != "" {
		return inferenceDetail.UserID
	}
	if spec != nil && strings.TrimSpace(spec.UserID) != "" {
		return spec.UserID
	}
	return opts.userID
}

func forceFlushTelemetry(ctx context.Context) error {
	provider, ok := atrace.TracerProvider.(*sdktrace.TracerProvider)
	if !ok {
		return nil
	}
	flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return provider.ForceFlush(flushCtx)
}

func resolveMetricReason(metricResult *evalresult.EvalMetricResult) string {
	if metricResult == nil {
		return ""
	}
	if metricResult.Details != nil {
		explicitReason := strings.TrimSpace(metricResult.Details.Reason)
		if explicitReason != "" {
			return explicitReason
		}
	}
	return fmt.Sprintf(
		"The metric %s ended with status %s and score %.2f against threshold %.2f.",
		metricResult.MetricName,
		metricResult.EvalStatus,
		metricResult.Score,
		metricResult.Threshold,
	)
}

func averageFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func injectRemoteTraceParent(ctx context.Context) (context.Context, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	traceID, err := newTraceID()
	if err != nil {
		return nil, "", err
	}
	spanID, err := newSpanID()
	if err != nil {
		return nil, "", err
	}
	spanContext := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: oteltrace.FlagsSampled,
		Remote:     true,
	})
	if !spanContext.IsValid() {
		return nil, "", errors.New("generated invalid span context")
	}
	return oteltrace.ContextWithRemoteSpanContext(ctx, spanContext), traceID.String(), nil
}

func newTraceID() (oteltrace.TraceID, error) {
	var traceID oteltrace.TraceID
	if _, err := cryptorand.Read(traceID[:]); err != nil {
		return oteltrace.TraceID{}, fmt.Errorf("generate trace id: %w", err)
	}
	if !traceID.IsValid() {
		return oteltrace.TraceID{}, errors.New("generate trace id: received invalid trace id")
	}
	return traceID, nil
}

func newSpanID() (oteltrace.SpanID, error) {
	var spanID oteltrace.SpanID
	if _, err := cryptorand.Read(spanID[:]); err != nil {
		return oteltrace.SpanID{}, fmt.Errorf("generate span id: %w", err)
	}
	if !spanID.IsValid() {
		return oteltrace.SpanID{}, errors.New("generate span id: received invalid span id")
	}
	return spanID, nil
}

func defaultRunName(datasetName string) string {
	trimmedName := strings.TrimSpace(datasetName)
	timestamp := time.Now().UTC().Format("20060102-150405")
	if trimmedName == "" {
		return timestamp
	}
	return fmt.Sprintf("%s-%s", trimmedName, timestamp)
}

func writeJSON(writer http.ResponseWriter, request *http.Request, statusCode int, value any) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(value); err != nil {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusInternalServerError)
		if _, writeErr := writer.Write([]byte("{\"message\":\"encode response\"}\n")); writeErr != nil {
			logResponseWriteError(request, fmt.Errorf("write fallback response body: %w", writeErr))
		}
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	if _, err := writer.Write(body.Bytes()); err != nil {
		logResponseWriteError(request, fmt.Errorf("write response body: %w", err))
	}
}

func writeJSONError(writer http.ResponseWriter, request *http.Request, statusCode int, message string) {
	writeJSON(writer, request, statusCode, map[string]string{"message": message})
}

func logResponseWriteError(request *http.Request, err error) {
	if request == nil {
		log.Errorf("langfuse handler: write response: %v", err)
		return
	}
	log.Errorf("langfuse handler: write response for %s %s: %v", request.Method, request.URL.RequestURI(), err)
}

func newExecutionContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if timeout == 0 || remaining < timeout {
			timeout = remaining
		}
	}
	if timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return context.WithCancel(ctx)
}
