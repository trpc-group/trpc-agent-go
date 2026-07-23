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
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// TestCandidateSmoke runs the fake candidate agent directly to confirm it emits
// a final assistant response through the runner (independent of the eval harness).
func TestCandidateSmoke(t *testing.T) {
	ctx := context.Background()
	cfg, err := loadLoopConfig("./data")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	candidateModel := newCandidateModel(nil, "candidate", "candidate-model", marker, poorRecap, map[string]string{}, golds)
	candidateAgent := newCandidateAgent(candidateModel, cfg.BaselineInstruction)
	r := runner.NewRunner(appName, candidateAgent)
	defer r.Close()

	events, err := r.Run(ctx, "u1", "s1", model.NewUserMessage("金队 vs 银队，比分 88-80，胜者 金队"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var final string
	var n int
	for evt := range events {
		n++
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			if c := evt.Response.Choices[0].Message.Content; c != "" {
				final = c
			}
		}
	}
	t.Logf("events=%d finalContent=%q", n, final)
	if final == "" {
		t.Fatalf("candidate produced no final content")
	}
}

// TestEvalSetLoads confirms the local evalset file parses into invocations.
func TestEvalSetLoads(t *testing.T) {
	m := evalsetlocal.New(evalset.WithBaseDir("./data"))
	set, err := m.Get(context.Background(), appName, validationEvalSetID)
	if err != nil {
		t.Fatalf("get evalset: %v", err)
	}
	t.Logf("evalSetID=%s cases=%d", set.EvalSetID, len(set.EvalCases))
	for _, c := range set.EvalCases {
		t.Logf("  case=%s invocations=%d", c.EvalID, len(c.Conversation))
		for _, inv := range c.Conversation {
			user := ""
			if inv.UserContent != nil {
				user = inv.UserContent.Content
			}
			gold := ""
			if inv.FinalResponse != nil {
				gold = inv.FinalResponse.Content
			}
			t.Logf("    inv=%s user=%q gold=%q", inv.InvocationID, user, gold)
		}
	}
	if len(set.EvalCases) == 0 {
		t.Fatalf("no eval cases loaded")
	}
}

// TestEvaluatorSmoke runs the full agent evaluator on the validation set and
// dumps the per-case metric results to locate why scores are missing.
func TestEvaluatorSmoke(t *testing.T) {
	ctx := context.Background()
	cfg, err := loadLoopConfig("./data")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	candidateModel := newCandidateModel(nil, "candidate", "candidate-model", marker, poorRecap, map[string]string{}, golds)
	candidateAgent := newCandidateAgent(candidateModel, cfg.BaselineInstruction)
	candidateRunner := runner.NewRunner(appName, candidateAgent)
	defer candidateRunner.Close()

	evalSetManager := evalsetlocal.New(evalset.WithBaseDir("./data"))
	metricManager := metriclocal.New(
		metric.WithBaseDir("./data"),
		metric.WithLocator(sharedMetricLocator{metricFileID: metricFileID}),
	)
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(t.TempDir()))

	ev, err := evaluation.New(
		appName,
		candidateRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithNumRuns(1),
	)
	if err != nil {
		t.Fatalf("new evaluator: %v", err)
	}
	defer ev.Close()

	res, err := ev.Evaluate(ctx, validationEvalSetID, evaluation.WithRunDetailsEnabled(true))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	t.Logf("evalSetID=%s cases=%d", res.EvalSetID, len(res.EvalCases))
	for _, c := range res.EvalCases {
		if c == nil {
			continue
		}
		t.Logf("  case=%s runResults=%d", c.EvalCaseID, len(c.EvalCaseResults))
		for _, rr := range c.EvalCaseResults {
			if rr == nil {
				continue
			}
			t.Logf("    runID=%d metrics=%d", rr.RunID, len(rr.OverallEvalMetricResults))
			for _, mr := range rr.OverallEvalMetricResults {
				if mr == nil {
					continue
				}
				reason := ""
				if mr.Details != nil {
					reason = mr.Details.Reason
				}
				t.Logf("      metric=%s score=%.3f status=%s reason=%q",
					mr.MetricName, mr.Score, mr.EvalStatus, reason)
			}
		}
	}
}
