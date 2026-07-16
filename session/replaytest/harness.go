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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// DefaultCaseTimeout is the default per-case execution timeout.
const DefaultCaseTimeout = 60 * time.Second

// DefaultMaxSnapshotSize is the default maximum snapshot size (10 MB).
const DefaultMaxSnapshotSize int64 = 10 * 1024 * 1024

// DefaultCircuitBreakerMaxFailures is the default consecutive failure count
// before the circuit breaker trips and skips the backend for remaining cases.
const DefaultCircuitBreakerMaxFailures = 5

// DefaultMaxMemoryUsagePct is the default heap usage percentage threshold.
// If heap usage exceeds this, capture is skipped to prevent OOM.
const DefaultMaxMemoryUsagePct = 0.85

// Harness executes cases and compares every backend with the first backend.
type Harness struct {
	Backends   []Backend
	Normalizer *Normalizer
	Allowed    []AllowedDiff
	// Timeout sets the per-case execution timeout. Zero means DefaultCaseTimeout.
	Timeout time.Duration
	// Retry configures retry behavior for transient backend errors.
	// Zero value means DefaultRetryPolicy. Overridden by Backend.Retry if set.
	Retry RetryPolicy
	// MaxSnapshotSize caps the size of a single normalized snapshot.
	// Zero means DefaultMaxSnapshotSize (10 MB).
	MaxSnapshotSize int64
	// Logf receives structured progress messages. Optional; defaults to no-op.
	Logf func(format string, args ...any)
	// SnapshotDir, if set, saves per-case snapshots to this directory for crash recovery.
	SnapshotDir string
	// GoldenDir, if set, enables golden trace regression checking.
	// Baseline snapshots are compared against previously saved golden traces.
	GoldenDir string
	// UpdateGolden, if true, updates golden traces instead of comparing.
	UpdateGolden bool
	// CircuitBreakerMaxFailures sets how many consecutive failures trip the circuit
	// breaker in RunSuite. Zero means DefaultCircuitBreakerMaxFailures. Negative disables.
	CircuitBreakerMaxFailures int
	// MaxMemoryUsagePct sets the heap usage threshold (0-1) above which captures are
	// skipped to prevent OOM. Zero means DefaultMaxMemoryUsagePct. Negative disables.
	MaxMemoryUsagePct float64
	// Parallelism controls how many cases RunSuite executes concurrently.
	// 0 or 1 means sequential (default). >1 enables parallel execution with
	// a worker pool of that size. Each worker gets isolated backend instances
	// via the BackendFactory, so cases must not share mutable state.
	Parallelism int
	// ProgressFunc is called after each case completes in RunSuite.
	// It receives the completed case count and total case count.
	// Optional; useful for CI progress reporting.
	ProgressFunc func(completed, total int, result CaseResult)
	// memoryCheckFn overrides the default memory pressure check for testing.
	// When set, this function is called instead of reading runtime.MemStats.
	// It returns an error if memory pressure is too high, or nil otherwise.
	memoryCheckFn func(maxUsagePct float64) error
}

func (h Harness) logf(format string, args ...any) {
	if h.Logf != nil {
		h.Logf(format, args...)
	}
}

type capturedSnapshot struct {
	backendName string
	snapshot    Snapshot
	metric      BackendMetric
}

