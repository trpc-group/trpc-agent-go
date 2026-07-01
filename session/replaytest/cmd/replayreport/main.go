//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
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

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/backends"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/harness"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	casesDir := flag.String("cases", "testdata/cases", "directory containing replay case JSON files")
	out := flag.String("out", "session_memory_summary_track_diff_report.json", "output JSON report path")
	mode := flag.String("mode", "light", "report mode label")
	flag.Parse()

	bs, err := backends.EnabledBackends(harness.NewMockSummarizer())
	if err != nil {
		return fmt.Errorf("enable backends: %w", err)
	}
	defer func() {
		for _, b := range bs {
			_ = b.Close()
		}
	}()

	report, err := harness.RunAll(context.Background(), *casesDir, *mode, bs)
	if err != nil {
		return fmt.Errorf("run replay cases: %w", err)
	}
	if err := report.WriteJSON(*out); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}
