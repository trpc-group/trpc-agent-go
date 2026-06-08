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
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	evaluatorregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func TestEvaluationSelector_SelectsHighestScore(t *testing.T) {
	registry := evaluatorregistry.New()
	require.NoError(t, registry.Register("content_score", &contentScoreEvaluator{
		scores: map[string]float64{"low": 0.2, "high": 0.9},
	}))
	selector := newEvaluationSelector(&options{
		metrics: []*metric.EvalMetric{
			contentScoreMetric(0.5),
		},
		registry: registry,
	})
	winner, err := selector.Select(context.Background(), selectRequest(
		candidateAttempt(0, "low"),
		candidateAttempt(1, "high"),
	))
	require.NoError(t, err)
	assert.Equal(t, 1, winner)
}

func TestEvaluationSelector_DoesNotSelectErrorCandidate(t *testing.T) {
	registry := evaluatorregistry.New()
	require.NoError(t, registry.Register("content_score", &contentScoreEvaluator{
		scores: map[string]float64{"ok": 0.1},
	}))
	selector := newEvaluationSelector(&options{
		metrics: []*metric.EvalMetric{
			contentScoreMetric(0),
		},
		registry: registry,
	})
	winner, err := selector.Select(context.Background(), selectRequest(
		errorCandidateAttempt(0, "model failed"),
		candidateAttempt(1, "ok"),
	))
	require.NoError(t, err)
	assert.Equal(t, 1, winner)
}

func TestEvaluationSelector_PairwiseSelectsMostWins(t *testing.T) {
	registry := evaluatorregistry.New()
	require.NoError(t, registry.Register("pairwise_preference", &pairwisePreferenceEvaluator{
		scores: map[string]float64{
			"A>B": 0.4,
			"A>C": 0.8,
			"B>C": 0.9,
		},
	}))
	selector := newEvaluationSelector(&options{
		metrics: []*metric.EvalMetric{
			pairwisePreferenceMetric(),
		},
		selectionMode: SelectionModePairwise,
		registry:      registry,
	})
	winner, err := selector.Select(context.Background(), selectRequest(
		candidateAttempt(0, "A"),
		candidateAttempt(1, "B"),
		candidateAttempt(2, "C"),
	))
	require.NoError(t, err)
	assert.Equal(t, 1, winner)
}

func TestEvaluationSelector_PairwiseEvaluatesEveryCandidatePair(t *testing.T) {
	preference := &pairwisePreferenceEvaluator{
		scores: map[string]float64{
			"A>B": 0.6,
			"A>C": 0.6,
			"B>C": 0.6,
		},
	}
	registry := evaluatorregistry.New()
	require.NoError(t, registry.Register("pairwise_preference", preference))
	selector := newEvaluationSelector(&options{
		metrics: []*metric.EvalMetric{
			pairwisePreferenceMetric(),
		},
		selectionMode: SelectionModePairwise,
		registry:      registry,
	})
	winner, err := selector.Select(context.Background(), selectRequest(
		candidateAttempt(0, "A"),
		candidateAttempt(1, "B"),
		candidateAttempt(2, "C"),
	))
	require.NoError(t, err)
	assert.Equal(t, 0, winner)
	assert.ElementsMatch(t, []string{"A>B", "A>C", "B>C"}, preference.pairs)
}

func TestEvaluationSelector_PairwisePrioritizesWinsOverMargin(t *testing.T) {
	registry := evaluatorregistry.New()
	require.NoError(t, registry.Register("pairwise_preference", &pairwisePreferenceEvaluator{
		scores: map[string]float64{
			"A>B": 0.51,
			"A>C": 0.51,
			"B>C": 1.0,
		},
	}))
	selector := newEvaluationSelector(&options{
		metrics: []*metric.EvalMetric{
			pairwisePreferenceMetric(),
		},
		selectionMode: SelectionModePairwise,
		registry:      registry,
	})
	winner, err := selector.Select(context.Background(), selectRequest(
		candidateAttempt(0, "A"),
		candidateAttempt(1, "B"),
		candidateAttempt(2, "C"),
	))
	require.NoError(t, err)
	assert.Equal(t, 0, winner)
}

func TestEvaluationSelector_PairwiseReturnsOnlyPassingCandidate(t *testing.T) {
	selector := newEvaluationSelector(&options{
		selectionMode: SelectionModePairwise,
	})
	winner, err := selector.Select(context.Background(), selectRequest(
		candidateAttempt(1, "only"),
	))
	require.NoError(t, err)
	assert.Equal(t, 1, winner)
}

