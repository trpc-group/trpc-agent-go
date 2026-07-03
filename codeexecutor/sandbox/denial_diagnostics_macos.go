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
	mu     sync.Mutex
	events []macosSandboxDenialEvent
}

type macosLogStreamMonitor struct {
	cancel context.CancelFunc
	done   chan struct{}
	ready  chan struct{}
	ring   *macosDenialRing
}

type macosDenialDiagnostics struct {
	mu            sync.RWMutex
	filter        DenialFilter
	sessionSuffix string
	caps          DiagnosticsCapability
	monitorOnce   sync.Once
	monitorErr    error
	prodMonitor   *macosLogStreamMonitor
}

func (r *Runtime) initDenialDiagnosticsState() {
	r.macosDenialDiagnostics()
}

func (r *Runtime) macosDenialDiagnostics() *macosDenialDiagnostics {
	if d, ok := r.denials.(*macosDenialDiagnostics); ok && d != nil {
		if d.sessionSuffix == "" {
			d.sessionSuffix = newMacOSSessionSuffix()
		}
		return d
	}
	d := &macosDenialDiagnostics{sessionSuffix: newMacOSSessionSuffix()}
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
	d.mu.RLock()
	caps := d.caps
	d.mu.RUnlock()
	caps.Supported = true
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
	d.mu.RUnlock()
	return sandboxDenialRun{
		enabled:              true,
		runTag:               newMacOSSandboxDenialRunTag(sessionSuffix),
		defaultDenyTaggable:  caps.DefaultDenyTaggable,
		explicitDenyTaggable: caps.ExplicitDenyTaggable,
	}
}

func (r *Runtime) ensureDenialMonitor() error {
	d := r.macosDenialDiagnostics()
	d.monitorOnce.Do(func() {
		d.monitorErr = r.initDenialMonitor(d)
	})
	return d.monitorErr
}

func (r *Runtime) initDenialMonitor(d *macosDenialDiagnostics) error {
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
		monitor, err := startMacOSLogStreamMonitor(sessionSuffix)
		if err != nil {
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

	caps, err := r.probeDiagnosticsCapabilities()
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
	monitor, err := startMacOSLogStreamMonitor(sessionSuffix)
	if err != nil {
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
	runTag string,
	cmd string,
	settleTimeout time.Duration,
) []Denial {
	d := r.macosDenialDiagnostics()
	d.mu.RLock()
	monitor := d.prodMonitor
	filter := cloneDenialFilter(d.filter)
	d.mu.RUnlock()
	if runTag == "" || monitor == nil {
		return nil
	}
	monitor.ring.waitForRunTagSettle(runTag, settleTimeout)
	events := monitor.ring.snapshot()
	var tagged []Denial
	for _, event := range events {
		if !event.tagged || !containsExactSandboxTag(event.denial.Raw, runTag) {
			continue
		}
		tagged = append(tagged, event.denial)
	}
	return applySandboxDenialFilters(tagged, cmd, filter)
}

func (r *Runtime) probeDiagnosticsCapabilities() (DiagnosticsCapability, error) {
	caps := DiagnosticsCapability{
		Supported: true,
	}
	seatbelt, err := r.macosPreflight()
	if err != nil {
		return caps, nil
	}

	probe := func() (DiagnosticsCapability, bool) {
		return r.runDiagnosticsCapabilityProbe(seatbelt)
	}

	probed, ok := probe()
	if ok {
		return probed, nil
	}
	time.Sleep(200 * time.Millisecond)
	probed, ok = probe()
	if ok {
		return probed, nil
	}
	return probed, nil
}

func (r *Runtime) runDiagnosticsCapabilityProbe(
	seatbelt string,
) (DiagnosticsCapability, bool) {
	caps := DiagnosticsCapability{
		Supported: true,
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
			return caps, false
		}
	}

	monitor, err := startMacOSLogStreamMonitor(probeSuffix)
	if err != nil {
		return caps, false
	}
	defer monitor.stop()
	time.Sleep(100 * time.Millisecond)

	policy := macosDiagnosticsProbePolicy(probeTag, explicitTag, probeExplicitPath)
	profilePath, err := writeMacOSSeatbeltProfile(policy)
	if err != nil {
		return caps, false
	}
	defer os.Remove(profilePath)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, spec := range []struct {
		args []string
	}{
		{args: []string{"/bin/cat", probeDefaultPath}},
		{args: []string{"/bin/cat", probeExplicitPath}},
	} {
		cmdArgs := append([]string{"-f", profilePath, "--"}, spec.args...)
		cmd := exec.CommandContext(ctx, seatbelt, cmdArgs...)
		cmd.Dir = probeDirCanon
		cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "LANG=C"}
		if err := cmd.Run(); err == nil {
			return caps, false
		}
		time.Sleep(100 * time.Millisecond)
	}

	monitor.ring.waitForSettle(sandboxDenialProbeTimeout)
	events := monitor.ring.snapshot()
	if len(events) == 0 {
		return caps, false
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
	return caps, true
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

func startMacOSLogStreamMonitor(suffix string) (*macosLogStreamMonitor, error) {
	return startMacOSLogStreamMonitorWithPredicate(
		fmt.Sprintf(`eventMessage ENDSWITH %q`, suffix),
	)
}

func startMacOSLogStreamMonitorWithPredicate(predicate string) (*macosLogStreamMonitor, error) {
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
	select {
	case <-monitor.ready:
		select {
		case err := <-startErr:
			cancel()
			<-monitor.done
			return nil, err
		default:
		}
	case <-time.After(sandboxDenialProbeTimeout):
	}
	return monitor, nil
}

func (m *macosLogStreamMonitor) stop() {
	if m.cancel != nil {
		m.cancel()
	}
	select {
	case <-m.done:
	case <-time.After(500 * time.Millisecond):
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
		ring.events[len(ring.events)-1] = macosSandboxDenialEvent{denial: denial, tagged: tagged}
		return
	}
	ring.events = append(ring.events, macosSandboxDenialEvent{denial: denial, tagged: tagged})
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

func (ring *macosDenialRing) snapshot() []macosSandboxDenialEvent {
	ring.mu.Lock()
	defer ring.mu.Unlock()
	out := make([]macosSandboxDenialEvent, len(ring.events))
	copy(out, ring.events)
	return out
}

func (ring *macosDenialRing) waitForSettle(timeout time.Duration) {
	if timeout <= 0 {
		timeout = sandboxDenialSettleTimeout
	}
	deadline := time.Now().Add(timeout)
	idleStart := time.Now()
	lastCount := -1
	for time.Now().Before(deadline) {
		count := ring.count()
		if count != lastCount {
			lastCount = count
			idleStart = time.Now()
		}
		if count > 0 && time.Since(idleStart) >= 50*time.Millisecond {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (ring *macosDenialRing) waitForRunTagSettle(runTag string, timeout time.Duration) {
	if runTag == "" {
		ring.waitForSettle(timeout)
		return
	}
	if timeout <= 0 {
		timeout = sandboxDenialSettleTimeout
	}
	deadline := time.Now().Add(timeout)
	idleStart := time.Now()
	lastCount := -1
	for time.Now().Before(deadline) {
		count := ring.countMatchingRunTag(runTag)
		if count != lastCount {
			lastCount = count
			idleStart = time.Now()
		}
		if count > 0 && time.Since(idleStart) >= 50*time.Millisecond {
			return
		}
		time.Sleep(10 * time.Millisecond)
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