// Run executes one case with isolated backend instances supplied by the caller.
func (h Harness) Run(ctx context.Context, replayCase Case) (CaseResult, error) {
	if err := validateCase(replayCase); err != nil {
		return CaseResult{}, &ReplayError{Kind: ErrCaseValidation, Case: replayCase.Name, Cause: err}
	}
	if len(h.Backends) < 2 {
		return CaseResult{}, &ReplayError{Kind: ErrCaseValidation, Cause: fmt.Errorf("replay harness requires at least two backends")}
	}
	// Merge case-level and harness-level allowed diffs.
	mergedAllowed := mergeAllowedDiffs(h.Allowed, replayCase.AllowedDiffs)
	if err := validateAllowedDiffs(mergedAllowed); err != nil {
		return CaseResult{}, &ReplayError{Kind: ErrCaseValidation, Case: replayCase.Name, Cause: err}
	}
	if err := validateBackends(h.Backends); err != nil {
		return CaseResult{}, &ReplayError{Kind: ErrCaseValidation, Cause: err}
	}
	if err := validateRequiredCapabilities(replayCase, h.Backends); err != nil {
		return CaseResult{}, &ReplayError{Kind: ErrCaseValidation, Case: replayCase.Name, Cause: err}
	}

	// Use mergedAllowed for all comparisons within this Run invocation.
	allowed := mergedAllowed

	timeout := h.Timeout
	if timeout <= 0 {
		timeout = DefaultCaseTimeout
	}
	maxSnapSize := h.MaxSnapshotSize
	if maxSnapSize <= 0 {
		maxSnapSize = DefaultMaxSnapshotSize
	}
	retry := h.Retry
	if retry.MaxAttempts <= 0 {
		retry = DefaultRetryPolicy()
	}
	maxMemPct := h.MaxMemoryUsagePct
	if maxMemPct == 0 {
		maxMemPct = DefaultMaxMemoryUsagePct
	}

	normalizer := h.Normalizer
	if normalizer == nil {
		normalizer = NewNormalizer(DefaultNormalizerConfig())
	}

	start := time.Now()
	result := CaseResult{
		Name:            replayCase.Name,
		SkippedBackends: make(map[string][]string),
		Capabilities:    make(map[string]map[string]CapabilityDesc, len(h.Backends)),
	}

	// Deferred cleanup: remove test data from all backends, even on panic.
	defer func() {
		for _, backend := range h.Backends {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			var key session.Key
			if backend.SessKey != nil {
				key = backend.SessKey()
			}
			var userKey memory.UserKey
			if key.AppName != "" {
				userKey = memory.UserKey{AppName: key.AppName, UserID: key.UserID}
			}
			if err := backend.Cleanup(cleanupCtx, key, userKey); err != nil {
				h.logf("replay: cleanup %s: %v", backend.Name, err)
			}
			// Leak detection: verify cleanup was effective.
			if err := backend.VerifyCleanup(cleanupCtx, key, userKey); err != nil {
				h.logf("replay: LEAK WARNING: %s: %v", backend.Name, err)
			}
			cancel()
		}
	}()

	// Probe backends that have health checks.
	for _, backend := range h.Backends {
		if backend.Probe != nil {
			h.logf("replay: case=%s backend=%s phase=probe", replayCase.Name, backend.Name)
			if err := retryOperation(ctx, retry, func(ctx context.Context) error {
				probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				return backend.Probe(probeCtx)
			}); err != nil {
				return result, &ReplayError{Kind: ErrBackendProbe, Backend: backend.Name, Case: replayCase.Name, Cause: err}
			}
		}
	}

	// Warm-up backends that have validation cycles.
	for _, backend := range h.Backends {
		if backend.WarmUp != nil {
			warmUpStart := time.Now()
			h.logf("replay: case=%s backend=%s phase=warmup", replayCase.Name, backend.Name)
			if err := retryOperation(ctx, retry, func(ctx context.Context) error {
				warmUpCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				return backend.WarmUp(warmUpCtx, backend)
			}); err != nil {
				return result, &ReplayError{Kind: ErrBackendWarmUp, Backend: backend.Name, Case: replayCase.Name, Cause: err}
			}
			h.logf("replay: case=%s backend=%s phase=warmup duration=%s",
				replayCase.Name, backend.Name, time.Since(warmUpStart))
		}
	}

	// Memory pressure check before capture — skip if heap is overloaded.
	if maxMemPct > 0 {
		checkFn := memoryPressureCheck
		if h.memoryCheckFn != nil {
			checkFn = h.memoryCheckFn
		}
		if err := checkFn(maxMemPct); err != nil {
			h.logf("replay: case=%s skipping capture: %v", replayCase.Name, err)
			result.Status = StatusSkip
			result.SkipReason = err.Error()
			result.Duration = time.Since(start).String()
			return result, nil
		}
	}

	snapshots := make([]capturedSnapshot, 0, len(h.Backends))
	sectionsCompared := 0
	sectionsSkipped := 0

	// Execute and capture on each backend.
	if len(h.Backends) <= 2 {
		// Sequential for 2 backends — avoids goroutine overhead.
		for i, backend := range h.Backends {
			snap, err := h.captureOnBackend(ctx, replayCase, backend, normalizer, retry, timeout, maxSnapSize, i, &result)
			if err != nil {
				return result, err
			}
			if snap != nil {
				snapshots = append(snapshots, *snap)
			}
		}
	} else {
		// Concurrent capture for 3+ backends.
		type backendCapture struct {
			snap  capturedSnapshot
			local CaseResult
		}
		snapResults := make([]backendCapture, len(h.Backends))
		g, gCtx := errgroup.WithContext(ctx)
		for i, backend := range h.Backends {
			i, backend := i, backend
			g.Go(func() error {
				local := result // copy
				// Deep-copy maps to avoid concurrent map writes.
				local.SkippedBackends = make(map[string][]string)
				local.Capabilities = make(map[string]map[string]CapabilityDesc)
				// Check capability support under the group context.
				unsupported := unsupportedRequiredCapabilities(replayCase, backend)
				if len(unsupported) > 0 {
					if i == 0 {
						return &ReplayError{Kind: ErrCaseValidation, Backend: backend.Name, Case: replayCase.Name,
							Cause: fmt.Errorf("baseline backend %q does not support required capabilities %v", backend.Name, unsupported)}
					}
					local.SkippedBackends[backend.Name] = unsupported
					snapResults[i].local = local
					return nil
				}
				snap, err := h.captureOnBackend(gCtx, replayCase, backend, normalizer, retry, timeout, maxSnapSize, i, &local)
				if err != nil {
					return err
				}
				snapResults[i].local = local
				if snap != nil {
					snapResults[i].snap = *snap
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return result, err
		}
		// Merge per-backend local results into shared result.
		for _, bc := range snapResults {
			for k, v := range bc.local.SkippedBackends {
				result.SkippedBackends[k] = v
			}
			if bc.local.Capabilities != nil {
				if result.Capabilities == nil {
					result.Capabilities = make(map[string]map[string]CapabilityDesc)
				}
				for k, v := range bc.local.Capabilities {
					result.Capabilities[k] = v
				}
			}
			if bc.local.PanicRecovered != nil {
				result.PanicRecovered = bc.local.PanicRecovered
				result.PanicStack = bc.local.PanicStack
			}
			if bc.local.Status == StatusSkip {
				result.Status = StatusSkip
				result.SkipReason = bc.local.SkipReason
			}
			for _, m := range bc.local.BackendMetrics {
				result.BackendMetrics = append(result.BackendMetrics, m)
			}
			if bc.snap.backendName != "" {
				snapshots = append(snapshots, bc.snap)
			}
		}
	}

	// If the context was cancelled during capture, preserve StatusSkip.
	if ctx.Err() != nil {
		result.Status = StatusSkip
		result.SkipReason = ctx.Err().Error()
		result.Duration = time.Since(start).String()
		return result, nil
	}

	if len(snapshots) < 2 {
		if len(result.SkippedBackends) > 0 {
			// Some backends were skipped — may or may not be able to compare.
			if len(snapshots) >= 1 {
				// At least one backend ran; can compare with remaining if any.
				result.Status = StatusMixed
			} else {
				result.Status = StatusInconclusive
			}
		} else {
			result.Status = StatusInconclusive
		}
		result.Duration = time.Since(start).String()
		return result, nil
	}

	// Collect backend metrics.
	for _, snap := range snapshots {
		result.BackendMetrics = append(result.BackendMetrics, snap.metric)
	}

	// Compute snapshot fingerprint from the baseline.
	if raw, err := json.Marshal(snapshots[0].snapshot); err == nil {
		result.SnapshotFingerprint = fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	}

	// Count sections.
	allCaps := h.Backends[0].Caps
	for _, cap := range []string{CapEvents, CapState, CapMemory, CapSummary, CapTrack} {
		if allCaps.Has(cap) {
			sectionsCompared++
		} else {
			sectionsSkipped++
		}
	}

	// Compare each backend against baseline in parallel for 3+ comparisons.
	baseline := snapshots[0]
	var diffs []Diff
	if len(snapshots) <= 3 {
		// Sequential for 2-3 snapshots — avoids goroutine overhead.
		for i := 1; i < len(snapshots); i++ {
			caseDiffs, err := Compare(
				replayCase.Name,
				baseline.backendName,
				snapshots[i].backendName,
				baseline.snapshot,
				snapshots[i].snapshot,
				allowed,
			)
			if err != nil {
				return result, &ReplayError{Kind: ErrComparison, Case: replayCase.Name, Cause: err}
			}
			diffs = append(diffs, caseDiffs...)
		}
	} else {
		// Parallel comparison for 4+ snapshots.
		type compareResult struct {
			diffs []Diff
			err   error
		}
		ch := make(chan compareResult, len(snapshots)-1)
		for i := 1; i < len(snapshots); i++ {
			go func(idx int) {
				caseDiffs, err := Compare(
					replayCase.Name,
					baseline.backendName,
					snapshots[idx].backendName,
					baseline.snapshot,
					snapshots[idx].snapshot,
					allowed,
				)
				ch <- compareResult{diffs: caseDiffs, err: err}
			}(i)
		}
		for i := 1; i < len(snapshots); i++ {
			cr := <-ch
			if cr.err != nil {
				return result, &ReplayError{Kind: ErrComparison, Case: replayCase.Name, Cause: cr.err}
			}
			diffs = append(diffs, cr.diffs...)
		}
	}

	result.Diffs = diffs
	result.Duration = time.Since(start).String()
	result.SectionsCompared = sectionsCompared
	result.SectionsSkipped = sectionsSkipped
	result.UnsupportedCaps = h.Backends[0].Caps.UnsupportedList()

	// Golden trace regression check.
	if h.GoldenDir != "" && !h.UpdateGolden {
		golden, found, err := LoadGoldenTrace(h.GoldenDir, replayCase.Name)
		if err != nil {
			h.logf("replay: %v", err)
		}
		if found && len(golden.Snapshots) > 0 {
			goldenDiffs, err := Compare(replayCase.Name, "golden", snapshots[0].backendName,
				golden.Snapshots[0], snapshots[0].snapshot, allowed)
			if err != nil {
				h.logf("replay: golden comparison error for %s: %v", replayCase.Name, err)
			} else if len(goldenDiffs) > 0 {
				h.logf("replay: GOLDEN REGRESSION in %s: %d diffs against golden trace",
					replayCase.Name, len(goldenDiffs))
				result.GoldenDiffs = goldenDiffs
			}
		}
	}
	if h.GoldenDir != "" && h.UpdateGolden {
		trace := &GoldenTrace{
			CaseName:  replayCase.Name,
			CreatedAt: time.Now(),
			Snapshots: []Snapshot{snapshots[0].snapshot},
		}
		if err := SaveGoldenTrace(h.GoldenDir, trace); err != nil {
			h.logf("replay: failed to update golden trace for %s: %v", replayCase.Name, err)
		}
	}

	// Classify status — if a panic was recovered, that takes precedence.
	if result.PanicRecovered != nil {
		result.Status = StatusFail
	} else {
		hasUnexpected := false
		for _, diff := range diffs {
			if !diff.Allowed {
				hasUnexpected = true
				break
			}
		}
		if hasUnexpected {
			result.Status = StatusFail
		} else if len(result.SkippedBackends) > 0 {
			// Some backends were skipped (capability gap) but no unexpected diffs.
			result.Status = StatusMixed
		} else {
			result.Status = StatusPass
		}
	}

	h.logf("replay: case=%s status=%s duration=%s diffs=%d skipped=%v",
		replayCase.Name, result.Status, result.Duration, len(diffs), len(result.SkippedBackends) > 0)

	return result, nil
}

// captureOnBackend executes the case and captures a snapshot on a single backend.
// Returns nil snapshot if the backend is skipped due to unsupported capabilities.
func (h Harness) captureOnBackend(
	ctx context.Context,
	replayCase Case,
	backend Backend,
	normalizer *Normalizer,
	retry RetryPolicy,
	timeout time.Duration,
	maxSnapSize int64,
	backendIdx int,
	result *CaseResult,
) (*capturedSnapshot, error) {
	caps := cloneCapabilities(backend.Caps)
	if result.Capabilities != nil {
		result.Capabilities[backend.Name] = caps
	}

	unsupported := unsupportedRequiredCapabilities(replayCase, backend)
	if len(unsupported) > 0 {
		if backendIdx == 0 {
			return nil, &ReplayError{Kind: ErrCaseValidation, Backend: backend.Name, Case: replayCase.Name,
				Cause: fmt.Errorf("baseline backend %q does not support required capabilities %v", backend.Name, unsupported)}
		}
		result.SkippedBackends[backend.Name] = unsupported
		return nil, nil
	}

	if ctx.Err() != nil {
		result.Status = StatusSkip
		result.SkipReason = ctx.Err().Error()
		return nil, nil
	}

	// Resolve per-backend retry policy: backend-level overrides harness-level.
	effectiveRetry := retry
	if backend.Retry != nil {
		effectiveRetry = *backend.Retry
	}

	// Resolve per-backend retryable checker.
	isRetryable := isTransientError
	if backend.IsRetryable != nil {
		isRetryable = backend.IsRetryable
	}

	var metric BackendMetric
	metric.Name = backend.Name

	// Rate limiting before run.
	if backend.RateLimit != nil {
		if err := backend.RateLimit(ctx); err != nil {
			return nil, &ReplayError{Kind: ErrBackendRun, Backend: backend.Name, Case: replayCase.Name, Cause: err}
		}
	}

	// Execute the case with panic recovery and timeout.
	if replayCase.Run != nil {
		runStart := time.Now()
		if err := h.executeRunWithProtection(ctx, replayCase, backend, timeout, result); err != nil {
			return nil, &ReplayError{Kind: ErrBackendRun, Backend: backend.Name, Case: replayCase.Name, Cause: err}
		}
		metric.RunDuration = time.Since(runStart)
	}

	// Rate limiting before capture.
	if backend.RateLimit != nil {
		if err := backend.RateLimit(ctx); err != nil {
			return nil, &ReplayError{Kind: ErrBackendCapture, Backend: backend.Name, Case: replayCase.Name, Cause: err}
		}
	}

	// Capture snapshot with retry for transient errors, recording retry metrics.
	captureStart := time.Now()
	var snapshot Snapshot
	var retryCount int
	var retryTotalDelay time.Duration
	if err := retryOperationWithMetrics(ctx, effectiveRetry, isRetryable, func(retryCtx context.Context) error {
		var captureErr error
		snapshot, captureErr = Capture(retryCtx, backend, CaptureOptions{
			NormalizerConfig:       normalizer.config,
			OrderEventsByTimestamp: replayCase.OrderEventsByTimestamp,
			UnorderedMemories:      replayCase.UnorderedMemories,
		}, normalizer)
		return captureErr
	}, &retryCount, &retryTotalDelay); err != nil {
		return nil, &ReplayError{Kind: ErrBackendCapture, Backend: backend.Name, Case: replayCase.Name, Cause: err}
	}
	metric.CaptureDuration = time.Since(captureStart)
	metric.RetryCount = retryCount
	metric.RetryTotalDelay = retryTotalDelay

	// CountOnly: strip event content, keep only counts for comparison.
	if replayCase.CountOnly {
		eventCount := len(snapshot.Events)
		snapshot.Events = nil
		snapshot.State = map[string]any{"event_count": eventCount}
	}

	// Snapshot size guard — serialize once and reuse for crash recovery.
	snapRaw, snapErr := json.Marshal(snapshot)
	if snapErr == nil {
		metric.SnapshotSize = int64(len(snapRaw))
		if metric.SnapshotSize > maxSnapSize {
			return nil, &ReplayError{Kind: ErrSnapshotTooLarge, Backend: backend.Name, Case: replayCase.Name,
				Cause: fmt.Errorf("snapshot exceeds size limit (%d > %d bytes)", metric.SnapshotSize, maxSnapSize)}
		}
	}
	metric.EventCount = len(snapshot.Events)

	h.logf("replay: case=%s backend=%s phase=capture duration=%s events=%d size=%d retries=%d",
		replayCase.Name, backend.Name, metric.CaptureDuration, metric.EventCount, metric.SnapshotSize, retryCount)

	// Save snapshot for crash recovery if SnapshotDir is set.
	// Reuse the already-marshaled bytes to avoid double serialization.
	if h.SnapshotDir != "" && snapErr == nil {
		snapPath := filepath.Join(h.SnapshotDir, fmt.Sprintf("%s_%s.json", replayCase.Name, backend.Name))
		if err := saveBytesAtomic(snapPath, snapRaw); err != nil {
			h.logf("replay: warning: failed to save snapshot %s: %v", snapPath, err)
		}
	}

	return &capturedSnapshot{
		backendName: backend.Name,
		snapshot:    snapshot,
		metric:      metric,
	}, nil
}

// backendsForCase creates a copy of each backend with a unique SessKey derived
// from the case name, preventing cross-case session key pollution when running
// cases in parallel.
func (h Harness) backendsForCase(c Case) []Backend {
	backends := make([]Backend, len(h.Backends))
	for i, b := range h.Backends {
		backends[i] = b
		if b.SessKey != nil {
			origKey := b.SessKey()
			caseKey := session.Key{
				AppName:   origKey.AppName,
				UserID:    origKey.UserID,
				SessionID: origKey.SessionID + "-" + c.Name,
			}
			backends[i].SessKey = func() session.Key { return caseKey }
		}
	}
	return backends
}

// RunSuite runs multiple cases with checkpoint/resume support, circuit breaker,
// and optional parallel execution. Completed case names and results are persisted
// in checkpointDir; on resume, those cases are skipped and their results loaded.
func (h Harness) RunSuite(ctx context.Context, cases []Case, checkpointDir string) (*Report, error) {
	if checkpointDir != "" {
		if err := os.MkdirAll(checkpointDir, 0o755); err != nil {
			return nil, fmt.Errorf("create checkpoint directory: %w", err)
		}
	}

	// Check for duplicate case names.
	caseNames := make(map[string]bool, len(cases))
	for _, c := range cases {
		if caseNames[c.Name] {
			return nil, fmt.Errorf("duplicate case name: %q", c.Name)
		}
		caseNames[c.Name] = true
	}

	suiteStart := time.Now()

	// Initialize circuit breaker.
	cbMax := h.CircuitBreakerMaxFailures
	if cbMax == 0 {
		cbMax = DefaultCircuitBreakerMaxFailures
	}
	var cb *circuitBreaker
	if cbMax > 0 {
		cb = newCircuitBreaker(cbMax)
	}

	// Load previously completed results from checkpoints.
	var results []CaseResult
	pendingCases := make([]Case, 0, len(cases))
	for _, c := range cases {
		if checkpointDir != "" {
			if loaded, ok := loadCheckpointResult(checkpointDir, c.Name); ok {
				results = append(results, loaded)
				h.logf("replay: loaded completed case %s from checkpoint", c.Name)
				continue
			}
		}
		pendingCases = append(pendingCases, c)
	}

	completed := len(results)
	total := len(cases)

	// Execute pending cases, optionally in parallel.
	parallelism := h.Parallelism
	if parallelism < 1 {
		parallelism = 1
	}

	if parallelism == 1 {
		// Sequential execution (original behavior).
		for _, c := range pendingCases {
			if ctx.Err() != nil {
				break
			}
			// Check circuit breaker.
			if cb != nil && h.allBackendsTripped(cb) {
				h.logf("replay: all backends tripped, stopping suite at case %s", c.Name)
				break
			}

			result, err := h.Run(ctx, c)
			if err != nil {
				h.recordCBFailure(cb, err)
				return nil, fmt.Errorf("run case %q: %w", c.Name, err)
			}
			h.recordCBSuccess(cb, result)

			results = append(results, result)
			completed++
			h.saveCheckpointAndProgress(checkpointDir, c.Name, result, completed, total)
		}
	} else {
		// Parallel execution with worker pool.
		suiteCtx, suiteCancel := context.WithCancel(ctx)
		defer suiteCancel()

		type indexedResult struct {
			index  int
			result CaseResult
			err    error
		}

		caseCh := make(chan int, len(pendingCases))
		resultCh := make(chan indexedResult, len(pendingCases))

		// Filter out cases where all backends are tripped before dispatching.
		runnableCases := make([]Case, 0, len(pendingCases))
		for _, c := range pendingCases {
			if cb != nil && h.allBackendsTripped(cb) {
				h.logf("replay: all backends tripped, skipping case %s", c.Name)
				continue
			}
			runnableCases = append(runnableCases, c)
		}

		// Dispatch case indices.
		for i := range runnableCases {
			caseCh <- i
		}
		close(caseCh)

		// Spawn workers.
		actualWorkers := parallelism
		if actualWorkers > len(runnableCases) {
			actualWorkers = len(runnableCases)
		}
		var wg sync.WaitGroup
		for w := 0; w < actualWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range caseCh {
					c := runnableCases[idx]
					mergedAllowed := mergeAllowedDiffs(h.Allowed, c.AllowedDiffs)
					caseSpec := c
					caseSpec.AllowedDiffs = nil
					// Use isolated backends instances per case to prevent
					// cross-case session key pollution in parallel execution.
					caseHarness := Harness{
						Backends:                  h.backendsForCase(c),
						Normalizer:                h.Normalizer,
						Allowed:                   mergedAllowed,
						Logf:                      h.Logf,
						Timeout:                   h.Timeout,
						Retry:                     h.Retry,
						MaxSnapshotSize:           h.MaxSnapshotSize,
						SnapshotDir:               h.SnapshotDir,
						GoldenDir:                 h.GoldenDir,
						UpdateGolden:              h.UpdateGolden,
						CircuitBreakerMaxFailures: h.CircuitBreakerMaxFailures,
						MaxMemoryUsagePct:         h.MaxMemoryUsagePct,
						Parallelism:               1,
						ProgressFunc:              h.ProgressFunc,
					}
					result, err := caseHarness.Run(suiteCtx, caseSpec)
					resultCh <- indexedResult{index: idx, result: result, err: err}
				}
			}()
		}

		// Close resultCh when all workers finish.
		go func() {
			wg.Wait()
			close(resultCh)
		}()

		// Collect results in dispatch order.
		orderedResults := make([]indexedResult, len(runnableCases))
		received := 0
		for ir := range resultCh {
			received++
			if ir.err != nil {
				h.recordCBFailure(cb, ir.err)
				// Cancel remaining work on first error.
				suiteCancel()
				wg.Wait()
				return nil, fmt.Errorf("run case %q: %w", runnableCases[ir.index].Name, ir.err)
			}
			h.recordCBSuccess(cb, ir.result)
			orderedResults[ir.index] = ir
			completed++
			h.saveCheckpointAndProgress(checkpointDir, runnableCases[ir.index].Name, ir.result, completed, total)
		}

		// Append in order.
		for _, ir := range orderedResults {
			if ir.result.Name != "" {
				results = append(results, ir.result)
			}
		}
	}

	// Sort results by original case order.
	resultMap := make(map[string]CaseResult, len(results))
	for _, r := range results {
		resultMap[r.Name] = r
	}
	sortedResults := make([]CaseResult, 0, len(cases))
	for _, c := range cases {
		if r, ok := resultMap[c.Name]; ok {
			sortedResults = append(sortedResults, r)
		}
	}

	backendNames := make([]string, len(h.Backends))
	for i, b := range h.Backends {
		backendNames[i] = b.Name
	}
	report := GenerateReport(sortedResults, backendNames)
	report.Summary.SuiteDuration = time.Since(suiteStart)

	// Accumulate retry metrics.
	for _, r := range sortedResults {
		for _, m := range r.BackendMetrics {
			report.Summary.TotalRetries += m.RetryCount
		}
	}

	return report, nil
}

// allBackendsTripped checks if all backends have tripped the circuit breaker.
func (h Harness) allBackendsTripped(cb *circuitBreaker) bool {
	if cb == nil {
		return false
	}
	for _, b := range h.Backends {
		if !cb.isTripped(b.Name) {
			return false
		}
	}
	return true
}

// recordCBFailure records a failure in the circuit breaker if applicable.
func (h Harness) recordCBFailure(cb *circuitBreaker, err error) {
	if cb == nil {
		return
	}
	var replayErr *ReplayError
	if errors.As(err, &replayErr) && replayErr.Backend != "" {
		cb.recordFailure(replayErr.Backend)
	}
}

// recordCBSuccess records success in the circuit breaker for participating backends.
func (h Harness) recordCBSuccess(cb *circuitBreaker, result CaseResult) {
	if cb == nil || result.Status == StatusInconclusive {
		return
	}
	for _, b := range h.Backends {
		if _, skipped := result.SkippedBackends[b.Name]; !skipped {
			cb.recordSuccess(b.Name)
		}
	}
}

// saveCheckpointAndProgress saves checkpoint and calls progress callback.
func (h Harness) saveCheckpointAndProgress(checkpointDir, caseName string, result CaseResult, completed, total int) {
	if checkpointDir != "" {
		if cpErr := saveCheckpointResult(checkpointDir, caseName, result); cpErr != nil {
			h.logf("replay: failed to save checkpoint for %s: %v", caseName, cpErr)
		}
	}
	if h.ProgressFunc != nil {
		h.ProgressFunc(completed, total, result)
	}
}

// executeRunWithProtection runs a Case.Run function with panic recovery and timeout.
// The goroutine communicates its outcome via a buffered channel so that on timeout
// the function can return without the goroutine racing to write to the shared result.
func (h Harness) executeRunWithProtection(
	ctx context.Context,
	replayCase Case,
	backend Backend,
	timeout time.Duration,
	result *CaseResult,
) error {
	caseCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type runOutcome struct {
		err            error
		panicRecovered any
		panicStack     string
	}
	outcomeCh := make(chan runOutcome, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				outcomeCh <- runOutcome{panicRecovered: r, panicStack: string(debug.Stack())}
			}
		}()
		outcomeCh <- runOutcome{err: replayCase.Run(caseCtx, backend)}
	}()

	select {
	case outcome := <-outcomeCh:
		if outcome.panicRecovered != nil {
			result.PanicRecovered = outcome.panicRecovered
			result.PanicStack = outcome.panicStack
			return nil
		}
		if outcome.err != nil {
			return fmt.Errorf("run on %s: %w", backend.Name, outcome.err)
		}
	case <-caseCtx.Done():
		// Context cancelled or timed out. The goroutine may still be running,
		// but it writes to the buffered outcomeCh so there is no data race
		// on the shared result after we return.
		if caseCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("run on %s: timed out after %s", backend.Name, timeout)
		}
		return fmt.Errorf("run on %s: %w", backend.Name, caseCtx.Err())
	}
	return nil
}

