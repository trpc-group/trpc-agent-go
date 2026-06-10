//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package bestofn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	evaluatorregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	metricregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service/local"
	evalstatus "trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

type evaluationSelector struct {
	metrics                 []*metric.EvalMetric
	selectionMode           SelectionMode
	contextMessages         []*model.Message
	evalSetManager          evalset.Manager
	judgeRunner             runner.Runner
	judgeRunnerNumSamples   *int
	registry                evaluatorregistry.Registry
	metricRegistry          metricregistry.Registry
	requirePassingCandidate bool
}

func newEvaluationSelector(o *options) runner.CandidateSelector {
	registry := o.registry
	if registry == nil {
		registry = evaluatorregistry.New()
	}
	metricRegistry := o.metricRegistry
	if metricRegistry == nil {
		metricRegistry = metricregistry.New()
	}
	selectionMode := o.selectionMode
	if selectionMode == "" {
		selectionMode = SelectionModePointwise
	}
	return &evaluationSelector{
		metrics:                 o.metrics,
		selectionMode:           selectionMode,
		contextMessages:         o.contextMessages,
		evalSetManager:          o.evalSetManager,
		judgeRunner:             o.judgeRunner,
		judgeRunnerNumSamples:   o.judgeRunnerNumSamples,
		registry:                registry,
		metricRegistry:          metricRegistry,
		requirePassingCandidate: o.requirePassingCandidate,
	}
}

func (v *evaluationSelector) Select(
	ctx context.Context,
	req *runner.CandidateSelectRequest,
) (int, error) {
	if req == nil {
		return 0, errors.New("bestofn: select request is nil")
	}
	if len(req.Attempts) == 0 {
		return 0, errors.New("bestofn: candidate attempts are empty")
	}
	evalSetManager := v.evalSetManager
	if evalSetManager == nil {
		evalSetManager = evalsetinmemory.New()
	}
	evalSetID := "bestofn-" + uuid.NewString()
	if _, err := evalSetManager.Create(ctx, req.AppName, evalSetID); err != nil {
		return 0, fmt.Errorf("create eval set: %w", err)
	}
	evalService, err := local.New(
		noOpRunner{},
		service.WithEvalSetManager(evalSetManager),
		service.WithRegistry(v.registry),
		service.WithMetricRegistry(v.metricRegistry),
	)
	if err != nil {
		return 0, fmt.Errorf("create evaluation service: %w", err)
	}
	defer evalService.Close()
	switch v.selectionMode {
	case SelectionModePointwise:
		return v.selectByPointwise(ctx, evalSetManager, evalService, evalSetID, req)
	case SelectionModePairwise:
		return v.selectByPairwise(ctx, evalSetManager, evalService, evalSetID, req)
	default:
		return 0, fmt.Errorf("bestofn: unsupported selection mode %q", v.selectionMode)
	}
}

func (v *evaluationSelector) selectByPointwise(
	ctx context.Context,
	evalSetManager evalset.Manager,
	evalService service.Service,
	evalSetID string,
	req *runner.CandidateSelectRequest,
) (int, error) {
	inferenceResults, attemptIndexes, err := v.buildInferenceResults(ctx, evalSetManager, evalSetID, req)
	if err != nil {
		return 0, err
	}
	runResult, err := evalService.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          req.AppName,
		EvalSetID:        evalSetID,
		InferenceResults: inferenceResults,
		EvaluateConfig: &service.EvaluateConfig{
			EvalMetrics: v.metricsWithJudgeRunner(),
		},
	})
	if err != nil {
		return 0, fmt.Errorf("evaluate candidates: %w", err)
	}
	winner, err := v.selectWinner(runResult.EvalCaseResults, attemptIndexes)
	if err != nil {
		return 0, err
	}
	return winner, nil
}

func (v *evaluationSelector) selectByPairwise(
	ctx context.Context,
	evalSetManager evalset.Manager,
	evalService service.Service,
	evalSetID string,
	req *runner.CandidateSelectRequest,
) (int, error) {
	candidates, err := v.buildCandidates(req)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 1 {
		if candidates[0].status != evalstatus.EvalStatusPassed {
			return 0, errors.New("bestofn: no passing candidate")
		}
		return candidates[0].attemptIndex, nil
	}
	pairScores := make([]*pairwiseCandidateScore, 0, len(candidates))
	for _, candidate := range candidates {
		pairScores = append(pairScores, &pairwiseCandidateScore{attemptIndex: candidate.attemptIndex})
	}
	inferenceResults, pairs, err := v.buildPairwiseInferenceResults(
		ctx,
		evalSetManager,
		evalSetID,
		req,
		candidates,
		pairScores,
	)
	if err != nil {
		return 0, err
	}
	if len(inferenceResults) != 0 {
		runResult, err := evalService.Evaluate(ctx, &service.EvaluateRequest{
			AppName:          req.AppName,
			EvalSetID:        evalSetID,
			InferenceResults: inferenceResults,
			EvaluateConfig: &service.EvaluateConfig{
				EvalMetrics: v.metricsWithJudgeRunner(),
			},
		})
		if err != nil {
			return 0, fmt.Errorf("evaluate candidate pairs: %w", err)
		}
		v.collectPairwiseResults(pairScores, pairs, runResult.EvalCaseResults)
	}
	return selectPairwiseWinner(pairScores)
}

