//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolfilter

import (
	"unicode/utf8"
)

// Report catalogs.
const (
	// CatalogTool identifies a tool-filtering report.
	CatalogTool = "tool"
	// CatalogSkill identifies a skill-filtering report.
	CatalogSkill = "skill"
)

// Report outcomes.
const (
	// OutcomeUntouched means the catalog was left unchanged: below
	// minToolCount, no scoreable candidates, no user message, lexical
	// zero overlap, all candidates retained, or a cached untouched result.
	OutcomeUntouched = "untouched"
	// OutcomeNarrowed means tools or skills were removed from the catalog.
	OutcomeNarrowed = "narrowed"
	// OutcomeFailOpen means the decision call failed and the full
	// catalog was kept.
	OutcomeFailOpen = "fail_open"
)

// Report describes the outcome of one tool- or skill-filtering decision for
// observability. Cached decision replays are not reported again. Tool and
// skill decisions use the invocation ID as TurnKey when an invocation exists.
type Report struct {
	// Catalog identifies the filtered catalog as CatalogTool or CatalogSkill.
	// Its zero value is unspecified.
	Catalog string
	// Outcome is one of OutcomeUntouched, OutcomeNarrowed or
	// OutcomeFailOpen.
	Outcome string
	// TurnKey identifies the invocation; it may be empty for untouched reports.
	TurnKey string
	// Reason explains an untouched or fail-open outcome.
	Reason string
	// Asked lists the candidate names that were scored in this turn.
	Asked []string
	// Unasked lists candidates beyond the question budget; they are kept
	// without a score.
	Unasked []string
	// Dropped lists the removed candidate names (empty unless narrowed).
	Dropped []string
	// Scores maps each asked candidate name to its P(relevant) score.
	Scores map[string]float64
	// CatalogTokensBefore and CatalogTokensAfter estimate the request
	// token cost of the tool catalog (or skill overview) before and after
	// filtering, and SavedTokens their difference. The estimate is one
	// fourth of the rune counts of names and descriptions — a heuristic
	// for observability, not billing.
	CatalogTokensBefore int
	CatalogTokensAfter  int
	SavedTokens         int
}

// estimateToolTokens approximates the request cost of one tool catalog
// entry. The divisor 4 mirrors the common characters-per-token ratio.
func estimateToolTokens(name, description string) int {
	return (utf8.RuneCountInString(name) + utf8.RuneCountInString(description)) / 4
}
