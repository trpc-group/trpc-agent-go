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
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrInjectedFailure identifies a deterministic replay fault.
var ErrInjectedFailure = errors.New("injected replay failure")

// Fixture adapts one Session and Memory backend pair to replay operations.
// Its methods must be safe for concurrent use because parallel replay operations
// may call them from multiple goroutines. Implementations that also implement
// FaultInjector must provide the same guarantee for ApplyWithFault.
type Fixture interface {
	Name() string
	Capabilities() CapabilitySet
	Apply(context.Context, Operation) error
	Snapshot(context.Context) (Snapshot, error)
	Close() error
}

// FaultInjector applies an operation at a deterministic failure point.
type FaultInjector interface {
	ApplyWithFault(context.Context, Operation) error
}

// Backend creates isolated fixtures for replay cases.
type Backend struct {
	Name string
	New  func(context.Context, string) (Fixture, error)
}

// Runner executes replay cases and compares every backend with the first one.
type Runner struct {
	Backends              []Backend
	NormalizeOptions      NormalizeOptions
	CompareOptions        CompareOptions
	UnsupportedAllowances []UnsupportedAllowance
}

// Run executes all cases and returns a deterministic difference report.
func (runner Runner) Run(ctx context.Context, cases []ReplayCase) (Report, error) {
	if err := validateRunnerInputs(runner.Backends, cases); err != nil {
		return Report{}, err
	}
	allowedDiffs, err := newAllowedDiffTracker(runner.CompareOptions.AllowedDiffRules)
	if err != nil {
		return Report{}, err
	}
	if err := validateAllowedDiffRuleScopes(
		runner.CompareOptions.AllowedDiffRules, runner.Backends, cases,
	); err != nil {
		return Report{}, err
	}
	allowances, err := validateUnsupportedAllowances(
		runner.UnsupportedAllowances, runner.Backends, cases,
	)
	if err != nil {
		return Report{}, err
	}
	baselineName := runner.Backends[0].Name
	differences := make([]Difference, 0)
	caseResults := make([]CaseResult, 0, len(cases))
	execution := matrixExecution{
		runner: runner, ctx: ctx, allowances: allowances, allowedDiffs: allowedDiffs,
	}
	for _, replayCase := range cases {
		result, caseDifferences, err := execution.runCase(replayCase)
		if err != nil {
			return Report{}, err
		}
		caseResults = append(caseResults, result)
		differences = append(differences, caseDifferences...)
	}
	if unused := unusedUnsupportedAllowances(allowances); len(unused) > 0 {
		return Report{}, fmt.Errorf("unused unsupported allowance: %s", unused[0])
	}
	if err := allowedDiffs.validateConsumed(); err != nil {
		return Report{}, err
	}
	return NewMatrixReport(baselineName, caseResults, differences), nil
}

type matrixExecution struct {
	runner       Runner
	ctx          context.Context
	allowances   map[unsupportedAllowanceKey]*allowanceState
	allowedDiffs *allowedDiffTracker
}

type candidateComparison struct {
	result      CaseBackendResult
	differences []Difference
	compared    bool
	failed      bool
}

func validateRunnerInputs(backends []Backend, cases []ReplayCase) error {
	if len(backends) == 0 {
		return errors.New("run replay cases: no backends configured")
	}
	backendNames := make(map[string]struct{}, len(backends))
	for i, backend := range backends {
		if backend.Name == "" || backend.New == nil {
			return fmt.Errorf("run replay cases: backend %d is invalid", i)
		}
		if _, exists := backendNames[backend.Name]; exists {
			return fmt.Errorf("run replay cases: backend name %q is duplicated", backend.Name)
		}
		backendNames[backend.Name] = struct{}{}
	}
	caseNames := make(map[string]struct{}, len(cases))
	for _, replayCase := range cases {
		if replayCase.Name == "" {
			return errors.New("run replay cases: case name is empty")
		}
		if _, exists := caseNames[replayCase.Name]; exists {
			return fmt.Errorf("run replay cases: case name %q is duplicated", replayCase.Name)
		}
		if err := validateReplayCase(replayCase); err != nil {
			return fmt.Errorf("run replay case %q: %w", replayCase.Name, err)
		}
		caseNames[replayCase.Name] = struct{}{}
	}
	return nil
}

func validateReplayCase(replayCase ReplayCase) error {
	for i, operation := range replayCase.Operations {
		if len(operation.After) > 0 {
			return fmt.Errorf("operation %d has top-level dependencies", i)
		}
		if err := validateOperationTree(operation); err != nil {
			return fmt.Errorf("operation %d (%s): %w", i, operation.Kind, err)
		}
	}
	if err := validateSnapshotInvariants(replayCase.Invariants); err != nil {
		return err
	}
	return nil
}

