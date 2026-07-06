//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// Config controls confidence routing and deterministic finding cleanup.
type Config struct {
	HighConfidenceThreshold   float64
	ReviewConfidenceThreshold float64
}

// DefaultConfig returns conservative review routing thresholds.
func DefaultConfig() Config {
	return Config{
		HighConfidenceThreshold:   0.85,
		ReviewConfidenceThreshold: 0.55,
	}
}

// NormalizeFindings redacts, routes, deduplicates, and sorts findings.
func NormalizeFindings(findings []Finding, cfg Config) []Finding {
	if cfg.HighConfidenceThreshold == 0 {
		cfg = DefaultConfig()
	}
	best := make(map[string]Finding)
	for _, finding := range findings {
		finding.Status = routeStatus(finding, cfg)
		finding.Fingerprint = Fingerprint(finding)
		existing, ok := best[finding.Fingerprint]
		if !ok || findingRank(finding) > findingRank(existing) {
			best[finding.Fingerprint] = finding
		}
	}
	out := make([]Finding, 0, len(best))
	for _, finding := range best {
		out = append(out, finding)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		if out[i].RuleID != out[j].RuleID {
			return out[i].RuleID < out[j].RuleID
		}
		return out[i].Title < out[j].Title
	})
	return out
}

// Fingerprint returns the stable duplicate key requested by the issue.
func Fingerprint(f Finding) string {
	key := fmt.Sprintf("%s:%d:%s:%s", f.File, f.Line, f.Category, f.RuleID)
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func routeStatus(f Finding, cfg Config) string {
	if f.Confidence >= cfg.HighConfidenceThreshold {
		return FindingStatusFinding
	}
	if isSevere(f.Severity) && f.Confidence >= cfg.ReviewConfidenceThreshold {
		return FindingStatusNeedsHumanReview
	}
	return FindingStatusWarning
}

func isSevere(severity string) bool {
	return severity == SeverityCritical || severity == SeverityHigh
}

func findingRank(f Finding) int {
	return severityRank(f.Severity)*1000 + int(f.Confidence*100)
}

func severityRank(severity string) int {
	switch severity {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	default:
		return 0
	}
}
