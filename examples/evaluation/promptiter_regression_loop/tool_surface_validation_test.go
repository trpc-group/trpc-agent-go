// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	criteriontext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/internal/regression"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	toolSurfaceValidationApp = "promptiter-tool-surface-validation"
	toolSurfaceTrainSet      = "tool-train"
	toolSurfaceValidationSet = "tool-validation"
	toolSurfaceName          = "lookup_flight"
	baselineToolDescription  = "Look up a traveler loyalty-profile record."
	candidateToolDescription = "Look up live flight status by flight number."
	toolSurfaceExpected      = "Use lookup_flight to retrieve live flight status."
	toolSurfaceFallback      = "No matching operational tool is available."
)

func TestToolDescriptionSurfaceEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	evalSetManager := evalsetinmemory.New()
	metricManager := metricinmemory.New()
	addToolSurfaceEvalSet(t, ctx, evalSetManager, metricManager, toolSurfaceTrainSet, "train-tool-case")
	addToolSurfaceEvalSet(t, ctx, evalSetManager, metricManager, toolSurfaceValidationSet, "validation-tool-case")

	lookupTool := function.NewFunctionTool(
		lookupFlight,
		function.WithName(toolSurfaceName),
		function.WithDescription(baselineToolDescription),
	)
	candidate := llmagent.New(
		candidateAgentName,
		llmagent.WithModel(&toolSurfaceModel{}),
		llmagent.WithInstruction("Answer travel operations questions from the available tools."),
		llmagent.WithTools([]tool.Tool{lookupTool}),
	)
	candidateRunner := runner.NewRunner(toolSurfaceValidationApp, candidate)
	agentEvaluator, err := evaluation.New(
		toolSurfaceValidationApp,
		candidateRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalresultinmemory.New()),
		evaluation.WithNumRuns(1),
	)
	if err != nil {
		_ = candidateRunner.Close()
		t.Fatalf("evaluation.New() error = %v", err)
	}
	defer func() {
		if err := errors.Join(agentEvaluator.Close(), candidateRunner.Close()); err != nil {
			t.Errorf("close tool-surface validation runtime: %v", err)
		}
	}()

	targetSurfaceID := astructure.SurfaceID(
		candidateAgentName,
		astructure.SurfaceTypeTool,
		toolSurfaceName,
	)
	engine, err := promptiterengine.New(
		ctx,
		promptiterengine.WithAgent(candidate),
		promptiterengine.WithAgentEvaluator(agentEvaluator),
		promptiterengine.WithBackwarder(&deterministicBackwarder{}),
		promptiterengine.WithAggregator(&deterministicAggregator{}),
		promptiterengine.WithOptimizer(&toolSurfaceOptimizer{description: candidateToolDescription}),
	)
	if err != nil {
		t.Fatalf("promptiterengine.New() error = %v", err)
	}
	request := &promptiterengine.RunRequest{
		Train:             []promptiterengine.EvalSetInput{{EvalSetID: toolSurfaceTrainSet}},
		Validation:        []promptiterengine.EvalSetInput{{EvalSetID: toolSurfaceValidationSet}},
		EvaluationOptions: promptiterengine.EvaluationOptions{EvalCaseParallelism: 1},
		AcceptancePolicy:  promptiterengine.AcceptancePolicy{MinScoreGain: 0.5},
		MaxRounds:         1,
		TargetSurfaceIDs:  []string{targetSurfaceID},
	}
	result, err := engine.Run(ctx, request)
	if err != nil {
		t.Fatalf("Engine.Run() error = %v", err)
	}
	round, err := requireSingleRound(result, 1)
	if err != nil {
		t.Fatalf("requireSingleRound() error = %v", err)
	}
	if !round.Acceptance.Accepted {
		t.Fatalf("PromptIter acceptance = %+v, want accepted", round.Acceptance)
	}
	candidatePrompt, err := promptFromProfile(result.AcceptedProfile, targetSurfaceID)
	if err != nil {
		t.Fatalf("promptFromProfile() error = %v", err)
	}
	if candidatePrompt.Text != candidateToolDescription {
		t.Fatalf("accepted tool description = %q, want %q", candidatePrompt.Text, candidateToolDescription)
	}

	baselineTrain := normalizeToolSurfaceEvaluation(t, round.Train)
	baselineValidation := normalizeToolSurfaceEvaluation(t, result.BaselineValidation)
	candidateValidation := normalizeToolSurfaceEvaluation(t, round.Validation)
	candidateRun, err := engine.Run(ctx, &promptiterengine.RunRequest{
		Train:             request.Train,
		Validation:        request.Validation,
		InitialProfile:    result.AcceptedProfile,
		EvaluationOptions: request.EvaluationOptions,
		AcceptancePolicy:  promptiterengine.AcceptancePolicy{MinScoreGain: 0.5},
		MaxRounds:         1,
		TargetSurfaceIDs:  request.TargetSurfaceIDs,
	})
	if err != nil {
		t.Fatalf("evaluate accepted tool profile: %v", err)
	}
	if len(candidateRun.Rounds) != 1 || candidateRun.Rounds[0].Train == nil {
		t.Fatal("accepted tool profile has no train evaluation")
	}
	candidateTrain := normalizeToolSurfaceEvaluation(t, candidateRun.Rounds[0].Train)

	delta, err := regression.Compare(baselineValidation, candidateValidation)
	if err != nil {
		t.Fatalf("regression.Compare() error = %v", err)
	}
	if delta.Counts[regression.DeltaNewPass] != 1 {
		t.Fatalf("delta counts = %+v, want one new pass", delta.Counts)
	}
	policy := regression.GatePolicy{
		MinValidationScoreGain:    0.5,
		RejectNewFailures:         true,
		RejectCriticalRegressions: true,
		CriticalCaseIDs:           []string{"validation-tool-case"},
	}
	decision, err := regression.Decide(policy, regression.GateInput{
		OriginalBaseline: baselineValidation,
		AcceptedBaseline: baselineValidation,
		Candidate:        candidateValidation,
	})
	if err != nil {
		t.Fatalf("regression.Decide() error = %v", err)
	}
	if !decision.Accepted {
		t.Fatalf("release gate = %+v, want accepted", decision)
	}

	catalog := regression.AttributionCatalog{MetricKinds: map[string]regression.MetricKind{
		"final_response_avg_score": regression.MetricFinalResponse,
	}}
	baselineAttribution := regression.MergeAttributions(
		regression.AttributeFailures(baselineTrain, catalog),
		regression.AttributeFailures(baselineValidation, catalog),
	)
	if baselineAttribution.Summary.TotalFailures != 2 {
		t.Fatalf("baseline failures = %d, want 2", baselineAttribution.Summary.TotalFailures)
	}
	report, err := regression.NewReport(regression.RunMetadata{
		ID:        "tool-surface-validation",
		Status:    "running",
		Mode:      "deterministic-tool-surface",
		Seed:      2003,
		Model:     "deterministic-tool-surface-model",
		StartedAt: time.Unix(0, 0).UTC(),
	}, baselineTrain, baselineValidation, baselineAttribution)
	if err != nil {
		t.Fatalf("regression.NewReport() error = %v", err)
	}
	patches, err := patchRecords(round.Patches)
	if err != nil {
		t.Fatalf("patchRecords() error = %v", err)
	}
	if len(patches) != 1 || patches[0].Text != candidateToolDescription {
		t.Fatalf("tool patch audit = %+v, want candidate description", patches)
	}
	if err := regression.AppendRound(report, regression.RoundReport{
		Attempt:            1,
		InputPrompt:        regression.PromptRecord{SurfaceID: targetSurfaceID, Text: baselineToolDescription},
		CandidatePrompt:    candidatePrompt,
		PromptIterAccepted: true,
		Train:              candidateTrain,
		Validation:         candidateValidation,
		Delta:              delta,
		BaselineDelta:      delta,
		Attribution: regression.MergeAttributions(
			regression.AttributeFailures(candidateTrain, catalog),
			regression.AttributeFailures(candidateValidation, catalog),
		),
		Gate:    *decision,
		Patches: patches,
		Usage:   regression.AddUsage(candidateTrain.Usage, candidateValidation.Usage),
	}); err != nil {
		t.Fatalf("regression.AppendRound() error = %v", err)
	}
	if err := regression.FinalizeReport(report, nil); err != nil {
		t.Fatalf("regression.FinalizeReport() error = %v", err)
	}
	var markdown bytes.Buffer
	if err := regression.WriteMarkdown(&markdown, report); err != nil {
		t.Fatalf("regression.WriteMarkdown() error = %v", err)
	}
	if !bytes.Contains(markdown.Bytes(), []byte(targetSurfaceID)) ||
		!bytes.Contains(markdown.Bytes(), []byte(candidateToolDescription)) {
		t.Fatalf("tool surface is missing from audit report:\n%s", markdown.String())
	}
}

