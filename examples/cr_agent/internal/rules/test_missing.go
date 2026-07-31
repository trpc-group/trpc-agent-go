//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rules

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/types"
)

// --- TEST-001: Missing test for new exported function ---
//
// Flags Go files in non-test packages that add new exported functions
// (func TitleCase...) without a corresponding _test.go file change.
// This rule is intentionally coarse-grained: it flags the file rather
// than the individual function.

var exportedFuncRe = compileRegexp(
	`^func\s+[A-Z]\w*\s*\(`,
)

type missingTestRule struct{}

func (r *missingTestRule) ID() string               { return "TEST-001" }
func (r *missingTestRule) Category() types.Category { return types.CategoryMissingTest }

func (r *missingTestRule) Evaluate(fc *diff.FileChange) []types.Finding {
	// Only flag Go source files (not _test.go).
	if !strings.HasSuffix(fc.NewPath, ".go") ||
		strings.HasSuffix(fc.NewPath, "_test.go") {
		return nil
	}
	var findings []types.Finding
	for _, li := range addedLines(fc) {
		if exportedFuncRe.MatchString(strings.TrimSpace(li.content)) {
			f := finding(r.ID(), types.SeverityLow, r.Category(),
				fc, li.lineNum,
				"New exported function added without visible test change",
				strings.TrimSpace(li.content),
				"Add a test in the corresponding _test.go file that "+
					"covers the new exported function's behavior and edge cases.",
			)
			f.Confidence = 0.45
			findings = append(findings, f)
			break // one per file is enough
		}
	}
	return findings
}
