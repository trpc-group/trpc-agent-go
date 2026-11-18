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
	"fmt"
	"math"
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
	unknownAllowedDiffPathCase := validCase
	unknownAllowedDiffPathCase.AllowedDiffs = []AllowedDiff{{
		BackendA: "left",
		BackendB: "right",
		Path:     "/session/not_a_field",
		Rule:     AllowedIgnore,
		Reason:   "unknown path",
	}}
	unknownNestedAllowedDiffPathCases := make([]Case, 0, 3)
	for _, path := range []string{
		"/state/app/*/not_a_field",
		"/summaries/*/not_a_field",
		"/memory_searches/*/*/memory/not_a_field",
	} {
		invalid := validCase
		invalid.AllowedDiffs = []AllowedDiff{{
			BackendA: "left",
			BackendB: "right",
			Path:     path,
			Rule:     AllowedIgnore,
			Reason:   "unknown nested path",
		}}
		unknownNestedAllowedDiffPathCases = append(unknownNestedAllowedDiffPathCases, invalid)
	}
	unknownAllowedDiffBackendCase := validCase
	unknownAllowedDiffBackendCase.AllowedDiffs = []AllowedDiff{{
		BackendA: "typo",
		BackendB: "right",
		Path:     "/events",
		Rule:     AllowedIgnore,
		Reason:   "unknown backend",
	}}
	unknownCapabilityBackend := right
	unknownCapabilityBackend.Name = "unknown-capability"
	unknownCapabilityBackend.Capabilities = PortableCapabilities()
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
		{name: "unknown allowed diff path", cases: []Case{unknownAllowedDiffPathCase}, backends: []Backend{left, right}},
		{name: "unknown state glob suffix", cases: []Case{unknownNestedAllowedDiffPathCases[0]}, backends: []Backend{left, right}},
		{name: "unknown summary glob suffix", cases: []Case{unknownNestedAllowedDiffPathCases[1]}, backends: []Backend{left, right}},
		{name: "unknown memory search glob suffix", cases: []Case{unknownNestedAllowedDiffPathCases[2]}, backends: []Backend{left, right}},
		{name: "unknown allowed diff backend", cases: []Case{unknownAllowedDiffBackendCase}, backends: []Backend{left, right}},
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

func TestRunnerRejectsTooManyBackends(t *testing.T) {
	backends := make([]Backend, 0, maxReplayBackends+1)
	for index := 0; index < maxReplayBackends+1; index++ {
		backend := InMemoryBackend()
		backend.Name = fmt.Sprintf("backend-%03d", index)
		backends = append(backends, backend)
	}
	_, err := (Runner{}).Run(context.Background(), []Case{PublicCases()[0]}, backends)
	if err == nil || !strings.Contains(err.Error(), "exceed limit") {
		t.Fatalf("Run() error = %v, want backend limit error", err)
	}
}

func TestRunnerRejectsTooManyCasesBeforeOpeningBackends(t *testing.T) {
	cases := make([]Case, maxReplayCases+1)
	openCalls := 0
	left := InMemoryBackend()
	left.Name = "left"
	left.Open = func(context.Context, string) (*Services, error) {
		openCalls++
		return nil, errors.New("must not open")
	}
	right := left
	right.Name = "right"
	_, err := (Runner{}).Run(context.Background(), cases, []Backend{left, right})
	if err == nil || !strings.Contains(err.Error(), "cases exceed limit") {
		t.Fatalf("Run() error = %v, want case limit error", err)
	}
	if openCalls != 0 {
		t.Fatalf("Run() opened backends %d times, want 0", openCalls)
	}
}

func TestAllowedDiffValidationRejectsTooManyRules(t *testing.T) {
	err := validateAllowedDiffs(make([]AllowedDiff, maxReplayAllowedDiffs+1))
	if err == nil || !strings.Contains(err.Error(), "allowed_diff rules exceed limit") {
		t.Fatalf("validateAllowedDiffs() error = %v, want rule limit error", err)
	}
}

func TestCompareRejectsTooManyDiffs(t *testing.T) {
	baselineState := make(CanonicalMap, maxReplayDiffsPerCase+1)
	actualState := make(CanonicalMap, maxReplayDiffsPerCase+1)
	for index := 0; index < maxReplayDiffsPerCase+1; index++ {
		key := fmt.Sprintf("key-%05d", index)
		baselineState[key] = 0
		actualState[key] = 1
	}
	_, err := Compare("diff-limit", Snapshot{
		Backend: "left",
		Case:    "diff-limit",
		State:   map[string]CanonicalMap{"session": baselineState},
	}, Snapshot{
		Backend: "right",
		Case:    "diff-limit",
		State:   map[string]CanonicalMap{"session": actualState},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds 10000 diffs") {
		t.Fatalf("Compare() error = %v, want diff limit error", err)
	}
}

func TestCompareSafelyEncodesSnapshots(t *testing.T) {
	t.Run("custom marshaler is rejected before mutating caller snapshot", func(t *testing.T) {
		baseline := minimalSnapshot("baseline", `{}`)
		actual := minimalSnapshot("actual", `{}`)
		baselineValue := &mutatingJSONExportedState{Values: map[string]string{"value": "original"}}
		actualValue := &mutatingJSONExportedState{Values: map[string]string{"value": "original"}}
		baseline.Session["custom"] = baselineValue
		actual.Session["custom"] = actualValue

		_, err := Compare("allowed", baseline, actual, nil)
		if err == nil || !strings.Contains(err.Error(), "cannot be cloned safely") {
			t.Fatalf("Compare() error = %v, want custom marshaler rejection", err)
		}
		if got := baselineValue.Values["value"]; got != "original" {
			t.Fatalf("baseline snapshot value = %q, want original", got)
		}
		if got := actualValue.Values["value"]; got != "original" {
			t.Fatalf("actual snapshot value = %q, want original", got)
		}
	})

	t.Run("opaque custom marshaler is rejected before execution", func(t *testing.T) {
		baseline := minimalSnapshot("baseline", `{}`)
		actual := minimalSnapshot("actual", `{}`)
		executed := false
		baseline.Session["custom"] = &opaqueSnapshotJSON{
			Channel:  make(chan string, 1),
			Executed: &executed,
		}

		_, err := Compare("allowed", baseline, actual, nil)
		if err == nil || !strings.Contains(err.Error(), "cannot be cloned safely") {
			t.Fatalf("Compare() error = %v, want safe-clone rejection", err)
		}
		if executed {
			t.Fatal("Compare() executed an opaque custom marshaler")
		}
	})

	t.Run("custom marshaler alias contract is rejected", func(t *testing.T) {
		baseline := minimalSnapshot("baseline", `{}`)
		actual := minimalSnapshot("actual", `{}`)
		shared := 1
		first, second := 1, 1
		baseline.Session["custom"] = &aliasSensitiveSnapshotJSON{First: &shared, Second: &shared}
		actual.Session["custom"] = &aliasSensitiveSnapshotJSON{First: &first, Second: &second}

		_, err := Compare("allowed", baseline, actual, nil)
		if err == nil || !strings.Contains(err.Error(), "cannot be cloned safely") {
			t.Fatalf("Compare() error = %v, want custom marshaler rejection", err)
		}
	})

	t.Run("slice-sensitive custom marshaler is not executed", func(t *testing.T) {
		baseline := minimalSnapshot("baseline", `{}`)
		actual := minimalSnapshot("actual", `{}`)
		executed := false
		baseline.Session["custom"] = &capacitySensitiveSnapshotJSON{
			Values:   make([]int, 1, 8),
			Executed: &executed,
		}

		_, err := Compare("allowed", baseline, actual, nil)
		if err == nil || !strings.Contains(err.Error(), "cannot be cloned safely") {
			t.Fatalf("Compare() error = %v, want custom marshaler rejection", err)
		}
		if executed {
			t.Fatal("Compare() executed a slice-sensitive custom marshaler")
		}
	})

	t.Run("total encoded size is bounded", func(t *testing.T) {
		baseline := minimalSnapshot("baseline", `{}`)
		actual := minimalSnapshot("actual", `{}`)
		baseline.Session["oversized"] = strings.Repeat("x", maxReplaySnapshotSize)

		_, err := Compare("allowed", baseline, actual, nil)
		if err == nil || !strings.Contains(err.Error(), "snapshot exceeds") {
			t.Fatalf("Compare() error = %v, want snapshot size limit", err)
		}
	})
}

func TestReplayRejectsOversizedAggregateSnapshot(t *testing.T) {
	replayCase := singleTurnCase()
	replayCase.Name = "oversized-aggregate-snapshot"
	replayCase.Requires = append(replayCase.Requires, CapabilitySessionState)
	replayCase.InitialState = make(session.StateMap)
	for index := 0; index < 8; index++ {
		replayCase.InitialState[fmt.Sprintf("state-%d", index)] = []byte(`"` + strings.Repeat("s", 800_000) + `"`)
	}
	replayCase.Steps = replayCase.Steps[:1]
	replayCase.Steps[0].Event.Event.Response.Choices[0].Message.Content = strings.Repeat("e", 2_500_000)

	if _, err := Replay(context.Background(), replayCase, InMemoryBackend()); err == nil ||
		!strings.Contains(err.Error(), "snapshot exceeds") {
		t.Fatalf("Replay() error = %v, want aggregate snapshot size rejection", err)
	}
}

func TestInMemoryBackendOpenHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	services, err := InMemoryBackend().Open(ctx, "canceled")
	if services != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Open() = (%v, %v), want (nil, context.Canceled)", services, err)
	}
}