func TestEvaluationSelector_PairwiseRejectsOnlyFailedCandidate(t *testing.T) {
	selector := newEvaluationSelector(&options{
		selectionMode: SelectionModePairwise,
	})
	_, err := selector.Select(context.Background(), selectRequest(
		errorCandidateAttempt(1, "model failed"),
	))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no passing candidate")
}

func TestEvaluationSelector_UsesConfiguredEvalSetManager(t *testing.T) {
	manager := evalsetinmemory.New()
	registry := evaluatorregistry.New()
	require.NoError(t, registry.Register("content_score", &contentScoreEvaluator{
		scores: map[string]float64{"ok": 1},
	}))
	selector := newEvaluationSelector(&options{
		metrics: []*metric.EvalMetric{
			contentScoreMetric(0.5),
		},
		evalSetManager: manager,
		registry:       registry,
	})
	winner, err := selector.Select(context.Background(), selectRequest(
		candidateAttempt(0, "ok"),
	))
	require.NoError(t, err)
	assert.Equal(t, 0, winner)
	evalSetIDs, err := manager.List(context.Background(), "app")
	require.NoError(t, err)
	assert.Len(t, evalSetIDs, 1)
}

func TestEvaluationSelector_EvalCaseStoresCandidateTraceInConversation(t *testing.T) {
	actual := &evalset.Invocation{
		InvocationID:  "candidate",
		UserContent:   &model.Message{Role: model.RoleUser, Content: "question"},
		FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "answer"},
	}
	selector := &evaluationSelector{}
	evalCase := selector.evalCase("case", selectRequest(), actual)
	assert.Equal(t, evalset.EvalModeTrace, evalCase.EvalMode)
	require.Len(t, evalCase.Conversation, 1)
	assert.Empty(t, evalCase.ActualConversation)
	assert.Equal(t, "candidate", evalCase.Conversation[0].InvocationID)
	require.NotNil(t, evalCase.Conversation[0].FinalResponse)
	assert.Equal(t, "answer", evalCase.Conversation[0].FinalResponse.Content)
}

func TestEvaluationSelector_ReturnsErrorWhenAllCandidatesFailInference(t *testing.T) {
	registry := evaluatorregistry.New()
	require.NoError(t, registry.Register("content_score", &contentScoreEvaluator{
		scores: map[string]float64{"unused": 1},
	}))
	selector := newEvaluationSelector(&options{
		metrics: []*metric.EvalMetric{
			contentScoreMetric(0),
		},
		registry: registry,
	})
	_, err := selector.Select(context.Background(), selectRequest(
		errorCandidateAttempt(0, "first failed"),
		errorCandidateAttempt(1, "second failed"),
	))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no passing candidate")
}

func TestEvaluationSelector_TieBreaksByAttemptIndex(t *testing.T) {
	registry := evaluatorregistry.New()
	require.NoError(t, registry.Register("content_score", &contentScoreEvaluator{
		scores: map[string]float64{"same-0": 0.8, "same-1": 0.8},
	}))
	selector := newEvaluationSelector(&options{
		metrics: []*metric.EvalMetric{
			contentScoreMetric(0.5),
		},
		registry: registry,
	})
	winner, err := selector.Select(context.Background(), selectRequest(
		candidateAttempt(0, "same-0"),
		candidateAttempt(1, "same-1"),
	))
	require.NoError(t, err)
	assert.Equal(t, 0, winner)
}

func TestEvaluationSelector_RequirePassingCandidate(t *testing.T) {
	registry := evaluatorregistry.New()
	require.NoError(t, registry.Register("content_score", &contentScoreEvaluator{
		scores: map[string]float64{"fail": 0.2, "pass": 0.7},
	}))
	selector := newEvaluationSelector(&options{
		metrics: []*metric.EvalMetric{
			contentScoreMetric(0.5),
		},
		registry:                registry,
		requirePassingCandidate: true,
	})
	winner, err := selector.Select(context.Background(), selectRequest(
		candidateAttempt(0, "fail"),
		candidateAttempt(1, "pass"),
	))
	require.NoError(t, err)
	assert.Equal(t, 1, winner)
}

func TestEvaluationSelector_RequirePassingCandidateFailsWhenNonePass(t *testing.T) {
	registry := evaluatorregistry.New()
	require.NoError(t, registry.Register("content_score", &contentScoreEvaluator{
		scores: map[string]float64{"fail-0": 0.2, "fail-1": 0.3},
	}))
	selector := newEvaluationSelector(&options{
		metrics: []*metric.EvalMetric{
			contentScoreMetric(0.5),
		},
		registry:                registry,
		requirePassingCandidate: true,
	})
	_, err := selector.Select(context.Background(), selectRequest(
		candidateAttempt(0, "fail-0"),
		candidateAttempt(1, "fail-1"),
	))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no passing candidate")
}

