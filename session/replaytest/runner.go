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
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	summaryIsolationSessionSuffix = "-summary-isolation"
	truncatedErrorSuffix          = "... [truncated]"
)

// Defensive limits keep malformed or hostile adapters from exhausting the
// replay process. They are intentionally conservative for a consistency test
// harness; larger workloads should be split into multiple cases.
const (
	// Consensus compares every backend pair, so bounding the matrix prevents
	// hostile or accidental configuration from causing quadratic resource use.
	maxReplayBackends                  = 64
	maxReplayCases                     = 1_000
	maxReplayAllowedDiffs              = 1_000
	maxReplayDiffsPerCase              = 10_000
	maxReplayReportDiffs               = 100_000
	maxReplayReportSize                = 64 << 20
	maxReplayIdentifierSize            = 4 << 10
	maxReplayPathSize                  = 16 << 10
	maxReplayExplanationSize           = 64 << 10
	maxReplayErrorSize                 = 64 << 10
	maxReplayCapabilityCount           = 15
	maxReplaySteps                     = 10_000
	maxReplayBranches                  = 2_000
	maxReplayEvents                    = 100_000
	maxReplayMemories                  = 100_000
	maxReplayJSONBytes                 = 8 << 20
	maxReplaySnapshotSize              = maxReplayJSONBytes
	maxReplayJSONDepth                 = 256
	maxReplayStateValueSize            = 1 << 20
	maxReplayStateTotalSize            = 8 << 20
	maxReplayStateKeySize              = 4 << 10
	maxReplayStateKeyTotalSize         = 8 << 20
	maxReplayStateKeyCount             = 100_000
	maxReplayMemorySize                = 8 << 20
	maxReplayMemoryTotalSize           = 64 << 20
	maxReplayMemorySearchNameSize      = 4 << 10
	maxReplayMemorySearchNameTotalSize = 8 << 20
	maxReplayMemorySearchCount         = 100_000
	maxReplayMemorySearchTotalSize     = 64 << 20
	maxReplaySummarySize               = 8 << 20
	maxReplaySummaryCount              = 100_000
	maxReplaySummaryKeySize            = 4 << 10
	maxReplaySummaryKeyTotalSize       = 8 << 20
	maxReplaySummaryTotalSize          = 8 << 20
	maxReplayTrackPayload              = 8 << 20
	maxReplayTrackCount                = 100_000
	maxReplayTrackNameSize             = 4 << 10
	maxReplayTrackNameTotalSize        = 8 << 20
	maxReplayTrackEvents               = 100_000
	maxReplayTrackTotalSize            = 8 << 20
	maxReplayEventSize                 = 8 << 20
	maxReplayEventsTotalSize           = 64 << 20
	maxReplayExpirationWait            = 5 * time.Second
	// Probe cleanup must outlive the replay context so cancellation cannot leave
	// persistent test data behind. Cooperative adapters can observe this bound;
	// adapters that ignore context may still block their caller.
	summaryProbeCleanupTimeout = 5 * time.Second
)

// Runner executes cases using either a named reference or oracle-free
// pairwise consensus.
type Runner struct {
	// Reference names the reference backend. An empty value selects the first
	// backend in reference mode; consensus mode requires Reference to be empty.
	Reference string
	// Mode selects the comparison strategy. Its zero value selects
	// ComparisonReference; ComparisonConsensus requires Reference to be empty.
	Mode ComparisonMode
	// Now supplies the report timestamp. A nil value uses time.Now.
	Now func() time.Time
}

// Run executes the complete matrix and returns a validated report. It stops
// without a partial report when ctx is canceled or comparison cannot continue;
// individual backend execution failures are recorded as blocking differences.
// Inputs and adapter outputs that exceed the fixed defensive limits documented
// in README.md are rejected.
func (r Runner) Run(
	ctx context.Context,
	cases []Case,
	backends []Backend,
) (Report, error) {
	if ctx == nil {
		return Report{}, errors.New("replaytest: context is required")
	}
	if len(cases) == 0 {
		return Report{}, errors.New("replaytest: no cases")
	}
	if len(cases) > maxReplayCases {
		return Report{}, fmt.Errorf("replaytest: %d cases exceed limit %d", len(cases), maxReplayCases)
	}
	if err := validateBackends(backends); err != nil {
		return Report{}, err
	}
	mode, reference, err := r.resolveComparison(backends)
	if err != nil {
		return Report{}, err
	}
	backendNames := make(map[string]struct{}, len(backends))
	for _, backend := range backends {
		backendNames[backend.Name] = struct{}{}
	}
	if err := validateCases(cases, backendNames); err != nil {
		return Report{}, err
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	report := newReport(r, cases, backends, mode, reference)
	reportBudget, err := newReportSizeBudget(report)
	if err != nil {
		return Report{}, err
	}
	totalDiffs := 0
	for _, replayCase := range cases {
		result, err := runCase(ctx, replayCase, backends, mode, reference)
		if err != nil {
			return Report{}, err
		}
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		if len(result.Diffs) > maxReplayReportDiffs-totalDiffs {
			return Report{}, fmt.Errorf("replaytest: report exceeds %d diffs", maxReplayReportDiffs)
		}
		totalDiffs += len(result.Diffs)
		if err := reportBudget.consumeCase(result, len(report.Cases)); err != nil {
			return Report{}, err
		}
		addCaseResult(&report, result)
	}
	if err := report.Validate(); err != nil {
		return Report{}, err
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	return report, nil
}

func (r Runner) resolveComparison(backends []Backend) (ComparisonMode, string, error) {
	mode := r.Mode
	if mode == "" {
		mode = ComparisonReference
	}
	if err := validateBoundedUTF8String("comparison mode", string(mode), maxReplayIdentifierSize); err != nil {
		return "", "", fmt.Errorf("replaytest: %w", err)
	}
	if mode != ComparisonReference && mode != ComparisonConsensus {
		return "", "", fmt.Errorf("replaytest: unknown comparison mode %q", mode)
	}
	reference := r.Reference
	if err := validateBoundedUTF8String("reference backend", reference, maxReplayIdentifierSize); err != nil {
		return "", "", fmt.Errorf("replaytest: %w", err)
	}
	if mode == ComparisonConsensus {
		if reference != "" {
			return "", "", errors.New("replaytest: consensus mode does not use a reference backend")
		}
		return mode, "", nil
	}
	if reference == "" {
		reference = backends[0].Name
	}
	if !hasBackend(backends, reference) {
		return "", "", fmt.Errorf("replaytest: reference backend %q not found", reference)
	}
	return mode, reference, nil
}

func validateCases(cases []Case, backendNames map[string]struct{}) error {
	caseNames := make(map[string]struct{}, len(cases))
	for _, replayCase := range cases {
		if err := validateCase(replayCase); err != nil {
			return err
		}
		if _, exists := caseNames[replayCase.Name]; exists {
			return fmt.Errorf("replaytest: duplicate case %q", replayCase.Name)
		}
		caseNames[replayCase.Name] = struct{}{}
		if err := validateRunnerAllowedDiffs(replayCase.AllowedDiffs); err != nil {
			return fmt.Errorf("replaytest: case %q: %w", replayCase.Name, err)
		}
		if err := validateAllowedDiffBackends(replayCase.AllowedDiffs, backendNames); err != nil {
			return fmt.Errorf("replaytest: case %q: %w", replayCase.Name, err)
		}
	}
	return nil
}

func newReport(
	r Runner,
	cases []Case,
	backends []Backend,
	mode ComparisonMode,
	reference string,
) Report {
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	report := Report{
		GeneratedAt:    now().UTC(),
		ComparisonMode: mode,
		Reference:      reference,
		Backends:       make([]string, 0, len(backends)),
		TotalCases:     len(cases),
		Cases:          make([]CaseResult, 0, len(cases)),
	}
	for _, backend := range backends {
		report.Backends = append(report.Backends, backend.Name)
	}
	return report
}

type replayOutcome struct {
	snapshots   map[string]Snapshot
	unsupported map[string][]Capability
	diffs       []Diff
}

func runCase(
	ctx context.Context,
	replayCase Case,
	backends []Backend,
	mode ComparisonMode,
	reference string,
) (CaseResult, error) {
	started := time.Now()
	outcome, err := replayOnBackends(ctx, replayCase, backends, mode, reference)
	if err != nil {
		return CaseResult{}, err
	}
	diffs, referenceResult, consensus, err := compareSnapshots(replayCase, backends, mode, reference, outcome)
	if err != nil {
		return CaseResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return CaseResult{}, err
	}
	locatorEvidence, err := buildLocatorEvidence(outcome.snapshots)
	if err != nil {
		return CaseResult{}, err
	}
	diffs = append(outcome.diffs, diffs...)
	diffs = append(diffs, capabilityDiffs(replayCase.Name, backends, mode, reference, outcome.unsupported)...)
	if len(diffs) > maxReplayDiffsPerCase {
		return CaseResult{}, fmt.Errorf(
			"replaytest: case %q exceeds %d diffs",
			replayCase.Name,
			maxReplayDiffsPerCase,
		)
	}
	result := CaseResult{
		Name:            replayCase.Name,
		Duration:        time.Since(started).Milliseconds(),
		Diffs:           diffs,
		LocatorEvidence: locatorEvidence,
		Consensus:       consensus,
		Reference:       referenceResult,
	}
	blocking, _ := countDiffs(result.Diffs)
	result.Status = expectedCaseStatus(blocking, len(outcome.unsupported) > 0)
	if err := ctx.Err(); err != nil {
		return CaseResult{}, err
	}
	return result, nil
}

func replayOnBackends(
	ctx context.Context,
	replayCase Case,
	backends []Backend,
	mode ComparisonMode,
	reference string,
) (replayOutcome, error) {
	outcome := replayOutcome{
		snapshots:   make(map[string]Snapshot, len(backends)),
		unsupported: make(map[string][]Capability),
	}
	for _, backend := range backends {
		if err := ctx.Err(); err != nil {
			return outcome, err
		}
		missing := missingCapabilities(replayCase.Requires, backend.Capabilities)
		if len(missing) > 0 {
			outcome.unsupported[backend.Name] = missing
			continue
		}
		snapshot, err := Replay(ctx, replayCase, backend)
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				if errors.Is(err, contextErr) {
					return outcome, err
				}
				return outcome, errors.Join(err, contextErr)
			}
			outcome.diffs = append(outcome.diffs, executionFailureDiff(replayCase.Name, backend.Name, mode, reference, err))
			continue
		}
		outcome.snapshots[backend.Name] = snapshot
	}
	return outcome, nil
}

func executionFailureDiff(
	caseName string,
	backendName string,
	mode ComparisonMode,
	reference string,
	err error,
) Diff {
	backendA := reference
	if mode == ComparisonConsensus {
		backendA = backendName
	}
	errorMessage := boundedErrorMessage(err)
	return Diff{
		Case:        caseName,
		BackendA:    backendA,
		BackendB:    backendName,
		SessionID:   caseName,
		Path:        "/execution",
		Baseline:    "success",
		Actual:      errorMessage,
		Explanation: "backend replay failed",
		Exclusion: &ExclusionEvidence{
			Backend: backendName,
			Kind:    ExclusionExecutionFailure,
			Error:   errorMessage,
		},
	}
}

func boundedErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	truncated := len(message) > maxReplayErrorSize
	if truncated {
		message = message[:maxReplayErrorSize-len(truncatedErrorSuffix)]
	}
	message = strings.ToValidUTF8(message, "?")
	if truncated {
		message += truncatedErrorSuffix
	}
	return strings.Clone(message)
}

func compareSnapshots(
	replayCase Case,
	backends []Backend,
	mode ComparisonMode,
	reference string,
	outcome replayOutcome,
) ([]Diff, *ReferenceResult, *ConsensusResult, error) {
	if mode == ComparisonConsensus {
		diffs, consensus, err := compareByConsensus(replayCase.Name, outcome.snapshots, replayCase.AllowedDiffs)
		return diffs, nil, &consensus, err
	}
	diffs, referenceResult, err := compareReferenceSnapshots(replayCase, backends, reference, outcome)
	return diffs, &referenceResult, nil, err
}

func compareReferenceSnapshots(
	replayCase Case,
	backends []Backend,
	reference string,
	outcome replayOutcome,
) ([]Diff, ReferenceResult, error) {
	result := ReferenceResult{ComparableBackends: sortedSnapshotNames(outcome.snapshots)}
	if err := validateAllowedDiffs(replayCase.AllowedDiffs); err != nil {
		return nil, ReferenceResult{}, err
	}
	baseline, baselineOK := outcome.snapshots[reference]
	if !baselineOK {
		_, referenceUnsupported := outcome.unsupported[reference]
		if referenceUnsupported || hasSelfExecutionDiff(outcome.diffs, reference) {
			return nil, result, nil
		}
		return nil, ReferenceResult{}, fmt.Errorf(
			"replaytest: reference backend %q produced neither a snapshot nor exclusion evidence",
			reference,
		)
	}
	baselineValue, err := snapshotValue(baseline)
	if err != nil {
		return nil, ReferenceResult{}, fmt.Errorf("encode baseline snapshot: %w", err)
	}
	var diffs []Diff
	for _, backend := range backends {
		if backend.Name == reference {
			continue
		}
		actual, ok := outcome.snapshots[backend.Name]
		if !ok {
			continue
		}
		if err := validateSnapshotMetadata(replayCase.Name, baseline, actual); err != nil {
			return nil, ReferenceResult{}, err
		}
		actualValue, err := snapshotValue(actual)
		if err != nil {
			return nil, ReferenceResult{}, fmt.Errorf("encode actual snapshot: %w", err)
		}
		pairDiffs, err := compareSnapshotValues(
			replayCase.Name,
			baseline,
			actual,
			baselineValue,
			actualValue,
			replayCase.AllowedDiffs,
		)
		if err != nil {
			return nil, ReferenceResult{}, err
		}
		blocking, allowed := countDiffs(pairDiffs)
		backendA, backendB := reference, backend.Name
		if backendA > backendB {
			backendA, backendB = backendB, backendA
		}
		result.Pairs = append(result.Pairs, PairComparison{
			BackendA: backendA, BackendB: backendB,
			BlockingDiffs: blocking, AllowedDiffs: allowed,
		})
		if len(pairDiffs) > maxReplayDiffsPerCase-len(diffs) {
			return nil, ReferenceResult{}, fmt.Errorf(
				"replaytest: case %q exceeds %d diffs",
				replayCase.Name,
				maxReplayDiffsPerCase,
			)
		}
		diffs = append(diffs, pairDiffs...)
	}
	sort.Slice(result.Pairs, func(i, j int) bool {
		if result.Pairs[i].BackendA != result.Pairs[j].BackendA {
			return result.Pairs[i].BackendA < result.Pairs[j].BackendA
		}
		return result.Pairs[i].BackendB < result.Pairs[j].BackendB
	})
	return diffs, result, nil
}

