//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalstatus "trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
)

const (
	regressionNodeID = "regression-writer"
	qualityMetric    = "quality"
)

var directiveInstructions = []struct {
	name        string
	instruction string
}{
	{"ROUTE_EXPLICITLY", "select the route that matches the user's intent"},
	{"VALIDATE_TOOL_ARGUMENTS", "verify required arguments and types before every tool call"},
	{"OUTPUT_JSON_WHEN_REQUESTED", "emit valid JSON with no surrounding prose when JSON is requested"},
	{"GROUND_IN_PROVIDED_CONTEXT", "never invent facts that are absent from the supplied context"},
	{"PRESERVE_SAFETY_CONSTRAINTS", "refuse unsafe requests and never reveal credentials or secrets"},
	{"REPORT_ENVIRONMENT_FAILURES", "distinguish timeouts and unavailable dependencies from model errors"},
}

type promptIterAudit struct {
	SurfaceID       string            `json:"surfaceId"`
	BaselinePrompt  string            `json:"baselinePrompt"`
	CandidatePrompt string            `json:"candidatePrompt"`
	Rounds          []promptIterRound `json:"rounds"`
}

type promptIterRound struct {
	Round             int     `json:"round"`
	CandidatePrompt   string  `json:"candidatePrompt"`
	TrainScore        float64 `json:"trainScore"`
	OptimizationScore float64 `json:"optimizationScore"`
	Accepted          bool    `json:"accepted"`
	ScoreDelta        float64 `json:"scoreDelta"`
	Reason            string  `json:"reason"`
}

func runDeterministicPromptIter(
	ctx context.Context,
	cfg *loadedConfig,
) (string, promptIterAudit, error) {
	baseline := strings.TrimSpace(cfg.Prompt)
	if baseline == "" {
		return "", promptIterAudit{}, errors.New("baseline prompt is empty")
	}
	surfaceID := astructure.SurfaceID(regressionNodeID, astructure.SurfaceTypeInstruction)
	if cfg.PromptIter.Target != surfaceID {
		return "", promptIterAudit{}, fmt.Errorf("PromptIter target %q does not match exported surface %q", cfg.PromptIter.Target, surfaceID)
	}
	structure := &astructure.Snapshot{
		StructureID: "promptiter-regression-loop-v1",
		EntryNodeID: regressionNodeID,
		Nodes: []astructure.Node{{
			NodeID: regressionNodeID,
			Kind:   astructure.NodeKindLLM,
			Name:   "regression-writer",
		}},
		Surfaces: []astructure.Surface{{
			SurfaceID: surfaceID,
			NodeID:    regressionNodeID,
			Type:      astructure.SurfaceTypeInstruction,
			Value:     astructure.SurfaceValue{Text: stringPointer(baseline)},
		}},
	}
	optimizerStage := newDeterministicOptimizer(baseline)
	evaluator := &deterministicAgentEvaluator{
		surfaceID:     surfaceID,
		evalSetID:     cfg.Train.EvalSetID,
		evalSet:       cfg.Train,
		currentPrompt: optimizerStage.currentPrompt,
	}
	engine, err := promptiterengine.New(
		ctx,
		promptiterengine.WithStructure(structure),
		promptiterengine.WithAgentEvaluator(evaluator),
		promptiterengine.WithBackwarder(&deterministicBackwarder{}),
		promptiterengine.WithAggregator(&deterministicAggregator{}),
		promptiterengine.WithOptimizer(optimizerStage),
	)
	if err != nil {
		return "", promptIterAudit{}, fmt.Errorf("create PromptIter engine: %w", err)
	}
	defer evaluator.Close()
	result, err := engine.Run(ctx, &promptiterengine.RunRequest{
		Train:            []promptiterengine.EvalSetInput{{EvalSetID: cfg.Train.EvalSetID}},
		Validation:       []promptiterengine.EvalSetInput{{EvalSetID: cfg.Train.EvalSetID}},
		AcceptancePolicy: promptiterengine.AcceptancePolicy{MinScoreGain: cfg.PromptIter.MinScoreGain},
		StopPolicy:       promptiterengine.StopPolicy{MaxRoundsWithoutAcceptance: 1},
		MaxRounds:        cfg.PromptIter.MaxRounds,
		TargetSurfaceIDs: []string{surfaceID},
	})
	if err != nil {
		return "", promptIterAudit{}, fmt.Errorf("run PromptIter engine: %w", err)
	}
	audit := promptIterAudit{
		SurfaceID:      surfaceID,
		BaselinePrompt: baseline,
	}
	for _, round := range result.Rounds {
		roundAudit := promptIterRound{Round: round.Round}
		roundAudit.CandidatePrompt = profileInstruction(round.OutputProfile, surfaceID)
		if round.Train != nil {
			roundAudit.TrainScore = round.Train.OverallScore
		}
		if round.Validation != nil {
			roundAudit.OptimizationScore = round.Validation.OverallScore
		}
		if round.Acceptance != nil {
			roundAudit.Accepted = round.Acceptance.Accepted
			roundAudit.ScoreDelta = round.Acceptance.ScoreDelta
			roundAudit.Reason = round.Acceptance.Reason
		}
		audit.Rounds = append(audit.Rounds, roundAudit)
	}
	candidate := profileInstruction(result.AcceptedProfile, surfaceID)
	if candidate == "" && len(result.Rounds) > 0 {
		candidate = profileInstruction(result.Rounds[len(result.Rounds)-1].OutputProfile, surfaceID)
	}
	if candidate == "" {
		return "", audit, errors.New("PromptIter returned no candidate instruction")
	}
	audit.CandidatePrompt = candidate
	return candidate, audit, nil
}

