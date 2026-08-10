//go:build darwin

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ds "github.com/bmatcuk/doublestar/v4"
)

const (
	macosLogPath                         = "/usr/bin/log"
	macosSandboxDenialBufferSize         = 100
	macosDenialCloseTimeout              = time.Second
	macosDenialMonitorStopTimeoutDefault = 500 * time.Millisecond
)

var (
	macosDenyRe                  = regexp.MustCompile(`deny\([^)]+\)\s+([^\s]+)(?:\s+(.+))?`)
	denialCapsCacheMu            sync.Mutex
	denialCapsByMacOSVer         = map[string]DiagnosticsCapability{}
	randomHexFallbackSeq         atomic.Uint64
	errDiagnosticsClosed         = errors.New("sandbox denial diagnostics closed")
	macosVersionMu               sync.Mutex
	macosVersionCached           string
	macosVersionReady            bool
	macOSVersionCommand          = defaultMacOSVersionCommand
	startMacOSLogStreamMonitorFn = startMacOSLogStreamMonitor
)

func defaultMacOSVersionCommand(ctx context.Context) *exec.Cmd {
	return exec.CommandContext(ctx, "/usr/bin/sw_vers", "-productVersion")
}

type macosLogEntry struct {
	EventMessage string `json:"eventMessage"`
	Timestamp    string `json:"timestamp"`
}

type macosSandboxDenialEvent struct {
	denial Denial
	tagged bool
}

type macosDenialRing struct {
	mu      sync.Mutex
	events  []macosSandboxDenialEvent
	dropped uint64
}

type macosLogStreamMonitor struct {
	cancel context.CancelFunc
	done   chan struct{}
	ready  chan struct{}
	ring   *macosDenialRing
	// stopTimeout bounds how long stop waits for the log-stream goroutine.
	// Zero uses macosDenialMonitorStopTimeoutDefault.
	stopTimeout time.Duration
}

// macosDenialCollectGeneration is a read-only handle to the denial ring that
// was active when a diagnostics-enabled run began. It intentionally omits
// cancel/stop so collection cannot own or extend the monitor process lifetime.
type macosDenialCollectGeneration struct {
	ring *macosDenialRing
	done <-chan struct{}
}

type macosDenialCloseAttempt struct {
	done chan struct{}
	err  error
}

type macosDenialLifecycle uint8

const (
	macosDenialIdle macosDenialLifecycle = iota
	macosDenialStarting
	macosDenialRunning
	macosDenialDegraded
	macosDenialClosed
)

type macosDenialDiagnostics struct {
	mu            sync.RWMutex
	initGate      chan struct{}
	closedCh      chan struct{}
	closeOnce     sync.Once
	closeAttempt  *macosDenialCloseAttempt
	state         macosDenialLifecycle
	initCancel    context.CancelFunc
	filter        DenialFilter
	sessionSuffix string
	caps          DiagnosticsCapability
	monitorErr    error
	initMonitor   *macosLogStreamMonitor
	prodMonitor   *macosLogStreamMonitor
}

func (r *Runtime) initDenialDiagnosticsState() {
	r.macosDenialDiagnostics()
}

func (r *Runtime) macosDenialDiagnostics() *macosDenialDiagnostics {
	if d, ok := r.denials.(*macosDenialDiagnostics); ok && d != nil {
		d.mu.Lock()
		if d.sessionSuffix == "" {
			d.sessionSuffix = newMacOSSessionSuffix()
		}
		if d.initGate == nil {
			d.initGate = make(chan struct{}, 1)
			d.initGate <- struct{}{}
		}
		if d.closedCh == nil {
			d.closedCh = make(chan struct{})
		}
		d.mu.Unlock()
		return d
	}
	initGate := make(chan struct{}, 1)
	initGate <- struct{}{}
	d := &macosDenialDiagnostics{
		initGate:      initGate,
		closedCh:      make(chan struct{}),
		sessionSuffix: newMacOSSessionSuffix(),
	}
	r.denials = d
	return d
}

func (r *Runtime) setDenialFilter(filter DenialFilter) {
	d := r.macosDenialDiagnostics()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.filter = cloneDenialFilter(filter)
}

func (r *Runtime) diagnosticsCapabilityForPlatform() DiagnosticsCapability {
	if r.backend != BackendAuto && r.backend != BackendMacOSSandboxExec {
		return DiagnosticsCapability{}
	}
	if normalizeProfile(r.profile).enforcement() != enforcementManaged {
		return DiagnosticsCapability{}
	}
	d := r.macosDenialDiagnostics()
	d.mu.Lock()
	defer d.mu.Unlock()
	caps := d.caps
	caps.Supported = true
	if r.liveProdMonitorLocked(d) == nil {
		caps.EventStreamAvailable = false
		caps.StrongCorrelation = false
	}
	return caps
}

