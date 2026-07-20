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
	allowances, err := validateUnsupportedAllowances(
		runner.UnsupportedAllowances, runner.Backends, cases,
	)
	if err != nil {
		return Report{}, err
	}
	baselineName := runner.Backends[0].Name
	differences := make([]Difference, 0)
	caseResults := make([]CaseResult, 0, len(cases))
	execution := matrixExecution{runner: runner, ctx: ctx, allowances: allowances}
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
	return NewMatrixReport(baselineName, caseResults, differences), nil
}

type matrixExecution struct {
	runner     Runner
	ctx        context.Context
	allowances map[unsupportedAllowanceKey]*allowanceState
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
	for i, backend := range backends {
		if backend.Name == "" || backend.New == nil {
			return fmt.Errorf("run replay cases: backend %d is invalid", i)
		}
	}
	for _, replayCase := range cases {
		if replayCase.Name == "" {
			return errors.New("run replay cases: case name is empty")
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
	differences, err := CompareSnapshots(CompareInput{
		Case: replayCase.Name, Backend: backend.Name,
		Baseline: baseline, Actual: actual,
		Options: execution.runner.CompareOptions,
	})
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
	return NormalizeSnapshot(snapshot, runner.NormalizeOptions), nil, nil
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
	for i, invariant := range replayCase.Invariants {
		if invariant.Name == "" || invariant.Check == nil {
			return fmt.Errorf("snapshot invariant %d is invalid", i)
		}
		if err := invariant.Check(snapshot); err != nil {
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
	return snapshot, nil
}

func executeOperation(ctx context.Context, fixture Fixture, operation Operation) error {
	if err := operation.Validate(); err != nil {
		return fmt.Errorf("validate operation: %w", err)
	}
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
	return done, nil
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
			Case:        caseName,
			Backend:     backend,
			Path:        "$.unsupported." + string(capability),
			Actual:      string(capability),
			AllowedDiff: allowed,
			Explanation: explanation,
		})
	}
	return differences, allAllowed
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
	backendNames := make(map[string]struct{}, len(backends))
	for _, backend := range backends {
		backendNames[backend.Name] = struct{}{}
	}
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
