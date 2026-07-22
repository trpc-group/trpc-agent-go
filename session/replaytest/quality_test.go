//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestRunnerRejectsInvalidConfiguration(t *testing.T) {
	validCase := PublicCases()[0]
	left := InMemoryBackend()
	left.Name = "left"
	right := InMemoryBackend()
	right.Name = "right"
	invalidAllowedDiffCase := validCase
	invalidAllowedDiffCase.AllowedDiffs = []AllowedDiff{{
		BackendA: "left",
		BackendB: "right",
		Path:     "relative",
		Rule:     AllowedIgnore,
		Reason:   "invalid path",
	}}
	unknownCapabilityBackend := right
	unknownCapabilityBackend.Name = "unknown-capability"
	unknownCapabilityBackend.Capabilities = FullCapabilities()
	unknownCapabilityBackend.Capabilities["not-a-capability"] = true
	tests := []struct {
		name     string
		runner   Runner
		cases    []Case
		backends []Backend
	}{
		{name: "no cases", cases: nil, backends: []Backend{left, right}},
		{name: "one backend", cases: []Case{validCase}, backends: []Backend{left}},
		{name: "unnamed backend", cases: []Case{validCase}, backends: []Backend{left, {Open: right.Open}}},
		{name: "missing factory", cases: []Case{validCase}, backends: []Backend{left, {Name: "right"}}},
		{name: "duplicate backend", cases: []Case{validCase}, backends: []Backend{left, left}},
		{name: "unknown mode", runner: Runner{Mode: "unknown"}, cases: []Case{validCase}, backends: []Backend{left, right}},
		{name: "missing reference", runner: Runner{Reference: "missing"}, cases: []Case{validCase}, backends: []Backend{left, right}},
		{name: "empty case", cases: []Case{{Name: "empty"}}, backends: []Backend{left, right}},
		{name: "invalid allowed diff", cases: []Case{invalidAllowedDiffCase}, backends: []Backend{left, right}},
		{name: "unknown backend capability", cases: []Case{validCase}, backends: []Backend{left, unknownCapabilityBackend}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.runner.Run(context.Background(), test.cases, test.backends); err == nil {
				t.Fatal("Run() unexpectedly accepted invalid configuration")
			}
		})
	}
}

func TestReplayRejectsInvalidBackendMetadata(t *testing.T) {
	replayCase := PublicCases()[0]
	backend := InMemoryBackend()
	backend.Name = ""
	if _, err := Replay(context.Background(), replayCase, backend); err == nil {
		t.Fatal("Replay() unexpectedly accepted an empty backend name")
	}
	backend = InMemoryBackend()
	backend.Capabilities[Capability("not-a-capability")] = true
	if _, err := Replay(context.Background(), replayCase, backend); err == nil {
		t.Fatal("Replay() unexpectedly accepted an unknown backend capability")
	}
}

func TestReplayRejectsMissingRequiredCapabilities(t *testing.T) {
	replayCase := memoryCase()
	backend := missingCapabilityBackend("missing-memory", CapabilityMemory)
	openCalls := 0
	backend.Open = func(context.Context, string) (*Services, error) {
		openCalls++
		return nil, errors.New("Open must not be called")
	}
	_, err := Replay(context.Background(), replayCase, backend)
	if err == nil || !strings.Contains(err.Error(), string(CapabilityMemory)) {
		t.Fatalf("Replay() error = %v, want missing memory capability", err)
	}
	if openCalls != 0 {
		t.Fatalf("Replay() opened a backend missing required capabilities %d times", openCalls)
	}
}

func TestReplayAndRunnerHonorContextCancellation(t *testing.T) {
	openCalls := 0
	backend := Backend{
		Name:         "canceled",
		Capabilities: FullCapabilities(),
		Open: func(context.Context, string) (*Services, error) {
			openCalls++
			return nil, errors.New("Open must not be called")
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Replay(ctx, PublicCases()[0], backend); !errors.Is(err, context.Canceled) {
		t.Fatalf("Replay() error = %v, want context.Canceled", err)
	}
	other := backend
	other.Name = "other"
	if _, err := (Runner{}).Run(ctx, PublicCases()[:1], []Backend{backend, other}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if openCalls != 0 {
		t.Fatalf("canceled operations opened %d backends", openCalls)
	}
	if _, err := Replay(nil, PublicCases()[0], backend); err == nil {
		t.Fatal("Replay() unexpectedly accepted a nil context")
	}
	if _, err := (Runner{}).Run(nil, PublicCases()[:1], []Backend{backend, other}); err == nil {
		t.Fatal("Run() unexpectedly accepted a nil context")
	}
}

func TestReplayRechecksCancellationAfterSuccessfulStep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := InMemoryBackend()
	open := backend.Open
	backend.Open = func(ctx context.Context, caseName string) (*Services, error) {
		services, err := open(ctx, caseName)
		if err == nil {
			services.Session = &cancelAfterAppendService{
				Service: services.Session,
				cancel:  cancel,
			}
		}
		return services, err
	}
	if _, err := Replay(ctx, PublicCases()[0], backend); !errors.Is(err, context.Canceled) {
		t.Fatalf("Replay() error = %v, want context.Canceled", err)
	}
}

func TestReplayStopsStateStepAfterSuccessfulCallCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := InMemoryBackend()
	open := backend.Open
	var wrapped *cancelAfterAppUpdateService
	backend.Open = func(ctx context.Context, caseName string) (*Services, error) {
		services, err := open(ctx, caseName)
		if err == nil {
			wrapped = &cancelAfterAppUpdateService{
				Service: services.Session,
				cancel:  cancel,
			}
			services.Session = wrapped
		}
		return services, err
	}
	replayCase := Case{
		Name:     "cancel-state-step",
		Requires: []Capability{CapabilitySession, CapabilityAppState},
		Steps: []Step{{
			Name: "update-then-delete",
			Kind: StepUpdateState,
			State: &StateInput{
				Scope:      StateScopeApp,
				Values:     session.StateMap{"value": []byte(`1`)},
				DeleteKeys: []string{"stale"},
			},
		}},
	}
	if _, err := Replay(ctx, replayCase, backend); !errors.Is(err, context.Canceled) {
		t.Fatalf("Replay() error = %v, want context.Canceled", err)
	}
	if wrapped == nil {
		t.Fatal("backend was not opened")
	}
	if wrapped.deleteCalls != 0 {
		t.Fatalf("DeleteAppState() calls after cancellation = %d, want 0", wrapped.deleteCalls)
	}
}

func TestReplayRechecksCancellationAfterClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := InMemoryBackend()
	open := backend.Open
	backend.Open = func(ctx context.Context, caseName string) (*Services, error) {
		services, err := open(ctx, caseName)
		if err == nil {
			services.Cleanup = func() error {
				cancel()
				return nil
			}
		}
		return services, err
	}
	if _, err := Replay(ctx, PublicCases()[0], backend); !errors.Is(err, context.Canceled) {
		t.Fatalf("Replay() error = %v, want context.Canceled", err)
	}
}

