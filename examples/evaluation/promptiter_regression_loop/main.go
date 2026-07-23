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
	"os"
	"time"
)

func main() {
	dataDir := flag.String("data-dir", "data", "directory containing evalsets, metrics, prompts, and promptiter config")
	outputDir := flag.String("output-dir", ".", "report output directory")
	timeout := flag.Duration("timeout", 3*time.Minute, "pipeline timeout")
	flag.Parse()
	pipeline, err := LoadPipeline(*dataDir)
	if err != nil {
		fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	report, err := pipeline.Run(ctx)
	if err != nil {
		fail(err)
	}
	if err := WriteReports(report, *outputDir); err != nil {
		fail(err)
	}
	selectedScore := report.BaselineValidation.OverallScore
	for _, round := range report.Rounds {
		if round.CandidateID == report.SelectedCandidate {
			selectedScore = round.Validation.OverallScore
		}
	}
	fmt.Printf("optimization complete: accepted=%t candidate=%s validation=%.3f duration=%dms\n", report.Accepted, report.SelectedCandidate, selectedScore, report.DurationMS)
}

func fail(err error) { fmt.Fprintln(os.Stderr, "error:", err); os.Exit(1) }
