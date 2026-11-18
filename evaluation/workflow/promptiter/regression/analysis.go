//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxEvidenceReferences = 8
	maxEvidenceRunes      = 240
)

var (
	authorizationPattern = regexp.MustCompile(
		`(?i)(?:"?authorization"?\s*[:=]\s*"?(?:bearer\s+)?)[^",\s;}\]]+`,
	)
	secretFieldPattern = regexp.MustCompile(
		`(?i)(?:"?(?:api[_-]?key|access[_-]?token|token|password|secret)"?\s*[:=]\s*["']?)[^"',\s;}\]]+`,
	)
	openAIKeyPattern = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`)
)

func comparisonEpsilon(value float64) (float64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errorsf("epsilon must be finite")
	}
	if value < 0 {
		return 0, errorsf("epsilon must be non-negative")
	}
	if value == 0 {
		return DefaultEpsilon, nil
	}
	return value, nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func normalizeResultStatus(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func isComparableResultStatus(value string) bool {
	switch normalizeResultStatus(value) {
	case "passed", "failed":
		return true
	default:
		return false
	}
}

func statusMatchesPassed(value string, passed bool) bool {
	status := normalizeResultStatus(value)
	return status == "passed" && passed || status == "failed" && !passed
}

func isValidDirection(direction ScoreDirection) bool {
	return direction == ScoreHigherIsBetter || direction == ScoreLowerIsBetter
}

func orientedDelta(raw float64, direction ScoreDirection) float64 {
	if direction == ScoreLowerIsBetter {
		return -raw
	}
	return raw
}

func makeEvidence(id, kind, summary string) EvidenceReference {
	return EvidenceReference{
		ID:      id,
		Kind:    kind,
		Summary: boundAndRedact(summary),
	}
}

func appendEvidence(
	items []EvidenceReference,
	additions ...EvidenceReference,
) []EvidenceReference {
	if len(items) >= maxEvidenceReferences {
		return items
	}
	seen := make(map[string]struct{}, len(items)+len(additions))
	for _, item := range items {
		seen[item.ID+"\x00"+item.Kind+"\x00"+item.Summary] = struct{}{}
	}
	for _, item := range additions {
		if len(items) >= maxEvidenceReferences {
			break
		}
		item.ID = strings.TrimSpace(item.ID)
		item.Kind = strings.TrimSpace(item.Kind)
		item.Summary = boundAndRedact(item.Summary)
		if item.ID == "" || item.Kind == "" || item.Summary == "" {
			continue
		}
		key := item.ID + "\x00" + item.Kind + "\x00" + item.Summary
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, item)
	}
	return items
}

func boundAndRedact(value string) string {
	value = strings.TrimSpace(value)
	value = authorizationPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	value = secretFieldPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	value = openAIKeyPattern.ReplaceAllString(value, "[REDACTED]")
	if utf8.RuneCountInString(value) <= maxEvidenceRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxEvidenceRunes]) + "…"
}

func errorsf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