// Capture loads and normalizes the replay-visible state of one backend.
func Capture(
	ctx context.Context,
	backend Backend,
	opts CaptureOptions,
	normalizer *Normalizer,
) (Snapshot, error) {
	sess, memories, err := loadBackend(ctx, backend)
	if err != nil {
		return Snapshot{}, err
	}

	// Load scoped states if not already provided in opts.
	if opts.AppState == nil || opts.UserState == nil {
		appState, userState := loadScopedStates(ctx, backend)
		if opts.AppState == nil {
			opts.AppState = appState
		}
		if opts.UserState == nil {
			opts.UserState = userState
		}
	}

	if normalizer == nil {
		normalizer = NewNormalizer(opts.NormalizerConfig)
	}

	return normalizer.Normalize(sess, memories, backend.Caps, opts)
}

// Supports reports whether a backend advertises one capability.
func Supports(backend Backend, capability string) bool {
	return backend.Caps.Has(capability)
}

func loadBackend(ctx context.Context, backend Backend) (*session.Session, []*memory.Entry, error) {
	if backend.Load != nil {
		return backend.Load(ctx, backend)
	}
	var key session.Key
	if backend.SessKey != nil {
		key = backend.SessKey()
	}
	sess, err := backend.Sess.GetSession(ctx, key)
	if err != nil {
		return nil, nil, fmt.Errorf("GetSession on %s: %w", backend.Name, err)
	}
	// A nil session is acceptable when only memory capabilities are being tested.
	var memories []*memory.Entry
	if backend.Mem != nil && backend.Caps.Has(CapMemory) {
		memories, err = backend.Mem.ReadMemories(ctx, memory.UserKey{
			AppName: key.AppName,
			UserID:  key.UserID,
		}, 0)
		if err != nil {
			return nil, nil, fmt.Errorf("ReadMemories on %s: %w", backend.Name, err)
		}
	}
	return sess, memories, nil
}

