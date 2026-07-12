// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
package main

import (
	"encoding/json"
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
	report := Report{Cases: len(Cases()), Backends: []string{"inmemory", "json-persistent"}}
	for _, tc := range Cases() {
		want := tc.Build()
		mem := &memoryBackend{name: "inmemory"}
		disk := NewJSONBackend(filepath.Join(tmp, tc.Name+".json"))
		_ = mem.Save(want)
		actual := clone(want)
		if *inject {
			tc.Mutate(&actual)
		}
		_ = disk.Save(actual)
		left, _ := mem.Load()
		right, _ := disk.Load()
		diffs := Compare(tc.Name, disk.Name(), left, right)
		if *inject && len(diffs) > 0 {
			report.DetectedInjected++
		}
		report.Differences = append(report.Differences, diffs...)
	}
	report.DurationMS = time.Since(started).Milliseconds()
	data, _ := json.MarshalIndent(report, "", "  ")
	if e := os.WriteFile(*out, append(data, '\n'), 0o644); e != nil {
		panic(e)
	}
	fmt.Printf("cases=%d differences=%d detected=%d duration=%dms\n", report.Cases, len(report.Differences), report.DetectedInjected, report.DurationMS)
}
