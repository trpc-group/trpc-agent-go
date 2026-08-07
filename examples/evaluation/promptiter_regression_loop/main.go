//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// CLI entry point for Evaluation + Optimization Regression Loop example.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/gates"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/pipeline"
)

func main() {
	modeFlag := flag.String("mode", "fake_deterministic", "Execution mode: fake_deterministic, trace_mode, real")
	dataDirFlag := flag.String("data-dir", "data", "Directory containing evalsets and configs")
	outputDirFlag := flag.String("output", "output", "Directory to write output JSON and Markdown reports")
	flag.Parse()

	log.Printf("[PromptIter Regression Loop] Starting pipeline in '%s' mode...", *modeFlag)

	trainPath := filepath.Join(*dataDirFlag, "train_evalset.json")
	valPath := filepath.Join(*dataDirFlag, "val_evalset.json")

	cfg := pipeline.Config{
		TrainSetPath: trainPath,
		ValSetPath:   valPath,
		Mode:         *modeFlag,
		OutputDir:    *outputDirFlag,
		GateConfig: gates.Config{
			MinValScoreGain:  0.10,
			AllowNewHardFail: false,
			KeyCaseIDs:       []string{"val_opt_03"},
			MaxCostBudgetUSD: 0.50,
		},
	}

	p := pipeline.New(cfg)
	report, err := p.Run()
	if err != nil {
		log.Fatalf("Pipeline execution failed: %v", err)
	}

	fmt.Println("==========================================================================")
	fmt.Println("             Evaluation + Optimization Regression Loop Complete            ")
	fmt.Println("==========================================================================")
	fmt.Printf("Baseline Val Score : %.4f\n", report.BaselineValScore)
	fmt.Printf("Best Val Score     : %.4f (Delta: %+.4f)\n", report.BestValScore, report.BestValScore-report.BaselineValScore)
	fmt.Printf("Overall Accepted   : %t\n", report.OverallAccepted)
	fmt.Printf("Recommendation     : %s\n", report.FinalRecommendation)
	fmt.Println("--------------------------------------------------------------------------")
	fmt.Printf("Reports generated in '%s/':\n", *outputDirFlag)
	fmt.Printf("  - %s/optimization_report.json\n", *outputDirFlag)
	fmt.Printf("  - %s/optimization_report.md\n", *outputDirFlag)
	fmt.Println("==========================================================================")
	os.Exit(0)
}
