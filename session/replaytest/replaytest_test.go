//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestPublicCases(t *testing.T) {
	cases := PublicCases()
	if len(cases) < 10 {
		t.Fatalf("PublicCases() returned %d cases, want at least 10", len(cases))
	}
	names := make(map[string]struct{}, len(cases))
	faults := make(map[FaultKind]struct{}, len(cases))
	for _, replayCase := range cases {
		if err := validateCase(replayCase); err != nil {
			t.Fatalf("case %q is invalid: %v", replayCase.Name, err)
		}
		if _, ok := names[replayCase.Name]; ok {
			t.Fatalf("duplicate case %q", replayCase.Name)
		}
		names[replayCase.Name] = struct{}{}
		if replayCase.Fault == "" {
			t.Fatalf("case %q has no acceptance fault", replayCase.Name)
		}
		faults[replayCase.Fault] = struct{}{}
	}
	if len(faults) < 10 {
		t.Fatalf("PublicCases() exercise %d distinct faults, want at least 10", len(faults))
	}
}

func TestCaseValidationRejectsAmbiguousInputs(t *testing.T) {
	validStep := messageStep("valid", "valid", 1, "user", "user", "hello", "")
	tests := []Case{
		{
			Name:       "unknown-order",
			Requires:   []Capability{CapabilitySession},
			EventOrder: "unordered",
			Steps:      []Step{validStep},
		},
		{
			Name:     "multiple-payloads",
			Requires: []Capability{CapabilitySession},
			Steps: []Step{{
				Name:  "ambiguous",
				Kind:  StepAppendEvent,
				Event: validStep.Event,
				State: &StateInput{Scope: StateScopeSession},
			}},
		},
		{
			Name:     "session-delete",
			Requires: []Capability{CapabilitySession, CapabilitySessionState},
			Steps: []Step{{
				Name:  "delete",
				Kind:  StepUpdateState,
				State: &StateInput{Scope: StateScopeSession, DeleteKeys: []string{"key"}},
			}},
		},
		{
			Name:     "missing-session-capability",
			Requires: nil,
			Steps:    []Step{validStep},
		},
		{
			Name:     "duplicate-capability",
			Requires: []Capability{CapabilitySession, CapabilitySession},
			Steps:    []Step{validStep},
		},
		{
			Name:     "unknown-capability",
			Requires: []Capability{CapabilitySession, "not-a-capability"},
			Steps:    []Step{validStep},
		},
		{
			Name:     "undeclared-memory-capability",
			Requires: []Capability{CapabilitySession},
			Steps: []Step{{
				Name:   "memory",
				Kind:   StepAddMemory,
				Memory: &MemoryInput{Memory: "value"},
			}},
		},
		{
			Name:     "duplicate-logical-event-id",
			Requires: []Capability{CapabilitySession, CapabilityConcurrent},
			Steps: []Step{{
				Name: "branches",
				Kind: StepConcurrent,
				Concurrent: [][]Step{
					{messageStep("left", "duplicate", 1, "user", "user", "left", "left")},
					{messageStep("right", "duplicate", 2, "user", "user", "right", "right")},
				},
			}},
		},
		{
			Name:     "empty-track-name",
			Requires: []Capability{CapabilitySession, CapabilityTrack},
			Steps: []Step{{
				Name:  "track",
				Kind:  StepAppendTrack,
				Track: &TrackInput{Event: &session.TrackEvent{}},
			}},
		},
	}
	for _, replayCase := range tests {
		if err := validateCase(replayCase); err == nil {
			t.Fatalf("case %q unexpectedly validated", replayCase.Name)
		}
	}
}

func TestRunnerRejectsDuplicateCases(t *testing.T) {
	replayCase := PublicCases()[0]
	left := InMemoryBackend()
	left.Name = "left"
	right := InMemoryBackend()
	right.Name = "right"
	_, err := (Runner{}).Run(
		context.Background(),
		[]Case{replayCase, replayCase},
		[]Backend{left, right},
	)
	if err == nil {
		t.Fatal("Run() unexpectedly accepted duplicate cases")
	}
}