// loadScopedStates reads AppState and UserState from a session service.
func loadScopedStates(ctx context.Context, backend Backend) (appState, userState session.StateMap) {
	var key session.Key
	if backend.SessKey != nil {
		key = backend.SessKey()
	}
	if key.AppName != "" {
		if states, err := backend.Sess.ListAppStates(ctx, key.AppName); err == nil {
			appState = states
		}
	}
	if key.AppName != "" && key.UserID != "" {
		userKey := session.UserKey{AppName: key.AppName, UserID: key.UserID}
		if states, err := backend.Sess.ListUserStates(ctx, userKey); err == nil {
			userState = states
		}
	}
	return appState, userState
}

// --- Retry ---

// retryOperation retries an operation with exponential backoff and jitter
// for transient errors only. Kept for backward compatibility.
func retryOperation(ctx context.Context, policy RetryPolicy, fn func(ctx context.Context) error) error {
	return retryOperationWithMetrics(ctx, policy, isTransientError, fn, nil, nil)
}

// retryOperationWithMetrics retries an operation with exponential backoff and jitter,
// using a custom isRetryable checker and recording retry metrics.
func retryOperationWithMetrics(
	ctx context.Context,
	policy RetryPolicy,
	isRetryable func(error) bool,
	fn func(ctx context.Context) error,
	retryCount *int,
	retryTotalDelay *time.Duration,
) error {
	if policy.MaxAttempts <= 1 {
		return fn(ctx)
	}
	var lastErr error
	attempts := 0
	var totalDelay time.Duration
	for attempt := 0; attempt < policy.MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lastErr = fn(ctx)
		if lastErr == nil {
			if retryCount != nil {
				*retryCount = attempts
			}
			if retryTotalDelay != nil {
				*retryTotalDelay = totalDelay
			}
			return nil
		}
		if !isRetryable(lastErr) {
			if retryCount != nil {
				*retryCount = attempts
			}
			if retryTotalDelay != nil {
				*retryTotalDelay = totalDelay
			}
			return lastErr
		}
		attempts++
		if attempt < policy.MaxAttempts-1 {
			delay := backoffDuration(policy, attempt)
			totalDelay += delay
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	if retryCount != nil {
		*retryCount = attempts
	}
	if retryTotalDelay != nil {
		*retryTotalDelay = totalDelay
	}
	return lastErr
}

// isTransientError reports whether an error is likely transient and worth retrying.
func isTransientError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := err.Error()
	// SQL driver transient errors.
	if strings.Contains(msg, "driver: bad connection") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "temporary") {
		return true
	}
	// Redis transient errors.
	if strings.Contains(msg, "CONNPOOL") ||
		strings.Contains(msg, "connection pool") {
		return true
	}
	return false
}