func profileInstruction(profile *promptiter.Profile, surfaceID string) string {
	if profile == nil {
		return ""
	}
	for _, override := range profile.Overrides {
		if override.SurfaceID == surfaceID && override.Value.Text != nil {
			return strings.TrimSpace(*override.Value.Text)
		}
	}
	return ""
}

type deterministicAgentEvaluator struct {
	surfaceID     string
	evalSetID     string
	evalSet       evalSetFile
	currentPrompt func() string
}

func (e *deterministicAgentEvaluator) Evaluate(
	_ context.Context,
	evalSetID string,
	_ ...evaluation.Option,
) (*evaluation.EvaluationResult, error) {
	if evalSetID != e.evalSetID {
		return nil, fmt.Errorf("unexpected eval set %q", evalSetID)
	}
	set := e.evalSet
	prompt := e.currentPrompt()
	overallStatus := evalstatus.EvalStatusPassed
	evalCases := make([]*evaluation.EvaluationCaseResult, 0, len(set.EvalCases))
	for _, spec := range set.EvalCases {
		output := fakeResponse(prompt, spec)
		score, passed := scoreOutput(spec, output)
		if spec.HardFailure && containsSensitiveDisclosure(output) {
			score = 0
			passed = false
		}
		metricStatus := evalstatus.EvalStatusPassed
		reason := fmt.Sprintf("required directive %s satisfied", spec.RequiredDirective)
		if !passed {
			metricStatus = evalstatus.EvalStatusFailed
			overallStatus = evalstatus.EvalStatusFailed
			reason = fmt.Sprintf("required directive %s missing; observed output: %s", spec.RequiredDirective, output)
		}
		metric := &evalresult.EvalMetricResult{
			MetricName: qualityMetric,
			Score:      score,
			EvalStatus: metricStatus,
			Details:    &evalresult.EvalMetricResultDetails{Reason: reason, Score: score},
		}
		invocationID := spec.EvalID + "-invocation"
		sessionID := spec.EvalID + "-session"
		trace := &atrace.Trace{
			RootAgentName:    regressionNodeID,
			RootInvocationID: invocationID,
			SessionID:        sessionID,
			StartedAt:        time.Unix(0, 0).UTC(),
			EndedAt:          time.Unix(0, 1).UTC(),
			Status:           atrace.TraceStatusCompleted,
			Steps: []atrace.Step{{
				StepID:            spec.EvalID + "-writer-step",
				InvocationID:      invocationID,
				AgentName:         regressionNodeID,
				NodeID:            regressionNodeID,
				NodeType:          string(astructure.NodeKindLLM),
				AppliedSurfaceIDs: []string{e.surfaceID},
				Input:             &atrace.Snapshot{Text: spec.Conversation[0].UserContent.Content},
				Output:            &atrace.Snapshot{Text: reason},
			}},
		}
		caseResult := &evalresult.EvalCaseResult{
			EvalSetID:                evalSetID,
			EvalID:                   spec.EvalID,
			RunID:                    1,
			FinalEvalStatus:          metricStatus,
			OverallEvalMetricResults: []*evalresult.EvalMetricResult{metric},
		}
		evalCases = append(evalCases, &evaluation.EvaluationCaseResult{
			EvalCaseID:      spec.EvalID,
			OverallStatus:   metricStatus,
			EvalCaseResults: []*evalresult.EvalCaseResult{caseResult},
			MetricResults:   []*evalresult.EvalMetricResult{metric},
			RunDetails: []*evaluation.EvaluationCaseRunDetails{{
				RunID: 1,
				Inference: &evaluation.EvaluationInferenceDetails{
					SessionID:       sessionID,
					UserID:          "promptiter-user",
					Status:          evalstatus.EvalStatusPassed,
					Inferences:      []*evalset.Invocation{{InvocationID: invocationID}},
					ExecutionTraces: []*atrace.Trace{trace},
				},
			}},
		})
	}
	return &evaluation.EvaluationResult{
		AppName:       "promptiter-regression-loop",
		EvalSetID:     evalSetID,
		OverallStatus: overallStatus,
		EvalCases:     evalCases,
	}, nil
}

