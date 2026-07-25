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

// matchPathPattern does simple glob matching for path patterns.
// Supports [*] as a wildcard for array indices and map keys.
func matchPathPattern(pattern, target string) bool {
	patternParts := strings.Split(pattern, "[*]")
	if len(patternParts) == 1 {
		return pattern == target
	}

	if !strings.HasPrefix(target, patternParts[0]) {
		return false
	}

	remaining := strings.TrimPrefix(target, patternParts[0])

	for i := 1; i < len(patternParts); i++ {
		part := patternParts[i]
		if part == "" {
			return true
		}
		idx := strings.Index(remaining, part)
		if idx < 0 {
			return false
		}
		remaining = remaining[idx+len(part):]
	}
	return remaining == ""
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
			Path:     "$.summaries[*].updated_at",
			Kind:     "timestamp_drift",
			Strategy: "allow_drift",
			MaxDrift: &DriftSpec{DurationMS: 5000},
			Note:     "Summary update timestamps drift across backends.",
		},
		{
			Path:     "$.tracks[*].events[*].timestamp",
			Kind:     "timestamp_drift",
			Strategy: "allow_drift",
			MaxDrift: &DriftSpec{DurationMS: 5000},
			Note:     "Track event timestamps drift across backends.",
		},
		{
			Path:     "$.memories[*].created_at",
			Kind:     "timestamp_drift",
			Strategy: "allow_drift",
			MaxDrift: &DriftSpec{DurationMS: 5000},
			Note:     "Memory creation timestamps drift across backends.",
		},
		{
			Path:     "$.memories[*].updated_at",
			Kind:     "timestamp_drift",
			Strategy: "allow_drift",
			MaxDrift: &DriftSpec{DurationMS: 5000},
			Note:     "Memory update timestamps drift across backends.",
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