func sortedSnapshotNames(snapshots map[string]Snapshot) []string {
	names := make([]string, 0, len(snapshots))
	for name := range snapshots {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
func capabilityDiffs(
	caseName string,
	backends []Backend,
	mode ComparisonMode,
	reference string,
	unsupported map[string][]Capability,
) []Diff {
	var diffs []Diff
	for _, backend := range backends {
		missing, ok := unsupported[backend.Name]
		if !ok {
			continue
		}
		backendA := reference
		if mode == ComparisonConsensus {
			backendA = backend.Name
		}
		for _, capability := range missing {
			diffs = append(diffs, Diff{
				Case:        caseName,
				BackendA:    backendA,
				BackendB:    backend.Name,
				SessionID:   caseName,
				Path:        "/capabilities/" + string(capability),
				Baseline:    true,
				Actual:      false,
				Allowed:     true,
				Explanation: "backend reports this capability as unsupported",
				Exclusion: &ExclusionEvidence{
					Backend:    backend.Name,
					Kind:       ExclusionUnsupportedCapability,
					Capability: capability,
				},
			})
		}
	}
	return diffs
}

func addCaseResult(report *Report, result CaseResult) {
	blocking, allowed := countDiffs(result.Diffs)
	report.BlockingDiffs += blocking
	report.AllowedDiffs += allowed
	switch result.Status {
	case StatusPassed:
		report.PassedCases++
	case StatusFailed:
		report.FailedCases++
	case StatusUnsupported:
		report.UnsupportedCases++
	}
	report.Cases = append(report.Cases, result)
}

func buildLocatorEvidence(snapshots map[string]Snapshot) (*LocatorEvidence, error) {
	if len(snapshots) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(snapshots))
	for name := range snapshots {
		names = append(names, name)
	}
	sort.Strings(names)
	evidence := &LocatorEvidence{
		MemoryIDs:       make(map[string][]string, len(names)),
		MemorySearchIDs: make(map[string]map[string][]string, len(names)),
	}
	for _, name := range names {
		snapshot := snapshots[name]
		ids := make([]string, len(snapshot.Memories))
		idSet := make(map[string]struct{}, len(snapshot.Memories))
		for index, memoryValue := range snapshot.Memories {
			id, ok := memoryValue["id"].(string)
			if !ok || id == "" {
				return nil, fmt.Errorf("replaytest: backend %q memory %d has no normalized id", name, index)
			}
			if err := validateUTF8String("locator memory id", id); err != nil {
				return nil, err
			}
			if _, exists := idSet[id]; exists {
				return nil, fmt.Errorf("replaytest: backend %q repeats normalized memory id %q", name, id)
			}
			ids[index] = id
			idSet[id] = struct{}{}
		}
		evidence.MemoryIDs[name] = ids
		searches := make(map[string][]string, len(snapshot.MemorySearches))
		totalResults := 0
		for query, results := range snapshot.MemorySearches {
			if query == "" {
				return nil, fmt.Errorf("replaytest: backend %q has an empty memory search name", name)
			}
			if err := validateUTF8String("locator memory search name", query); err != nil {
				return nil, err
			}
			if len(results) > maxReplayMemories || totalResults > maxReplayMemories-len(results) {
				return nil, fmt.Errorf("replaytest: backend %q memory search evidence exceeds %d results", name, maxReplayMemories)
			}
			totalResults += len(results)
			searchIDs := make([]string, len(results))
			seen := make(map[string]struct{}, len(results))
			for index, result := range results {
				id, ok := result["id"].(string)
				if !ok || id == "" {
					return nil, fmt.Errorf("replaytest: backend %q search %q result %d has no normalized id", name, query, index)
				}
				if _, exists := idSet[id]; !exists {
					return nil, fmt.Errorf("replaytest: backend %q search %q result %q is absent from memory catalog", name, query, id)
				}
				if _, exists := seen[id]; exists {
					return nil, fmt.Errorf("replaytest: backend %q search %q repeats memory id %q", name, query, id)
				}
				seen[id] = struct{}{}
				searchIDs[index] = id
			}
			searches[query] = searchIDs
		}
		evidence.MemorySearchIDs[name] = searches
	}
	return evidence, nil
}

// Replay executes one case on one isolated backend and captures only the
// snapshot domains selected by Case.Requires. It always closes non-nil services,
// including partial services returned with an Open error, and propagates context
// cancellation instead of recording it as backend behavior.
func Replay(ctx context.Context, replayCase Case, backend Backend) (snapshot Snapshot, err error) {
	if ctx == nil {
		return Snapshot{}, errors.New("replaytest: context is required")
	}
	if err := validateCase(replayCase); err != nil {
		return Snapshot{}, err
	}
	if err := validateBackend(backend); err != nil {
		return Snapshot{}, err
	}
	if missing := missingCapabilities(replayCase.Requires, backend.Capabilities); len(missing) > 0 {
		return Snapshot{}, fmt.Errorf(
			"replaytest: backend %q does not support required capabilities: %v",
			backend.Name,
			missing,
		)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	services, err := openReplayServices(ctx, replayCase.Name, backend)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() {
		err = finishReplay(ctx, backend.Name, services, err)
	}()
	return replayWithServices(ctx, replayCase, backend.Name, services)
}

func openReplayServices(
	ctx context.Context,
	caseName string,
	backend Backend,
) (*Services, error) {
	services, openErr := backend.Open(ctx, caseName)
	contextErr := ctx.Err()
	if openErr != nil {
		wrapped := fmt.Errorf("open backend %s: %w", backend.Name, openErr)
		if services != nil {
			if closeErr := services.Close(); closeErr != nil {
				wrapped = errors.Join(wrapped, fmt.Errorf("close backend %s after open failure: %w", backend.Name, closeErr))
			}
		}
		if contextErr == nil {
			contextErr = ctx.Err()
		}
		if contextErr != nil {
			wrapped = errors.Join(wrapped, contextErr)
		}
		return nil, wrapped
	}
	if contextErr != nil {
		if services == nil {
			return nil, contextErr
		}
		if closeErr := services.Close(); closeErr != nil {
			return nil, errors.Join(
				contextErr,
				fmt.Errorf("close backend %s after canceled open: %w", backend.Name, closeErr),
			)
		}
		return nil, contextErr
	}
	if services == nil {
		return nil, fmt.Errorf("open backend %s: incomplete services", backend.Name)
	}
	return services, nil
}

func finishReplay(
	ctx context.Context,
	backendName string,
	services *Services,
	runErr error,
) error {
	closeErr := services.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close backend %s: %w", backendName, closeErr)
	}
	return errors.Join(runErr, closeErr, ctx.Err())
}

//nolint:gocyclo // Replay lifecycle and probe cleanup ordering are kept together to make every exit path visible.
func replayWithServices(
	ctx context.Context,
	replayCase Case,
	backendName string,
	services *Services,
) (snapshot Snapshot, err error) {
	if isNilInterface(services.Session) {
		return Snapshot{}, fmt.Errorf("open backend %s: incomplete services", backendName)
	}
	required := capabilitySet(replayCase.Requires)
	if (required[CapabilityMemory] || required[CapabilityMemorySearch]) && isNilInterface(services.Memory) {
		return Snapshot{}, fmt.Errorf("open backend %s: memory capability has no service", backendName)
	}

	key := session.Key{AppName: "replaytest", UserID: "user-1", SessionID: replayCase.Name}
	sess, err := services.Session.CreateSession(ctx, key, cloneState(replayCase.InitialState))
	if err != nil {
		return Snapshot{}, fmt.Errorf("create session: %w", err)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return Snapshot{}, contextErr
	}
	if sess == nil {
		return Snapshot{}, errors.New("create session: backend returned nil session")
	}
	if err := validateSessionIdentity(sess, key); err != nil {
		return Snapshot{}, fmt.Errorf("create session: %w", err)
	}
	exec := execution{
		services:               services,
		key:                    key,
		session:                sess,
		required:               required,
		eventStateKeys:         collectEventStateKeys(replayCase.Steps),
		memorySearches:         make(map[string][]*memory.Entry),
		memorySearchReferences: make(map[string]map[string]string),
		eventPages:             make(map[string][]event.Event),
		expirationChecks:       make(map[string]bool),
	}
	probeNeedsCleanup := false
	if required[CapabilitySummary] {
		probeNeedsCleanup = true
		defer func() {
			if !probeNeedsCleanup {
				return
			}
			if cleanupErr := exec.cleanupSummaryIsolation(ctx); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("verify summary isolation: %w", cleanupErr))
			}
		}()
		if err := exec.createSummaryIsolationProbe(ctx); err != nil {
			return Snapshot{}, fmt.Errorf("verify summary isolation: %w", err)
		}
	}
	for _, step := range replayCase.Steps {
		if err := exec.runStep(ctx, step); err != nil {
			return Snapshot{}, fmt.Errorf("step %q (%s): %w", step.Name, step.Kind, err)
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return Snapshot{}, contextErr
		}
	}
	if required[CapabilitySummary] {
		if err := exec.verifySummaryIsolation(ctx); err != nil {
			return Snapshot{}, fmt.Errorf("verify summary isolation: %w", err)
		}
		cleanupErr := exec.cleanupSummaryIsolation(ctx)
		if cleanupErr != nil {
			return Snapshot{}, fmt.Errorf("verify summary isolation: %w", cleanupErr)
		}
		probeNeedsCleanup = false
		if contextErr := ctx.Err(); contextErr != nil {
			return Snapshot{}, contextErr
		}
	}
	snapshot, err = exec.snapshot(
		ctx,
		backendName,
		replayCase.Name,
		replayCase.EventOrder,
		buildCausalOrderPlan(replayCase.Steps),
	)
	if err != nil {
		return Snapshot{}, err
	}
	if _, err := snapshotValue(snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("encode snapshot: %w", err)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return Snapshot{}, contextErr
	}
	return snapshot, nil
}

type execution struct {
	services               *Services
	key                    session.Key
	session                *session.Session
	required               Capabilities
	eventStateKeys         map[string]struct{}
	memorySearches         map[string][]*memory.Entry
	memorySearchReferences map[string]map[string]string
	eventPages             map[string][]event.Event
	expirationChecks       map[string]bool
}

func (e *execution) runStep(ctx context.Context, step Step) error {
	if step.Recovery == RecoveryNone {
		return e.runStepOnce(ctx, step)
	}
	witness, err := e.captureRecoveryWitness(ctx, step)
	if err != nil {
		return fmt.Errorf("capture recovery witness: %w", err)
	}
	var writeErr error
	if step.FailBeforeWrite {
		writeErr = errors.New("replaytest: injected pre-commit write failure")
	} else {
		writeErr = e.runStepOnce(ctx, step)
	}
	if writeErr == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrUncertainCommit, writeErr, err)
	}
	committed, verifyErr := e.verifyRecoveredCommit(ctx, step, witness)
	if verifyErr != nil {
		return errors.Join(
			ErrUncertainCommit,
			writeErr,
			fmt.Errorf("verify uncertain commit: %w", verifyErr),
		)
	}
	if committed {
		return nil
	}
	if step.Recovery == RecoveryRetryIdempotent {
		retryStep := step
		retryStep.Recovery = RecoveryNone
		retryErr := e.runStepOnce(ctx, retryStep)
		if retryErr == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(ErrUncertainCommit, writeErr, retryErr, err)
		}
		committed, verifyErr = e.verifyRecoveredCommit(ctx, step, witness)
		if verifyErr != nil {
			return errors.Join(
				ErrUncertainCommit,
				writeErr,
				retryErr,
				fmt.Errorf("verify uncertain commit after retry: %w", verifyErr),
			)
		}
		if committed {
			return nil
		}
		writeErr = errors.Join(writeErr, fmt.Errorf("idempotent retry: %w", retryErr))
	}
	return errors.Join(
		ErrUncertainCommit,
		fmt.Errorf("step %q (%s): %w", step.Name, step.Kind, writeErr),
	)
}

func (e *execution) runStepOnce(ctx context.Context, step Step) error {
	switch step.Kind {
	case StepAppendEvent:
		return e.appendEvent(ctx, step.Event)
	case StepUpdateState:
		return e.updateState(ctx, step.State)
	case StepAddMemory:
		return e.addMemory(ctx, step.Memory)
	case StepSearchMemory:
		return e.searchMemory(ctx, step.Name, step.MemorySearch)
	case StepCreateSummary:
		return e.createSummary(ctx, step.Summary)
	case StepAppendTrack:
		return e.appendTrack(ctx, step.Track)
	case StepReloadSession:
		return e.reload(ctx)
	case StepGetEventPage:
		return e.getEventPage(ctx, step.Name, step.EventPage)
	case StepObserveSessionExpiration:
		return e.observeSessionExpiration(ctx, step.Name, step.Expiration)
	case StepConcurrent:
		return e.runConcurrent(ctx, step.Concurrent)
	default:
		return fmt.Errorf("unknown step kind %q", step.Kind)
	}
}

func (e *execution) getEventPage(ctx context.Context, name string, input *EventPageInput) error {
	page, err := e.services.Session.GetSession(
		ctx,
		e.key,
		session.WithGetSessionEventPage(input.Offset, input.Limit),
	)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if page == nil {
		return errors.New("event page: backend returned nil session")
	}
	if err := validateSessionIdentity(page, e.key); err != nil {
		return fmt.Errorf("event page: %w", err)
	}
	if len(page.Events) > input.Limit {
		return fmt.Errorf("event page returned %d events, limit is %d", len(page.Events), input.Limit)
	}
	events, err := snapshotSessionEvents(page)
	if err != nil {
		return fmt.Errorf("event page: %w", err)
	}
	e.eventPages[name] = events
	return nil
}