func (r *Runtime) newSandboxDenialRun(
	profile PermissionProfile,
) sandboxDenialRun {
	d := r.macosDenialDiagnostics()
	if profile.enforcement() != enforcementManaged {
		return sandboxDenialRun{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state == macosDenialClosed {
		return sandboxDenialRun{}
	}
	monitor := r.liveProdMonitorLocked(d)
	if monitor == nil || monitor.ring == nil {
		return sandboxDenialRun{}
	}
	var done <-chan struct{}
	if monitor.done != nil {
		done = monitor.done
	}
	return sandboxDenialRun{
		enabled:              true,
		runTag:               newMacOSSandboxDenialRunTag(d.sessionSuffix),
		droppedAtStart:       monitor.ring.dropCount(),
		defaultDenyTaggable:  d.caps.DefaultDenyTaggable,
		explicitDenyTaggable: d.caps.ExplicitDenyTaggable,
		collectGeneration: &macosDenialCollectGeneration{
			ring: monitor.ring,
			done: done,
		},
	}
}

func (r *Runtime) sandboxDenialRunForCollecting(
	profile PermissionProfile,
) sandboxDenialRun {
	run := r.newSandboxDenialRun(profile)
	if !run.enabled || run.collectGeneration == nil {
		return sandboxDenialRun{}
	}
	return run
}

func (r *Runtime) sandboxDenialCollectingReady() bool {
	d := r.macosDenialDiagnostics()
	d.mu.Lock()
	defer d.mu.Unlock()
	return r.liveProdMonitorLocked(d) != nil
}

func (r *Runtime) liveProdMonitorLocked(
	d *macosDenialDiagnostics,
) *macosLogStreamMonitor {
	if d.state == macosDenialClosed {
		return nil
	}
	m := d.prodMonitor
	if m == nil || !d.caps.EventStreamAvailable {
		return nil
	}
	if m.done == nil {
		return m
	}
	select {
	case <-m.done:
		d.prodMonitor = nil
		d.caps.EventStreamAvailable = false
		d.caps.StrongCorrelation = false
		d.state = macosDenialIdle
		return nil
	default:
		d.state = macosDenialRunning
		return m
	}
}

func (r *Runtime) closeDenialDiagnostics() (retErr error) {
	d := r.macosDenialDiagnostics()
	d.mu.Lock()
	d.state = macosDenialClosed
	d.closeOnce.Do(func() {
		close(d.closedCh)
	})
	cancelInit := d.initCancel
	d.caps.EventStreamAvailable = false
	d.caps.StrongCorrelation = false
	if attempt := d.closeAttempt; attempt != nil {
		d.mu.Unlock()
		<-attempt.done
		return attempt.err
	}
	attempt := &macosDenialCloseAttempt{done: make(chan struct{})}
	d.closeAttempt = attempt
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		attempt.err = retErr
		if d.closeAttempt == attempt {
			d.closeAttempt = nil
		}
		close(attempt.done)
		d.mu.Unlock()
	}()
	if cancelInit != nil {
		cancelInit()
	}

	timer := time.NewTimer(macosDenialCloseTimeout)
	defer timer.Stop()
	select {
	case <-d.initGate:
	case <-timer.C:
		return errors.New("timed out waiting for sandbox denial diagnostics initialization")
	}
	defer d.unlockInit()

	d.mu.Lock()
	initMonitor := d.initMonitor
	prodMonitor := d.prodMonitor
	d.mu.Unlock()
	var initStopErr error
	if initMonitor != nil {
		initStopErr = initMonitor.stop()
	}
	var prodStopErr error
	if prodMonitor != nil {
		if prodMonitor == initMonitor {
			prodStopErr = initStopErr
		} else {
			prodStopErr = prodMonitor.stop()
		}
	}
	stopErr := errors.Join(initStopErr, prodStopErr)
	d.mu.Lock()
	defer d.mu.Unlock()
	if initStopErr == nil && d.initMonitor == initMonitor {
		d.initMonitor = nil
	}
	if prodStopErr == nil && d.prodMonitor == prodMonitor {
		d.prodMonitor = nil
	}
	if stopErr != nil {
		d.monitorErr = stopErr
		return stopErr
	}
	d.monitorErr = nil
	return nil
}

func (r *Runtime) ensureDenialMonitor(ctx context.Context) error {
	d := r.macosDenialDiagnostics()
	if err := d.lockInit(ctx); err != nil {
		if errors.Is(err, errDiagnosticsClosed) {
			return nil
		}
		return err
	}
	defer d.unlockInit()

	d.mu.Lock()
	if d.state == macosDenialClosed {
		d.mu.Unlock()
		return nil
	}
	if d.initMonitor != nil {
		monitor := d.initMonitor
		d.mu.Unlock()
		stopErr := monitor.stop()
		d.mu.Lock()
		if stopErr != nil {
			d.monitorErr = stopErr
			d.mu.Unlock()
			return stopErr
		}
		if d.initMonitor == monitor {
			d.initMonitor = nil
		}
		d.monitorErr = nil
		if d.state == macosDenialClosed {
			d.mu.Unlock()
			return nil
		}
	}
	if r.liveProdMonitorLocked(d) != nil {
		d.mu.Unlock()
		return nil
	}
	if d.state == macosDenialDegraded {
		d.mu.Unlock()
		return nil
	}
	if d.prodMonitor != nil {
		err := d.monitorErr
		d.mu.Unlock()
		if err != nil {
			return err
		}
		return errors.New("previous log stream monitor has not stopped")
	}
	initCtx, cancel := context.WithCancel(ctx)
	d.state = macosDenialStarting
	d.initCancel = cancel
	d.mu.Unlock()

	err := r.initDenialMonitor(initCtx, d)
	cancel()
	d.mu.Lock()
	d.initCancel = nil
	if d.state != macosDenialClosed {
		d.monitorErr = err
		if err != nil {
			d.state = macosDenialIdle
		} else if d.prodMonitor != nil {
			d.state = macosDenialRunning
		} else if d.state == macosDenialStarting {
			// activateDenialMonitor should have already chosen Idle or
			// Degraded. Keep retryable Idle if state was left unchanged.
			d.state = macosDenialIdle
		}
	}
	d.mu.Unlock()
	return err
}

func (d *macosDenialDiagnostics) lockInit(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-d.closedCh:
		return errDiagnosticsClosed
	case <-d.initGate:
		return nil
	}
}

