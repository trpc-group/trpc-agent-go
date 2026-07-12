//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package findings

import (
	"fmt"
	"sort"
)

const confidenceThreshold = 0.6

// Dedup removes duplicate findings keyed by file, line, and category.
func Dedup(items []Finding) []Finding {
	seen := make(map[string]struct{}, len(items))
	out := make([]Finding, 0, len(items))
	for _, f := range items {
		key := fmt.Sprintf("%s:%d:%s", f.File, f.Line, f.Category)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].RuleID < out[j].RuleID
	})
	return out
}

// Partition splits findings by confidence threshold.
// 分组
func Partition(items []Finding) (confirmed, warnings []Finding) {
	for _, f := range items {
		if f.Confidence < confidenceThreshold {
			warnings = append(warnings, f)
			continue
		}
		confirmed = append(confirmed, f)
	}
	return confirmed, warnings
}

// Merge concatenates finding slices from multiple sources.
func Merge(slices ...[]Finding) []Finding {
	total := 0
	for _, s := range slices {
		total += len(s)
	}
	out := make([]Finding, 0, total)
	for _, s := range slices {
		out = append(out, s...)
	}
	return out
}

// BuildMetrics computes review metrics from confirmed findings and warnings.
func BuildMetrics(confirmed, warnings []Finding, durationMs int) ReviewMetrics {
	counts := make(map[string]int)
	for _, f := range confirmed {
		counts[f.Severity]++
	}
	return ReviewMetrics{
		TotalDurationMs: durationMs,
		FindingCount:    len(confirmed),
		WarningCount:    len(warnings),
		SeverityCounts:  counts,
	}
}
