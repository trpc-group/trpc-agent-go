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
	"path/filepath"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// MetricSnapshot is one metric outcome on one evaluated case.
type MetricSnapshot struct {
	// Name identifies the metric.
	Name string `json:"name"`
	// Score is the numeric metric result.
	Score float64 `json:"score"`
	// Status is the pass/fail/not-evaluated state.
	Status status.EvalStatus `json:"status"`
	// Reason explains degraded scores when present.
	Reason string `json:"reason,omitempty"`
}

// CaseSnapshot is the pipeline-internal view of one evaluated case, shared by
// attribution, delta computation, and reporting.
type CaseSnapshot struct {
	// EvalSetID identifies the originating eval set.
	EvalSetID string `json:"evalSetId"`
	// EvalCaseID identifies the case.
	EvalCaseID string `json:"evalCaseId"`
	// Pass is true when every evaluated metric passed.
	Pass bool `json:"pass"`
	// Score is the mean score over evaluated metrics.
	Score float64 `json:"score"`
	// Metrics stores per-metric outcomes.
	Metrics []MetricSnapshot `json:"metrics"`
	// ActualInvocations stores actual invocations (including tool calls) when
	// the source result carried run details.
	ActualInvocations []*evalset.Invocation `json:"-"`
	// Trace stores the execution trace when the source result carried one.
	Trace *atrace.Trace `json:"-"`
}

// finishSnapshot derives Pass and Score from the collected metrics.
func finishSnapshot(snapshot *CaseSnapshot) {
	evaluated := 0
	total := 0.0
	pass := true
	for _, metric := range snapshot.Metrics {
		if metric.Status == status.EvalStatusNotEvaluated {
			continue
		}
		evaluated++
		total += metric.Score
		if metric.Status != status.EvalStatusPassed {
			pass = false
		}
	}
	if evaluated > 0 {
		snapshot.Score = total / float64(evaluated)
	}
	snapshot.Pass = pass && evaluated > 0
}

// SnapshotsFromEngineResult adapts one engine evaluation result (per-case
// metric outcomes plus traces) into case snapshots.
func SnapshotsFromEngineResult(result *promptiterengine.EvaluationResult) []CaseSnapshot {
	if result == nil {
		return nil
	}
	snapshots := make([]CaseSnapshot, 0)
	for _, evalSet := range result.EvalSets {
		for _, caseResult := range evalSet.Cases {
			snapshot := CaseSnapshot{
				EvalSetID:  evalSet.EvalSetID,
				EvalCaseID: caseResult.EvalCaseID,
				Trace:      caseResult.Trace,
			}
			for _, metric := range caseResult.Metrics {
				snapshot.Metrics = append(snapshot.Metrics, MetricSnapshot{
					Name:   metric.MetricName,
					Score:  metric.Score,
					Status: metric.Status,
					Reason: metric.Reason,
				})
			}
			finishSnapshot(&snapshot)
			snapshots = append(snapshots, snapshot)
		}
	}
	return snapshots
}

