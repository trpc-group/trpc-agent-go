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

// --- SENS-001: Credentials in log statements ---
//
// Detects log.Printf/logrus calls that reference password, token,
// secret, or key variables.

var logCredentialsRe = compileRegexp(
	`(?i)(?:log\.(?:Printf|Println|Print|Fatal|Panic)|logrus\.|zap\.|slog\.)\s*\(.*(?:password|passwd|secret|token|apiKey|api_key|credential|privateKey)`,
)

type logCredentialsRule struct{}

func (r *logCredentialsRule) ID() string               { return "SENS-001" }
func (r *logCredentialsRule) Category() types.Category { return types.CategorySensitiveLeak }

func (r *logCredentialsRule) Evaluate(fc *diff.FileChange) []types.Finding {
	var findings []types.Finding
	for _, li := range addedLines(fc) {
		if logCredentialsRe.MatchString(li.content) {
			f := finding(r.ID(), types.SeverityHigh, r.Category(),
				fc, li.lineNum,
				"Sensitive data may be written to logs",
				strings.TrimSpace(li.content),
				"Redact or mask sensitive fields before logging. Use a "+
					"dedicated secret type that implements Stringer safely.",
			)
			f.Confidence = 0.8
			findings = append(findings, f)
		}
	}
	return findings
}
