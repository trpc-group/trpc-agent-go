//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package dedup implements the DedupEngine GraphAgent node.
// Merges findings from RuleEngine and LLMAnalyzer, deduplicates by
// confidence-based selection, and splits into findings / warnings.
package dedup

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/state"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/types"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// dedupKey is the (file, line, category) triple used for deduplication.
type dedupKey struct {
	File     string
	Line     int
	Category string
}

// Run is the DedupEngine GraphAgent node.
// Reads rule_findings and llm_findings from state, writes findings and warnings.
func Run(ctx context.Context, gs graph.State) (any, error) {
	start := time.Now()
	defer func() { gs[state.StateKeyNodeDedupEngineMs] = time.Since(start).Milliseconds() }()

	ruleFindings, _ := gs[state.StateKeyRuleFindings].([]types.Finding)
	llmFindings, _ := gs[state.StateKeyLLMFindings].([]types.Finding)
	cfg, _ := gs[state.StateKeyDedupConfig].(types.DedupConfig)

	threshold := cfg.ConfidenceThreshold
	if threshold == 0 {
		threshold = 0.6
	}

	// Phase 1: Merge — keep higher confidence per (file, line, category)
	merged := make(map[dedupKey]types.Finding)
	addFindings(merged, ruleFindings)
	addFindings(merged, llmFindings)

	// Phase 2: Split by confidence threshold
	var findings, warnings []types.Finding
	for _, f := range merged {
		if f.Confidence >= threshold {
			findings = append(findings, f)
		} else {
			f.Severity = "warning"
			warnings = append(warnings, f)
		}
	}

	// Phase 3: Apply caps
	maxPerFile := cfg.MaxFindingsPerFile
	if maxPerFile == 0 {
		maxPerFile = 20
	}
	maxTotal := cfg.MaxTotalFindings
	if maxTotal == 0 {
		maxTotal = 100
	}
	findings = applyCaps(findings, maxPerFile, maxTotal)

	gs[state.StateKeyFindings] = findings
	gs[state.StateKeyWarnings] = warnings
	return gs, nil
}

func addFindings(merged map[dedupKey]types.Finding, incoming []types.Finding) {
	for _, f := range incoming {
		key := dedupKey{File: f.File, Line: f.Line, Category: f.Category}
		existing, ok := merged[key]
		if !ok || f.Confidence > existing.Confidence {
			if f.ID == "" {
				f.ID = uuid.New().String()
			}
			merged[key] = f
		}
	}
}

func applyCaps(findings []types.Finding, maxPerFile, maxTotal int) []types.Finding {
	// Sort by severity so we keep the most severe when capping
	sortBySeverity(findings)

	if len(findings) > maxTotal {
		findings = findings[:maxTotal]
	}

	// Per-file cap
	fileCounts := make(map[string]int)
	var capped []types.Finding
	for _, f := range findings {
		fileCounts[f.File]++
		if fileCounts[f.File] <= maxPerFile {
			capped = append(capped, f)
		}
	}
	return capped
}

func sortBySeverity(findings []types.Finding) {
	rank := map[string]int{
		"critical": 0, "high": 1, "medium": 2, "low": 3, "warning": 4,
	}
	for i := 0; i < len(findings); i++ {
		for j := i + 1; j < len(findings); j++ {
			if rank[findings[i].Severity] > rank[findings[j].Severity] {
				findings[i], findings[j] = findings[j], findings[i]
			}
		}
	}
	_ = fmt.Sprintf("") // suppress unused import if needed
}
