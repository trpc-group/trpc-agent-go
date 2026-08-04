//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"strings"
	"testing"
)

func TestNormalizeFindingsRedactsAllStringFields(t *testing.T) {
	findings := NormalizeFindings([]Finding{{
		ID:             "password=supersecretvalue-id",
		Severity:       "password=supersecretvalue-severity",
		Category:       "password=supersecretvalue-category",
		File:           "password=supersecretvalue-file.go",
		Title:          "password=supersecretvalue-title",
		Evidence:       "password=supersecretvalue-evidence",
		Recommendation: "password=supersecretvalue-recommendation",
		Confidence:     0.99,
		Source:         "password=supersecretvalue-source",
		RuleID:         "password=supersecretvalue-rule",
		Status:         "password=supersecretvalue-status",
		Fingerprint:    "password=supersecretvalue-fingerprint",
	}}, DefaultConfig())
	if len(findings) != 1 {
		t.Fatalf("NormalizeFindings() returned %d findings, want 1", len(findings))
	}
	raw := findings[0]
	for _, value := range []string{
		raw.ID,
		raw.Severity,
		raw.Category,
		raw.File,
		raw.Title,
		raw.Evidence,
		raw.Recommendation,
		raw.Source,
		raw.RuleID,
		raw.Status,
		raw.Fingerprint,
	} {
		if strings.Contains(value, "supersecretvalue") {
			t.Fatalf("NormalizeFindings() leaked secret: %#v", raw)
		}
	}
}