func TestReplayRechecksCancellationAfterFailedOpenCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	openErr := errors.New("injected open failure")
	backend := Backend{
		Name:         "failed-open-cancellation",
		Capabilities: Capabilities{CapabilitySession: true},
		Open: func(context.Context, string) (*Services, error) {
			return &Services{Cleanup: func() error {
				cancel()
				return nil
			}}, openErr
		},
	}
	_, err := Replay(ctx, PublicCases()[0], backend)
	if !errors.Is(err, openErr) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Replay() error = %v, want open failure and context.Canceled", err)
	}
}

func TestReplayPreservesFailedOpenWhenAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	openErr := errors.New("injected open failure")
	cleanupErr := errors.New("injected cleanup failure")
	cleanupCalls := 0
	backend := Backend{
		Name:         "failed-canceled-open",
		Capabilities: Capabilities{CapabilitySession: true},
		Open: func(context.Context, string) (*Services, error) {
			cancel()
			return &Services{Cleanup: func() error {
				cleanupCalls++
				return cleanupErr
			}}, openErr
		},
	}
	_, err := Replay(ctx, PublicCases()[0], backend)
	if !errors.Is(err, openErr) ||
		!errors.Is(err, context.Canceled) ||
		!errors.Is(err, cleanupErr) {
		t.Fatalf("Replay() error = %v, want open, cancellation, and cleanup failures", err)
	}
	if cleanupCalls != 1 {
		t.Fatalf("Replay() cleanup calls = %d, want 1", cleanupCalls)
	}
}

func TestReplayClosesSuccessfulOpenAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleanupErr := errors.New("injected cleanup failure")
	cleanupCalls := 0
	backend := Backend{
		Name:         "successful-canceled-open",
		Capabilities: Capabilities{CapabilitySession: true},
		Open: func(context.Context, string) (*Services, error) {
			cancel()
			return &Services{Cleanup: func() error {
				cleanupCalls++
				return cleanupErr
			}}, nil
		},
	}
	_, err := Replay(ctx, PublicCases()[0], backend)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, cleanupErr) {
		t.Fatalf("Replay() error = %v, want cancellation and cleanup failures", err)
	}
	if cleanupCalls != 1 {
		t.Fatalf("Replay() cleanup calls = %d, want 1", cleanupCalls)
	}
}

func TestCloneStatePreservesNilAndEmptyValues(t *testing.T) {
	cloned := cloneState(session.StateMap{
		"nil":   nil,
		"empty": []byte{},
		"value": []byte("value"),
	})
	if cloned["nil"] != nil {
		t.Fatalf("cloneState() nil value = %#v, want nil", cloned["nil"])
	}
	if cloned["empty"] == nil || len(cloned["empty"]) != 0 {
		t.Fatalf("cloneState() empty value = %#v, want non-nil empty bytes", cloned["empty"])
	}
	if string(cloned["value"]) != "value" {
		t.Fatalf("cloneState() value = %q, want value", cloned["value"])
	}
}

func TestPreparedInputsAreDeepCopied(t *testing.T) {
	t.Run("event state delta", func(t *testing.T) {
		inputEvent := event.New("invocation", "author")
		inputEvent.StateDelta = map[string][]byte{
			"nil":   nil,
			"empty": {},
			"value": []byte("value"),
		}
		exec := execution{session: &session.Session{CreatedAt: caseEpoch}}
		prepared, err := exec.prepareEvent(&EventInput{
			LogicalID: "logical-event",
			Event:     inputEvent,
		})
		if err != nil {
			t.Fatalf("prepareEvent() error = %v", err)
		}
		if prepared.StateDelta["nil"] != nil {
			t.Fatalf("prepared nil state = %#v", prepared.StateDelta["nil"])
		}
		if prepared.StateDelta["empty"] == nil || len(prepared.StateDelta["empty"]) != 0 {
			t.Fatalf("prepared empty state = %#v", prepared.StateDelta["empty"])
		}
		prepared.StateDelta["value"][0] = 'x'
		if string(inputEvent.StateDelta["value"]) != "value" {
			t.Fatalf("prepareEvent() mutated input state = %q", inputEvent.StateDelta["value"])
		}
	})

	t.Run("memory metadata", func(t *testing.T) {
		eventTime := caseEpoch
		input := &memory.Metadata{
			EventTime:    &eventTime,
			Participants: []string{"user"},
		}
		cloned := cloneMemoryMetadata(input)
		input.Participants[0] = "changed"
		*input.EventTime = input.EventTime.Add(time.Hour)
		if cloned.Participants[0] != "user" || !cloned.EventTime.Equal(caseEpoch) {
			t.Fatalf("cloneMemoryMetadata() retained input ownership: %#v", cloned)
		}
	})
}