func TestNewRunnerOption_ValidatesConfiguration(t *testing.T) {
	_, err := NewRunnerOption()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "eval metrics are empty")
	_, err = NewRunnerOption(
		WithAttempts(0),
		WithEvalMetrics(&metric.EvalMetric{}),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "attempts must be greater than 0")
	_, err = NewRunnerOption(
		WithAttempts(1),
		WithEvalMetrics(&metric.EvalMetric{}),
	)
	require.NoError(t, err)
	_, err = NewRunnerOption(
		WithEvalMetrics(&metric.EvalMetric{}),
		WithJudgeRunnerNumSamples(2),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "judge runner is required")
	_, err = NewRunnerOption(
		WithEvalMetrics(&metric.EvalMetric{}),
		WithJudgeRunner(noOpRunner{}),
		WithJudgeRunnerNumSamples(2),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LLM judge metric")
	_, err = NewRunnerOption(
		WithEvalMetrics(&metric.EvalMetric{}),
		WithEvalSetManager(nil),
	)
	require.NoError(t, err)
	_, err = NewRunnerOption(
		WithEvalMetrics(&metric.EvalMetric{}),
		WithSelectionMode(SelectionMode("unknown")),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported selection mode")
	_, err = NewRunnerOption(
		WithEvalMetrics(&metric.EvalMetric{}),
		WithSelectionMode(SelectionModePairwise),
		WithRequirePassingCandidate(true),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "require passing candidate")
}

func TestEvaluationSelector_MetricsWithJudgeRunnerInjectsRunner(t *testing.T) {
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{},
		},
	}
	selector := newEvaluationSelector(&options{
		metrics:     []*metric.EvalMetric{evalMetric},
		judgeRunner: noOpRunner{},
	})
	got := selector.(*evaluationSelector).metricsWithJudgeRunner()
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Criterion)
	require.NotNil(t, got[0].Criterion.LLMJudge)
	require.NotNil(t, got[0].Criterion.LLMJudge.JudgeRunnerOptions)
	assert.NotNil(t, got[0].Criterion.LLMJudge.JudgeRunnerOptions.Runner)
}

func TestInvocationFromAttempt_PrefersAttemptInvocationFinalResponse(t *testing.T) {
	attempt := &runner.CandidateAttempt{
		Index:        0,
		InvocationID: "root",
		Events: []*event.Event{
			responseEventWithInvocation("root", "root-final"),
			responseEventWithInvocation("child", "child-final"),
		},
	}
	invocation, err := invocationFromAttempt(model.NewUserMessage("question"), attempt)
	require.NoError(t, err)
	require.NotNil(t, invocation.FinalResponse)
	assert.Equal(t, "root-final", invocation.FinalResponse.Content)
}

func TestInvocationFromAttempt_PopulatesIntermediateResponses(t *testing.T) {
	attempt := &runner.CandidateAttempt{
		Index:        0,
		InvocationID: "root",
		Events: []*event.Event{
			responseEventWithInvocation("planner", "plan"),
			responseEventWithInvocation("root", "final"),
		},
	}
	invocation, err := invocationFromAttempt(model.NewUserMessage("question"), attempt)
	require.NoError(t, err)
	require.NotNil(t, invocation.FinalResponse)
	assert.Equal(t, "final", invocation.FinalResponse.Content)
	require.Len(t, invocation.IntermediateResponses, 1)
	assert.Equal(t, "plan", invocation.IntermediateResponses[0].Content)
}

func TestInvocationFromAttempt_DoesNotUseToolResultAsIntermediateResponse(t *testing.T) {
	invocationID := "root"
	callID := "call"
	attempt := &runner.CandidateAttempt{
		Index:        0,
		InvocationID: invocationID,
		Events: []*event.Event{
			toolCallEvent(invocationID, callID, "query"),
			toolResultEvent(invocationID, callID, "result"),
			responseEventWithInvocation(invocationID, "final"),
		},
	}
	invocation, err := invocationFromAttempt(model.NewUserMessage("question"), attempt)
	require.NoError(t, err)
	require.NotNil(t, invocation.FinalResponse)
	assert.Equal(t, "final", invocation.FinalResponse.Content)
	assert.Empty(t, invocation.IntermediateResponses)
}