func validateOperationTree(operation Operation) error {
	if err := operation.Validate(); err != nil {
		return fmt.Errorf("validate operation: %w", err)
	}
	if operation.Kind != OperationParallel {
		return nil
	}
	if _, err := parallelDependencies(operation.Parallel); err != nil {
		return err
	}
	for i, child := range operation.Parallel {
		if err := validateOperationTree(child); err != nil {
			return fmt.Errorf("parallel operation %d (%s): %w", i, child.Kind, err)
		}
	}
	return nil
}

func validateSnapshotInvariants(invariants []SnapshotInvariant) error {
	for i, invariant := range invariants {
		if invariant.Name == "" || invariant.Check == nil {
			return fmt.Errorf("snapshot invariant %d is invalid", i)
		}
	}
	return nil
}

func (execution matrixExecution) runCase(
	replayCase ReplayCase,
) (CaseResult, []Difference, error) {
	baselineBackend := execution.runner.Backends[0]
	baseline, err := execution.runner.runCase(execution.ctx, baselineBackend, replayCase)
	if err != nil {
		return CaseResult{}, nil, fmt.Errorf(
			"run replay case %q on baseline %q: %w",
			replayCase.Name, baselineBackend.Name, err,
		)
	}
	result := CaseResult{Case: replayCase.Name}
	differences := make([]Difference, 0)
	comparedCandidates := 0
	caseFailed := false
	for _, backend := range execution.runner.Backends[1:] {
		comparison, err := execution.compareCandidate(replayCase, baseline, backend)
		if err != nil {
			return CaseResult{}, nil, err
		}
		result.Backends = append(result.Backends, comparison.result)
		differences = append(differences, comparison.differences...)
		if comparison.compared {
			comparedCandidates++
		}
		caseFailed = caseFailed || comparison.failed
	}
	result.Status = aggregateCaseStatus(caseFailed, comparedCandidates)
	return result, differences, nil
}

func (execution matrixExecution) compareCandidate(
	replayCase ReplayCase,
	baseline Snapshot,
	backend Backend,
) (candidateComparison, error) {
	actual, unsupported, err := execution.runner.runComparableCase(
		execution.ctx, backend, replayCase,
	)
	if err != nil {
		return candidateComparison{}, fmt.Errorf(
			"run replay case %q on backend %q: %w", replayCase.Name, backend.Name, err,
		)
	}
	if len(unsupported) > 0 {
		differences, allowed := unsupportedDifferences(
			replayCase.Name, backend.Name, unsupported, execution.allowances,
		)
		return candidateComparison{
			result: CaseBackendResult{
				Backend: backend.Name, Status: ResultUnsupported,
				Unsupported: append([]Capability(nil), unsupported...),
			},
			differences: differences,
			failed:      !allowed,
		}, nil
	}
	if err := validateSnapshot(replayCase, actual); err != nil {
		return candidateComparison{}, fmt.Errorf(
			"validate replay case %q on backend %q: %w",
			replayCase.Name, backend.Name, err,
		)
	}
	differences, err := compareSnapshots(CompareInput{
		Case: replayCase.Name, Backend: backend.Name,
		Baseline: baseline, Actual: actual,
		Options: execution.runner.CompareOptions,
	}, execution.allowedDiffs)
	if err != nil {
		return candidateComparison{}, fmt.Errorf("compare replay snapshots: %w", err)
	}
	status := differencesStatus(differences)
	return candidateComparison{
		result:      CaseBackendResult{Backend: backend.Name, Status: status},
		differences: differences,
		compared:    true,
		failed:      status == ResultFail,
	}, nil
}

func differencesStatus(differences []Difference) ResultStatus {
	for _, difference := range differences {
		if !difference.AllowedDiff {
			return ResultFail
		}
	}
	return ResultPass
}

func aggregateCaseStatus(failed bool, comparedCandidates int) ResultStatus {
	if failed {
		return ResultFail
	}
	if comparedCandidates == 0 {
		return ResultInconclusive
	}
	return ResultPass
}

