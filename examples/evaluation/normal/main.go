//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main provides a minimal end-to-end example of running a local
// evaluation with the built-in tool trajectory metric.
package main

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/tooltrajectory"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func main() {
	ctx := context.Background()

	// Prepare a deterministic agent and populate an evaluation set the agent must satisfy.
	agentInstance := &calculatorAgent{name: "demo-eval-agent"}
	appName := agentInstance.Info().Name
	evalSetID := "sample-eval-set"

	evalSetManager := evalsetinmemory.New()
	if _, err := evalSetManager.Create(ctx, appName, evalSetID); err != nil {
		panic(fmt.Errorf("create eval set: %w", err))
	}
	if err := addSampleEvalCase(ctx, evalSetManager, appName, evalSetID); err != nil {
		panic(err)
	}

	// Register the tool trajectory evaluator so the local service can locate it by metric name.
	registry := evaluator.NewRegistry()
	toolTrajectory := tooltrajectory.New()
	if err := registry.Register(toolTrajectory.Name(), toolTrajectory); err != nil {
		panic(fmt.Errorf("register evaluator: %w", err))
	}

	// Configure the agent evaluator with the prepared managers and metrics.
	trajectoryMetric := &evalset.EvalMetric{
		MetricName: toolTrajectory.Name(),
		Threshold:  1.0,
	}
	agentEvaluator, err := evaluation.NewAgentEvaluator(
		agentInstance,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithEvaluatorRegistry(registry),
		evaluation.WithEvalMetrics([]*evalset.EvalMetric{trajectoryMetric}),
	)
	if err != nil {
		panic(fmt.Errorf("new agent evaluator: %w", err))
	}

	// Run the evaluation and print a compact summary.
	result, err := agentEvaluator.Evaluate(ctx, evalSetID)
	if err != nil {
		panic(fmt.Errorf("evaluate: %w", err))
	}

	fmt.Printf("App: %s\nEval Set: %s\nOverall Status: %s\nExecution Time: %s\n", result.AppName, result.EvalSetID, result.OverallStatus.String(), result.ExecutionTime)
	for _, caseResult := range result.EvalCases {
		fmt.Printf("\nCase %s => %s\n", caseResult.EvalCaseID, caseResult.OverallStatus.String())
		for _, metric := range caseResult.Metrics {
			fmt.Printf("  • Metric %s: score=%.1f threshold=%.1f status=%s\n", metric.MetricName, metric.Score, metric.Threshold, metric.Status.String())
		}
	}
}

func addSampleEvalCase(ctx context.Context, manager evalset.Manager, appName, evalSetID string) error {
	now := time.Now().UTC()
	evalCase := &evalset.EvalCase{
		EvalID:            "case-1",
		CreationTimestamp: evalset.EpochTime{Time: now},
		SessionInput: &evalset.SessionInput{
			AppName: appName,
			UserID:  "eval-user",
		},
		Conversation: []*evalset.Invocation{
			{
				InvocationID:      "expected-invocation-1",
				CreationTimestamp: evalset.EpochTime{Time: now},
				UserContent: &evalset.Content{
					Role:  "user",
					Parts: []evalset.Part{{Text: "Use the calculator tool to add 2 and 2."}},
				},
				FinalResponse: &evalset.Content{
					Role:  "assistant",
					Parts: []evalset.Part{{Text: "The answer is 4."}},
				},
				IntermediateData: &evalset.IntermediateData{
					ToolUses: []evalset.FunctionCall{{
						ID:   "call-1",
						Name: "calculator",
					}},
				},
			},
		},
	}
	if err := manager.AddCase(ctx, appName, evalSetID, evalCase); err != nil {
		return fmt.Errorf("add eval case: %w", err)
	}
	return nil
}

// calculatorAgent emits a fixed set of events that exercise the tool trajectory metric.
type calculatorAgent struct {
	name string
}

func (a *calculatorAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	_ = inv
	out := make(chan *event.Event, 2)
	go func() {
		defer close(out)
		invocationID := fmt.Sprintf("actual-%d", time.Now().UnixNano())
		// Emit a tool-call event so the evaluator can compare the trajectory.
		out <- event.NewResponseEvent(invocationID, a.name, &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{{
						ID:   "call-1",
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name: "calculator",
						},
					}},
				},
			}},
		})
		// Emit the final assistant answer.
		out <- event.NewResponseEvent(invocationID, a.name, &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "The answer is 4.",
				},
			}},
		})
	}()
	return out, nil
}

func (a *calculatorAgent) Tools() []tool.Tool {
	return nil
}

func (a *calculatorAgent) Info() agent.Info {
	return agent.Info{
		Name:        a.name,
		Description: "Deterministic calculator agent used for evaluation demos",
	}
}

func (a *calculatorAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *calculatorAgent) FindSubAgent(string) agent.Agent {
	return nil
}