func TestInvocationFromAttempt_DoesNotUseToolResultAsFinalResponse(t *testing.T) {
	invocationID := "root"
	callID := "call"
	attempt := &runner.CandidateAttempt{
		Index:        0,
		InvocationID: invocationID,
		Events: []*event.Event{
			toolCallEvent(invocationID, callID, "query"),
			toolResultEvent(invocationID, callID, "result"),
		},
	}
	invocation, err := invocationFromAttempt(model.NewUserMessage("question"), attempt)
	require.NoError(t, err)
	assert.Nil(t, invocation.FinalResponse)
	assert.Empty(t, invocation.IntermediateResponses)
}

func TestInvocationFromAttempt_IgnoresNonFinalAttemptResponse(t *testing.T) {
	attempt := &runner.CandidateAttempt{
		Index:        0,
		InvocationID: "root",
		FinalResponse: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Done:   false,
			Choices: []model.Choice{
				{Index: 0, Message: model.Message{Role: model.RoleAssistant, Content: "partial"}},
			},
		},
	}
	invocation, err := invocationFromAttempt(model.NewUserMessage("question"), attempt)
	require.NoError(t, err)
	assert.Nil(t, invocation.FinalResponse)
}

func TestInvocationFromAttempt_ReturnsToolResultMismatchError(t *testing.T) {
	attempt := &runner.CandidateAttempt{
		Index:        0,
		InvocationID: "root",
		Events: []*event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Index: 0, Message: model.Message{Role: model.RoleTool, ToolID: "missing", Content: "{}"}},
					},
				},
			},
		},
	}
	_, err := invocationFromAttempt(model.NewUserMessage("question"), attempt)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tool ID missing")
}

func TestEvaluationSelector_SelectWinnerTreatsNaNAsNotEvaluated(t *testing.T) {
	selector := &evaluationSelector{}
	winner, err := selector.selectWinner([]*evalresult.EvalCaseResult{
		{
			FinalEvalStatus: status.EvalStatusPassed,
			OverallEvalMetricResults: []*evalresult.EvalMetricResult{
				{Score: math.NaN(), EvalStatus: status.EvalStatusPassed},
			},
		},
		{
			FinalEvalStatus: status.EvalStatusPassed,
			OverallEvalMetricResults: []*evalresult.EvalMetricResult{
				{Score: 0.1, EvalStatus: status.EvalStatusPassed},
			},
		},
	}, []int{0, 1})
	require.NoError(t, err)
	assert.Equal(t, 1, winner)
}

func contentScoreMetric(threshold float64) *metric.EvalMetric {
	return &metric.EvalMetric{
		EvaluatorName: "content_score",
		Threshold:     threshold,
	}
}

func pairwisePreferenceMetric() *metric.EvalMetric {
	return &metric.EvalMetric{
		EvaluatorName: "pairwise_preference",
		Threshold:     0.5,
	}
}

type contentScoreEvaluator struct {
	scores map[string]float64
}

func (e *contentScoreEvaluator) Name() string {
	return "content_score"
}

func (e *contentScoreEvaluator) Description() string {
	return "Scores candidates by final response content."
}

func (e *contentScoreEvaluator) Evaluate(
	ctx context.Context,
	actuals []*evalset.Invocation,
	expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric,
) (*evaluator.EvaluateResult, error) {
	results := make([]*evaluator.PerInvocationResult, 0, len(actuals))
	total := 0.0
	overall := status.EvalStatusPassed
	for _, actual := range actuals {
		score := e.score(actual)
		itemStatus := status.EvalStatusPassed
		if score < evalMetric.Threshold {
			itemStatus = status.EvalStatusFailed
			overall = status.EvalStatusFailed
		}
		total += score
		results = append(results, &evaluator.PerInvocationResult{
			ActualInvocation: actual,
			Score:            score,
			Status:           itemStatus,
			Details:          &evaluator.PerInvocationDetails{Score: score},
		})
	}
	if len(actuals) == 0 {
		return &evaluator.EvaluateResult{
			OverallStatus:        status.EvalStatusNotEvaluated,
			PerInvocationResults: results,
		}, nil
	}
	return &evaluator.EvaluateResult{
		OverallScore:         total / float64(len(actuals)),
		OverallStatus:        overall,
		PerInvocationResults: results,
	}, nil
}

func (e *contentScoreEvaluator) score(actual *evalset.Invocation) float64 {
	if actual == nil || actual.FinalResponse == nil {
		return 0
	}
	return e.scores[actual.FinalResponse.Content]
}

type pairwisePreferenceEvaluator struct {
	scores map[string]float64
	pairs  []string
}