// backoffDuration computes the delay for a given attempt with optional jitter.
// Uses full-jitter algorithm (random value between 0 and delay) for better
// thundering-herd avoidance. Guards against tiny delays to prevent panic.
func backoffDuration(policy RetryPolicy, attempt int) time.Duration {
	delay := time.Duration(float64(policy.InitialDelay) * pow(policy.BackoffFactor, attempt))
	if delay > policy.MaxDelay {
		delay = policy.MaxDelay
	}
	if policy.Jitter && delay >= 2*time.Millisecond {
		jitter := time.Duration(rand.Int63n(int64(delay)))
		delay = jitter
	}
	return delay
}

func pow(base float64, exp int) float64 {
	result := 1.0
	for i := 0; i < exp; i++ {
		result *= base
	}
	return result
}

// --- Memory Pressure Guard ---

func memoryPressureCheck(maxUsagePct float64) error {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	if m.HeapSys > 0 {
		usage := float64(m.HeapInuse) / float64(m.HeapSys)
		if usage > maxUsagePct {
			return fmt.Errorf("memory pressure too high: %.1f%% heap usage (%d/%d bytes)",
				usage*100, m.HeapInuse, m.HeapSys)
		}
	}
	return nil
}

// --- Snapshot Crash Recovery ---

// saveBytesAtomic writes raw bytes atomically via .tmp + rename.
func saveBytesAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// --- Checkpoint ---