type candidateEvaluation struct {
	attemptIndex int
	invocation   *evalset.Invocation
	status       evalstatus.EvalStatus
}

func (v *evaluationSelector) buildCandidates(req *runner.CandidateSelectRequest) ([]*candidateEvaluation, error) {
	candidates := make([]*candidateEvaluation, 0, len(req.Attempts))
	for _, attempt := range req.Attempts {
		if attempt == nil {
			continue
		}
		actual, err := invocationFromAttempt(req.Message, attempt)
		if err != nil {
			return nil, fmt.Errorf("build candidate invocation %d: %w", attempt.Index, err)
		}
		inferenceStatus := evalstatus.EvalStatusPassed
		errorMessage := candidateErrorMessage(attempt)
		if errorMessage != "" {
			inferenceStatus = evalstatus.EvalStatusFailed
		}
		candidates = append(candidates, &candidateEvaluation{
			attemptIndex: attempt.Index,
			invocation:   actual,
			status:       inferenceStatus,
		})
	}
	if len(candidates) == 0 {
		return nil, errors.New("bestofn: no usable candidate attempts")
	}
	return candidates, nil
}

type pairwiseComparison struct {
	left  int
	right int
}

func (v *evaluationSelector) buildPairwiseInferenceResults(
	ctx context.Context,
	manager evalset.Manager,
	evalSetID string,
	req *runner.CandidateSelectRequest,
	candidates []*candidateEvaluation,
	scores []*pairwiseCandidateScore,
) ([]*service.InferenceResult, []*pairwiseComparison, error) {
	results := make([]*service.InferenceResult, 0)
	pairs := make([]*pairwiseComparison, 0)
	for left := 0; left < len(candidates); left++ {
		for right := left + 1; right < len(candidates); right++ {
			if recordStatusComparison(scores[left], scores[right], candidates[left], candidates[right]) {
				continue
			}
			evalCaseID := fmt.Sprintf("pair-%d-%d", candidates[left].attemptIndex, candidates[right].attemptIndex)
			evalCase := v.pairwiseEvalCase(evalCaseID, req, candidates[left].invocation, candidates[right].invocation)
			if err := manager.AddCase(ctx, req.AppName, evalSetID, evalCase); err != nil {
				return nil, nil, fmt.Errorf("add pairwise eval case %s: %w", evalCaseID, err)
			}
			results = append(results, &service.InferenceResult{
				AppName:    req.AppName,
				EvalSetID:  evalSetID,
				EvalCaseID: evalCaseID,
				EvalMode:   evalCase.EvalMode,
				Inferences: []*evalset.Invocation{
					candidates[left].invocation,
				},
				SessionID: req.SessionID,
				UserID:    req.UserID,
				Status:    evalstatus.EvalStatusPassed,
			})
			pairs = append(pairs, &pairwiseComparison{left: left, right: right})
		}
	}
	return results, pairs, nil
}

func recordStatusComparison(
	leftScore *pairwiseCandidateScore,
	rightScore *pairwiseCandidateScore,
	left *candidateEvaluation,
	right *candidateEvaluation,
) bool {
	leftPassed := left.status == evalstatus.EvalStatusPassed
	rightPassed := right.status == evalstatus.EvalStatusPassed
	if leftPassed && rightPassed {
		return false
	}
	if leftPassed {
		leftScore.recordWin(rightScore, 0.5)
		return true
	}
	if rightPassed {
		rightScore.recordWin(leftScore, 0.5)
		return true
	}
	return true
}

func (v *evaluationSelector) pairwiseEvalCase(
	evalCaseID string,
	req *runner.CandidateSelectRequest,
	actual *evalset.Invocation,
	expected *evalset.Invocation,
) *evalset.EvalCase {
	return &evalset.EvalCase{
		EvalID:          evalCaseID,
		EvalMode:        evalset.EvalModeTrace,
		ContextMessages: v.contextMessages,
		Conversation: []*evalset.Invocation{
			expected,
		},
		ActualConversation: []*evalset.Invocation{
			actual,
		},
		SessionInput: &evalset.SessionInput{
			AppName: req.AppName,
			UserID:  req.UserID,
		},
	}
}

