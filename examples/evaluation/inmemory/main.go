package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"

	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
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
	fmt.Printf("Overall Status: %s\n", result.OverallStatus.String())
	runs := 0
	if len(result.EvalCases) > 0 {
		runs = len(result.EvalCases[0].EvalCaseResults)
	}
	fmt.Printf("Runs: %d\n", runs)

	for _, caseResult := range result.EvalCases {
		fmt.Printf("Case %s -> %s\n", caseResult.EvalCaseID, caseResult.OverallStatus.String())
		for _, metricResult := range caseResult.MetricResults {
			fmt.Printf("  Metric %s: score %.2f (threshold %.2f) => %s\n",
				metricResult.MetricName,
				metricResult.Score,
				metricResult.Threshold,
				metricResult.EvalStatus.String(),
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
					UserContent: &genai.Content{
						Role: "user",
						Parts: []*genai.Part{
							{
								Text: "calc add 2 3",
							},
						},
					},
					FinalResponse: &genai.Content{
						Role: "assistant",
						Parts: []*genai.Part{
							{
								Text: "calc result: 5",
							},
						},
					},
					IntermediateData: &evalset.IntermediateData{
						ToolUses: []*genai.FunctionCall{
							{
								Name: "calculator",
								Args: map[string]interface{}{
									"operation": "add",
									"a":         2.0,
									"b":         3.0,
								},
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
					UserContent: &genai.Content{
						Role: "user",
						Parts: []*genai.Part{
							{
								Text: "calc multiply 6 7",
							},
						},
					},
					FinalResponse: &genai.Content{
						Role: "assistant",
						Parts: []*genai.Part{
							{
								Text: "calc result: 42",
							},
						},
					},
					IntermediateData: &evalset.IntermediateData{
						ToolUses: []*genai.FunctionCall{
							{
								Name: "calculator",
								Args: map[string]interface{}{
									"operation": "multiply",
									"a":         6.0,
									"b":         7.0,
								},
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

func prepareMetric(ctx context.Context, metricManager metric.Manager) error {
	evalMetric := &metric.EvalMetric{
		MetricName: "tool_trajectory_avg_score",
		Threshold:  1.0,
	}
	return metricManager.Add(ctx, appName, evalSetID, evalMetric)
}
