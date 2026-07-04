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
	"os"
	"sort"
)

// ReportConfig controls how diff reports are generated.
type ReportConfig struct {
	// OutputPath is the file path for the JSON report.
	// Defaults to "session_memory_summary_track_diff_report.json".
	OutputPath string
}

// GenerateReport writes the diff reports to a JSON file.
// Callers should pass an absolute OutputPath (e.g. using os.TempDir())
// to avoid writing into the repository tree.
func GenerateReport(reports []DiffReport, cfg ReportConfig) error {
	path := cfg.OutputPath
	if path == "" {
		path = os.TempDir() + "/session_memory_summary_track_diff_report.json"
	}

	// Sort a copy so callers are not affected.
	sorted := make([]DiffReport, len(reports))
	copy(sorted, reports)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].CaseName != sorted[j].CaseName {
			return sorted[i].CaseName < sorted[j].CaseName
		}
		if sorted[i].BackendA != sorted[j].BackendA {
			return sorted[i].BackendA < sorted[j].BackendA
		}
		return sorted[i].BackendB < sorted[j].BackendB
	})

	data, err := json.MarshalIndent(sorted, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