func (e *deterministicAgentEvaluator) Close() error { return nil }

type deterministicBackwarder struct{}

func (deterministicBackwarder) Backward(
	_ context.Context,
	request *backwarder.Request,
) (*backwarder.Result, error) {
	if request == nil || len(request.AllowedGradientSurfaceIDs) == 0 {
		return nil, errors.New("backward request has no target surface")
	}
	gradient := "improve the prompt using the failed trace evidence"
	if request.Output != nil && strings.TrimSpace(request.Output.Text) != "" {
		gradient = request.Output.Text
	}
	return &backwarder.Result{Gradients: []promptiter.SurfaceGradient{{
		EvalSetID:  request.EvalSetID,
		EvalCaseID: request.EvalCaseID,
		StepID:     request.StepID,
		SurfaceID:  request.AllowedGradientSurfaceIDs[0],
		Severity:   promptiter.LossSeverityP1,
		Gradient:   gradient,
	}}}, nil
}

type deterministicAggregator struct{}

func (deterministicAggregator) Aggregate(
	_ context.Context,
	request *aggregator.Request,
) (*aggregator.Result, error) {
	if request == nil || len(request.Gradients) == 0 {
		return nil, errors.New("aggregation request has no gradients")
	}
	return &aggregator.Result{Gradient: &promptiter.AggregatedSurfaceGradient{
		SurfaceID: request.SurfaceID,
		NodeID:    request.NodeID,
		Type:      request.Type,
		Gradients: append([]promptiter.SurfaceGradient(nil), request.Gradients...),
	}}, nil
}

type deterministicOptimizer struct {
	mu      sync.RWMutex
	current string
}

func newDeterministicOptimizer(baseline string) *deterministicOptimizer {
	return &deterministicOptimizer{current: strings.TrimSpace(baseline)}
}

func (o *deterministicOptimizer) currentPrompt() string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.current
}

func (o *deterministicOptimizer) Optimize(
	_ context.Context,
	request *optimizer.Request,
) (*optimizer.Result, error) {
	if request == nil || request.Surface == nil || request.Gradient == nil {
		return nil, errors.New("optimizer request is incomplete")
	}
	baseline := ""
	if request.Surface.Value.Text != nil {
		baseline = strings.TrimSpace(*request.Surface.Value.Text)
	}
	candidate := candidateFromGradients(baseline, request.Gradient.Gradients)
	o.mu.Lock()
	o.current = candidate
	o.mu.Unlock()
	return &optimizer.Result{Patch: &promptiter.SurfacePatch{
		SurfaceID: request.Surface.SurfaceID,
		Value:     astructure.SurfaceValue{Text: stringPointer(candidate)},
		Reason:    "apply only remediation directives observed in training loss gradients",
	}}, nil
}

func candidateFromGradients(baseline string, gradients []promptiter.SurfaceGradient) string {
	var combined strings.Builder
	for _, gradient := range gradients {
		combined.WriteString(strings.ToUpper(gradient.Gradient))
		combined.WriteByte('\n')
	}
	evidence := combined.String()
	candidate := strings.TrimSpace(baseline)
	for _, directive := range directiveInstructions {
		if !strings.Contains(evidence, directive.name) || strings.Contains(candidate, directive.name) {
			continue
		}
		candidate += fmt.Sprintf("\n%d. %s: %s.", countAppliedDirectives(candidate)+1, directive.name, directive.instruction)
	}
	return strings.TrimSpace(candidate)
}

func countAppliedDirectives(prompt string) int {
	count := 0
	for _, directive := range directiveInstructions {
		if strings.Contains(prompt, directive.name) {
			count++
		}
	}
	return count
}

func stringPointer(value string) *string { return &value }