func (runner Runner) runComparableCase(
	ctx context.Context,
	backend Backend,
	replayCase ReplayCase,
) (Snapshot, []Capability, error) {
	fixture, err := backend.New(ctx, replayCase.Name)
	if err != nil {
		return Snapshot{}, nil, fmt.Errorf("create fixture: %w", err)
	}
	if fixture == nil {
		return Snapshot{}, nil, fmt.Errorf("create fixture: returned nil")
	}
	if fixture.Name() != backend.Name {
		nameErr := fmt.Errorf(
			"create fixture: name %q does not match backend %q",
			fixture.Name(),
			backend.Name,
		)
		return Snapshot{}, nil, errors.Join(nameErr, fixture.Close())
	}
	missing := fixture.Capabilities().Missing(replayCase.Capabilities...)
	if len(missing) > 0 {
		if err := fixture.Close(); err != nil {
			return Snapshot{}, nil, fmt.Errorf("close unsupported fixture: %w", err)
		}
		return Snapshot{}, missing, nil
	}
	snapshot, err := executeCase(ctx, fixture, replayCase)
	if err != nil {
		return Snapshot{}, nil, err
	}
	normalized, err := NormalizeSnapshotWithError(snapshot, runner.NormalizeOptions)
	if err != nil {
		return Snapshot{}, nil, fmt.Errorf("normalize snapshot: %w", err)
	}
	return normalized, nil, nil
}

