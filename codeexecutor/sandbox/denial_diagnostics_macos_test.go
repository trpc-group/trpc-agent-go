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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
			Scope:   DenialFilterAll,
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
			Scope:   DenialFilterAll,
			Command: "gh",
			Targets: []DenialTargetMatcher{{Exact: "/private/tmp/foo"}},
		}},
	})
	if len(filtered) != 0 {
		t.Fatalf("command-pattern filter = %#v, want empty", filtered)
	}
	kept := applyMacOSSandboxDenialFilters(denials, "/bin/cat", DenialFilter{
		Ignore: []DenialIgnoreRule{{
			Scope:   DenialFilterAll,
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
	denials := []Denial{
		{Operation: "file-read-data", Target: "/private/tmp/foo", Raw: "first"},
		{Operation: "file-read-data", Target: "/private/tmp/foo", Raw: "second"},
		{Operation: "file-read-metadata", Target: "/private/tmp/foo", Raw: "third"},
	}
	filtered := applyMacOSSandboxDenialFilters(denials, "/bin/cat", DenialFilter{})
	if len(filtered) != 2 {
		t.Fatalf("deduped denials = %#v, want two operation+target pairs", filtered)
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

func TestSandboxDenialFilterScopeMismatchDoesNotApply(t *testing.T) {
	denials := []Denial{{
		Operation: "file-read-data",
		Target:    "/private/tmp/foo",
	}}
	filtered := applyMacOSSandboxDenialFilters(denials, "/bin/cat", DenialFilter{
		Ignore: []DenialIgnoreRule{{
			Scope:   DenialFilterScope("other"),
			Targets: []DenialTargetMatcher{{Exact: "/private/tmp/foo"}},
		}},
	})
	if len(filtered) != 1 {
		t.Fatalf("scope mismatch filter = %#v, want original denial", filtered)
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
		Ignore: []DenialIgnoreRule{{Scope: DenialFilterDenials}},
	})
	if len(filtered) != 1 {
		t.Fatalf("empty ignore rule = %#v, want original denial", filtered)
	}
}

func TestSandboxDenialFilterDenialsScope(t *testing.T) {
	denials := []Denial{{
		Operation: "file-read-data",
		Target:    "/private/tmp/foo",
	}}
	if !macosDenialFilterScopeMatches(DenialFilterDenials, DenialFilterDenials) {
		t.Fatal("denials scope should match denials output")
	}
	if !macosDenialFilterScopeMatches("", DenialFilterDenials) {
		t.Fatal("empty scope should match denials output")
	}
	if macosDenialFilterScopeMatches(DenialFilterScope("other"), DenialFilterDenials) {
		t.Fatal("unknown scope should not match denials output")
	}
	filtered := applyMacOSSandboxDenialFilters(denials, "/bin/cat", DenialFilter{
		Ignore: []DenialIgnoreRule{{
			Scope:   DenialFilterDenials,
			Targets: []DenialTargetMatcher{{Exact: "/private/tmp/foo"}},
		}},
	})
	if filtered != nil {
		t.Fatalf("denials scope filter = %#v, want nil", filtered)
	}
}

func TestSandboxDenialInvalidGlobDoesNotMatch(t *testing.T) {
	if macosDenialTargetMatches("/private/tmp/foo", []DenialTargetMatcher{{Glob: "[invalid"}}) {
		t.Fatal("invalid glob should not match target")
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
	d.prodMonitor = &macosLogStreamMonitor{ring: ring}
	d.caps = DiagnosticsCapability{
		EventStreamAvailable: true,
		StrongCorrelation:    true,
		ProbeCompleted:       true,
	}
	d.mu.Unlock()

	denials := rt.collectSandboxDenials(runTag, "/bin/cat", time.Millisecond)
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
	d.prodMonitor = &macosLogStreamMonitor{ring: ring}
	d.caps = DiagnosticsCapability{
		EventStreamAvailable: true,
		StrongCorrelation:    true,
		ProbeCompleted:       true,
	}
	d.mu.Unlock()

	denials := rt.collectSandboxDenials(runTag, "/bin/cat", time.Millisecond)
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

func TestInitDenialMonitorHonorsCachedCapsWithoutEventStream(t *testing.T) {
	resetDiagnosticsCapsCacheForTest()
	t.Cleanup(resetDiagnosticsCapsCacheForTest)
	storeCachedDiagnosticsCaps(DiagnosticsCapability{
		Supported:            true,
		ProbeCompleted:       true,
		EventStreamAvailable: false,
	})

	rt := NewRuntime()
	if err := rt.ensureDenialMonitor(); err != nil {
		t.Fatalf("ensureDenialMonitor: %v", err)
	}
	if rt.sandboxDenialCollectingReady() {
		t.Fatal("sandboxDenialCollectingReady = true, want false for cached unavailable stream")
	}
	caps := rt.DiagnosticsCapability()
	if caps.EventStreamAvailable || !caps.ProbeCompleted || !caps.Supported {
		t.Fatalf("caps = %#v, want supported probed cache without event stream", caps)
	}
}

func TestInitDenialMonitorUsesCachedCapsWithAvailableEventStream(t *testing.T) {
	resetDiagnosticsCapsCacheForTest()
	t.Cleanup(resetDiagnosticsCapsCacheForTest)
	storeCachedDiagnosticsCaps(DiagnosticsCapability{
		Supported:            true,
		ProbeCompleted:       true,
		EventStreamAvailable: true,
		StrongCorrelation:    true,
	})

	rt := NewRuntime()
	if err := rt.ensureDenialMonitor(); err != nil {
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
	if shouldFilterMacOSSandboxDenial(denial, "/bin/cat", filter, DenialFilterDenials) {
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
	first := ring.snapshot()[0].denial.Target
	if !strings.HasSuffix(first, "/private/tmp/b") {
		t.Fatalf("oldest event target = %q, want second inserted target", first)
	}
}

func TestMacOSDenialRingWaitForSettleUsesDefaultTimeout(t *testing.T) {
	ring := &macosDenialRing{}
	start := time.Now()
	ring.waitForSettle(0)
	if time.Since(start) < 250*time.Millisecond {
		t.Fatalf("waitForSettle(0) returned too quickly: %s", time.Since(start))
	}
}

func TestMacOSDenialRingWaitForRunTagSettleEmptyTagUsesSettle(t *testing.T) {
	ring := &macosDenialRing{}
	start := time.Now()
	ring.waitForRunTagSettle("", 0)
	if time.Since(start) < 250*time.Millisecond {
		t.Fatalf("waitForRunTagSettle empty tag returned too quickly: %s", time.Since(start))
	}
}

func TestMacOSDenialRingWaitForRunTagSettleUsesDefaultTimeout(t *testing.T) {
	ring := &macosDenialRing{}
	start := time.Now()
	ring.waitForRunTagSettle("TRPC_RUN_tag_END_0123456789abcdef_SBX", 0)
	if time.Since(start) < 250*time.Millisecond {
		t.Fatalf("waitForRunTagSettle default timeout returned too quickly: %s", time.Since(start))
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

func TestParseMacOSLogTimestampFallsBackToNow(t *testing.T) {
	before := time.Now()
	got := parseMacOSLogTimestamp("not-a-timestamp")
	if got.Before(before.Add(-time.Second)) {
		t.Fatalf("fallback timestamp = %v, want recent time", got)
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
	if shouldFilterMacOSSandboxDenial(denial, "/bin/cat", filter, DenialFilterDenials) {
		t.Fatal("raw mismatch should not filter denial")
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
		cancel: func() {},
		done:   make(chan struct{}),
	}
	start := time.Now()
	monitor.stop()
	if time.Since(start) < 400*time.Millisecond {
		t.Fatalf("stop returned too quickly: %s", time.Since(start))
	}
}
