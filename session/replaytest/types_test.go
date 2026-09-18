//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"testing"
	"time"
)

func TestCapabilitySetReportsSupportAndMissingCapabilities(t *testing.T) {
	set := CapabilitySet{CapabilitySession: true, CapabilitySummary: true}
	if !set.Supports(CapabilitySession, CapabilitySummary) {
		t.Fatal("CapabilitySet.Supports() = false for supported capabilities")
	}
	if set.Supports(CapabilityMemory) {
		t.Fatal("CapabilitySet.Supports() = true for missing capability")
	}
	missing := set.Missing(CapabilitySession, CapabilityMemory, CapabilityTrack)
	if len(missing) != 2 || missing[0] != CapabilityMemory || missing[1] != CapabilityTrack {
		t.Fatalf("CapabilitySet.Missing() = %v", missing)
	}
}

func TestReportHasUnexpectedDifferences(t *testing.T) {
	allowed := Report{Differences: []Difference{{AllowedDiff: true}}}
	if allowed.HasUnexpectedDifferences() {
		t.Fatal("allowed report has unexpected differences")
	}
	allowed.Differences = append(allowed.Differences, Difference{AllowedDiff: false})
	if !allowed.HasUnexpectedDifferences() {
		t.Fatal("unexpected report was accepted")
	}
	if DefaultCompareOptions().ScoreTolerance != defaultScoreTolerance {
		t.Fatalf("DefaultCompareOptions() = %#v", DefaultCompareOptions())
	}
	if DefaultCompareOptions().DurationTolerance != time.Millisecond {
		t.Fatalf("DefaultCompareOptions() = %#v", DefaultCompareOptions())
	}
}