func (e *execution) observeSessionExpiration(
	ctx context.Context,
	name string,
	input *ExpirationInput,
) error {
	if e.services.SessionTTL <= 0 {
		return errors.New("session expiration: backend did not report a positive session TTL")
	}
	if input.Wait <= e.services.SessionTTL {
		return fmt.Errorf(
			"session expiration wait %v must exceed backend TTL %v",
			input.Wait,
			e.services.SessionTTL,
		)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	visible, err := e.services.Session.GetSession(ctx, e.key)
	if err != nil {
		return fmt.Errorf("get session before expiration: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if visible == nil {
		return errors.New("session was not visible before its reported TTL")
	}
	if err := validateSessionIdentity(visible, e.key); err != nil {
		return fmt.Errorf("get session before expiration: %w", err)
	}
	timer := time.NewTimer(input.Wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	got, err := e.services.Session.GetSession(ctx, e.key)
	if err != nil {
		return fmt.Errorf("get expired session: %w", err)
	}
	if got != nil {
		return errors.New("session remained visible after its reported TTL")
	}
	e.expirationChecks[name] = true
	return nil
}

func (e *execution) appendEvent(ctx context.Context, input *EventInput) error {
	evt, err := e.prepareEvent(input)
	if err != nil {
		return err
	}
	return e.services.Session.AppendEvent(ctx, e.session, evt)
}

func (e *execution) prepareEvent(input *EventInput) (*event.Event, error) {
	if input == nil || input.Event == nil || input.LogicalID == "" {
		return nil, errors.New("invalid event input")
	}
	evt, err := cloneReplayEvent(input.Event)
	if err != nil {
		return nil, err
	}
	evt.Timestamp = e.session.CreatedAt.Add(input.Offset)
	if evt.Response != nil {
		evt.Response.Timestamp = evt.Timestamp
	}
	if err := event.SetExtension(evt, logicalEventIDExtension, input.LogicalID); err != nil {
		return nil, fmt.Errorf("set logical event id: %w", err)
	}
	if err := validatePreparedEventSize(evt); err != nil {
		return nil, err
	}
	return evt, nil
}

func cloneReplayEvent(input *event.Event) (*event.Event, error) {
	if input == nil {
		return nil, errors.New("event is nil")
	}
	cloned := input.Clone()
	cloned.Version = input.Version
	cloned.FilterKey = input.FilterKey
	cloned.StateDelta = cloneByteMap(input.StateDelta)
	if input.StructuredOutput != nil {
		if err := validateJSONValue("event structured output", input.StructuredOutput); err != nil {
			return nil, err
		}
		value := cloneJSONValue(reflect.ValueOf(input.StructuredOutput))
		if !value.IsValid() || !value.CanInterface() {
			return nil, errors.New("event structured output cannot be cloned safely")
		}
		cloned.StructuredOutput = value.Interface()
	}
	if input.ParentMetadata != nil {
		metadata := *input.ParentMetadata
		cloned.ParentMetadata = &metadata
	}
	if input.Response == nil {
		return cloned, nil
	}
	for index := range input.Response.Choices {
		source := &input.Response.Choices[index]
		destination := &cloned.Response.Choices[index]
		destination.FinishReason = cloneStringPointer(source.FinishReason)
		var err error
		destination.Message, err = cloneReplayMessage(source.Message)
		if err != nil {
			return nil, fmt.Errorf("clone response choice %d message: %w", index, err)
		}
		destination.Delta, err = cloneReplayMessage(source.Delta)
		if err != nil {
			return nil, fmt.Errorf("clone response choice %d delta: %w", index, err)
		}
	}
	if input.Response.Error != nil {
		cloned.Response.Error.Param = cloneStringPointer(input.Response.Error.Param)
		cloned.Response.Error.Code = cloneStringPointer(input.Response.Error.Code)
	}
	return cloned, nil
}

func cloneReplayMessage(input model.Message) (model.Message, error) {
	cloned := input
	if input.ContentParts != nil {
		cloned.ContentParts = make([]model.ContentPart, len(input.ContentParts))
		for index := range input.ContentParts {
			cloned.ContentParts[index] = cloneContentPart(input.ContentParts[index])
		}
	}
	if input.ToolCalls != nil {
		cloned.ToolCalls = make([]model.ToolCall, len(input.ToolCalls))
		for index := range input.ToolCalls {
			call := input.ToolCalls[index]
			call.Index = cloneIntPointer(call.Index)
			call.Function.Arguments = cloneBytes(call.Function.Arguments)
			if call.ExtraFields != nil {
				extraFields, err := cloneToolCallExtraFields(call.ExtraFields)
				if err != nil {
					return model.Message{}, fmt.Errorf("tool call %d extra fields: %w", index, err)
				}
				call.ExtraFields = extraFields
			}
			cloned.ToolCalls[index] = call
		}
	}
	return cloned, nil
}

func cloneContentPart(input model.ContentPart) model.ContentPart {
	cloned := input
	cloned.Text = cloneStringPointer(input.Text)
	if input.Image != nil {
		image := *input.Image
		image.Data = cloneBytes(input.Image.Data)
		cloned.Image = &image
	}
	if input.Audio != nil {
		audio := *input.Audio
		audio.Data = cloneBytes(input.Audio.Data)
		cloned.Audio = &audio
	}
	if input.Video != nil {
		video := *input.Video
		video.Data = cloneBytes(input.Video.Data)
		cloned.Video = &video
	}
	if input.File != nil {
		file := *input.File
		file.Data = cloneBytes(input.File.Data)
		cloned.File = &file
	}
	if input.ContentRef != nil {
		contentRef := *input.ContentRef
		cloned.ContentRef = &contentRef
	}
	return cloned
}

func cloneStringPointer(input *string) *string {
	if input == nil {
		return nil
	}
	cloned := *input
	return &cloned
}

func cloneIntPointer(input *int) *int {
	if input == nil {
		return nil
	}
	cloned := *input
	return &cloned
}

func cloneToolCallExtraFields(input map[string]any) (map[string]any, error) {
	if err := validateJSONValue("tool call extra fields", input); err != nil {
		return nil, err
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		reflected := reflect.ValueOf(value)
		if !reflected.IsValid() {
			cloned[key] = nil
			continue
		}
		copy := cloneJSONValue(reflected)
		if copy.IsValid() {
			cloned[key] = copy.Interface()
		}
	}
	return cloned, nil
}

var (
	jsonMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	timeValueType     = reflect.TypeOf(time.Time{})
	rawMessageType    = reflect.TypeOf(json.RawMessage(nil))
)

// cloneJSONValue preserves concrete JSON value types and nil containers. The
// caller validates the graph first, so recursive JSON-visible values are acyclic.
func cloneJSONValue(input reflect.Value) reflect.Value {
	return cloneJSONValueRecursively(input, make(map[jsonReference]reflect.Value))
}

//nolint:gocyclo // Reflection kinds and alias preservation form one closed deep-copy dispatch.
func cloneJSONValueRecursively(
	input reflect.Value,
	seen map[jsonReference]reflect.Value,
) reflect.Value {
	if !input.IsValid() {
		return input
	}
	switch input.Kind() {
	case reflect.Interface:
		if input.IsNil() {
			return reflect.Zero(input.Type())
		}
		value := cloneJSONValueRecursively(input.Elem(), seen)
		cloned := reflect.New(input.Type()).Elem()
		cloned.Set(value)
		return cloned
	case reflect.Pointer:
		if input.IsNil() {
			return reflect.Zero(input.Type())
		}
		reference, _ := jsonValueReference(input)
		if cloned, ok := seen[reference]; ok {
			return cloned
		}
		cloned := reflect.New(input.Type().Elem())
		seen[reference] = cloned
		cloned.Elem().Set(cloneJSONValueRecursively(input.Elem(), seen))
		return cloned
	case reflect.Map:
		if input.IsNil() {
			return reflect.Zero(input.Type())
		}
		reference, _ := jsonValueReference(input)
		if cloned, ok := seen[reference]; ok {
			return cloned
		}
		cloned := reflect.MakeMapWithSize(input.Type(), input.Len())
		seen[reference] = cloned
		iterator := input.MapRange()
		for iterator.Next() {
			key := cloneJSONValueRecursively(iterator.Key(), seen)
			value := cloneJSONValueRecursively(iterator.Value(), seen)
			cloned.SetMapIndex(key, value)
		}
		return cloned
	case reflect.Slice:
		if input.IsNil() {
			return reflect.Zero(input.Type())
		}
		reference, _ := jsonValueReference(input)
		if cloned, ok := seen[reference]; ok {
			return cloned
		}
		cloned := reflect.MakeSlice(input.Type(), input.Len(), input.Len())
		seen[reference] = cloned
		for index := 0; index < input.Len(); index++ {
			value := cloneJSONValueRecursively(input.Index(index), seen)
			cloned.Index(index).Set(value)
		}
		return cloned
	case reflect.Array:
		cloned := reflect.New(input.Type()).Elem()
		for index := 0; index < input.Len(); index++ {
			value := cloneJSONValueRecursively(input.Index(index), seen)
			cloned.Index(index).Set(value)
		}
		return cloned
	case reflect.Struct:
		cloned := reflect.New(input.Type()).Elem()
		cloned.Set(input)
		for index := 0; index < input.NumField(); index++ {
			field := input.Type().Field(index)
			if field.PkgPath != "" || strings.Split(field.Tag.Get("json"), ",")[0] == "-" {
				continue
			}
			value := cloneJSONValueRecursively(input.Field(index), seen)
			cloned.Field(index).Set(value)
		}
		return cloned
	default:
		return input
	}
}

func implementsMarshaler(value reflect.Value, marshalerType reflect.Type) bool {
	if !value.IsValid() || !value.CanInterface() {
		return false
	}
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return false
	}
	if value.Type().Implements(marshalerType) {
		return true
	}
	return value.Kind() != reflect.Pointer && value.CanAddr() &&
		reflect.PointerTo(value.Type()).Implements(marshalerType)
}

func isCustomJSONState(value reflect.Value, mapKey bool) bool {
	if isKnownSafeMarshaler(value) {
		return false
	}
	if mapKey {
		return implementsMarshaler(value, textMarshalerType)
	}
	return implementsMarshaler(value, jsonMarshalerType) ||
		implementsMarshaler(value, textMarshalerType)
}

func isKnownSafeMarshaler(value reflect.Value) bool {
	if !value.IsValid() {
		return false
	}
	typeOf := value.Type()
	if typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}
	return typeOf == timeValueType || typeOf == rawMessageType
}

func containsCustomJSONState(
	value reflect.Value,
	visiting map[jsonReference]struct{},
) bool {
	if !value.IsValid() {
		return false
	}
	if isCustomJSONState(value, false) {
		return true
	}
	if reference, ok := jsonValueReference(value); ok {
		if _, exists := visiting[reference]; exists {
			return false
		}
		visiting[reference] = struct{}{}
		defer delete(visiting, reference)
	}
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		return !value.IsNil() && containsCustomJSONState(value.Elem(), visiting)
	case reflect.Map:
		return mapContainsCustomJSONState(value, visiting)
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice && value.IsNil() {
			return false
		}
		for index := 0; index < value.Len(); index++ {
			if containsCustomJSONState(value.Index(index), visiting) {
				return true
			}
		}
	case reflect.Struct:
		return structContainsCustomJSONState(value, visiting)
	}
	return false
}

func mapContainsCustomJSONState(
	value reflect.Value,
	visiting map[jsonReference]struct{},
) bool {
	if value.IsNil() {
		return false
	}
	iterator := value.MapRange()
	for iterator.Next() {
		if isCustomJSONState(iterator.Key(), true) ||
			containsCustomJSONState(iterator.Value(), visiting) {
			return true
		}
	}
	return false
}

func structContainsCustomJSONState(
	value reflect.Value,
	visiting map[jsonReference]struct{},
) bool {
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		if strings.Split(field.Tag.Get("json"), ",")[0] == "-" {
			continue
		}
		if field.PkgPath != "" {
			if field.Anonymous && !value.Field(index).IsZero() {
				return true
			}
			continue
		}
		if containsCustomJSONState(value.Field(index), visiting) {
			return true
		}
	}
	return false
}

func (e *execution) updateState(ctx context.Context, input *StateInput) error {
	if input == nil {
		return errors.New("state input is nil")
	}
	switch input.Scope {
	case StateScopeApp:
		return e.updateAppState(ctx, input)
	case StateScopeUser:
		return e.updateUserState(ctx, input)
	case StateScopeSession:
		if input.Clear {
			return errors.New("session state clear is not exposed by session.Service")
		}
		if len(input.DeleteKeys) > 0 {
			return errors.New("session state deletion is not exposed by session.Service")
		}
		if len(input.Values) > 0 {
			return e.services.Session.UpdateSessionState(ctx, e.key, cloneState(input.Values))
		}
	default:
		return fmt.Errorf("unknown state scope %q", input.Scope)
	}
	return nil
}