func (runner Runner) runCase(
	ctx context.Context,
	backend Backend,
	replayCase ReplayCase,
) (Snapshot, error) {
	snapshot, missing, err := runner.runComparableCase(ctx, backend, replayCase)
	if err != nil {
		return Snapshot{}, err
	}
	if len(missing) > 0 {
		return Snapshot{}, fmt.Errorf("missing capabilities: %v", missing)
	}
	if err := validateSnapshot(replayCase, snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func validateSnapshot(replayCase ReplayCase, snapshot Snapshot) error {
	if err := validateSnapshotInvariants(replayCase.Invariants); err != nil {
		return err
	}
	for _, invariant := range replayCase.Invariants {
		if err := invariant.Check(cloneSnapshot(snapshot)); err != nil {
			return fmt.Errorf(
				"snapshot invariant %q: %w", invariant.Name, err,
			)
		}
	}
	return nil
}

func executeCase(ctx context.Context, fixture Fixture, replayCase ReplayCase) (
	snapshot Snapshot,
	err error,
) {
	defer func() {
		err = errors.Join(err, fixture.Close())
	}()
	for i, operation := range replayCase.Operations {
		if len(operation.After) > 0 {
			return Snapshot{}, fmt.Errorf("operation %d has top-level dependencies", i)
		}
		if err := executeOperation(ctx, fixture, operation); err != nil {
			return Snapshot{}, fmt.Errorf("operation %d (%s): %w", i, operation.Kind, err)
		}
	}
	snapshot, err = fixture.Snapshot(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read snapshot: %w", err)
	}
	return isolateSnapshot(snapshot)
}

func executeOperation(ctx context.Context, fixture Fixture, operation Operation) error {
	if err := operation.Validate(); err != nil {
		return fmt.Errorf("validate operation: %w", err)
	}
	operation = cloneOperation(operation)
	var err error
	if operation.InjectedFailure != "" {
		injector, ok := fixture.(FaultInjector)
		if !ok {
			return fmt.Errorf("fixture %q does not support fault injection", fixture.Name())
		}
		err = injector.ApplyWithFault(ctx, operation)
	} else if operation.Kind == OperationParallel {
		err = executeParallel(ctx, fixture, operation.Parallel)
	} else {
		err = fixture.Apply(ctx, operation)
	}
	if operation.ExpectFailure {
		if err == nil {
			return fmt.Errorf("expected injected failure, got nil")
		}
		if !errors.Is(err, ErrInjectedFailure) {
			return fmt.Errorf("expected injected failure: %w", err)
		}
		return nil
	}
	return err
}

func executeParallel(ctx context.Context, fixture Fixture, operations []Operation) error {
	done, err := parallelDependencies(operations)
	if err != nil {
		return err
	}
	errorsByIndex := make([]error, len(operations))
	ready := make(chan struct{}, len(operations))
	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	waitGroup.Add(len(operations))
	for i := range operations {
		go func(index int) {
			defer waitGroup.Done()
			ready <- struct{}{}
			<-start
			if err := ctx.Err(); err != nil {
				errorsByIndex[index] = err
				return
			}
			for _, dependency := range operations[index].After {
				select {
				case <-done[dependency]:
				case <-ctx.Done():
					errorsByIndex[index] = ctx.Err()
					return
				}
			}
			errorsByIndex[index] = executeOperation(ctx, fixture, operations[index])
			if operations[index].Name != "" {
				close(done[operations[index].Name])
			}
		}(i)
	}
	for range operations {
		<-ready
	}
	close(start)
	waitGroup.Wait()
	return errors.Join(errorsByIndex...)
}

func parallelDependencies(operations []Operation) (map[string]chan struct{}, error) {
	done := make(map[string]chan struct{})
	dependencies := make(map[string][]string)
	for i, operation := range operations {
		if err := operation.Validate(); err != nil {
			return nil, fmt.Errorf("parallel operation %d: %w", i, err)
		}
		if operation.Name == "" {
			if len(operation.After) > 0 {
				return nil, fmt.Errorf("parallel operation %d with dependencies requires name", i)
			}
			continue
		}
		if _, exists := done[operation.Name]; exists {
			return nil, fmt.Errorf("duplicate parallel operation name %q", operation.Name)
		}
		done[operation.Name] = make(chan struct{})
		dependencies[operation.Name] = operation.After
	}
	for name, after := range dependencies {
		for _, dependency := range after {
			if dependency == name {
				return nil, fmt.Errorf("parallel operation %q depends on itself", name)
			}
			if _, exists := done[dependency]; !exists {
				return nil, fmt.Errorf("parallel operation %q has unknown dependency %q", name, dependency)
			}
		}
	}
	if hasDependencyCycle(dependencies) {
		return nil, fmt.Errorf("parallel operations contain dependency cycle")
	}
	if err := validateParallelSessionCreateConsumers(operations, dependencies); err != nil {
		return nil, err
	}
	if err := validateParallelStateMutations(operations, dependencies); err != nil {
		return nil, err
	}
	if err := validateParallelOrderedSessionAppends(operations, dependencies); err != nil {
		return nil, err
	}
	if err := validateParallelSummaryUpdates(operations, dependencies); err != nil {
		return nil, err
	}
	if err := validateParallelSessionAppendSummaryOrdering(operations, dependencies); err != nil {
		return nil, err
	}
	if err := validateParallelMemoryAccesses(operations, dependencies); err != nil {
		return nil, err
	}
	return done, nil
}

func validateParallelSessionCreateConsumers(
	operations []Operation,
	dependencies map[string][]string,
) error {
	creates := make([]map[string]struct{}, len(operations))
	consumers := make([]map[string]struct{}, len(operations))
	for i, operation := range operations {
		creates[i] = operationSessionCreates(operation)
		consumers[i] = operationSessionConsumers(operation)
	}
	for i := 0; i < len(operations); i++ {
		for j := i + 1; j < len(operations); j++ {
			if sessionID, ok := firstOverlappingSessionID(creates[i], creates[j]); ok {
				return fmt.Errorf("parallel create session operations for session %q are duplicated", sessionID)
			}
			if sessionID, ok := firstOverlappingSessionID(creates[i], consumers[j]); ok &&
				!parallelOperationDependsOnOperation(operations[j], operations[i], dependencies) {
				return fmt.Errorf(
					"parallel session consumer for session %q must depend on create session",
					sessionID,
				)
			}
			if sessionID, ok := firstOverlappingSessionID(creates[j], consumers[i]); ok &&
				!parallelOperationDependsOnOperation(operations[i], operations[j], dependencies) {
				return fmt.Errorf(
					"parallel session consumer for session %q must depend on create session",
					sessionID,
				)
			}
		}
	}
	return nil
}

func operationSessionCreates(operation Operation) map[string]struct{} {
	creates := make(map[string]struct{})
	collectSessionCreates(operation, creates)
	return creates
}

func collectSessionCreates(operation Operation, creates map[string]struct{}) {
	switch operation.Kind {
	case OperationCreateSession:
		creates[operation.SessionID] = struct{}{}
	case OperationParallel:
		for _, child := range operation.Parallel {
			collectSessionCreates(child, creates)
		}
	}
}

func operationSessionConsumers(operation Operation) map[string]struct{} {
	consumers := make(map[string]struct{})
	collectSessionConsumers(operation, consumers)
	return consumers
}

func collectSessionConsumers(operation Operation, consumers map[string]struct{}) {
	switch operation.Kind {
	case OperationAppendEvent, OperationUpdateState, OperationUpdateSummary, OperationAppendTrack:
		consumers[operation.SessionID] = struct{}{}
	case OperationParallel:
		for _, child := range operation.Parallel {
			collectSessionConsumers(child, consumers)
		}
	}
}

func firstOverlappingSessionID(
	left map[string]struct{},
	right map[string]struct{},
) (string, bool) {
	ids := make([]string, 0)
	for id := range left {
		if _, exists := right[id]; exists {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return "", false
	}
	return ids[0], true
}

type stateMutationRef struct {
	sessionID string
	key       string
}

func validateParallelStateMutations(
	operations []Operation,
	dependencies map[string][]string,
) error {
	mutations := make([]map[stateMutationRef]struct{}, len(operations))
	for i, operation := range operations {
		mutations[i] = operationStateMutations(operation)
	}
	for i := 0; i < len(operations); i++ {
		for j := i + 1; j < len(operations); j++ {
			if parallelOperationsOrdered(operations[i], operations[j], dependencies) {
				continue
			}
			overlap := overlappingStateMutations(mutations[i], mutations[j])
			if len(overlap) > 0 {
				ref := overlap[0]
				return fmt.Errorf(
					"parallel state operations for session %q key %q must be ordered with dependencies",
					ref.sessionID, ref.key,
				)
			}
		}
	}
	return nil
}

func overlappingStateMutations(
	left map[stateMutationRef]struct{},
	right map[stateMutationRef]struct{},
) []stateMutationRef {
	refs := make([]stateMutationRef, 0)
	for ref := range left {
		if _, exists := right[ref]; exists {
			refs = append(refs, ref)
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].sessionID != refs[j].sessionID {
			return refs[i].sessionID < refs[j].sessionID
		}
		return refs[i].key < refs[j].key
	})
	return refs
}

func operationStateMutations(operation Operation) map[stateMutationRef]struct{} {
	mutations := make(map[stateMutationRef]struct{})
	collectStateMutations(operation, mutations)
	return mutations
}

func collectStateMutations(operation Operation, mutations map[stateMutationRef]struct{}) {
	switch operation.Kind {
	case OperationAppendEvent:
		if operation.Event == nil {
			return
		}
		for key := range operation.Event.StateDelta {
			mutations[stateMutationRef{sessionID: operation.SessionID, key: key}] = struct{}{}
		}
	case OperationUpdateState:
		for key := range operation.StateUpdates {
			mutations[stateMutationRef{sessionID: operation.SessionID, key: key}] = struct{}{}
		}
		for _, key := range operation.StateDeletes {
			mutations[stateMutationRef{sessionID: operation.SessionID, key: key}] = struct{}{}
		}
	case OperationParallel:
		for _, child := range operation.Parallel {
			collectStateMutations(child, mutations)
		}
	}
}

type orderedSessionAppendRef struct {
	sessionID  string
	collection string
}

func validateParallelOrderedSessionAppends(
	operations []Operation,
	dependencies map[string][]string,
) error {
	appends := make([]map[orderedSessionAppendRef]struct{}, len(operations))
	for i, operation := range operations {
		appends[i] = operationOrderedSessionAppends(operation)
	}
	for i := 0; i < len(operations); i++ {
		for j := i + 1; j < len(operations); j++ {
			if parallelOperationsOrdered(operations[i], operations[j], dependencies) {
				continue
			}
			overlap := overlappingOrderedSessionAppends(appends[i], appends[j])
			if len(overlap) > 0 {
				ref := overlap[0]
				return fmt.Errorf(
					"parallel append operations for session %q collection %q must be ordered with dependencies",
					ref.sessionID, ref.collection,
				)
			}
		}
	}
	return nil
}

func operationOrderedSessionAppends(operation Operation) map[orderedSessionAppendRef]struct{} {
	appends := make(map[orderedSessionAppendRef]struct{})
	collectOrderedSessionAppends(operation, appends)
	return appends
}

func collectOrderedSessionAppends(
	operation Operation,
	appends map[orderedSessionAppendRef]struct{},
) {
	switch operation.Kind {
	case OperationAppendEvent:
		appends[orderedSessionAppendRef{
			sessionID: operation.SessionID, collection: "events",
		}] = struct{}{}
	case OperationAppendTrack:
		appends[orderedSessionAppendRef{
			sessionID: operation.SessionID, collection: "track:" + operation.TrackName,
		}] = struct{}{}
	case OperationParallel:
		for _, child := range operation.Parallel {
			collectOrderedSessionAppends(child, appends)
		}
	}
}

func overlappingOrderedSessionAppends(
	left map[orderedSessionAppendRef]struct{},
	right map[orderedSessionAppendRef]struct{},
) []orderedSessionAppendRef {
	refs := make([]orderedSessionAppendRef, 0)
	for ref := range left {
		if _, exists := right[ref]; exists {
			refs = append(refs, ref)
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].sessionID != refs[j].sessionID {
			return refs[i].sessionID < refs[j].sessionID
		}
		return refs[i].collection < refs[j].collection
	})
	return refs
}

type summaryUpdateRef struct {
	sessionID string
	filterKey string
}

func validateParallelSummaryUpdates(
	operations []Operation,
	dependencies map[string][]string,
) error {
	updates := make([]map[summaryUpdateRef]struct{}, len(operations))
	for i, operation := range operations {
		updates[i] = operationSummaryUpdates(operation)
	}
	for i := 0; i < len(operations); i++ {
		for j := i + 1; j < len(operations); j++ {
			if parallelOperationsOrdered(operations[i], operations[j], dependencies) {
				continue
			}
			overlap := overlappingSummaryUpdates(updates[i], updates[j])
			if len(overlap) > 0 {
				ref := overlap[0]
				return fmt.Errorf(
					"parallel summary updates for session %q filter %q must be ordered with dependencies",
					ref.sessionID, ref.filterKey,
				)
			}
		}
	}
	return nil
}

func operationSummaryUpdates(operation Operation) map[summaryUpdateRef]struct{} {
	updates := make(map[summaryUpdateRef]struct{})
	collectSummaryUpdates(operation, updates)
	return updates
}

func collectSummaryUpdates(operation Operation, updates map[summaryUpdateRef]struct{}) {
	switch operation.Kind {
	case OperationUpdateSummary:
		if operation.Summary == nil {
			return
		}
		updates[summaryUpdateRef{
			sessionID: operation.SessionID,
			filterKey: operation.Summary.FilterKey,
		}] = struct{}{}
	case OperationParallel:
		for _, child := range operation.Parallel {
			collectSummaryUpdates(child, updates)
		}
	}
}

func overlappingSummaryUpdates(
	left map[summaryUpdateRef]struct{},
	right map[summaryUpdateRef]struct{},
) []summaryUpdateRef {
	refs := make([]summaryUpdateRef, 0)
	for ref := range left {
		if _, exists := right[ref]; exists {
			refs = append(refs, ref)
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].sessionID != refs[j].sessionID {
			return refs[i].sessionID < refs[j].sessionID
		}
		return refs[i].filterKey < refs[j].filterKey
	})
	return refs
}