func checkpointExists(dir, caseName string) bool {
	path := filepath.Join(dir, caseName+".done")
	_, err := os.Stat(path)
	return err == nil
}

func saveCheckpoint(dir, caseName string) error {
	path := filepath.Join(dir, caseName+".done")
	return os.WriteFile(path, []byte(time.Now().Format(time.RFC3339)), 0o644)
}

// saveCheckpointResult atomically saves a CaseResult for crash recovery.
// On resume, loadCheckpointResult can restore the result instead of re-running.
func saveCheckpointResult(dir, caseName string, result CaseResult) error {
	b, err := json.Marshal(result)
	if err != nil {
		// Fall back to simple done-marker on marshal failure.
		return saveCheckpoint(dir, caseName)
	}
	path := filepath.Join(dir, caseName+".result.json")
	return saveBytesAtomic(path, b)
}

// loadCheckpointResult loads a previously saved CaseResult.
// Returns (result, true) if found, (zero, false) otherwise.
func loadCheckpointResult(dir, caseName string) (CaseResult, bool) {
	// Try the new result format first.
	path := filepath.Join(dir, caseName+".result.json")
	if data, err := os.ReadFile(path); err == nil {
		var result CaseResult
		if err := json.Unmarshal(data, &result); err == nil && result.Name != "" {
			return result, true
		}
	}
	// Fall back to old done-marker format (no result data).
	if checkpointExists(dir, caseName) {
		return CaseResult{Name: caseName, Status: StatusPass}, true
	}
	return CaseResult{}, false
}

