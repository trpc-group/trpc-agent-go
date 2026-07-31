//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package report

import (
	"bytes"
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/domain"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
)

func TestRenderJSONUsesArraysForEmptyCollections(t *testing.T) {
	data, err := RenderJSON(DTO{TaskID: "empty", Status: domain.StatusCompleted})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"findings": []`,
		`"needs_human_review": []`,
		`"sandbox_runs": []`,
		`"governance": []`,
		`"artifacts": []`,
		`"files": []`,
		`"parser_warnings": []`,
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatalf("JSON missing array %s:\n%s", want, data)
		}
	}
}

func TestRenderJSONAndMarkdownRedactAndSummarize(t *testing.T) {
	dto := DTO{
		TaskID: "task-1",
		Status: domain.StatusNeedsHumanReview,
		Findings: []domain.Finding{{
			Severity:       domain.SeverityHigh,
			Category:       domain.CategorySecrets,
			File:           "a|b.go",
			Line:           2,
			Title:          "token leaked",
			Evidence:       "fixture-secret-value-github-token",
			Recommendation: "remove token",
			Confidence:     0.98,
			Source:         "rule",
			RuleID:         "secrets.github-token",
		}},
		SandboxRuns: []sandbox.Result{{CommandID: "go-test", ExitCode: 1, DurationMS: 12}},
		Governance:  []string{"allow:go-test:abc"},
		Artifacts:   []string{"review_report.json"},
		ArtifactDetails: []Artifact{{
			Path:        "review_report.json",
			SHA256:      "sha256:abc123",
			Bytes:       42,
			ContentType: "application/json",
			Durable:     true,
		}},
	}
	js, err := RenderJSON(dto)
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	if !json.Valid(js) || bytes.Contains(js, []byte("fixture-secret-value")) {
		t.Fatalf("invalid or unredacted json: %s", js)
	}
	md := RenderMarkdown(dto)
	if bytes.Contains([]byte(md), []byte("fixture-secret-value")) || !bytes.Contains([]byte(md), []byte("\\|")) {
		t.Fatalf("markdown not redacted/escaped: %s", md)
	}
	for _, section := range []string{"## Summary", "## Metrics", "## Reviewed Files", "## Parser Warnings", "## Findings", "## Needs Human Review", "## Governance", "## Sandbox", "## Artifacts"} {
		if !bytes.Contains([]byte(md), []byte(section)) {
			t.Fatalf("report omitted %s:\n%s", section, md)
		}
	}
	if !bytes.Contains(js, []byte(`"stats"`)) || !bytes.Contains(js, []byte(`"metrics"`)) || !bytes.Contains(js, []byte(`"files"`)) || !bytes.Contains(js, []byte(`"artifact_details"`)) {
		t.Fatalf("report omitted required summaries:\njson=%s\nmd=%s", js, md)
	}
	if !bytes.Contains(js, []byte(`"sha256": "sha256:abc123"`)) || !bytes.Contains([]byte(md), []byte("sha256:abc123")) {
		t.Fatalf("artifact metadata omitted:\njson=%s\nmd=%s", js, md)
	}
}

func TestRenderJSONAndMarkdownDoNotMutateCallerDTO(t *testing.T) {
	dto := DTO{
		TaskID:     "mutate",
		Status:     domain.StatusCompleted,
		Findings:   []domain.Finding{{Evidence: "fixture-secret-value-github-token"}},
		Governance: []string{"allow:token=\"fixture-secret-value-governance\""},
		SandboxRuns: []sandbox.Result{{
			Stdout: "password=\"fixture-secret-value-stdout\"",
		}},
		ArtifactDetails: []Artifact{{Path: "review_report.json"}},
	}
	if _, err := RenderJSON(dto); err != nil {
		t.Fatalf("json: %v", err)
	}
	if dto.Findings[0].Evidence != "fixture-secret-value-github-token" ||
		dto.Governance[0] != "allow:token=\"fixture-secret-value-governance\"" ||
		dto.SandboxRuns[0].Stdout != "password=\"fixture-secret-value-stdout\"" ||
		dto.ArtifactDetails[0].Path != "review_report.json" {
		t.Fatalf("RenderJSON mutated caller DTO: %#v", dto)
	}
	_ = RenderMarkdown(dto)
	if dto.Findings[0].Evidence != "fixture-secret-value-github-token" || dto.SandboxRuns[0].Stdout != "password=\"fixture-secret-value-stdout\"" {
		t.Fatalf("RenderMarkdown mutated caller DTO: %#v", dto)
	}
}
