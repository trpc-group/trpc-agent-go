//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redact

import (
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/findings"
)

// RedactFindings returns copies with sensitive evidence redacted.
func RedactFindings(items []findings.Finding) []findings.Finding {
	out := make([]findings.Finding, len(items))
	for i, f := range items {
		f.Evidence = RedactString(f.Evidence)
		f.Title = RedactString(f.Title)
		f.Recommendation = RedactString(f.Recommendation)
		out[i] = f
	}
	return out
}
