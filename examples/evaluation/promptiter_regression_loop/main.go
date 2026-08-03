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

	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/internal/regression"
)

var (
	scenario  = flag.String("scenario", "success", "Deterministic scenario: success, ineffective, or overfit")
	outputDir = flag.String("output-dir", "./output", "Report output directory")
)

func main() {
	flag.Parse()
	ctx := context.Background()
	engine, err := newDeterministicEngine(ctx, *scenario)
	if err != nil {
		log.Fatal(err)
	}
	report, err := regression.Run(ctx, engine, deterministicRequest(), regression.GateConfig{
		MinScoreGain: 0.05, MaxNewFailures: 0, MaxScoreRegressions: 0,
		CriticalCaseIDs: []string{"validation_account_security"}, MaxModelCalls: 10, MaxTokens: 1000,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := regression.WriteReports(*outputDir, report); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("scenario=%s accepted=%t report=%s\n", *scenario, report.Accepted, *outputDir)
}

type caseSpec struct {
	id     string
	score  float64
	reason string
}