func validateParallelSessionAppendSummaryOrdering(
	operations []Operation,
	dependencies map[string][]string,
) error {
	appends := make([]map[orderedSessionAppendRef]struct{}, len(operations))
	summaries := make([]map[summaryUpdateRef]struct{}, len(operations))
	for i, operation := range operations {
		appends[i] = operationOrderedSessionAppends(operation)
		summaries[i] = operationSummaryUpdates(operation)
	}
	for i := 0; i < len(operations); i++ {
		for j := i + 1; j < len(operations); j++ {
			if parallelOperationsOrdered(operations[i], operations[j], dependencies) {
				continue
			}
			if sessionID, ok := firstOverlappingSessionAppendSummary(appends[i], summaries[j]); ok {
				return fmt.Errorf(
					"parallel append event and summary update for session %q must be ordered with dependencies",
					sessionID,
				)
			}
			if sessionID, ok := firstOverlappingSessionAppendSummary(appends[j], summaries[i]); ok {
				return fmt.Errorf(
					"parallel append event and summary update for session %q must be ordered with dependencies",
					sessionID,
				)
			}
		}
	}
	return nil
}

func firstOverlappingSessionAppendSummary(
	appends map[orderedSessionAppendRef]struct{},
	summaries map[summaryUpdateRef]struct{},
) (string, bool) {
	appendSessions := make(map[string]struct{})
	for appendRef := range appends {
		if appendRef.collection == "events" {
			appendSessions[appendRef.sessionID] = struct{}{}
		}
	}
	summarySessions := make(map[string]struct{}, len(summaries))
	for summaryRef := range summaries {
		summarySessions[summaryRef.sessionID] = struct{}{}
	}
	return firstOverlappingSessionID(appendSessions, summarySessions)
}