func (d *macosDenialDiagnostics) unlockInit() {
	d.initGate <- struct{}{}
}

func (r *Runtime) initDenialMonitor(
	ctx context.Context,
	d *macosDenialDiagnostics,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cached, ok, err := loadCachedDiagnosticsCaps(ctx); err != nil {
		return err
	} else if ok {
		caps := cached
		caps.ProbeCompleted = true
		return r.activateDenialMonitor(ctx, d, caps)
	}

	caps, err := r.probeDiagnosticsCapabilities(ctx, d)
	if err != nil {
		return err
	}
	if caps.ProbeCompleted {
		if err := storeCachedDiagnosticsCaps(ctx, caps); err != nil {
			return err
		}
	}
	return r.activateDenialMonitor(ctx, d, caps)
}

func (r *Runtime) activateDenialMonitor(
	ctx context.Context,
	d *macosDenialDiagnostics,
	caps DiagnosticsCapability,
) error {
	if !caps.EventStreamAvailable {
		d.mu.Lock()
		if d.state != macosDenialClosed {
			d.caps = caps
			if caps.ProbeCompleted {
				// Completed probe with no event stream is a confirmed host
				// limitation for this runtime.
				d.state = macosDenialDegraded
			} else {
				// Incomplete probes stay retryable on later diagnostics runs.
				d.state = macosDenialIdle
			}
		}
		d.mu.Unlock()
		return nil
	}
	d.mu.RLock()
	sessionSuffix := d.sessionSuffix
	d.mu.RUnlock()
	monitor, err := startMacOSLogStreamMonitorFn(ctx, sessionSuffix)
	if err != nil {
		if monitor != nil {
			retainInitializationMonitor(d, monitor, err)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		// Transient production-monitor startup failure: keep probe caps and
		// remain retryable so a later diagnostics request can start again.
		d.mu.Lock()
		if d.state != macosDenialClosed {
			d.caps = caps
			d.state = macosDenialIdle
		}
		d.mu.Unlock()
		return nil
	}
	return installDenialMonitor(d, caps, monitor)
}

func retainInitializationMonitor(
	d *macosDenialDiagnostics,
	monitor *macosLogStreamMonitor,
	err error,
) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.initMonitor = monitor
	d.monitorErr = err
}

func releaseInitializationMonitor(
	d *macosDenialDiagnostics,
	monitor *macosLogStreamMonitor,
) error {
	stopErr := monitor.stop()
	d.mu.Lock()
	defer d.mu.Unlock()
	if stopErr != nil {
		d.initMonitor = monitor
		d.monitorErr = stopErr
		return stopErr
	}
	if d.initMonitor == monitor {
		d.initMonitor = nil
	}
	d.monitorErr = nil
	return nil
}

func installDenialMonitor(
	d *macosDenialDiagnostics,
	caps DiagnosticsCapability,
	monitor *macosLogStreamMonitor,
) error {
	d.mu.Lock()
	if d.state == macosDenialClosed {
		d.prodMonitor = monitor
		d.monitorErr = nil
		d.mu.Unlock()
		stopErr := monitor.stop()
		d.mu.Lock()
		if stopErr != nil {
			d.monitorErr = stopErr
			d.mu.Unlock()
			return stopErr
		}
		if d.prodMonitor == monitor {
			d.prodMonitor = nil
		}
		d.monitorErr = nil
		d.mu.Unlock()
		return nil
	}
	d.caps = caps
	d.prodMonitor = monitor
	d.state = macosDenialRunning
	d.mu.Unlock()
	return nil
}

// collectSandboxDenials waits the full settleTimeout (default 300ms) on the
// run-start ring generation, then snapshots tagged denials for this run.
// Context cancellation ends the settle wait early and still returns whatever
// events are already buffered.
func (r *Runtime) collectSandboxDenials(
	ctx context.Context,
	run sandboxDenialRun,
	cmd string,
	settleTimeout time.Duration,
) ([]Denial, bool) {
	gen, _ := run.collectGeneration.(*macosDenialCollectGeneration)
	d := r.macosDenialDiagnostics()
	d.mu.Lock()
	filter := cloneDenialFilter(d.filter)
	// Reap a dead current production monitor so later runs can restart it,
	// without switching this collection away from the run-start ring.
	_ = r.liveProdMonitorLocked(d)
	d.mu.Unlock()
	if run.runTag == "" || gen == nil || gen.ring == nil {
		return nil, false
	}
	_ = gen.ring.waitForRunTagSettle(ctx, run.runTag, settleTimeout)
	events, truncated := gen.ring.snapshotSince(run.droppedAtStart)
	var tagged []Denial
	for _, event := range events {
		if !event.tagged || !containsExactSandboxTag(event.denial.Raw, run.runTag) {
			continue
		}
		tagged = append(tagged, event.denial)
	}
	return applyMacOSSandboxDenialFilters(tagged, cmd, filter), truncated
}

