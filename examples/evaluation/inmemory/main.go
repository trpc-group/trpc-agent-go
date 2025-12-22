//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Model to use for evaluation runs")
	streaming = flag.Bool("streaming", false, "Enable streaming responses from the agent")
	numRuns   = flag.Int("runs", 1, "Number of times to repeat the evaluation loop per case")
)

const (
	appName   = "math-eval-app"
	evalSetID = "math-basic"
)

func main() {
	flag.Parse()
	ctx := context.Background()
	// New runner.
	run := runner.NewRunner(appName, newCalculatorAgent(*modelName, *streaming))

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer run.Close()

	// New manager and registry for evaluation.
	evalSetManager := evalsetinmemory.New()
	metricManager := metricinmemory.New()
	evalResultManager := evalresultinmemory.New()
	registry := registry.New()
	// Prepare evalset and metric.
	if err := prepareEvalSet(ctx, evalSetManager); err != nil {
		log.Fatalf("prepare eval set: %v", err)
	}
	if err := prepareMetric(ctx, metricManager); err != nil {
		log.Fatalf("prepare metric: %v", err)
	}
	// New agent evaluator.
	agentEvaluator, err := evaluation.New(
		appName,
		run,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithRegistry(registry),
		evaluation.WithNumRuns(*numRuns),
	)
	if err != nil {
		log.Fatalf("create evaluator: %v", err)
	}
	// Run evaluate.
	result, err := agentEvaluator.Evaluate(ctx, evalSetID)
	if err != nil {
		log.Fatalf("evaluate: %v", err)
	}
	printSummary(ctx, result, evalResultManager)
}

func printSummary(ctx context.Context, result *evaluation.EvaluationResult, evalResultManager evalresult.Manager) {
	fmt.Println("✅ Evaluation completed")
	fmt.Printf("App: %s\n", result.AppName)
	fmt.Printf("Eval Set: %s\n", result.EvalSetID)
	fmt.Printf("Overall Status: %s\n", result.OverallStatus)
	runs := 0
	if len(result.EvalCases) > 0 {
		runs = len(result.EvalCases[0].EvalCaseResults)
	}
	fmt.Printf("Runs: %d\n", runs)

	for _, caseResult := range result.EvalCases {
		fmt.Printf("Case %s -> %s\n", caseResult.EvalCaseID, caseResult.OverallStatus)
		for _, metricResult := range caseResult.MetricResults {
			fmt.Printf("  Metric %s: score %.2f (threshold %.2f) => %s\n",
				metricResult.MetricName,
				metricResult.Score,
				metricResult.Threshold,
				metricResult.EvalStatus,
			)
		}
		fmt.Println()
	}

	fmt.Println("✅ Evaluation details:")
	evalSetResultIDs, err := evalResultManager.List(ctx, appName)
	if err != nil {
		fmt.Printf("eval result manager list: %v\n", err)
		return
	}
	for _, evalSetResultID := range evalSetResultIDs {
		evalSetResult, err := evalResultManager.Get(ctx, appName, evalSetResultID)
		if err != nil {
			fmt.Printf("eval result manager get: %v\n", err)
			return
		}
		data, err := json.MarshalIndent(evalSetResult, "", "  ")
		if err != nil {
			fmt.Printf("eval result manager marshal: %v\n", err)
			return
		}
		fmt.Println(string(data))
	}
}

func prepareEvalSet(ctx context.Context, evalSetManager evalset.Manager) error {
	if _, err := evalSetManager.Create(ctx, appName, evalSetID); err != nil {
		return err
	}
	cases := []*evalset.EvalCase{
		{
			EvalID: "calc_add",
			Conversation: []*evalset.Invocation{
				{
					InvocationID: "calc_add-1",
					UserContent: &model.Message{
						Role:    model.RoleUser,
						Content: "calc add 2 3",
					},
					FinalResponse: &model.Message{
						Role:    model.RoleAssistant,
						Content: "calc result: 5",
					},
					IntermediateData: &evalset.IntermediateData{
						ToolCalls: []*model.ToolCall{
							{
								Type: "function",
								ID:   "tool_use_1",
								Function: model.FunctionDefinitionParam{
									Name: "calculator",
									Arguments: mustJSON(map[string]any{
										"operation": "add",
										"a":         2.0,
										"b":         3.0,
									}),
								},
							},
						},
						ToolResponses: []*model.Message{
							{
								Role:     model.RoleTool,
								ToolID:   "tool_use_1",
								ToolName: "calculator",
								Content: string(mustJSON(map[string]any{
									"a":         2.0,
									"b":         3.0,
									"operation": "add",
									"result":    5.0,
								})),
							},
						},
					},
				},
			},
			SessionInput: &evalset.SessionInput{
				AppName: appName,
				UserID:  "user",
			},
		},
		{
			EvalID: "calc_multiply",
			Conversation: []*evalset.Invocation{
				{
					InvocationID: "calc_multiply-1",
					UserContent: &model.Message{
						Role:    model.RoleUser,
						Content: "calc multiply 6 7",
					},
					FinalResponse: &model.Message{
						Role:    model.RoleAssistant,
						Content: "calc result: 42",
					},
					IntermediateData: &evalset.IntermediateData{
						ToolCalls: []*model.ToolCall{
							{
								Type: "function",
								ID:   "tool_use_2",
								Function: model.FunctionDefinitionParam{
									Name: "calculator",
									Arguments: mustJSON(map[string]any{
										"operation": "multiply",
										"a":         6.0,
										"b":         7.0,
									}),
								},
							},
						},
						ToolResponses: []*model.Message{
							{
								Role:     model.RoleTool,
								ToolID:   "tool_use_2",
								ToolName: "calculator",
								Content: string(mustJSON(map[string]any{
									"a":         6.0,
									"b":         7.0,
									"operation": "multiply",
									"result":    42.0,
								})),
							},
						},
					},
				},
			},
			SessionInput: &evalset.SessionInput{
				AppName: appName,
				UserID:  "user",
			},
		},
	}
	for _, evalCase := range cases {
		if err := evalSetManager.AddCase(ctx, appName, evalSetID, evalCase); err != nil {
			return err
		}
	}
	return nil
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		log.Errorf("mustJSON: %v", err)
		return nil
	}
	return data
}

func prepareMetric(ctx context.Context, metricManager metric.Manager) error {
	evalMetric := &metric.EvalMetric{
		MetricName: "tool_trajectory_avg_score",
		Threshold:  1.0,
		Criterion:  criterion.New(),
	}
	return metricManager.Add(ctx, appName, evalSetID, evalMetric)
}