type memoryScopeRef struct {
	appName string
	userID  string
}

func validateParallelMemoryAccesses(
	operations []Operation,
	dependencies map[string][]string,
) error {
	writes := make([]map[memoryScopeRef]struct{}, len(operations))
	searches := make([]map[memoryScopeRef]struct{}, len(operations))
	for i, operation := range operations {
		writes[i] = operationMemoryWrites(operation)
		searches[i] = operationMemorySearches(operation)
	}
	for i := 0; i < len(operations); i++ {
		for j := i + 1; j < len(operations); j++ {
			if parallelOperationsOrdered(operations[i], operations[j], dependencies) {
				continue
			}
			if scope, ok := firstOverlappingMemoryScope(writes[i], writes[j]); ok {
				return fmt.Errorf(
					"parallel memory writes for app %q user %q must be ordered with dependencies",
					scope.appName, scope.userID,
				)
			}
			if scope, ok := firstOverlappingMemoryScope(writes[i], searches[j]); ok {
				return fmt.Errorf(
					"parallel memory search and write for app %q user %q must be ordered with dependencies",
					scope.appName, scope.userID,
				)
			}
			if scope, ok := firstOverlappingMemoryScope(searches[i], writes[j]); ok {
				return fmt.Errorf(
					"parallel memory search and write for app %q user %q must be ordered with dependencies",
					scope.appName, scope.userID,
				)
			}
		}
	}
	return nil
}

