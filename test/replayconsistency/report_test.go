//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// updateReport regenerates the committed sample artifact. Writing it only on
// demand keeps an ordinary test run from dirtying the working tree.
var updateReport = flag.Bool("update-report", false, "rewrite the committed sample diff report")

// lightweightBudget is the ceiling the lightweight mode is expected to stay
// under. The issue asks for thirty seconds; the assertion exists so that a
// case which starts waiting on something is noticed rather than tolerated.
const lightweightBudget = 30 * time.Second

func TestReport(t *testing.T) {
	start := time.Now()
	report, err := BuildReport(context.Background(), Scenarios(), LightweightBackends())
	if err != nil {
		t.Fatalf("build report: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed > lightweightBudget {
		t.Errorf("lightweight run took %s, over the %s budget", elapsed, lightweightBudget)
	}
	if report.Mode != "lightweight" {
		t.Errorf("mode = %q, want lightweight", report.Mode)
	}
	if report.Baseline != "inmemory" {
		t.Errorf("baseline = %q, want inmemory", report.Baseline)
	}
	if got, want := report.Summary.Cases, len(Scenarios()); got != want {
		t.Errorf("cases = %d, want %d", got, want)
	}
	if report.Summary.Fatal != 0 {
		t.Errorf("report has %d unclassified differences; every difference must be allowed or known",
			report.Summary.Fatal)
	}

	// Every recorded difference must locate itself well enough to act on.
	for _, c := range report.Cases {
		for _, d := range c.Divergences {
			if d.Path == "" {
				t.Errorf("case %q: divergence with no path", c.Case)
			}
			if d.Backend == "" || d.Baseline == "" {
				t.Errorf("case %q: divergence at %s names no backend pair", c.Case, d.Path)
			}
			if (d.AllowedDiff || d.Known) && d.Reason == "" {
				t.Errorf("case %q: classified divergence at %s carries no explanation", c.Case, d.Path)
			}
		}
	}

	data, err := report.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round Report
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("report does not round-trip: %v", err)
	}
	if round.Summary != report.Summary {
		t.Errorf("summary changed across a round trip: %+v vs %+v", round.Summary, report.Summary)
	}

	path := filepath.Join("testdata", ReportFileName)
	if *updateReport {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("create testdata: %v", err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("wrote %s", path)
		return
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("sample report %s is missing; regenerate it with "+
			"go test ./replayconsistency/ -run TestReport -update-report", path)
	}
}