func TestReplayPreservesEmptyStateValue(t *testing.T) {
	replayCase := PublicCases()[0]
	replayCase.Name = "empty-state-replay"
	replayCase.Requires = []Capability{CapabilitySession, CapabilitySessionState}
	replayCase.InitialState = session.StateMap{"empty": []byte{}}
	snapshot, err := Replay(context.Background(), replayCase, InMemoryBackend())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	value, ok := snapshot.State["session"]["empty"].(CanonicalMap)
	if !ok || value["kind"] != "bytes" || value["base64"] != "" {
		t.Fatalf("Replay() normalized empty state = %#v, want tagged empty bytes", snapshot.State["session"]["empty"])
	}
}

func TestReportValidationRejectsMalformedReports(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Report)
	}{
		{name: "missing generation time", mutate: func(report *Report) { report.GeneratedAt = time.Time{} }},
		{name: "too few backends", mutate: func(report *Report) { report.Backends = report.Backends[:1] }},
		{name: "unknown comparison mode", mutate: func(report *Report) { report.ComparisonMode = "unknown" }},
		{name: "empty backend", mutate: func(report *Report) { report.Backends[1] = "" }},
		{name: "duplicate backend", mutate: func(report *Report) { report.Backends[1] = report.Backends[0] }},
		{name: "missing reference", mutate: func(report *Report) { report.Reference = "missing" }},
		{name: "consensus reference", mutate: func(report *Report) {
			report.ComparisonMode = ComparisonConsensus
		}},
		{name: "case count mismatch", mutate: func(report *Report) { report.TotalCases++ }},
		{name: "no cases", mutate: func(report *Report) {
			report.TotalCases = 0
			report.PassedCases = 0
			report.Cases = nil
		}},
		{name: "empty case name", mutate: func(report *Report) { report.Cases[0].Name = "" }},
		{name: "duplicate case", mutate: func(report *Report) {
			report.TotalCases = 2
			report.PassedCases = 2
			report.Cases = append(report.Cases, report.Cases[0])
		}},
		{name: "unknown status", mutate: func(report *Report) { report.Cases[0].Status = "unknown" }},
		{name: "negative duration", mutate: func(report *Report) { report.Cases[0].Duration = -1 }},
		{name: "invalid diff locator", mutate: func(report *Report) {
			setBlockingReportDiff(report, Diff{BackendA: "baseline", BackendB: "actual", SessionID: "clean", Path: "/state"})
		}},
		{name: "unknown left backend", mutate: func(report *Report) {
			setBlockingReportDiff(report, validReportDiff())
			report.Cases[0].Diffs[0].BackendA = "missing"
		}},
		{name: "unknown right backend", mutate: func(report *Report) {
			setBlockingReportDiff(report, validReportDiff())
			report.Cases[0].Diffs[0].BackendB = "missing"
		}},
		{name: "allowed diff without explanation", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Allowed = true
			report.AllowedDiffs = 1
			report.Cases[0].Diffs = []Diff{diff}
		}},
		{name: "allowed execution failure", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/execution"
			diff.Allowed = true
			diff.Explanation = "invalid allowance"
			report.AllowedDiffs = 1
			report.Cases[0].Diffs = []Diff{diff}
		}},
		{name: "blocking capability evidence", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/capabilities/session"
			setBlockingReportDiff(report, diff)
		}},
		{name: "unknown capability evidence", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/capabilities/not-real"
			diff.Baseline = true
			diff.Actual = false
			diff.Allowed = true
			diff.Explanation = "forged capability"
			report.PassedCases = 0
			report.UnsupportedCases = 1
			report.AllowedDiffs = 1
			report.Cases[0].Status = StatusUnsupported
			report.Cases[0].Diffs = []Diff{diff}
		}},
		{name: "malformed capability evidence", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/capabilities/memory"
			diff.Baseline = false
			diff.Actual = true
			diff.Allowed = true
			diff.Explanation = "reversed capability values"
			report.PassedCases = 0
			report.UnsupportedCases = 1
			report.AllowedDiffs = 1
			report.Cases[0].Status = StatusUnsupported
			report.Cases[0].Diffs = []Diff{diff}
		}},
		{name: "reference diff direction", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.BackendA = "actual"
			diff.BackendB = "baseline"
			setBlockingReportDiff(report, diff)
		}},
		{name: "reference semantic self diff", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.BackendB = "baseline"
			setBlockingReportDiff(report, diff)
		}},
		{name: "consensus data in reference mode", mutate: func(report *Report) {
			report.Cases[0].Consensus = &ConsensusResult{}
		}},
		{name: "missing consensus data", mutate: func(report *Report) {
			report.ComparisonMode = ComparisonConsensus
			report.Reference = ""
		}},
		{name: "incorrect diff counters", mutate: func(report *Report) { report.BlockingDiffs = 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := validReferenceReport()
			test.mutate(&report)
			if err := report.Validate(); err == nil {
				t.Fatal("Validate() unexpectedly accepted a malformed report")
			}
		})
	}
}