func (v *evaluationSelector) collectPairwiseResults(
	scores []*pairwiseCandidateScore,
	pairs []*pairwiseComparison,
	results []*evalresult.EvalCaseResult,
) {
	for i, pair := range pairs {
		if i >= len(results) || pair == nil {
			continue
		}
		score, ok := averageMetricScore(results[i])
		if !ok {
			continue
		}
		left := scores[pair.left]
		right := scores[pair.right]
		switch {
		case score > 0.5:
			left.recordWin(right, score-0.5)
		case score < 0.5:
			right.recordWin(left, 0.5-score)
		default:
			left.recordTie(right)
		}
	}
}

func (v *evaluationSelector) buildInferenceResults(
	ctx context.Context,
	manager evalset.Manager,
	evalSetID string,
	req *runner.CandidateSelectRequest,
) ([]*service.InferenceResult, []int, error) {
	results := make([]*service.InferenceResult, 0, len(req.Attempts))
	indexes := make([]int, 0, len(req.Attempts))
	for _, attempt := range req.Attempts {
		if attempt == nil {
			continue
		}
		evalCaseID := fmt.Sprintf("attempt-%d", attempt.Index)
		actual, err := invocationFromAttempt(req.Message, attempt)
		if err != nil {
			return nil, nil, fmt.Errorf("build candidate invocation %d: %w", attempt.Index, err)
		}
		evalCase := v.evalCase(evalCaseID, req, actual)
		if err := manager.AddCase(ctx, req.AppName, evalSetID, evalCase); err != nil {
			return nil, nil, fmt.Errorf("add eval case %s: %w", evalCaseID, err)
		}
		inferenceStatus := evalstatus.EvalStatusPassed
		errorMessage := candidateErrorMessage(attempt)
		if errorMessage != "" {
			inferenceStatus = evalstatus.EvalStatusFailed
		}
		results = append(results, &service.InferenceResult{
			AppName:      req.AppName,
			EvalSetID:    evalSetID,
			EvalCaseID:   evalCaseID,
			EvalMode:     evalCase.EvalMode,
			Inferences:   []*evalset.Invocation{actual},
			SessionID:    req.SessionID,
			UserID:       req.UserID,
			Status:       inferenceStatus,
			ErrorMessage: errorMessage,
		})
		indexes = append(indexes, attempt.Index)
	}
	if len(results) == 0 {
		return nil, nil, errors.New("bestofn: no usable candidate attempts")
	}
	return results, indexes, nil
}

func (v *evaluationSelector) evalCase(
	evalCaseID string,
	req *runner.CandidateSelectRequest,
	actual *evalset.Invocation,
) *evalset.EvalCase {
	evalCase := &evalset.EvalCase{
		EvalID:          evalCaseID,
		EvalMode:        evalset.EvalModeTrace,
		ContextMessages: v.contextMessages,
		Conversation: []*evalset.Invocation{
			actual,
		},
		SessionInput: &evalset.SessionInput{
			AppName: req.AppName,
			UserID:  req.UserID,
		},
	}
	return evalCase
}

func (v *evaluationSelector) metricsWithJudgeRunner() []*metric.EvalMetric {
	if v.judgeRunner == nil {
		return v.metrics
	}
	metrics := make([]*metric.EvalMetric, 0, len(v.metrics))
	for _, evalMetric := range v.metrics {
		metrics = append(metrics, v.metricWithJudgeRunner(evalMetric))
	}
	return metrics
}

func (v *evaluationSelector) metricWithJudgeRunner(
	evalMetric *metric.EvalMetric,
) *metric.EvalMetric {
	if evalMetric == nil || v.judgeRunner == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return evalMetric
	}
	metricCopy := *evalMetric
	criterionCopy := *evalMetric.Criterion
	llmJudgeCopy := *evalMetric.Criterion.LLMJudge
	judgeRunnerOptions := &metricllm.JudgeRunnerOptions{Runner: v.judgeRunner}
	if v.judgeRunnerNumSamples != nil {
		numSamples := *v.judgeRunnerNumSamples
		judgeRunnerOptions.NumSamples = &numSamples
	}
	llmJudgeCopy.JudgeRunnerOptions = judgeRunnerOptions
	criterionCopy.LLMJudge = &llmJudgeCopy
	metricCopy.Criterion = &criterionCopy
	return &metricCopy
}

