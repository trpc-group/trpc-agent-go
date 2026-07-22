//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package analysis

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
)

const findingThreshold = 0.80

var severityRank = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
var bucketRank = map[reviewmodel.Bucket]int{
	reviewmodel.BucketFindings: 0, reviewmodel.BucketWarnings: 1, reviewmodel.BucketHumanReview: 2,
}

// Findings applies confidence policy, strict dedup, redaction, and stable order.
func Findings(candidates []reviewmodel.Finding) []reviewmodel.Finding {
	merged := make(map[string]reviewmodel.Finding, len(candidates))
	for _, candidate := range candidates {
		candidate = sanitize(candidate)
		candidate.Bucket = chooseBucket(candidate)
		key := dedupKey(candidate)
		if existing, ok := merged[key]; ok {
			merged[key] = merge(existing, candidate)
			continue
		}
		merged[key] = candidate
	}
	result := make([]reviewmodel.Finding, 0, len(merged))
	for _, finding := range merged {
		result = append(result, finding)
	}
	sort.Slice(result, func(left, right int) bool { return less(result[left], result[right]) })
	return result
}

func chooseBucket(finding reviewmodel.Finding) reviewmodel.Bucket {
	if finding.Bucket == reviewmodel.BucketHumanReview {
		return finding.Bucket
	}
	if finding.Confidence >= findingThreshold {
		return reviewmodel.BucketFindings
	}
	return reviewmodel.BucketWarnings
}

func dedupKey(finding reviewmodel.Finding) string {
	path := filepath.ToSlash(filepath.Clean(finding.File))
	return fmt.Sprintf("%s:%d:%s", strings.ToLower(path), finding.Line, strings.ToLower(finding.Category))
}

func merge(left, right reviewmodel.Finding) reviewmodel.Finding {
	best, other := left, right
	if right.Confidence > left.Confidence {
		best, other = right, left
	}
	best.RuleID = joinUnique(best.RuleID, other.RuleID)
	best.Source = joinUnique(best.Source, other.Source)
	best.Bucket = chooseBucket(best)
	return best
}

func joinUnique(left, right string) string {
	values := make(map[string]struct{})
	for _, value := range strings.Split(left+","+right, ",") {
		value = strings.TrimSpace(value)
		if value != "" {
			values[value] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(values))
	for value := range values {
		ordered = append(ordered, value)
	}
	sort.Strings(ordered)
	return strings.Join(ordered, ",")
}

func sanitize(finding reviewmodel.Finding) reviewmodel.Finding {
	finding.File = filepath.ToSlash(filepath.Clean(finding.File))
	finding.Title = redact.String(finding.Title)
	finding.Evidence = redact.String(finding.Evidence)
	finding.Recommendation = redact.String(finding.Recommendation)
	finding.Source = redact.String(finding.Source)
	return finding
}

func less(left, right reviewmodel.Finding) bool {
	if bucketRank[left.Bucket] != bucketRank[right.Bucket] {
		return bucketRank[left.Bucket] < bucketRank[right.Bucket]
	}
	if severityRank[left.Severity] != severityRank[right.Severity] {
		return severityRank[left.Severity] < severityRank[right.Severity]
	}
	if left.File != right.File {
		return left.File < right.File
	}
	if left.Line != right.Line {
		return left.Line < right.Line
	}
	return left.RuleID < right.RuleID
}
