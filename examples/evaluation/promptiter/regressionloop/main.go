//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
)

func main() {
	dataDir := flag.String("data-dir", "./promptiter/regressionloop/data", "Directory containing eval sets and PromptIter configuration")
	outputDir := flag.String("output-dir", "./promptiter/regressionloop/output", "Directory for optimization reports")
	flag.Parse()
	report, err := runPipeline(context.Background(), *dataDir, *outputDir)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf(
		"PromptIter regression loop completed: decision=%s accepted=%s rounds=%d\n",
		report.Decision,
		valueOrNone(report.AcceptedCandidateID),
		len(report.Rounds),
	)
}