func TestConsensusValidationRejectsMalformedMatrix(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ConsensusResult, *[]Diff, map[string]struct{})
	}{
		{name: "unsorted backends", mutate: func(result *ConsensusResult, _ *[]Diff, _ map[string]struct{}) {
			result.ComparableBackends = []string{"b", "a"}
		}},
		{name: "unknown backend", mutate: func(result *ConsensusResult, _ *[]Diff, _ map[string]struct{}) {
			result.ComparableBackends = []string{"a", "c"}
			result.Pairs = []PairComparison{{BackendA: "a", BackendB: "c"}}
		}},
		{name: "duplicate backend", mutate: func(result *ConsensusResult, _ *[]Diff, _ map[string]struct{}) {
			result.ComparableBackends = []string{"a", "a"}
		}},
		{name: "missing pair", mutate: func(result *ConsensusResult, _ *[]Diff, _ map[string]struct{}) {
			result.Pairs = nil
		}},
		{name: "unordered pair", mutate: func(result *ConsensusResult, _ *[]Diff, _ map[string]struct{}) {
			result.Pairs = []PairComparison{{BackendA: "b", BackendB: "a"}}
		}},
		{name: "pair backend unavailable", mutate: func(result *ConsensusResult, _ *[]Diff, _ map[string]struct{}) {
			result.Pairs = []PairComparison{{BackendA: "a", BackendB: "c"}}
		}},
		{name: "duplicate pair", mutate: func(result *ConsensusResult, _ *[]Diff, known map[string]struct{}) {
			known["c"] = struct{}{}
			result.ComparableBackends = []string{"a", "b", "c"}
			result.Pairs = []PairComparison{
				{BackendA: "a", BackendB: "b"},
				{BackendA: "a", BackendB: "b"},
				{BackendA: "b", BackendB: "c"},
			}
		}},
		{name: "pair counters", mutate: func(result *ConsensusResult, _ *[]Diff, _ map[string]struct{}) {
			result.Pairs[0].BlockingDiffs = 1
		}},
		{name: "comparable self diff", mutate: func(_ *ConsensusResult, diffs *[]Diff, _ map[string]struct{}) {
			*diffs = []Diff{{BackendA: "a", BackendB: "a", Path: "/execution"}}
		}},
		{name: "invalid exclusion evidence", mutate: func(_ *ConsensusResult, diffs *[]Diff, known map[string]struct{}) {
			known["c"] = struct{}{}
			*diffs = []Diff{{BackendA: "c", BackendB: "c", Path: "/state"}}
		}},
		{name: "unknown capability exclusion", mutate: func(_ *ConsensusResult, diffs *[]Diff, known map[string]struct{}) {
			known["c"] = struct{}{}
			*diffs = []Diff{{BackendA: "c", BackendB: "c", Path: "/capabilities/not-real", Allowed: true}}
		}},
		{name: "missing exclusion evidence", mutate: func(_ *ConsensusResult, _ *[]Diff, known map[string]struct{}) {
			known["c"] = struct{}{}
		}},
		{name: "pair outside matrix", mutate: func(_ *ConsensusResult, diffs *[]Diff, known map[string]struct{}) {
			known["c"] = struct{}{}
			*diffs = []Diff{{BackendA: "a", BackendB: "c", Path: "/session/id"}}
		}},
		{name: "inconsistent verdict", mutate: func(result *ConsensusResult, _ *[]Diff, _ map[string]struct{}) {
			result.Verdict = ConsensusAmbiguous
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ConsensusResult{
				Verdict:            ConsensusUnanimous,
				ComparableBackends: []string{"a", "b"},
				Pairs:              []PairComparison{{BackendA: "a", BackendB: "b"}},
			}
			known := map[string]struct{}{"a": {}, "b": {}}
			var diffs []Diff
			test.mutate(&result, &diffs, known)
			if err := validateConsensusResult("case", result, diffs, known); err == nil {
				t.Fatal("validateConsensusResult() unexpectedly accepted a malformed matrix")
			}
		})
	}
}

func TestConsensusPairKeysDoNotCollide(t *testing.T) {
	backends := []string{"a", "a\x00b", "b\x00c", "c"}
	known := make(map[string]struct{}, len(backends))
	pairs := make([]PairComparison, 0, 6)
	for left := 0; left < len(backends); left++ {
		known[backends[left]] = struct{}{}
		for right := left + 1; right < len(backends); right++ {
			pairs = append(pairs, PairComparison{BackendA: backends[left], BackendB: backends[right]})
		}
	}
	result := ConsensusResult{
		Verdict:            ConsensusUnanimous,
		ComparableBackends: backends,
		Pairs:              pairs,
	}
	if err := validateConsensusResult("nul-name", result, nil, known); err != nil {
		t.Fatalf("validateConsensusResult() rejected distinct structured keys: %v", err)
	}
}

func TestInjectFaultRejectsMissingPrerequisites(t *testing.T) {
	kinds := []FaultKind{
		FaultEventContent,
		FaultEventOrder,
		FaultToolArguments,
		FaultStateValue,
		FaultMemoryContent,
		FaultDuplicateMemory,
		FaultMemorySearchOrder,
		FaultSummaryText,
		FaultSummaryMissing,
		FaultSummaryFilterKey,
		FaultSummaryStale,
		FaultTrackPayload,
		FaultDuplicateEvent,
		"unknown",
	}
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			if _, err := InjectFault(Snapshot{}, kind); err == nil {
				t.Fatal("InjectFault() unexpectedly accepted an incomplete snapshot")
			}
		})
	}

	t.Run("invalid memory payload", func(t *testing.T) {
		input := Snapshot{Memories: []CanonicalMap{{"memory": "invalid"}}}
		if _, err := InjectFault(input, FaultMemoryContent); err == nil {
			t.Fatal("InjectFault() unexpectedly accepted an invalid memory payload")
		}
	})
	t.Run("invalid track payload", func(t *testing.T) {
		input := Snapshot{Tracks: map[string][]CanonicalMap{"track": {{"payload": "invalid"}}}}
		if _, err := InjectFault(input, FaultTrackPayload); err == nil {
			t.Fatal("InjectFault() unexpectedly accepted an invalid track payload")
		}
	})
	t.Run("nil summary payload", func(t *testing.T) {
		input := Snapshot{Summaries: map[string]CanonicalMap{"empty": nil}}
		for _, kind := range []FaultKind{
			FaultSummaryText,
			FaultSummaryMissing,
			FaultSummaryFilterKey,
			FaultSummaryStale,
		} {
			if _, err := InjectFault(input, kind); err == nil {
				t.Fatalf("InjectFault(%q) unexpectedly accepted a nil summary", kind)
			}
		}
	})
	t.Run("unencodable snapshot", func(t *testing.T) {
		input := Snapshot{Session: CanonicalMap{"invalid": func() {}}}
		if _, err := InjectFault(input, FaultEventContent); err == nil {
			t.Fatal("InjectFault() unexpectedly encoded an unsupported value")
		}
	})
}

