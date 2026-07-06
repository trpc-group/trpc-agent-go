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