func (r *Runtime) probeDiagnosticsCapabilities(
	ctx context.Context,
	d *macosDenialDiagnostics,
) (DiagnosticsCapability, error) {
	caps := DiagnosticsCapability{
		Supported: true,
	}
	if err := ctx.Err(); err != nil {
		return caps, err
	}
	seatbelt, err := r.macosPreflightContext(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return caps, ctxErr
		}
		return caps, nil
	}

	probe := func() (DiagnosticsCapability, bool, error) {
		return r.runDiagnosticsCapabilityProbe(ctx, seatbelt, d)
	}

	probed, ok, err := probe()
	if err != nil {
		return probed, err
	}
	if ok {
		return probed, nil
	}
	if err := waitCtx(ctx, 200*time.Millisecond); err != nil {
		return probed, err
	}
	probed, ok, err = probe()
	if err != nil {
		return probed, err
	}
	if ok {
		return probed, nil
	}
	return probed, nil
}

func (r *Runtime) runDiagnosticsCapabilityProbe(
	ctx context.Context,
	seatbelt string,
	d *macosDenialDiagnostics,
) (
	caps DiagnosticsCapability,
	ok bool,
	retErr error,
) {
	caps = DiagnosticsCapability{
		Supported: true,
	}
	if err := ctx.Err(); err != nil {
		return caps, false, err
	}
	probeSuffix := newMacOSProbeSuffix()
	probeTag := "TRPC_RUN_PROBE_D_" + randomHex(8) + probeSuffix
	explicitTag := "TRPC_RUN_PROBE_E_" + randomHex(8) + probeSuffix
	probeDir, err := createMacOSProbeDir()
	if err != nil {
		return incompleteDiagnosticsProbe(ctx, caps)
	}
	// Clean the lexical MkdirTemp path so a replaced symlink cannot redirect
	// RemoveAll onto an outside directory.
	defer os.RemoveAll(probeDir)
	probeDefaultLexical := filepath.Join(probeDir, "default_target")
	probeExplicitLexical := filepath.Join(probeDir, "explicit_target")
	for _, path := range []string{probeDefaultLexical, probeExplicitLexical} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			return incompleteDiagnosticsProbe(ctx, caps)
		}
	}
	// Seatbelt/log matching needs canonical paths; cleanup above stays lexical.
	probeDefaultPath := canonicalizeProbePath(probeDefaultLexical)
	probeExplicitPath := canonicalizeProbePath(probeExplicitLexical)

	monitor, err := startMacOSProbeLogStreamMonitor(
		ctx,
		probeSuffix,
		probeDefaultPath,
		probeExplicitPath,
	)
	if err != nil {
		if monitor != nil {
			retainInitializationMonitor(d, monitor, err)
		}
		return incompleteDiagnosticsProbe(ctx, caps)
	}
	defer func() {
		if stopErr := releaseInitializationMonitor(d, monitor); stopErr != nil {
			caps.EventStreamAvailable = false
			caps.ProbeCompleted = false
			ok = false
			if retErr == nil {
				retErr = stopErr
			}
		}
	}()
	if err := waitCtx(ctx, 100*time.Millisecond); err != nil {
		return caps, false, err
	}

	policy := macosDiagnosticsProbePolicy(probeTag, explicitTag, probeExplicitPath)
	profilePath, err := writeMacOSSeatbeltProfile(policy)
	if err != nil {
		return incompleteDiagnosticsProbe(ctx, caps)
	}
	defer os.Remove(profilePath)

	probeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	for _, spec := range []struct {
		args []string
	}{
		{args: []string{"/bin/cat", probeDefaultPath}},
		{args: []string{"/bin/cat", probeExplicitPath}},
	} {
		if err := probeCtx.Err(); err != nil {
			return incompleteDiagnosticsProbe(ctx, caps)
		}
		cmdArgs := append([]string{"-f", profilePath, "--"}, spec.args...)
		cmd := exec.CommandContext(probeCtx, seatbelt, cmdArgs...)
		cmd.Dir = probeDir
		cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "LANG=C"}
		if err := cmd.Run(); err == nil {
			return incompleteDiagnosticsProbe(ctx, caps)
		}
		if err := waitCtx(probeCtx, 100*time.Millisecond); err != nil {
			return incompleteDiagnosticsProbe(ctx, caps)
		}
	}

	defaultExp := probeExpectation{
		Tag:       probeTag,
		Operation: "file-read*",
		Target:    probeDefaultPath,
	}
	explicitExp := probeExpectation{
		Tag:       explicitTag,
		Operation: "file-read*",
		Target:    probeExplicitPath,
	}
	// Wait independently for each deny form. Only probe-target denials count as
	// evidence; ambient Sandbox noise cannot complete or cache the probe.
	defaultRes, explicitRes, err := waitForProbeFormCapabilities(
		ctx,
		monitor.ring,
		defaultExp,
		explicitExp,
		sandboxDenialProbeTimeout,
	)
	if err != nil {
		return caps, false, err
	}
	if !defaultRes.targetObserved || !explicitRes.targetObserved {
		return incompleteDiagnosticsProbe(ctx, caps)
	}
	caps = diagnosticsCapabilityFromProbe(
		true,
		defaultRes.tagMatched,
		explicitRes.tagMatched,
	)
	return caps, true, nil
}

