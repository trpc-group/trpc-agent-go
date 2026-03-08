//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptiterator

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/issue"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type instructionEchoRunner struct {
	mu           sync.Mutex
	instructions []string
}

func (r *instructionEchoRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	opts := &agent.RunOptions{}
	for _, opt := range runOpts {
		if opt != nil {
			opt(opts)
		}
	}
	instruction := strings.TrimSpace(opts.Instruction)
	r.mu.Lock()
	r.instructions = append(r.instructions, instruction)
	r.mu.Unlock()
	// Emit a final response echoing the instruction.
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Done:    true,
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: instruction}}},
		},
		InvocationID: "inv",
		Author:       "candidate",
	}
	close(ch)
	return ch, nil
}

func (r *instructionEchoRunner) Close() error { return nil }

type containsKeywordEvaluator struct {
	name    string
	keyword string
}

func (e *containsKeywordEvaluator) Name() string { return e.name }

func (e *containsKeywordEvaluator) Description() string {
	return "Checks whether the candidate final response contains the configured keyword."
}

func (e *containsKeywordEvaluator) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation, evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	perInvocation := make([]*evaluator.PerInvocationResult, 0, len(actuals))
	overallPassed := true
	for _, inv := range actuals {
		content := ""
		if inv != nil && inv.FinalResponse != nil {
			content = inv.FinalResponse.Content
		}
		passed := strings.Contains(content, e.keyword)
		if !passed {
			overallPassed = false
		}
		score := 0.0
		if passed {
			score = 1.0
		}
		st := status.EvalStatusPassed
		if !passed {
			st = status.EvalStatusFailed
		}
		perInvocation = append(perInvocation, &evaluator.PerInvocationResult{Score: score, Status: st})
	}
	overallStatus := status.EvalStatusPassed
	if !overallPassed {
		overallStatus = status.EvalStatusFailed
	}
	overallScore := 0.0
	if overallPassed {
		overallScore = 1.0
	}
	return &evaluator.EvaluateResult{
		OverallScore:         overallScore,
		OverallStatus:        overallStatus,
		PerInvocationResults: perInvocation,
	}, nil
}

type staticAggregator struct {
	mu       sync.Mutex
	received [][]issue.IssueRecord
	out      *issue.AggregatedGradient
}

func (a *staticAggregator) Aggregate(ctx context.Context, rawIssues []issue.IssueRecord) (*issue.AggregatedGradient, error) {
	a.mu.Lock()
	a.received = append(a.received, append([]issue.IssueRecord(nil), rawIssues...))
	a.mu.Unlock()
	return a.out, nil
}

type appendKeywordOptimizer struct {
	keyword string
}

func (o *appendKeywordOptimizer) Optimize(ctx context.Context, currentPrompt string, gradient *issue.AggregatedGradient) (string, error) {
	if strings.Contains(currentPrompt, o.keyword) {
		return currentPrompt, nil
	}
	return strings.TrimSpace(currentPrompt + " " + o.keyword), nil
}

func TestPromptIterator_Run_OptimizesUntilPass(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	caseID := "case"
	metricName := "metric"
	// Prepare in-memory evaluation assets.
	evalSetMgr := evalsetinmemory.New()
	_, err := evalSetMgr.Create(ctx, appName, evalSetID)
	require.NoError(t, err)
	require.NoError(t, evalSetMgr.AddCase(ctx, appName, evalSetID, &evalset.EvalCase{
		EvalID: caseID,
		Conversation: []*evalset.Invocation{
			{
				InvocationID: "inv-1",
				UserContent:  &model.Message{Role: model.RoleUser, Content: "input"},
			},
		},
		SessionInput: &evalset.SessionInput{AppName: appName, UserID: "demo-user", State: map[string]any{}},
	}))
	// Register metrics for the eval set.
	metricMgr := metricinmemory.New()
	require.NoError(t, metricMgr.Add(ctx, appName, evalSetID, &metric.EvalMetric{
		MetricName: metricName,
		Threshold:  1.0,
	}))
	// Register evaluator implementations.
	reg := registry.New()
	require.NoError(t, reg.Register(metricName, &containsKeywordEvaluator{name: metricName, keyword: "good"}))
	// Build workflow dependencies.
	candidate := &instructionEchoRunner{}
	agg := &staticAggregator{
		out: &issue.AggregatedGradient{
			Issues: []issue.AggregatedIssue{{Severity: issue.SeverityP0, Key: "need_good", Summary: "missing keyword", Action: "add good", Cases: []string{caseID}}},
			Notes:  "add keyword",
		},
	}
	opt := &appendKeywordOptimizer{keyword: "good"}
	// Create the workflow with a single optimization round.
	w, err := New(appName, candidate,
		WithMaxOptimizationRounds(1),
		WithEvalSetManager(evalSetMgr),
		WithMetricManager(metricMgr),
		WithRegistry(reg),
		WithIssueExtractor(func(evalSetID string, caseResult *evalresult.EvalCaseResult) []issue.IssueRecord {
			if caseResult == nil || caseResult.FinalEvalStatus == status.EvalStatusPassed {
				return nil
			}
			return []issue.IssueRecord{{
				Issue:      issue.Issue{Severity: issue.SeverityP0, Key: "failed", Summary: "failed", Action: "fix"},
				EvalSetID:  evalSetID,
				EvalCaseID: caseResult.EvalID,
				MetricName: metricName,
			}}
		}),
		WithAggregator(agg),
		WithOptimizer(opt),
	)
	require.NoError(t, err)
	// Run the workflow and verify it converges.
	res, err := w.Run(ctx, "bad", []string{evalSetID})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Passed)
	assert.Equal(t, 1, res.OptimizationRounds)
	assert.Equal(t, "bad good", res.FinalPrompt)
	require.Len(t, res.Rounds, 2)
	assert.False(t, res.Rounds[0].Passed)
	assert.True(t, res.Rounds[1].Passed)
	candidate.mu.Lock()
	instructions := append([]string(nil), candidate.instructions...)
	candidate.mu.Unlock()
	assert.GreaterOrEqual(t, len(instructions), 2)
	assert.Equal(t, "bad", instructions[0])
	assert.Equal(t, "bad good", instructions[len(instructions)-1])
	agg.mu.Lock()
	received := len(agg.received)
	agg.mu.Unlock()
	assert.Equal(t, 1, received)
}