func TestRunnerInMemoryMatrix(t *testing.T) {
	reference := InMemoryBackend()
	reference.Name = "inmemory-reference"
	comparison := InMemoryBackend()
	comparison.Name = "inmemory-comparison"

	started := time.Now()
	report, err := (Runner{Reference: reference.Name}).Run(
		context.Background(),
		PublicCases(),
		[]Backend{reference, comparison},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !report.IsClean() {
		t.Fatalf("Run() produced blocking differences: %+v", report)
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.PassedCases != len(PublicCases()) {
		t.Fatalf("PassedCases = %d, want %d", report.PassedCases, len(PublicCases()))
	}
	if elapsed := time.Since(started); elapsed >= 30*time.Second {
		t.Fatalf("lightweight in-memory matrix took %v, want < 30s", elapsed)
	}
}

func TestConcurrentStepReloadsSessionBeforeSummary(t *testing.T) {
	replayCase := Case{
		Name:       "concurrent_then_summary",
		Requires:   []Capability{CapabilitySession, CapabilitySummary, CapabilityConcurrent},
		EventOrder: EventOrderCausal,
		Steps: []Step{
			messageStep("user", "user", 1, "user", model.RoleUser, "run both branches", ""),
			{
				Name: "parallel-events",
				Kind: StepConcurrent,
				Concurrent: [][]Step{
					{messageStep("branch-a", "branch-a", 2, "assistant", model.RoleAssistant, "alpha", "")},
					{messageStep("branch-b", "branch-b", 3, "assistant", model.RoleAssistant, "beta", "")},
				},
			},
			{Name: "summary", Kind: StepCreateSummary, Summary: &SummaryInput{Force: true}},
		},
	}

	snapshot, err := Replay(context.Background(), replayCase, InMemoryBackend())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	summary, ok := snapshot.Summaries[session.SummaryFilterKeyAllContents]
	if !ok {
		t.Fatal("summary after concurrent step is missing")
	}
	text, ok := summary["text"].(string)
	if !ok || !strings.Contains(text, "alpha") || !strings.Contains(text, "beta") {
		t.Fatalf("summary text = %v, want both concurrent branch events", summary["text"])
	}
}

func TestRunnerConsensusIdentifiesSingleOutlier(t *testing.T) {
	goodA := InMemoryBackend()
	goodA.Name = "good-a"
	goodB := InMemoryBackend()
	goodB.Name = "good-b"
	outlier := eventAuthorDriftBackend("outlier")
	backends := []Backend{outlier, goodB, goodA}

	report, err := (Runner{Mode: ComparisonConsensus}).Run(
		context.Background(),
		[]Case{PublicCases()[0]},
		backends,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if report.ComparisonMode != ComparisonConsensus || report.Reference != "" {
		t.Fatalf("Run() report mode/reference = %q/%q", report.ComparisonMode, report.Reference)
	}
	if report.FailedCases != 1 || len(report.Cases) != 1 {
		t.Fatalf("Run() report counters = %+v", report)
	}
	consensus := report.Cases[0].Consensus
	if consensus == nil {
		t.Fatal("Run() did not emit consensus analysis")
	}
	if consensus.Verdict != ConsensusOutlier || !reflect.DeepEqual(consensus.Outliers, []string{"outlier"}) {
		t.Fatalf("consensus verdict/outliers = %q/%v", consensus.Verdict, consensus.Outliers)
	}
	if !reflect.DeepEqual(consensus.ComparableBackends, []string{"good-a", "good-b", "outlier"}) {
		t.Fatalf("consensus backends = %v", consensus.ComparableBackends)
	}
	if len(consensus.Pairs) != 3 {
		t.Fatalf("consensus pairs = %d, want 3", len(consensus.Pairs))
	}
	for _, pair := range consensus.Pairs {
		if pair.BackendA == "good-a" && pair.BackendB == "good-b" {
			if pair.BlockingDiffs != 0 {
				t.Fatalf("agreeing pair has %d blocking diffs", pair.BlockingDiffs)
			}
			continue
		}
		if pair.BlockingDiffs == 0 {
			t.Fatalf("outlier pair %+v has no blocking diff", pair)
		}
	}

	referenceReport, err := (Runner{Reference: "outlier"}).Run(
		context.Background(),
		[]Case{PublicCases()[0]},
		backends,
	)
	if err != nil {
		t.Fatalf("reference Run() error = %v", err)
	}
	if referenceReport.FailedCases != 1 || referenceReport.Cases[0].Consensus != nil {
		t.Fatalf("reference report = %+v", referenceReport)
	}
	comparedBackends := make(map[string]struct{})
	for _, diff := range referenceReport.Cases[0].Diffs {
		if !diff.Allowed {
			comparedBackends[diff.BackendB] = struct{}{}
		}
	}
	if len(comparedBackends) != 2 {
		t.Fatalf("faulty reference implicated %d backends, want 2", len(comparedBackends))
	}

	report.Cases[0].Consensus.Pairs[0].BlockingDiffs++
	if err := report.Validate(); err == nil {
		t.Fatal("Validate() accepted tampered consensus counters")
	}
}

func TestRunnerConsensusDoesNotGuessWithTwoBackends(t *testing.T) {
	good := InMemoryBackend()
	good.Name = "good"
	outlier := eventAuthorDriftBackend("different")
	report, err := (Runner{Mode: ComparisonConsensus}).Run(
		context.Background(),
		[]Case{PublicCases()[0]},
		[]Backend{good, outlier},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	consensus := report.Cases[0].Consensus
	if consensus == nil || consensus.Verdict != ConsensusAmbiguous || len(consensus.Outliers) != 0 {
		t.Fatalf("two-backend consensus = %+v", consensus)
	}
	consensus.Outliers = []string{}
	if err := report.Validate(); err != nil {
		t.Fatalf("Validate() rejected an empty outliers array: %v", err)
	}
}

func TestRunnerConsensusRejectsReference(t *testing.T) {
	left := InMemoryBackend()
	left.Name = "left"
	right := InMemoryBackend()
	right.Name = "right"
	_, err := (Runner{Mode: ComparisonConsensus, Reference: left.Name}).Run(
		context.Background(),
		[]Case{PublicCases()[0]},
		[]Backend{left, right},
	)
	if err == nil {
		t.Fatal("Run() unexpectedly accepted a consensus reference")
	}
}

func TestRunnerConsensusRecordsExcludedBackendEvidence(t *testing.T) {
	caseUnderTest := PublicCases()[0]
	goodA := InMemoryBackend()
	goodA.Name = "good-a"
	goodB := InMemoryBackend()
	goodB.Name = "good-b"

	t.Run("execution failure", func(t *testing.T) {
		failed := openFailureBackend("failed")
		report, err := (Runner{Mode: ComparisonConsensus}).Run(
			context.Background(),
			[]Case{caseUnderTest},
			[]Backend{goodA, failed, goodB},
		)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if report.Cases[0].Status != StatusFailed || report.Cases[0].Consensus.Verdict != ConsensusUnanimous {
			t.Fatalf("execution failure report = %+v", report)
		}
		if countEvidence(report.Cases[0].Diffs, "failed", "/execution") != 1 {
			t.Fatalf("execution evidence = %+v", report.Cases[0].Diffs)
		}
	})

	t.Run("unsupported capability", func(t *testing.T) {
		unsupported := missingCapabilityBackend("unsupported", CapabilitySession)
		report, err := (Runner{Mode: ComparisonConsensus}).Run(
			context.Background(),
			[]Case{caseUnderTest},
			[]Backend{unsupported, goodB, goodA},
		)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if report.Cases[0].Status != StatusUnsupported || report.Cases[0].Consensus.Verdict != ConsensusUnanimous {
			t.Fatalf("unsupported report = %+v", report)
		}
		if countEvidence(report.Cases[0].Diffs, "unsupported", "/capabilities/session") != 1 {
			t.Fatalf("capability evidence = %+v", report.Cases[0].Diffs)
		}
		report.PassedCases = 1
		report.UnsupportedCases = 0
		report.Cases[0].Status = StatusPassed
		if err := report.Validate(); err == nil {
			t.Fatal("Validate() accepted passed status with capability evidence")
		}
	})

	t.Run("insufficient comparable backends", func(t *testing.T) {
		unsupported := missingCapabilityBackend("unsupported", CapabilitySession)
		report, err := (Runner{Mode: ComparisonConsensus}).Run(
			context.Background(),
			[]Case{caseUnderTest},
			[]Backend{goodA, unsupported},
		)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if report.Cases[0].Status != StatusUnsupported ||
			report.Cases[0].Consensus.Verdict != ConsensusInsufficient {
			t.Fatalf("insufficient report = %+v", report)
		}
	})
}

func TestRunnerReferenceDoesNotDuplicateMissingBaselineEvidence(t *testing.T) {
	caseUnderTest := PublicCases()[0]
	good := InMemoryBackend()
	good.Name = "good"

	t.Run("execution failure", func(t *testing.T) {
		failed := openFailureBackend("failed")
		report, err := (Runner{Reference: failed.Name}).Run(
			context.Background(),
			[]Case{caseUnderTest},
			[]Backend{failed, good},
		)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if report.Cases[0].Status != StatusFailed ||
			countEvidence(report.Cases[0].Diffs, failed.Name, "/execution") != 1 {
			t.Fatalf("reference execution evidence = %+v", report.Cases[0])
		}
	})

	t.Run("unsupported capability", func(t *testing.T) {
		unsupported := missingCapabilityBackend("unsupported", CapabilitySession)
		report, err := (Runner{Reference: unsupported.Name}).Run(
			context.Background(),
			[]Case{caseUnderTest},
			[]Backend{unsupported, good},
		)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if report.Cases[0].Status != StatusUnsupported || report.BlockingDiffs != 0 {
			t.Fatalf("reference unsupported report = %+v", report)
		}
	})
}

func TestClassifyConsensus(t *testing.T) {
	tests := []struct {
		name     string
		backends []string
		pairs    []PairComparison
		verdict  ConsensusVerdict
		outliers []string
	}{
		{
			name:     "insufficient",
			backends: []string{"a"},
			verdict:  ConsensusInsufficient,
		},
		{
			name:     "unanimous",
			backends: []string{"a", "b", "c"},
			pairs: []PairComparison{
				{BackendA: "a", BackendB: "b"},
				{BackendA: "a", BackendB: "c"},
				{BackendA: "b", BackendB: "c"},
			},
			verdict: ConsensusUnanimous,
		},
		{
			name:     "strict outlier",
			backends: []string{"a", "b", "c"},
			pairs: []PairComparison{
				{BackendA: "a", BackendB: "b"},
				{BackendA: "a", BackendB: "c", BlockingDiffs: 1},
				{BackendA: "b", BackendB: "c", BlockingDiffs: 1},
			},
			verdict:  ConsensusOutlier,
			outliers: []string{"c"},
		},
		{
			name:     "two backend disagreement",
			backends: []string{"a", "b"},
			pairs: []PairComparison{
				{BackendA: "a", BackendB: "b", BlockingDiffs: 1},
			},
			verdict: ConsensusAmbiguous,
		},
		{
			name:     "split vote",
			backends: []string{"a", "b", "c", "d"},
			pairs: []PairComparison{
				{BackendA: "a", BackendB: "b"},
				{BackendA: "a", BackendB: "c", BlockingDiffs: 1},
				{BackendA: "a", BackendB: "d", BlockingDiffs: 1},
				{BackendA: "b", BackendB: "c", BlockingDiffs: 1},
				{BackendA: "b", BackendB: "d", BlockingDiffs: 1},
				{BackendA: "c", BackendB: "d"},
			},
			verdict: ConsensusAmbiguous,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verdict, outliers := classifyConsensus(test.backends, test.pairs)
			if verdict != test.verdict || !reflect.DeepEqual(outliers, test.outliers) {
				t.Fatalf("classifyConsensus() = %q/%v, want %q/%v", verdict, outliers, test.verdict, test.outliers)
			}
		})
	}
}

func TestConsensusValidationRequiresBackendExclusionEvidence(t *testing.T) {
	backends := make([]Backend, 3)
	for index, name := range []string{"a", "b", "c"} {
		backends[index] = InMemoryBackend()
		backends[index].Name = name
	}
	report, err := (Runner{Mode: ComparisonConsensus}).Run(
		context.Background(),
		[]Case{PublicCases()[0]},
		backends,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	consensus := report.Cases[0].Consensus
	consensus.ComparableBackends = []string{"a", "b"}
	consensus.Pairs = []PairComparison{{BackendA: "a", BackendB: "b"}}
	if err := report.Validate(); err == nil {
		t.Fatal("Validate() accepted a silently excluded backend")
	}
}

func TestConsensusValidationRejectsReversePairDiff(t *testing.T) {
	left := InMemoryBackend()
	left.Name = "a"
	right := InMemoryBackend()
	right.Name = "b"
	report, err := (Runner{Mode: ComparisonConsensus}).Run(
		context.Background(),
		[]Case{PublicCases()[0]},
		[]Backend{left, right},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	report.PassedCases = 0
	report.FailedCases = 1
	report.BlockingDiffs = 1
	report.Cases[0].Status = StatusFailed
	report.Cases[0].Diffs = append(report.Cases[0].Diffs, Diff{
		Case:        report.Cases[0].Name,
		BackendA:    "b",
		BackendB:    "a",
		SessionID:   report.Cases[0].Name,
		Path:        "/session/id",
		Baseline:    "b",
		Actual:      "a",
		Explanation: "tampered reverse pair",
	})
	if err := report.Validate(); err == nil {
		t.Fatal("Validate() accepted a reverse-direction consensus diff")
	}
}

func TestConsensusValidationRejectsReservedPairEvidence(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		allowed bool
	}{
		{name: "execution", path: "/execution"},
		{name: "capability", path: "/capabilities/session", allowed: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left := InMemoryBackend()
			left.Name = "a"
			right := InMemoryBackend()
			right.Name = "b"
			report, err := (Runner{Mode: ComparisonConsensus}).Run(
				context.Background(),
				[]Case{PublicCases()[0]},
				[]Backend{left, right},
			)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			diff := Diff{
				Case:        report.Cases[0].Name,
				BackendA:    "a",
				BackendB:    "b",
				SessionID:   report.Cases[0].Name,
				Path:        test.path,
				Baseline:    "success",
				Actual:      "tampered",
				Allowed:     test.allowed,
				Explanation: "tampered reserved path",
			}
			report.Cases[0].Diffs = append(report.Cases[0].Diffs, diff)
			if test.allowed {
				report.AllowedDiffs = 1
				report.UnsupportedCases = 1
				report.PassedCases = 0
				report.Cases[0].Status = StatusUnsupported
				report.Cases[0].Consensus.Pairs[0].AllowedDiffs = 1
			} else {
				report.BlockingDiffs = 1
				report.FailedCases = 1
				report.PassedCases = 0
				report.Cases[0].Status = StatusFailed
				report.Cases[0].Consensus.Pairs[0].BlockingDiffs = 1
			}
			if err := report.Validate(); err == nil {
				t.Fatalf("Validate() accepted pairwise %s evidence", test.name)
			}
		})
	}
}

func TestReplayClosesIncompleteServices(t *testing.T) {
	reference := InMemoryBackend()
	cleaned := false
	backend := Backend{
		Name: "incomplete",
		Open: func(ctx context.Context, caseName string) (*Services, error) {
			services, err := reference.Open(ctx, caseName)
			if err != nil {
				return nil, err
			}
			memoryService := services.Memory
			services.Memory = nil
			services.Cleanup = func() error {
				cleaned = true
				return memoryService.Close()
			}
			return services, nil
		},
	}
	if _, err := Replay(context.Background(), memoryCase(), backend); err == nil {
		t.Fatal("Replay() unexpectedly accepted incomplete services")
	}
	if !cleaned {
		t.Fatal("Replay() did not clean up incomplete services")
	}
}

func TestReplayReadsOnlyRequiredCapabilities(t *testing.T) {
	reference := InMemoryBackend()
	unexpectedCall := errors.New("unexpected unsupported capability call")
	backend := Backend{
		Name:         "session-only",
		Capabilities: Capabilities{CapabilitySession: true},
		Open: func(ctx context.Context, caseName string) (*Services, error) {
			services, err := reference.Open(ctx, caseName)
			if err != nil {
				return nil, err
			}
			memoryService := services.Memory
			services.Memory = nil
			services.Session = &unexpectedStateReadService{
				Service: services.Session,
				err:     unexpectedCall,
			}
			services.Cleanup = memoryService.Close
			return services, nil
		},
	}
	snapshot, err := Replay(context.Background(), singleTurnCase(), backend)
	if err != nil {
		t.Fatalf("Replay() called an unrequired capability: %v", err)
	}
	if len(snapshot.Memories) != 0 || len(snapshot.Summaries) != 0 || len(snapshot.Tracks) != 0 {
		t.Fatalf("snapshot contains unrequired domains: %+v", snapshot)
	}
	for scope, state := range snapshot.State {
		if len(state) != 0 {
			t.Fatalf("snapshot %s state = %v, want empty", scope, state)
		}
	}
}

func TestReplayRejectsCrossSessionSummaryLeak(t *testing.T) {
	base := InMemoryBackend()
	backend := base
	backend.Name = "summary-leak"
	backend.Open = func(ctx context.Context, caseName string) (*Services, error) {
		services, err := base.Open(ctx, caseName)
		if err != nil {
			return nil, err
		}
		services.Session = &summaryLeakService{Service: services.Session}
		return services, nil
	}
	if _, err := Replay(context.Background(), summaryUpdateCase(), backend); err == nil {
		t.Fatal("Replay() unexpectedly accepted a cross-session summary leak")
	}
}

func TestEveryPublicCaseDetectsInjectedFault(t *testing.T) {
	for _, replayCase := range PublicCases() {
		replayCase := replayCase
		t.Run(replayCase.Name, func(t *testing.T) {
			t.Parallel()
			backend := InMemoryBackend()
			backend.Name = "baseline"
			baseline, err := Replay(context.Background(), replayCase, backend)
			if err != nil {
				t.Fatalf("Replay() error = %v", err)
			}
			faulted, err := InjectFault(baseline, replayCase.Fault)
			if err != nil {
				t.Fatalf("InjectFault(%q) error = %v", replayCase.Fault, err)
			}
			diffs, err := Compare(replayCase.Name, baseline, faulted, nil)
			if err != nil {
				t.Fatalf("Compare() error = %v", err)
			}
			blocking, _ := countDiffs(diffs)
			if blocking == 0 {
				t.Fatalf("fault %q was not detected", replayCase.Fault)
			}
			for _, diff := range diffs {
				if diff.Case != replayCase.Name || diff.SessionID == "" || diff.Path == "" {
					t.Fatalf("diff lacks required locator: %+v", diff)
				}
			}
		})
	}
}

func TestCompareAllowedDiff(t *testing.T) {
	baseline := minimalSnapshot("baseline", `{"score":1}`)
	actual := minimalSnapshot("actual", `{"score":2}`)
	rules := []AllowedDiff{{
		BackendA: "baseline",
		BackendB: "actual",
		Path:     "/state/session/score",
		Rule:     AllowedIgnore,
		Reason:   "the fixture intentionally demonstrates an allowed backend-private value",
	}}
	diffs, err := Compare("allowed", baseline, actual, rules)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(diffs) != 1 || !diffs[0].Allowed || diffs[0].Explanation != rules[0].Reason {
		t.Fatalf("Compare() diffs = %+v, want one documented allowed diff", diffs)
	}
}

func TestCompareAllowedDiffBackendPairIsUnordered(t *testing.T) {
	baseline := minimalSnapshot("baseline", `{"score":1}`)
	actual := minimalSnapshot("actual", `{"score":2}`)
	baseline.Case = "allowed-reverse"
	actual.Case = "allowed-reverse"
	rules := []AllowedDiff{{
		BackendA: "actual",
		BackendB: "baseline",
		Path:     "/state/session/score",
		Rule:     AllowedIgnore,
		Reason:   "backend pairs are unordered in consensus comparisons",
	}}
	diffs, err := Compare("allowed-reverse", baseline, actual, rules)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(diffs) != 1 || !diffs[0].Allowed {
		t.Fatalf("Compare() diffs = %+v, want one allowed diff", diffs)
	}
}

func TestCompareAllowedDiffBackendWildcardPairIsUnordered(t *testing.T) {
	tests := []struct {
		name     string
		baseline string
		actual   string
		allowed  bool
	}{
		{name: "sqlite first", baseline: "sqlite", actual: "zeta", allowed: true},
		{name: "sqlite second", baseline: "alpha", actual: "sqlite", allowed: true},
		{name: "sqlite absent", baseline: "mysql", actual: "postgres", allowed: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := minimalSnapshot(test.baseline, `{"score":1}`)
			actual := minimalSnapshot(test.actual, `{"score":2}`)
			baseline.Case = test.name
			actual.Case = test.name
			rules := []AllowedDiff{{
				BackendA: "*",
				BackendB: "sqlite",
				Path:     "/state/session/score",
				Rule:     AllowedIgnore,
				Reason:   "SQLite exposes a documented backend-private value",
			}}
			diffs, err := Compare(test.name, baseline, actual, rules)
			if err != nil {
				t.Fatalf("Compare() error = %v", err)
			}
			if len(diffs) != 1 || diffs[0].Allowed != test.allowed {
				t.Fatalf("Compare() diffs = %+v, want allowed=%t", diffs, test.allowed)
			}
		})
	}
}

func TestCompareAllowedDiffRules(t *testing.T) {
	tests := []struct {
		name     string
		baseline any
		actual   any
		rule     AllowedDiff
		allowed  bool
	}{
		{
			name:     "same type",
			baseline: "baseline",
			actual:   "actual",
			rule: AllowedDiff{
				Rule: AllowedSameType,
			},
			allowed: true,
		},
		{
			name:     "within delta",
			baseline: 10.0,
			actual:   10.25,
			rule: AllowedDiff{
				Rule:  AllowedWithinDelta,
				Delta: 0.5,
			},
			allowed: true,
		},
		{
			name:     "outside delta",
			baseline: 10.0,
			actual:   11.0,
			rule: AllowedDiff{
				Rule:  AllowedWithinDelta,
				Delta: 0.5,
			},
		},
		{
			name:     "large integers outside zero delta",
			baseline: int64(9007199254740992),
			actual:   int64(9007199254740993),
			rule: AllowedDiff{
				Rule:  AllowedWithinDelta,
				Delta: 0,
			},
		},
		{
			name:     "large integers within unit delta",
			baseline: int64(9007199254740992),
			actual:   int64(9007199254740993),
			rule: AllowedDiff{
				Rule:  AllowedWithinDelta,
				Delta: 1,
			},
			allowed: true,
		},
		{
			name:     "scientific notation within exact delta",
			baseline: json.Number("1e3"),
			actual:   json.Number("1000.5"),
			rule: AllowedDiff{
				Rule:  AllowedWithinDelta,
				Delta: 0.5,
			},
			allowed: true,
		},
		{
			name:     "excessive exponent is not expanded",
			baseline: json.Number("1e1000000"),
			actual:   json.Number("2e1000000"),
			rule: AllowedDiff{
				Rule:  AllowedWithinDelta,
				Delta: 1,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := minimalSnapshot("baseline", `{}`)
			actual := minimalSnapshot("actual", `{}`)
			baseline.Case = test.name
			actual.Case = test.name
			baseline.Session["private"] = test.baseline
			actual.Session["private"] = test.actual
			test.rule.BackendA = "baseline"
			test.rule.BackendB = "actual"
			test.rule.Path = "/session/private"
			test.rule.Reason = "backend-specific fixture value"
			diffs, err := Compare(test.name, baseline, actual, []AllowedDiff{test.rule})
			if err != nil {
				t.Fatalf("Compare() error = %v", err)
			}
			if len(diffs) != 1 || diffs[0].Allowed != test.allowed {
				t.Fatalf("Compare() diffs = %+v, want allowed=%t", diffs, test.allowed)
			}
		})
	}
}

func TestExactNumberBounds(t *testing.T) {
	for _, value := range []json.Number{
		json.Number(strings.Repeat("9", maxExactNumberCharacters+1)),
		json.Number("1e1025"),
		json.Number("1e-1025"),
		json.Number("1/2"),
	} {
		if _, ok := exactNumber(value); ok {
			t.Fatalf("exactNumber(%q) unexpectedly accepted an unbounded or invalid value", value)
		}
	}
}

func TestAllowedDiffValidation(t *testing.T) {
	tests := []AllowedDiff{
		{BackendA: "a", BackendB: "b", Path: "relative", Rule: AllowedIgnore, Reason: "bad path"},
		{BackendA: "a", BackendB: "b", Path: "/x", Rule: "unknown", Reason: "bad rule"},
		{BackendA: "a", BackendB: "b", Path: "/x", Rule: AllowedIgnore},
		{BackendA: "a", BackendB: "b", Path: "/x", Rule: AllowedWithinDelta, Delta: -1, Reason: "bad delta"},
		{BackendA: "a", BackendB: "b", Path: "/x", Rule: AllowedWithinDelta, Delta: math.NaN(), Reason: "bad delta"},
		{BackendA: "a", BackendB: "b", Path: "/x", Rule: AllowedWithinDelta, Delta: math.Inf(1), Reason: "bad delta"},
	}
	for index, rule := range tests {
		if err := validateAllowedDiffs([]AllowedDiff{rule}); err == nil {
			t.Fatalf("rule %d unexpectedly validated: %+v", index, rule)
		}
	}
}

func TestTrackPayloadValuesRemainSemantic(t *testing.T) {
	left := map[string]any{
		"status":               "ok",
		"duration_ms":          float64(10),
		"expected_duration_ms": float64(20),
		"deadline_at":          "2026-07-01T00:00:00Z",
		"nested": map[string]any{
			"latency": float64(2),
		},
	}
	right := map[string]any{
		"status":               "ok",
		"duration_ms":          float64(500),
		"expected_duration_ms": float64(600),
		"deadline_at":          "2026-07-02T00:00:00Z",
		"nested": map[string]any{
			"latency": float64(9),
		},
	}
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	if string(leftJSON) == string(rightJSON) {
		t.Fatalf("distinct track payloads compare equal: %s", leftJSON)
	}
}

func TestZeroTimestampIsNotMarkedPresent(t *testing.T) {
	value := map[string]any{
		"created_at": time.Time{}.Format(time.RFC3339Nano),
		"updated_at": caseEpoch.Format(time.RFC3339Nano),
	}
	normalizeTimestamps(value, "created_at", "updated_at")
	if value["created_at"] != nil {
		t.Fatalf("zero created_at = %v, want nil", value["created_at"])
	}
	if value["updated_at"] != presentMarker {
		t.Fatalf("updated_at = %v, want %q", value["updated_at"], presentMarker)
	}
}

func TestEventExtensionTimestampsRemainSemantic(t *testing.T) {
	evt := event.Event{
		ID:        "physical-event",
		Timestamp: caseEpoch,
		Response:  &model.Response{Timestamp: caseEpoch},
		Extensions: map[string]json.RawMessage{
			"custom.example/v1": json.RawMessage(`{"timestamp":"2026-07-01T00:00:00Z","created_at":"semantic"}`),
		},
	}
	events, _, _, err := normalizeEvents([]event.Event{evt}, EventOrderGlobal)
	if err != nil {
		t.Fatalf("normalizeEvents() error = %v", err)
	}
	if events[0]["timestamp"] != presentMarker {
		t.Fatalf("event timestamp = %v, want %q", events[0]["timestamp"], presentMarker)
	}
	response, ok := events[0]["response"].(map[string]any)
	if !ok || response["timestamp"] != presentMarker {
		t.Fatalf("response timestamp = %v, want %q", response["timestamp"], presentMarker)
	}
	extensions, ok := events[0]["extensions"].(map[string]any)
	if !ok {
		t.Fatalf("event extensions = %T, want object", events[0]["extensions"])
	}
	payload, ok := extensions["custom.example/v1"].(map[string]any)
	if !ok {
		t.Fatalf("custom extension = %T, want object", extensions["custom.example/v1"])
	}
	if payload["timestamp"] != "2026-07-01T00:00:00Z" || payload["created_at"] != "semantic" {
		t.Fatalf("semantic extension timestamps were normalized: %v", payload)
	}
}

func TestCausalEventNormalizationIgnoresGlobalInterleaving(t *testing.T) {
	root := causalEvent(t, "root", "", "root")
	branchA1 := causalEvent(t, "a-1", "branch/a", "a-1")
	branchA2 := causalEvent(t, "a-2", "branch/a", "a-2")
	branchB1 := causalEvent(t, "b-1", "branch/b", "b-1")
	branchB2 := causalEvent(t, "b-2", "branch/b", "b-2")

	left, leftOrder, _, err := normalizeEvents([]event.Event{
		root, branchA1, branchB1, branchA2, branchB2,
	}, EventOrderCausal)
	if err != nil {
		t.Fatalf("normalizeEvents(left) error = %v", err)
	}
	right, rightOrder, _, err := normalizeEvents([]event.Event{
		root, branchB1, branchA1, branchB2, branchA2,
	}, EventOrderCausal)
	if err != nil {
		t.Fatalf("normalizeEvents(right) error = %v", err)
	}
	if !reflect.DeepEqual(left, right) || !reflect.DeepEqual(leftOrder, rightOrder) {
		t.Fatalf("causally equivalent interleavings differ:\nleft=%+v\nright=%+v", left, right)
	}
}

func TestMemoryEventTimeRemainsSemantic(t *testing.T) {
	instant := time.Date(2026, time.July, 1, 8, 30, 0, 0, time.UTC)
	sameInstant := instant.In(time.FixedZone("UTC+8", 8*60*60))
	entry := func(eventTime *time.Time) *memory.Entry {
		return &memory.Entry{
			ID:      "backend-id",
			AppName: "replaytest",
			UserID:  "user-1",
			Memory: &memory.Memory{
				Memory:    "A dated event.",
				EventTime: eventTime,
			},
		}
	}
	left, err := normalizeMemories([]*memory.Entry{entry(&instant)})
	if err != nil {
		t.Fatalf("normalizeMemories(left) error = %v", err)
	}
	right, err := normalizeMemories([]*memory.Entry{entry(&sameInstant)})
	if err != nil {
		t.Fatalf("normalizeMemories(right) error = %v", err)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("equivalent instants differ: %+v != %+v", left, right)
	}
	drifted := instant.Add(time.Second)
	other, err := normalizeMemories([]*memory.Entry{entry(&drifted)})
	if err != nil {
		t.Fatalf("normalizeMemories(drifted) error = %v", err)
	}
	if reflect.DeepEqual(left, other) {
		t.Fatal("different memory event times were normalized away")
	}
}

func TestReportJSONRoundTrip(t *testing.T) {
	report := Report{
		GeneratedAt:    caseEpoch,
		ComparisonMode: ComparisonReference,
		Reference:      "inmemory",
		Backends:       []string{"inmemory", "sqlite"},
		TotalCases:     1,
		FailedCases:    1,
		BlockingDiffs:  1,
		Cases: []CaseResult{{
			Name:   "summary_filter_key",
			Status: StatusFailed,
			Diffs: []Diff{{
				Case:             "summary_filter_key",
				BackendA:         "inmemory",
				BackendB:         "sqlite",
				SessionID:        "summary_filter_key",
				SummaryFilterKey: "agent/weather",
				Path:             "/summaries/agent~1weather/text",
				Baseline:         "expected",
				Actual:           "drifted",
			}},
		}},
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(raw), `"summary_filter_key":"agent/weather"`) {
		t.Fatalf("report JSON lacks summary locator: %s", raw)
	}
	var decoded Report
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("decoded Validate() error = %v", err)
	}
}

func TestWriteReportAndSample(t *testing.T) {
	raw, err := os.ReadFile("testdata/session_memory_summary_track_diff_report.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var report Report
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("sample report is invalid JSON: %v", err)
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("sample report Validate() error = %v", err)
	}
	var output bytes.Buffer
	if err := WriteReport(&output, report); err != nil {
		t.Fatalf("WriteReport() error = %v", err)
	}
	if !json.Valid(output.Bytes()) || !bytes.Contains(output.Bytes(), []byte("fault_injection_demo")) {
		t.Fatalf("WriteReport() output is invalid: %s", output.Bytes())
	}
	if err := WriteReport(nil, report); err == nil {
		t.Fatal("WriteReport(nil) unexpectedly succeeded")
	}
}

func TestReportValidationRejectsIncorrectCounters(t *testing.T) {
	report := Report{
		GeneratedAt:    caseEpoch,
		ComparisonMode: ComparisonReference,
		Reference:      "baseline",
		Backends:       []string{"baseline", "actual"},
		TotalCases:     1,
		PassedCases:    1,
		Cases: []CaseResult{{
			Name:   "clean",
			Status: StatusPassed,
		}},
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}
	report.PassedCases = 0
	if err := report.Validate(); err == nil {
		t.Fatal("report with incorrect status counters unexpectedly validated")
	}
}

func causalEvent(t *testing.T, logicalID, filterKey, content string) event.Event {
	t.Helper()
	step := messageStep("causal-"+logicalID, logicalID, 1, "assistant", "assistant", content, filterKey)
	evt := step.Event.Event.Clone()
	evt.ID = "physical-" + logicalID
	if err := event.SetExtension(evt, logicalEventIDExtension, logicalID); err != nil {
		t.Fatalf("SetExtension() error = %v", err)
	}
	return *evt
}

type eventAuthorDriftService struct {
	session.Service
}

type unexpectedStateReadService struct {
	session.Service
	err error
}

type summaryLeakService struct {
	session.Service
}

func (s *summaryLeakService) GetSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) (*session.Session, error) {
	sess, err := s.Service.GetSession(ctx, key, options...)
	if err != nil || sess == nil || !strings.HasSuffix(key.SessionID, "-summary-isolation") {
		return sess, err
	}
	sess = sess.Clone()
	sess.Summaries = map[string]*session.Summary{
		session.SummaryFilterKeyAllContents: {Summary: "leaked summary"},
	}
	return sess, nil
}

