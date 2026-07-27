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
)

const (
	macosLogPath                 = "/usr/bin/log"
	macosSandboxDenialBufferSize = 100
)

var (
	macosDenyRe          = regexp.MustCompile(`deny\([^)]+\)\s+([^\s]+)(?:\s+(.+))?`)
	denialCapsCacheMu    sync.Mutex
	denialCapsByMacOSVer = map[string]DiagnosticsCapability{}
	randomHexFallbackSeq atomic.Uint64
)

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
}

type macosDenialDiagnostics struct {
	mu            sync.RWMutex
	initGate      chan struct{}
	filter        DenialFilter
	sessionSuffix string
	caps          DiagnosticsCapability
	monitorErr    error
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
		d.mu.Unlock()
		return d
	}
	initGate := make(chan struct{}, 1)
	initGate <- struct{}{}
	d := &macosDenialDiagnostics{
		initGate:      initGate,
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
	d.mu.RLock()
	caps := d.caps
	sessionSuffix := d.sessionSuffix
	monitor := d.prodMonitor
	d.mu.RUnlock()
	var droppedAtStart uint64
	if monitor != nil && monitor.ring != nil {
		droppedAtStart = monitor.ring.dropCount()
	}
	return sandboxDenialRun{
		enabled:              true,
		runTag:               newMacOSSandboxDenialRunTag(sessionSuffix),
		droppedAtStart:       droppedAtStart,
		defaultDenyTaggable:  caps.DefaultDenyTaggable,
		explicitDenyTaggable: caps.ExplicitDenyTaggable,
	}
}

func (r *Runtime) sandboxDenialRunForCollecting(
	profile PermissionProfile,
) sandboxDenialRun {
	run := r.newSandboxDenialRun(profile)
	if !run.enabled || !r.sandboxDenialCollectingReady() {
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
		return nil
	default:
		return m
	}
}