func (v *evaluationSelector) selectWinner(
	results []*evalresult.EvalCaseResult,
	attemptIndexes []int,
) (int, error) {
	var best *candidateScore
	for i, result := range results {
		if result == nil {
			continue
		}
		score := scoreCandidate(result, attemptIndexes[i])
		if score == nil {
			continue
		}
		if v.requirePassingCandidate && score.status != evalstatus.EvalStatusPassed {
			continue
		}
		if best == nil || score.betterThan(best) {
			best = score
		}
	}
	if best == nil {
		return 0, errors.New("bestofn: no passing candidate")
	}
	return best.attemptIndex, nil
}

type candidateScore struct {
	attemptIndex int
	score        float64
	status       evalstatus.EvalStatus
}

func scoreCandidate(
	result *evalresult.EvalCaseResult,
	attemptIndex int,
) *candidateScore {
	score, ok := averageMetricScore(result)
	if !ok {
		return nil
	}
	return &candidateScore{
		attemptIndex: attemptIndex,
		score:        score,
		status:       result.FinalEvalStatus,
	}
}

func averageMetricScore(result *evalresult.EvalCaseResult) (float64, bool) {
	if result == nil {
		return 0, false
	}
	total := 0.0
	count := 0
	for _, metricResult := range result.OverallEvalMetricResults {
		if metricResult == nil || metricResult.EvalStatus == evalstatus.EvalStatusNotEvaluated {
			continue
		}
		if math.IsNaN(metricResult.Score) {
			continue
		}
		total += metricResult.Score
		count++
	}
	if count == 0 {
		return 0, false
	}
	return total / float64(count), true
}

func (s *candidateScore) betterThan(other *candidateScore) bool {
	if s.score != other.score {
		return s.score > other.score
	}
	if s.status != other.status {
		return s.status == evalstatus.EvalStatusPassed
	}
	return s.attemptIndex < other.attemptIndex
}

type pairwiseCandidateScore struct {
	attemptIndex int
	wins         int
	margin       float64
	comparisons  int
}

func (s *pairwiseCandidateScore) recordWin(other *pairwiseCandidateScore, margin float64) {
	s.wins++
	s.margin += margin
	s.comparisons++
	other.margin -= margin
	other.comparisons++
}

func (s *pairwiseCandidateScore) recordTie(other *pairwiseCandidateScore) {
	s.comparisons++
	other.comparisons++
}

func selectPairwiseWinner(scores []*pairwiseCandidateScore) (int, error) {
	var best *pairwiseCandidateScore
	for _, score := range scores {
		if score == nil || score.comparisons == 0 {
			continue
		}
		if best == nil || score.betterThan(best) {
			best = score
		}
	}
	if best == nil {
		return 0, errors.New("bestofn: no pairwise comparison result")
	}
	return best.attemptIndex, nil
}

func (s *pairwiseCandidateScore) betterThan(other *pairwiseCandidateScore) bool {
	if s.wins != other.wins {
		return s.wins > other.wins
	}
	if s.margin != other.margin {
		return s.margin > other.margin
	}
	return s.attemptIndex < other.attemptIndex
}

func invocationFromAttempt(
	message model.Message,
	attempt *runner.CandidateAttempt,
) (*evalset.Invocation, error) {
	invocationID, finalResponse, err := invocationIdentityAndFinalResponse(attempt)
	if err != nil {
		return nil, err
	}
	inv := &evalset.Invocation{
		InvocationID:          invocationID,
		UserContent:           &message,
		FinalResponse:         finalResponse,
		IntermediateResponses: intermediateResponsesFromEvents(attempt.Events, invocationID, finalResponse),
	}
	if inv.FinalResponse == nil && attempt.FinalResponse != nil {
		inv.FinalResponse = messageFromResponse(attempt.FinalResponse)
	}
	tools, err := toolsFromEvents(attempt.Events)
	if err != nil {
		return nil, err
	}
	inv.Tools = tools
	return inv, nil
}

func intermediateResponsesFromEvents(
	events []*event.Event,
	invocationID string,
	finalResponse *model.Message,
) []*model.Message {
	responses := make([]*model.Message, 0)
	for _, evt := range events {
		if evt == nil || evt.IsRunnerCompletion() {
			continue
		}
		message := finalResponseFromEvent(evt)
		if message == nil {
			continue
		}
		if evt.InvocationID == invocationID && messagesEqual(message, finalResponse) {
			continue
		}
		responses = append(responses, message)
	}
	return responses
}

func messagesEqual(a *model.Message, b *model.Message) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Role == b.Role &&
		a.Content == b.Content &&
		a.ToolID == b.ToolID &&
		a.ToolName == b.ToolName &&
		a.ReasoningContent == b.ReasoningContent &&
		a.ReasoningSignature == b.ReasoningSignature
}

