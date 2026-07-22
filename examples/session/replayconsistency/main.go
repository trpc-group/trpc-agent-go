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
	if err := runReplay(*out, *inject); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runReplay(output string, inject bool) error {
	started := time.Now()
	tmp, e := os.MkdirTemp("", "replay-json-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(tmp)
	report := Report{Cases: len(Cases()), Backends: []string{"inmemory-services", "sqlite-services"}}
	for _, tc := range Cases() {
		want := tc.Expected()
		mem, disk, err := newReplayBackends(filepath.Join(tmp, tc.Name))
		if err != nil {
			return err
		}
		if err := tc.Run(mem); err != nil {
			return errors.Join(err, mem.Close(), disk.Close())
		}
		if err := tc.Run(disk); err != nil {
			return errors.Join(err, mem.Close(), disk.Close())
		}
		left, err := mem.Load()
		if err != nil {
			return errors.Join(err, mem.Close(), disk.Close())
		}
		right, err := disk.Load()
		if err != nil {
			return errors.Join(err, mem.Close(), disk.Close())
		}
		before := caseDifferences(tc.Name, want, mem.Name(), left, disk.Name(), right)
		diffs := before
		if inject {
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
			return err
		}
	}
	report.DurationMS = time.Since(started).Milliseconds()
	data, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		return e
	}
	if e := os.WriteFile(output, append(data, '\n'), 0o644); e != nil {
		return e
	}
	fmt.Printf("cases=%d differences=%d detected=%d duration=%dms\n", report.Cases, len(report.Differences), report.DetectedInjected, report.DurationMS)
	return validateReplayReport(report, inject)
}

func newReplayBackends(path string) (Backend, Backend, error) {
	return newReplayBackendsWith(path, NewInMemoryBackend, NewSQLiteBackend)
}

func newReplayBackendsWith(
	path string,
	newMemory func() Backend,
	newDisk func(string) (Backend, error),
) (Backend, Backend, error) {
	memoryBackend := newMemory()
	diskBackend, err := newDisk(path)
	if err != nil {
		return nil, nil, errors.Join(err, memoryBackend.Close())
	}
	return memoryBackend, diskBackend, nil
}

func validateReplayReport(report Report, inject bool) error {
	if inject {
		if report.DetectedInjected != report.Cases {
			return fmt.Errorf("fault injection campaign detected %d of %d cases", report.DetectedInjected, report.Cases)
		}
		return nil
	}
	nonAllowed := 0
	for _, diff := range report.Differences {
		if !diff.Allowed {
			nonAllowed++
		}
	}
	if nonAllowed > 0 {
		return fmt.Errorf("replay consistency check found %d non-allowed differences", nonAllowed)
	}
	return nil
}

func caseDifferences(caseName string, expected Snapshot, leftName string, left Snapshot, rightName string, right Snapshot) []Difference {
	var diffs []Difference
	diffs = append(diffs, Compare(caseName, leftName, expected, left)...)
	diffs = append(diffs, Compare(caseName, rightName, expected, right)...)
	diffs = append(diffs, CompareBackends(caseName, leftName+"-vs-"+rightName, left, right)...)
	return diffs
}
