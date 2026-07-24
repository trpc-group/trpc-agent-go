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
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"time"
)

var (
	configPath  = flag.String("config", "./data/promptiter.json", "Path to the regression-loop configuration")
	outputDir   = flag.String("output-dir", "./output", "Directory for optimization_report.json and optimization_report.md")
	writePrompt = flag.Bool("write-prompt", false, "Write an accepted candidate back to the configured prompt source")
	timeout     = flag.Duration("timeout", 3*time.Minute, "Maximum pipeline runtime")
)

func main() {
	flag.Parse()
	if *timeout <= 0 {
		log.Fatal("timeout must be greater than zero")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	report, err := runPipeline(ctx, pipelineOptions{
		ConfigPath:  *configPath,
		OutputDir:   *outputDir,
		WritePrompt: *writePrompt,
	})
	if err != nil {
		log.Fatal(err)
	}
	decision := "REJECT"
	if report.GateDecision.Accepted {
		decision = "ACCEPT"
	}
	fmt.Printf("PromptIter regression loop completed: %s\n", decision)
	fmt.Printf("Validation score: %.4f -> %.4f (%+.4f)\n",
		report.Baseline.Validation.Score,
		report.Candidate.Validation.Score,
		report.Delta.ScoreDelta,
	)
	fmt.Printf("Reports: %s, %s\n",
		filepath.Join(*outputDir, reportJSONFile),
		filepath.Join(*outputDir, reportMDFile),
	)
}
