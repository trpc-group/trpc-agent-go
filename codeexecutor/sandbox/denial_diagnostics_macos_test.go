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
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func denialRunForMonitor(
	runTag string,
	monitor *macosLogStreamMonitor,
	droppedAtStart uint64,
) sandboxDenialRun {
	var done <-chan struct{}
	var ring *macosDenialRing
	if monitor != nil {
		ring = monitor.ring
		if monitor.done != nil {
			done = monitor.done
		}
	}
	return sandboxDenialRun{
		enabled:        true,
		runTag:         runTag,
		droppedAtStart: droppedAtStart,
		collectGeneration: &macosDenialCollectGeneration{
			ring: ring,
			done: done,
		},
	}
}

func TestMacOSSandboxDenialAutoNoiseFilter(t *testing.T) {
	for _, denial := range []Denial{
		{Operation: "mach-lookup", Target: "mDNSResponder"},
		{Operation: "mach-lookup", Target: "com.apple.diagnosticd"},
		{Operation: "mach-lookup", Target: "com.apple.analyticsd"},
	} {
		if !macosSandboxDenialAutoNoise(denial) {
			t.Fatalf("auto noise filter did not match %#v", denial)
		}
	}
	for _, denial := range []Denial{
		{Operation: "file-read-data", Target: "/private/tmp/user-file"},
		{Operation: "file-read-data", Target: "/Users/me/my-analyticsd-project/foo"},
		{Operation: "mach-lookup", Target: "com.apple.trustd.agent"},
		{Operation: "file-read-data", Target: "/dev/dtracehelper"},
	} {
		if macosSandboxDenialAutoNoise(denial) {
			t.Fatalf("auto noise filter matched user-relevant denial %#v", denial)
		}
	}
}

func TestSandboxDenialConfiguredFilters(t *testing.T) {
	denials := []Denial{{
		Operation: "file-read-data",
		Target:    "/dev/dtracehelper",
		Raw:       "Sandbox: cat deny file-read-data /dev/dtracehelper",
	}}
	filtered := applyMacOSSandboxDenialFilters(denials, "/bin/cat", DenialFilter{
		Ignore: []DenialIgnoreRule{{
			Targets: []DenialTargetMatcher{{Prefix: "/dev/dtracehelper"}},
		}},
	})
	if len(filtered) != 0 {
		t.Fatalf("configured filter = %#v, want empty", filtered)
	}
	if filtered != nil {
		t.Fatalf("configured filter returned %#v, want nil empty result", filtered)
	}
}

func TestSandboxDenialCommandPatternFilter(t *testing.T) {
	denials := []Denial{{
		Operation: "file-read-data",
		Target:    "/private/tmp/foo",
	}}
	filtered := applyMacOSSandboxDenialFilters(denials, "/bin/gh", DenialFilter{
		Ignore: []DenialIgnoreRule{{
			Command: "gh",
			Targets: []DenialTargetMatcher{{Exact: "/private/tmp/foo"}},
		}},
	})
	if len(filtered) != 0 {
		t.Fatalf("command-pattern filter = %#v, want empty", filtered)
	}
	kept := applyMacOSSandboxDenialFilters(denials, "/bin/cat", DenialFilter{
		Ignore: []DenialIgnoreRule{{
			Command: "gh",
			Targets: []DenialTargetMatcher{{Exact: "/private/tmp/foo"}},
		}},
	})
	if len(kept) != 1 {
		t.Fatalf("command-pattern kept = %#v, want one denial", kept)
	}
}

func TestSandboxDenialDisableAutomatic(t *testing.T) {
	denials := []Denial{{
		Operation: "mach-lookup",
		Target:    "com.apple.diagnosticd",
	}}
	filtered := applyMacOSSandboxDenialFilters(denials, "/bin/cat", DenialFilter{
		DisableAutomatic: true,
	})
	if len(filtered) != 1 {
		t.Fatalf("disable automatic = %#v, want diagnosticd denial kept", filtered)
	}
}

func TestSandboxDenialDeduplicatesByOperationAndTarget(t *testing.T) {
	firstTimestamp := time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC)
	denials := []Denial{
		{
			Operation: "file-read-data",
			Target:    "/private/tmp/foo",
			Raw:       "first",
			Timestamp: firstTimestamp,
		},
		{
			Operation: "file-read-data",
			Target:    "/private/tmp/foo",
			Raw:       "second",
			Timestamp: firstTimestamp.Add(time.Second),
		},
		{Operation: "file-read-metadata", Target: "/private/tmp/foo", Raw: "third"},
	}
	filtered := applyMacOSSandboxDenialFilters(denials, "/bin/cat", DenialFilter{})
	if len(filtered) != 2 {
		t.Fatalf("deduped denials = %#v, want two operation+target pairs", filtered)
	}
	if filtered[0].Raw != "first" || !filtered[0].Timestamp.Equal(firstTimestamp) {
		t.Fatalf("deduped denial = %#v, want first event metadata", filtered[0])
	}
}

func TestSandboxDenialTargetSuffixGlobAndRawFilters(t *testing.T) {
	denials := []Denial{
		{Operation: "file-read-data", Target: "/private/tmp/cache.sock"},
		{Operation: "file-read-data", Target: "/private/tmp/app.env"},
		{Operation: "file-read-data", Target: "/private/tmp/report.log", Raw: "duplicate report"},
	}
	filtered := applyMacOSSandboxDenialFilters(denials, "/bin/cat", DenialFilter{
		Ignore: []DenialIgnoreRule{
			{Targets: []DenialTargetMatcher{{Suffix: ".sock"}}},
			{Targets: []DenialTargetMatcher{{Glob: "/private/tmp/*.env"}}},
			{RawContains: []string{"duplicate report"}},
		},
	})
	if filtered != nil {
		t.Fatalf("suffix/glob/raw filter = %#v, want nil", filtered)
	}
}

func TestCloneSandboxDenialFilterDeepCopiesSlices(t *testing.T) {
	filter := DenialFilter{
		Ignore: []DenialIgnoreRule{{
			Operations:  []string{"file-read-data"},
			Targets:     []DenialTargetMatcher{{Exact: "/private/tmp/foo"}},
			RawContains: []string{"deny"},
		}},
	}
	clone := cloneDenialFilter(filter)
	filter.Ignore[0].Operations[0] = "mach-lookup"
	filter.Ignore[0].Targets[0].Exact = "/private/tmp/bar"
	filter.Ignore[0].RawContains[0] = "allow"

	if clone.Ignore[0].Operations[0] != "file-read-data" ||
		clone.Ignore[0].Targets[0].Exact != "/private/tmp/foo" ||
		clone.Ignore[0].RawContains[0] != "deny" {
		t.Fatalf("clone shares nested slices: %#v", clone)
	}
}

func TestSandboxDenialEmptyInputReturnsNil(t *testing.T) {
	if got := applyMacOSSandboxDenialFilters(nil, "/bin/cat", DenialFilter{}); got != nil {
		t.Fatalf("nil input = %#v, want nil", got)
	}
}

func TestSandboxDenialOperationFilter(t *testing.T) {
	denials := []Denial{{
		Operation: "file-read-data",
		Target:    "/private/tmp/foo",
	}}
	filtered := applyMacOSSandboxDenialFilters(denials, "/bin/cat", DenialFilter{
		Ignore: []DenialIgnoreRule{{
			Operations: []string{"file-read-data"},
		}},
	})
	if filtered != nil {
		t.Fatalf("operation filter = %#v, want nil", filtered)
	}
	kept := applyMacOSSandboxDenialFilters(denials, "/bin/cat", DenialFilter{
		Ignore: []DenialIgnoreRule{{
			Operations: []string{"mach-lookup"},
		}},
	})
	if len(kept) != 1 {
		t.Fatalf("operation kept = %#v, want one denial", kept)
	}
}

func TestSandboxDenialEmptyIgnoreRuleDoesNotMatch(t *testing.T) {
	denials := []Denial{{
		Operation: "file-read-data",
		Target:    "/private/tmp/foo",
	}}
	filtered := applyMacOSSandboxDenialFilters(denials, "/bin/cat", DenialFilter{
		Ignore: []DenialIgnoreRule{{}},
	})
	if len(filtered) != 1 {
		t.Fatalf("empty ignore rule = %#v, want original denial", filtered)
	}
}

func TestSandboxDenialInvalidGlobDoesNotMatch(t *testing.T) {
	if macosDenialTargetMatches("/private/tmp/foo", []DenialTargetMatcher{{Glob: "[invalid"}}) {
		t.Fatal("invalid glob should not match target")
	}
}

