// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func main() {
	out := flag.String("output", "session_memory_summary_track_diff_report.json", "report path")
	inject := flag.Bool("inject", false, "inject one mismatch into each case")
	flag.Parse()
	started := time.Now()
	tmp, e := os.MkdirTemp("", "replay-json-")
	if e != nil {
		panic(e)
	}
	defer os.RemoveAll(tmp)
	report := Report{Cases: len(Cases()), Backends: []string{"inmemory-services", "sqlite-services"}}
	for _, tc := range Cases() {
		want := tc.Expected()
		mem := NewInMemoryBackend()
		disk, err := NewSQLiteBackend(filepath.Join(tmp, tc.Name))
		if err != nil {
			panic(err)
		}
		if err := tc.Run(mem); err != nil {
			panic(err)
		}
		if err := tc.Run(disk); err != nil {
			panic(err)
		}
		left, err := mem.Load()
		if err != nil {
			panic(err)
		}
		right, err := disk.Load()
		if err != nil {
			panic(err)
		}
		before := caseDifferences(tc.Name, want, mem.Name(), left, disk.Name(), right)
		diffs := before
		if *inject {
			// Corrupt the observed backend result, not the standardized input. This
			// models data loss/corruption after real service operations and avoids
			// having backend validation silently repair the injected fault.
			tc.Mutate(&right)
			diffs = caseDifferences(tc.Name, want, mem.Name(), left, disk.Name(), right)
			if HasNewNonAllowedDiff(before, diffs, tc.FaultPath) {
				report.DetectedInjected++
			}
		}
		report.Differences = append(report.Differences, diffs...)
		if err := errors.Join(mem.Close(), disk.Close()); err != nil {
			panic(err)
		}
	}
	report.DurationMS = time.Since(started).Milliseconds()
	data, _ := json.MarshalIndent(report, "", "  ")
	if e := os.WriteFile(*out, append(data, '\n'), 0o644); e != nil {
		panic(e)
	}
	fmt.Printf("cases=%d differences=%d detected=%d duration=%dms\n", report.Cases, len(report.Differences), report.DetectedInjected, report.DurationMS)
}

func caseDifferences(caseName string, expected Snapshot, leftName string, left Snapshot, rightName string, right Snapshot) []Difference {
	var diffs []Difference
	diffs = append(diffs, Compare(caseName, leftName, expected, left)...)
	diffs = append(diffs, Compare(caseName, rightName, expected, right)...)
	diffs = append(diffs, Compare(caseName, leftName+"-vs-"+rightName, left, right)...)
	return diffs
}
