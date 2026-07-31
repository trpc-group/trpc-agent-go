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

// --- DB-001: Transaction without Rollback ---
//
// Detects sql.Begin/BeginTx without a corresponding defer
// tx.Rollback() in the next few lines. The absence of a defer
// Rollback means the transaction may be left open on error paths,
// holding locks and connection-pool slots.

var txBeginRe = compileRegexp(
	`(?:db\.Begin|\.BeginTx)\s*\(`,
)

type transactionNotRolledBackRule struct{}

func (r *transactionNotRolledBackRule) ID() string { return "DB-001" }
func (r *transactionNotRolledBackRule) Category() types.Category {
	return types.CategoryDBLifecycle
}

func (r *transactionNotRolledBackRule) Evaluate(fc *diff.FileChange) []types.Finding {
	var findings []types.Finding
	// Check both context and added lines for tx.Begin.
	lines := allLines(fc)
	added := addedLines(fc)
	for i, li := range lines {
		if !txBeginRe.MatchString(li.content) {
			continue
		}
		// Look ahead for defer tx.Rollback() in the next 5 lines.
		hasRollback := false
		end := i + 5
		if end > len(lines) {
			end = len(lines)
		}
		for j := i + 1; j < end; j++ {
			content := strings.TrimSpace(lines[j].content)
			// Skip comment lines — they may mention "defer Rollback"
			// without actually implementing it.
			if strings.HasPrefix(content, "//") {
				continue
			}
			if strings.Contains(content, "defer") &&
				strings.Contains(content, "Rollback") {
				hasRollback = true
				break
			}
		}
		if !hasRollback {
			// Report on the Begin line, whether it's a new addition or
			// existing context where the safety was never present.
			f := finding(r.ID(), types.SeverityHigh, r.Category(),
				fc, li.lineNum,
				"Database transaction without defer Rollback()",
				strings.TrimSpace(li.content),
				"Add defer tx.Rollback() immediately after Begin to "+
					"ensure the transaction is rolled back on error paths. "+
					"A rolled-back committed transaction is a no-op.",
			)
			f.Confidence = 0.8
			if !isAddedLineDB(li, added) {
				f.Confidence = 0.65
			}
			findings = append(findings, f)
		}
	}
	return findings
}

func isAddedLineDB(target lineInfo, added []lineInfo) bool {
	for _, a := range added {
		if a.lineNum == target.lineNum && a.content == target.content {
			return true
		}
	}
	return false
}