func TestDeterministicSummarizerContract(t *testing.T) {
	summarizer := &DeterministicSummarizer{}
	if summarizer.ShouldSummarize(nil) {
		t.Fatal("ShouldSummarize(nil) = true")
	}
	if _, err := summarizer.Summarize(context.Background(), nil); !errors.Is(err, session.ErrNilSession) {
		t.Fatalf("Summarize(nil) error = %v", err)
	}
	sess := &session.Session{Events: []event.Event{
		{Author: "empty"},
		{
			Author: "assistant",
			Response: &model.Response{Choices: []model.Choice{{
				Delta: model.Message{Role: model.RoleAssistant, Content: "delta"},
			}}},
		},
	}}
	if !summarizer.ShouldSummarize(sess) {
		t.Fatal("ShouldSummarize(non-empty) = false")
	}
	got, err := summarizer.Summarize(context.Background(), sess)
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if got != "summary[empty:|assistant:assistant:delta]" {
		t.Fatalf("Summarize() = %q", got)
	}
	summarizer.SetPrompt("ignored")
	summarizer.SetModel(nil)
	if !reflect.DeepEqual(summarizer.Metadata(), map[string]any{"name": "replaytest-deterministic"}) {
		t.Fatalf("Metadata() = %v", summarizer.Metadata())
	}
}

