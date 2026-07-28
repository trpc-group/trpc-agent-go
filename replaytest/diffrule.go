//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"math"
	"strings"
)

// DiffRule classifies an expected difference between backends.
//
// Rules can be defined at three levels:
//  1. Global — applied to all specs (framework default).
//  2. Per-spec — in Spec.AllowedDiffs.
//  3. Per-backend-pair — via DiffRule.Backends.
type DiffRule struct {
	// Path is a JSONPath-like glob pattern identifying affected fields.
	// Examples: "$.events[*].id", "$.state[*]", "$.summaries[*].summary".
	Path string `json:"path"`

	// Kind classifies the difference for reporting.
	// Common values: "auto_id", "timestamp_drift", "float_precision",
	// "backend_metadata", "unsupported_feature", "json_field_order".
	Kind string `json:"kind"`

	// Strategy determines how the diff is handled:
	//   "ignore"         — the diff is suppressed entirely.
	//   "allow_drift"    — the diff is allowed within MaxDrift.
	//   "allow_extra_keys"   — extra keys in the right snapshot are allowed.
	//   "allow_missing_keys" — missing keys in the right snapshot are allowed.
	Strategy string `json:"strategy"`

	// Backends optionally limits the rule to comparisons involving these
	// specific backends. When empty, the rule applies to all backends.
	Backends []string `json:"backends,omitempty"`

	// MaxDrift specifies numeric tolerances (only used with Strategy "allow_drift").
	MaxDrift *DriftSpec `json:"max_drift,omitempty"`

	// Note is a human-readable justification for the diff.
	Note string `json:"note,omitempty"`
}

// DriftSpec defines numeric tolerances for allowed drift.
type DriftSpec struct {
	// DurationMS is the maximum allowed timestamp drift in milliseconds.
	DurationMS int `json:"duration_ms,omitempty"`
	// FloatEpsilon is the maximum allowed float difference.
	FloatEpsilon float64 `json:"float_epsilon,omitempty"`
	// DurationNS is the maximum allowed time.Duration drift in nanoseconds.
	DurationNS float64 `json:"duration_ns,omitempty"`
}

// MatchPath reports whether the rule's Path pattern matches the diff path.
func (r *DiffRule) MatchPath(diffPath string) bool {
	if r.Path == diffPath {
		return true
	}
	return matchPathPattern(r.Path, diffPath)
}

// matchPathPattern does segment-by-segment glob matching.
// [*] in the pattern matches exactly one path segment in the target.
// A segment is delimited by '.' or '[' / ']' boundaries.
// $.state[*] matches $.state.a but not $.state.a.b.
func matchPathPattern(pattern, target string) bool {
	patSegs := tokenizePath(pattern)
	tgtSegs := tokenizePath(target)
	if len(patSegs) != len(tgtSegs) {
		return false
	}
	for i, ps := range patSegs {
		if ps == "*" {
			continue
		}
		if ps != tgtSegs[i] {
			return false
		}
	}
	return true
}

// tokenizePath splits a JSONPath-like string into segments.
// "$.events[3].id"   → ["$", "events", "3", "id"]
// "$.events[*].id"   → ["$", "events", "*", "id"]
// "$.memories.[x].y" → ["$", "memories", "x", "y"]
func tokenizePath(s string) []string {
	var segs []string
	cur := strings.Builder{}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch ch {
		case '.':
			if cur.Len() > 0 {
				segs = append(segs, cur.String())
				cur.Reset()
			}
		case '[':
			if cur.Len() > 0 {
				segs = append(segs, cur.String())
				cur.Reset()
			}
			// Collect content inside [ ... ] as one segment.
			j := i + 1
			for j < len(s) && s[j] != ']' {
				j++
			}
			inner := s[i+1 : j]
			// Strip quotes if present: ["key"] → key
			if len(inner) >= 2 && inner[0] == '"' && inner[len(inner)-1] == '"' {
				inner = inner[1 : len(inner)-1]
			}
			segs = append(segs, inner)
			i = j // loop will advance past ']'
		case ']':
			// closing bracket: start new empty segment
		case '*':
			// Preserve [*] wildcard as a segment.
			if cur.Len() > 0 {
				segs = append(segs, cur.String())
				cur.Reset()
			}
			segs = append(segs, "*")
			// skip past the closing ']' if present
			if i+1 < len(s) && s[i+1] == ']' {
				i++
			}
		default:
			cur.WriteByte(ch)
		}
	}
	if cur.Len() > 0 {
		segs = append(segs, cur.String())
	}
	return segs
}