func TestInjectFaultDoesNotMutateSnapshotCustomMarshaler(t *testing.T) {
	input := minimalSnapshot("backend", `{}`)
	value := &mutatingJSONExportedState{Values: map[string]string{"value": "original"}}
	input.Session["custom"] = value

	if _, err := InjectFault(input, FaultStateValue); err == nil ||
		!strings.Contains(err.Error(), "cannot be cloned safely") {
		t.Fatalf("InjectFault() error = %v, want custom marshaler rejection", err)
	}
	if got := value.Values["value"]; got != "original" {
		t.Fatalf("input snapshot value = %q, want original", got)
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
	backend = InMemoryBackend()
	backend.Name = string([]byte{0xff})
	if _, err := Replay(context.Background(), replayCase, backend); err == nil ||
		!strings.Contains(err.Error(), "invalid UTF-8") {
		t.Fatalf("Replay() backend name error = %v, want invalid UTF-8", err)
	}
}

func TestReplayRejectsTypedNilServicesWithoutPanicking(t *testing.T) {
	var typedNilSession *typedNilSessionService
	backend := Backend{
		Name:         "typed-nil-session",
		Capabilities: Capabilities{CapabilitySession: true},
		Open: func(context.Context, string) (*Services, error) {
			return &Services{Session: typedNilSession}, nil
		},
	}
	if _, err := Replay(context.Background(), PublicCases()[0], backend); err == nil ||
		!strings.Contains(err.Error(), "incomplete services") {
		t.Fatalf("Replay() error = %v, want incomplete services", err)
	}

	var typedNilMemory *typedNilMemoryService
	services := &Services{Memory: typedNilMemory}
	if err := services.Close(); err != nil {
		t.Fatalf("Services.Close() error = %v, want nil", err)
	}
}

func TestConfigurationIdentifiersRequireValidUTF8(t *testing.T) {
	invalid := string([]byte{0xff})
	tests := []struct {
		name   string
		mutate func(*Case)
	}{
		{name: "case name", mutate: func(replayCase *Case) { replayCase.Name = invalid }},
		{name: "step name", mutate: func(replayCase *Case) { replayCase.Steps[0].Name = invalid }},
		{name: "logical event id", mutate: func(replayCase *Case) { replayCase.Steps[0].Event.LogicalID = invalid }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			replayCase := PublicCases()[0]
			test.mutate(&replayCase)
			if err := validateCase(replayCase); err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
				t.Fatalf("validateCase() error = %v, want invalid UTF-8", err)
			}
		})
	}
	if err := validateAllowedDiffs([]AllowedDiff{{
		BackendA: "left",
		BackendB: "right",
		Path:     "/events",
		Rule:     AllowedIgnore,
		Reason:   invalid,
	}}); err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Fatalf("validateAllowedDiffs() error = %v, want invalid UTF-8", err)
	}
}

func TestConfigurationStringsHaveDefensiveLimits(t *testing.T) {
	oversizedIdentifier := strings.Repeat("x", maxReplayIdentifierSize+1)
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "backend name", run: func() error {
			backend := InMemoryBackend()
			backend.Name = oversizedIdentifier
			return validateBackend(backend)
		}},
		{name: "case name", run: func() error {
			candidate := PublicCases()[0]
			candidate.Name = oversizedIdentifier
			return validateCase(candidate)
		}},
		{name: "case description", run: func() error {
			candidate := PublicCases()[0]
			candidate.Description = strings.Repeat("x", maxReplayExplanationSize+1)
			return validateCase(candidate)
		}},
		{name: "event order", run: func() error {
			candidate := PublicCases()[0]
			candidate.EventOrder = EventOrderMode(oversizedIdentifier)
			return validateCase(candidate)
		}},
		{name: "required capability", run: func() error {
			candidate := PublicCases()[0]
			candidate.Requires = []Capability{Capability(oversizedIdentifier)}
			return validateCase(candidate)
		}},
		{name: "step name", run: func() error {
			candidate := PublicCases()[0]
			candidate.Steps[0].Name = oversizedIdentifier
			return validateCase(candidate)
		}},
		{name: "step kind", run: func() error {
			candidate := PublicCases()[0]
			candidate.Steps[0].Kind = StepKind(oversizedIdentifier)
			return validateCase(candidate)
		}},
		{name: "recovery mode", run: func() error {
			candidate := PublicCases()[0]
			candidate.Steps[0].Recovery = RecoveryMode(oversizedIdentifier)
			return validateCase(candidate)
		}},
		{name: "allowed backend", run: func() error {
			return validateAllowedDiffs([]AllowedDiff{{
				BackendA: oversizedIdentifier,
				BackendB: "right",
				Path:     "/events",
				Rule:     AllowedIgnore,
				Reason:   "bounded identifier",
			}})
		}},
		{name: "allowed path", run: func() error {
			return validateAllowedDiffs([]AllowedDiff{{
				BackendA: "left",
				BackendB: "right",
				Path:     "/" + strings.Repeat("x", maxReplayPathSize),
				Rule:     AllowedIgnore,
				Reason:   "bounded path",
			}})
		}},
		{name: "allowed reason", run: func() error {
			return validateAllowedDiffs([]AllowedDiff{{
				BackendA: "left",
				BackendB: "right",
				Path:     "/events",
				Rule:     AllowedIgnore,
				Reason:   strings.Repeat("x", maxReplayExplanationSize+1),
			}})
		}},
		{name: "allowed rule", run: func() error {
			return validateAllowedDiffs([]AllowedDiff{{
				BackendA: "left",
				BackendB: "right",
				Path:     "/events",
				Rule:     AllowedRule(oversizedIdentifier),
				Reason:   "bounded rule",
			}})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); err == nil || !strings.Contains(err.Error(), "exceeds") {
				t.Fatalf("validation error = %v, want size limit", err)
			}
		})
	}
}

func TestExecutionFailureDiffReadsAndBoundsErrorOnce(t *testing.T) {
	unstable := &countingError{}
	diff := executionFailureDiff("case", "backend", ComparisonConsensus, "", unstable)
	if unstable.calls != 1 {
		t.Fatalf("error Error() calls = %d, want 1", unstable.calls)
	}
	actual, ok := diff.Actual.(string)
	if !ok || diff.Exclusion == nil || actual != diff.Exclusion.Error {
		t.Fatalf("execution evidence contains inconsistent error text: %+v", diff)
	}

	large := executionFailureDiff(
		"case",
		"backend",
		ComparisonConsensus,
		"",
		errors.New(strings.Repeat("x", maxReplayErrorSize+1)),
	)
	message, ok := large.Actual.(string)
	if !ok || len(message) > maxReplayErrorSize || !strings.HasSuffix(message, truncatedErrorSuffix) {
		t.Fatalf("bounded execution error length/suffix = %d/%q", len(message), message)
	}
}