func TestComparisonAndNormalizationEdgeCases(t *testing.T) {
	t.Run("unencodable baseline", func(t *testing.T) {
		baseline := minimalSnapshot("baseline", `{}`)
		baseline.Case = "invalid"
		baseline.Session["invalid"] = func() {}
		actual := minimalSnapshot("actual", `{}`)
		actual.Case = "invalid"
		if _, err := Compare("invalid", baseline, actual, nil); err == nil {
			t.Fatal("Compare() unexpectedly encoded an unsupported baseline value")
		}
	})
	t.Run("unencodable actual", func(t *testing.T) {
		actual := minimalSnapshot("actual", `{}`)
		actual.Case = "invalid"
		actual.Session["invalid"] = func() {}
		baseline := minimalSnapshot("baseline", `{}`)
		baseline.Case = "invalid"
		if _, err := Compare("invalid", baseline, actual, nil); err == nil {
			t.Fatal("Compare() unexpectedly encoded an unsupported actual value")
		}
	})
	t.Run("empty case name", func(t *testing.T) {
		if _, err := Compare("", minimalSnapshot("baseline", `{}`), minimalSnapshot("actual", `{}`), nil); err == nil {
			t.Fatal("Compare() unexpectedly accepted an empty case name")
		}
	})
	t.Run("invalid snapshot metadata", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*Snapshot, *Snapshot)
		}{
			{name: "empty backend", mutate: func(baseline, _ *Snapshot) { baseline.Backend = "" }},
			{name: "same backend", mutate: func(baseline, actual *Snapshot) { actual.Backend = baseline.Backend }},
			{name: "wrong baseline case", mutate: func(baseline, _ *Snapshot) { baseline.Case = "other" }},
			{name: "wrong actual case", mutate: func(_, actual *Snapshot) { actual.Case = "other" }},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				baseline := minimalSnapshot("baseline", `{}`)
				actual := minimalSnapshot("actual", `{}`)
				test.mutate(&baseline, &actual)
				if _, err := Compare("allowed", baseline, actual, nil); err == nil {
					t.Fatal("Compare() unexpectedly accepted invalid snapshot metadata")
				}
			})
		}
	})
	t.Run("large integer precision", func(t *testing.T) {
		baseline := minimalSnapshot("baseline", `{}`)
		actual := minimalSnapshot("actual", `{}`)
		baseline.Case = "large-integer"
		actual.Case = "large-integer"
		baseline.Session["sequence"] = int64(9007199254740992)
		actual.Session["sequence"] = int64(9007199254740993)
		diffs, err := Compare("large-integer", baseline, actual, nil)
		if err != nil {
			t.Fatalf("Compare() error = %v", err)
		}
		if len(diffs) != 1 || diffs[0].Path != "/session/sequence" {
			t.Fatalf("Compare() diffs = %+v, want exact large-integer difference", diffs)
		}
	})
	t.Run("missing value is not the same type as null", func(t *testing.T) {
		baseline := minimalSnapshot("baseline", `{}`)
		actual := minimalSnapshot("actual", `{}`)
		baseline.Case = "presence"
		actual.Case = "presence"
		baseline.Session["optional"] = nil
		diffs, err := Compare("presence", baseline, actual, []AllowedDiff{{
			BackendA: "baseline",
			BackendB: "actual",
			Path:     "/session/optional",
			Rule:     AllowedSameType,
			Reason:   "matching JSON types are accepted",
		}})
		if err != nil {
			t.Fatalf("Compare() error = %v", err)
		}
		if len(diffs) != 1 || diffs[0].Allowed {
			t.Fatalf("Compare() diffs = %+v, want a blocking presence difference", diffs)
		}
	})
	t.Run("diff session locator fallback", func(t *testing.T) {
		baseline := minimalSnapshot("baseline", `{}`)
		actual := minimalSnapshot("actual", `{}`)
		baseline.Case = "locator-case"
		actual.Case = "locator-case"
		baseline.Session["id"] = ""
		actual.Session["id"] = "actual-session"
		diffs, err := Compare("locator-case", baseline, actual, nil)
		if err != nil {
			t.Fatalf("Compare() error = %v", err)
		}
		for _, diff := range diffs {
			if diff.SessionID != "actual-session" {
				t.Fatalf("diff session ID = %q, want actual fallback", diff.SessionID)
			}
		}

		actual.Session["id"] = ""
		baseline.Session["sequence"] = 1
		actual.Session["sequence"] = 2
		diffs, err = Compare("locator-case", baseline, actual, nil)
		if err != nil {
			t.Fatalf("Compare() error = %v", err)
		}
		for _, diff := range diffs {
			if diff.SessionID != "locator-case" {
				t.Fatalf("diff session ID = %q, want case fallback", diff.SessionID)
			}
		}
	})
	t.Run("state values", func(t *testing.T) {
		state := normalizeState(session.StateMap{
			"nil":                         nil,
			"json-null":                   []byte(`null`),
			"literal-nil":                 []byte("<nil>"),
			"json":                        []byte(`{"b":2,"a":1}`),
			"large":                       []byte(`9007199254740993`),
			"text":                        []byte("plain"),
			"tracks":                      []byte(`true`),
			session.StateAppPrefix + "x":  []byte(`true`),
			session.StateUserPrefix + "x": []byte(`true`),
		}, "session")
		want := CanonicalMap{
			"nil":       CanonicalMap{"kind": "nil"},
			"json-null": CanonicalMap{"kind": "json", "json": "null"},
			"literal-nil": CanonicalMap{
				"kind":   "bytes",
				"base64": "PG5pbD4=",
			},
			"json":  CanonicalMap{"kind": "json", "json": `{"a":1,"b":2}`},
			"large": CanonicalMap{"kind": "json", "json": "9007199254740993"},
			"text": CanonicalMap{
				"kind":   "bytes",
				"base64": "cGxhaW4=",
			},
		}
		if !reflect.DeepEqual(state, want) {
			t.Fatalf("normalizeState() = %v, want %v", state, want)
		}
	})
	t.Run("state encodings do not collide", func(t *testing.T) {
		tests := []struct {
			name  string
			left  []byte
			right []byte
		}{
			{name: "nil and JSON null", left: nil, right: []byte(`null`)},
			{name: "nil and literal sentinel", left: nil, right: []byte("<nil>")},
			{
				name:  "bytes and same-shaped JSON",
				left:  []byte{0xff},
				right: []byte(`{"kind":"bytes","base64":"/w=="}`),
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				baseline := minimalSnapshot("baseline", "1")
				actual := minimalSnapshot("actual", "1")
				baseline.Case = "state-encoding"
				actual.Case = "state-encoding"
				baseline.State["session"] = normalizeState(session.StateMap{"value": test.left}, "session")
				actual.State["session"] = normalizeState(session.StateMap{"value": test.right}, "session")
				diffs, err := Compare("state-encoding", baseline, actual, nil)
				if err != nil {
					t.Fatalf("Compare() error = %v", err)
				}
				if len(diffs) == 0 {
					t.Fatal("Compare() did not distinguish state representations")
				}
			})
		}
	})
	t.Run("timestamp forms", func(t *testing.T) {
		for _, value := range []any{nil, "", float64(0)} {
			if got := normalizeTimestampPresence(value); got != nil {
				t.Fatalf("normalizeTimestampPresence(%v) = %v", value, got)
			}
		}
		if got := normalizeTimestampPresence(true); got != presentMarker {
			t.Fatalf("normalizeTimestampPresence(true) = %v", got)
		}
	})
	t.Run("nil session", func(t *testing.T) {
		if _, err := normalizeSnapshot(
			"backend",
			"case",
			EventOrderGlobal,
			nil,
			FullCapabilities(),
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
		); !errors.Is(err, session.ErrNilSession) {
			t.Fatalf("normalizeSnapshot(nil) error = %v", err)
		}
	})
	t.Run("invalid track payloads", func(t *testing.T) {
		for _, payload := range []json.RawMessage{{}, json.RawMessage("{")} {
			sess := &session.Session{Tracks: map[session.Track]*session.TrackEvents{
				"broken": {
					Track: "broken",
					Events: []session.TrackEvent{{
						Track:   "broken",
						Payload: payload,
					}},
				},
			}}
			if _, err := normalizeTracks(sess, time.Time{}); err == nil {
				t.Fatalf("normalizeTracks() accepted invalid JSON %q", payload)
			}
		}
	})
	t.Run("event state delta JSON order", func(t *testing.T) {
		eventWithState := func(id string, value []byte) event.Event {
			evt := event.Event{
				ID:         id,
				StateDelta: session.StateMap{"value": value},
			}
			if err := event.SetExtension(&evt, logicalEventIDExtension, "logical-event"); err != nil {
				t.Fatalf("SetExtension() error = %v", err)
			}
			return evt
		}
		left, _, _, err := normalizeEvents(
			[]event.Event{eventWithState("left", []byte(`{"b":2,"a":1}`))},
			EventOrderGlobal,
			nil,
			time.Time{},
		)
		if err != nil {
			t.Fatalf("normalizeEvents(left) error = %v", err)
		}
		right, _, _, err := normalizeEvents(
			[]event.Event{eventWithState("right", []byte(`{"a":1,"b":2}`))},
			EventOrderGlobal,
			nil,
			time.Time{},
		)
		if err != nil {
			t.Fatalf("normalizeEvents(right) error = %v", err)
		}
		if !reflect.DeepEqual(left, right) {
			t.Fatalf("equivalent state delta JSON differs: %v != %v", left, right)
		}
	})
	t.Run("event state delta preserves scoped and unexpected keys", func(t *testing.T) {
		baseTime := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
		step := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
		evt := step.Event.Event.Clone()
		evt.Timestamp = baseTime.Add(time.Second)
		evt.StateDelta = session.StateMap{
			"app:shared":        []byte(`true`),
			"user:profile":      []byte(`"active"`),
			replayTrackStateKey: []byte(`"unexpected"`),
		}
		if err := event.SetExtension(evt, logicalEventIDExtension, step.Event.LogicalID); err != nil {
			t.Fatalf("SetExtension() error = %v", err)
		}
		events, _, _, err := normalizeEvents(
			[]event.Event{*evt},
			EventOrderGlobal,
			nil,
			baseTime,
		)
		if err != nil {
			t.Fatalf("normalizeEvents() error = %v", err)
		}
		stateDelta, ok := events[0]["stateDelta"].(CanonicalMap)
		if !ok {
			t.Fatalf("normalized state delta = %#v, want CanonicalMap", events[0]["stateDelta"])
		}
		for _, key := range []string{"app:shared", "user:profile", replayTrackStateKey} {
			if _, exists := stateDelta[key]; !exists {
				t.Fatalf("normalized state delta omitted %q: %#v", key, stateDelta)
			}
		}
	})
	t.Run("session snapshot preserves applied event state delta", func(t *testing.T) {
		evt := causalEvent(t, "state-delta", "", "updated")
		evt.StateDelta = session.StateMap{
			"app:shared":   []byte(`true`),
			"user:profile": []byte(`"active"`),
		}
		sess := session.NewSession("replaytest", "user-1", "state-delta")
		sess.CreatedAt = caseEpoch
		sess.Events = []event.Event{evt}
		sess.SetState("app:shared", []byte(`true`))
		sess.SetState("user:profile", []byte(`"active"`))
		sess.SetState("app:merged-only", []byte(`"noise"`))

		snapshot, err := normalizeSnapshot(
			"backend",
			"state-delta",
			EventOrderGlobal,
			nil,
			Capabilities{CapabilitySession: true, CapabilitySessionState: true},
			map[string]struct{}{
				"app:shared":   {},
				"user:profile": {},
			},
			sess,
			nil,
			nil,
			nil,
			nil,
		)
		if err != nil {
			t.Fatalf("normalizeSnapshot() error = %v", err)
		}
		state := snapshot.State["session"]
		for _, key := range []string{"app:shared", "user:profile"} {
			if _, ok := state[key]; !ok {
				t.Fatalf("session snapshot omitted applied delta %q: %#v", key, state)
			}
		}
		if _, ok := state["app:merged-only"]; ok {
			t.Fatalf("session snapshot retained merged-only state: %#v", state)
		}
	})
	t.Run("invalid event identities", func(t *testing.T) {
		eventWithIdentity := func(physicalID, logicalID string) event.Event {
			evt := event.Event{ID: physicalID}
			if logicalID != "" {
				if err := event.SetExtension(&evt, logicalEventIDExtension, logicalID); err != nil {
					t.Fatalf("SetExtension() error = %v", err)
				}
			}
			return evt
		}
		tests := []struct {
			name   string
			events []event.Event
		}{
			{name: "missing physical", events: []event.Event{eventWithIdentity("", "logical")}},
			{name: "missing logical", events: []event.Event{eventWithIdentity("physical", "")}},
			{
				name: "duplicate physical",
				events: []event.Event{
					eventWithIdentity("same", "left"),
					eventWithIdentity("same", "right"),
				},
			},
			{
				name: "duplicate logical",
				events: []event.Event{
					eventWithIdentity("left", "same"),
					eventWithIdentity("right", "same"),
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				if _, _, _, err := normalizeEvents(
					test.events,
					EventOrderGlobal,
					nil,
					time.Time{},
				); err == nil {
					t.Fatal("normalizeEvents() accepted an invalid event identity")
				}
			})
		}
	})
	t.Run("nil track and summary entries", func(t *testing.T) {
		sess := &session.Session{
			ID: "session",
			Tracks: map[session.Track]*session.TrackEvents{
				"empty": nil,
			},
			Summaries: map[string]*session.Summary{
				"empty": nil,
				"branch": {
					Summary: "summary",
					Boundary: &session.SummaryBoundary{
						Version:     session.SummaryBoundaryVersion,
						CutoffAt:    caseEpoch,
						LastEventID: "unknown-physical-id",
					},
				},
			},
		}
		tracks, err := normalizeTracks(sess, time.Time{})
		if err != nil || tracks["empty"] != nil {
			t.Fatalf("normalizeTracks() = %v, %v", tracks, err)
		}
		unknown := sess.Summaries["branch"]
		delete(sess.Summaries, "branch")
		summaries, err := normalizeSummaries(sess, sess.GetEvents(), nil)
		if err != nil {
			t.Fatalf("normalizeSummaries() error = %v", err)
		}
		if summaries["empty"] != nil {
			t.Fatalf("nil summary = %v", summaries["empty"])
		}
		sess.Summaries["branch"] = unknown
		if _, err := normalizeSummaries(sess, sess.GetEvents(), nil); err == nil {
			t.Fatal("normalizeSummaries() accepted an unknown event anchor")
		}
	})
}