func invocationIdentityAndFinalResponse(
	attempt *runner.CandidateAttempt,
) (string, *model.Message, error) {
	if attempt == nil {
		return "", nil, errors.New("attempt is nil")
	}
	invocationID := attempt.InvocationID
	finalByInvocationID := make(map[string]*model.Message)
	var fallbackFinal *model.Message
	for _, evt := range attempt.Events {
		if evt == nil {
			continue
		}
		if evt.IsRunnerCompletion() && evt.InvocationID != "" {
			invocationID = evt.InvocationID
		} else if invocationID == "" && evt.InvocationID != "" {
			invocationID = evt.InvocationID
		}
		message := finalResponseFromEvent(evt)
		if message == nil {
			continue
		}
		if evt.IsRunnerCompletion() {
			return invocationID, message, nil
		}
		if evt.InvocationID != "" {
			finalByInvocationID[evt.InvocationID] = message
			continue
		}
		fallbackFinal = message
	}
	if invocationID != "" && finalByInvocationID[invocationID] != nil {
		return invocationID, finalByInvocationID[invocationID], nil
	}
	return invocationID, fallbackFinal, nil
}

func finalResponseFromEvent(evt *event.Event) *model.Message {
	if evt == nil || evt.Response == nil || !isAssistantFinalResponse(evt.Response) {
		return nil
	}
	message := evt.Response.Choices[0].Message
	return &message
}

func messageFromResponse(response *model.Response) *model.Message {
	if !isAssistantFinalResponse(response) {
		return nil
	}
	message := response.Choices[0].Message
	return &message
}

func isAssistantFinalResponse(response *model.Response) bool {
	return response != nil &&
		response.IsFinalResponse() &&
		!response.IsToolResultResponse() &&
		len(response.Choices) > 0
}

func candidateErrorMessage(attempt *runner.CandidateAttempt) string {
	if attempt == nil {
		return ""
	}
	for _, evt := range attempt.Events {
		if evt == nil || !evt.IsError() {
			continue
		}
		if evt.Response != nil && evt.Response.Error != nil && evt.Response.Error.Message != "" {
			return evt.Response.Error.Message
		}
		return "candidate attempt produced an error event"
	}
	if attempt.FinalResponse != nil && attempt.FinalResponse.Error != nil {
		if attempt.FinalResponse.Error.Message != "" {
			return attempt.FinalResponse.Error.Message
		}
		return "candidate attempt produced an error response"
	}
	return ""
}

func toolsFromEvents(events []*event.Event) ([]*evalset.Tool, error) {
	tools := make([]*evalset.Tool, 0)
	toolIndex := make(map[string]int)
	for _, evt := range events {
		if evt == nil || evt.Response == nil {
			continue
		}
		if evt.Response.IsToolCallResponse() {
			for _, toolCall := range toolCallsFromEvent(evt) {
				tools = append(tools, toolCall)
				toolIndex[toolCall.ID] = len(tools) - 1
			}
		}
		if evt.Response.IsToolResultResponse() {
			if err := mergeToolResults(evt, toolIndex, tools); err != nil {
				return nil, err
			}
		}
	}
	return tools, nil
}

func toolCallsFromEvent(evt *event.Event) []*evalset.Tool {
	tools := make([]*evalset.Tool, 0)
	for _, choice := range evt.Response.Choices {
		for _, toolCall := range choice.Message.ToolCalls {
			tools = append(tools, &evalset.Tool{
				ID:        toolCall.ID,
				Name:      toolCall.Function.Name,
				Arguments: parseJSONOrString(toolCall.Function.Arguments),
			})
		}
	}
	return tools
}

func mergeToolResults(
	evt *event.Event,
	toolIndex map[string]int,
	tools []*evalset.Tool,
) error {
	for _, choice := range evt.Response.Choices {
		toolID := choice.Message.ToolID
		idx, ok := toolIndex[toolID]
		if !ok {
			return fmt.Errorf("tool ID %s not found in tool ID index for tool result response", toolID)
		}
		tools[idx].Result = parseJSONOrString([]byte(choice.Message.Content))
	}
	return nil
}

func parseJSONOrString(raw []byte) any {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err == nil {
		return value
	}
	return string(raw)
}

type noOpRunner struct{}

func (noOpRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	runOpts ...agent.RunOption,
) (<-chan *event.Event, error) {
	return nil, errors.New("bestofn: no-op runner cannot run inference")
}

func (noOpRunner) Close() error {
	return nil
}