func TestReplayRejectsMissingRequiredCapabilities(t *testing.T) {
	replayCase := memoryCase()
	backend := missingCapabilityBackend("missing-memory", CapabilityMemory)
	openCalls := 0
	backend.Open = func(context.Context, string) (*Services, error) {
		openCalls++
		return nil, errors.New("open must not be called")
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
		Capabilities: PortableCapabilities(),
		Open: func(context.Context, string) (*Services, error) {
			openCalls++
			return nil, errors.New("open must not be called")
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

func TestRunnerPreservesCleanupFailureOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleanupErr := errors.New("injected cleanup failure")
	first := InMemoryBackend()
	first.Name = "canceling"
	open := first.Open
	first.Open = func(ctx context.Context, caseName string) (*Services, error) {
		services, err := open(ctx, caseName)
		if err == nil {
			services.Cleanup = func() error {
				cancel()
				return cleanupErr
			}
		}
		return services, err
	}
	second := InMemoryBackend()
	second.Name = "not-opened"
	secondOpenCalls := 0
	secondOpen := second.Open
	second.Open = func(ctx context.Context, caseName string) (*Services, error) {
		secondOpenCalls++
		return secondOpen(ctx, caseName)
	}

	report, err := (Runner{}).Run(ctx, PublicCases()[:1], []Backend{first, second})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, cleanupErr) {
		t.Fatalf("Run() error = %v, want cancellation and cleanup failures", err)
	}
	if !reflect.DeepEqual(report, Report{}) {
		t.Fatalf("Run() report = %#v, want no partial report", report)
	}
	if secondOpenCalls != 0 {
		t.Fatalf("Run() opened backend after cancellation %d times", secondOpenCalls)
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

	t.Run("event structured output", func(t *testing.T) {
		inputEvent := event.New("invocation", "author")
		inputEvent.StructuredOutput = map[string]any{
			"items": []any{map[string]any{"value": "original"}},
		}
		exec := execution{session: &session.Session{CreatedAt: caseEpoch}}
		prepared, err := exec.prepareEvent(&EventInput{
			LogicalID: "logical-event",
			Event:     inputEvent,
		})
		if err != nil {
			t.Fatalf("prepareEvent() error = %v", err)
		}
		prepared.StructuredOutput.(map[string]any)["items"].([]any)[0].(map[string]any)["value"] = "changed"
		got := inputEvent.StructuredOutput.(map[string]any)["items"].([]any)[0].(map[string]any)["value"]
		if got != "original" {
			t.Fatalf("prepareEvent() mutated structured output = %q", got)
		}
	})

	t.Run("event response graph", func(t *testing.T) {
		type extraFixture struct {
			Values []string `json:"values"`
		}
		text := "text"
		finishReason := "stop"
		index := 1
		param := "parameter"
		code := "code"
		inputEvent := event.NewResponseEvent("invocation", "author", &model.Response{
			Choices: []model.Choice{{
				FinishReason: &finishReason,
				Message: model.Message{
					ContentParts: []model.ContentPart{{
						Text:       &text,
						Image:      &model.Image{Data: []byte("image")},
						Audio:      &model.Audio{Data: []byte("audio")},
						Video:      &model.Video{Data: []byte("video"), Format: "mp4"},
						File:       &model.File{Data: []byte("file")},
						ContentRef: &model.ContentRef{RequestID: "request"},
					}},
					ToolCalls: []model.ToolCall{{
						Index: &index,
						Function: model.FunctionDefinitionParam{
							Arguments: []byte(`{"value":"original"}`),
						},
						ExtraFields: map[string]any{
							"nested":      []any{map[string]any{"value": "original"}},
							"array":       [1]map[string]any{{"value": "original"}},
							"structured":  &extraFixture{Values: []string{"original"}},
							"bytes":       []byte("bytes"),
							"nil":         nil,
							"nil-map":     map[string]any(nil),
							"nil-slice":   []any(nil),
							"empty-map":   map[string]any{},
							"empty-slice": []any{},
						},
					}},
				},
			}},
			Error: &model.ResponseError{Param: &param, Code: &code},
		})
		inputEvent.ParentMetadata = &event.ParentInvocationMetadata{TriggerID: "original"}
		exec := execution{session: &session.Session{CreatedAt: caseEpoch}}
		prepared, err := exec.prepareEvent(&EventInput{
			LogicalID: "logical-event",
			Event:     inputEvent,
		})
		if err != nil {
			t.Fatalf("prepareEvent() error = %v", err)
		}
		prepared.ParentMetadata.TriggerID = "changed"
		choice := &prepared.Response.Choices[0]
		*choice.FinishReason = "changed"
		*choice.Message.ContentParts[0].Text = "changed"
		choice.Message.ContentParts[0].Image.Data[0] = 'x'
		choice.Message.ContentParts[0].Audio.Data[0] = 'x'
		choice.Message.ContentParts[0].Video.Data[0] = 'x'
		choice.Message.ContentParts[0].Video.Format = "changed"
		choice.Message.ContentParts[0].File.Data[0] = 'x'
		choice.Message.ContentParts[0].ContentRef.RequestID = "changed"
		choice.Message.ToolCalls[0].Function.Arguments[0] = 'x'
		*choice.Message.ToolCalls[0].Index = 2
		choice.Message.ToolCalls[0].ExtraFields["nested"].([]any)[0].(map[string]any)["value"] = "changed"
		choice.Message.ToolCalls[0].ExtraFields["array"].([1]map[string]any)[0]["value"] = "changed"
		choice.Message.ToolCalls[0].ExtraFields["structured"].(*extraFixture).Values[0] = "changed"
		choice.Message.ToolCalls[0].ExtraFields["bytes"].([]byte)[0] = 'x'
		*prepared.Response.Error.Param = "changed"
		*prepared.Response.Error.Code = "changed"

		originalChoice := &inputEvent.Response.Choices[0]
		nilValue, hasNil := choice.Message.ToolCalls[0].ExtraFields["nil"]
		if inputEvent.ParentMetadata.TriggerID != "original" ||
			*originalChoice.FinishReason != "stop" ||
			*originalChoice.Message.ContentParts[0].Text != "text" ||
			string(originalChoice.Message.ContentParts[0].Image.Data) != "image" ||
			string(originalChoice.Message.ContentParts[0].Audio.Data) != "audio" ||
			string(originalChoice.Message.ContentParts[0].Video.Data) != "video" ||
			originalChoice.Message.ContentParts[0].Video.Format != "mp4" ||
			string(originalChoice.Message.ContentParts[0].File.Data) != "file" ||
			originalChoice.Message.ContentParts[0].ContentRef.RequestID != "request" ||
			string(originalChoice.Message.ToolCalls[0].Function.Arguments) != `{"value":"original"}` ||
			*originalChoice.Message.ToolCalls[0].Index != 1 ||
			originalChoice.Message.ToolCalls[0].ExtraFields["nested"].([]any)[0].(map[string]any)["value"] != "original" ||
			originalChoice.Message.ToolCalls[0].ExtraFields["array"].([1]map[string]any)[0]["value"] != "original" ||
			originalChoice.Message.ToolCalls[0].ExtraFields["structured"].(*extraFixture).Values[0] != "original" ||
			string(originalChoice.Message.ToolCalls[0].ExtraFields["bytes"].([]byte)) != "bytes" ||
			!hasNil || nilValue != nil ||
			choice.Message.ToolCalls[0].ExtraFields["nil-map"].(map[string]any) != nil ||
			choice.Message.ToolCalls[0].ExtraFields["nil-slice"].([]any) != nil ||
			choice.Message.ToolCalls[0].ExtraFields["empty-map"].(map[string]any) == nil ||
			len(choice.Message.ToolCalls[0].ExtraFields["empty-map"].(map[string]any)) != 0 ||
			choice.Message.ToolCalls[0].ExtraFields["empty-slice"].([]any) == nil ||
			len(choice.Message.ToolCalls[0].ExtraFields["empty-slice"].([]any)) != 0 ||
			*inputEvent.Response.Error.Param != "parameter" ||
			*inputEvent.Response.Error.Code != "code" {
			t.Fatalf("prepareEvent() retained input ownership: %#v", inputEvent)
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

func TestRunnerIsolatesEventInputsBetweenBackends(t *testing.T) {
	step := responseEvent("tool-call", 1, "assistant", model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					Function: model.FunctionDefinitionParam{
						Name:      "tool",
						Arguments: []byte(`{"value":"original"}`),
					},
				}},
			},
		}},
	})
	replayCase := Case{
		Name:     "backend-event-isolation",
		Requires: []Capability{CapabilitySession},
		Steps: []Step{
			messageStep("user", "user", 0, "user", model.RoleUser, "run tool", ""),
			step,
		},
	}
	mutating := InMemoryBackend()
	mutating.Name = "mutating"
	open := mutating.Open
	mutating.Open = func(ctx context.Context, caseName string) (*Services, error) {
		services, err := open(ctx, caseName)
		if err == nil {
			services.Session = &mutatingAppendSessionService{Service: services.Session}
		}
		return services, err
	}
	baseline := InMemoryBackend()
	baseline.Name = "baseline"

	mutatingSnapshot, err := Replay(context.Background(), replayCase, mutating)
	if err != nil {
		t.Fatalf("Replay() mutating backend error = %v", err)
	}
	baselineSnapshot, err := Replay(context.Background(), replayCase, baseline)
	if err != nil {
		t.Fatalf("Replay() baseline backend error = %v", err)
	}
	mutatingArguments := normalizedToolArguments(t, mutatingSnapshot.Events[1], "message")
	baselineArguments := normalizedToolArguments(t, baselineSnapshot.Events[1], "message")
	if mutatingArguments == baselineArguments {
		t.Fatalf("Replay() arguments unexpectedly match: mutating=%q baseline=%q", mutatingArguments, baselineArguments)
	}

	report, err := (Runner{}).Run(context.Background(), []Case{replayCase}, []Backend{mutating, baseline})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if report.BlockingDiffs == 0 {
		t.Fatalf("Run() report = %#v, want backend mutation to remain visible", report)
	}
	arguments := replayCase.Steps[1].Event.Event.Response.Choices[0].Message.ToolCalls[0].Function.Arguments
	if string(arguments) != `{"value":"original"}` {
		t.Fatalf("Run() mutated case input arguments = %q", arguments)
	}
}

func TestRunnerRejectsCustomJSONMarshalerWithUnclonableState(t *testing.T) {
	custom := &customJSONHiddenState{
		Visible: "original",
		hidden:  map[string]string{"value": "original"},
	}
	embedded := &embeddedCustomJSONState{
		embeddedCustomJSONFields{Custom: custom},
	}
	sharedChannel := make(chan string, 1)
	sharedChannel <- "original"
	channelState := &customJSONChannelState{Channel: sharedChannel}
	step := responseEvent("custom-json", 1, "assistant", model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ExtraFields: map[string]any{
						"channel":  channelState,
						"custom":   custom,
						"embedded": embedded,
					},
				}},
			},
		}},
	})
	replayCase := Case{
		Name:     "backend-custom-json-isolation",
		Requires: []Capability{CapabilitySession},
		Steps: []Step{
			messageStep("user", "user", 0, "user", model.RoleUser, "run", ""),
			step,
		},
	}

	err := validateCase(replayCase)
	if err == nil || !strings.Contains(err.Error(), "cannot be cloned safely") {
		t.Fatalf("validateCase() error = %v, want unclonable custom marshaler rejection", err)
	}
	if custom.hidden["value"] != "original" {
		t.Fatalf("validateCase() mutated custom marshaler input = %#v", custom.hidden)
	}
	if len(sharedChannel) != 1 {
		t.Fatalf("validateCase() consumed custom marshaler channel, length = %d", len(sharedChannel))
	}
}

func TestCaseValidationDoesNotMutateCustomJSONMarshalerInput(t *testing.T) {
	custom := &mutatingJSONExportedState{
		Values: map[string]string{"value": "original"},
	}
	step := responseEvent("mutating-json", 1, "assistant", model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type:        "function",
					ExtraFields: map[string]any{"custom": custom},
				}},
			},
		}},
	})
	replayCase := Case{
		Name:     "mutating-json-input",
		Requires: []Capability{CapabilitySession},
		Steps:    []Step{step},
	}
	err := validateCase(replayCase)
	if err == nil || !strings.Contains(err.Error(), "cannot be cloned safely") {
		t.Fatalf("validateCase() error = %v, want custom marshaler rejection", err)
	}
	if custom.Values["value"] != "original" {
		t.Fatalf("validateCase() mutated custom marshaler input = %#v", custom.Values)
	}
}

func TestRunnerRejectsCustomTextMarshalerWithUnclonableState(t *testing.T) {
	custom := &customTextHiddenState{hidden: map[string]string{"value": "original"}}
	step := responseEvent("custom-text", 1, "assistant", model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ExtraFields: map[string]any{
						"custom":     custom,
						"custom-key": map[*customTextHiddenState]string{custom: "value"},
					},
				}},
			},
		}},
	})
	replayCase := Case{
		Name:     "backend-custom-text-isolation",
		Requires: []Capability{CapabilitySession},
		Steps: []Step{
			messageStep("user", "user", 0, "user", model.RoleUser, "run", ""),
			step,
		},
	}

	err := validateCase(replayCase)
	if err == nil || !strings.Contains(err.Error(), "cannot be cloned safely") {
		t.Fatalf("validateCase() error = %v, want unclonable custom marshaler rejection", err)
	}
	if custom.hidden["value"] != "original" {
		t.Fatalf("validateCase() mutated custom text marshaler input = %#v", custom.hidden)
	}
}

func TestCloneToolCallExtraFieldsPreservesSafeMarshalerTypes(t *testing.T) {
	input := map[string]any{
		"raw":      json.RawMessage(`{"value":"raw"}`),
		"time":     time.Unix(123, 456).UTC(),
		"json-key": map[jsonOnlyMapKey]string{1: "value"},
	}
	cloned, err := cloneToolCallExtraFields(input)
	if err != nil {
		t.Fatalf("cloneToolCallExtraFields() error = %v", err)
	}
	if _, ok := cloned["raw"].(json.RawMessage); !ok {
		t.Fatalf("raw message type = %T, want json.RawMessage", cloned["raw"])
	}
	if _, ok := cloned["time"].(time.Time); !ok {
		t.Fatalf("time type = %T, want time.Time", cloned["time"])
	}
	if _, ok := cloned["json-key"].(map[jsonOnlyMapKey]string); !ok {
		t.Fatalf("json map-key type = %T, want map[jsonOnlyMapKey]string", cloned["json-key"])
	}
	if _, err := cloneToolCallExtraFields(map[string]any{
		"text-key": map[textMapKey]string{"key": "value"},
	}); err == nil || !strings.Contains(err.Error(), "cannot be cloned safely") {
		t.Fatalf("cloneToolCallExtraFields() error = %v, want custom map-key marshaler rejection", err)
	}
}