func TestReplayPropagatesLifecycleFailures(t *testing.T) {
	caseUnderTest := PublicCases()[0]
	openBase := InMemoryBackend().Open
	createErr := errors.New("create failure")
	appendErr := errors.New("append failure")
	cleanupErr := errors.New("cleanup failure")
	openErr := errors.New("open failure")
	partialCleaned := false
	partialBackend := Backend{
		Name:         "partial-open",
		Capabilities: capabilitySet(caseUnderTest.Requires),
		Open: func(context.Context, string) (*Services, error) {
			return &Services{Cleanup: func() error {
				partialCleaned = true
				return cleanupErr
			}}, openErr
		}}
	if _, err := Replay(context.Background(), caseUnderTest, partialBackend); !errors.Is(err, openErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("Replay() partial open error = %v, want open and cleanup failures", err)
	}
	if !partialCleaned {
		t.Fatal("Replay() did not clean partial services returned with an open error")
	}
	tests := []struct {
		name    string
		backend Backend
		want    []error
	}{
		{
			name: "nil services",
			backend: Backend{Name: "nil-services", Open: func(context.Context, string) (*Services, error) {
				return nil, nil
			}},
		},
		{
			name: "create session",
			backend: Backend{Name: "create-failure", Open: func(ctx context.Context, caseName string) (*Services, error) {
				services, err := openBase(ctx, caseName)
				if err != nil {
					return nil, err
				}
				services.Session = &createFailureSessionService{Service: services.Session, err: createErr}
				return services, nil
			}},
			want: []error{createErr},
		},
		{
			name: "cleanup after success",
			backend: Backend{Name: "cleanup-failure", Open: func(ctx context.Context, caseName string) (*Services, error) {
				services, err := openBase(ctx, caseName)
				if err != nil {
					return nil, err
				}
				services.Cleanup = func() error { return cleanupErr }
				return services, nil
			}},
			want: []error{cleanupErr},
		},
		{
			name: "operation and cleanup",
			backend: Backend{Name: "joined-failures", Open: func(ctx context.Context, caseName string) (*Services, error) {
				services, err := openBase(ctx, caseName)
				if err != nil {
					return nil, err
				}
				services.Session = &appendFailureSessionService{Service: services.Session, err: appendErr}
				services.Cleanup = func() error { return cleanupErr }
				return services, nil
			}},
			want: []error{appendErr, cleanupErr},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.backend.Capabilities = capabilitySet(caseUnderTest.Requires)
			_, err := Replay(context.Background(), caseUnderTest, test.backend)
			if err == nil {
				t.Fatal("Replay() unexpectedly succeeded")
			}
			for _, want := range test.want {
				if !errors.Is(err, want) {
					t.Fatalf("Replay() error = %v, want %v", err, want)
				}
			}
		})
	}
	var services *Services
	if err := services.Close(); err != nil {
		t.Fatalf("nil Services.Close() error = %v", err)
	}
}