func (r *Runtime) closeDenialDiagnostics() error {
	d := r.macosDenialDiagnostics()
	if err := d.lockInit(context.Background()); err != nil {
		return err
	}
	defer d.unlockInit()
	d.mu.Lock()
	m := d.prodMonitor
	d.caps.EventStreamAvailable = false
	d.caps.StrongCorrelation = false
	d.mu.Unlock()
	var stopErr error
	if m != nil {
		stopErr = m.stop()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if stopErr != nil {
		d.monitorErr = stopErr
		return stopErr
	}
	if d.prodMonitor == m {
		d.prodMonitor = nil
	}
	d.monitorErr = nil
	return nil
}

func (r *Runtime) ensureDenialMonitor(ctx context.Context) error {
	d := r.macosDenialDiagnostics()
	if err := d.lockInit(ctx); err != nil {
		return err
	}
	defer d.unlockInit()

	d.mu.Lock()
	if r.liveProdMonitorLocked(d) != nil {
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
	d.mu.Unlock()

	err := r.initDenialMonitor(ctx, d)
	d.mu.Lock()
	d.monitorErr = err
	d.mu.Unlock()
	return err
}

func (d *macosDenialDiagnostics) lockInit(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
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
	if cached, ok := loadCachedDiagnosticsCaps(); ok {
		caps := cached
		caps.ProbeCompleted = true
		if !caps.EventStreamAvailable {
			d.mu.Lock()
			d.caps = caps
			d.mu.Unlock()
			return nil
		}
		d.mu.RLock()
		sessionSuffix := d.sessionSuffix
		d.mu.RUnlock()
		monitor, err := startMacOSLogStreamMonitor(ctx, sessionSuffix)
		if err != nil {
			if errors.Is(err, context.Canceled) ||
				errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			caps.EventStreamAvailable = false
			caps.StrongCorrelation = false
			d.mu.Lock()
			d.caps = caps
			d.mu.Unlock()
			return nil
		}
		d.mu.Lock()
		d.caps = caps
		d.prodMonitor = monitor
		d.mu.Unlock()
		return nil
	}

	caps, err := r.probeDiagnosticsCapabilities(ctx)
	if err != nil {
		return err
	}
	if caps.ProbeCompleted {
		storeCachedDiagnosticsCaps(caps)
	}
	if !caps.EventStreamAvailable {
		d.mu.Lock()
		d.caps = caps
		d.mu.Unlock()
		return nil
	}
	d.mu.RLock()
	sessionSuffix := d.sessionSuffix
	d.mu.RUnlock()
	monitor, err := startMacOSLogStreamMonitor(ctx, sessionSuffix)
	if err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		caps.EventStreamAvailable = false
		caps.StrongCorrelation = false
		d.mu.Lock()
		d.caps = caps
		d.mu.Unlock()
		return nil
	}
	d.mu.Lock()
	d.caps = caps
	d.prodMonitor = monitor
	d.mu.Unlock()
	return nil
}

func (r *Runtime) collectSandboxDenials(
	ctx context.Context,
	runTag string,
	droppedAtStart uint64,
	cmd string,
	settleTimeout time.Duration,
) ([]Denial, bool) {
	d := r.macosDenialDiagnostics()
	d.mu.Lock()
	monitor := r.liveProdMonitorLocked(d)
	filter := cloneDenialFilter(d.filter)
	d.mu.Unlock()
	if runTag == "" || monitor == nil {
		return nil, false
	}
	_ = monitor.ring.waitForRunTagSettle(ctx, runTag, settleTimeout)
	events, truncated := monitor.ring.snapshotSince(droppedAtStart)
	var tagged []Denial
	for _, event := range events {
		if !event.tagged || !containsExactSandboxTag(event.denial.Raw, runTag) {
			continue
		}
		tagged = append(tagged, event.denial)
	}
	return applyMacOSSandboxDenialFilters(tagged, cmd, filter), truncated
}

func (r *Runtime) probeDiagnosticsCapabilities(
	ctx context.Context,
) (DiagnosticsCapability, error) {
	caps := DiagnosticsCapability{
		Supported: true,
	}
	if err := ctx.Err(); err != nil {
		return caps, err
	}
	seatbelt, err := r.macosPreflightContext(ctx)
	if err != nil {
		return caps, nil
	}

	probe := func() (DiagnosticsCapability, bool, error) {
		return r.runDiagnosticsCapabilityProbe(ctx, seatbelt)
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
) (DiagnosticsCapability, bool, error) {
	caps := DiagnosticsCapability{
		Supported: true,
	}
	if err := ctx.Err(); err != nil {
		return caps, false, err
	}
	probeSuffix := newMacOSProbeSuffix()
	probeTag := "TRPC_RUN_PROBE_D_" + randomHex(8) + probeSuffix
	explicitTag := "TRPC_RUN_PROBE_E_" + randomHex(8) + probeSuffix
	probeDir := filepath.Join("/private/tmp", ".trpc_sbx_probe_"+randomHex(4))
	if err := os.MkdirAll(probeDir, 0o700); err != nil {
		probeDir = filepath.Join(os.TempDir(), ".trpc_sbx_probe_"+randomHex(4))
		_ = os.MkdirAll(probeDir, 0o700)
	}
	probeDirCanon, err := canonicalizeExistingPath(probeDir)
	if err != nil {
		probeDirCanon = probeDir
	}
	defer os.RemoveAll(probeDirCanon)
	probeDefaultPath := filepath.Join(probeDirCanon, "default_target")
	probeExplicitPath := filepath.Join(probeDirCanon, "explicit_target")
	for _, path := range []string{probeDefaultPath, probeExplicitPath} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			return caps, false, nil
		}
	}

	monitor, err := startMacOSLogStreamMonitor(ctx, probeSuffix)
	if err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return caps, false, err
		}
		return caps, false, nil
	}
	defer monitor.stop()
	if err := waitCtx(ctx, 100*time.Millisecond); err != nil {
		return caps, false, err
	}

	policy := macosDiagnosticsProbePolicy(probeTag, explicitTag, probeExplicitPath)
	profilePath, err := writeMacOSSeatbeltProfile(policy)
	if err != nil {
		return caps, false, nil
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
			return caps, false, err
		}
		cmdArgs := append([]string{"-f", profilePath, "--"}, spec.args...)
		cmd := exec.CommandContext(probeCtx, seatbelt, cmdArgs...)
		cmd.Dir = probeDirCanon
		cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "LANG=C"}
		if err := cmd.Run(); err == nil {
			return caps, false, nil
		}
		if err := waitCtx(probeCtx, 100*time.Millisecond); err != nil {
			return caps, false, err
		}
	}

	if err := monitor.ring.waitForSettle(ctx, sandboxDenialProbeTimeout); err != nil {
		return caps, false, err
	}
	events, _ := monitor.ring.snapshot()
	if len(events) == 0 {
		return caps, false, nil
	}

	caps.EventStreamAvailable = true
	caps.ProbeCompleted = true
	if probeMatched(events, probeExpectation{
		Tag:       probeTag,
		Operation: "file-read*",
		Target:    probeDefaultPath,
	}) {
		caps.DefaultDenyTaggable = true
	}
	if probeMatched(events, probeExpectation{
		Tag:       explicitTag,
		Operation: "file-read*",
		Target:    probeExplicitPath,
	}) {
		caps.ExplicitDenyTaggable = true
	}
	caps.StrongCorrelation = caps.ExplicitDenyTaggable || caps.DefaultDenyTaggable
	return caps, true, nil
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
		_ = monitor.stop()
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
	select {
	case <-m.done:
		return nil
	case <-time.After(500 * time.Millisecond):
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

func (ring *macosDenialRing) countMatchingRunTag(runTag string) int {
	ring.mu.Lock()
	defer ring.mu.Unlock()
	count := 0
	for _, event := range ring.events {
		if event.tagged && containsExactSandboxTag(event.denial.Raw, runTag) {
			count++
		}
	}
	return count
}

func (ring *macosDenialRing) snapshot() ([]macosSandboxDenialEvent, bool) {
	return ring.snapshotSince(0)
}

func (ring *macosDenialRing) snapshotSince(
	droppedAtStart uint64,
) ([]macosSandboxDenialEvent, bool) {
	ring.mu.Lock()
	defer ring.mu.Unlock()
	out := make([]macosSandboxDenialEvent, len(ring.events))
	copy(out, ring.events)
	return out, ring.dropped > droppedAtStart
}

func (ring *macosDenialRing) dropCount() uint64 {
	ring.mu.Lock()
	defer ring.mu.Unlock()
	return ring.dropped
}

func (ring *macosDenialRing) waitForSettle(
	ctx context.Context,
	timeout time.Duration,
) error {
	if timeout <= 0 {
		timeout = sandboxDenialSettleTimeout
	}
	deadline := time.Now().Add(timeout)
	idleStart := time.Now()
	lastCount := -1
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		count := ring.count()
		if count != lastCount {
			lastCount = count
			idleStart = time.Now()
		}
		if count > 0 && time.Since(idleStart) >= 50*time.Millisecond {
			return nil
		}
		if err := waitCtx(ctx, 10*time.Millisecond); err != nil {
			return err
		}
	}
	return nil
}

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
	deadline := time.Now().Add(timeout)
	idleStart := time.Now()
	lastCount := -1
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		count := ring.countMatchingRunTag(runTag)
		if count != lastCount {
			lastCount = count
			idleStart = time.Now()
		}
		if count > 0 && time.Since(idleStart) >= 50*time.Millisecond {
			return nil
		}
		if err := waitCtx(ctx, 10*time.Millisecond); err != nil {
			return err
		}
	}
	return nil
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
		Operation:  denyMatch[1],
		Target:     target,
		Raw:        raw,
		Timestamp:  parseMacOSLogTimestamp(timestamp),
		Source:     DenialSourceMacOSUnifiedLog,
		Confidence: DenialConfidenceStrong,
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
	return time.Now()
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
		if shouldFilterMacOSSandboxDenial(denial, cmd, filter, DenialFilterDenials) {
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
			Scope:       rule.Scope,
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
	scope DenialFilterScope,
) bool {
	if !filter.DisableAutomatic && macosSandboxDenialAutoNoise(denial) {
		return true
	}
	for _, rule := range filter.Ignore {
		if !macosDenialFilterScopeMatches(rule.Scope, scope) {
			continue
		}
		if rule.Command != "" && !strings.Contains(cmd, rule.Command) {
			continue
		}
		if len(rule.Operations) > 0 && !stringSliceContains(rule.Operations, denial.Operation) {
			continue
		}
		if len(rule.Targets) > 0 && !macosDenialTargetMatches(denial.Target, rule.Targets) {
			continue
		}
		if len(rule.RawContains) > 0 && !stringSliceContainsSubstring(rule.RawContains, denial.Raw) {
			continue
		}
		if rule.Command == "" && len(rule.Operations) == 0 &&
			len(rule.Targets) == 0 && len(rule.RawContains) == 0 {
			continue
		}
		return true
	}
	return false
}

func macosDenialFilterScopeMatches(ruleScope, want DenialFilterScope) bool {
	switch ruleScope {
	case "", DenialFilterAll:
		return true
	case DenialFilterDenials:
		return want == DenialFilterDenials || want == DenialFilterAll
	default:
		return false
	}
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
			ok, err := filepath.Match(matcher.Glob, target)
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
		if strings.Contains(raw, value) {
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

func macOSVersionKey() string {
	out, err := exec.Command("/usr/bin/sw_vers", "-productVersion").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func loadCachedDiagnosticsCaps() (DiagnosticsCapability, bool) {
	denialCapsCacheMu.Lock()
	defer denialCapsCacheMu.Unlock()
	caps, ok := denialCapsByMacOSVer[macOSVersionKey()]
	return caps, ok
}

func storeCachedDiagnosticsCaps(caps DiagnosticsCapability) {
	denialCapsCacheMu.Lock()
	defer denialCapsCacheMu.Unlock()
	denialCapsByMacOSVer[macOSVersionKey()] = caps
}

func resetDiagnosticsCapsCacheForTest() {
	denialCapsCacheMu.Lock()
	defer denialCapsCacheMu.Unlock()
	denialCapsByMacOSVer = map[string]DiagnosticsCapability{}
}