func TestEventExtraFieldsRejectCyclicJSONValues(t *testing.T) {
	cyclic := &cyclicJSONValue{}
	cyclic.Next = cyclic
	step := responseEvent("cyclic-extra-fields", 1, "assistant", model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type:        "function",
					ExtraFields: map[string]any{"cyclic": cyclic},
				}},
			},
		}},
	})
	replayCase := Case{
		Name:     "cyclic-extra-fields",
		Requires: []Capability{CapabilitySession},
		Steps:    []Step{step},
	}
	if err := validateCase(replayCase); err == nil || !strings.Contains(err.Error(), "cyclic JSON data") {
		t.Fatalf("validateCase() error = %v, want cyclic JSON data", err)
	}
}

func TestEventExtraFieldsRejectCycleBeforeCustomMarshal(t *testing.T) {
	cyclic := &recursiveCycleJSONValue{}
	cyclic.Next = cyclic
	step := responseEvent("recursive-cycle-extra-fields", 1, "assistant", model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type:        "function",
					ExtraFields: map[string]any{"cyclic": cyclic},
				}},
			},
		}},
	})
	replayCase := Case{
		Name:     "recursive-cycle-extra-fields",
		Requires: []Capability{CapabilitySession},
		Steps:    []Step{step},
	}
	if err := validateCase(replayCase); err == nil || !strings.Contains(err.Error(), "cyclic JSON data") {
		t.Fatalf("validateCase() error = %v, want cycle rejection before custom marshal", err)
	}
}

func TestValidateJSONValueRejectsHiddenCustomMarshalCycle(t *testing.T) {
	cyclic := &recursiveHiddenCycleJSONValue{}
	cyclic.Next = cyclic
	err := validateJSONValue("hidden cycle", map[string]any{"value": cyclic})
	if err == nil || !strings.Contains(err.Error(), "cyclic JSON data") {
		t.Fatalf("validateJSONValue() error = %v, want cyclic JSON data", err)
	}
}

func TestValidateJSONValueAcceptsOverlappingAcyclicSlices(t *testing.T) {
	value := make([]any, 1)
	value[0] = value[:0]
	if err := validateJSONValue("overlapping slices", value); err != nil {
		t.Fatalf("validateJSONValue() error = %v, want valid acyclic JSON", err)
	}
}

func TestValidateJSONValueRejectsExcessiveNesting(t *testing.T) {
	var value any = "leaf"
	for index := 0; index <= maxReplayJSONDepth; index++ {
		value = []any{value}
	}
	err := validateJSONValue("nested value", value)
	if err == nil || !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("validateJSONValue() error = %v, want nesting depth limit", err)
	}
}

func TestDecodeJSONRejectsExcessiveNesting(t *testing.T) {
	raw := make([]byte, 0, maxReplayJSONDepth*2+1)
	for index := 0; index <= maxReplayJSONDepth; index++ {
		raw = append(raw, '[')
	}
	raw = append(raw, '0')
	for index := 0; index <= maxReplayJSONDepth; index++ {
		raw = append(raw, ']')
	}
	var value any
	err := decodeJSON(raw, &value)
	if err == nil || !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("decodeJSON() error = %v, want nesting depth limit", err)
	}
}

func TestCompareRejectsExcessiveSnapshotNesting(t *testing.T) {
	var value any = "leaf"
	for index := 0; index <= maxReplayJSONDepth; index++ {
		value = map[string]any{"nested": value}
	}
	baseline := minimalSnapshot("baseline", "1")
	actual := minimalSnapshot("actual", "1")
	baseline.Session["nested"] = value
	if _, err := Compare("allowed", baseline, actual, nil); err == nil ||
		!strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("Compare() error = %v, want nesting depth limit", err)
	}
}

func TestReplayRejectsNilMemorySearchResult(t *testing.T) {
	backend := InMemoryBackend()
	open := backend.Open
	backend.Open = func(ctx context.Context, name string) (*Services, error) {
		services, err := open(ctx, name)
		if err != nil {
			return services, err
		}
		services.Memory = &nilMemorySearchService{Service: services.Memory}
		return services, nil
	}
	_, err := Replay(context.Background(), Case{
		Name:     "nil-memory-search-result",
		Requires: []Capability{CapabilitySession, CapabilityMemory, CapabilityMemorySearch},
		Steps:    []Step{{Name: "search", Kind: StepSearchMemory, MemorySearch: &MemorySearchInput{Query: "query"}}},
	}, backend)
	if err == nil || !strings.Contains(err.Error(), "memory search") {
		t.Fatalf("Replay() error = %v, want nil memory search result error", err)
	}
}

