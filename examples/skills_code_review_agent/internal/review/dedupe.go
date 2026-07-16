//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

func DedupeFindings(in []Finding) []Finding {
	best := map[string]Finding{}
	for _, f := range in {
		f.Fingerprint = findingFingerprint(f)
		key := fmt.Sprintf("%s:%d:%s", f.File, f.Line, f.Category)
		prev, ok := best[key]
		if !ok || betterFinding(f, prev) {
			best[key] = f
		}
	}
	out := make([]Finding, 0, len(best))
	for _, f := range best {
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

func findingFingerprint(f Finding) string {
	key := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%s:%d:%s", f.File, f.Line, f.Category)))
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func betterFinding(a, b Finding) bool {
	if severityRank(a.Severity) != severityRank(b.Severity) {
		return severityRank(a.Severity) > severityRank(b.Severity)
	}
	return a.Confidence > b.Confidence
}

func severityRank(s Severity) int {
	switch s {
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
