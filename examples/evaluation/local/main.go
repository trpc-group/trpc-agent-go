package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
	dataDir   = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files")
	outputDir = flag.String("output-dir", "./output", "Directory where evaluation results will be stored")
	modelName = flag.String("model", "deepseek-chat", "Model to use for evaluation runs")
	streaming = flag.Bool("streaming", false, "Enable streaming responses from the agent")
	numRuns   = flag.Int("runs", 1, "Number of times to repeat the evaluation loop per case")
	evalSetID = flag.String("eval-set", "math-basic", "Evaluation set identifier to execute")
)

const appName = "math-eval-app"

func main() {
	flag.Parse()
	ctx := context.Background()
	// New runner.
	runner := runner.NewRunner(appName, newCalculatorAgent(*modelName, *streaming))
	// New manager and registry for evaluation.
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(*dataDir))
	metricManager := metriclocal.New(metric.WithBaseDir(*dataDir))
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(*outputDir))
	registry := registry.New()
	// New agent evaluator.
	agentEvaluator, err := evaluation.New(
		appName,
		runner,
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
	result, err := agentEvaluator.Evaluate(ctx, *evalSetID)
	if err != nil {
		log.Fatalf("evaluate: %v", err)
	}
	printSummary(result, *outputDir)
}

func printSummary(result *evaluation.EvaluationResult, outDir string) {
	fmt.Println("✅ Evaluation completed with local storage")
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
	fmt.Printf("Results saved under: %s\n", outDir)
}
