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
func GenerateReport(reports []DiffReport, cfg ReportConfig) error {
	path := cfg.OutputPath
	if path == "" {
		path = "session_memory_summary_track_diff_report.json"
	}

	// Sort reports by case name + backend pair for deterministic output.
	sort.Slice(reports, func(i, j int) bool {
		if reports[i].CaseName != reports[j].CaseName {
			return reports[i].CaseName < reports[j].CaseName
		}
		if reports[i].BackendA != reports[j].BackendA {
			return reports[i].BackendA < reports[j].BackendA
		}
		return reports[i].BackendB < reports[j].BackendB
	})

	data, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