func TestSandboxDenialDoublestarGlobMatchesNestedTarget(t *testing.T) {
	target := "/private/tmp/nested/cache.sock"
	if !macosDenialTargetMatches(target, []DenialTargetMatcher{
		{Glob: "/private/**/cache.sock"},
	}) {
		t.Fatalf("doublestar glob did not match nested target %q", target)
	}
	filtered := applyMacOSSandboxDenialFilters(
		[]Denial{{
			Operation: "file-read-data",
			Target:    target,
			Raw:       "Sandbox: cat deny(1) file-read-data " + target,
		}},
		"/bin/cat",
		DenialFilter{
			Ignore: []DenialIgnoreRule{{
				Targets: []DenialTargetMatcher{{Glob: "/private/**/*.sock"}},
			}},
		},
	)
	if filtered != nil {
		t.Fatalf("nested doublestar ignore = %#v, want nil", filtered)
	}
	if macosDenialTargetMatches(target, []DenialTargetMatcher{
		{Glob: "/private/*/cache.sock"},
	}) {
		t.Fatal("single-star glob unexpectedly matched nested path")
	}
}

func TestCloneSandboxDenialFilterWithoutIgnoreRules(t *testing.T) {
	filter := DenialFilter{DisableAutomatic: true}
	clone := cloneDenialFilter(filter)
	if clone.DisableAutomatic != filter.DisableAutomatic || len(clone.Ignore) != 0 {
		t.Fatalf("clone without ignore rules = %#v, want %#v", clone, filter)
	}
}

func TestCollectSandboxDenialsAppliesRuntimeDenialFilter(t *testing.T) {
	runTag := "TRPC_RUN_filter_END_0123456789abcdef_SBX"
	rt := NewRuntime(WithDenialFilter(DenialFilter{
		Ignore: []DenialIgnoreRule{{
			Targets: []DenialTargetMatcher{{Prefix: "/dev/dtracehelper"}},
		}},
	}))
	ring := &macosDenialRing{
		events: []macosSandboxDenialEvent{
			{
				denial: Denial{
					Operation: "file-read-data",
					Target:    "/dev/dtracehelper",
					Raw:       "Sandbox: cat deny file-read-data /dev/dtracehelper\n" + runTag,
				},
				tagged: true,
			},
			{
				denial: Denial{
					Operation: "file-read-data",
					Target:    "/private/tmp/keep",
					Raw:       "Sandbox: cat deny file-read-data /private/tmp/keep\n" + runTag,
				},
				tagged: true,
			},
		},
	}
	d := rt.macosDenialDiagnostics()
	d.mu.Lock()
	monitor := &macosLogStreamMonitor{ring: ring}
	d.prodMonitor = monitor
	d.caps = DiagnosticsCapability{
		EventStreamAvailable: true,
		StrongCorrelation:    true,
		ProbeCompleted:       true,
	}
	d.mu.Unlock()

	denials, _ := rt.collectSandboxDenials(
		context.Background(),
		denialRunForMonitor(runTag, monitor, 0),
		"/bin/cat",
		time.Millisecond,
	)
	if len(denials) != 1 || denials[0].Target != "/private/tmp/keep" {
		t.Fatalf("filtered denials = %#v, want only /private/tmp/keep", denials)
	}
}

func TestCollectSandboxDenialsFiltersAutomaticNoise(t *testing.T) {
	runTag := "TRPC_RUN_noise_END_0123456789abcdef_SBX"
	rt := NewRuntime()
	ring := &macosDenialRing{
		events: []macosSandboxDenialEvent{
			{
				denial: Denial{
					Operation: "mach-lookup",
					Target:    "com.apple.diagnosticd",
					Raw:       "Sandbox: cat deny mach-lookup com.apple.diagnosticd\n" + runTag,
				},
				tagged: true,
			},
			{
				denial: Denial{
					Operation: "file-read-data",
					Target:    "/private/tmp/keep",
					Raw:       "Sandbox: cat deny file-read-data /private/tmp/keep\n" + runTag,
				},
				tagged: true,
			},
		},
	}
	d := rt.macosDenialDiagnostics()
	d.mu.Lock()
	monitor := &macosLogStreamMonitor{ring: ring}
	d.prodMonitor = monitor
	d.caps = DiagnosticsCapability{
		EventStreamAvailable: true,
		StrongCorrelation:    true,
		ProbeCompleted:       true,
	}
	d.mu.Unlock()

	denials, _ := rt.collectSandboxDenials(
		context.Background(),
		denialRunForMonitor(runTag, monitor, 0),
		"/bin/cat",
		time.Millisecond,
	)
	if len(denials) != 1 || denials[0].Target != "/private/tmp/keep" {
		t.Fatalf("auto-filtered denials = %#v, want only user-relevant denial", denials)
	}
}

func TestRandomHexProducesExpectedLength(t *testing.T) {
	if got := randomHex(8); len(got) != 16 {
		t.Fatalf("randomHex(8) = %q, want 16 hex chars", got)
	}
	if got := randomHex(4); len(got) != 8 {
		t.Fatalf("randomHex(4) = %q, want 8 hex chars", got)
	}
}