type toolSurfaceModel struct{}

func (m *toolSurfaceModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, errors.New("model request is nil")
	}
	description := ""
	for _, candidateTool := range request.Tools {
		if candidateTool == nil || candidateTool.Declaration() == nil ||
			candidateTool.Declaration().Name != toolSurfaceName {
			continue
		}
		description = candidateTool.Declaration().Description
		break
	}
	if description == "" {
		return nil, errors.New("lookup_flight tool declaration is missing")
	}
	content := toolSurfaceFallback
	if description == candidateToolDescription {
		content = toolSurfaceExpected
	}
	responses := make(chan *model.Response, 1)
	responses <- &model.Response{
		ID:      "tool-surface-response",
		Object:  model.ObjectTypeChatCompletion,
		Model:   "deterministic-tool-surface-model",
		Done:    true,
		Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage(content)}},
		Usage:   &model.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}
	close(responses)
	return responses, nil
}

func (m *toolSurfaceModel) Info() model.Info {
	return model.Info{Name: "deterministic-tool-surface-model"}
}

type toolSurfaceOptimizer struct {
	description string
}

func (o *toolSurfaceOptimizer) Optimize(
	ctx context.Context,
	request *optimizer.Request,
) (*optimizer.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil || request.Surface == nil || request.Gradient == nil {
		return nil, errors.New("tool optimizer request is incomplete")
	}
	if request.Surface.Type != astructure.SurfaceTypeTool || len(request.Surface.Value.Tools) != 1 {
		return nil, fmt.Errorf("surface %q is not one tool declaration", request.Surface.SurfaceID)
	}
	tools := append([]astructure.ToolRef(nil), request.Surface.Value.Tools...)
	tools[0].Description = o.description
	return &optimizer.Result{Patch: &promptiter.SurfacePatch{
		SurfaceID: request.Surface.SurfaceID,
		Value:     astructure.SurfaceValue{Tools: tools},
		Reason:    "clarify live flight status lookup",
	}}, nil
}

