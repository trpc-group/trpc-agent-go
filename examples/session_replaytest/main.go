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
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	outputDir := flag.String("output", "output", "Output directory for diff report")
	flag.Parse()

	log.Println("[Session Replaytest] Initializing multi-backend replay consistency harness...")

	bInMemory := NewMockBackend("InMemory")
	bSQLite := NewMockBackend("SQLite")

	harness := NewReplayHarness(bInMemory, bSQLite)
	cases := GetDefaultReplayCases()

	allDiffs := make([]DiffEntry, 0)
	for _, c := range cases {
		diffs := harness.RunCase(c)
		allDiffs = append(allDiffs, diffs...)
	}

	report := DiffReport{
		TotalCases: len(cases),
		Diffs:      allDiffs,
		Passed:     len(allDiffs) == 0,
	}

	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	reportPath := fmt.Sprintf("%s/session_memory_summary_track_diff_report.json", *outputDir)
	if err := WriteReport(reportPath, report); err != nil {
		log.Fatalf("Failed to write report: %v", err)
	}

	fmt.Println("==========================================================================")
	fmt.Println("       Session / Memory Multi-Backend Replay Consistency Complete        ")
	fmt.Println("==========================================================================")
	fmt.Printf("Total Cases Executed  : %d\n", report.TotalCases)
	fmt.Printf("Total Diffs Detected  : %d\n", len(report.Diffs))
	fmt.Printf("Consistency Pass Status: %v\n", report.Passed)
	fmt.Printf("Report Generated at    : '%s'\n", reportPath)
	fmt.Println("==========================================================================")

	if !report.Passed {
		os.Exit(1)
	}
}
