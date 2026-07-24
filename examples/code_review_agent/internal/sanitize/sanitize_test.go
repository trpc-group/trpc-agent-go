//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sanitize

import (
	"encoding/json"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

func TestReportRedactsEveryCallerControlledString(t *testing.T) {
	const secret = "abcdefghijklmnop"
	value := "token=" + secret
	in := review.ReviewReport{
		Task: review.ReviewTask{ID: value, Status: review.StatusCompleted, InputType: review.InputTypeDiffFile,
			InputSummary: value, RepoPath: value, Error: value},
		Files: []review.ChangedFile{{OldPath: value, NewPath: value,
			Language: value, PackageName: value, Hunks: []review.Hunk{{
				Header: value, Lines: []review.DiffLine{{Kind: value, Content: value}},
			}}}},
		Findings: []review.Finding{{Severity: value, Category: value, File: value,
			Title: value, Evidence: value, Recommendation: value, Source: value, RuleID: value}},
		SandboxRuns: []review.SandboxRun{{Command: value, Status: value,
			StdoutExcerpt: value, StderrExcerpt: value, Error: value, FailureKind: value}},
		PermissionDecisions: []review.PermissionDecision{{Command: value, Decision: value, Reason: value}},
		FilterDecisions: []review.FilterDecision{{RuleID: value, File: value, Source: value,
			Stage: value, Decision: value, Reason: value}},
		Artifacts: []review.Artifact{{Kind: value, Path: value, SHA256: value}},
		Summary:   value,
	}
	out := Report(in)
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("sanitized report leaked plaintext: %s", data)
	}
	if !strings.Contains(string(data), "REDACTED") {
		t.Fatalf("sanitized report lacks redaction markers: %s", data)
	}
	if in.Task.ID != value || in.Files[0].NewPath != value {
		t.Fatal("Report mutated its input")
	}
}
