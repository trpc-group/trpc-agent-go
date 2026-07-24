//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"os"
	"path/filepath"
)

// BuildReport builds a replay diff report.
func BuildReport(cases []ReplayCase, diffs []Diff) Report {
	names := make([]string, 0, len(cases))
	for _, tc := range cases {
		names = append(names, tc.Name)
	}
	return Report{
		GeneratedBy: "trpc-agent-go/session/replaytest",
		Cases:       names,
		Diffs:       diffs,
	}
}

// WriteReport writes report as stable, indented JSON.
func WriteReport(path string, report Report) error {
	data, err := CanonicalJSON(report)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
