// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regressionloop"
)

func main() {
	configPath := flag.String("config", "", "path to config file")
	mode := flag.String("mode", "fake", "run mode: fake, trace-smoke, real")
	outputDir := flag.String("output-dir", "./output", "output directory")
	flag.Parse()

	if *configPath == "" {
		fmt.Println("Usage: main -config <config.json> [-mode fake|trace-smoke|real] [-output-dir <dir>]")
		os.Exit(1)
	}

	config, err := regressionloop.LoadConfig(*configPath)
	if err != nil {
		fmt.Printf("Failed to load config: %v\n", err)
		os.Exit(1)
	}

	if *mode != "" {
		config.Mode = *mode
	}
	if *outputDir != "" {
		config.Output.OutputDir = *outputDir
	}

	fmt.Printf("Starting regression loop in %s mode...\n", config.Mode)
	fmt.Printf("Seed: %d\n", config.Seed)
	fmt.Printf("Output directory: %s\n", config.Output.OutputDir)

	pipeline := regressionloop.NewPipeline(config)
	report, err := pipeline.Run(context.Background())
	if err != nil {
		fmt.Printf("Pipeline failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n=== Optimization Report ===\n")
	fmt.Printf("Result: %s\n", report.GateDecision.Result)
	fmt.Printf("Baseline Train: %.4f\n", report.BaselineTrainScore)
	fmt.Printf("Baseline Val: %.4f\n", report.BaselineValScore)
	fmt.Printf("Candidate Train: %.4f\n", report.CandidateTrainScore)
	fmt.Printf("Candidate Val: %.4f\n", report.CandidateValScore)
	fmt.Printf("Train Delta: %+.4f\n", report.ScoreDeltaTrain)
	fmt.Printf("Val Delta: %+.4f\n", report.ScoreDeltaVal)
	fmt.Printf("Duration: %dms\n", report.RunMeta.DurationMS)

	if len(report.GateDecision.RejectionReasons) > 0 {
		fmt.Printf("\nRejection Reasons:\n")
		for _, reason := range report.GateDecision.RejectionReasons {
			fmt.Printf("  - %s\n", reason)
		}
	}

	if len(report.GateDecision.AcceptanceReasons) > 0 {
		fmt.Printf("\nAcceptance Reasons:\n")
		for _, reason := range report.GateDecision.AcceptanceReasons {
			fmt.Printf("  - %s\n", reason)
		}
	}

	fmt.Printf("\nReports written to: %s\n", config.Output.OutputDir)
}
