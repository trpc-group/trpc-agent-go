//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	"trpc.group/trpc-go/trpc-agent-go/test/replayconsistency"
)

func main() {
	var (
		integration = flag.Bool(
			"integration",
			false,
			"include environment-configured integration backends",
		)
		output = flag.String(
			"output",
			"session_memory_summary_track_diff_report.json",
			"JSON report path",
		)
		timeout = flag.Duration(
			"timeout",
			30*time.Second,
			"overall replay timeout",
		)
	)
	flag.Parse()

	factories := replayconsistency.LightweightFactories()
	mode := "lightweight"
	if *integration {
		factories = replayconsistency.AllFactoriesFromEnvironment()
		mode = "integration"
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	started := time.Now()
	runner := replaytest.Runner{
		BaselineBackend: "inmemory",
		Backends:        factories,
	}
	result, err := runner.Run(ctx, replaytest.StandardCases())
	if err != nil {
		fmt.Fprintf(os.Stderr, "replay failed: %v\n", err)
		os.Exit(1)
	}
	if err := replaytest.WriteReport(*output, result.Report); err != nil {
		fmt.Fprintf(os.Stderr, "write report: %v\n", err)
		os.Exit(1)
	}

	printResult(mode, factories, result.Report, *output, time.Since(started))
	if result.Report.Summary.Failed > 0 ||
		result.Report.Summary.DisallowedDiffs > 0 {
		os.Exit(1)
	}
}

func printResult(
	mode string,
	factories []replaytest.BackendFactory,
	report *replaytest.DiffReport,
	output string,
	elapsed time.Duration,
) {
	backendNames := make([]string, 0, len(factories))
	for _, factory := range factories {
		status := ""
		if factory.Open == nil {
			status = " (unsupported)"
		}
		backendNames = append(backendNames, factory.Name+status)
	}

	fmt.Println("Session / Memory Replay Consistency")
	fmt.Printf("Mode: %s\n", mode)
	fmt.Printf("Baseline: %s\n", report.BaselineBackend)
	fmt.Printf("Backends: %s\n", strings.Join(backendNames, ", "))
	fmt.Println()
	fmt.Printf(
		"%-29s %-12s %-12s %s\n",
		"CASE",
		"BACKEND",
		"STATUS",
		"DIFFS",
	)
	for _, comparison := range report.Cases {
		allowed, disallowed := differenceCounts(comparison.Differences)
		fmt.Printf(
			"%-29s %-12s %-12s %d allowed / %d disallowed\n",
			comparison.Case,
			comparison.Backend,
			comparison.Status,
			allowed,
			disallowed,
		)
	}
	fmt.Println()
	fmt.Printf("Comparisons: %d\n", report.Summary.CaseComparisons)
	fmt.Printf(
		"Passed: %d, Failed: %d, Unsupported: %d\n",
		report.Summary.Passed,
		report.Summary.Failed,
		report.Summary.Unsupported,
	)
	fmt.Printf(
		"Allowed diffs: %d, Disallowed diffs: %d\n",
		report.Summary.AllowedDiffs,
		report.Summary.DisallowedDiffs,
	)
	fmt.Printf("Elapsed: %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("Report: %s\n", output)
}

func differenceCounts(differences []replaytest.Difference) (int, int) {
	var allowed int
	for _, difference := range differences {
		if difference.AllowedDiff {
			allowed++
		}
	}
	return allowed, len(differences) - allowed
}
