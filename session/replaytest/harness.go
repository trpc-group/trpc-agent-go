//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Capture reads public backend state and normalizes it.
func (r *Runtime) Capture(ctx context.Context) (Snapshot, error) {
	input, err := r.load(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	input.MemoryQueries = r.memoryQuerySnapshot()
	// Backend capabilities are the sole source of runtime skip decisions.
	// A custom loader may capture data, but it may not exempt sections from
	// comparison by injecting additional unsupported declarations.
	input.Unsupported = unsupportedCapabilities(r.Backend.Capabilities)
	return r.Normalizer.Normalize(input, r.Ledger)
}

func (r *Runtime) load(ctx context.Context) (CaptureInput, error) {
	if r.Backend.Load != nil {
		return r.Backend.Load(ctx, r.Backend)
	}
	sess, err := r.Backend.Session.GetSession(ctx, r.Backend.SessionKey)
	if err != nil {
		return CaptureInput{}, fmt.Errorf("load session from %s: %w", r.Backend.Name, err)
	}
	appState, err := r.Backend.Session.ListAppStates(ctx, r.Backend.SessionKey.AppName)
	if err != nil {
		return CaptureInput{}, fmt.Errorf("load app state from %s: %w", r.Backend.Name, err)
	}
	userKey := session.UserKey{AppName: r.Backend.SessionKey.AppName, UserID: r.Backend.SessionKey.UserID}
	userState, err := r.Backend.Session.ListUserStates(ctx, userKey)
	if err != nil {
		return CaptureInput{}, fmt.Errorf("load user state from %s: %w", r.Backend.Name, err)
	}
	var memories []*memory.Entry
	if r.Backend.Memory != nil && r.Backend.Capabilities.Supports(CapabilityMemory) {
		memories, err = r.Backend.Memory.ReadMemories(ctx, replayMemoryUserKey(r.Backend), 1000)
		if err != nil {
			return CaptureInput{}, fmt.Errorf("load memories from %s: %w", r.Backend.Name, err)
		}
	}
	return CaptureInput{Session: sess, AppState: appState, UserState: userState, Memories: memories, Unsupported: unsupportedCapabilities(r.Backend.Capabilities)}, nil
}

func (r *Runtime) captureCheckpoint(ctx context.Context, name, afterOp string) error {
	snapshot, err := r.Capture(ctx)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.checkpoints = append(r.checkpoints, CheckpointSnapshot{Name: name, AfterOp: afterOp, Snapshot: snapshot})
	r.mu.Unlock()
	return nil
}
func (r *Runtime) trace(ctx context.Context) (Trace, error) {
	final, err := r.Capture(ctx)
	if err != nil {
		return Trace{}, err
	}
	r.mu.Lock()
	points := append([]CheckpointSnapshot(nil), r.checkpoints...)
	r.mu.Unlock()
	return Trace{Backend: r.Backend.Name, Checkpoints: points, Final: final}, nil
}

// RunCase executes one replay program across already-isolated backends.
func RunCase(ctx context.Context, replayCase ReplayCase, backends []Backend) (RunResult, error) {
	started := time.Now()
	if err := validateReplayCase(replayCase); err != nil {
		return RunResult{}, err
	}
	if len(backends) < 2 {
		return RunResult{}, fmt.Errorf("replay case requires at least two backends")
	}
	report := CaseReport{Name: replayCase.Name, Status: StatusPassed, Capabilities: make(map[string]CapabilitySet), Diffs: make([]Diff, 0)}
	traces := make(map[string]Trace)
	skippedRequired, err := executeReplayBackends(
		ctx,
		replayCase,
		backends,
		&report,
		traces,
	)
	if err != nil {
		return RunResult{}, err
	}
	if len(traces) < 2 {
		report.Status = incompleteCaseStatus(report.Diffs, skippedRequired)
		report.Duration = time.Since(started)
		return RunResult{Report: report, Traces: traces}, nil
	}
	baselineName := backends[0].Name
	baseline, ok := traces[baselineName]
	if !ok {
		return RunResult{}, fmt.Errorf("baseline backend %q did not produce a trace", baselineName)
	}
	if hasEventOrderContract(replayCase.Order) {
		baseline = validateAndCanonicalizeEventOrder(
			replayCase,
			baselineName,
			traces,
			&report,
		)
	}
	if err := compareBackendTraces(
		replayCase,
		backends,
		baselineName,
		baseline,
		traces,
		&report,
	); err != nil {
		return RunResult{}, err
	}
	report.Status = completedCaseStatus(report.Diffs, skippedRequired)
	report.Duration = time.Since(started)
	return RunResult{Report: report, Traces: traces}, nil
}

func executeReplayBackends(
	ctx context.Context,
	replayCase ReplayCase,
	backends []Backend,
	report *CaseReport,
	traces map[string]Trace,
) (bool, error) {
	skippedRequired := false
	for index, backend := range backends {
		missing, err := registerReplayBackend(
			replayCase,
			backend,
			report,
		)
		if err != nil {
			return false, err
		}
		if len(missing) > 0 {
			skippedRequired = true
			if index == 0 {
				return false, fmt.Errorf(
					"baseline backend %q lacks required capabilities %v",
					backend.Name,
					missing,
				)
			}
			appendMissingCapabilityDiffs(
				replayCase.Name,
				backends[0].Name,
				backend,
				missing,
				report,
			)
			continue
		}
		trace, err := executeReplayProgram(ctx, replayCase, backend)
		if err != nil {
			return false, err
		}
		traces[backend.Name] = trace
	}
	return skippedRequired, nil
}

func registerReplayBackend(
	replayCase ReplayCase,
	backend Backend,
	report *CaseReport,
) ([]CapabilityName, error) {
	if err := backend.Validate(); err != nil {
		return nil, err
	}
	if _, exists := report.Capabilities[backend.Name]; exists {
		return nil, fmt.Errorf("duplicate backend name %q", backend.Name)
	}
	report.Backends = append(report.Backends, backend.Name)
	report.Capabilities[backend.Name] = backend.Capabilities.Clone()
	report.Unsupported = append(
		report.Unsupported,
		declaredUnsupported(backend.Name, backend.Capabilities)...,
	)
	return missingCapabilities(replayCase.Required, backend)
}

func appendMissingCapabilityDiffs(
	caseName string,
	baselineName string,
	backend Backend,
	missing []CapabilityName,
	report *CaseReport,
) {
	for _, capability := range missing {
		declared := backend.Capabilities[capability]
		if declared.AllowedDiff {
			continue
		}
		report.Diffs = append(report.Diffs, Diff{
			Case:            caseName,
			SessionID:       backend.SessionKey.SessionID,
			BackendA:        baselineName,
			BackendB:        backend.Name,
			Section:         "capabilities",
			Path:            appendMapPath("$.capabilities", string(capability)),
			Baseline:        true,
			Compared:        false,
			BaselinePresent: true,
			ComparedPresent: true,
			AllowedDiff:     false,
			Explanation:     declared.Reason,
		})
	}
}

func executeReplayProgram(
	ctx context.Context,
	replayCase ReplayCase,
	backend Backend,
) (Trace, error) {
	runtime := NewRuntime(backend, replayCase.Normalize)
	for _, operation := range replayCase.Operations {
		if err := operation.Execute(ctx, runtime); err != nil {
			return Trace{}, fmt.Errorf(
				"case %q backend %q operation %q: %w",
				replayCase.Name,
				backend.Name,
				operation.OperationID(),
				err,
			)
		}
	}
	trace, err := runtime.trace(ctx)
	if err != nil {
		return Trace{}, fmt.Errorf(
			"capture case %q backend %q: %w",
			replayCase.Name,
			backend.Name,
			err,
		)
	}
	return trace, nil
}

func hasEventOrderContract(contract EventOrderContract) bool {
	return len(contract.ExactLogicalIDs) > 0 ||
		len(contract.HappensBefore) > 0
}

func validateAndCanonicalizeEventOrder(
	replayCase ReplayCase,
	baselineName string,
	traces map[string]Trace,
	report *CaseReport,
) Trace {
	for name, trace := range traces {
		report.Diffs = append(
			report.Diffs,
			validateTraceEventOrder(
				replayCase.Name,
				name,
				trace,
				replayCase.Order,
			)...,
		)
		traces[name] = canonicalizeTraceEventOrder(trace)
	}
	return traces[baselineName]
}

func compareBackendTraces(
	replayCase ReplayCase,
	backends []Backend,
	baselineName string,
	baseline Trace,
	traces map[string]Trace,
	report *CaseReport,
) error {
	for _, backend := range backends[1:] {
		compared, exists := traces[backend.Name]
		if !exists {
			continue
		}
		diffs, err := CompareTraces(replayCase.Name, baselineName, backend.Name, baseline, compared, replayCase.Allowed)
		if err != nil {
			return err
		}
		report.Diffs = append(report.Diffs, diffs...)
	}
	return nil
}

func incompleteCaseStatus(diffs []Diff, skippedRequired bool) CaseStatus {
	switch {
	case HasBlockingDiff(diffs):
		return StatusFailed
	case skippedRequired:
		return StatusMixed
	default:
		return StatusInconclusive
	}
}

func completedCaseStatus(diffs []Diff, skippedRequired bool) CaseStatus {
	if HasBlockingDiff(diffs) {
		return StatusFailed
	}
	if skippedRequired {
		return StatusMixed
	}
	return StatusPassed
}

// RunSuite creates fresh backend instances for every case.
func RunSuite(ctx context.Context, cases []ReplayCase, factories []BackendFactory) (Report, error) {
	if len(factories) < 2 {
		return Report{}, fmt.Errorf("replay suite requires at least two backend factories")
	}
	reports := make([]CaseReport, 0, len(cases))
	names := make([]string, len(factories))
	for i := range factories {
		names[i] = factories[i].Name
	}
	for _, replayCase := range cases {
		backends := make([]Backend, 0, len(factories))
		var cleanup []func() error
		for _, factory := range factories {
			if factory.Create == nil {
				err := fmt.Errorf(
					"backend factory %q has no create function",
					factory.Name,
				)
				if closeErr := closeBackendFactories(cleanup); closeErr != nil {
					err = errors.Join(err, closeErr)
				}
				return Report{}, err
			}
			backend, closeBackend, err := factory.Create(ctx, replayCase.Name)
			if err != nil {
				if closeBackend != nil {
					cleanup = append(cleanup, closeBackend)
				}
				closeErr := closeBackendFactories(cleanup)
				if closeErr != nil {
					err = errors.Join(err, closeErr)
				}
				return Report{}, fmt.Errorf("create backend %q for case %q: %w", factory.Name, replayCase.Name, err)
			}
			if backend.Name == "" {
				backend.Name = factory.Name
			}
			if backend.Capabilities == nil {
				backend.Capabilities = factory.Capabilities.Clone()
			}
			backends = append(backends, backend)
			if closeBackend != nil {
				cleanup = append(cleanup, closeBackend)
			}
		}
		result, runErr := RunCase(ctx, replayCase, backends)
		closeErr := closeBackendFactories(cleanup)
		if runErr != nil {
			if closeErr != nil {
				runErr = errors.Join(runErr, closeErr)
			}
			return Report{}, runErr
		}
		if closeErr != nil {
			return Report{}, fmt.Errorf("close backends for case %q: %w", replayCase.Name, closeErr)
		}
		reports = append(reports, result.Report)
	}
	return BuildReport(factories[0].Name, names, reports), nil
}

func closeBackendFactories(cleanup []func() error) error {
	var result error
	for i := len(cleanup) - 1; i >= 0; i-- {
		result = errors.Join(result, cleanup[i]())
	}
	return result
}

func validateReplayCase(replayCase ReplayCase) error {
	if strings.TrimSpace(replayCase.Name) == "" {
		return fmt.Errorf("replay case name is required")
	}
	if len(replayCase.Operations) == 0 {
		return fmt.Errorf("replay case %q has no operations", replayCase.Name)
	}
	seen := make(map[string]struct{}, len(replayCase.Operations))
	for index, operation := range replayCase.Operations {
		if operation == nil {
			return fmt.Errorf("replay case %q operation %d is nil", replayCase.Name, index)
		}
		id := strings.TrimSpace(operation.OperationID())
		if id == "" {
			return fmt.Errorf("replay case %q operation %d has no id", replayCase.Name, index)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("replay case %q has duplicate operation id %q", replayCase.Name, id)
		}
		seen[id] = struct{}{}
	}
	return validateAllowedDiffs(replayCase.Allowed)
}
func missingCapabilities(required []CapabilityName, backend Backend) ([]CapabilityName, error) {
	var missing []CapabilityName
	for _, capability := range required {
		declared, exists := backend.Capabilities[capability]
		if !exists {
			return nil, fmt.Errorf("backend %q must explicitly declare capability %q", backend.Name, capability)
		}
		if !declared.Supported {
			if strings.TrimSpace(declared.Reason) == "" {
				return nil, fmt.Errorf("backend %q unsupported capability %q requires a reason", backend.Name, capability)
			}
			missing = append(missing, capability)
		}
	}
	return missing, nil
}

func declaredUnsupported(
	backend string,
	capabilities CapabilitySet,
) []Unsupported {
	names := make([]string, 0, len(capabilities))
	for name, capability := range capabilities {
		if !capability.Supported {
			names = append(names, string(name))
		}
	}
	sort.Strings(names)
	result := make([]Unsupported, 0, len(names))
	for _, name := range names {
		capability := capabilities[CapabilityName(name)]
		result = append(result, Unsupported{
			Backend:     backend,
			Capability:  CapabilityName(name),
			AllowedDiff: capability.AllowedDiff,
			Reason:      capability.Reason,
		})
	}
	return result
}

func unsupportedCapabilities(capabilities CapabilitySet) map[CapabilityName]string {
	result := make(map[CapabilityName]string)
	for name, capability := range capabilities {
		if !capability.Supported {
			result[name] = capability.Reason
		}
	}
	return result
}

// ValidateEventOrder verifies exact multiplicity and happens-before edges.
func ValidateEventOrder(caseName, backend string, snapshot Snapshot, contract EventOrderContract) []Diff {
	indexes := make(map[string][]int)
	for index, value := range snapshot.Events {
		id, _ := value["id"].(string)
		indexes[id] = append(indexes[id], index)
	}
	var diffs []Diff
	expected := make(map[string]struct{}, len(contract.ExactLogicalIDs))
	for _, logical := range contract.ExactLogicalIDs {
		id := normalizedEventID(logical)
		expected[id] = struct{}{}
		positions := indexes[id]
		if len(positions) != 1 {
			diffs = append(diffs, Diff{Case: caseName, SessionID: snapshot.SessionID, BackendA: backend, BackendB: "order_contract", Section: "events", Path: "$.events", Baseline: 1, Compared: len(positions), BaselinePresent: true, ComparedPresent: true, Explanation: fmt.Sprintf("event %s must appear exactly once", id)})
		}
	}
	if len(expected) > 0 {
		for id, positions := range indexes {
			if _, ok := expected[id]; ok {
				continue
			}
			for _, index := range positions {
				diffs = append(diffs, Diff{
					Case: caseName, SessionID: snapshot.SessionID,
					BackendA: backend, BackendB: "order_contract",
					Section: "events", Path: fmt.Sprintf("$.events[%d]", index),
					Baseline: MissingValue{Missing: true}, Compared: id,
					BaselinePresent: false, ComparedPresent: true,
					Explanation: "unexpected event outside the exact logical event set",
					EventIndex:  intPointer(index),
				})
			}
		}
	}
	for _, edge := range contract.HappensBefore {
		leftID, rightID := normalizedEventID(edge[0]), normalizedEventID(edge[1])
		left, right := indexes[leftID], indexes[rightID]
		if len(left) != 1 || len(right) != 1 {
			continue
		}
		if left[0] >= right[0] {
			diffs = append(diffs, Diff{Case: caseName, SessionID: snapshot.SessionID, BackendA: backend, BackendB: "order_contract", Section: "events", Path: fmt.Sprintf("$.events[%d]", right[0]), Baseline: leftID + " before " + rightID, Compared: fmt.Sprintf("indexes %d >= %d", left[0], right[0]), BaselinePresent: true, ComparedPresent: true, Explanation: "causal event order violated", EventIndex: intPointer(right[0])})
		}
	}
	return diffs
}

func validateTraceEventOrder(
	caseName, backend string,
	trace Trace,
	contract EventOrderContract,
) []Diff {
	diffs := ValidateEventOrder(
		caseName,
		backend,
		trace.Final,
		contract,
	)
	for i := range diffs {
		diffs[i].Checkpoint = "final"
	}
	for _, checkpoint := range trace.Checkpoints {
		if !containsExactLogicalEventSet(
			checkpoint.Snapshot,
			contract.ExactLogicalIDs,
		) {
			continue
		}
		checkpointDiffs := ValidateEventOrder(
			caseName,
			backend,
			checkpoint.Snapshot,
			contract,
		)
		for i := range checkpointDiffs {
			checkpointDiffs[i].Checkpoint = checkpoint.Name
		}
		diffs = append(diffs, checkpointDiffs...)
	}
	return diffs
}

func containsExactLogicalEventSet(
	snapshot Snapshot,
	logicalIDs []string,
) bool {
	if len(logicalIDs) == 0 {
		return true
	}
	found := make(map[string]struct{}, len(snapshot.Events))
	for _, value := range snapshot.Events {
		id, _ := value["id"].(string)
		found[id] = struct{}{}
	}
	for _, logicalID := range logicalIDs {
		if _, ok := found[normalizedEventID(logicalID)]; !ok {
			return false
		}
	}
	return true
}

func canonicalizeTraceEventOrder(trace Trace) Trace {
	trace.Final.Events = sortedEventsByLogicalID(trace.Final.Events)
	for i := range trace.Checkpoints {
		trace.Checkpoints[i].Snapshot.Events = sortedEventsByLogicalID(trace.Checkpoints[i].Snapshot.Events)
	}
	return trace
}
func sortedEventsByLogicalID(events []map[string]any) []map[string]any {
	result := append([]map[string]any(nil), events...)
	sort.SliceStable(result, func(i, j int) bool {
		left, _ := result[i]["id"].(string)
		right, _ := result[j]["id"].(string)
		return left < right
	})
	return result
}
func normalizedEventID(logical string) string {
	if strings.HasPrefix(logical, string(IdentityEvent)+":") {
		return logical
	}
	return string(IdentityEvent) + ":" + logical
}
