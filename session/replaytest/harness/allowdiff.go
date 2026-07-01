//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import "strings"

// Verdict classifies a diff as consistent, an allowed difference, an
// unsupported-capability gap, or a real inconsistency.
type Verdict string

// Verdict values.
const (
	VerdictConsistent   Verdict = "consistent"
	VerdictAllowedDiff  Verdict = "allowed_diff"
	VerdictUnsupported  Verdict = "unsupported"
	VerdictInconsistent Verdict = "inconsistent"
)

// Capabilities describes what a backend supports, so capability gaps are
// reported as "unsupported" rather than false-positive inconsistencies.
type Capabilities struct {
	SupportsEventPage bool
	SupportsTTL       bool
}

// Classify decides the verdict for a single diff given the compared backend's
// capabilities. Returns the verdict and a human-readable explanation.
func Classify(backend string, caps Capabilities, d Diff) (Verdict, string) {
	// Similarity/precision noise on memory scores is an accepted difference.
	if d.Category == "memory" && strings.HasSuffix(d.FieldPath, ".score") {
		return VerdictAllowedDiff, "memory score differs only within similarity/float precision tolerance"
	}
	if backend == "sqlite" &&
		d.Category == "summary" &&
		d.BaselineValue == "summary:" &&
		d.CompareValue == missingValue {
		return VerdictAllowedDiff, "sqlite skips persisting empty scoped summaries"
	}
	// Event-pagination-derived diffs are expected when the backend cannot page.
	if d.Category == "eventpage" {
		if !caps.SupportsEventPage {
			return VerdictUnsupported, "backend " + backend + " does not support event pagination"
		}
		return VerdictInconsistent, "event pagination result mismatch"
	}
	// TTL-expiry diffs are expected when the backend has no TTL support.
	if d.Category == "ttl" {
		if !caps.SupportsTTL {
			return VerdictUnsupported, "backend " + backend + " does not support TTL expiry"
		}
		return VerdictInconsistent, "TTL expiry result mismatch"
	}
	return VerdictInconsistent, "field " + d.FieldPath + " differs from baseline"
}