type toolSurfaceArgs struct {
	FlightNumber string `json:"flightNumber" jsonschema:"description=Flight number,required"`
}

func lookupFlight(_ context.Context, args toolSurfaceArgs) (string, error) {
	return "status for " + args.FlightNumber, nil
}

func addToolSurfaceEvalSet(
	t *testing.T,
	ctx context.Context,
	evalSetManager evalset.Manager,
	metricManager metric.Manager,
	evalSetID string,
	caseID string,
) {
	t.Helper()
	if _, err := evalSetManager.Create(ctx, toolSurfaceValidationApp, evalSetID); err != nil {
		t.Fatalf("create eval set %q: %v", evalSetID, err)
	}
	if err := evalSetManager.AddCase(ctx, toolSurfaceValidationApp, evalSetID, &evalset.EvalCase{
		EvalID: caseID,
		Conversation: []*evalset.Invocation{{
			InvocationID:  caseID + "-1",
			UserContent:   &model.Message{Role: model.RoleUser, Content: "Check the current status of flight TR123."},
			FinalResponse: &model.Message{Role: model.RoleAssistant, Content: toolSurfaceExpected},
		}},
		SessionInput: &evalset.SessionInput{
			AppName: toolSurfaceValidationApp,
			UserID:  "tool-surface-user",
		},
	}); err != nil {
		t.Fatalf("add eval case %q: %v", caseID, err)
	}
	if err := metricManager.Add(ctx, toolSurfaceValidationApp, evalSetID, &metric.EvalMetric{
		MetricName: "final_response_avg_score",
		Threshold:  1,
		Criterion: &criterion.Criterion{FinalResponse: &finalresponse.FinalResponseCriterion{
			Text: &criteriontext.TextCriterion{MatchStrategy: criteriontext.TextMatchStrategyExact},
		}},
	}); err != nil {
		t.Fatalf("add metric for %q: %v", evalSetID, err)
	}
}

func normalizeToolSurfaceEvaluation(
	t *testing.T,
	result *promptiterengine.EvaluationResult,
) *regression.EvaluationResult {
	t.Helper()
	normalized, err := regression.NormalizeEngineEvaluation(result)
	if err != nil {
		t.Fatalf("NormalizeEngineEvaluation() error = %v", err)
	}
	return normalized
}
