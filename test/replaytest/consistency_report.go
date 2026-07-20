//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvReportPath is the env var name for overriding the diff report output path.
const EnvReportPath = "TRPC_AGENT_REPLAY_REPORT_PATH"
// defaultReportName is the generated output filename. It is gitignored so
// routine test runs never dirty the working tree. For a permanent reference
// see sample_diff_report.json (tracked, read-only example).
const defaultReportName = "replay_diff_report.json"

// WriteDiffReport writes the diff entries as a JSON array to the given path.
// If path is empty, the path is resolved via DiffReportPath().
func WriteDiffReport(path string, diffs []DiffEntry) error {
	if path == "" {
		path = DiffReportPath()
	}
	if diffs == nil {
		diffs = []DiffEntry{}
	}
	encoded, err := json.MarshalIndent(diffs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal diff report: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create diff report dir: %w", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write diff report: %w", err)
	}
	return nil
}

// DiffReportPath returns the file path for the diff report.
// The env var TRPC_AGENT_REPLAY_REPORT_PATH takes precedence;
// otherwise the default "replay_diff_report.json" (gitignored) is used.
// For the permanent reference artifact see sample_diff_report.json.
func DiffReportPath() string {
	if env := strings.TrimSpace(os.Getenv(EnvReportPath)); env != "" {
		return env
	}
	return defaultReportName
}

// HasUnexpectedDiffs reports whether any diff entry is not marked Allowed.
func HasUnexpectedDiffs(diffs []DiffEntry) bool {
	for _, d := range diffs {
		if !d.Allowed {
			return true
		}
	}
	return false
}