// startMacOSProbeLogStreamMonitor starts a temporary probe monitor scoped to
// this probe's suffix and target paths. Ambient Sandbox: traffic is excluded so
// it cannot establish EventStreamAvailable or overflow the probe ring.
func startMacOSProbeLogStreamMonitor(
	ctx context.Context,
	suffix, defaultPath, explicitPath string,
) (*macosLogStreamMonitor, error) {
	return startMacOSLogStreamMonitorWithPredicate(
		ctx,
		macosProbeLogStreamPredicate(suffix, defaultPath, explicitPath),
	)
}

func macosProbeLogStreamPredicate(suffix, defaultPath, explicitPath string) string {
	return fmt.Sprintf(
		`eventMessage ENDSWITH %q OR eventMessage CONTAINS %q OR eventMessage CONTAINS %q`,
		suffix,
		defaultPath,
		explicitPath,
	)
}

type probeFormResult struct {
	targetObserved bool
	tagMatched     bool
}

func waitForProbeFormCapabilities(
	ctx context.Context,
	ring *macosDenialRing,
	defaultExp, explicitExp probeExpectation,
	timeout time.Duration,
) (defaultRes, explicitRes probeFormResult, err error) {
	if ring == nil {
		return probeFormResult{}, probeFormResult{}, nil
	}
	type result struct {
		form probeFormResult
		err  error
	}
	defaultCh := make(chan result, 1)
	explicitCh := make(chan result, 1)
	go func() {
		form, waitErr := ring.waitForProbeForm(ctx, defaultExp, timeout)
		defaultCh <- result{form: form, err: waitErr}
	}()
	go func() {
		form, waitErr := ring.waitForProbeForm(ctx, explicitExp, timeout)
		explicitCh <- result{form: form, err: waitErr}
	}()
	defaultOut := <-defaultCh
	explicitOut := <-explicitCh
	if defaultOut.err != nil {
		return probeFormResult{}, probeFormResult{}, defaultOut.err
	}
	if explicitOut.err != nil {
		return probeFormResult{}, probeFormResult{}, explicitOut.err
	}
	return defaultOut.form, explicitOut.form, nil
}

func diagnosticsCapabilityFromProbe(
	streamAvailable bool,
	defaultMatched, explicitMatched bool,
) DiagnosticsCapability {
	caps := DiagnosticsCapability{Supported: true}
	if !streamAvailable {
		return caps
	}
	caps.EventStreamAvailable = true
	caps.ProbeCompleted = true
	caps.DefaultDenyTaggable = defaultMatched
	caps.ExplicitDenyTaggable = explicitMatched
	caps.StrongCorrelation = defaultMatched || explicitMatched
	return caps
}

func incompleteDiagnosticsProbe(
	ctx context.Context,
	caps DiagnosticsCapability,
) (DiagnosticsCapability, bool, error) {
	if err := ctx.Err(); err != nil {
		return caps, false, err
	}
	return caps, false, nil
}

type probeExpectation struct {
	Tag       string
	Operation string
	Target    string
}

func probeMatched(events []macosSandboxDenialEvent, exp probeExpectation) bool {
	for _, event := range events {
		if !containsExactSandboxTag(event.denial.Raw, exp.Tag) {
			continue
		}
		if !probeOperationMatches(event.denial.Operation, exp.Operation) {
			continue
		}
		if !probeTargetMatches(event.denial.Target, exp.Target) {
			continue
		}
		return true
	}
	return false
}

func probeTargetObserved(events []macosSandboxDenialEvent, exp probeExpectation) bool {
	for _, event := range events {
		if !probeOperationMatches(event.denial.Operation, exp.Operation) {
			continue
		}
		if !probeTargetMatches(event.denial.Target, exp.Target) {
			continue
		}
		return true
	}
	return false
}

func probeOperationMatches(got, want string) bool {
	if got == want {
		return true
	}
	if want != "file-read*" {
		return false
	}
	return strings.HasPrefix(got, "file-read") ||
		got == "file-test-existence" ||
		got == "file-map-executable"
}

func probeTargetMatches(logged, expected string) bool {
	if logged == expected {
		return true
	}
	loggedCanon, err := canonicalizeProbeTargetPath(logged)
	if err != nil {
		return false
	}
	expectedCanon, err := canonicalizeProbeTargetPath(expected)
	if err != nil {
		return false
	}
	return loggedCanon == expectedCanon
}