func operationMemoryWrites(operation Operation) map[memoryScopeRef]struct{} {
	writes := make(map[memoryScopeRef]struct{})
	collectMemoryWrites(operation, writes)
	return writes
}

func collectMemoryWrites(operation Operation, writes map[memoryScopeRef]struct{}) {
	switch operation.Kind {
	case OperationWriteMemory:
		if operation.Memory == nil {
			return
		}
		writes[memoryScopeRef{
			appName: operation.Memory.AppName,
			userID:  operation.Memory.UserID,
		}] = struct{}{}
	case OperationParallel:
		for _, child := range operation.Parallel {
			collectMemoryWrites(child, writes)
		}
	}
}

func operationMemorySearches(operation Operation) map[memoryScopeRef]struct{} {
	searches := make(map[memoryScopeRef]struct{})
	collectMemorySearches(operation, searches)
	return searches
}

func collectMemorySearches(operation Operation, searches map[memoryScopeRef]struct{}) {
	switch operation.Kind {
	case OperationSearchMemory:
		searches[memoryScopeRef{
			appName: operation.SearchAppName,
			userID:  operation.SearchUserID,
		}] = struct{}{}
	case OperationParallel:
		for _, child := range operation.Parallel {
			collectMemorySearches(child, searches)
		}
	}
}

func firstOverlappingMemoryScope(
	left map[memoryScopeRef]struct{},
	right map[memoryScopeRef]struct{},
) (memoryScopeRef, bool) {
	refs := make([]memoryScopeRef, 0)
	for ref := range left {
		if _, exists := right[ref]; exists {
			refs = append(refs, ref)
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].appName != refs[j].appName {
			return refs[i].appName < refs[j].appName
		}
		return refs[i].userID < refs[j].userID
	})
	if len(refs) == 0 {
		return memoryScopeRef{}, false
	}
	return refs[0], true
}

func parallelOperationsOrdered(
	first Operation,
	second Operation,
	dependencies map[string][]string,
) bool {
	if first.Name == "" || second.Name == "" {
		return false
	}
	return parallelOperationDependsOn(first.Name, second.Name, dependencies, nil) ||
		parallelOperationDependsOn(second.Name, first.Name, dependencies, nil)
}

func parallelOperationDependsOnOperation(
	operation Operation,
	dependency Operation,
	dependencies map[string][]string,
) bool {
	if operation.Name == "" || dependency.Name == "" {
		return false
	}
	return parallelOperationDependsOn(operation.Name, dependency.Name, dependencies, nil)
}

func parallelOperationDependsOn(
	name string,
	dependency string,
	dependencies map[string][]string,
	visited map[string]struct{},
) bool {
	if visited == nil {
		visited = make(map[string]struct{})
	}
	if _, exists := visited[name]; exists {
		return false
	}
	visited[name] = struct{}{}
	for _, after := range dependencies[name] {
		if after == dependency ||
			parallelOperationDependsOn(after, dependency, dependencies, visited) {
			return true
		}
	}
	return false
}

func hasDependencyCycle(dependencies map[string][]string) bool {
	const (
		unvisited = iota
		visiting
		visited
	)
	states := make(map[string]int, len(dependencies))
	var visit func(string) bool
	visit = func(name string) bool {
		switch states[name] {
		case visiting:
			return true
		case visited:
			return false
		}
		states[name] = visiting
		for _, dependency := range dependencies[name] {
			if visit(dependency) {
				return true
			}
		}
		states[name] = visited
		return false
	}
	for name := range dependencies {
		if visit(name) {
			return true
		}
	}
	return false
}

