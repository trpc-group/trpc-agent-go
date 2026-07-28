//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package reporter_test

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/reporter"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/rules"
)

// A decoded Git path may hold a newline, pipe, or backtick; none must break the
// Markdown table or the surrounding inline code span.
func TestMarkdownEscapesUserPaths(t *testing.T) {
	f := rules.Finding{
		RuleID:         "X-001",
		Category:       rules.CatVet,
		Severity:       rules.SeverityHigh,
		Title:          "t",
		File:           "a`b|c\nd.go",
		Line:           1,
		Confidence:     "high",
		Evidence:       "e",
		Recommendation: "r",
	}
	md := reporter.ToMarkdown(reporter.Build("id", "", "", []rules.Finding{f}, reporter.Metrics{}))

	var fileRows []string
	for _, l := range strings.Split(md, "\n") {
		if strings.HasPrefix(l, "| File |") {
			fileRows = append(fileRows, l)
		}
	}
	if len(fileRows) != 1 {
		t.Fatalf("want exactly one File row, got %d (a newline likely split the table)", len(fileRows))
	}
	row := fileRows[0]
	if !strings.Contains(row, "d.go") {
		t.Errorf("path tail dropped to another line: %q", row)
	}
	if !strings.Contains(row, `\|`) {
		t.Errorf("pipe not escaped in File cell: %q", row)
	}
}