func canonicalizeProbeTargetPath(path string) (string, error) {
	parent, err := canonicalizeExistingPath(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

func macosDiagnosticsProbePolicy(probeTag, explicitTag, explicitPath string) string {
	base := strings.Replace(
		macosPreflightPolicy(),
		"(deny default)",
		fmt.Sprintf("(deny default (with message %s))", sbplString(probeTag)),
		1,
	)
	explicitRegex := "^" + regexp.QuoteMeta(filepath.ToSlash(explicitPath)) + "$"
	explicit := fmt.Sprintf(
		`(deny file-read* file-map-executable file-test-existence (regex #"%s") (with message %s))`,
		strings.ReplaceAll(explicitRegex, `"`, `\"`),
		sbplString(explicitTag),
	)
	return base + "\n\n" + explicit
}

func startMacOSLogStreamMonitor(
	ctx context.Context,
	suffix string,
) (*macosLogStreamMonitor, error) {
	return startMacOSLogStreamMonitorWithPredicate(
		ctx,
		fmt.Sprintf(`eventMessage ENDSWITH %q`, suffix),
	)
}

func startMacOSLogStreamMonitorWithPredicate(
	ctx context.Context,
	predicate string,
) (*macosLogStreamMonitor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	monitorCtx, cancel := context.WithCancel(context.Background())
	ring := &macosDenialRing{}
	monitor := &macosLogStreamMonitor{
		cancel: cancel,
		done:   make(chan struct{}),
		ready:  make(chan struct{}),
		ring:   ring,
	}
	cmd := exec.CommandContext(
		monitorCtx,
		macosLogPath,
		"stream",
		"--style", "ndjson",
		"--predicate", predicate,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	startErr := make(chan error, 1)
	go func() {
		defer close(monitor.done)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		sawLine := false
		for scanner.Scan() {
			if !sawLine {
				sawLine = true
				close(monitor.ready)
			}
			ring.addLine(scanner.Bytes(), "")
		}
		scanErr := scanner.Err()
		if scanErr != nil {
			// Log diagnostics are best-effort and must not fail command execution.
		}
		if !sawLine {
			if scanErr != nil {
				startErr <- scanErr
			} else {
				startErr <- errors.New("log stream exited before emitting output")
			}
			close(monitor.ready)
		}
		_ = cmd.Wait()
	}()
	readyTimer := time.NewTimer(sandboxDenialProbeTimeout)
	defer readyTimer.Stop()
	select {
	case <-monitor.ready:
		select {
		case err := <-startErr:
			cancel()
			<-monitor.done
			return nil, err
		default:
		}
	case <-ctx.Done():
		if stopErr := monitor.stop(); stopErr != nil {
			return monitor, errors.Join(ctx.Err(), stopErr)
		}
		return nil, ctx.Err()
	case <-readyTimer.C:
	}
	return monitor, nil
}

func (m *macosLogStreamMonitor) stop() error {
	if m == nil {
		return nil
	}
	if m.cancel != nil {
		m.cancel()
	}
	if m.done == nil {
		return nil
	}
	timeout := m.stopTimeout
	if timeout <= 0 {
		timeout = macosDenialMonitorStopTimeoutDefault
	}
	select {
	case <-m.done:
		return nil
	case <-time.After(timeout):
		return errors.New("timed out stopping macOS log stream monitor")
	}
}

func waitCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (ring *macosDenialRing) addLine(line []byte, runTag string) {
	if !bytes.Contains(line, []byte("Sandbox:")) ||
		!bytes.Contains(line, []byte("deny(")) {
		return
	}
	denial, tagged, ok := parseMacOSSandboxDenialLogLine(line, runTag)
	if !ok {
		return
	}
	ring.mu.Lock()
	defer ring.mu.Unlock()
	if len(ring.events) >= macosSandboxDenialBufferSize {
		copy(ring.events, ring.events[1:])
		ring.events[len(ring.events)-1] = macosSandboxDenialEvent{
			denial: denial,
			tagged: tagged,
		}
		ring.dropped++
		return
	}
	ring.events = append(ring.events, macosSandboxDenialEvent{
		denial: denial,
		tagged: tagged,
	})
}

func (ring *macosDenialRing) count() int {
	ring.mu.Lock()
	defer ring.mu.Unlock()
	return len(ring.events)
}

func (ring *macosDenialRing) snapshot() ([]macosSandboxDenialEvent, bool) {
	return ring.snapshotSince(0)
}

func (ring *macosDenialRing) snapshotSince(
	droppedAtStart uint64,
) ([]macosSandboxDenialEvent, bool) {
	ring.mu.Lock()
	out := make([]macosSandboxDenialEvent, len(ring.events))
	copy(out, ring.events)
	droppedNow := ring.dropped
	truncated := droppedNow > droppedAtStart
	ring.mu.Unlock()
	return out, truncated
}

func (ring *macosDenialRing) dropCount() uint64 {
	ring.mu.Lock()
	defer ring.mu.Unlock()
	return ring.dropped
}

// waitForSettle keeps the collection window open for the full bounded timeout.
// Production collection has no stronger completion signal than the timeout, so
// a short idle gap after the first event must not finalize the wait while later
// correlated denials may still arrive.
func (ring *macosDenialRing) waitForSettle(
	ctx context.Context,
	timeout time.Duration,
) error {
	_ = ring
	if timeout <= 0 {
		timeout = sandboxDenialSettleTimeout
	}
	return waitCtx(ctx, timeout)
}

// waitForRunTagSettle waits the full settle window for tagged production
// collection. Unlike probe form waits, there is no known completion signal other
// than the bounded timeout, so the wait never returns early after a short idle.
func (ring *macosDenialRing) waitForRunTagSettle(
	ctx context.Context,
	runTag string,
	timeout time.Duration,
) error {
	if runTag == "" {
		return ring.waitForSettle(ctx, timeout)
	}
	if timeout <= 0 {
		timeout = sandboxDenialSettleTimeout
	}
	return waitCtx(ctx, timeout)
}

// waitForProbeForm waits until the expected tag matches or the full timeout
// elapses. A target denial without the expected tag is not enough to finish
// early; the wait continues so a delayed tag can still arrive.
func (ring *macosDenialRing) waitForProbeForm(
	ctx context.Context,
	exp probeExpectation,
	timeout time.Duration,
) (probeFormResult, error) {
	if timeout <= 0 {
		timeout = sandboxDenialProbeTimeout
	}
	deadline := time.Now().Add(timeout)
	for {
		events, _ := ring.snapshot()
		if probeMatched(events, exp) {
			return probeFormResult{targetObserved: true, tagMatched: true}, nil
		}
		if err := ctx.Err(); err != nil {
			return probeFormResult{}, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return probeFormResult{
				targetObserved: probeTargetObserved(events, exp),
				tagMatched:     false,
			}, nil
		}
		sleep := 10 * time.Millisecond
		if sleep > remaining {
			sleep = remaining
		}
		if err := waitCtx(ctx, sleep); err != nil {
			return probeFormResult{}, err
		}
	}
}

func parseMacOSSandboxDenialLogLine(
	line []byte,
	runTag string,
) (Denial, bool, bool) {
	var entry macosLogEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return Denial{}, false, false
	}
	return parseMacOSSandboxDenialEvent(entry.EventMessage, entry.Timestamp, runTag)
}

func parseMacOSSandboxDenialEvent(
	eventMessage string,
	timestamp string,
	runTag string,
) (Denial, bool, bool) {
	if eventMessage == "" ||
		!strings.Contains(eventMessage, "Sandbox:") ||
		!strings.Contains(eventMessage, "deny(") {
		return Denial{}, false, false
	}
	tagged := strings.Contains(eventMessage, "TRPC_RUN_")
	if runTag != "" {
		tagged = containsExactSandboxTag(eventMessage, runTag)
	}
	idx := strings.Index(eventMessage, "Sandbox:")
	if idx < 0 {
		return Denial{}, false, false
	}
	raw := strings.TrimSpace(eventMessage[idx+len("Sandbox:"):])
	firstLine := raw
	if before, _, ok := strings.Cut(raw, "\n"); ok {
		firstLine = strings.TrimSpace(before)
	}
	denyMatch := macosDenyRe.FindStringSubmatch(firstLine)
	if len(denyMatch) < 2 {
		return Denial{}, false, false
	}
	target := ""
	if len(denyMatch) >= 3 {
		target = strings.TrimSpace(denyMatch[2])
	}
	return Denial{
		Operation: denyMatch[1],
		Target:    target,
		Raw:       raw,
		Timestamp: parseMacOSLogTimestamp(timestamp),
	}, tagged, true
}

func parseMacOSLogTimestamp(timestamp string) time.Time {
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999-0700",
		"2006-01-02 15:04:05.999999999-0700",
		time.RFC3339Nano,
	} {
		if ts, err := time.Parse(layout, timestamp); err == nil {
			return ts
		}
	}
	return time.Time{}
}

func applyMacOSSandboxDenialFilters(
	denials []Denial,
	cmd string,
	filter DenialFilter,
) []Denial {
	if len(denials) == 0 {
		return nil
	}
	out := make([]Denial, 0, len(denials))
	seen := map[string]bool{}
	for _, denial := range denials {
		if shouldFilterMacOSSandboxDenial(denial, cmd, filter) {
			continue
		}
		key := denial.Operation + "\x00" + denial.Target
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, denial)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneDenialFilter(filter DenialFilter) DenialFilter {
	if len(filter.Ignore) == 0 {
		return filter
	}
	clone := DenialFilter{
		DisableAutomatic: filter.DisableAutomatic,
		Ignore:           make([]DenialIgnoreRule, len(filter.Ignore)),
	}
	for i, rule := range filter.Ignore {
		clone.Ignore[i] = DenialIgnoreRule{
			Command:     rule.Command,
			Operations:  append([]string(nil), rule.Operations...),
			Targets:     append([]DenialTargetMatcher(nil), rule.Targets...),
			RawContains: append([]string(nil), rule.RawContains...),
		}
	}
	return clone
}

func shouldFilterMacOSSandboxDenial(
	denial Denial,
	cmd string,
	filter DenialFilter,
) bool {
	if !filter.DisableAutomatic && macosSandboxDenialAutoNoise(denial) {
		return true
	}
	for _, rule := range filter.Ignore {
		hasRawContains := stringSliceHasNonEmpty(rule.RawContains)
		if rule.Command != "" && !strings.Contains(cmd, rule.Command) {
			continue
		}
		if len(rule.Operations) > 0 && !stringSliceContains(rule.Operations, denial.Operation) {
			continue
		}
		if len(rule.Targets) > 0 && !macosDenialTargetMatches(denial.Target, rule.Targets) {
			continue
		}
		if hasRawContains && !stringSliceContainsSubstring(rule.RawContains, denial.Raw) {
			continue
		}
		if rule.Command == "" && len(rule.Operations) == 0 &&
			len(rule.Targets) == 0 && !hasRawContains {
			continue
		}
		return true
	}
	return false
}

func macosDenialTargetMatches(target string, matchers []DenialTargetMatcher) bool {
	for _, matcher := range matchers {
		if matcher.Exact != "" && target == matcher.Exact {
			return true
		}
		if matcher.Prefix != "" && strings.HasPrefix(target, matcher.Prefix) {
			return true
		}
		if matcher.Suffix != "" && strings.HasSuffix(target, matcher.Suffix) {
			return true
		}
		if matcher.Glob != "" {
			ok, err := ds.Match(
				filepath.ToSlash(matcher.Glob),
				filepath.ToSlash(target),
			)
			if err == nil && ok {
				return true
			}
		}
	}
	return false
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func stringSliceContainsSubstring(values []string, raw string) bool {
	for _, value := range values {
		if value != "" && strings.Contains(raw, value) {
			return true
		}
	}
	return false
}

func stringSliceHasNonEmpty(values []string) bool {
	for _, value := range values {
		if value != "" {
			return true
		}
	}
	return false
}

func macosSandboxDenialAutoNoise(denial Denial) bool {
	if denial.Operation != "mach-lookup" {
		return false
	}
	switch denial.Target {
	case "mDNSResponder", "com.apple.diagnosticd", "com.apple.analyticsd":
		return true
	default:
		return false
	}
}

func newMacOSSessionSuffix() string {
	return "_END_" + randomHex(8) + "_SBX"
}

func newMacOSProbeSuffix() string {
	return "_END_" + randomHex(8) + "_PROBE_SBX"
}

func newMacOSSandboxDenialRunTag(sessionSuffix string) string {
	return "TRPC_RUN_" + randomHex(8) + sessionSuffix
}

func containsExactSandboxTag(raw, tag string) bool {
	if tag == "" {
		return false
	}
	start := 0
	for {
		idx := strings.Index(raw[start:], tag)
		if idx < 0 {
			return false
		}
		idx += start
		beforeOK := idx == 0 || !isSandboxTagChar(raw[idx-1])
		after := idx + len(tag)
		afterOK := after == len(raw) || !isSandboxTagChar(raw[after])
		if beforeOK && afterOK {
			return true
		}
		start = idx + len(tag)
	}
}

func isSandboxTagChar(ch byte) bool {
	return ch == '_' ||
		(ch >= 'a' && ch <= 'z') ||
		(ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9')
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		seed := fmt.Sprintf("%d:%d", time.Now().UnixNano(), randomHexFallbackSeq.Add(1))
		sum := sha256.Sum256([]byte(seed))
		return hex.EncodeToString(sum[:])[:n*2]
	}
	return hex.EncodeToString(b)
}

// createMacOSProbeDir atomically creates the capability-probe working directory.
// Callers must remove the returned lexical path (not a canonicalized form) so
// cleanup cannot follow a replaced symlink onto an outside tree.
func createMacOSProbeDir() (string, error) {
	dir, err := os.MkdirTemp("/private/tmp", ".trpc_sbx_probe_")
	if err != nil {
		dir, err = os.MkdirTemp(os.TempDir(), ".trpc_sbx_probe_")
		if err != nil {
			return "", err
		}
	}
	return dir, nil
}

func canonicalizeProbePath(path string) string {
	canon, err := canonicalizeExistingPath(path)
	if err != nil {
		return path
	}
	return canon
}

func macOSVersionKey(ctx context.Context) (string, error) {
	macosVersionMu.Lock()
	if macosVersionReady {
		key := macosVersionCached
		macosVersionMu.Unlock()
		return key, nil
	}
	macosVersionMu.Unlock()

	cmd := macOSVersionCommand(ctx)
	out, err := cmd.Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		out = []byte("unknown")
	}
	key := strings.TrimSpace(string(out))
	if key == "" {
		key = "unknown"
	}

	macosVersionMu.Lock()
	defer macosVersionMu.Unlock()
	if !macosVersionReady {
		macosVersionCached = key
		macosVersionReady = true
	}
	return macosVersionCached, nil
}

func loadCachedDiagnosticsCaps(
	ctx context.Context,
) (DiagnosticsCapability, bool, error) {
	key, err := macOSVersionKey(ctx)
	if err != nil {
		return DiagnosticsCapability{}, false, err
	}
	denialCapsCacheMu.Lock()
	defer denialCapsCacheMu.Unlock()
	caps, ok := denialCapsByMacOSVer[key]
	return caps, ok, nil
}

func storeCachedDiagnosticsCaps(
	ctx context.Context,
	caps DiagnosticsCapability,
) error {
	key, err := macOSVersionKey(ctx)
	if err != nil {
		return err
	}
	denialCapsCacheMu.Lock()
	defer denialCapsCacheMu.Unlock()
	denialCapsByMacOSVer[key] = caps
	return nil
}

func resetDiagnosticsCapsCacheForTest() {
	denialCapsCacheMu.Lock()
	denialCapsByMacOSVer = map[string]DiagnosticsCapability{}
	denialCapsCacheMu.Unlock()

	macosVersionMu.Lock()
	macosVersionCached = ""
	macosVersionReady = false
	macosVersionMu.Unlock()
}