func TestCreateMacOSProbeDirIgnoresSymlinkCollision(t *testing.T) {
	victim, err := os.MkdirTemp("/private/tmp", "trpc-probe-victim-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(victim) })
	marker := filepath.Join(victim, "outside-marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Pre-create a symlink that the old MkdirAll path would have adopted.
	collision := filepath.Join("/private/tmp", ".trpc_sbx_probe_symlink_collision")
	_ = os.Remove(collision)
	if err := os.Symlink(victim, collision); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(collision) })

	probeDir, err := createMacOSProbeDir()
	if err != nil {
		t.Fatal(err)
	}
	if probeDir == collision {
		t.Fatalf("createMacOSProbeDir reused symlink collision path %q", probeDir)
	}
	info, err := os.Lstat(probeDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Fatalf("probe dir = %#v, want a real directory", info)
	}

	for _, name := range []string{"default_target", "explicit_target"} {
		if err := os.WriteFile(filepath.Join(probeDir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(probeDir); err != nil {
		t.Fatalf("lexical cleanup: %v", err)
	}

	got, err := os.ReadFile(marker)
	if err != nil || string(got) != "keep" {
		t.Fatalf("outside marker = %q err=%v, want keep", got, err)
	}
	if _, err := os.Stat(filepath.Join(victim, "default_target")); !os.IsNotExist(err) {
		t.Fatalf("victim polluted by probe write: err=%v", err)
	}
	if _, err := os.Lstat(collision); err != nil {
		t.Fatalf("collision symlink removed unexpectedly: %v", err)
	}
}

func TestMacOSProbeDirLexicalCleanupDoesNotFollowReplacedSymlink(t *testing.T) {
	probeDir, err := createMacOSProbeDir()
	if err != nil {
		t.Fatal(err)
	}
	// Intentionally do not defer RemoveAll(probeDir): the test replaces it.

	victim, err := os.MkdirTemp("/private/tmp", "trpc-probe-victim-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(victim) })
	marker := filepath.Join(victim, "outside-marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(probeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, probeDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(probeDir) })

	// Cleaning the lexical path must remove only the symlink, not the target.
	if err := os.RemoveAll(probeDir); err != nil {
		t.Fatalf("lexical cleanup after symlink replace: %v", err)
	}
	got, err := os.ReadFile(marker)
	if err != nil || string(got) != "keep" {
		t.Fatalf("outside marker = %q err=%v, want keep", got, err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("victim directory removed by lexical cleanup: %v", err)
	}
}

func TestIncompleteDiagnosticsProbePreservesCallerCancellation(t *testing.T) {
	caps := DiagnosticsCapability{Supported: true}
	got, ok, err := incompleteDiagnosticsProbe(context.Background(), caps)
	if err != nil || ok || got != caps {
		t.Fatalf("healthy incomplete probe = %#v, %v, %v", got, ok, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, ok, err = incompleteDiagnosticsProbe(ctx, caps)
	if !errors.Is(err, context.Canceled) || ok || got != caps {
		t.Fatalf("canceled incomplete probe = %#v, %v, %v", got, ok, err)
	}
}

func TestInitDenialMonitorHonorsCachedCapsWithoutEventStream(t *testing.T) {
	resetDiagnosticsCapsCacheForTest()
	t.Cleanup(resetDiagnosticsCapsCacheForTest)
	storeCachedDiagnosticsCaps(context.Background(), DiagnosticsCapability{
		Supported:            true,
		ProbeCompleted:       true,
		EventStreamAvailable: false,
	})

	rt := NewRuntime()
	if err := rt.ensureDenialMonitor(context.Background()); err != nil {
		t.Fatalf("ensureDenialMonitor: %v", err)
	}
	if rt.sandboxDenialCollectingReady() {
		t.Fatal("sandboxDenialCollectingReady = true, want false for cached unavailable stream")
	}
	caps := rt.DiagnosticsCapability()
	if caps.EventStreamAvailable || !caps.ProbeCompleted || !caps.Supported {
		t.Fatalf("caps = %#v, want supported probed cache without event stream", caps)
	}
	d := rt.macosDenialDiagnostics()
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.state != macosDenialDegraded {
		t.Fatalf("diagnostics state = %v, want degraded", d.state)
	}
}

func TestInitDenialMonitorSkipsCollectionWhenNeitherFormTaggable(t *testing.T) {
	resetDiagnosticsCapsCacheForTest()
	t.Cleanup(resetDiagnosticsCapsCacheForTest)
	storeCachedDiagnosticsCaps(context.Background(), DiagnosticsCapability{
		Supported:            true,
		ProbeCompleted:       true,
		EventStreamAvailable: true,
		StrongCorrelation:    false,
		DefaultDenyTaggable:  false,
		ExplicitDenyTaggable: false,
	})

	startCalls := 0
	oldStart := startMacOSLogStreamMonitorFn
	startMacOSLogStreamMonitorFn = func(
		ctx context.Context,
		suffix string,
	) (*macosLogStreamMonitor, error) {
		startCalls++
		return nil, errors.New("production monitor must not start for false/false caps")
	}
	t.Cleanup(func() { startMacOSLogStreamMonitorFn = oldStart })

	rt := NewRuntime()
	if err := rt.ensureDenialMonitor(context.Background()); err != nil {
		t.Fatalf("ensureDenialMonitor: %v", err)
	}
	if startCalls != 0 {
		t.Fatalf("production monitor start calls = %d, want 0", startCalls)
	}
	if rt.sandboxDenialCollectingReady() {
		t.Fatal("sandboxDenialCollectingReady = true, want false when neither form is taggable")
	}
	run := rt.sandboxDenialRunForCollecting(WorkspaceWriteProfile())
	if run.enabled || run.runTag != "" || run.collectGeneration != nil {
		t.Fatalf("collecting run = %#v, want disabled", run)
	}
	caps := rt.DiagnosticsCapability()
	if !caps.Supported || !caps.ProbeCompleted || !caps.EventStreamAvailable {
		t.Fatalf("caps = %#v, want preserved stream-available false/false report", caps)
	}
	if caps.StrongCorrelation || caps.DefaultDenyTaggable || caps.ExplicitDenyTaggable {
		t.Fatalf("caps = %#v, want StrongCorrelation and both taggable false", caps)
	}
	d := rt.macosDenialDiagnostics()
	d.mu.RLock()
	state := d.state
	prod := d.prodMonitor
	d.mu.RUnlock()
	if state != macosDenialDegraded {
		t.Fatalf("diagnostics state = %v, want degraded", state)
	}
	if prod != nil {
		t.Fatal("prodMonitor started for non-correlatable caps")
	}

	start := time.Now()
	got := rt.collectRunDiagnostics(
		context.Background(),
		run,
		"/bin/cat",
		false,
	)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("disabled collection took %s, want no settle wait", elapsed)
	}
	if len(got.Denials) != 0 || got.Truncated {
		t.Fatalf("diagnostics = %#v, want zero value without collection", got)
	}
}

func TestActivateDenialMonitorKeepsIncompleteProbeRetryable(t *testing.T) {
	rt := NewRuntime()
	d := rt.macosDenialDiagnostics()
	d.mu.Lock()
	d.state = macosDenialStarting
	d.mu.Unlock()

	if err := rt.activateDenialMonitor(context.Background(), d, DiagnosticsCapability{
		Supported:            true,
		ProbeCompleted:       false,
		EventStreamAvailable: false,
	}); err != nil {
		t.Fatalf("activateDenialMonitor: %v", err)
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.state != macosDenialIdle {
		t.Fatalf("diagnostics state = %v, want idle for incomplete probe", d.state)
	}
}

func TestEnsureDenialMonitorRetriesAfterTransientMonitorStartFailure(t *testing.T) {
	resetDiagnosticsCapsCacheForTest()
	t.Cleanup(resetDiagnosticsCapsCacheForTest)
	storeCachedDiagnosticsCaps(context.Background(), DiagnosticsCapability{
		Supported:            true,
		ProbeCompleted:       true,
		EventStreamAvailable: true,
		StrongCorrelation:    true,
	})

	calls := 0
	oldStart := startMacOSLogStreamMonitorFn
	startMacOSLogStreamMonitorFn = func(
		ctx context.Context,
		suffix string,
	) (*macosLogStreamMonitor, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("transient log stream start failure")
		}
		ready := make(chan struct{})
		close(ready)
		done := make(chan struct{})
		var stopOnce sync.Once
		return &macosLogStreamMonitor{
			cancel: func() {
				stopOnce.Do(func() { close(done) })
			},
			done:  done,
			ready: ready,
			ring:  &macosDenialRing{},
		}, nil
	}
	t.Cleanup(func() {
		startMacOSLogStreamMonitorFn = oldStart
	})

	rt := NewRuntime()
	t.Cleanup(func() { _ = rt.Close() })
	if err := rt.ensureDenialMonitor(context.Background()); err != nil {
		t.Fatalf("first ensureDenialMonitor: %v", err)
	}
	d := rt.macosDenialDiagnostics()
	d.mu.RLock()
	firstState := d.state
	d.mu.RUnlock()
	if firstState != macosDenialIdle {
		t.Fatalf("after transient failure state = %v, want idle", firstState)
	}
	if rt.sandboxDenialCollectingReady() {
		t.Fatal("collecting ready after transient failure")
	}

	if err := rt.ensureDenialMonitor(context.Background()); err != nil {
		t.Fatalf("second ensureDenialMonitor: %v", err)
	}
	if !rt.sandboxDenialCollectingReady() {
		t.Fatal("second ensureDenialMonitor did not recover monitor")
	}
	if calls != 2 {
		t.Fatalf("start calls = %d, want 2", calls)
	}
}

func TestMacOSVersionKeyHonorsContextCancellation(t *testing.T) {
	resetDiagnosticsCapsCacheForTest()
	t.Cleanup(resetDiagnosticsCapsCacheForTest)

	started := make(chan struct{})
	oldCommand := macOSVersionCommand
	macOSVersionCommand = func(ctx context.Context) *exec.Cmd {
		select {
		case <-started:
		default:
			close(started)
		}
		return exec.CommandContext(ctx, "/bin/sleep", "30")
	}
	t.Cleanup(func() {
		macOSVersionCommand = oldCommand
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := macOSVersionKey(ctx)
		errCh <- err
	}()
	<-started
	cancel()
	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("macOSVersionKey error = %v, want context.Canceled", err)
	}

	canceled, cancelCached := context.WithCancel(context.Background())
	cancelCached()
	if _, _, err := loadCachedDiagnosticsCaps(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("loadCachedDiagnosticsCaps error = %v, want context.Canceled", err)
	}
}

func TestInitDenialMonitorUsesCachedCapsWithAvailableEventStream(t *testing.T) {
	resetDiagnosticsCapsCacheForTest()
	t.Cleanup(resetDiagnosticsCapsCacheForTest)
	storeCachedDiagnosticsCaps(context.Background(), DiagnosticsCapability{
		Supported:            true,
		ProbeCompleted:       true,
		EventStreamAvailable: true,
		StrongCorrelation:    true,
	})

	rt := NewRuntime()
	t.Cleanup(func() { _ = rt.Close() })
	if err := rt.ensureDenialMonitor(context.Background()); err != nil {
		t.Fatalf("ensureDenialMonitor: %v", err)
	}
	if !rt.sandboxDenialCollectingReady() {
		t.Skip("log stream unavailable on this host")
	}
	caps := rt.DiagnosticsCapability()
	if !caps.EventStreamAvailable || !caps.ProbeCompleted {
		t.Fatalf("caps = %#v, want cached stream availability", caps)
	}
}

func TestDiagnosticsCapabilityNonMacOSBackendReturnsZero(t *testing.T) {
	rt := NewRuntime(
		WithBackend(BackendLinuxBubblewrap),
		WithPermissionProfile(WorkspaceWriteProfile()),
	)
	caps := rt.DiagnosticsCapability()
	if caps != (DiagnosticsCapability{}) {
		t.Fatalf("non-macOS backend caps = %#v, want zero value", caps)
	}
}

func TestContainsExactSandboxTagRejectsPartialEmbeddedMatch(t *testing.T) {
	tag := "TRPC_RUN_abcd_END_0123456789abcdef_SBX"
	raw := "prefix TRPC_RUN_abcdXEND_0123456789abcdef_SBX suffix"
	if containsExactSandboxTag(raw, tag) {
		t.Fatalf("containsExactSandboxTag matched embedded partial tag in %q", raw)
	}
	if !containsExactSandboxTag("deny\n"+tag, tag) {
		t.Fatal("containsExactSandboxTag did not match exact boundary tag")
	}
}

func TestStringSliceContainsSubstring(t *testing.T) {
	if !stringSliceContainsSubstring([]string{"needle", "other"}, "hay needle hay") {
		t.Fatal("stringSliceContainsSubstring did not find needle")
	}
	if stringSliceContainsSubstring([]string{"missing", "absent"}, "hay stack") {
		t.Fatal("stringSliceContainsSubstring matched unexpectedly")
	}
}

func TestShouldFilterMacOSSandboxDenialSkipsNonMatchingOperations(t *testing.T) {
	denial := Denial{Operation: "file-read-data", Target: "/private/tmp/foo", Raw: "deny"}
	filter := DenialFilter{
		Ignore: []DenialIgnoreRule{{
			Operations: []string{"mach-lookup"},
			Targets:    []DenialTargetMatcher{{Exact: "/private/tmp/foo"}},
		}},
	}
	if shouldFilterMacOSSandboxDenial(denial, "/bin/cat", filter) {
		t.Fatal("operation mismatch should not filter denial")
	}
}

func TestMacOSDenialDiagnosticsInitializesEmptySessionSuffix(t *testing.T) {
	rt := NewRuntime()
	rt.denials = &macosDenialDiagnostics{}
	got := rt.macosDenialDiagnostics()
	if got.sessionSuffix == "" {
		t.Fatal("macosDenialDiagnostics did not initialize empty session suffix")
	}
}

func TestMacOSDenialRingAddLineIgnoresNonSandboxLines(t *testing.T) {
	ring := &macosDenialRing{}
	ring.addLine([]byte(`{"eventMessage":"kernel: unrelated"}`), "")
	if ring.count() != 0 {
		t.Fatalf("non-sandbox line count = %d, want 0", ring.count())
	}
}

func TestMacOSDenialRingAddLineIgnoresInvalidJSON(t *testing.T) {
	ring := &macosDenialRing{}
	ring.addLine([]byte(`not-json`), "")
	if ring.count() != 0 {
		t.Fatalf("invalid json count = %d, want 0", ring.count())
	}
}

func TestMacOSDenialRingAddLineIgnoresUnrecognizedDenyFormat(t *testing.T) {
	ring := &macosDenialRing{}
	ring.addLine([]byte(`{"eventMessage":"Sandbox: cat allow file-read-data /tmp"}`), "")
	if ring.count() != 0 {
		t.Fatalf("unrecognized deny format count = %d, want 0", ring.count())
	}
}

func TestMacOSDenialRingBufferOverflowEvictsOldest(t *testing.T) {
	ring := &macosDenialRing{}
	for i := 0; i < macosSandboxDenialBufferSize+1; i++ {
		line := []byte(`{"eventMessage":"Sandbox: cat deny(1) file-read-data /private/tmp/` + string(rune('a'+i%26)) + `\nTRPC_RUN_tag_END_0123456789abcdef_SBX"}`)
		ring.addLine(line, "TRPC_RUN_tag_END_0123456789abcdef_SBX")
	}
	if ring.count() != macosSandboxDenialBufferSize {
		t.Fatalf("ring count = %d, want %d", ring.count(), macosSandboxDenialBufferSize)
	}
	events, truncated := ring.snapshot()
	if !truncated {
		t.Fatal("snapshot Truncated = false, want true after overflow")
	}
	first := events[0].denial.Target
	if !strings.HasSuffix(first, "/private/tmp/b") {
		t.Fatalf("oldest event target = %q, want second inserted target", first)
	}
}

func TestMacOSDenialRingWaitForSettleUsesDefaultTimeout(t *testing.T) {
	ring := &macosDenialRing{}
	start := time.Now()
	_ = ring.waitForSettle(context.Background(), 0)
	if time.Since(start) < 250*time.Millisecond {
		t.Fatalf("waitForSettle(0) returned too quickly: %s", time.Since(start))
	}
}

func TestMacOSDenialRingWaitForRunTagSettleEmptyTagUsesSettle(t *testing.T) {
	ring := &macosDenialRing{}
	start := time.Now()
	_ = ring.waitForRunTagSettle(context.Background(), "", 0)
	if time.Since(start) < 250*time.Millisecond {
		t.Fatalf("waitForRunTagSettle empty tag returned too quickly: %s", time.Since(start))
	}
}

func TestMacOSDenialRingWaitForRunTagSettleUsesDefaultTimeout(t *testing.T) {
	ring := &macosDenialRing{}
	start := time.Now()
	_ = ring.waitForRunTagSettle(
		context.Background(),
		"TRPC_RUN_tag_END_0123456789abcdef_SBX",
		0,
	)
	if time.Since(start) < 250*time.Millisecond {
		t.Fatalf("waitForRunTagSettle default timeout returned too quickly: %s", time.Since(start))
	}
}

func TestCollectSandboxDenialsKeepsFullSettleWindowForDelayedSecondEvent(t *testing.T) {
	runTag := "TRPC_RUN_delayed2_END_0123456789abcdef_SBX"
	rt := NewRuntime()
	ring := &macosDenialRing{}
	monitor := &macosLogStreamMonitor{ring: ring}
	d := rt.macosDenialDiagnostics()
	d.mu.Lock()
	d.prodMonitor = monitor
	d.caps = DiagnosticsCapability{
		EventStreamAvailable: true,
		StrongCorrelation:    true,
		ProbeCompleted:       true,
	}
	d.mu.Unlock()

	ring.addLine([]byte(
		`{"eventMessage":"Sandbox: cat deny(1) file-read-data /private/tmp/first\n`+
			runTag+`"}`,
	), runTag)
	go func() {
		time.Sleep(80 * time.Millisecond)
		ring.addLine([]byte(
			`{"eventMessage":"Sandbox: cat deny(1) file-read-data /private/tmp/second\n`+
				runTag+`"}`,
		), runTag)
	}()

	start := time.Now()
	denials, truncated := rt.collectSandboxDenials(
		context.Background(),
		denialRunForMonitor(runTag, monitor, 0),
		"/bin/cat",
		300*time.Millisecond,
	)
	elapsed := time.Since(start)
	if elapsed < 250*time.Millisecond {
		t.Fatalf("collection returned after %s, want full settle window", elapsed)
	}
	if truncated {
		t.Fatal("Truncated = true, want false")
	}
	if len(denials) != 2 {
		t.Fatalf("denials=%#v, want first and delayed second event", denials)
	}
	targets := []string{denials[0].Target, denials[1].Target}
	if targets[0] != "/private/tmp/first" || targets[1] != "/private/tmp/second" {
		t.Fatalf("targets=%v, want [/private/tmp/first /private/tmp/second]", targets)
	}
}

func TestParseMacOSSandboxDenialLogLineInvalidJSON(t *testing.T) {
	_, _, ok := parseMacOSSandboxDenialLogLine([]byte("{"), "")
	if ok {
		t.Fatal("invalid json should not parse")
	}
}

func TestParseMacOSSandboxDenialEventRejectsNonDenyMessage(t *testing.T) {
	_, _, ok := parseMacOSSandboxDenialEvent("kernel: allow file-read-data /tmp", "", "")
	if ok {
		t.Fatal("non-deny message should not parse")
	}
}

func TestParseMacOSSandboxDenialEventMissingSandboxPrefix(t *testing.T) {
	_, _, ok := parseMacOSSandboxDenialEvent("cat deny(1) file-read-data /tmp", "", "")
	if ok {
		t.Fatal("message without Sandbox: prefix should not parse")
	}
}

func TestParseMacOSSandboxDenialEventUnrecognizedDenyFormat(t *testing.T) {
	_, _, ok := parseMacOSSandboxDenialEvent("Sandbox: cat allow file-read-data /tmp", "", "")
	if ok {
		t.Fatal("unrecognized deny format should not parse")
	}
	_, _, ok = parseMacOSSandboxDenialEvent("Sandbox: cat not-a-deny-format", "", "")
	if ok {
		t.Fatal("missing deny() should not parse")
	}
}

func TestParseMacOSLogTimestampReturnsZeroOnFailure(t *testing.T) {
	for _, timestamp := range []string{"", "not-a-timestamp"} {
		if got := parseMacOSLogTimestamp(timestamp); !got.IsZero() {
			t.Fatalf("timestamp %q parsed as %v, want zero time", timestamp, got)
		}
	}
}

func TestParseMacOSLogTimestampRecognizesEventTime(t *testing.T) {
	const timestamp = "2026-07-28 11:10:12.123456+0800"
	got := parseMacOSLogTimestamp(timestamp)
	if got.IsZero() {
		t.Fatalf("timestamp %q parsed as zero time", timestamp)
	}
	if got.Format("2006-01-02 15:04:05.999999-0700") != timestamp {
		t.Fatalf("timestamp parsed as %v, want %q", got, timestamp)
	}
}

func TestProbeMatchedSkipsMismatchedTags(t *testing.T) {
	events := []macosSandboxDenialEvent{{
		denial: Denial{
			Operation: "file-read-data",
			Target:    "/private/tmp/foo",
			Raw:       "Sandbox: cat deny(1) file-read-data /private/tmp/foo\nTRPC_RUN_other_END_0123456789abcdef_SBX",
		},
	}}
	if probeMatched(events, probeExpectation{
		Tag:       "TRPC_RUN_want_END_0123456789abcdef_SBX",
		Operation: "file-read-data",
		Target:    "/private/tmp/foo",
	}) {
		t.Fatal("probeMatched should ignore events with different tags")
	}
}

func TestProbeOperationMatchesRejectsUnrelatedOperation(t *testing.T) {
	if probeOperationMatches("mach-lookup", "file-read*") {
		t.Fatal("probeOperationMatches accepted unrelated operation")
	}
}

func TestProbeTargetMatchesUsesCanonicalPaths(t *testing.T) {
	name := "trpc-probe-canonical-" + randomHex(4)
	logged := filepath.Join("/tmp", name)
	if err := os.WriteFile(logged, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(logged) })
	expected := filepath.Join("/private/tmp", name)
	if !probeTargetMatches(logged, expected) {
		t.Fatalf("probeTargetMatches(%q, %q) = false, want true", logged, expected)
	}
	if probeTargetMatches(filepath.Join("/tmp", "missing-"+randomHex(4)), expected) {
		t.Fatal("probeTargetMatches accepted missing path")
	}
	if probeTargetMatches("/definitely/missing/path", expected) {
		t.Fatal("probeTargetMatches accepted invalid logged path")
	}
	if probeTargetMatches(logged, "/definitely/missing/path") {
		t.Fatal("probeTargetMatches accepted invalid expected path")
	}
}

func TestCanonicalizeProbeTargetPathJoinsParentAndBase(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "child")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := canonicalizeProbeTargetPath(target)
	if err != nil {
		t.Fatalf("canonicalizeProbeTargetPath: %v", err)
	}
	want, err := canonicalizeExistingPath(target)
	if err != nil {
		t.Fatalf("canonicalizeExistingPath: %v", err)
	}
	if got != want {
		t.Fatalf("canonicalizeProbeTargetPath = %q, want %q", got, want)
	}
}

func TestShouldFilterMacOSSandboxDenialSkipsNonMatchingRawContains(t *testing.T) {
	denial := Denial{Operation: "file-read-data", Target: "/private/tmp/foo", Raw: "keep me"}
	filter := DenialFilter{
		Ignore: []DenialIgnoreRule{{
			RawContains: []string{"drop me"},
		}},
	}
	if shouldFilterMacOSSandboxDenial(denial, "/bin/cat", filter) {
		t.Fatal("raw mismatch should not filter denial")
	}
}

func TestShouldFilterMacOSSandboxDenialIgnoresEmptyRawContains(t *testing.T) {
	denial := Denial{
		Operation: "file-read-data",
		Target:    "/private/tmp/foo",
		Raw:       "keep me",
	}
	if shouldFilterMacOSSandboxDenial(denial, "/bin/cat", DenialFilter{
		Ignore: []DenialIgnoreRule{{RawContains: []string{"", ""}}},
	}) {
		t.Fatal("all-empty RawContains should not filter denial")
	}
	if !shouldFilterMacOSSandboxDenial(denial, "/bin/cat", DenialFilter{
		Ignore: []DenialIgnoreRule{{
			Operations:  []string{"file-read-data"},
			RawContains: []string{""},
		}},
	}) {
		t.Fatal("all-empty RawContains should be unset when another constraint matches")
	}
	if !shouldFilterMacOSSandboxDenial(denial, "/bin/cat", DenialFilter{
		Ignore: []DenialIgnoreRule{{RawContains: []string{"", "keep"}}},
	}) {
		t.Fatal("empty RawContains entry should not prevent a non-empty match")
	}
}

func TestContainsExactSandboxTagEmptyTagReturnsFalse(t *testing.T) {
	if containsExactSandboxTag("Sandbox: deny\nTRPC_RUN_tag_END_0123456789abcdef_SBX", "") {
		t.Fatal("empty tag should never match")
	}
}

func TestProbeOperationMatchesAcceptsFileReadWildcard(t *testing.T) {
	for _, op := range []string{"file-read-data", "file-test-existence", "file-map-executable"} {
		if !probeOperationMatches(op, "file-read*") {
			t.Fatalf("probeOperationMatches(%q, file-read*) = false, want true", op)
		}
	}
}

func TestProbeMatchedSkipsMismatchedOperations(t *testing.T) {
	tag := "TRPC_RUN_want_END_0123456789abcdef_SBX"
	events := []macosSandboxDenialEvent{{
		denial: Denial{
			Operation: "mach-lookup",
			Target:    "/private/tmp/foo",
			Raw:       "Sandbox: cat deny(1) mach-lookup /private/tmp/foo\n" + tag,
		},
	}}
	if probeMatched(events, probeExpectation{
		Tag:       tag,
		Operation: "file-read*",
		Target:    "/private/tmp/foo",
	}) {
		t.Fatal("probeMatched should ignore events with different operations")
	}
}

func TestParseMacOSSandboxDenialEventUsesRunTagForTagging(t *testing.T) {
	runTag := "TRPC_RUN_tag_END_0123456789abcdef_SBX"
	denial, tagged, ok := parseMacOSSandboxDenialEvent(
		"Sandbox: cat deny(1) file-read-data /private/tmp/foo\n"+runTag,
		"",
		runTag,
	)
	if !ok || !tagged || denial.Operation != "file-read-data" {
		t.Fatalf("parseMacOSSandboxDenialEvent = %#v tagged=%v ok=%v", denial, tagged, ok)
	}
}

func TestMacOSLogStreamMonitorStopTimesOutWhenDoneNeverCloses(t *testing.T) {
	monitor := &macosLogStreamMonitor{
		cancel:      func() {},
		done:        make(chan struct{}),
		stopTimeout: 50 * time.Millisecond,
	}
	start := time.Now()
	err := monitor.stop()
	if err == nil {
		t.Fatal("stop() = nil, want timeout error")
	}
	if !strings.Contains(err.Error(), "timed out stopping macOS log stream monitor") {
		t.Fatalf("stop() error = %v, want timed out stopping macOS log stream monitor", err)
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Fatalf("stop returned too quickly: %s", time.Since(start))
	}
}

func TestRuntimeCloseStopsDenialMonitor(t *testing.T) {
	resetDiagnosticsCapsCacheForTest()
	t.Cleanup(resetDiagnosticsCapsCacheForTest)
	storeCachedDiagnosticsCaps(context.Background(), DiagnosticsCapability{
		Supported:            true,
		ProbeCompleted:       true,
		EventStreamAvailable: true,
		StrongCorrelation:    true,
	})
	rt := NewRuntime()
	t.Cleanup(func() { _ = rt.Close() })
	if err := rt.ensureDenialMonitor(context.Background()); err != nil {
		t.Fatalf("ensureDenialMonitor: %v", err)
	}
	if !rt.sandboxDenialCollectingReady() {
		t.Skip("log stream unavailable on this host")
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if rt.sandboxDenialCollectingReady() {
		t.Fatal("sandboxDenialCollectingReady = true after Close, want false")
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestLiveProdMonitorClearsDeadMonitorAndRestarts(t *testing.T) {
	resetDiagnosticsCapsCacheForTest()
	t.Cleanup(resetDiagnosticsCapsCacheForTest)
	storeCachedDiagnosticsCaps(context.Background(), DiagnosticsCapability{
		Supported:            true,
		ProbeCompleted:       true,
		EventStreamAvailable: true,
		StrongCorrelation:    true,
	})
	rt := NewRuntime()
	t.Cleanup(func() { _ = rt.Close() })
	done := make(chan struct{})
	close(done)
	d := rt.macosDenialDiagnostics()
	d.mu.Lock()
	d.prodMonitor = &macosLogStreamMonitor{done: done, ring: &macosDenialRing{}}
	d.caps = DiagnosticsCapability{
		EventStreamAvailable: true,
		StrongCorrelation:    true,
		ProbeCompleted:       true,
	}
	d.state = macosDenialRunning
	d.mu.Unlock()
	if rt.sandboxDenialCollectingReady() {
		t.Fatal("sandboxDenialCollectingReady = true for closed done channel")
	}
	caps := rt.DiagnosticsCapability()
	if caps.EventStreamAvailable {
		t.Fatalf("caps.EventStreamAvailable = true after dead monitor, want false")
	}
	d.mu.RLock()
	state := d.state
	d.mu.RUnlock()
	if state != macosDenialIdle {
		t.Fatalf("diagnostics state = %v after monitor death, want idle", state)
	}
	if err := rt.ensureDenialMonitor(context.Background()); err != nil {
		t.Fatalf("ensureDenialMonitor after monitor death: %v", err)
	}
	if !rt.sandboxDenialCollectingReady() {
		t.Skip("log stream unavailable on this host")
	}
}

func TestEnsureDenialMonitorDoesNotRestartAfterClose(t *testing.T) {
	rt := NewRuntime()
	d := rt.macosDenialDiagnostics()
	d.mu.Lock()
	d.prodMonitor = &macosLogStreamMonitor{ring: &macosDenialRing{}}
	d.caps.EventStreamAvailable = true
	d.mu.Unlock()
	if err := rt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := rt.ensureDenialMonitor(context.Background()); err != nil {
		t.Fatalf("ensureDenialMonitor after Close: %v", err)
	}
	if rt.sandboxDenialCollectingReady() {
		t.Fatal("sandboxDenialCollectingReady = true after Close")
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.prodMonitor != nil {
		t.Fatal("ensureDenialMonitor restarted monitor after Close")
	}
}

func TestCollectSandboxDenialsReportsTruncated(t *testing.T) {
	runTag := "TRPC_RUN_trunc_END_0123456789abcdef_SBX"
	rt := NewRuntime()
	ring := &macosDenialRing{}
	for i := 0; i < macosSandboxDenialBufferSize+1; i++ {
		line := []byte(
			`{"eventMessage":"Sandbox: cat deny(1) file-read-data /private/tmp/x` +
				string(rune('a'+i%26)) + `\n` + runTag + `"}`,
		)
		ring.addLine(line, runTag)
	}
	d := rt.macosDenialDiagnostics()
	d.mu.Lock()
	monitor := &macosLogStreamMonitor{ring: ring}
	d.prodMonitor = monitor
	d.caps = DiagnosticsCapability{
		EventStreamAvailable: true,
		StrongCorrelation:    true,
		ProbeCompleted:       true,
	}
	d.mu.Unlock()
	denials, truncated := rt.collectSandboxDenials(
		context.Background(),
		denialRunForMonitor(runTag, monitor, 0),
		"/bin/cat",
		time.Millisecond,
	)
	if !truncated {
		t.Fatal("truncated = false, want true after ring overflow")
	}
	if len(denials) == 0 {
		t.Fatal("denials empty after overflow, want remaining tagged events")
	}
}

func TestEnsureDenialMonitorHonorsCanceledContext(t *testing.T) {
	resetDiagnosticsCapsCacheForTest()
	t.Cleanup(resetDiagnosticsCapsCacheForTest)
	rt := NewRuntime()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := rt.ensureDenialMonitor(ctx)
	if err == nil {
		t.Fatal("ensureDenialMonitor with canceled context returned nil error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ensureDenialMonitor error = %v, want context.Canceled", err)
	}
}

func TestEnsureDenialMonitorHonorsCancellationWhileInitializationBusy(t *testing.T) {
	rt := NewRuntime()
	d := rt.macosDenialDiagnostics()
	if err := d.lockInit(context.Background()); err != nil {
		t.Fatalf("lockInit: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- rt.ensureDenialMonitor(ctx)
	}()
	<-started
	cancel()

	select {
	case err := <-done:
		d.unlockInit()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ensureDenialMonitor error = %v, want context.Canceled", err)
		}
	case <-time.After(100 * time.Millisecond):
		d.unlockInit()
		err := <-done
		t.Fatalf(
			"ensureDenialMonitor stayed blocked after cancellation; eventual error = %v",
			err,
		)
	}
}

func TestRuntimeCloseRetainsMonitorWhenStopDoesNotComplete(t *testing.T) {
	rt := NewRuntime()
	d := rt.macosDenialDiagnostics()
	monitor := &macosLogStreamMonitor{
		cancel: func() {},
		done:   make(chan struct{}),
		ring:   &macosDenialRing{},
	}
	d.mu.Lock()
	d.prodMonitor = monitor
	d.caps.EventStreamAvailable = true
	d.mu.Unlock()

	if err := rt.Close(); err == nil {
		t.Fatal("Close returned nil when monitor did not stop")
	}
	d.mu.RLock()
	if d.prodMonitor != monitor {
		d.mu.RUnlock()
		t.Fatal("Close discarded ownership of a monitor that did not stop")
	}
	if d.state != macosDenialClosed {
		d.mu.RUnlock()
		t.Fatal("Close did not leave diagnostics in terminal state")
	}
	d.mu.RUnlock()
	if err := rt.ensureDenialMonitor(context.Background()); err != nil {
		t.Fatalf("ensureDenialMonitor after failed Close: %v", err)
	}
	close(monitor.done)
	if err := rt.Close(); err != nil {
		t.Fatalf("retry Close after monitor exit: %v", err)
	}
}

func TestRuntimeCloseRetainsInitializationMonitorWhenStopDoesNotComplete(
	t *testing.T,
) {
	rt := NewRuntime()
	d := rt.macosDenialDiagnostics()
	monitor := &macosLogStreamMonitor{
		cancel: func() {},
		done:   make(chan struct{}),
		ring:   &macosDenialRing{},
	}
	d.mu.Lock()
	d.initMonitor = monitor
	d.mu.Unlock()

	if err := rt.Close(); err == nil {
		t.Fatal("Close returned nil when initialization monitor did not stop")
	}
	d.mu.RLock()
	if d.initMonitor != monitor {
		d.mu.RUnlock()
		t.Fatal("Close discarded ownership of an initialization monitor")
	}
	d.mu.RUnlock()

	close(monitor.done)
	if err := rt.Close(); err != nil {
		t.Fatalf("retry Close after initialization monitor exit: %v", err)
	}
}

func TestReleaseInitializationMonitorRetainsOwnershipOnTimeout(t *testing.T) {
	rt := NewRuntime()
	d := rt.macosDenialDiagnostics()
	monitor := &macosLogStreamMonitor{
		cancel: func() {},
		done:   make(chan struct{}),
		ring:   &macosDenialRing{},
	}

	if err := releaseInitializationMonitor(d, monitor); err == nil {
		t.Fatal("releaseInitializationMonitor returned nil when stop timed out")
	}
	d.mu.RLock()
	if d.initMonitor != monitor || d.monitorErr == nil {
		d.mu.RUnlock()
		t.Fatal("failed release did not retain initialization monitor ownership")
	}
	d.mu.RUnlock()

	close(monitor.done)
	if err := rt.Close(); err != nil {
		t.Fatalf("Close after initialization monitor exit: %v", err)
	}
}

func TestInstallDenialMonitorRetainsOwnershipWhenClosedStopTimesOut(t *testing.T) {
	rt := NewRuntime()
	d := rt.macosDenialDiagnostics()
	monitor := &macosLogStreamMonitor{
		cancel: func() {},
		done:   make(chan struct{}),
		ring:   &macosDenialRing{},
	}
	d.mu.Lock()
	d.state = macosDenialClosed
	d.mu.Unlock()

	if err := installDenialMonitor(
		d,
		DiagnosticsCapability{EventStreamAvailable: true},
		monitor,
	); err == nil {
		t.Fatal("installDenialMonitor returned nil when stop did not complete")
	}
	d.mu.RLock()
	if d.prodMonitor != monitor || d.monitorErr == nil {
		d.mu.RUnlock()
		t.Fatal("failed stop did not retain monitor ownership and error")
	}
	d.mu.RUnlock()

	close(monitor.done)
	if err := rt.Close(); err != nil {
		t.Fatalf("Close after monitor exit: %v", err)
	}
}

func TestRuntimeCloseJoinsInFlightCloseAttempt(t *testing.T) {
	rt := NewRuntime()
	d := rt.macosDenialDiagnostics()
	wantErr := errors.New("shared close result")
	attempt := &macosDenialCloseAttempt{done: make(chan struct{})}
	d.mu.Lock()
	d.closeAttempt = attempt
	d.mu.Unlock()

	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		result <- rt.Close()
	}()
	<-started
	attempt.err = wantErr
	close(attempt.done)
	if err := <-result; !errors.Is(err, wantErr) {
		t.Fatalf("concurrent Close error = %v, want shared result", err)
	}

	d.mu.Lock()
	d.closeAttempt = nil
	d.mu.Unlock()
}

func TestRuntimeCloseIsBoundedWhileInitializationBusy(t *testing.T) {
	rt := NewRuntime()
	d := rt.macosDenialDiagnostics()
	if err := d.lockInit(context.Background()); err != nil {
		t.Fatalf("lockInit: %v", err)
	}
	canceled := make(chan struct{}, 1)
	d.mu.Lock()
	d.initCancel = func() {
		select {
		case canceled <- struct{}{}:
		default:
		}
	}
	d.mu.Unlock()

	start := time.Now()
	err := rt.Close()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Close returned nil while initialization gate remained held")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("Close took %s, want a bounded return", elapsed)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("Close did not cancel in-flight initialization")
	}
	if err := rt.ensureDenialMonitor(context.Background()); err != nil {
		t.Fatalf("ensureDenialMonitor after Close: %v", err)
	}
	d.unlockInit()
	if err := rt.Close(); err != nil {
		t.Fatalf("retry Close after initialization completed: %v", err)
	}
}

func TestMacOSDenialRingTruncationUsesRunBaseline(t *testing.T) {
	ring := &macosDenialRing{}
	runTag := "TRPC_RUN_baseline_END_0123456789abcdef_SBX"
	for i := 0; i < macosSandboxDenialBufferSize+1; i++ {
		ring.addLine([]byte(
			`{"eventMessage":"Sandbox: cat deny(1) file-read-data /tmp/old`+
				string(rune('a'+i%26))+`"}`,
		), "")
	}
	baseline := ring.dropCount()
	if _, truncated := ring.snapshotSince(baseline); truncated {
		t.Fatal("historical drops marked a new run truncated")
	}
	ring.addLine([]byte(
		`{"eventMessage":"Sandbox: cat deny(1) file-read-data /tmp/current\n`+
			runTag+`"}`,
	), runTag)
	if _, truncated := ring.snapshotSince(baseline); !truncated {
		t.Fatal("drop after run baseline was not reported")
	}
}

func TestCollectSandboxDenialsHonorsCanceledContext(t *testing.T) {
	rt := NewRuntime()
	monitor := &macosLogStreamMonitor{ring: &macosDenialRing{}}
	d := rt.macosDenialDiagnostics()
	d.mu.Lock()
	d.prodMonitor = monitor
	d.caps.EventStreamAvailable = true
	d.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, _ = rt.collectSandboxDenials(
		ctx,
		denialRunForMonitor(
			"TRPC_RUN_canceled_END_0123456789abcdef_SBX",
			monitor,
			0,
		),
		"/bin/cat",
		2*time.Second,
	)
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("collection ignored canceled context and took %s", elapsed)
	}
}

func TestCollectSandboxDenialsUsesRunStartMonitorAfterRestart(t *testing.T) {
	runTag := "TRPC_RUN_gen_END_0123456789abcdef_SBX"
	rt := NewRuntime()

	oldRing := &macosDenialRing{}
	oldRing.addLine([]byte(
		`{"eventMessage":"Sandbox: cat deny(1) file-read-data /private/tmp/keep\n`+
			runTag+`"}`,
	), runTag)
	for i := 0; i < macosSandboxDenialBufferSize; i++ {
		oldRing.addLine([]byte(
			`{"eventMessage":"Sandbox: cat deny(1) file-read-data /private/tmp/x`+
				string(rune('a'+i%26))+`\n`+runTag+`"}`,
		), runTag)
	}
	oldDone := make(chan struct{})
	close(oldDone)
	oldMonitor := &macosLogStreamMonitor{ring: oldRing, done: oldDone}

	newReady := make(chan struct{})
	close(newReady)
	newMonitor := &macosLogStreamMonitor{
		ring:  &macosDenialRing{},
		ready: newReady,
		done:  make(chan struct{}),
	}

	d := rt.macosDenialDiagnostics()
	d.mu.Lock()
	d.prodMonitor = newMonitor
	d.caps.EventStreamAvailable = true
	d.caps.StrongCorrelation = true
	d.state = macosDenialRunning
	d.mu.Unlock()

	denials, truncated := rt.collectSandboxDenials(
		context.Background(),
		denialRunForMonitor(runTag, oldMonitor, 0),
		"/bin/cat",
		time.Millisecond,
	)
	if len(denials) == 0 {
		t.Fatal("denials empty after monitor restart, want events from run-start ring")
	}
	if !truncated {
		t.Fatal("truncated = false, want true from run-start ring overflow")
	}
	if !rt.sandboxDenialCollectingReady() {
		t.Fatal("replacement monitor should keep collecting ready")
	}
}

func TestCollectSandboxDenialsReapsDeadCurrentMonitor(t *testing.T) {
	runTag := "TRPC_RUN_reap_END_0123456789abcdef_SBX"
	rt := NewRuntime()
	ring := &macosDenialRing{}
	ring.addLine([]byte(
		`{"eventMessage":"Sandbox: cat deny(1) file-read-data /private/tmp/keep\n`+
			runTag+`"}`,
	), runTag)
	done := make(chan struct{})
	close(done)
	monitor := &macosLogStreamMonitor{ring: ring, done: done}

	d := rt.macosDenialDiagnostics()
	d.mu.Lock()
	d.prodMonitor = monitor
	d.caps.EventStreamAvailable = true
	d.state = macosDenialRunning
	d.mu.Unlock()

	denials, _ := rt.collectSandboxDenials(
		context.Background(),
		denialRunForMonitor(runTag, monitor, 0),
		"/bin/cat",
		time.Millisecond,
	)
	if len(denials) != 1 || denials[0].Target != "/private/tmp/keep" {
		t.Fatalf("denials=%#v, want event from dead monitor ring", denials)
	}
	if rt.sandboxDenialCollectingReady() {
		t.Fatal("sandboxDenialCollectingReady = true after reaping dead monitor")
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.prodMonitor != nil {
		t.Fatal("prodMonitor still set after reaping dead monitor")
	}
}

func TestCollectSandboxDenialsSettlesWithFreshContextAfterCancel(t *testing.T) {
	runTag := "TRPC_RUN_freshctx_END_0123456789abcdef_SBX"
	rt := NewRuntime()
	ring := &macosDenialRing{}
	monitor := &macosLogStreamMonitor{ring: ring}
	d := rt.macosDenialDiagnostics()
	d.mu.Lock()
	d.prodMonitor = monitor
	d.caps.EventStreamAvailable = true
	d.mu.Unlock()

	runCtx, cancel := context.WithCancel(context.Background())
	cancel()
	go func() {
		time.Sleep(40 * time.Millisecond)
		ring.addLine([]byte(
			`{"eventMessage":"Sandbox: cat deny(1) file-read-data /private/tmp/keep\n`+
				runTag+`"}`,
		), runTag)
	}()

	collectCtx, collectCancel := context.WithTimeout(
		context.Background(),
		200*time.Millisecond,
	)
	defer collectCancel()
	denials, _ := rt.collectSandboxDenials(
		collectCtx,
		denialRunForMonitor(runTag, monitor, 0),
		"/bin/cat",
		200*time.Millisecond,
	)
	if len(denials) != 1 || denials[0].Target != "/private/tmp/keep" {
		t.Fatalf("denials=%#v, want delayed event under fresh collect context", denials)
	}

	start := time.Now()
	_, _ = rt.collectSandboxDenials(
		runCtx,
		denialRunForMonitor(runTag, monitor, 0),
		"/bin/cat",
		200*time.Millisecond,
	)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("canceled run context collection took %s, want immediate return", elapsed)
	}
}

func TestDiagnosticsCapabilityFromProbeAllowsNeitherTaggable(t *testing.T) {
	// Completed false/false is a valid cached probe result. Activation must
	// preserve the report without starting production collection; see
	// TestInitDenialMonitorSkipsCollectionWhenNeitherFormTaggable.
	caps := diagnosticsCapabilityFromProbe(true, false, false)
	if !caps.Supported || !caps.EventStreamAvailable || !caps.ProbeCompleted {
		t.Fatalf("caps=%+v, want completed stream-available probe", caps)
	}
	if caps.DefaultDenyTaggable || caps.ExplicitDenyTaggable || caps.StrongCorrelation {
		t.Fatalf("caps=%+v, want both tag forms false", caps)
	}
	incomplete := diagnosticsCapabilityFromProbe(false, true, true)
	if incomplete.EventStreamAvailable || incomplete.ProbeCompleted ||
		incomplete.DefaultDenyTaggable || incomplete.ExplicitDenyTaggable {
		t.Fatalf("incomplete caps=%+v, want only Supported", incomplete)
	}
}

func TestWaitForProbeFormAcceptsDelayedSecondMatch(t *testing.T) {
	ring := &macosDenialRing{}
	defaultTag := "TRPC_RUN_PROBE_D_delayed_END_abc_PROBE_SBX"
	explicitTag := "TRPC_RUN_PROBE_E_delayed_END_abc_PROBE_SBX"
	defaultPath := "/private/tmp/.trpc_sbx_probe/default_target"
	explicitPath := "/private/tmp/.trpc_sbx_probe/explicit_target"
	defaultExp := probeExpectation{
		Tag:       defaultTag,
		Operation: "file-read*",
		Target:    defaultPath,
	}
	explicitExp := probeExpectation{
		Tag:       explicitTag,
		Operation: "file-read*",
		Target:    explicitPath,
	}

	ring.addLine([]byte(
		`{"eventMessage":"Sandbox: cat(1) deny(1) file-read-data `+defaultPath+`\n`+
			defaultTag+`"}`,
	), defaultTag)

	go func() {
		time.Sleep(80 * time.Millisecond)
		ring.addLine([]byte(
			`{"eventMessage":"Sandbox: cat(1) deny(1) file-read-data `+explicitPath+`\n`+
				explicitTag+`"}`,
		), explicitTag)
	}()

	// Old settle behavior would finalize after the first event + 50ms idle and
	// miss the delayed explicit event. Independent waits must still observe it.
	defaultRes, explicitRes, err := waitForProbeFormCapabilities(
		context.Background(),
		ring,
		defaultExp,
		explicitExp,
		250*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("waitForProbeFormCapabilities: %v", err)
	}
	if !defaultRes.tagMatched || !explicitRes.tagMatched {
		t.Fatalf(
			"matched default=%+v explicit=%+v, want both tagged after delayed second event",
			defaultRes,
			explicitRes,
		)
	}
}

func TestWaitForProbeFormTimesOutWithoutTarget(t *testing.T) {
	ring := &macosDenialRing{}
	ring.addLine([]byte(
		`{"eventMessage":"Sandbox: cat(1) deny(1) file-read-data /tmp/other\n`+
			`TRPC_RUN_PROBE_D_other_END_abc_PROBE_SBX"}`,
	), "TRPC_RUN_PROBE_D_other_END_abc_PROBE_SBX")
	start := time.Now()
	form, err := ring.waitForProbeForm(
		context.Background(),
		probeExpectation{
			Tag:       "TRPC_RUN_PROBE_E_missing_END_abc_PROBE_SBX",
			Operation: "file-read*",
			Target:    "/private/tmp/.trpc_sbx_probe/explicit_target",
		},
		80*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("waitForProbeForm: %v", err)
	}
	if form.targetObserved || form.tagMatched {
		t.Fatalf("waitForProbeForm = %+v, want no target evidence", form)
	}
	if elapsed := time.Since(start); elapsed < 70*time.Millisecond {
		t.Fatalf("waitForProbeForm returned too quickly: %s", elapsed)
	}
}

func TestWaitForProbeFormReportsUntaggedTargetAsNotTaggable(t *testing.T) {
	ring := &macosDenialRing{}
	defaultPath := "/private/tmp/.trpc_sbx_probe/default_target"
	explicitPath := "/private/tmp/.trpc_sbx_probe/explicit_target"
	ring.addLine([]byte(
		`{"eventMessage":"Sandbox: cat(1) deny(1) file-read-data `+defaultPath+`"}`,
	), "")
	ring.addLine([]byte(
		`{"eventMessage":"Sandbox: cat(1) deny(1) file-read-data `+explicitPath+`"}`,
	), "")
	defaultRes, explicitRes, err := waitForProbeFormCapabilities(
		context.Background(),
		ring,
		probeExpectation{
			Tag:       "TRPC_RUN_PROBE_D_untagged_END_abc_PROBE_SBX",
			Operation: "file-read*",
			Target:    defaultPath,
		},
		probeExpectation{
			Tag:       "TRPC_RUN_PROBE_E_untagged_END_abc_PROBE_SBX",
			Operation: "file-read*",
			Target:    explicitPath,
		},
		50*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("waitForProbeFormCapabilities: %v", err)
	}
	if !defaultRes.targetObserved || defaultRes.tagMatched {
		t.Fatalf("default form=%+v, want target without tag", defaultRes)
	}
	if !explicitRes.targetObserved || explicitRes.tagMatched {
		t.Fatalf("explicit form=%+v, want target without tag", explicitRes)
	}
	caps := diagnosticsCapabilityFromProbe(
		true,
		defaultRes.tagMatched,
		explicitRes.tagMatched,
	)
	if !caps.ProbeCompleted || caps.DefaultDenyTaggable || caps.ExplicitDenyTaggable {
		t.Fatalf("caps=%+v, want completed false/false", caps)
	}
}

func TestProbeIncompleteWithoutTargetEvidence(t *testing.T) {
	ring := &macosDenialRing{}
	// Ambient Sandbox noise without the probe targets must not complete probe.
	ring.addLine([]byte(
		`{"eventMessage":"Sandbox: other(1) deny(1) file-read-data /private/tmp/unrelated"}`,
	), "")
	defaultRes, explicitRes, err := waitForProbeFormCapabilities(
		context.Background(),
		ring,
		probeExpectation{
			Tag:       "TRPC_RUN_PROBE_D_noise_END_abc_PROBE_SBX",
			Operation: "file-read*",
			Target:    "/private/tmp/.trpc_sbx_probe/default_target",
		},
		probeExpectation{
			Tag:       "TRPC_RUN_PROBE_E_noise_END_abc_PROBE_SBX",
			Operation: "file-read*",
			Target:    "/private/tmp/.trpc_sbx_probe/explicit_target",
		},
		40*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("waitForProbeFormCapabilities: %v", err)
	}
	if defaultRes.targetObserved || explicitRes.targetObserved {
		t.Fatalf(
			"ambient noise produced target evidence: default=%+v explicit=%+v",
			defaultRes,
			explicitRes,
		)
	}
}

func TestStartMacOSProbeLogStreamMonitorPredicateIsTargetScoped(t *testing.T) {
	suffix := "_END_abc_PROBE_SBX"
	defaultPath := "/private/tmp/.trpc_sbx_probe_xyz/default_target"
	explicitPath := "/private/tmp/.trpc_sbx_probe_xyz/explicit_target"
	predicate := macosProbeLogStreamPredicate(suffix, defaultPath, explicitPath)
	if strings.Contains(predicate, `CONTAINS "Sandbox: "`) {
		t.Fatalf("probe predicate still accepts ambient Sandbox traffic: %s", predicate)
	}
	if !strings.Contains(predicate, defaultPath) || !strings.Contains(predicate, explicitPath) {
		t.Fatalf("probe predicate missing target paths: %s", predicate)
	}
	if !strings.Contains(predicate, suffix) {
		t.Fatalf("probe predicate missing suffix: %s", predicate)
	}
}