func (e *pairwisePreferenceEvaluator) Name() string {
	return "pairwise_preference"
}

func (e *pairwisePreferenceEvaluator) Description() string {
	return "Scores whether the actual candidate is preferred over the expected candidate."
}

func (e *pairwisePreferenceEvaluator) Evaluate(
	ctx context.Context,
	actuals []*evalset.Invocation,
	expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric,
) (*evaluator.EvaluateResult, error) {
	results := make([]*evaluator.PerInvocationResult, 0, len(actuals))
	total := 0.0
	overall := status.EvalStatusPassed
	for i, actual := range actuals {
		score := e.score(actual, expecteds[i])
		itemStatus := status.EvalStatusPassed
		if score < evalMetric.Threshold {
			itemStatus = status.EvalStatusFailed
			overall = status.EvalStatusFailed
		}
		total += score
		results = append(results, &evaluator.PerInvocationResult{
			ActualInvocation:   actual,
			ExpectedInvocation: expecteds[i],
			Score:              score,
			Status:             itemStatus,
			Details:            &evaluator.PerInvocationDetails{Score: score},
		})
	}
	if len(actuals) == 0 {
		return &evaluator.EvaluateResult{
			OverallStatus:        status.EvalStatusNotEvaluated,
			PerInvocationResults: results,
		}, nil
	}
	return &evaluator.EvaluateResult{
		OverallScore:         total / float64(len(actuals)),
		OverallStatus:        overall,
		PerInvocationResults: results,
	}, nil
}

func (e *pairwisePreferenceEvaluator) score(actual *evalset.Invocation, expected *evalset.Invocation) float64 {
	if actual == nil || actual.FinalResponse == nil || expected == nil || expected.FinalResponse == nil {
		return 0.5
	}
	pair := actual.FinalResponse.Content + ">" + expected.FinalResponse.Content
	e.pairs = append(e.pairs, pair)
	score, ok := e.scores[pair]
	if !ok {
		return 0.5
	}
	return score
}

func selectRequest(attempts ...*runner.CandidateAttempt) *runner.CandidateSelectRequest {
	return &runner.CandidateSelectRequest{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
		Message:   model.NewUserMessage("question"),
		Attempts:  attempts,
	}
}

func candidateAttempt(index int, content string) *runner.CandidateAttempt {
	response := &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{
			{Index: 0, Message: model.Message{Role: model.RoleAssistant, Content: content}},
		},
	}
	return &runner.CandidateAttempt{
		Index:         index,
		InvocationID:  content,
		FinalResponse: response,
		Events: []*event.Event{
			event.NewResponseEvent(content, "candidate", response),
		},
	}
}

func toolCandidateAttempt(index int, query string, result string) *runner.CandidateAttempt {
	invocationID := "tool-candidate"
	callID := "call-" + query
	response := &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{
			{Index: 0, Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
		},
	}
	return &runner.CandidateAttempt{
		Index:         index,
		InvocationID:  invocationID,
		FinalResponse: response,
		Events: []*event.Event{
			toolCallEvent(invocationID, callID, query),
			toolResultEvent(invocationID, callID, result),
			event.NewResponseEvent(invocationID, "candidate", response),
		},
	}
}

func toolCallEvent(invocationID string, callID string, query string) *event.Event {
	response := &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{
							ID:   callID,
							Type: "function",
							Function: model.FunctionDefinitionParam{
								Name:      "lookup",
								Arguments: []byte(`{"query":"` + query + `"}`),
							},
						},
					},
				},
			},
		},
	}
	return event.NewResponseEvent(invocationID, "candidate", response)
}

func toolResultEvent(invocationID string, callID string, result string) *event.Event {
	response := &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.NewToolMessage(
					callID,
					"lookup",
					`{"value":"`+result+`"}`,
				),
			},
		},
	}
	return event.NewResponseEvent(invocationID, "candidate", response)
}

func errorCandidateAttempt(index int, message string) *runner.CandidateAttempt {
	return &runner.CandidateAttempt{
		Index:        index,
		InvocationID: "error",
		Events: []*event.Event{
			event.NewErrorEvent("error", "candidate", "api_error", message),
		},
	}
}

func responseEventWithInvocation(invocationID string, content string) *event.Event {
	response := &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{
			{Index: 0, Message: model.Message{Role: model.RoleAssistant, Content: content}},
		},
	}
	return event.NewResponseEvent(invocationID, "candidate", response)
}

var _ evaluator.Evaluator = (*contentScoreEvaluator)(nil)
