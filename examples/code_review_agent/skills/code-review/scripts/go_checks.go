//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package scripts

import (
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/rules"
)

// GoChecks parses a unified diff and returns deterministic review findings.
// It is the Skill script helper for Go-specific checks.
func GoChecks(diff string) ([]review.Finding, error) {
	files, err := diffparse.Parse(diff)
	if err != nil {
		return nil, err
	}
	return rules.Evaluate(files), nil
}
