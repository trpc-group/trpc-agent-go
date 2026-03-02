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
	"errors"
	"flag"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	evalresultmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/mysql"
	evalsetmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/mysql"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	metricmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/mysql"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
	mysqlDSN    = flag.String("dsn", "user:password@tcp(localhost:3306)/db?parseTime=true&charset=utf8mb4", "MySQL DSN used by evaluation managers")
	tablePrefix = flag.String("table-prefix", "evaluation_example", "Table prefix applied to evaluation tables")
	skipDBInit  = flag.Bool("skip-db-init", false, "Skip table creation during manager initialization")
	modelName   = flag.String("model", "deepseek-chat", "Model to use for evaluation runs")
	streaming   = flag.Bool("streaming", false, "Enable streaming responses from the agent")
	numRuns     = flag.Int("runs", 1, "Number of times to repeat the evaluation loop per case")
	evalSetID   = flag.String("eval-set", "math-basic", "Evaluation set identifier to execute")
)

const appName = "math-eval-app"

func main() {
	flag.Parse()
	if err := run(context.Background()); err != nil {
		log.Fatalf("run evaluation: %v", err)
	}
}

func run(ctx context.Context) error {
	if *mysqlDSN == "" {
		return errors.New("missing MySQL DSN, set -dsn")
	}

	runner := runner.NewRunner(appName, newCalculatorAgent(*modelName, *streaming))
	defer runner.Close()

	evalSetManager, err := evalsetmysql.New(
		evalsetmysql.WithMySQLClientDSN(*mysqlDSN),
		evalsetmysql.WithTablePrefix(*tablePrefix),
		evalsetmysql.WithSkipDBInit(*skipDBInit),
	)
	if err != nil {
		return fmt.Errorf("create mysql evalset manager: %w", err)
	}
	metricManager, err := metricmysql.New(
		metricmysql.WithMySQLClientDSN(*mysqlDSN),
		metricmysql.WithTablePrefix(*tablePrefix),
		metricmysql.WithSkipDBInit(*skipDBInit),
	)
	if err != nil {
		_ = evalSetManager.Close()
		return fmt.Errorf("create mysql metric manager: %w", err)
	}
	evalResultManager, err := evalresultmysql.New(
		evalresultmysql.WithMySQLClientDSN(*mysqlDSN),
		evalresultmysql.WithTablePrefix(*tablePrefix),
		evalresultmysql.WithSkipDBInit(*skipDBInit),
	)
	if err != nil {
		_ = evalSetManager.Close()
		_ = metricManager.Close()
		return fmt.Errorf("create mysql evalresult manager: %w", err)
	}

	registry := registry.New()
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
		evalSetManager.Close()
		metricManager.Close()
		evalResultManager.Close()
		return fmt.Errorf("create evaluator: %w", err)
	}
	defer agentEvaluator.Close()

	result, err := agentEvaluator.Evaluate(ctx, *evalSetID)
	if err != nil {
		return fmt.Errorf("evaluate: %w", err)
	}
	printSummary(result)
	return nil
}

func printSummary(result *evaluation.EvaluationResult) {
	fmt.Println("✅ Evaluation completed with MySQL storage")
	fmt.Printf("App: %s\n", result.AppName)
	fmt.Printf("Eval Set: %s\n", result.EvalSetID)
	fmt.Printf("Overall Status: %s\n", result.OverallStatus)
	if result.EvalResult != nil {
		fmt.Printf("EvalSetResult ID: %s\n", result.EvalResult.EvalSetResultID)
	}
	runs := 0
	if len(result.EvalCases) > 0 {
		runs = len(result.EvalCases[0].EvalCaseResults)
	}
	fmt.Printf("Runs: %d\n", runs)

	for _, caseResult := range result.EvalCases {
		fmt.Printf("Case %s -> %s\n", caseResult.EvalCaseID, caseResult.OverallStatus)
		for _, metricResult := range caseResult.MetricResults {
			fmt.Printf("  Metric %s: score %.2f, threshold %.2f, status %s\n",
				metricResult.MetricName,
				metricResult.Score,
				metricResult.Threshold,
				metricResult.EvalStatus,
			)
		}
		fmt.Println()
	}
}