func unsupportedDifferences(
	caseName string,
	backend string,
	capabilities []Capability,
	allowances map[unsupportedAllowanceKey]*allowanceState,
) ([]Difference, bool) {
	differences := make([]Difference, 0, len(capabilities))
	allAllowed := true
	for _, capability := range capabilities {
		key := unsupportedAllowanceKey{
			backend: backend, caseName: caseName, capability: capability,
		}
		allowance := allowances[key]
		explanation := fmt.Sprintf("backend does not support %s", capability)
		allowed := allowance != nil
		if allowance != nil {
			allowance.consumed = true
			explanation = allowance.reason
		} else {
			allAllowed = false
		}
		differences = append(differences, Difference{
			Case:            caseName,
			Backend:         backend,
			Path:            "$.unsupported." + string(capability),
			Baseline:        missingValueMarker,
			Actual:          string(capability),
			BaselineMissing: true,
			AllowedDiff:     allowed,
			Explanation:     explanation,
		})
	}
	return differences, allAllowed
}

func validateAllowedDiffRuleScopes(
	rules []AllowedDiffRule,
	backends []Backend,
	cases []ReplayCase,
) error {
	backendNames := candidateBackendNames(backends)
	caseNames := make(map[string]struct{}, len(cases))
	for _, replayCase := range cases {
		caseNames[replayCase.Name] = struct{}{}
	}
	for i, rule := range rules {
		if _, ok := backendNames[rule.Backend]; !ok {
			if len(backends) > 0 && rule.Backend == backends[0].Name {
				return fmt.Errorf(
					"allowed diff rule %d references baseline backend %q",
					i, rule.Backend,
				)
			}
			return fmt.Errorf(
				"allowed diff rule %d references unknown backend %q",
				i, rule.Backend,
			)
		}
		if _, ok := caseNames[rule.Case]; !ok {
			return fmt.Errorf(
				"allowed diff rule %d references unknown case %q",
				i, rule.Case,
			)
		}
	}
	return nil
}

type unsupportedAllowanceKey struct {
	backend    string
	caseName   string
	capability Capability
}

type allowanceState struct {
	reason   string
	consumed bool
}

func validateUnsupportedAllowances(
	configured []UnsupportedAllowance,
	backends []Backend,
	cases []ReplayCase,
) (map[unsupportedAllowanceKey]*allowanceState, error) {
	backendNames := candidateBackendNames(backends)
	caseNames := make(map[string]struct{}, len(cases))
	for _, replayCase := range cases {
		caseNames[replayCase.Name] = struct{}{}
	}
	validated := make(map[unsupportedAllowanceKey]*allowanceState, len(configured))
	for i, allowance := range configured {
		if allowance.Backend == "" || allowance.Case == "" ||
			allowance.Capability == "" || allowance.Reason == "" {
			return nil, fmt.Errorf("unsupported allowance %d has empty fields", i)
		}
		if _, ok := backendNames[allowance.Backend]; !ok {
			if len(backends) > 0 && allowance.Backend == backends[0].Name {
				return nil, fmt.Errorf(
					"unsupported allowance %d references baseline backend %q",
					i, allowance.Backend,
				)
			}
			return nil, fmt.Errorf(
				"unsupported allowance %d references unknown backend %q",
				i, allowance.Backend,
			)
		}
		if _, ok := caseNames[allowance.Case]; !ok {
			return nil, fmt.Errorf(
				"unsupported allowance %d references unknown case %q",
				i, allowance.Case,
			)
		}
		key := unsupportedAllowanceKey{
			backend: allowance.Backend, caseName: allowance.Case,
			capability: allowance.Capability,
		}
		if _, exists := validated[key]; exists {
			return nil, fmt.Errorf("unsupported allowance %d is duplicated", i)
		}
		validated[key] = &allowanceState{reason: allowance.Reason}
	}
	return validated, nil
}

func candidateBackendNames(backends []Backend) map[string]struct{} {
	names := make(map[string]struct{}, len(backends))
	if len(backends) <= 1 {
		return names
	}
	for _, backend := range backends[1:] {
		names[backend.Name] = struct{}{}
	}
	return names
}

func unusedUnsupportedAllowances(
	allowances map[unsupportedAllowanceKey]*allowanceState,
) []string {
	unused := make([]string, 0)
	for key, state := range allowances {
		if state.consumed {
			continue
		}
		unused = append(unused, fmt.Sprintf(
			"backend=%s case=%s capability=%s",
			key.backend, key.caseName, key.capability,
		))
	}
	sort.Strings(unused)
	return unused
}