// --- Report I/O ---

// createTempFile is the function used by WriteReport to create a temp file.
// It can be overridden in tests to inject failures.
var createTempFile = os.CreateTemp

// WriteReport atomically writes an indented JSON report alongside a SHA-256
// checksum sidecar file. The report file is valid JSON; the checksum is stored
// in a separate .sha256 file with the same base name.
// Uses fsync for durability before atomic rename.
func WriteReport(path string, report Report) error {
	if path == "" {
		return &ReplayError{Kind: ErrReportWrite, Cause: fmt.Errorf("report path must not be empty")}
	}
	if report.Version == "" {
		report.Version = "v2"
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return &ReplayError{Kind: ErrReportWrite, Cause: fmt.Errorf("marshal replay report: %w", err)}
	}
	// Compute checksum over the raw JSON bytes.
	// Invariant: ReadReportWithVerify must recompute on the same JSON bytes.
	content := append(raw, '\n')
	checksum := sha256.Sum256(content)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return &ReplayError{Kind: ErrReportWrite, Cause: fmt.Errorf("create report directory: %w", err)}
	}

	// Write to temp file, fsync, then rename atomically.
	f, err := createTempFile(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return &ReplayError{Kind: ErrReportWrite, Cause: fmt.Errorf("create temp report file: %w", err)}
	}
	tempName := f.Name()

	cleanup := func() {
		os.Remove(tempName)
	}

	if _, err := f.Write(content); err != nil {
		f.Close()
		cleanup()
		return &ReplayError{Kind: ErrReportWrite, Cause: fmt.Errorf("write temp report %s: %w", tempName, err)}
	}
	// Fsync for durability before rename.
	if err := f.Sync(); err != nil {
		f.Close()
		cleanup()
		return &ReplayError{Kind: ErrReportWrite, Cause: fmt.Errorf("fsync temp report %s: %w", tempName, err)}
	}
	if err := f.Close(); err != nil {
		cleanup()
		return &ReplayError{Kind: ErrReportWrite, Cause: fmt.Errorf("close temp report %s: %w", tempName, err)}
	}
	if err := os.Rename(tempName, path); err != nil {
		cleanup()
		return &ReplayError{Kind: ErrReportWrite, Cause: fmt.Errorf("rename %s to %s: %w", tempName, path, err)}
	}

	// Write checksum sidecar file (same directory, .sha256 extension).
	sha256Path := path + ".sha256"
	sha256Content := fmt.Sprintf("%x  %s\n", checksum, filepath.Base(path))
	if err := os.WriteFile(sha256Path, []byte(sha256Content), 0o644); err != nil {
		// Non-fatal: the report itself is valid; log but don't fail.
		_ = err
	}

	return nil
}