// SnapshotsFromEvaluationResult adapts one raw evaluation result (as returned
// by AgentEvaluator.Evaluate) into case snapshots, keeping actual invocations
// for failure attribution when run details were requested.
func SnapshotsFromEvaluationResult(result *evaluation.EvaluationResult) []CaseSnapshot {
	if result == nil {
		return nil
	}
	snapshots := make([]CaseSnapshot, 0, len(result.EvalCases))
	for _, evalCase := range result.EvalCases {
		if evalCase == nil || len(evalCase.EvalCaseResults) == 0 {
			continue
		}
		snapshot := CaseSnapshot{
			EvalSetID:  result.EvalSetID,
			EvalCaseID: evalCase.EvalCaseID,
		}
		for _, metric := range evalCase.EvalCaseResults[0].OverallEvalMetricResults {
			if metric == nil {
				continue
			}
			metricSnapshot := MetricSnapshot{
				Name:   metric.MetricName,
				Score:  metric.Score,
				Status: metric.EvalStatus,
			}
			if metric.Details != nil {
				metricSnapshot.Reason = metric.Details.Reason
			}
			snapshot.Metrics = append(snapshot.Metrics, metricSnapshot)
		}
		if len(evalCase.RunDetails) > 0 && evalCase.RunDetails[0] != nil && evalCase.RunDetails[0].Inference != nil {
			inference := evalCase.RunDetails[0].Inference
			snapshot.ActualInvocations = inference.Inferences
			if len(inference.ExecutionTraces) == 1 {
				snapshot.Trace = inference.ExecutionTraces[0]
			}
		}
		finishSnapshot(&snapshot)
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}

// SharedMetricLocator maps every eval set to the single shared metrics.json
// file, matching the issue's required input layout.
type SharedMetricLocator struct{}

// Build implements the metric file locator interface.
func (l *SharedMetricLocator) Build(baseDir, appName, _ string) string {
	return filepath.Join(baseDir, appName, "metrics.json")
}

// astructureTextValue wraps a string into a text surface value.
func astructureTextValue(text string) astructure.SurfaceValue {
	return astructure.SurfaceValue{Text: &text}
}

// ScopeCost accumulates cost counters for one runner scope.
type ScopeCost struct {
	// RunCalls counts runner invocations (one per inference request).
	RunCalls int64 `json:"runCalls"`
	// ModelCalls counts model completions observed in the event stream.
	ModelCalls int64 `json:"modelCalls"`
	// PromptTokens sums prompt tokens reported by model usage.
	PromptTokens int64 `json:"promptTokens"`
	// CompletionTokens sums completion tokens reported by model usage.
	CompletionTokens int64 `json:"completionTokens"`
}

func (c ScopeCost) add(other ScopeCost) ScopeCost {
	return ScopeCost{
		RunCalls:         c.RunCalls + other.RunCalls,
		ModelCalls:       c.ModelCalls + other.ModelCalls,
		PromptTokens:     c.PromptTokens + other.PromptTokens,
		CompletionTokens: c.CompletionTokens + other.CompletionTokens,
	}
}

func (c ScopeCost) subtract(other ScopeCost) ScopeCost {
	return ScopeCost{
		RunCalls:         c.RunCalls - other.RunCalls,
		ModelCalls:       c.ModelCalls - other.ModelCalls,
		PromptTokens:     c.PromptTokens - other.PromptTokens,
		CompletionTokens: c.CompletionTokens - other.CompletionTokens,
	}
}

// CostSummary is a point-in-time view over all scopes.
type CostSummary struct {
	// Scopes stores per-scope counters keyed by scope name.
	Scopes map[string]ScopeCost `json:"scopes"`
	// Total aggregates every scope.
	Total ScopeCost `json:"total"`
}

// Subtract returns the per-scope difference summary minus previous.
func (s CostSummary) Subtract(previous CostSummary) CostSummary {
	delta := CostSummary{Scopes: make(map[string]ScopeCost, len(s.Scopes))}
	for scope, cost := range s.Scopes {
		delta.Scopes[scope] = cost.subtract(previous.Scopes[scope])
	}
	delta.Total = s.Total.subtract(previous.Total)
	return delta
}

// CostTracker accumulates runner costs across concurrent evaluations.
type CostTracker struct {
	mu     sync.Mutex
	scopes map[string]*ScopeCost
}

// NewCostTracker creates an empty tracker.
func NewCostTracker() *CostTracker {
	return &CostTracker{scopes: make(map[string]*ScopeCost)}
}

func (t *CostTracker) scope(name string) *ScopeCost {
	cost, ok := t.scopes[name]
	if !ok {
		cost = &ScopeCost{}
		t.scopes[name] = cost
	}
	return cost
}

func (t *CostTracker) addRun(scope string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scope(scope).RunCalls++
}

// addCompletion counts one model completion; usage is optional because not
// every OpenAI-compatible endpoint reports token usage.
func (t *CostTracker) addCompletion(scope string, usage *model.Usage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	cost := t.scope(scope)
	cost.ModelCalls++
	if usage != nil {
		cost.PromptTokens += int64(usage.PromptTokens)
		cost.CompletionTokens += int64(usage.CompletionTokens)
	}
}

// Snapshot copies the current counters.
func (t *CostTracker) Snapshot() CostSummary {
	t.mu.Lock()
	defer t.mu.Unlock()
	summary := CostSummary{Scopes: make(map[string]ScopeCost, len(t.scopes))}
	for name, cost := range t.scopes {
		summary.Scopes[name] = *cost
		summary.Total = summary.Total.add(*cost)
	}
	return summary
}

// Wrap decorates a runner so every invocation and model usage observed in its
// event stream is accounted under the given scope.
func (t *CostTracker) Wrap(scope string, inner runner.Runner) runner.Runner {
	return &countingRunner{scope: scope, tracker: t, inner: inner}
}

type countingRunner struct {
	scope   string
	tracker *CostTracker
	inner   runner.Runner
}

// Run implements runner.Runner.
func (r *countingRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	runOpts ...agent.RunOption,
) (<-chan *event.Event, error) {
	r.tracker.addRun(r.scope)
	events, err := r.inner.Run(ctx, userID, sessionID, message, runOpts...)
	if err != nil || events == nil {
		return events, err
	}
	// The buffer keeps the relay from deadlocking when a consumer stops
	// draining early (e.g. an evaluator returning on the first error event);
	// the ctx guard releases the goroutine once the run is abandoned.
	out := make(chan *event.Event, 64)
	go func() {
		defer close(out)
		for evt := range events {
			if evt != nil && evt.Response != nil && !evt.IsPartial &&
				evt.Response.Object == model.ObjectTypeChatCompletion {
				r.tracker.addCompletion(r.scope, evt.Usage)
			}
			select {
			case out <- evt:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// Close implements runner.Runner.
func (r *countingRunner) Close() error {
	return r.inner.Close()
}