func (s *unexpectedStateReadService) ListAppStates(context.Context, string) (session.StateMap, error) {
	return nil, s.err
}

func (s *unexpectedStateReadService) ListUserStates(context.Context, session.UserKey) (session.StateMap, error) {
	return nil, s.err
}

func (s *eventAuthorDriftService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	options ...session.Option,
) error {
	drifted := evt.Clone()
	drifted.Author += "-drifted"
	return s.Service.AppendEvent(ctx, sess, drifted, options...)
}

func eventAuthorDriftBackend(name string) Backend {
	backend := InMemoryBackend()
	backend.Name = name
	open := backend.Open
	backend.Open = func(ctx context.Context, caseName string) (*Services, error) {
		services, err := open(ctx, caseName)
		if err != nil {
			return nil, err
		}
		services.Session = &eventAuthorDriftService{Service: services.Session}
		return services, nil
	}
	return backend
}

func openFailureBackend(name string) Backend {
	return Backend{
		Name:         name,
		Capabilities: FullCapabilities(),
		Open: func(context.Context, string) (*Services, error) {
			return nil, errors.New("injected open failure")
		},
	}
}

func missingCapabilityBackend(name string, capability Capability) Backend {
	backend := InMemoryBackend()
	backend.Name = name
	backend.Capabilities[capability] = false
	return backend
}

func countEvidence(diffs []Diff, backend, path string) int {
	count := 0
	for _, diff := range diffs {
		if diff.BackendA == backend && diff.BackendB == backend && diff.Path == path {
			count++
		}
	}
	return count
}

func minimalSnapshot(backend, score string) Snapshot {
	return Snapshot{
		Backend: backend,
		Case:    "allowed",
		Session: CanonicalMap{"id": "session-1", "app_name": "app", "user_id": "user"},
		State: map[string]map[string]string{
			"app": {}, "user": {}, "session": {"score": score},
		},
		Summaries: map[string]CanonicalMap{},
		Tracks:    map[string][]CanonicalMap{},
	}
}