// MatchBackend reports whether the rule applies to the given backend name.
func (r *DiffRule) MatchBackend(backendName string) bool {
	if len(r.Backends) == 0 {
		return true
	}
	for _, b := range r.Backends {
		if b == backendName {
			return true
		}
	}
	return false
}

// IsDriftAllowed checks whether a numeric drift is within tolerance.
func (d *DriftSpec) IsDriftAllowed(kind string, drift float64) bool {
	if d == nil {
		return false
	}
	switch kind {
	case "timestamp_drift":
		if d.DurationMS > 0 && drift <= float64(d.DurationMS) {
			return true
		}
		if d.DurationNS > 0 && drift <= d.DurationNS {
			return true
		}
	case "float_precision":
		if d.FloatEpsilon > 0 && drift <= d.FloatEpsilon {
			return true
		}
	case "duration_drift":
		if d.DurationNS > 0 && drift <= d.DurationNS {
			return true
		}
	}
	return false
}

// DefaultDiffRules returns the set of built-in diff rules that apply to
// all specs. These handle auto-generated IDs, timestamps, and backend
// metadata that are known to differ across implementations.
func DefaultDiffRules() []DiffRule {
	return []DiffRule{
		{
			Path:     "$.events[*].id",
			Kind:     "auto_id",
			Strategy: "ignore",
			Note:     "Event IDs are auto-generated and differ per backend.",
		},
		{
			Path:     "$.events[*].timestamp",
			Kind:     "timestamp_drift",
			Strategy: "allow_drift",
			MaxDrift: &DriftSpec{DurationMS: 5000},
			Note:     "Timestamps may drift slightly across backend writes.",
		},
		{
			Path:     "$.events[*].requestID",
			Kind:     "auto_id",
			Strategy: "ignore",
			Note:     "Request IDs are auto-generated.",
		},
		{
			Path:     "$.events[*].invocationId",
			Kind:     "auto_id",
			Strategy: "ignore",
			Note:     "Invocation IDs are auto-generated.",
		},
		{
			Path:     "$.events[*].parentInvocationId",
			Kind:     "auto_id",
			Strategy: "ignore",
			Note:     "Parent invocation IDs are auto-generated.",
		},
		{
			Path:     "$.events[*].usage[*]",
			Kind:     "timing_metadata",
			Strategy: "ignore",
			Note:     "Usage/timing fields are backend-specific or non-deterministic.",
		},
		{
			Path:     "$.events[*].response.id",
			Kind:     "auto_id",
			Strategy: "ignore",
			Note:     "Response IDs are auto-generated per call.",
		},
		{
			Path:     "$.tracks[*].events[*].timestamp",
			Kind:     "timestamp_drift",
			Strategy: "allow_drift",
			MaxDrift: &DriftSpec{DurationMS: 5000},
			Note:     "Track event timestamps drift across backends.",
		},
		{
			Path:     "$.memories[*].memory.eventTime",
			Kind:     "timestamp_drift",
			Strategy: "allow_drift",
			MaxDrift: &DriftSpec{DurationMS: 5000},
			Note:     "Memory eventTime may drift after normalization.",
		},
		{
			Path:     "$.memories[*].score",
			Kind:     "float_precision",
			Strategy: "allow_drift",
			MaxDrift: &DriftSpec{FloatEpsilon: 1e-4},
			Note:     "Memory search scores may differ by floating epsilon.",
		},
	}
}

// MergeDiffRules combines global rules with spec-level rules. Spec-level
// rules take precedence when they share the same path.
func MergeDiffRules(global, spec []DiffRule) []DiffRule {
	if len(spec) == 0 {
		return global
	}
	seen := make(map[string]int)
	merged := make([]DiffRule, 0, len(global)+len(spec))
	for _, r := range spec {
		seen[r.Path] = len(merged)
		merged = append(merged, r)
	}
	for _, r := range global {
		if _, exists := seen[r.Path]; !exists {
			merged = append(merged, r)
		}
	}
	return merged
}

// floatEqual checks two floats for approximate equality within an epsilon.
func floatEqual(a, b, epsilon float64) bool {
	if math.IsNaN(a) && math.IsNaN(b) {
		return true
	}
	if math.IsInf(a, 0) && math.IsInf(b, 0) {
		return math.Signbit(a) == math.Signbit(b)
	}
	return math.Abs(a-b) <= epsilon
}