// ReadReport reads a JSON report from disk.
func ReadReport(path string) (*Report, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read replay report: %w", err)
	}
	var report Report
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, fmt.Errorf("unmarshal replay report: %w", err)
	}
	return &report, nil
}

// ReadReportWithVerify reads a report, verifies the SHA-256 checksum sidecar,
// and checks the report version.
func ReadReportWithVerify(path string) (*Report, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read replay report: %w", err)
	}
	// Verify checksum from sidecar file if present.
	sha256Path := path + ".sha256"
	if sha256Raw, err := os.ReadFile(sha256Path); err == nil {
		// Sidecar format: "<hex_checksum>  <filename>\n"
		parts := strings.SplitN(strings.TrimSpace(string(sha256Raw)), "  ", 2)
		if len(parts) >= 1 {
			storedChecksum := parts[0]
			computed := fmt.Sprintf("%x", sha256.Sum256(raw))
			if computed != storedChecksum {
				return nil, fmt.Errorf("report checksum mismatch: computed %s, stored %s", computed, storedChecksum)
			}
		}
	}
	var report Report
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, fmt.Errorf("unmarshal replay report: %w", err)
	}
	// Version guard: reject unknown versions to prevent silent misinterpretation.
	if report.Version != "" && report.Version != "v2" {
		return nil, fmt.Errorf("unsupported report version %q (expected v2)", report.Version)
	}
	return &report, nil
}

// GenerateReport builds a Report from CaseResults.
func GenerateReport(results []CaseResult, backends []string) *Report {
	now := time.Now()
	hostname, _ := os.Hostname()
	runID := fmt.Sprintf("%s-%d-%s", now.Format("20060102-150405"), os.Getpid(), hostname)
	report := &Report{
		ReportID:    runID,
		Version:     "v2",
		RunID:       runID,
		GeneratedAt: &now,
		Backends:    backends,
		Cases:       results,
		Summary:     ReportSummary{},
	}
	for _, r := range results {
		report.Summary.TotalCases++
		switch r.Status {
		case StatusPass:
			report.Summary.PassedCases++
		case StatusFail:
			report.Summary.FailedCases++
		case StatusSkip:
			report.Summary.SkippedCases++
		case StatusInconclusive:
			report.Summary.InconclusiveCases++
		case StatusMixed:
			// Mixed = pass + skipped backends — count as passed for pass-rate.
			report.Summary.PassedCases++
		}
		for _, d := range r.Diffs {
			report.Summary.TotalDiffs++
			if d.Allowed {
				report.Summary.AllowedDiffs++
			}
			switch d.Severity {
			case SeverityCritical:
				report.Summary.CriticalDiffs++
			case SeverityMajor:
				report.Summary.MajorDiffs++
			case SeverityMinor:
				report.Summary.MinorDiffs++
			}
		}
	}
	return report
}

// --- Validation helpers ---

// mergeAllowedDiffs combines harness-level and case-level allowed diffs.
// Case-level rules are appended after harness-level rules.
func mergeAllowedDiffs(harness, caseLevel []AllowedDiff) []AllowedDiff {
	if len(harness) == 0 {
		return caseLevel
	}
	if len(caseLevel) == 0 {
		return harness
	}
	merged := make([]AllowedDiff, 0, len(harness)+len(caseLevel))
	merged = append(merged, harness...)
	merged = append(merged, caseLevel...)
	return merged
}

func validateCase(replayCase Case) error {
	if strings.TrimSpace(replayCase.Name) == "" {
		return fmt.Errorf("replay case requires a name")
	}
	if replayCase.Run == nil {
		// Ops and ParallelGroups are reserved for future declarative execution.
		// Currently only Run is supported; reject cases that rely on Ops alone.
		if len(replayCase.Ops) > 0 || len(replayCase.ParallelGroups) > 0 {
			return fmt.Errorf("replay case %q uses Ops/ParallelGroups without Run; declarative execution is not yet implemented, use Run instead", replayCase.Name)
		}
		return fmt.Errorf("replay case %q requires a Run function", replayCase.Name)
	}
	seen := make(map[string]struct{}, len(replayCase.RequiredCaps))
	for _, cap := range replayCase.RequiredCaps {
		if strings.TrimSpace(cap) == "" {
			return fmt.Errorf("replay case %q has an empty required capability", replayCase.Name)
		}
		if _, exists := seen[cap]; exists {
			return fmt.Errorf("replay case %q has duplicate required capability %q", replayCase.Name, cap)
		}
		seen[cap] = struct{}{}
	}
	return nil
}

func validateBackends(backends []Backend) error {
	names := make(map[string]struct{}, len(backends))
	for _, backend := range backends {
		if strings.TrimSpace(backend.Name) == "" {
			return fmt.Errorf("backend has an empty name")
		}
		if backend.Sess == nil {
			return fmt.Errorf("backend %q has nil session service", backend.Name)
		}
		if _, exists := names[backend.Name]; exists {
			return fmt.Errorf("duplicate backend name %q", backend.Name)
		}
		names[backend.Name] = struct{}{}
	}
	return nil
}

func validateRequiredCapabilities(replayCase Case, backends []Backend) error {
	// Only the baseline (first) backend must support all required capabilities.
	// Non-baseline backends can be missing capabilities — they will be skipped.
	if len(backends) == 0 {
		return nil
	}
	for _, name := range replayCase.RequiredCaps {
		if !backends[0].Caps.Has(name) {
			return fmt.Errorf(
				"baseline backend %q does not support required capability %q for replay case %q",
				backends[0].Name, name, replayCase.Name,
			)
		}
	}
	return nil
}

func unsupportedRequiredCapabilities(replayCase Case, backend Backend) []string {
	var unsupported []string
	for _, name := range replayCase.RequiredCaps {
		if !backend.Caps.Has(name) {
			unsupported = append(unsupported, name)
		}
	}
	return unsupported
}

func cloneCapabilities(caps Capabilities) map[string]CapabilityDesc {
	result := make(map[string]CapabilityDesc, len(caps))
	for name, desc := range caps {
		result[name] = desc
	}
	return result
}