func (e *execution) updateAppState(ctx context.Context, input *StateInput) error {
	if len(input.Values) > 0 {
		if err := e.services.Session.UpdateAppState(ctx, e.key.AppName, cloneState(input.Values)); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	deleteKeys := input.DeleteKeys
	if input.Clear {
		current, err := e.services.Session.ListAppStates(ctx, e.key.AppName)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		deleteKeys, err = stateKeysForClear("app state before clear", current)
		if err != nil {
			return err
		}
	}
	for _, key := range deleteKeys {
		if err := e.services.Session.DeleteAppState(ctx, e.key.AppName, key); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (e *execution) updateUserState(ctx context.Context, input *StateInput) error {
	userKey := session.UserKey{AppName: e.key.AppName, UserID: e.key.UserID}
	if len(input.Values) > 0 {
		if err := e.services.Session.UpdateUserState(ctx, userKey, cloneState(input.Values)); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	deleteKeys := input.DeleteKeys
	if input.Clear {
		current, err := e.services.Session.ListUserStates(ctx, userKey)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		deleteKeys, err = stateKeysForClear("user state before clear", current)
		if err != nil {
			return err
		}
	}
	for _, key := range deleteKeys {
		if err := e.services.Session.DeleteUserState(ctx, userKey, key); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

func stateKeys(state session.StateMap) []string {
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func stateKeysForClear(owner string, state session.StateMap) ([]string, error) {
	if err := validateStateMapKeys(owner, state); err != nil {
		return nil, err
	}
	return stateKeys(state), nil
}

func (e *execution) addMemory(ctx context.Context, input *MemoryInput) error {
	if input == nil || input.Memory == "" {
		return errors.New("invalid memory input")
	}
	userKey := memory.UserKey{AppName: e.key.AppName, UserID: e.key.UserID}
	var opts []memory.AddOption
	if input.Metadata != nil {
		opts = append(opts, memory.WithMetadata(cloneMemoryMetadata(input.Metadata)))
	}
	return e.services.Memory.AddMemory(
		ctx,
		userKey,
		input.Memory,
		append([]string(nil), input.Topics...),
		opts...,
	)
}

//nolint:gocyclo // Search execution validates one point-in-time identity, ranking, and resource contract.
func (e *execution) searchMemory(
	ctx context.Context,
	name string,
	input *MemorySearchInput,
) error {
	if input == nil || strings.TrimSpace(input.Query) == "" {
		return errors.New("invalid memory search input")
	}
	if _, exists := e.memorySearches[name]; exists {
		return fmt.Errorf("memory search name %q is repeated", name)
	}
	options := input.Options
	options.Query = input.Query
	if input.Options.TimeAfter != nil {
		value := *input.Options.TimeAfter
		options.TimeAfter = &value
	}
	if input.Options.TimeBefore != nil {
		value := *input.Options.TimeBefore
		options.TimeBefore = &value
	}
	userKey := memory.UserKey{AppName: e.key.AppName, UserID: e.key.UserID}
	results, err := e.services.Memory.SearchMemories(
		ctx,
		userKey,
		input.Query,
		memory.WithSearchOptions(options),
	)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(results) > maxReplayMemories {
		return fmt.Errorf("memory search %q returned %d results, limit is %d", name, len(results), maxReplayMemories)
	}
	if options.MaxResults > 0 && len(results) > options.MaxResults {
		return fmt.Errorf(
			"memory search %q returned %d results, requested limit is %d",
			name,
			len(results),
			options.MaxResults,
		)
	}
	if err := validateMemoryOwnership(results, userKey, fmt.Sprintf("memory search %q", name)); err != nil {
		return err
	}
	catalog, err := e.services.Memory.ReadMemories(ctx, userKey, 0)
	if err != nil {
		return fmt.Errorf("read memory catalog after search: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateMemoryOwnership(catalog, userKey, "memory catalog"); err != nil {
		return err
	}
	references := make(map[string]string, len(catalog))
	for index, entry := range catalog {
		fingerprint, err := memoryEntryFingerprint(entry, fmt.Sprintf("memory catalog %d", index))
		if err != nil {
			return err
		}
		if _, exists := references[entry.ID]; exists {
			return fmt.Errorf("memory catalog repeats id %q", entry.ID)
		}
		references[entry.ID] = fingerprint
	}
	seen := make(map[string]struct{}, len(results))
	for index, entry := range results {
		if entry == nil {
			return fmt.Errorf("memory search %q result %d is nil", name, index)
		}
		if _, exists := seen[entry.ID]; exists {
			return fmt.Errorf("memory search %q repeats id %q", name, entry.ID)
		}
		seen[entry.ID] = struct{}{}
	}
	e.memorySearches[name] = cloneMemoryEntries(results)
	e.memorySearchReferences[name] = references
	return nil
}

func (e *execution) createSummary(ctx context.Context, input *SummaryInput) error {
	if input == nil {
		return errors.New("summary input is nil")
	}
	if err := e.services.Session.CreateSessionSummary(ctx, e.session, input.FilterKey, input.Force); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.reload(ctx)
}

func (e *execution) appendTrack(ctx context.Context, input *TrackInput) error {
	if input == nil || input.Event == nil {
		return errors.New("track input is nil")
	}
	trackService, ok := e.services.Session.(session.TrackService)
	if !ok {
		return errors.New("track capability advertised but service does not implement session.TrackService")
	}
	copyEvent := prepareTrackEvent(e.session, input)
	return trackService.AppendTrackEvent(ctx, e.session, copyEvent)
}

func prepareTrackEvent(sess *session.Session, input *TrackInput) *session.TrackEvent {
	copyEvent := *input.Event
	copyEvent.Payload = cloneBytes(input.Event.Payload)
	copyEvent.Timestamp = sess.CreatedAt.Add(input.Offset)
	return &copyEvent
}

func validateSessionIdentity(sess *session.Session, key session.Key) error {
	if sess.AppName != key.AppName || sess.UserID != key.UserID || sess.ID != key.SessionID {
		return fmt.Errorf(
			"backend returned session %q/%q/%q for %q/%q/%q",
			sess.AppName,
			sess.UserID,
			sess.ID,
			key.AppName,
			key.UserID,
			key.SessionID,
		)
	}
	return nil
}

func (e *execution) reload(ctx context.Context) error {
	sess, err := e.services.Session.GetSession(ctx, e.key)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if sess == nil {
		return errors.New("get session: backend returned nil session")
	}
	if err := validateSessionIdentity(sess, e.key); err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	e.session = sess
	return nil
}

func (e *execution) summaryIsolationKey() session.Key {
	probeKey := e.key
	probeKey.SessionID += summaryIsolationSessionSuffix
	return probeKey
}

func (e *execution) createSummaryIsolationProbe(ctx context.Context) error {
	probeKey := e.summaryIsolationKey()
	probe, err := e.services.Session.CreateSession(ctx, probeKey, nil)
	if err != nil {
		return fmt.Errorf("create probe session: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if probe == nil {
		return errors.New("create probe session: backend returned nil session")
	}
	if err := validateSessionIdentity(probe, probeKey); err != nil {
		return fmt.Errorf("create probe session: %w", err)
	}
	return nil
}

func (e *execution) verifySummaryIsolation(ctx context.Context) error {
	probeKey := e.summaryIsolationKey()
	probe, err := e.services.Session.GetSession(ctx, probeKey)
	if err != nil {
		return fmt.Errorf("get probe session: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if probe == nil {
		return errors.New("get probe session: backend returned nil session")
	}
	if err := validateSessionIdentity(probe, probeKey); err != nil {
		return fmt.Errorf("get probe session: %w", err)
	}
	probe.SummariesMu.RLock()
	summaryCount := len(probe.Summaries)
	probe.SummariesMu.RUnlock()
	if summaryCount != 0 {
		return fmt.Errorf("fresh probe session contains %d summaries", summaryCount)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (e *execution) cleanupSummaryIsolation(ctx context.Context) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), summaryProbeCleanupTimeout)
	defer cancel()
	if err := e.services.Session.DeleteSession(cleanupCtx, e.summaryIsolationKey()); err != nil {
		return fmt.Errorf("delete probe session: %w", err)
	}
	return nil
}

func (e *execution) runConcurrent(ctx context.Context, branches [][]Step) error {
	if len(branches) == 0 {
		return errors.New("concurrent step has no branches")
	}
	if err := validateConcurrentSession(e.session); err != nil {
		return err
	}
	branchCtx, cancelBranches := context.WithCancel(ctx)
	defer cancelBranches()
	start := make(chan struct{})
	errs := make([]error, len(branches))
	var wg sync.WaitGroup
	for i, branch := range branches {
		i, branch := i, append([]Step(nil), branch...)
		branchExecution := &execution{
			services: e.services,
			key:      e.key,
			session:  e.session.Clone(),
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-branchCtx.Done():
				if ctx.Err() != nil {
					errs[i] = branchCtx.Err()
				}
				return
			case <-start:
			}
			for _, nested := range branch {
				if err := branchExecution.runStep(branchCtx, nested); err != nil {
					err = stripSiblingBranchCancellation(ctx, branchCtx, err)
					if err != nil {
						errs[i] = fmt.Errorf("nested step %q: %w", nested.Name, err)
					}
					cancelBranches()
					return
				}
				if err := branchCtx.Err(); err != nil {
					if ctx.Err() != nil {
						errs[i] = err
					}
					cancelBranches()
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.reload(ctx)
}

func stripSiblingBranchCancellation(parentCtx, branchCtx context.Context, err error) error {
	if err == nil || parentCtx.Err() != nil || !errors.Is(branchCtx.Err(), context.Canceled) {
		return err
	}
	stripped, _ := stripCancellationLeaves(err, 0)
	return stripped
}

func stripCancellationLeaves(err error, depth int) (error, bool) {
	if err == nil {
		return nil, false
	}
	if err == context.Canceled {
		return nil, true
	}
	if depth >= 100 {
		return err, false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		kept := make([]error, 0, len(children))
		changed := false
		for _, child := range children {
			stripped, childChanged := stripCancellationLeaves(child, depth+1)
			changed = changed || childChanged
			if stripped != nil {
				kept = append(kept, stripped)
			}
		}
		if !changed {
			return err, false
		}
		return errors.Join(kept...), true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		stripped, changed := stripCancellationLeaves(wrapped.Unwrap(), depth+1)
		if !changed {
			return err, false
		}
		return stripped, true
	}
	return err, false
}

func validateConcurrentSession(sess *session.Session) error {
	if sess == nil {
		return session.ErrNilSession
	}
	sess.TracksMu.RLock()
	defer sess.TracksMu.RUnlock()
	for track, history := range sess.Tracks {
		if history == nil {
			return fmt.Errorf("session track %q has nil history", track)
		}
	}
	return nil
}

func (e *execution) snapshot(
	ctx context.Context,
	backendName string,
	caseName string,
	eventOrder EventOrderMode,
	eventOrderPlan *causalOrderPlan,
) (Snapshot, error) {
	var sess *session.Session
	var err error
	if len(e.expirationChecks) > 0 {
		// Expiration cases intentionally end with an absent persisted session.
		// Validation restricts them to this sole observation, so the initially
		// created empty session supplies only common snapshot metadata.
		sess = e.session
	} else {
		sess, err = e.services.Session.GetSession(ctx, e.key)
		if err != nil {
			return Snapshot{}, fmt.Errorf("get session: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if sess == nil {
			return Snapshot{}, errors.New("get session: backend returned nil session")
		}
	}
	if err := validateSessionIdentity(sess, e.key); err != nil {
		return Snapshot{}, fmt.Errorf("get session: %w", err)
	}
	var appState session.StateMap
	if e.required[CapabilityAppState] {
		appState, err = e.services.Session.ListAppStates(ctx, e.key.AppName)
		if err != nil {
			return Snapshot{}, fmt.Errorf("list app state: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
	}
	userKey := session.UserKey{AppName: e.key.AppName, UserID: e.key.UserID}
	var userState session.StateMap
	if e.required[CapabilityUserState] {
		userState, err = e.services.Session.ListUserStates(ctx, userKey)
		if err != nil {
			return Snapshot{}, fmt.Errorf("list user state: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
	}
	memoryKey := memory.UserKey{
		AppName: e.key.AppName,
		UserID:  e.key.UserID,
	}
	var memories []*memory.Entry
	if e.required[CapabilityMemory] {
		memories, err = e.services.Memory.ReadMemories(ctx, memoryKey, 0)
		if err != nil {
			return Snapshot{}, fmt.Errorf("read memories: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if err := validateMemoryOwnership(memories, memoryKey, "memory catalog"); err != nil {
			return Snapshot{}, err
		}
	}
	if err := validateMemorySearchReferences(memories, e.memorySearches, memoryKey); err != nil {
		return Snapshot{}, err
	}
	return normalizeSnapshot(
		backendName,
		caseName,
		eventOrder,
		eventOrderPlan,
		e.required,
		e.eventStateKeys,
		sess,
		appState,
		userState,
		memories,
		e.memorySearches,
		snapshotObservations{
			memorySearchReferences: e.memorySearchReferences,
			eventPages:             e.eventPages,
			expirationChecks:       e.expirationChecks,
		},
	)
}

func validateMemoryOwnership(
	entries []*memory.Entry,
	key memory.UserKey,
	owner string,
) error {
	for index, entry := range entries {
		if entry == nil {
			continue
		}
		if entry.AppName != key.AppName || entry.UserID != key.UserID {
			return fmt.Errorf(
				"%s %d belongs to %q/%q, want %q/%q",
				owner,
				index,
				entry.AppName,
				entry.UserID,
				key.AppName,
				key.UserID,
			)
		}
	}
	return nil
}

func validateMemorySearchReferences(
	catalog []*memory.Entry,
	searches map[string][]*memory.Entry,
	key memory.UserKey,
) error {
	if len(searches) == 0 {
		return nil
	}
	catalogEntries := make(map[string]struct{}, len(catalog))
	for index, entry := range catalog {
		if _, err := memoryEntryFingerprint(entry, fmt.Sprintf("memory catalog %d", index)); err != nil {
			return err
		}
		if _, exists := catalogEntries[entry.ID]; exists {
			return fmt.Errorf("duplicate memory id %q", entry.ID)
		}
		catalogEntries[entry.ID] = struct{}{}
	}
	names := make([]string, 0, len(searches))
	for name := range searches {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		results := searches[name]
		owner := fmt.Sprintf("memory search %q", name)
		if err := validateMemoryOwnership(results, key, owner); err != nil {
			return err
		}
		for _, entry := range results {
			_, exists := catalogEntries[entry.ID]
			if !exists {
				return fmt.Errorf("%s returned unknown id %q", owner, entry.ID)
			}
		}
	}
	return nil
}

func memoryEntryFingerprint(entry *memory.Entry, owner string) (string, error) {
	value, err := normalizeMemoryEntry(entry, owner)
	if err != nil {
		return "", err
	}
	delete(value, "id")
	delete(value, "score")
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal %s: %w", owner, err)
	}
	return string(raw), nil
}

func cloneState(input session.StateMap) session.StateMap {
	if input == nil {
		return nil
	}
	out := make(session.StateMap, len(input))
	for key, value := range input {
		out[key] = cloneBytes(value)
	}
	return out
}

func cloneByteMap(input map[string][]byte) map[string][]byte {
	if input == nil {
		return nil
	}
	out := make(map[string][]byte, len(input))
	for key, value := range input {
		out[key] = cloneBytes(value)
	}
	return out
}

func cloneBytes(input []byte) []byte {
	if input == nil {
		return nil
	}
	out := make([]byte, len(input))
	copy(out, input)
	return out
}

func cloneMemoryMetadata(input *memory.Metadata) *memory.Metadata {
	if input == nil {
		return nil
	}
	out := *input
	out.Participants = append([]string(nil), input.Participants...)
	if input.EventTime != nil {
		eventTime := *input.EventTime
		out.EventTime = &eventTime
	}
	return &out
}

func cloneMemoryEntries(input []*memory.Entry) []*memory.Entry {
	output := make([]*memory.Entry, len(input))
	for index, entry := range input {
		if entry == nil {
			continue
		}
		copyEntry := *entry
		if entry.Memory != nil {
			copyMemory := *entry.Memory
			copyMemory.Topics = append([]string(nil), entry.Memory.Topics...)
			copyMemory.Participants = append([]string(nil), entry.Memory.Participants...)
			if entry.Memory.LastUpdated != nil {
				value := *entry.Memory.LastUpdated
				copyMemory.LastUpdated = &value
			}
			if entry.Memory.EventTime != nil {
				value := *entry.Memory.EventTime
				copyMemory.EventTime = &value
			}
			copyEntry.Memory = &copyMemory
		}
		output[index] = &copyEntry
	}
	return output
}

func validateBackends(backends []Backend) error {
	if len(backends) < 2 {
		return errors.New("replaytest: at least two backends are required")
	}
	if len(backends) > maxReplayBackends {
		return fmt.Errorf("replaytest: %d backends exceed limit %d", len(backends), maxReplayBackends)
	}
	seen := make(map[string]struct{}, len(backends))
	for _, backend := range backends {
		if err := validateBackend(backend); err != nil {
			return err
		}
		if _, ok := seen[backend.Name]; ok {
			return fmt.Errorf("replaytest: duplicate backend %q", backend.Name)
		}
		seen[backend.Name] = struct{}{}
	}
	return nil
}

func validateBackend(backend Backend) error {
	if backend.Name == "" || backend.Open == nil {
		return errors.New("replaytest: backend name and factory are required")
	}
	if backend.Name == "*" {
		return errors.New("replaytest: backend name \"*\" is reserved")
	}
	if err := validateBoundedUTF8String("backend name", backend.Name, maxReplayIdentifierSize); err != nil {
		return fmt.Errorf("replaytest: %w", err)
	}
	if len(backend.Capabilities) > maxReplayCapabilityCount {
		return fmt.Errorf("replaytest: backend %q has %d capabilities, limit is %d", backend.Name, len(backend.Capabilities), maxReplayCapabilityCount)
	}
	capabilities := make([]Capability, 0, len(backend.Capabilities))
	for capability := range backend.Capabilities {
		capabilities = append(capabilities, capability)
	}
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i] < capabilities[j] })
	for _, capability := range capabilities {
		if err := validateBoundedUTF8String("backend capability", string(capability), maxReplayIdentifierSize); err != nil {
			return fmt.Errorf("replaytest: backend %q: %w", backend.Name, err)
		}
		if !isKnownCapability(capability) {
			return fmt.Errorf(
				"replaytest: backend %q declares unknown capability %q",
				backend.Name,
				capability,
			)
		}
	}
	return nil
}

//nolint:gocyclo // Cross-step case invariants must be evaluated together before any backend is opened.
func validateCase(replayCase Case) error {
	if replayCase.Name == "" {
		return errors.New("replaytest: case name is required")
	}
	if err := validateBoundedUTF8String("case name", replayCase.Name, maxReplayIdentifierSize); err != nil {
		return fmt.Errorf("replaytest: %w", err)
	}
	if err := validateBoundedUTF8String("case description", replayCase.Description, maxReplayExplanationSize); err != nil {
		return fmt.Errorf("replaytest: case %q: %w", replayCase.Name, err)
	}
	if err := validateBoundedUTF8String("event order", string(replayCase.EventOrder), maxReplayIdentifierSize); err != nil {
		return fmt.Errorf("replaytest: case %q: %w", replayCase.Name, err)
	}
	if len(replayCase.Requires) > maxReplayCapabilityCount {
		return fmt.Errorf("replaytest: case %q has %d required capabilities, limit is %d", replayCase.Name, len(replayCase.Requires), maxReplayCapabilityCount)
	}
	if len(replayCase.Steps) == 0 {
		return fmt.Errorf("replaytest: case %q has no steps", replayCase.Name)
	}
	steps, branches := replayStepCounts(replayCase.Steps)
	if steps > maxReplaySteps {
		return fmt.Errorf("replaytest: case %q has %d steps, limit is %d", replayCase.Name, steps, maxReplaySteps)
	}
	if branches > maxReplayBranches {
		return fmt.Errorf("replaytest: case %q has %d branches, limit is %d", replayCase.Name, branches, maxReplayBranches)
	}
	switch replayCase.EventOrder {
	case "", EventOrderGlobal, EventOrderCausal:
	default:
		return fmt.Errorf("replaytest: case %q has unknown event order %q", replayCase.Name, replayCase.EventOrder)
	}
	if err := validateStateKeys("initial state", StateScopeSession, replayCase.InitialState, nil); err != nil {
		return fmt.Errorf("replaytest: case %q: %w", replayCase.Name, err)
	}
	for _, step := range replayCase.Steps {
		if err := validateStep(step); err != nil {
			return fmt.Errorf("replaytest: case %q: %w", replayCase.Name, err)
		}
	}
	if err := validateLogicalEventIDs(replayCase.Steps, make(map[string]string)); err != nil {
		return fmt.Errorf("replaytest: case %q: %w", replayCase.Name, err)
	}
	if err := validateMemorySearchNames(replayCase.Steps, make(map[string]struct{})); err != nil {
		return fmt.Errorf("replaytest: case %q: %w", replayCase.Name, err)
	}
	if err := validateEventPageNames(replayCase.Steps, make(map[string]struct{})); err != nil {
		return fmt.Errorf("replaytest: case %q: %w", replayCase.Name, err)
	}
	if containsStepKind(replayCase.Steps, StepObserveSessionExpiration) &&
		(len(replayCase.Steps) != 1 || replayCase.Steps[0].Kind != StepObserveSessionExpiration ||
			len(replayCase.InitialState) != 0) {
		return fmt.Errorf(
			"replaytest: case %q: session expiration must be the sole step with empty initial state",
			replayCase.Name,
		)
	}
	if containsConcurrentStep(replayCase.Steps) {
		if containsConcurrentStepKind(replayCase.Steps, StepAppendEvent) &&
			replayCase.EventOrder != EventOrderCausal {
			return fmt.Errorf("replaytest: case %q: concurrent event steps require causal event ordering", replayCase.Name)
		}
		if containsConcurrentStepKind(replayCase.Steps, StepAppendEvent) &&
			containsStepKind(replayCase.Steps, StepGetEventPage) {
			return fmt.Errorf(
				"replaytest: case %q: event pagination cannot follow concurrent event writes",
				replayCase.Name,
			)
		}
		if containsConcurrentStepKind(replayCase.Steps, StepAppendEvent) &&
			containsStepKind(replayCase.Steps, StepCreateSummary) {
			return fmt.Errorf("replaytest: case %q: concurrent cases cannot contain summary steps", replayCase.Name)
		}
		if err := validateConcurrentHistory(replayCase.Steps); err != nil {
			return fmt.Errorf("replaytest: case %q: %w", replayCase.Name, err)
		}
	}
	return validateCaseCapabilities(replayCase)
}

func replayStepCounts(steps []Step) (count, branches int) {
	work := make([][]Step, 0, 1)
	work = append(work, steps)
	for len(work) > 0 {
		current := work[len(work)-1]
		work = work[:len(work)-1]
		for _, step := range current {
			count++
			// Once either limit is exceeded, the caller only needs a value
			// greater than the limit. Stop traversing immediately so a hostile
			// case cannot force an unbounded walk before validation rejects it.
			if count > maxReplaySteps {
				return count, branches
			}
			if step.Kind != StepConcurrent {
				continue
			}
			branches += len(step.Concurrent)
			if branches > maxReplayBranches {
				return count, branches
			}
			work = append(work, step.Concurrent...)
		}
	}
	return count, branches
}

func containsConcurrentStep(steps []Step) bool {
	for _, step := range steps {
		if step.Kind == StepConcurrent {
			return true
		}
	}
	return false
}

func containsStepKind(steps []Step, kind StepKind) bool {
	for _, step := range steps {
		if step.Kind == kind {
			return true
		}
		for _, branch := range step.Concurrent {
			if containsStepKind(branch, kind) {
				return true
			}
		}
	}
	return false
}

func containsConcurrentStepKind(steps []Step, kind StepKind) bool {
	for _, step := range steps {
		if step.Kind != StepConcurrent {
			continue
		}
		for _, branch := range step.Concurrent {
			if containsStepKind(branch, kind) {
				return true
			}
		}
	}
	return false
}

func validateConcurrentHistory(steps []Step) error {
	hasUserAnchor := false
	for _, step := range steps {
		switch step.Kind {
		case StepAppendEvent:
			if replayEventIsPersistable(step.Event.Event) && step.Event.Event.IsUserMessage() {
				hasUserAnchor = true
			}
		case StepConcurrent:
			if containsConcurrentBranchKind(step.Concurrent, StepAppendEvent) && !hasUserAnchor {
				return fmt.Errorf("step %q requires a preceding persisted user event", step.Name)
			}
		}
	}
	return nil
}

func containsConcurrentBranchKind(branches [][]Step, kind StepKind) bool {
	for _, branch := range branches {
		if containsStepKind(branch, kind) {
			return true
		}
	}
	return false
}

func validateLogicalEventIDs(steps []Step, owners map[string]string) error {
	for _, step := range steps {
		if step.Kind == StepAppendEvent {
			logicalID := step.Event.LogicalID
			if owner, exists := owners[logicalID]; exists {
				return fmt.Errorf(
					"logical event id %q is reused by steps %q and %q",
					logicalID,
					owner,
					step.Name,
				)
			}
			owners[logicalID] = step.Name
		}
		if step.Kind != StepConcurrent {
			continue
		}
		for _, branch := range step.Concurrent {
			if err := validateLogicalEventIDs(branch, owners); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateMemorySearchNames(steps []Step, names map[string]struct{}) error {
	for _, step := range steps {
		if step.Kind == StepSearchMemory {
			if _, exists := names[step.Name]; exists {
				return fmt.Errorf("memory search name %q is repeated", step.Name)
			}
			names[step.Name] = struct{}{}
		}
		for _, branch := range step.Concurrent {
			if err := validateMemorySearchNames(branch, names); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateEventPageNames(steps []Step, names map[string]struct{}) error {
	for _, step := range steps {
		if step.Kind == StepGetEventPage {
			if _, exists := names[step.Name]; exists {
				return fmt.Errorf("event page name %q is repeated", step.Name)
			}
			names[step.Name] = struct{}{}
		}
		for _, branch := range step.Concurrent {
			if err := validateEventPageNames(branch, names); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateCaseCapabilities(replayCase Case) error {
	declared := make(Capabilities, len(replayCase.Requires))
	for _, capability := range replayCase.Requires {
		if err := validateBoundedUTF8String("required capability", string(capability), maxReplayIdentifierSize); err != nil {
			return fmt.Errorf("replaytest: case %q: %w", replayCase.Name, err)
		}
		if !isKnownCapability(capability) {
			return fmt.Errorf("replaytest: case %q requires unknown capability %q", replayCase.Name, capability)
		}
		if declared[capability] {
			return fmt.Errorf("replaytest: case %q repeats capability %q", replayCase.Name, capability)
		}
		declared[capability] = true
	}
	used := Capabilities{CapabilitySession: true}
	if len(replayCase.InitialState) > 0 {
		used[CapabilitySessionState] = true
	}
	for _, step := range replayCase.Steps {
		collectStepCapabilities(step, used)
	}
	usedCapabilities := make([]Capability, 0, len(used))
	for capability := range used {
		usedCapabilities = append(usedCapabilities, capability)
	}
	sort.Slice(usedCapabilities, func(i, j int) bool { return usedCapabilities[i] < usedCapabilities[j] })
	for _, capability := range usedCapabilities {
		if !declared[capability] {
			return fmt.Errorf("replaytest: case %q uses undeclared capability %q", replayCase.Name, capability)
		}
	}
	return nil
}

func collectStepCapabilities(step Step, capabilities Capabilities) {
	switch step.Kind {
	case StepAppendEvent:
		if len(step.Event.Event.StateDelta) > 0 {
			capabilities[CapabilitySessionState] = true
		}
		for key := range step.Event.Event.StateDelta {
			switch {
			case strings.HasPrefix(key, session.StateAppPrefix):
				capabilities[CapabilityAppState] = true
			case strings.HasPrefix(key, session.StateUserPrefix):
				capabilities[CapabilityUserState] = true
			default:
				capabilities[CapabilitySessionState] = true
			}
		}
	case StepUpdateState:
		switch step.State.Scope {
		case StateScopeApp:
			capabilities[CapabilityAppState] = true
		case StateScopeUser:
			capabilities[CapabilityUserState] = true
		case StateScopeSession:
			capabilities[CapabilitySessionState] = true
		}
	case StepAddMemory:
		capabilities[CapabilityMemory] = true
	case StepSearchMemory:
		capabilities[CapabilityMemory] = true
		capabilities[CapabilityMemorySearch] = true
	case StepCreateSummary:
		capabilities[CapabilitySummary] = true
	case StepAppendTrack:
		capabilities[CapabilityTrack] = true
	case StepGetEventPage:
		capabilities[CapabilityEventPage] = true
	case StepObserveSessionExpiration:
		capabilities[CapabilitySessionTTL] = true
	case StepConcurrent:
		collectConcurrentCapabilities(step.Concurrent, capabilities)
	}
}

func collectConcurrentCapabilities(branches [][]Step, capabilities Capabilities) {
	capabilities[CapabilityConcurrent] = true
	for _, branch := range branches {
		for _, nested := range branch {
			if capability, ok := concurrentDomainCapability(nested.Kind); ok {
				capabilities[capability] = true
			}
			collectStepCapabilities(nested, capabilities)
		}
	}
}

func concurrentDomainCapability(kind StepKind) (Capability, bool) {
	switch kind {
	case StepUpdateState:
		return CapabilityConcurrentState, true
	case StepAddMemory:
		return CapabilityConcurrentMemory, true
	case StepCreateSummary:
		return CapabilityConcurrentSummary, true
	case StepAppendTrack:
		return CapabilityConcurrentTrack, true
	default:
		return "", false
	}
}

func collectEventStateKeys(steps []Step) map[string]struct{} {
	keys := make(map[string]struct{})
	for _, step := range steps {
		if step.Kind == StepAppendEvent {
			for key := range step.Event.Event.StateDelta {
				keys[key] = struct{}{}
			}
		}
		for _, branch := range step.Concurrent {
			for key := range collectEventStateKeys(branch) {
				keys[key] = struct{}{}
			}
		}
	}
	return keys
}

func validateStep(step Step) error {
	if step.Name == "" {
		return errors.New("unnamed step")
	}
	if err := validateBoundedUTF8String("step name", step.Name, maxReplayIdentifierSize); err != nil {
		return err
	}
	if err := validateBoundedUTF8String("step kind", string(step.Kind), maxReplayIdentifierSize); err != nil {
		return err
	}
	if err := validateBoundedUTF8String("recovery mode", string(step.Recovery), maxReplayIdentifierSize); err != nil {
		return err
	}
	if err := validateRecoveryMode(step); err != nil {
		return err
	}
	payloads := stepPayloadCount(step)
	wantPayloads := 1
	if step.Kind == StepReloadSession {
		wantPayloads = 0
	}
	if payloads != wantPayloads {
		return fmt.Errorf("step %q has %d payloads, want %d", step.Name, payloads, wantPayloads)
	}
	return validateStepKind(step)
}

func validateRecoveryMode(step Step) error {
	if step.FailBeforeWrite && step.Recovery == RecoveryNone {
		return fmt.Errorf("step %q pre-commit failure requires recovery", step.Name)
	}
	switch step.Recovery {
	case RecoveryNone:
		return nil
	case RecoveryVerify:
		switch step.Kind {
		case StepAppendEvent:
			if step.Event == nil || step.Event.Event == nil {
				return nil
			}
			if !replayEventIsPersistable(step.Event.Event) || len(step.Event.Event.StateDelta) > 0 {
				return fmt.Errorf(
					"step %q can verify recovery only for persisted events without state delta",
					step.Name,
				)
			}
			return nil
		case StepUpdateState, StepAddMemory, StepAppendTrack:
			return nil
		case StepCreateSummary:
			// Summary text is generated by a backend-owned summarizer. The
			// replay runner cannot reconstruct its expected bytes, so observing
			// a changed summary is not proof that this request committed the
			// requested content. Reject the ambiguous recovery mode explicitly.
			return fmt.Errorf("step %q cannot verify summary recovery without an expected summary", step.Name)
		default:
			return fmt.Errorf("step %q cannot verify recovery for kind %q", step.Name, step.Kind)
		}
	case RecoveryRetryIdempotent:
		if step.Kind == StepUpdateState || step.Kind == StepAddMemory {
			return nil
		}
		return fmt.Errorf("step %q cannot idempotently retry kind %q", step.Name, step.Kind)
	default:
		return fmt.Errorf("step %q has unknown recovery mode %q", step.Name, step.Recovery)
	}
}

func stepPayloadCount(step Step) int {
	count := 0
	for _, populated := range []bool{
		step.Event != nil,
		step.State != nil,
		step.Memory != nil,
		step.MemorySearch != nil,
		step.Summary != nil,
		step.Track != nil,
		step.EventPage != nil,
		step.Expiration != nil,
		len(step.Concurrent) > 0,
	} {
		if populated {
			count++
		}
	}
	return count
}

//nolint:gocyclo // Step kind dispatch is the authoritative payload schema for the closed operation set.
func validateStepKind(step Step) error {
	switch step.Kind {
	case StepAppendEvent:
		if step.Event == nil {
			return fmt.Errorf("step %q kind %q requires event payload", step.Name, step.Kind)
		}
		return validateEventStep(step)
	case StepUpdateState:
		if step.State == nil {
			return fmt.Errorf("step %q kind %q requires state payload", step.Name, step.Kind)
		}
		return validateStateStep(step)
	case StepAddMemory:
		if step.Memory == nil {
			return fmt.Errorf("step %q kind %q requires memory payload", step.Name, step.Kind)
		}
		if step.Memory.Memory == "" {
			return fmt.Errorf("step %q has invalid memory input", step.Name)
		}
		if err := validateMemoryInputStrings(step.Memory); err != nil {
			return fmt.Errorf("step %q: %w", step.Name, err)
		}
	case StepSearchMemory:
		if step.MemorySearch == nil {
			return fmt.Errorf("step %q kind %q requires memory search payload", step.Name, step.Kind)
		}
		if err := validateMemorySearchInput(step.MemorySearch); err != nil {
			return fmt.Errorf("step %q: %w", step.Name, err)
		}
	case StepCreateSummary:
		if step.Summary == nil {
			return fmt.Errorf("step %q kind %q requires summary payload", step.Name, step.Kind)
		}
		if len(step.Summary.FilterKey) > maxReplaySummaryKeySize {
			return fmt.Errorf("step %q summary filter key exceeds %d bytes", step.Name, maxReplaySummaryKeySize)
		}
		if err := validateUTF8String("summary filter key", step.Summary.FilterKey); err != nil {
			return fmt.Errorf("step %q: %w", step.Name, err)
		}
	case StepAppendTrack:
		return validateTrackStep(step)
	case StepReloadSession:
		return nil
	case StepGetEventPage:
		if step.EventPage == nil {
			return fmt.Errorf("step %q kind %q requires event page payload", step.Name, step.Kind)
		}
		if step.EventPage.Offset < 0 || step.EventPage.Offset > maxReplayEvents ||
			step.EventPage.Limit <= 0 || step.EventPage.Limit > maxReplayEvents {
			return fmt.Errorf(
				"step %q event page requires offset within [0, %d] and limit within [1, %d]",
				step.Name,
				maxReplayEvents,
				maxReplayEvents,
			)
		}
		return nil
	case StepObserveSessionExpiration:
		if step.Expiration == nil {
			return fmt.Errorf("step %q kind %q requires expiration payload", step.Name, step.Kind)
		}
		if step.Expiration.Wait <= 0 || step.Expiration.Wait > maxReplayExpirationWait {
			return fmt.Errorf(
				"step %q expiration wait must be within (0, %v]",
				step.Name,
				maxReplayExpirationWait,
			)
		}
		return nil
	case StepConcurrent:
		if len(step.Concurrent) == 0 {
			return fmt.Errorf("step %q kind %q requires concurrent payload", step.Name, step.Kind)
		}
		return validateConcurrentStep(step)
	default:
		return fmt.Errorf("step %q has unknown kind %q", step.Name, step.Kind)
	}
	return nil
}

func validateMemoryInputStrings(input *MemoryInput) error {
	if len(input.Memory) > maxReplayMemorySize {
		return fmt.Errorf("memory content exceeds %d bytes", maxReplayMemorySize)
	}
	totalBytes := len(input.Memory)
	if err := validateUTF8String("memory content", input.Memory); err != nil {
		return err
	}
	if len(input.Topics) > maxReplayMemories {
		return fmt.Errorf("memory topics contain more than %d entries", maxReplayMemories)
	}
	for index, topic := range input.Topics {
		if len(topic) > maxReplayMemorySize {
			return fmt.Errorf("memory topic %d exceeds %d bytes", index, maxReplayMemorySize)
		}
		if totalBytes > maxReplayMemorySize-len(topic) {
			return fmt.Errorf("memory input metadata exceeds %d bytes", maxReplayMemorySize)
		}
		totalBytes += len(topic)
		if err := validateUTF8String(fmt.Sprintf("memory topic %d", index), topic); err != nil {
			return err
		}
	}
	if input.Metadata == nil {
		return nil
	}
	if len(input.Metadata.Participants) > maxReplayMemories {
		return fmt.Errorf("memory participants contain more than %d entries", maxReplayMemories)
	}
	if len(string(input.Metadata.Kind)) > maxReplayMemorySize || len(input.Metadata.Location) > maxReplayMemorySize {
		return fmt.Errorf("memory metadata field exceeds %d bytes", maxReplayMemorySize)
	}
	if totalBytes > maxReplayMemorySize-len(input.Metadata.Kind) {
		return fmt.Errorf("memory input metadata exceeds %d bytes", maxReplayMemorySize)
	}
	totalBytes += len(input.Metadata.Kind)
	if totalBytes > maxReplayMemorySize-len(input.Metadata.Location) {
		return fmt.Errorf("memory input metadata exceeds %d bytes", maxReplayMemorySize)
	}
	totalBytes += len(input.Metadata.Location)
	if err := validateUTF8String("memory kind", string(input.Metadata.Kind)); err != nil {
		return err
	}
	for index, participant := range input.Metadata.Participants {
		if len(participant) > maxReplayMemorySize {
			return fmt.Errorf("memory participant %d exceeds %d bytes", index, maxReplayMemorySize)
		}
		if totalBytes > maxReplayMemorySize-len(participant) {
			return fmt.Errorf("memory input metadata exceeds %d bytes", maxReplayMemorySize)
		}
		totalBytes += len(participant)
		if err := validateUTF8String(
			fmt.Sprintf("memory participant %d", index),
			participant,
		); err != nil {
			return err
		}
	}
	return validateUTF8String("memory location", input.Metadata.Location)
}

func validateMemorySearchInput(input *MemorySearchInput) error {
	if strings.TrimSpace(input.Query) == "" {
		return errors.New("invalid memory search input")
	}
	if len(input.Query) > maxReplayMemorySize {
		return fmt.Errorf("memory search query exceeds %d bytes", maxReplayMemorySize)
	}
	if err := validateUTF8String("memory search query", input.Query); err != nil {
		return err
	}
	options := input.Options
	switch options.Kind {
	case "", memory.KindFact, memory.KindEpisode:
	default:
		return fmt.Errorf("memory search has unknown kind %q", options.Kind)
	}
	if options.MaxResults < 0 || options.MaxResults > maxReplayMemories {
		return fmt.Errorf("memory search max results must be between 0 and %d", maxReplayMemories)
	}
	if math.IsNaN(options.SimilarityThreshold) || math.IsInf(options.SimilarityThreshold, 0) ||
		options.SimilarityThreshold < 0 || options.SimilarityThreshold > 1 {
		return errors.New("memory search similarity threshold must be finite and within [0,1]")
	}
	if options.TimeAfter != nil && options.TimeBefore != nil && options.TimeAfter.After(*options.TimeBefore) {
		return errors.New("memory search time range is reversed")
	}
	if options.HybridRRFK < 0 {
		return errors.New("memory search hybrid RRF k must be non-negative")
	}
	return nil
}

func validateTrackStep(step Step) error {
	if step.Track == nil {
		return fmt.Errorf("step %q kind %q requires track payload", step.Name, step.Kind)
	}
	if step.Track.Event == nil || step.Track.Event.Track == "" {
		return fmt.Errorf("step %q has invalid track input", step.Name)
	}
	if err := validateUTF8String("track name", string(step.Track.Event.Track)); err != nil {
		return fmt.Errorf("step %q: %w", step.Name, err)
	}
	if len(step.Track.Event.Track) > maxReplayTrackNameSize {
		return fmt.Errorf("step %q track name exceeds %d bytes", step.Name, maxReplayTrackNameSize)
	}
	if payload := step.Track.Event.Payload; payload != nil {
		if len(payload) > maxReplayTrackPayload {
			return fmt.Errorf("step %q track payload exceeds %d bytes", step.Name, maxReplayTrackPayload)
		}
		if err := validateUTF8String("track JSON payload", string(payload)); err != nil {
			return fmt.Errorf("step %q: %w", step.Name, err)
		}
		var decoded any
		if err := decodeJSON(payload, &decoded); err != nil {
			return fmt.Errorf("step %q has invalid track JSON payload: %w", step.Name, err)
		}
	}
	return nil
}

func validateEventStep(step Step) error {
	if step.Event.Event == nil || step.Event.LogicalID == "" {
		return fmt.Errorf("step %q has invalid event input", step.Name)
	}
	if err := validateUTF8String("logical event id", step.Event.LogicalID); err != nil {
		return fmt.Errorf("step %q: %w", step.Name, err)
	}
	if err := validateEventComparisonStrings(step.Event.Event, "event"); err != nil {
		return fmt.Errorf("step %q: %w", step.Name, err)
	}
	if err := validateEventToolCallArguments(step.Event.Event); err != nil {
		return fmt.Errorf("step %q: %w", step.Name, err)
	}
	extensionKeys := make([]string, 0, len(step.Event.Event.Extensions))
	for key := range step.Event.Event.Extensions {
		extensionKeys = append(extensionKeys, key)
	}
	sort.Strings(extensionKeys)
	for _, key := range extensionKeys {
		if key == "" {
			return fmt.Errorf("step %q event extension key must not be empty", step.Name)
		}
		if key == logicalEventIDExtension {
			return fmt.Errorf("step %q event extension %q is reserved", step.Name, key)
		}
		raw := step.Event.Event.Extensions[key]
		if raw != nil {
			if err := validateUTF8String("event extension value", string(raw)); err != nil {
				return fmt.Errorf("step %q event extension %q: %w", step.Name, key, err)
			}
			var decoded any
			if err := decodeJSON(raw, &decoded); err != nil {
				return fmt.Errorf("step %q event extension %q contains invalid JSON: %w", step.Name, key, err)
			}
		}
	}
	if err := validateEventStateDelta(step.Name, step.Event.Event.StateDelta); err != nil {
		return err
	}
	clonedEvent, err := cloneReplayEvent(step.Event.Event)
	if err != nil {
		return fmt.Errorf("step %q event cannot be cloned: %w", step.Name, err)
	}
	if err := event.SetExtension(clonedEvent, logicalEventIDExtension, step.Event.LogicalID); err != nil {
		return fmt.Errorf("step %q cannot set logical event id: %w", step.Name, err)
	}
	if err := validatePreparedEventSize(clonedEvent); err != nil {
		return fmt.Errorf("step %q: %w", step.Name, err)
	}
	return nil
}

func validatePreparedEventSize(evt *event.Event) error {
	encoded, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("event cannot be encoded: %w", err)
	}
	if len(encoded) > maxReplayEventSize {
		return fmt.Errorf("event exceeds %d bytes", maxReplayEventSize)
	}
	return nil
}

func validateEventComparisonStrings(evt *event.Event, owner string) error {
	if err := validateEventMetadataStrings(evt, owner); err != nil {
		return err
	}
	if evt.Response == nil {
		return nil
	}
	return validateResponseStrings(evt.Response, owner)
}

func validateEventMetadataStrings(evt *event.Event, owner string) error {
	check := func(name, value string) error {
		return validateUTF8String(owner+" "+name, value)
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "request id", value: evt.RequestID},
		{name: "invocation id", value: evt.InvocationID},
		{name: "parent invocation id", value: evt.ParentInvocationID},
		{name: "author", value: evt.Author},
		{name: "branch", value: evt.Branch},
		{name: "tag", value: evt.Tag},
		{name: "filter key", value: evt.FilterKey},
	} {
		if err := check(field.name, field.value); err != nil {
			return err
		}
	}
	if evt.ParentMetadata != nil {
		for _, field := range []struct {
			name  string
			value string
		}{
			{name: "parent trigger type", value: evt.ParentMetadata.TriggerType},
			{name: "parent trigger id", value: evt.ParentMetadata.TriggerID},
			{name: "parent trigger name", value: evt.ParentMetadata.TriggerName},
		} {
			if err := check(field.name, field.value); err != nil {
				return err
			}
		}
	}
	longRunningToolIDs := make([]string, 0, len(evt.LongRunningToolIDs))
	for id := range evt.LongRunningToolIDs {
		longRunningToolIDs = append(longRunningToolIDs, id)
	}
	sort.Strings(longRunningToolIDs)
	for _, id := range longRunningToolIDs {
		if err := check("long-running tool id", id); err != nil {
			return err
		}
	}
	extensionKeys := make([]string, 0, len(evt.Extensions))
	for key := range evt.Extensions {
		extensionKeys = append(extensionKeys, key)
	}
	sort.Strings(extensionKeys)
	for _, key := range extensionKeys {
		if err := check("extension key", key); err != nil {
			return err
		}
		if raw := evt.Extensions[key]; raw != nil {
			if err := check(fmt.Sprintf("extension %q value", key), string(raw)); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateResponseStrings(response *model.Response, owner string) error {
	check := func(name, value string) error {
		return validateUTF8String(owner+" "+name, value)
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "response id", value: response.ID},
		{name: "response object", value: response.Object},
		{name: "response model", value: response.Model},
	} {
		if err := check(field.name, field.value); err != nil {
			return err
		}
	}
	if response.SystemFingerprint != nil {
		if err := check("response system fingerprint", *response.SystemFingerprint); err != nil {
			return err
		}
	}
	if response.Error != nil {
		for _, field := range []struct {
			name  string
			value string
		}{
			{name: "response error message", value: response.Error.Message},
			{name: "response error type", value: response.Error.Type},
		} {
			if err := check(field.name, field.value); err != nil {
				return err
			}
		}
		if response.Error.Param != nil {
			if err := check("response error param", *response.Error.Param); err != nil {
				return err
			}
		}
		if response.Error.Code != nil {
			if err := check("response error code", *response.Error.Code); err != nil {
				return err
			}
		}
	}
	for choiceIndex := range response.Choices {
		if err := validateChoiceStrings(
			&response.Choices[choiceIndex],
			owner,
			choiceIndex,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateChoiceStrings(choice *model.Choice, owner string, choiceIndex int) error {
	check := func(name, value string) error {
		return validateUTF8String(owner+" "+name, value)
	}
	if choice.FinishReason != nil {
		if err := check(
			fmt.Sprintf("choice %d finish reason", choiceIndex),
			*choice.FinishReason,
		); err != nil {
			return err
		}
	}
	if err := validateLogprobsStrings(choice.Logprobs, check, choiceIndex); err != nil {
		return err
	}
	for _, field := range []struct {
		name    string
		message *model.Message
	}{
		{name: "message", message: &choice.Message},
		{name: "delta", message: &choice.Delta},
	} {
		if err := validateMessageStrings(
			field.message,
			fmt.Sprintf("%s choice %d %s", owner, choiceIndex, field.name),
		); err != nil {
			return err
		}
	}
	return nil
}

func validateLogprobsStrings(
	logprobs *model.Logprobs,
	check func(string, string) error,
	choiceIndex int,
) error {
	if logprobs == nil {
		return nil
	}
	for tokenIndex := range logprobs.Content {
		token := &logprobs.Content[tokenIndex]
		if err := check(
			fmt.Sprintf("choice %d logprob token %d", choiceIndex, tokenIndex),
			token.Token,
		); err != nil {
			return err
		}
		for topIndex := range token.TopLogprobs {
			if err := check(
				fmt.Sprintf("choice %d logprob token %d top token %d", choiceIndex, tokenIndex, topIndex),
				token.TopLogprobs[topIndex].Token,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateMessageStrings(message *model.Message, owner string) error {
	check := func(name, value string) error {
		return validateUTF8String(owner+" "+name, value)
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "role", value: string(message.Role)},
		{name: "content", value: message.Content},
		{name: "tool id", value: message.ToolID},
		{name: "tool name", value: message.ToolName},
		{name: "reasoning content", value: message.ReasoningContent},
		{name: "reasoning signature", value: message.ReasoningSignature},
	} {
		if err := check(field.name, field.value); err != nil {
			return err
		}
	}
	for index := range message.ContentParts {
		if err := validateContentPartStrings(&message.ContentParts[index], owner, index); err != nil {
			return err
		}
	}
	for index := range message.ToolCalls {
		if err := validateToolCallStrings(&message.ToolCalls[index], owner, index); err != nil {
			return err
		}
	}
	return nil
}

func validateContentPartStrings(part *model.ContentPart, owner string, index int) error {
	check := func(name, value string) error {
		return validateUTF8String(fmt.Sprintf("%s content part %d %s", owner, index, name), value)
	}
	if err := check("type", string(part.Type)); err != nil {
		return err
	}
	if part.Text != nil {
		if err := check("text", *part.Text); err != nil {
			return err
		}
	}
	if part.Image != nil {
		for _, field := range []struct {
			name  string
			value string
		}{
			{name: "image URL", value: part.Image.URL},
			{name: "image detail", value: part.Image.Detail},
			{name: "image format", value: part.Image.Format},
		} {
			if err := check(field.name, field.value); err != nil {
				return err
			}
		}
	}
	if part.Audio != nil {
		if err := check("audio format", part.Audio.Format); err != nil {
			return err
		}
	}
	if part.Video != nil {
		for _, field := range []struct {
			name  string
			value string
		}{
			{name: "video URL", value: part.Video.URL},
			{name: "video format", value: part.Video.Format},
		} {
			if err := check(field.name, field.value); err != nil {
				return err
			}
		}
	}
	if part.File != nil {
		for _, field := range []struct {
			name  string
			value string
		}{
			{name: "file name", value: part.File.Name},
			{name: "file URL", value: part.File.URL},
			{name: "file id", value: part.File.FileID},
			{name: "file MIME type", value: part.File.MimeType},
		} {
			if err := check(field.name, field.value); err != nil {
				return err
			}
		}
	}
	if part.ContentRef != nil {
		for _, field := range []struct {
			name  string
			value string
		}{
			{name: "content reference artifact", value: part.ContentRef.ArtifactRef},
			{name: "content reference artifact name", value: part.ContentRef.ArtifactName},
			{name: "content reference MIME type", value: part.ContentRef.MimeType},
			{name: "content reference SHA-256", value: part.ContentRef.SHA256},
			{name: "content reference original name", value: part.ContentRef.OriginalName},
			{name: "content reference event id", value: part.ContentRef.EventID},
			{name: "content reference request id", value: part.ContentRef.RequestID},
		} {
			if err := check(field.name, field.value); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateToolCallStrings(call *model.ToolCall, owner string, index int) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "type", value: call.Type},
		{name: "id", value: call.ID},
		{name: "function name", value: call.Function.Name},
		{name: "function description", value: call.Function.Description},
	} {
		if err := validateUTF8String(
			fmt.Sprintf("%s tool call %d %s", owner, index, field.name),
			field.value,
		); err != nil {
			return err
		}
	}
	if call.ExtraFields == nil {
		return nil
	}
	return validateJSONValue(
		fmt.Sprintf("%s tool call %d extra fields", owner, index),
		call.ExtraFields,
	)
}

func validateEventToolCallArguments(evt *event.Event) error {
	if evt.Response == nil {
		return nil
	}
	for choiceIndex := range evt.Response.Choices {
		choice := &evt.Response.Choices[choiceIndex]
		for _, field := range []struct {
			name  string
			calls []model.ToolCall
		}{
			{name: "message", calls: choice.Message.ToolCalls},
			{name: "delta", calls: choice.Delta.ToolCalls},
		} {
			for callIndex := range field.calls {
				arguments := field.calls[callIndex].Function.Arguments
				if len(arguments) == 0 {
					continue
				}
				owner := fmt.Sprintf(
					"event choice %d %s tool call %d arguments",
					choiceIndex,
					field.name,
					callIndex,
				)
				if err := validateUTF8String(owner, string(arguments)); err != nil {
					return err
				}
				if evt.IsPartial {
					continue
				}
				if _, err := canonicalJSONString(string(arguments)); err != nil {
					return fmt.Errorf(
						"%s has invalid JSON: %w",
						owner,
						err,
					)
				}
			}
		}
	}
	return nil
}

func validateJSONValue(owner string, value any) error {
	raw, err := marshalJSONValue(owner, value, maxReplayJSONBytes)
	if err != nil {
		return err
	}
	var decoded any
	if err := decodeJSON(raw, &decoded); err != nil {
		return fmt.Errorf("%s contains invalid JSON data: %w", owner, err)
	}
	return nil
}

func marshalJSONValue(owner string, value any, limit int) ([]byte, error) {
	// Walk the original object graph before invoking custom MarshalJSON methods.
	// A custom marshaler is user-controlled and may recurse through a cycle
	// itself; validating after json.Marshal would give it a chance to overflow
	// the stack before our cycle guard runs.
	reflected := reflect.ValueOf(value)
	if err := validateJSONGraphBudget(owner, reflected, limit); err != nil {
		return nil, err
	}
	if err := validateJSONStrings(owner, reflected); err != nil {
		return nil, err
	}
	if containsCustomJSONState(reflected, make(map[jsonReference]struct{})) {
		return nil, fmt.Errorf("%s contains custom marshaler state that cannot be cloned safely", owner)
	}
	marshalValue := value
	if reflected.IsValid() {
		cloned := cloneJSONValue(reflected)
		if !cloned.IsValid() || !cloned.CanInterface() {
			return nil, fmt.Errorf("%s cannot be cloned safely", owner)
		}
		marshalValue = cloned.Interface()
	}
	raw, err := json.Marshal(marshalValue)
	if err != nil {
		return nil, fmt.Errorf("%s contains invalid JSON data: %w", owner, err)
	}
	if len(raw) > limit {
		return nil, fmt.Errorf("%s exceeds %d JSON bytes", owner, limit)
	}
	return raw, nil
}

func validateJSONGraphBudget(owner string, value reflect.Value, limit int) error {
	// Every additional JSON node needs at least two compact bytes once its
	// delimiter is included. Bounding nodes therefore rejects graphs that
	// cannot fit before any full slice, array, or map traversal takes place.
	remaining := (limit + 1) / 2
	return consumeJSONGraphBudget(owner, value, &remaining, make(map[jsonReference]struct{}), 0, limit)
}

//nolint:gocyclo // Reflection-kind accounting and cycle detection share one pre-marshaling resource boundary.
func consumeJSONGraphBudget(
	owner string,
	value reflect.Value,
	remaining *int,
	visiting map[jsonReference]struct{},
	depth int,
	limit int,
) error {
	if !value.IsValid() {
		return nil
	}
	if depth > maxReplayJSONDepth {
		return fmt.Errorf("%s exceeds JSON nesting depth limit %d", owner, maxReplayJSONDepth)
	}
	if value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if !value.IsNil() {
			if reference, ok := jsonValueReference(value); ok {
				if _, exists := visiting[reference]; exists {
					return fmt.Errorf("%s contains cyclic JSON data", owner)
				}
				visiting[reference] = struct{}{}
				defer delete(visiting, reference)
			}
			return consumeJSONGraphBudget(owner, value.Elem(), remaining, visiting, depth+1, limit)
		}
	}
	if *remaining <= 0 {
		return fmt.Errorf("%s exceeds %d JSON bytes", owner, limit)
	}
	*remaining--
	if reference, ok := jsonValueReference(value); ok {
		if _, exists := visiting[reference]; exists {
			return fmt.Errorf("%s contains cyclic JSON data", owner)
		}
		visiting[reference] = struct{}{}
		defer delete(visiting, reference)
	}
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		return nil
	case reflect.Map:
		if value.Len() > *remaining/2 {
			return fmt.Errorf("%s exceeds %d JSON bytes", owner, limit)
		}
		iterator := value.MapRange()
		for iterator.Next() {
			if err := consumeJSONGraphBudget(owner, iterator.Key(), remaining, visiting, depth+1, limit); err != nil {
				return err
			}
			if err := consumeJSONGraphBudget(owner, iterator.Value(), remaining, visiting, depth+1, limit); err != nil {
				return err
			}
		}
	case reflect.String:
		if limit < 2 || value.Len() > limit-2 {
			return fmt.Errorf("%s exceeds %d JSON bytes", owner, limit)
		}
	case reflect.Slice:
		if value.Type() == rawMessageType {
			if value.Len() > limit {
				return fmt.Errorf("%s exceeds %d JSON bytes", owner, limit)
			}
			return nil
		}
		if value.Type().Elem().Kind() == reflect.Uint8 {
			if limit < 2 || base64.StdEncoding.EncodedLen(value.Len()) > limit-2 {
				return fmt.Errorf("%s exceeds %d JSON bytes", owner, limit)
			}
			return nil
		}
		if value.Len() > *remaining {
			return fmt.Errorf("%s exceeds %d JSON bytes", owner, limit)
		}
		for index := 0; index < value.Len(); index++ {
			if err := consumeJSONGraphBudget(owner, value.Index(index), remaining, visiting, depth+1, limit); err != nil {
				return err
			}
		}
	case reflect.Array:
		if value.Len() > *remaining {
			return fmt.Errorf("%s exceeds %d JSON bytes", owner, limit)
		}
		for index := 0; index < value.Len(); index++ {
			if err := consumeJSONGraphBudget(owner, value.Index(index), remaining, visiting, depth+1, limit); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			if field.PkgPath != "" || strings.Split(field.Tag.Get("json"), ",")[0] == "-" {
				continue
			}
			if err := consumeJSONGraphBudget(owner, value.Field(index), remaining, visiting, depth+1, limit); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateJSONStrings(owner string, value reflect.Value) error {
	return validateJSONStringsRecursively(
		owner,
		value,
		make(map[jsonReference]struct{}),
		0,
	)
}

type jsonReference struct {
	typeOf   reflect.Type
	pointer  uintptr
	length   int
	capacity int
}

func validateJSONStringsRecursively(
	owner string,
	value reflect.Value,
	visiting map[jsonReference]struct{},
	depth int,
) error {
	if !value.IsValid() {
		return nil
	}
	if depth > maxReplayJSONDepth {
		return fmt.Errorf("%s exceeds JSON nesting depth limit %d", owner, maxReplayJSONDepth)
	}
	if reference, ok := jsonValueReference(value); ok {
		if _, exists := visiting[reference]; exists {
			return fmt.Errorf("%s contains cyclic JSON data", owner)
		}
		visiting[reference] = struct{}{}
		defer delete(visiting, reference)
	}
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		return validateJSONStringsRecursively(owner, value.Elem(), visiting, depth+1)
	case reflect.String:
		return validateUTF8String(owner, value.String())
	case reflect.Map:
		return validateJSONMapStrings(owner, value, visiting, depth)
	case reflect.Slice:
		return validateJSONSliceStrings(owner, value, visiting, depth)
	case reflect.Array:
		return validateJSONSequenceStrings(owner, value, visiting, depth)
	case reflect.Struct:
		return validateJSONStructStrings(owner, value, visiting, depth)
	}
	return nil
}

func validateJSONMapStrings(
	owner string,
	value reflect.Value,
	visiting map[jsonReference]struct{},
	depth int,
) error {
	iterator := value.MapRange()
	for iterator.Next() {
		if err := validateJSONStringsRecursively(owner+" key", iterator.Key(), visiting, depth+1); err != nil {
			return err
		}
		if err := validateJSONStringsRecursively(owner+" value", iterator.Value(), visiting, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func validateJSONSliceStrings(
	owner string,
	value reflect.Value,
	visiting map[jsonReference]struct{},
	depth int,
) error {
	if value.Type() == reflect.TypeOf(json.RawMessage(nil)) {
		return validateUTF8String(owner, string(value.Bytes()))
	}
	if value.Type().Elem().Kind() == reflect.Uint8 {
		return nil
	}
	return validateJSONSequenceStrings(owner, value, visiting, depth)
}

func validateJSONSequenceStrings(
	owner string,
	value reflect.Value,
	visiting map[jsonReference]struct{},
	depth int,
) error {
	for index := 0; index < value.Len(); index++ {
		if err := validateJSONStringsRecursively(
			fmt.Sprintf("%s item %d", owner, index),
			value.Index(index),
			visiting,
			depth+1,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateJSONStructStrings(
	owner string,
	value reflect.Value,
	visiting map[jsonReference]struct{},
	depth int,
) error {
	// A custom marshaler can read unexported fields directly. Walk the complete
	// object graph for cycle detection in that case, while retaining the normal
	// UTF-8 checks only for fields encoding/json would otherwise expose.
	customMarshaler := implementsMarshaler(value, jsonMarshalerType) ||
		implementsMarshaler(value, textMarshalerType)
	if customMarshaler {
		if err := detectJSONCycles(owner, value, visiting, depth); err != nil {
			return err
		}
	}
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		if field.PkgPath != "" || strings.Split(field.Tag.Get("json"), ",")[0] == "-" {
			continue
		}
		if err := validateJSONStringsRecursively(
			owner+" field "+field.Name,
			value.Field(index),
			visiting,
			depth+1,
		); err != nil {
			return err
		}
	}
	return nil
}

func detectJSONCycles(owner string, value reflect.Value, visiting map[jsonReference]struct{}, depth int) error {
	if !value.IsValid() || value.Type() == timeValueType {
		return nil
	}
	if depth > maxReplayJSONDepth {
		return fmt.Errorf("%s exceeds JSON nesting depth limit %d", owner, maxReplayJSONDepth)
	}
	if reference, ok := jsonValueReference(value); ok {
		if _, exists := visiting[reference]; exists {
			return fmt.Errorf("%s contains cyclic JSON data", owner)
		}
		visiting[reference] = struct{}{}
		defer delete(visiting, reference)
	}
	return detectJSONCycleChildren(owner, value, visiting, depth)
}

func detectJSONCycleChildren(owner string, value reflect.Value, visiting map[jsonReference]struct{}, depth int) error {
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		return detectJSONCycles(owner, value.Elem(), visiting, depth+1)
	case reflect.Map:
		return detectJSONMapCycles(owner, value, visiting, depth)
	case reflect.Slice, reflect.Array:
		return detectJSONSequenceCycles(owner, value, visiting, depth)
	case reflect.Struct:
		return detectJSONStructCycles(owner, value, visiting, depth)
	default:
		return nil
	}
}

func detectJSONMapCycles(owner string, value reflect.Value, visiting map[jsonReference]struct{}, depth int) error {
	if value.IsNil() {
		return nil
	}
	iterator := value.MapRange()
	for iterator.Next() {
		if err := detectJSONCycles(owner, iterator.Key(), visiting, depth+1); err != nil {
			return err
		}
		if err := detectJSONCycles(owner, iterator.Value(), visiting, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func detectJSONSequenceCycles(owner string, value reflect.Value, visiting map[jsonReference]struct{}, depth int) error {
	if value.Kind() == reflect.Slice && value.IsNil() {
		return nil
	}
	for index := 0; index < value.Len(); index++ {
		if err := detectJSONCycles(owner, value.Index(index), visiting, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func detectJSONStructCycles(owner string, value reflect.Value, visiting map[jsonReference]struct{}, depth int) error {
	for index := 0; index < value.NumField(); index++ {
		if err := detectJSONCycles(owner, value.Field(index), visiting, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func jsonValueReference(value reflect.Value) (jsonReference, bool) {
	switch value.Kind() {
	case reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return jsonReference{}, false
		}
		reference := jsonReference{typeOf: value.Type(), pointer: value.Pointer()}
		if value.Kind() == reflect.Slice {
			reference.length = value.Len()
			reference.capacity = value.Cap()
		}
		return reference, true
	default:
		return jsonReference{}, false
	}
}

func validateEventStateDelta(stepName string, stateDelta session.StateMap) error {
	if len(stateDelta) > maxReplayStateKeyCount {
		return fmt.Errorf("step %q event state delta contains %d keys, limit is %d", stepName, len(stateDelta), maxReplayStateKeyCount)
	}
	keys := make([]string, 0, len(stateDelta))
	for key := range stateDelta {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	totalKeyBytes := 0
	totalValueBytes := 0
	for _, key := range keys {
		if err := validateUTF8String("event state delta key", key); err != nil {
			return fmt.Errorf("step %q: %w", stepName, err)
		}
		if key == "" {
			return fmt.Errorf("step %q event state delta: state key must not be empty", stepName)
		}
		if key == replayTrackStateKey {
			return fmt.Errorf(
				"step %q event state delta: state key %q is reserved for backend track indexing",
				stepName,
				key,
			)
		}
		if len(key) > maxReplayStateKeySize {
			return fmt.Errorf("step %q event state delta key %q exceeds %d bytes", stepName, key, maxReplayStateKeySize)
		}
		if totalKeyBytes > maxReplayStateKeyTotalSize-len(key) {
			return fmt.Errorf("step %q event state delta keys exceed %d total bytes", stepName, maxReplayStateKeyTotalSize)
		}
		totalKeyBytes += len(key)
		if len(stateDelta[key]) > maxReplayStateValueSize {
			return fmt.Errorf("step %q event state delta key %q exceeds %d bytes", stepName, key, maxReplayStateValueSize)
		}
		if totalValueBytes > maxReplayStateTotalSize-len(stateDelta[key]) {
			return fmt.Errorf("step %q event state delta values exceed %d total bytes", stepName, maxReplayStateTotalSize)
		}
		totalValueBytes += len(stateDelta[key])
		for _, prefix := range []string{
			session.StateAppPrefix,
			session.StateUserPrefix,
			session.StateTempPrefix,
		} {
			if key == prefix {
				return fmt.Errorf(
					"step %q event state delta: state key %q has an empty scoped name",
					stepName,
					key,
				)
			}
		}
	}
	return nil
}

func validateStateStep(step Step) error {
	if len(step.State.Values) == 0 && len(step.State.DeleteKeys) == 0 && !step.State.Clear {
		return fmt.Errorf("step %q has no state mutations", step.Name)
	}
	switch step.State.Scope {
	case StateScopeApp, StateScopeUser:
		return validateStateKeys(
			fmt.Sprintf("step %q", step.Name),
			step.State.Scope,
			step.State.Values,
			step.State.DeleteKeys,
		)
	case StateScopeSession:
		if step.State.Clear {
			return fmt.Errorf("step %q cannot clear session state", step.Name)
		}
		if len(step.State.DeleteKeys) > 0 {
			return fmt.Errorf("step %q cannot delete session state", step.Name)
		}
		return validateStateKeys(
			fmt.Sprintf("step %q", step.Name),
			step.State.Scope,
			step.State.Values,
			nil,
		)
	default:
		return fmt.Errorf("step %q has unknown state scope %q", step.Name, step.State.Scope)
	}
}

func validateStateKeys(
	owner string,
	scope StateScope,
	values session.StateMap,
	deleteKeys []string,
) error {
	if len(values) > maxReplayStateKeyCount || len(deleteKeys) > maxReplayStateKeyCount-len(values) {
		return fmt.Errorf(
			"%s contains %d state key operations, limit is %d",
			owner,
			len(values)+len(deleteKeys),
			maxReplayStateKeyCount,
		)
	}
	valueKeys := make([]string, 0, len(values))
	for key := range values {
		valueKeys = append(valueKeys, key)
	}
	sort.Strings(valueKeys)
	totalKeyBytes := 0
	totalValueBytes := 0
	for _, key := range valueKeys {
		if err := validateStateKey(scope, key); err != nil {
			return fmt.Errorf("%s: %w", owner, err)
		}
		if len(key) > maxReplayStateKeySize {
			return fmt.Errorf("%s state key %q exceeds %d bytes", owner, key, maxReplayStateKeySize)
		}
		if totalKeyBytes > maxReplayStateKeyTotalSize-len(key) {
			return fmt.Errorf("%s state keys exceed %d total bytes", owner, maxReplayStateKeyTotalSize)
		}
		totalKeyBytes += len(key)
		if len(values[key]) > maxReplayStateValueSize {
			return fmt.Errorf("%s state key %q exceeds %d bytes", owner, key, maxReplayStateValueSize)
		}
		if totalValueBytes > maxReplayStateTotalSize-len(values[key]) {
			return fmt.Errorf("%s state values exceed %d total bytes", owner, maxReplayStateTotalSize)
		}
		totalValueBytes += len(values[key])
	}
	for _, key := range deleteKeys {
		if err := validateStateKey(scope, key); err != nil {
			return fmt.Errorf("%s: %w", owner, err)
		}
		if len(key) > maxReplayStateKeySize {
			return fmt.Errorf("%s state key %q exceeds %d bytes", owner, key, maxReplayStateKeySize)
		}
		if totalKeyBytes > maxReplayStateKeyTotalSize-len(key) {
			return fmt.Errorf("%s state keys exceed %d total bytes", owner, maxReplayStateKeyTotalSize)
		}
		totalKeyBytes += len(key)
	}
	return nil
}

func validateStateKey(scope StateScope, key string) error {
	if err := validateUTF8String(string(scope)+" state key", key); err != nil {
		return err
	}
	if key == "" {
		return errors.New("state key must not be empty")
	}
	if scope == StateScopeSession && key == replayTrackStateKey {
		return fmt.Errorf("%s state key %q is reserved for backend track indexing", scope, key)
	}
	if strings.HasPrefix(key, session.StateAppPrefix) ||
		strings.HasPrefix(key, session.StateUserPrefix) {
		return fmt.Errorf("%s state key %q must not include a scope prefix", scope, key)
	}
	if (scope == StateScopeApp || scope == StateScopeUser) &&
		strings.HasPrefix(key, session.StateTempPrefix) {
		return fmt.Errorf("%s state key %q must not include a scope prefix", scope, key)
	}
	return nil
}

func validateConcurrentStep(step Step) error {
	if len(step.Concurrent) < 2 {
		return fmt.Errorf("step %q must contain at least two concurrent branches", step.Name)
	}
	owners := newConcurrentWriteOwners()
	var concurrentKind StepKind
	for branchIndex, branch := range step.Concurrent {
		if len(branch) == 0 {
			return fmt.Errorf("step %q has an empty concurrent branch", step.Name)
		}
		for _, nested := range branch {
			if err := validateStep(nested); err != nil {
				return fmt.Errorf("step %q: %w", step.Name, err)
			}
			if concurrentKind != "" && concurrentKind != nested.Kind {
				return fmt.Errorf(
					"step %q cannot mix concurrent %s and %s writes",
					step.Name,
					concurrentKind,
					nested.Kind,
				)
			}
			concurrentKind = nested.Kind
			if err := owners.validate(step.Name, branchIndex, nested); err != nil {
				return err
			}
		}
	}
	return nil
}

type concurrentWriteOwners struct {
	state   map[string]int
	memory  map[string]int
	summary map[string]int
	track   map[string]int
}

func newConcurrentWriteOwners() *concurrentWriteOwners {
	return &concurrentWriteOwners{
		state:   make(map[string]int),
		memory:  make(map[string]int),
		summary: make(map[string]int),
		track:   make(map[string]int),
	}
}

func (o *concurrentWriteOwners) validate(
	stepName string,
	branchIndex int,
	nested Step,
) error {
	switch nested.Kind {
	case StepAppendEvent:
		return validateConcurrentEvent(stepName, branchIndex, nested)
	case StepUpdateState:
		return o.validateState(stepName, branchIndex, nested.State)
	case StepAddMemory:
		return claimConcurrentOwner(
			o.memory,
			nested.Memory.Memory,
			branchIndex,
			fmt.Sprintf("step %q has concurrent memory conflict for %q", stepName, nested.Memory.Memory),
		)
	case StepCreateSummary:
		return o.validateSummary(stepName, branchIndex, nested.Summary)
	case StepAppendTrack:
		trackName := string(nested.Track.Event.Track)
		return claimConcurrentOwner(
			o.track,
			trackName,
			branchIndex,
			fmt.Sprintf("step %q has concurrent track conflict for %q", stepName, trackName),
		)
	default:
		return fmt.Errorf("step %q branch %d contains unsupported concurrent kind %q", stepName, branchIndex, nested.Kind)
	}
}

func validateConcurrentEvent(stepName string, branchIndex int, nested Step) error {
	if len(nested.Event.Event.StateDelta) > 0 {
		return fmt.Errorf("step %q branch %d event %q contains a state delta", stepName, branchIndex, nested.Name)
	}
	if !replayEventIsPersistable(nested.Event.Event) {
		return fmt.Errorf("step %q branch %d event %q is not persistable", stepName, branchIndex, nested.Name)
	}
	return nil
}

func (o *concurrentWriteOwners) validateState(
	stepName string,
	branchIndex int,
	input *StateInput,
) error {
	if input.Clear {
		return fmt.Errorf("step %q branch %d cannot concurrently clear %s state", stepName, branchIndex, input.Scope)
	}
	for key := range input.Values {
		if err := o.claimState(stepName, branchIndex, input.Scope, key); err != nil {
			return err
		}
	}
	for _, key := range input.DeleteKeys {
		if err := o.claimState(stepName, branchIndex, input.Scope, key); err != nil {
			return err
		}
	}
	return nil
}

func (o *concurrentWriteOwners) claimState(
	stepName string,
	branchIndex int,
	scope StateScope,
	key string,
) error {
	return claimConcurrentOwner(
		o.state,
		stateFootprintKey(scope, key),
		branchIndex,
		fmt.Sprintf("step %q has concurrent state conflict on %s:%s", stepName, scope, key),
	)
}

func (o *concurrentWriteOwners) validateSummary(
	stepName string,
	branchIndex int,
	input *SummaryInput,
) error {
	if input.FilterKey == "" {
		return fmt.Errorf("step %q branch %d cannot concurrently create a full-session summary", stepName, branchIndex)
	}
	return claimConcurrentOwner(
		o.summary,
		input.FilterKey,
		branchIndex,
		fmt.Sprintf("step %q has concurrent summary conflict for filter key %q", stepName, input.FilterKey),
	)
}

func claimConcurrentOwner(
	owners map[string]int,
	key string,
	branchIndex int,
	conflictMessage string,
) error {
	if owner, exists := owners[key]; exists && owner != branchIndex {
		return errors.New(conflictMessage)
	}
	owners[key] = branchIndex
	return nil
}

func stateFootprintKey(scope StateScope, key string) string {
	return string(scope) + ":" + key
}

func replayEventIsPersistable(evt *event.Event) bool {
	return evt != nil && evt.Response != nil && !evt.IsPartial && evt.IsValidContent()
}

type causalOrderPlan struct {
	lanes        map[string]string
	predecessors map[string][]string
}

func buildCausalOrderPlan(steps []Step) *causalOrderPlan {
	if !containsConcurrentStep(steps) {
		return nil
	}
	plan := &causalOrderPlan{
		lanes:        make(map[string]string),
		predecessors: make(map[string][]string),
	}
	frontier := make([]string, 0)
	for stepIndex, step := range steps {
		switch step.Kind {
		case StepAppendEvent:
			if !replayEventIsPersistable(step.Event.Event) {
				continue
			}
			plan.predecessors[step.Event.LogicalID] = append([]string(nil), frontier...)
			frontier = []string{step.Event.LogicalID}
		case StepConcurrent:
			exits := make([]string, 0, len(step.Concurrent))
			for branchIndex, branch := range step.Concurrent {
				branchFrontier := append([]string(nil), frontier...)
				lane := fmt.Sprintf("%d/%d", stepIndex, branchIndex)
				for _, nested := range branch {
					if nested.Kind != StepAppendEvent {
						continue
					}
					logicalID := nested.Event.LogicalID
					plan.lanes[logicalID] = lane
					plan.predecessors[logicalID] = append([]string(nil), branchFrontier...)
					branchFrontier = []string{logicalID}
				}
				exits = appendUniqueStrings(exits, branchFrontier...)
			}
			if len(exits) > 0 {
				frontier = exits
			}
		}
	}
	return plan
}

func appendUniqueStrings(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func hasBackend(backends []Backend, name string) bool {
	for _, backend := range backends {
		if backend.Name == name {
			return true
		}
	}
	return false
}

func missingCapabilities(required []Capability, actual Capabilities) []Capability {
	var missing []Capability
	for _, capability := range required {
		if !actual[capability] {
			missing = append(missing, capability)
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
	return missing
}

func capabilitySet(capabilities []Capability) Capabilities {
	set := make(Capabilities, len(capabilities))
	for _, capability := range capabilities {
		set[capability] = true
	}
	return set
}

func countDiffs(diffs []Diff) (blocking, allowed int) {
	for _, diff := range diffs {
		if diff.Allowed {
			allowed++
		} else {
			blocking++
		}
	}
	return blocking, allowed
}

func hasSelfExecutionDiff(diffs []Diff, backend string) bool {
	for _, diff := range diffs {
		if diff.BackendA == backend && diff.BackendB == backend && diff.Path == "/execution" {
			return true
		}
	}
	return false
}

func cloneJSONMap(input CanonicalMap) CanonicalMap {
	raw, _ := json.Marshal(input)
	var output CanonicalMap
	_ = decodeJSON(raw, &output)
	return output
}