func TestValidateCaseRejectsInvalidMemorySearchOptions(t *testing.T) {
	after := caseEpoch.Add(time.Hour)
	before := caseEpoch
	tests := []struct {
		name    string
		options memory.SearchOptions
		want    string
	}{
		{name: "unknown kind", options: memory.SearchOptions{Kind: "profile"}, want: "unknown kind"},
		{name: "negative max results", options: memory.SearchOptions{MaxResults: -1}, want: "max results"},
		{name: "excessive max results", options: memory.SearchOptions{MaxResults: maxReplayMemories + 1}, want: "max results"},
		{name: "NaN threshold", options: memory.SearchOptions{SimilarityThreshold: math.NaN()}, want: "similarity threshold"},
		{name: "infinite threshold", options: memory.SearchOptions{SimilarityThreshold: math.Inf(1)}, want: "similarity threshold"},
		{name: "negative threshold", options: memory.SearchOptions{SimilarityThreshold: -0.1}, want: "similarity threshold"},
		{name: "threshold above one", options: memory.SearchOptions{SimilarityThreshold: 1.1}, want: "similarity threshold"},
		{name: "reversed time range", options: memory.SearchOptions{TimeAfter: &after, TimeBefore: &before}, want: "time range"},
		{name: "negative hybrid RRF k", options: memory.SearchOptions{HybridRRFK: -1}, want: "hybrid RRF k"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateCase(Case{
				Name:     "invalid-memory-search-options",
				Requires: []Capability{CapabilitySession, CapabilityMemory, CapabilityMemorySearch},
				Steps: []Step{{
					Name: "search",
					Kind: StepSearchMemory,
					MemorySearch: &MemorySearchInput{
						Query:   "query",
						Options: test.options,
					},
				}},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateCase() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReplayRejectsMemorySearchResultAboveRequestedLimit(t *testing.T) {
	backend := InMemoryBackend()
	open := backend.Open
	backend.Open = func(ctx context.Context, name string) (*Services, error) {
		services, err := open(ctx, name)
		if err != nil {
			return services, err
		}
		services.Memory = &ignoringMemorySearchLimitService{Service: services.Memory}
		return services, nil
	}
	replayCase := Case{
		Name:     "memory-search-limit",
		Requires: []Capability{CapabilitySession, CapabilityMemory, CapabilityMemorySearch},
		Steps: []Step{
			{Name: "first", Kind: StepAddMemory, Memory: &MemoryInput{Memory: "first"}},
			{Name: "second", Kind: StepAddMemory, Memory: &MemoryInput{Memory: "second"}},
			{Name: "search", Kind: StepSearchMemory, MemorySearch: &MemorySearchInput{
				Query:   "memory",
				Options: memory.SearchOptions{MaxResults: 1},
			}},
		},
	}
	if _, err := Replay(context.Background(), replayCase, backend); err == nil ||
		!strings.Contains(err.Error(), "requested limit is 1") {
		t.Fatalf("Replay() error = %v, want requested result limit rejection", err)
	}
}

func TestNormalizeMemorySearchesRejectsExcessiveResults(t *testing.T) {
	_, err := normalizeMemorySearches(map[string][]*memory.Entry{
		"oversized": make([]*memory.Entry, maxReplayMemories+1),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "total results") {
		t.Fatalf("normalizeMemorySearches() error = %v, want result limit rejection", err)
	}
}

func TestValidateCaseRejectsOversizedStateValue(t *testing.T) {
	tooLarge := make([]byte, maxReplayStateValueSize+1)
	err := validateCase(Case{
		Name:     "oversized-state",
		Requires: []Capability{CapabilitySession, CapabilitySessionState},
		Steps: []Step{{Name: "write", Kind: StepUpdateState, State: &StateInput{
			Scope:  StateScopeSession,
			Values: session.StateMap{"large": tooLarge},
		}}},
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("validateCase() error = %v, want size limit error", err)
	}
}

func TestValidateCaseBoundsInjectedLogicalEventID(t *testing.T) {
	step := responseEvent("small", 1, "assistant", model.Response{
		Done:    true,
		Choices: []model.Choice{{Message: model.NewAssistantMessage("ok")}},
	})
	// Control characters expand to six-byte JSON escapes. Keeping the raw ID
	// below the event limit proves validation measures the final persisted event.
	step.Event.LogicalID = strings.Repeat("\x01", maxReplayEventSize/4)
	err := validateCase(Case{
		Name:     "oversized-injected-logical-id",
		Requires: []Capability{CapabilitySession},
		Steps:    []Step{step},
	})
	if err == nil || !strings.Contains(err.Error(), "event exceeds") {
		t.Fatalf("validateCase() error = %v, want final event size limit", err)
	}
}

func TestValidateCaseRejectsAggregateInputBounds(t *testing.T) {
	t.Run("state values", func(t *testing.T) {
		values := make(session.StateMap)
		for index := 0; index < maxReplayStateTotalSize/maxReplayStateValueSize+1; index++ {
			values[fmt.Sprintf("key-%d", index)] = make([]byte, maxReplayStateValueSize)
		}
		err := validateCase(Case{
			Name:     "aggregate-state-input",
			Requires: []Capability{CapabilitySession, CapabilitySessionState},
			Steps: []Step{{Name: "write", Kind: StepUpdateState, State: &StateInput{
				Scope: StateScopeSession, Values: values,
			}}},
		})
		if err == nil || !strings.Contains(err.Error(), "state values") {
			t.Fatalf("validateCase() error = %v, want aggregate state limit", err)
		}
	})

	t.Run("event state delta", func(t *testing.T) {
		values := make(session.StateMap)
		for index := 0; index < maxReplayStateTotalSize/maxReplayStateValueSize+1; index++ {
			values[fmt.Sprintf("key-%d", index)] = make([]byte, maxReplayStateValueSize)
		}
		step := responseEvent("aggregate-event", 1, "user", model.Response{
			Done:    true,
			Choices: []model.Choice{{Message: model.NewUserMessage("hello")}},
		})
		step.Event.Event.StateDelta = values
		err := validateCase(Case{
			Name:     "aggregate-event-state-input",
			Requires: []Capability{CapabilitySession, CapabilitySessionState},
			Steps:    []Step{step},
		})
		if err == nil || !strings.Contains(err.Error(), "state delta values") {
			t.Fatalf("validateCase() error = %v, want aggregate event state limit", err)
		}
	})

	t.Run("state value and delete operations", func(t *testing.T) {
		deleteKeys := make([]string, maxReplayStateKeyCount)
		for index := range deleteKeys {
			deleteKeys[index] = fmt.Sprintf("delete-%d", index)
		}
		err := validateCase(Case{
			Name:     "aggregate-state-key-operations",
			Requires: []Capability{CapabilitySession, CapabilityAppState},
			Steps: []Step{{Name: "write-delete", Kind: StepUpdateState, State: &StateInput{
				Scope: StateScopeApp, Values: session.StateMap{"value": nil}, DeleteKeys: deleteKeys,
			}}},
		})
		if err == nil || !strings.Contains(err.Error(), "state key operations") {
			t.Fatalf("validateCase() error = %v, want aggregate state key operation limit", err)
		}
	})

	t.Run("memory metadata", func(t *testing.T) {
		topics := make([]string, maxReplayStateTotalSize/maxReplayMemorySize+2)
		for index := range topics {
			topics[index] = strings.Repeat("t", maxReplayMemorySize/2)
		}
		err := validateCase(Case{
			Name:     "aggregate-memory-input",
			Requires: []Capability{CapabilitySession, CapabilityMemory},
			Steps: []Step{{Name: "memory", Kind: StepAddMemory, Memory: &MemoryInput{
				Memory: "memory", Topics: topics,
			}}},
		})
		if err == nil || !strings.Contains(err.Error(), "metadata") {
			t.Fatalf("validateCase() error = %v, want aggregate memory metadata limit", err)
		}
	})
}

func TestNormalizeRejectsOversizedBackendOutput(t *testing.T) {
	t.Run("unrequested domains are not copied", func(t *testing.T) {
		sess := session.NewSession("replaytest", "user", "unrequested-domains")
		sess.Tracks = map[session.Track]*session.TrackEvents{"invalid": nil}
		sess.Summaries = map[string]*session.Summary{"invalid": nil}
		sess.State = session.StateMap{
			"ignored": make([]byte, maxReplayStateValueSize+1),
		}

		if _, err := normalizeSnapshot(
			"backend", "unrequested-domains", EventOrderGlobal, nil,
			Capabilities{CapabilitySession: true},
			nil, sess, nil, nil, nil, nil,
		); err != nil {
			t.Fatalf("normalizeSnapshot() touched an unrequested domain: %v", err)
		}
	})

	t.Run("session state value", func(t *testing.T) {
		sess := &session.Session{State: session.StateMap{
			"large": make([]byte, maxReplayStateValueSize+1),
		}}
		_, err := normalizeSnapshot(
			"backend", "oversized-state", EventOrderGlobal, nil,
			Capabilities{CapabilitySession: true, CapabilitySessionState: true},
			nil, sess, nil, nil, nil, nil,
		)
		if err == nil || !strings.Contains(err.Error(), "session state key") {
			t.Fatalf("normalizeSnapshot() error = %v, want session state output limit", err)
		}
	})

	t.Run("encoded session state value", func(t *testing.T) {
		sess := &session.Session{State: session.StateMap{
			"large": bytes.Repeat([]byte{0xff}, maxReplayStateValueSize),
		}}
		_, err := normalizeSnapshot(
			"backend", "encoded-oversized-state", EventOrderGlobal, nil,
			Capabilities{CapabilitySession: true, CapabilitySessionState: true},
			nil, sess, nil, nil, nil, nil,
		)
		if err == nil || !strings.Contains(err.Error(), "normalized output") {
			t.Fatalf("normalizeSnapshot() error = %v, want normalized state output limit", err)
		}
	})

	t.Run("state key bounds", func(t *testing.T) {
		longKey := strings.Repeat("k", maxReplayStateKeySize+1)
		if err := validateStateMapKeys("backend state", session.StateMap{longKey: nil}); err == nil ||
			!strings.Contains(err.Error(), "key") {
			t.Fatalf("validateStateMapKeys() error = %v, want key size limit", err)
		}
		state := make(session.StateMap, maxReplayStateKeyCount+1)
		for index := 0; index <= maxReplayStateKeyCount; index++ {
			state[fmt.Sprintf("key-%d", index)] = nil
		}
		if err := validateStateMapKeys("backend state", state); err == nil ||
			!strings.Contains(err.Error(), "keys") {
			t.Fatalf("validateStateMapKeys() error = %v, want key count limit", err)
		}
		if _, err := stateKeysForClear("backend state before clear", state); err == nil ||
			!strings.Contains(err.Error(), "keys") {
			t.Fatalf("stateKeysForClear() error = %v, want key count limit", err)
		}
	})

	t.Run("event JSON", func(t *testing.T) {
		evt := event.Event{
			ID:        "physical-event",
			Timestamp: caseEpoch,
			Author:    "assistant",
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: strings.Repeat("x", maxReplayEventSize)},
			}}},
		}
		if err := event.SetExtension(&evt, logicalEventIDExtension, "logical-event"); err != nil {
			t.Fatalf("SetExtension() error = %v", err)
		}
		if _, _, _, err := normalizeEvents([]event.Event{evt}, EventOrderGlobal, nil, caseEpoch); err == nil ||
			!strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("normalizeEvents() error = %v, want event output limit", err)
		}
	})

	t.Run("event pages share aggregate budget", func(t *testing.T) {
		evt := event.Event{
			ID:        "physical-event",
			Timestamp: caseEpoch,
			Author:    "assistant",
		}
		if err := event.SetExtension(&evt, logicalEventIDExtension, "logical-event"); err != nil {
			t.Fatalf("SetExtension() error = %v", err)
		}
		normalized, _, _, err := normalizeEvents([]event.Event{evt}, EventOrderGlobal, nil, caseEpoch)
		if err != nil {
			t.Fatalf("normalizeEvents() error = %v", err)
		}
		raw, err := json.Marshal(normalized[0])
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		pages := map[string][]event.Event{
			"first":  {evt},
			"second": {evt},
		}
		_, err = normalizeEventPagesWithByteLimit(pages, caseEpoch, 2*len(raw)-1)
		if err == nil || !strings.Contains(err.Error(), "normalized event pages exceed") {
			t.Fatalf("normalizeEventPages() error = %v, want aggregate byte limit", err)
		}
	})

	t.Run("memory content", func(t *testing.T) {
		_, err := normalizeMemoryEntry(&memory.Entry{
			ID:     "memory",
			Memory: &memory.Memory{Memory: strings.Repeat("x", maxReplayMemorySize+1)},
		}, "oversized memory")
		if err == nil || !strings.Contains(err.Error(), "content exceeds") {
			t.Fatalf("normalizeMemoryEntry() error = %v, want memory content limit", err)
		}
	})

	t.Run("summary aggregate", func(t *testing.T) {
		chunk := strings.Repeat("x", maxReplaySummaryTotalSize/2)
		sess := &session.Session{Summaries: map[string]*session.Summary{
			"one": {Summary: chunk},
			"two": {Summary: chunk},
		}}
		if _, err := normalizeSummaries(sess, nil, nil); err == nil || !strings.Contains(err.Error(), "summaries exceed") {
			t.Fatalf("normalizeSummaries() error = %v, want summary aggregate limit", err)
		}
	})

	t.Run("memory search aggregate", func(t *testing.T) {
		content := strings.Repeat("x", maxReplayMemorySize/2)
		catalog := []*memory.Entry{
			{ID: "memory-a", Memory: &memory.Memory{Memory: content, Topics: []string{"a"}}},
			{ID: "memory-b", Memory: &memory.Memory{Memory: content, Topics: []string{"b"}}},
		}
		_, ids, err := normalizeMemoryCatalog(catalog)
		if err != nil {
			t.Fatalf("normalizeMemoryCatalog() error = %v", err)
		}
		searches := map[string][]*memory.Entry{}
		for index := 0; index < 9; index++ {
			searches[fmt.Sprintf("query-%d", index)] = []*memory.Entry{
				{ID: "memory-a", Memory: &memory.Memory{Memory: content, Topics: []string{"a"}}},
				{ID: "memory-b", Memory: &memory.Memory{Memory: content, Topics: []string{"b"}}},
			}
		}
		_, err = normalizeMemorySearches(searches, ids)
		if err == nil || !strings.Contains(err.Error(), "memory searches") {
			t.Fatalf("normalizeMemorySearches() error = %v, want search aggregate limit", err)
		}
	})

	t.Run("track count and aggregate", func(t *testing.T) {
		tracks := make(map[session.Track]*session.TrackEvents, maxReplayTrackCount+1)
		for index := 0; index <= maxReplayTrackCount; index++ {
			name := session.Track(fmt.Sprintf("track-%d", index))
			tracks[name] = &session.TrackEvents{Track: name}
		}
		if _, err := normalizeTracks(&session.Session{Tracks: tracks}, time.Time{}); err == nil ||
			!strings.Contains(err.Error(), "track catalog") {
			t.Fatalf("normalizeTracks() error = %v, want track count limit", err)
		}

		payload := json.RawMessage("\"" + strings.Repeat("x", maxReplayTrackTotalSize/2) + "\"")
		sess := &session.Session{Tracks: map[session.Track]*session.TrackEvents{
			"one": {Track: "one", Events: []session.TrackEvent{{Track: "one", Payload: payload}}},
			"two": {Track: "two", Events: []session.TrackEvent{{Track: "two", Payload: payload}}},
		}}
		if _, err := normalizeTracks(sess, time.Time{}); err == nil || !strings.Contains(err.Error(), "normalized tracks exceed") {
			t.Fatalf("normalizeTracks() aggregate error = %v, want track byte limit", err)
		}
	})
}

func TestNormalizeSnapshotHandlesSharedSessionWrites(t *testing.T) {
	sess := session.NewSession("replaytest", "user", "shared")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for index := 0; index < 100; index++ {
			if err := sess.AppendTrackEvent(&session.TrackEvent{
				Track:   "tools",
				Payload: json.RawMessage(fmt.Sprintf(`{"index":%d}`, index)),
			}); err != nil {
				t.Errorf("AppendTrackEvent() error = %v", err)
				return
			}
		}
	}()
	for index := 0; index < 100; index++ {
		if _, err := normalizeSnapshot(
			"backend", "shared-session", EventOrderGlobal, nil,
			Capabilities{CapabilitySession: true, CapabilityTrack: true},
			nil, sess, nil, nil, nil, nil,
		); err != nil {
			t.Fatalf("normalizeSnapshot() error = %v", err)
		}
	}
	<-done
}

func TestValidateCaseShortCircuitsOversizedStepTree(t *testing.T) {
	steps := make([]Step, maxReplaySteps+1)
	err := validateCase(Case{
		Name:  "oversized-step-tree",
		Steps: steps,
	})
	if err == nil || !strings.Contains(err.Error(), "steps") {
		t.Fatalf("validateCase() error = %v, want step limit error", err)
	}
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
		{name: "too many backends", mutate: func(report *Report) {
			extra := make([]string, maxReplayBackends-len(report.Backends)+1)
			for index := range extra {
				extra[index] = fmt.Sprintf("extra-%03d", index)
			}
			report.Backends = append(report.Backends, extra...)
		}},
		{name: "unknown comparison mode", mutate: func(report *Report) { report.ComparisonMode = "unknown" }},
		{name: "empty backend", mutate: func(report *Report) { report.Backends[1] = "" }},
		{name: "reserved wildcard backend", mutate: func(report *Report) { report.Backends[1] = "*" }},
		{name: "invalid backend UTF-8", mutate: func(report *Report) { report.Backends[1] = string([]byte{0xff}) }},
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
		{name: "forged event index locator", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/events/0/author"
			forged := 999
			diff.EventIndex = &forged
			setBlockingReportDiff(report, diff)
		}},
		{name: "missing event index locator", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/events/0/author"
			setBlockingReportDiff(report, diff)
		}},
		{name: "event locator on state path", mutate: func(report *Report) {
			diff := validReportDiff()
			locator := 0
			diff.EventIndex = &locator
			setBlockingReportDiff(report, diff)
		}},
		{name: "forged track locator", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/tracks/tool~1weather/0/status"
			diff.TrackName = "different"
			setBlockingReportDiff(report, diff)
		}},
		{name: "missing memory locator", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/memories/0/content"
			setBlockingReportDiff(report, diff)
		}},
		{name: "unknown diff domain", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/not-a-snapshot-field"
			setBlockingReportDiff(report, diff)
		}},
		{name: "non-canonical event index", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/events/00/author"
			index := 0
			diff.EventIndex = &index
			setBlockingReportDiff(report, diff)
		}},
		{name: "non-numeric event index", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/events/foo/author"
			setBlockingReportDiff(report, diff)
		}},
		{name: "negative memory index", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/memories/-1/content"
			diff.MemoryID = "memory-0"
			report.Cases[0].LocatorEvidence = validLocatorEvidence()
			setBlockingReportDiff(report, diff)
		}},
		{name: "forged memory index", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/memories/999/content"
			diff.MemoryID = "memory-0"
			report.Cases[0].LocatorEvidence = validLocatorEvidence()
			setBlockingReportDiff(report, diff)
		}},
		{name: "forged memory id", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/memories/0/content"
			diff.MemoryID = "forged"
			report.Cases[0].LocatorEvidence = validLocatorEvidence()
			setBlockingReportDiff(report, diff)
		}},
		{name: "missing memory evidence", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/memories/0/content"
			diff.MemoryID = "memory-0"
			setBlockingReportDiff(report, diff)
		}},
		{name: "forged memory search query", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/memory_searches/forged/0/content"
			diff.MemoryID = "memory-0"
			report.Cases[0].LocatorEvidence = validLocatorEvidence()
			setBlockingReportDiff(report, diff)
		}},
		{name: "empty memory search query", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/memory_searches//0/content"
			diff.MemoryID = "memory-0"
			report.Cases[0].LocatorEvidence = validLocatorEvidence()
			setBlockingReportDiff(report, diff)
		}},
		{name: "invalid diff JSON pointer escape", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/state/~2"
			setBlockingReportDiff(report, diff)
		}},
		{name: "unknown session field", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/session/not_a_field"
			setBlockingReportDiff(report, diff)
		}},
		{name: "unknown state suffix", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/state/app/key/not_a_field"
			setBlockingReportDiff(report, diff)
		}},
		{name: "unknown summary field", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/summaries/filter/not_a_field"
			setBlockingReportDiff(report, diff)
		}},
		{name: "unknown memory field", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/memories/0/not_a_field"
			diff.MemoryID = "memory-0"
			report.Cases[0].LocatorEvidence = validLocatorEvidence()
			setBlockingReportDiff(report, diff)
		}},
		{name: "unknown track field", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.Path = "/tracks/name/0/not_a_field"
			diff.TrackName = "name"
			setBlockingReportDiff(report, diff)
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
			diff.Explanation = ""
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
		{name: "duplicate semantic diff", mutate: func(report *Report) {
			diff := validReportDiff()
			report.PassedCases = 0
			report.FailedCases = 1
			report.BlockingDiffs = 2
			report.Cases[0].Status = StatusFailed
			report.Cases[0].Diffs = []Diff{diff, diff}
			report.Cases[0].Reference.Pairs[0].BlockingDiffs = 2
		}},
		{name: "conflicting duplicate semantic diff", mutate: func(report *Report) {
			blocking := validReportDiff()
			allowed := blocking
			allowed.Allowed = true
			allowed.Explanation = "forged allowance"
			report.PassedCases = 0
			report.FailedCases = 1
			report.BlockingDiffs = 1
			report.AllowedDiffs = 1
			report.Cases[0].Status = StatusFailed
			report.Cases[0].Diffs = []Diff{blocking, allowed}
			report.Cases[0].Reference.Pairs[0].BlockingDiffs = 1
			report.Cases[0].Reference.Pairs[0].AllowedDiffs = 1
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

func TestReportStringFieldsHaveDefensiveLimits(t *testing.T) {
	oversized := strings.Repeat("x", maxReplayIdentifierSize+1)
	tests := []struct {
		name   string
		mutate func(*Report)
	}{
		{name: "reference backend", mutate: func(report *Report) { report.Reference = oversized }},
		{name: "diff backend", mutate: func(report *Report) {
			diff := validReportDiff()
			diff.BackendB = oversized
			setBlockingReportDiff(report, diff)
		}},
		{name: "reference comparable backend", mutate: func(report *Report) {
			report.Cases[0].Reference.ComparableBackends[1] = oversized
		}},
		{name: "reference pair backend", mutate: func(report *Report) {
			report.Cases[0].Reference.Pairs[0].BackendB = oversized
		}},
		{name: "consensus verdict", mutate: func(report *Report) {
			setValidConsensusReport(report)
			report.Cases[0].Consensus.Verdict = ConsensusVerdict(oversized)
		}},
		{name: "consensus comparable backend", mutate: func(report *Report) {
			setValidConsensusReport(report)
			report.Cases[0].Consensus.ComparableBackends[1] = oversized
		}},
		{name: "consensus pair backend", mutate: func(report *Report) {
			setValidConsensusReport(report)
			report.Cases[0].Consensus.Pairs[0].BackendB = oversized
		}},
		{name: "consensus outlier", mutate: func(report *Report) {
			setValidConsensusReport(report)
			report.Cases[0].Consensus.Outliers = []string{oversized}
		}},
		{name: "locator evidence backend", mutate: func(report *Report) {
			report.Cases[0].LocatorEvidence = &LocatorEvidence{
				MemoryIDs: map[string][]string{oversized: {}},
			}
		}},
		{name: "exclusion kind", mutate: func(report *Report) {
			diff := executionFailureDiff("clean", "actual", ComparisonReference, "baseline", errors.New("failed"))
			diff.Exclusion.Kind = ExclusionKind(oversized)
			setBlockingReportDiff(report, diff)
		}},
		{name: "exclusion capability", mutate: func(report *Report) {
			diff := capabilityDiffs(
				"clean",
				[]Backend{{Name: "actual"}},
				ComparisonReference,
				"baseline",
				map[string][]Capability{"actual": {CapabilityMemory}},
			)[0]
			diff.Exclusion.Capability = Capability(oversized)
			report.PassedCases = 0
			report.UnsupportedCases = 1
			report.AllowedDiffs = 1
			report.Cases[0].Status = StatusUnsupported
			report.Cases[0].Diffs = []Diff{diff}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := validReferenceReport()
			test.mutate(&report)
			if err := report.Validate(); err == nil || !strings.Contains(err.Error(), "exceeds") {
				t.Fatalf("Validate() error = %v, want defensive string limit", err)
			}
		})
	}
}

func TestReportStringFieldsRequireValidUTF8(t *testing.T) {
	invalid := string([]byte{0xff})
	tests := []struct {
		name   string
		mutate func(*Report)
	}{
		{name: "reference backend", mutate: func(report *Report) { report.Reference = invalid }},
		{name: "consensus verdict", mutate: func(report *Report) {
			setValidConsensusReport(report)
			report.Cases[0].Consensus.Verdict = ConsensusVerdict(invalid)
		}},
		{name: "locator evidence backend", mutate: func(report *Report) {
			report.Cases[0].LocatorEvidence = &LocatorEvidence{
				MemoryIDs: map[string][]string{invalid: {}},
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := validReferenceReport()
			test.mutate(&report)
			if err := report.Validate(); err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
				t.Fatalf("Validate() error = %v, want invalid UTF-8", err)
			}
		})
	}
}

func TestReferenceReportRequiresCompleteBackendAndPairManifest(t *testing.T) {
	t.Run("unaccounted backend", func(t *testing.T) {
		report := validReferenceReport()
		report.Backends = append(report.Backends, "phantom")
		if err := report.Validate(); err == nil || !strings.Contains(err.Error(), "exactly one of comparable or excluded") {
			t.Fatalf("Validate() error = %v, want unaccounted backend rejection", err)
		}
	})

	t.Run("missing pair", func(t *testing.T) {
		report := validReferenceReport()
		report.Cases[0].Reference.Pairs = nil
		if err := report.Validate(); err == nil || !strings.Contains(err.Error(), "pairs, want") {
			t.Fatalf("Validate() error = %v, want missing pair rejection", err)
		}
	})

	t.Run("dropped diff", func(t *testing.T) {
		report := validReferenceReport()
		setBlockingReportDiff(&report, validReportDiff())
		report.Cases[0].Diffs = nil
		report.Cases[0].Status = StatusPassed
		report.PassedCases = 1
		report.FailedCases = 0
		report.BlockingDiffs = 0
		if err := report.Validate(); err == nil || !strings.Contains(err.Error(), "pair counters do not add up") {
			t.Fatalf("Validate() error = %v, want dropped diff rejection", err)
		}
	})
}

func TestReportValidationRejectsTooManyDiffsBeforeIteration(t *testing.T) {
	report := validReferenceReport()
	report.PassedCases = 0
	report.FailedCases = 1
	report.BlockingDiffs = maxReplayDiffsPerCase + 1
	report.Cases[0].Status = StatusFailed
	report.Cases[0].Diffs = make([]Diff, maxReplayDiffsPerCase+1)
	err := report.Validate()
	if err == nil || !strings.Contains(err.Error(), "diffs, limit is") {
		t.Fatalf("Validate() error = %v, want diff limit error", err)
	}
}

func TestReportValidationDoesNotEchoOversizedCaseName(t *testing.T) {
	report := validReferenceReport()
	report.Cases[0].Name = strings.Repeat("\\", 1<<20)
	report.Cases[0].Diffs = make([]Diff, maxReplayDiffsPerCase+1)
	err := report.Validate()
	if err == nil {
		t.Fatal("Validate() unexpectedly accepted too many diffs")
	}
	if len(err.Error()) > maxReplayIdentifierSize {
		t.Fatalf("Validate() error has %d bytes, want a bounded diagnostic", len(err.Error()))
	}
}

type oversizedZeroSlice []struct{}

func (oversizedZeroSlice) MarshalJSON() ([]byte, error) {
	panic("oversized value reached custom marshaler")
}

func TestReportValidationRejectsOversizedJSONBeforeGraphTraversal(t *testing.T) {
	report := validReferenceReport()
	diff := validReportDiff()
	diff.Baseline = make(oversizedZeroSlice, maxReplayJSONBytes/2+1)
	setBlockingReportDiff(&report, diff)
	err := report.Validate()
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Validate() error = %v, want preflight JSON size rejection", err)
	}
	if strings.Contains(err.Error(), "custom marshaler") {
		t.Fatalf("Validate() traversed oversized value before rejecting it: %v", err)
	}
}

func TestReportValidationAcceptsBoundedByteSlice(t *testing.T) {
	report := validReferenceReport()
	diff := validReportDiff()
	diff.Baseline = make([]byte, maxReplayJSONBytes/2+1)
	setBlockingReportDiff(&report, diff)
	if err := report.Validate(); err != nil {
		t.Fatalf("Validate() rejected bounded base64 JSON value: %v", err)
	}
}

func TestReportValidationRejectsTooManyCasesBeforeIteration(t *testing.T) {
	report := validReferenceReport()
	report.TotalCases = maxReplayCases + 1
	report.Cases = make([]CaseResult, report.TotalCases)
	err := report.Validate()
	if err == nil || !strings.Contains(err.Error(), "cases, limit is") {
		t.Fatalf("Validate() error = %v, want case limit error", err)
	}
}

func TestReportValidationRejectsOversizedAggregateEncoding(t *testing.T) {
	report := validReferenceReport()
	report.PassedCases = 0
	report.FailedCases = 1
	report.Cases[0].Status = StatusFailed

	// Reuse one allocation so the regression exercises aggregate report work
	// without allocating a separate multi-megabyte value for every diff.
	largeValue := strings.Repeat("x", 7<<20)
	for index := 0; index < 10; index++ {
		diff := validReportDiff()
		diff.Path = fmt.Sprintf("/state/session/key-%d", index)
		diff.Baseline = largeValue
		diff.Actual = "changed"
		report.Cases[0].Diffs = append(report.Cases[0].Diffs, diff)
	}
	report.BlockingDiffs = len(report.Cases[0].Diffs)
	report.Cases[0].Reference.Pairs[0].BlockingDiffs = report.BlockingDiffs

	err := report.Validate()
	if err == nil || !strings.Contains(err.Error(), "encoded-size budget") {
		t.Fatalf("Validate() error = %v, want aggregate encoded-size limit", err)
	}
}

func TestRunnerStopsBeforeBuildingAnOversizedReport(t *testing.T) {
	cases := make([]Case, 100)
	for index := range cases {
		cases[index] = PublicCases()[0]
		cases[index].Name = fmt.Sprintf("report-budget-%03d", index)
	}
	backendErr := errors.New(strings.Repeat("x", maxReplayErrorSize))
	openCalls := 0
	newFailingBackend := func(name string) Backend {
		return Backend{
			Name:         name,
			Capabilities: PortableCapabilities(),
			Open: func(context.Context, string) (*Services, error) {
				openCalls++
				return nil, backendErr
			},
		}
	}
	_, err := (Runner{}).Run(
		context.Background(),
		cases,
		[]Backend{newFailingBackend("left"), newFailingBackend("right")},
	)
	if err == nil || !strings.Contains(err.Error(), "encoded-size budget") {
		t.Fatalf("Run() error = %v, want aggregate report limit", err)
	}
	if openCalls >= len(cases)*2 {
		t.Fatalf("Run() opened all %d backends before rejecting its report", openCalls)
	}
}

func TestReferenceReportRejectsSemanticDiffsForExcludedBackends(t *testing.T) {
	for _, excluded := range []string{"actual", "baseline"} {
		t.Run(excluded, func(t *testing.T) {
			report := validReferenceReport()
			exclusion := Diff{
				Case:        "clean",
				BackendA:    "baseline",
				BackendB:    excluded,
				SessionID:   "clean",
				Path:        "/execution",
				Baseline:    "success",
				Actual:      "injected failure",
				Explanation: "backend replay failed",
				Exclusion: &ExclusionEvidence{
					Backend: excluded,
					Kind:    ExclusionExecutionFailure,
					Error:   "injected failure",
				},
			}
			semantic := validReportDiff()
			semantic.Baseline = "before"
			semantic.Actual = "after"
			report.PassedCases = 0
			report.FailedCases = 1
			report.BlockingDiffs = 2
			report.Cases[0].Status = StatusFailed
			report.Cases[0].Diffs = []Diff{exclusion, semantic}
			if err := report.Validate(); err == nil {
				t.Fatal("Validate() accepted a semantic diff involving an excluded backend")
			}
		})
	}
}

func TestReportRejectsPartialIndexedLocatorEvidence(t *testing.T) {
	report := validReferenceReport()
	diff := validReportDiff()
	diff.Path = "/memories/0/content"
	diff.MemoryID = "memory-0"
	diff.Baseline = "before"
	diff.Actual = "after"
	setBlockingReportDiff(&report, diff)
	report.Cases[0].LocatorEvidence = &LocatorEvidence{
		MemoryIDs: map[string][]string{
			"baseline": {"memory-0"},
		},
	}
	if err := report.Validate(); err == nil {
		t.Fatal("Validate() unexpectedly accepted indexed memory evidence for only one backend")
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
		{name: "forged execution exclusion payload", mutate: func(_ *ConsensusResult, diffs *[]Diff, known map[string]struct{}) {
			known["c"] = struct{}{}
			*diffs = []Diff{{
				BackendA: "c", BackendB: "c", SessionID: "case", Path: "/execution",
				Baseline: "success", Actual: "backend failed", Explanation: "backend replay failed",
			}}
		}},
		{name: "forged capability exclusion payload", mutate: func(_ *ConsensusResult, diffs *[]Diff, known map[string]struct{}) {
			known["c"] = struct{}{}
			*diffs = []Diff{{
				BackendA: "c", BackendB: "c", SessionID: "case", Path: "/capabilities/session",
				Baseline: true, Actual: false, Allowed: true,
				Explanation: "backend reports this capability as unsupported",
			}}
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

func TestConsensusValidationRejectsConflictingExclusionKinds(t *testing.T) {
	result := ConsensusResult{
		Verdict:            ConsensusUnanimous,
		ComparableBackends: []string{"a", "b"},
		Pairs:              []PairComparison{{BackendA: "a", BackendB: "b"}},
	}
	known := map[string]struct{}{"a": {}, "b": {}, "c": {}}
	diffs := []Diff{
		{
			Case: "case", BackendA: "c", BackendB: "c", SessionID: "case",
			Path: "/execution", Baseline: "success", Actual: "failed",
			Explanation: "backend replay failed",
			Exclusion:   &ExclusionEvidence{Backend: "c", Kind: ExclusionExecutionFailure, Error: "failed"},
		},
		{
			Case: "case", BackendA: "c", BackendB: "c", SessionID: "case",
			Path: "/capabilities/memory", Baseline: true, Actual: false, Allowed: true,
			Explanation: "backend reports this capability as unsupported",
			Exclusion:   &ExclusionEvidence{Backend: "c", Kind: ExclusionUnsupportedCapability, Capability: CapabilityMemory},
		},
	}
	if err := validateConsensusResult("case", result, diffs, known); err == nil {
		t.Fatal("validateConsensusResult() unexpectedly accepted conflicting exclusion kinds")
	}
}

func TestReportValidationAcceptsVerifiableMemoryLocators(t *testing.T) {
	report := validReferenceReport()
	report.Cases[0].LocatorEvidence = validLocatorEvidence()
	diff := validReportDiff()
	diff.Path = "/memories/0/content"
	diff.MemoryID = "memory-0"
	setBlockingReportDiff(&report, diff)
	if err := report.Validate(); err != nil {
		t.Fatalf("Validate() rejected a verifiable memory locator: %v", err)
	}

	report = validReferenceReport()
	report.Cases[0].LocatorEvidence = validLocatorEvidence()
	diff = validReportDiff()
	diff.Path = "/memory_searches/query/0/score"
	diff.MemoryID = "memory-0"
	setBlockingReportDiff(&report, diff)
	if err := report.Validate(); err != nil {
		t.Fatalf("Validate() rejected a verifiable memory search locator: %v", err)
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
	t.Run("nil memory payload", func(t *testing.T) {
		input := Snapshot{Memories: []CanonicalMap{{"memory": map[string]any(nil)}}}
		if _, err := InjectFault(input, FaultMemoryContent); err == nil {
			t.Fatal("InjectFault() unexpectedly accepted a nil memory payload")
		}
	})
	t.Run("invalid track payload", func(t *testing.T) {
		input := Snapshot{Tracks: map[string][]CanonicalMap{"track": {{"payload": "invalid"}}}}
		if _, err := InjectFault(input, FaultTrackPayload); err == nil {
			t.Fatal("InjectFault() unexpectedly accepted an invalid track payload")
		}
	})
	t.Run("nil track payload", func(t *testing.T) {
		input := Snapshot{Tracks: map[string][]CanonicalMap{"track": {{"payload": map[string]any(nil)}}}}
		if _, err := InjectFault(input, FaultTrackPayload); err == nil {
			t.Fatal("InjectFault() unexpectedly accepted a nil track payload")
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
	t.Run("nil stale boundary", func(t *testing.T) {
		input := Snapshot{Summaries: map[string]CanonicalMap{
			"summary": {"boundary": map[string]any(nil)},
		}}
		if _, err := InjectFault(input, FaultSummaryStale); err == nil {
			t.Fatal("InjectFault() unexpectedly accepted a nil summary boundary")
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
	t.Run("invalid UTF-8 snapshot value", func(t *testing.T) {
		baseline := minimalSnapshot("baseline", `{}`)
		actual := minimalSnapshot("actual", `{}`)
		baseline.Case = "invalid-utf8"
		actual.Case = "invalid-utf8"
		baseline.Session["value"] = string([]byte{0xff})
		actual.Session["value"] = "\ufffd"
		if _, err := Compare("invalid-utf8", baseline, actual, nil); err == nil ||
			!strings.Contains(err.Error(), "invalid UTF-8") {
			t.Fatalf("Compare() error = %v, want invalid UTF-8", err)
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
		if diffs[0].Explanation != "actual path is missing" {
			t.Fatalf("Compare() explanation = %q, want missing actual path", diffs[0].Explanation)
		}
	})
	t.Run("missing baseline is distinguishable from null", func(t *testing.T) {
		baseline := minimalSnapshot("baseline", `{}`)
		actual := minimalSnapshot("actual", `{}`)
		baseline.Case = "presence"
		actual.Case = "presence"
		actual.Session["optional"] = nil
		diffs, err := Compare("presence", baseline, actual, nil)
		if err != nil {
			t.Fatalf("Compare() error = %v", err)
		}
		if len(diffs) != 1 || diffs[0].Explanation != "baseline path is missing" {
			t.Fatalf("Compare() diffs = %+v, want a missing baseline explanation", diffs)
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
		if len(diffs) == 0 {
			t.Fatal("Compare() did not report the session ID difference")
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
		if len(diffs) == 0 {
			t.Fatal("Compare() did not report the sequence difference")
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
			{
				name:  "invalid UTF-8 and replacement character",
				left:  []byte{'"', 0xff, '"'},
				right: []byte(`"\ufffd"`),
			},
			{
				name:  "unpaired high surrogate and replacement character",
				left:  []byte(`"\ud800"`),
				right: []byte(`"\ufffd"`),
			},
			{
				name:  "unpaired low surrogate and replacement character",
				left:  []byte(`"\udc00"`),
				right: []byte(`"\ufffd"`),
			},
			{
				name:  "high surrogate followed by non-low escape",
				left:  []byte(`"\ud800\u0061"`),
				right: []byte(`"\ufffda"`),
			},
			{
				name:  "duplicate JSON key and last-key-wins object",
				left:  []byte(`{"value":1,"value":2}`),
				right: []byte(`{"value":2}`),
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
	t.Run("valid surrogate pair remains JSON", func(t *testing.T) {
		escaped := normalizeState(session.StateMap{"value": []byte(`"\ud83d\ude00"`)}, "session")
		encoded := normalizeState(session.StateMap{
			"value": {'"', 0xf0, 0x9f, 0x98, 0x80, '"'},
		}, "session")
		if !reflect.DeepEqual(escaped, encoded) {
			t.Fatalf("equivalent surrogate pair encodings differ: %v != %v", escaped, encoded)
		}
	})
	t.Run("escaped surrogate text remains JSON", func(t *testing.T) {
		state := normalizeState(session.StateMap{"value": []byte(`"\\ud800"`)}, "session")
		value, ok := state["value"].(CanonicalMap)
		if !ok || value["kind"] != "json" {
			t.Fatalf("escaped surrogate text = %#v, want JSON", state["value"])
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
			PortableCapabilities(),
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

func TestIndentedReportSizeMatchesJSONIndent(t *testing.T) {
	inputs := []string{
		`{}`,
		`[]`,
		`{"text":"escaped \" quote","empty":[],"nested":{"values":[1,true,null]}}`,
	}
	for _, input := range inputs {
		var output bytes.Buffer
		if err := json.Indent(&output, []byte(input), "", "  "); err != nil {
			t.Fatalf("json.Indent(%q) error = %v", input, err)
		}
		want := output.Len() + 1
		got, err := indentedReportSize([]byte(input), want)
		if err != nil {
			t.Fatalf("indentedReportSize(%q) error = %v", input, err)
		}
		if got != want {
			t.Fatalf("indentedReportSize(%q) = %d, want %d", input, got, want)
		}
		if _, err := indentedReportSize([]byte(input), want-1); err == nil {
			t.Fatalf("indentedReportSize(%q) unexpectedly accepted an undersized limit", input)
		}
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
			Reference: &ReferenceResult{
				ComparableBackends: []string{"actual", "baseline"},
				Pairs:              []PairComparison{{BackendA: "actual", BackendB: "baseline"}},
			},
		}},
	}
}

func setValidConsensusReport(report *Report) {
	report.ComparisonMode = ComparisonConsensus
	report.Reference = ""
	report.Cases[0].Reference = nil
	report.Cases[0].Consensus = &ConsensusResult{
		Verdict:            ConsensusUnanimous,
		ComparableBackends: []string{"actual", "baseline"},
		Pairs:              []PairComparison{{BackendA: "actual", BackendB: "baseline"}},
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

func validLocatorEvidence() *LocatorEvidence {
	return &LocatorEvidence{
		MemoryIDs: map[string][]string{
			"baseline": {"memory-0"},
			"actual":   {"memory-0"},
		},
		MemorySearchIDs: map[string]map[string][]string{
			"baseline": {"query": {"memory-0"}},
			"actual":   {"query": {"memory-0"}},
		},
	}
}

func setBlockingReportDiff(report *Report, diff Diff) {
	report.PassedCases = 0
	report.FailedCases = 1
	report.BlockingDiffs = 1
	report.Cases[0].Status = StatusFailed
	report.Cases[0].Diffs = []Diff{diff}
	if report.Cases[0].Reference != nil && len(report.Cases[0].Reference.Pairs) == 1 {
		report.Cases[0].Reference.Pairs[0].BlockingDiffs = 1
		report.Cases[0].Reference.Pairs[0].AllowedDiffs = 0
	}
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

type mutatingAppendSessionService struct {
	session.Service
}

type customJSONHiddenState struct {
	Visible string `json:"visible"`
	hidden  map[string]string
}

type embeddedCustomJSONFields struct {
	Custom *customJSONHiddenState `json:"custom"`
}

type embeddedCustomJSONState struct {
	embeddedCustomJSONFields
}

type customJSONChannelState struct {
	Channel chan string
}

type mutatingJSONExportedState struct {
	Values map[string]string `json:"values"`
}

type opaqueSnapshotJSON struct {
	Channel  chan string
	Executed *bool
}

type capacitySensitiveSnapshotJSON struct {
	Values   []int
	Executed *bool
}

func (value *opaqueSnapshotJSON) MarshalJSON() ([]byte, error) {
	*value.Executed = true
	return []byte(`{"value":"executed"}`), nil
}

type aliasSensitiveSnapshotJSON struct {
	First  *int
	Second *int
}

func (value *aliasSensitiveSnapshotJSON) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]bool{"same": value.First == value.Second})
}

func (value *capacitySensitiveSnapshotJSON) MarshalJSON() ([]byte, error) {
	*value.Executed = true
	return json.Marshal(map[string]int{"capacity": cap(value.Values)})
}

func (value *mutatingJSONExportedState) MarshalJSON() ([]byte, error) {
	value.Values["value"] = "mutated"
	return json.Marshal(value.Values)
}

func (value *customJSONChannelState) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]int{"queued": len(value.Channel)})
}

type customTextHiddenState struct {
	hidden map[string]string
}

func (value *customTextHiddenState) MarshalText() ([]byte, error) {
	return []byte(value.hidden["value"]), nil
}

type jsonOnlyMapKey int

func (jsonOnlyMapKey) MarshalJSON() ([]byte, error) {
	return []byte(`"ignored"`), nil
}

type textMapKey string

func (textMapKey) MarshalText() ([]byte, error) {
	return []byte("text-key"), nil
}

func (value *customJSONHiddenState) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{
		"hidden":  value.hidden["value"],
		"visible": value.Visible,
	})
}

type cyclicJSONValue struct {
	Next *cyclicJSONValue `json:"next"`
}

func (*cyclicJSONValue) MarshalJSON() ([]byte, error) {
	return []byte(`{"valid":true}`), nil
}

type recursiveCycleJSONValue struct {
	Next *recursiveCycleJSONValue `json:"next"`
}

func (value *recursiveCycleJSONValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(value.Next)
}

type recursiveHiddenCycleJSONValue struct {
	Next *recursiveHiddenCycleJSONValue
}

func (value *recursiveHiddenCycleJSONValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(value.Next)
}

type nilMemorySearchService struct{ memory.Service }

type ignoringMemorySearchLimitService struct{ memory.Service }

type typedNilSessionService struct{ session.Service }

type typedNilMemoryService struct{ memory.Service }

func (s *nilMemorySearchService) SearchMemories(context.Context, memory.UserKey, string, ...memory.SearchOption) ([]*memory.Entry, error) {
	return []*memory.Entry{nil}, nil
}

func (s *ignoringMemorySearchLimitService) SearchMemories(
	ctx context.Context,
	key memory.UserKey,
	_ string,
	_ ...memory.SearchOption,
) ([]*memory.Entry, error) {
	return s.Service.ReadMemories(ctx, key, 0)
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

func (s *mutatingAppendSessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	options ...session.Option,
) error {
	if len(evt.Response.Choices[0].Message.ToolCalls) > 0 {
		evt.Response.Choices[0].Message.ToolCalls[0].Function.Arguments = []byte(`{"value":"mutated"}`)
	}
	return s.Service.AppendEvent(ctx, sess, evt, options...)
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

type countingError struct {
	calls int
}

func (e *countingError) Error() string {
	e.calls++
	return fmt.Sprintf("failure-%d", e.calls)
}