func TestReplayRejectsNilSessionResults(t *testing.T) {
	tests := []struct {
		name       string
		replayCase Case
		wrap       func(session.Service) session.Service
	}{
		{
			name:       "create session",
			replayCase: PublicCases()[0],
			wrap: func(service session.Service) session.Service {
				return &nilCreateSessionService{Service: service}
			},
		},
		{
			name: "reload session",
			replayCase: Case{
				Name:     "nil-reload",
				Requires: []Capability{CapabilitySession},
				Steps:    []Step{{Name: "reload", Kind: StepReloadSession}},
			},
			wrap: func(service session.Service) session.Service {
				return &nilGetSessionService{Service: service}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := InMemoryBackend()
			open := backend.Open
			backend.Open = func(ctx context.Context, caseName string) (*Services, error) {
				services, err := open(ctx, caseName)
				if err != nil {
					return nil, err
				}
				services.Session = test.wrap(services.Session)
				return services, nil
			}
			if _, err := Replay(context.Background(), test.replayCase, backend); err == nil {
				t.Fatal("Replay() unexpectedly accepted a nil session")
			}
		})
	}
}

func TestWriteReportPropagatesWriterFailure(t *testing.T) {
	err := WriteReport(failingWriter{}, validReferenceReport())
	if err == nil {
		t.Fatal("WriteReport() unexpectedly ignored writer failure")
	}
}

func validReferenceReport() Report {
	return Report{
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
}

func validReportDiff() Diff {
	return Diff{
		Case:      "clean",
		BackendA:  "baseline",
		BackendB:  "actual",
		SessionID: "clean",
		Path:      "/session/id",
	}
}

func setBlockingReportDiff(report *Report, diff Diff) {
	report.PassedCases = 0
	report.FailedCases = 1
	report.BlockingDiffs = 1
	report.Cases[0].Status = StatusFailed
	report.Cases[0].Diffs = []Diff{diff}
}

type createFailureSessionService struct {
	session.Service
	err error
}

func (s *createFailureSessionService) CreateSession(
	context.Context,
	session.Key,
	session.StateMap,
	...session.Option,
) (*session.Session, error) {
	return nil, s.err
}

type appendFailureSessionService struct {
	session.Service
	err error
}

func (s *appendFailureSessionService) AppendEvent(
	context.Context,
	*session.Session,
	*event.Event,
	...session.Option,
) error {
	return s.err
}

type nilCreateSessionService struct {
	session.Service
}

func (*nilCreateSessionService) CreateSession(
	context.Context,
	session.Key,
	session.StateMap,
	...session.Option,
) (*session.Session, error) {
	return nil, nil
}

type nilGetSessionService struct {
	session.Service
}

type cancelAfterAppendService struct {
	session.Service
	cancel context.CancelFunc
}

type cancelAfterAppUpdateService struct {
	session.Service
	cancel      context.CancelFunc
	deleteCalls int
}

func (s *cancelAfterAppUpdateService) UpdateAppState(
	context.Context,
	string,
	session.StateMap,
) error {
	s.cancel()
	return nil
}

func (s *cancelAfterAppUpdateService) DeleteAppState(
	context.Context,
	string,
	string,
) error {
	s.deleteCalls++
	return nil
}

func (s *cancelAfterAppendService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	options ...session.Option,
) error {
	if err := s.Service.AppendEvent(ctx, sess, evt, options...); err != nil {
		return err
	}
	s.cancel()
	return nil
}

func (*nilGetSessionService) GetSession(
	context.Context,
	session.Key,
	...session.Option,
) (*session.Session, error) {
	return nil, nil
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("injected writer failure")
}
