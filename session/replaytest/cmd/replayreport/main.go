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

	// Append one demonstrator "inconsistent" row from a locally-reachable
	// fault-wrapped backend so the sample report exercises all local verdicts
	// (allowed_diff from the clean run + inconsistent here) without needing an
	// external service. "unsupported" only appears with an integration backend.
	// The demo uses its own fresh backends because it replays a case whose
	// session key collides with one already created on the shared backends.
	if demo, err := loadCaseByName(*casesDir, "01_single_turn_plain_conversation.faulty"); err == nil {
		demoBackends, derr := backends.EnabledBackends(harness.NewMockSummarizer())
		if derr != nil {
			fmt.Fprintf(os.Stderr, "replayreport: skip fault demo: %v\n", derr)
		} else {
			if cr, rerr := harness.RunFaultDemo(context.Background(), demoBackends, demo); rerr == nil {
				report.AddCase(cr)
			} else {
				fmt.Fprintf(os.Stderr, "replayreport: skip fault demo: %v\n", rerr)
			}
			for _, b := range demoBackends {
				_ = b.Close()
			}
		}
	}

	if err := report.WriteJSON(*out); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// loadCaseByName returns the loaded case whose Name matches, so the report tool
// can pull a specific fault-carrying case for the demonstrator row.
func loadCaseByName(dir, name string) (*harness.ReplayCase, error) {
	cases, err := harness.LoadCases(dir)
	if err != nil {
		return nil, err
	}
	for _, c := range cases {
		if c.Name == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("case %q not found in %s", name, dir)
}
