//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import "testing"

func TestSandboxDenialConfiguredFilters(t *testing.T) {
	denials := []Denial{{
		Operation: "file-read-data",
		Target:    "/dev/dtracehelper",
		Raw:       "Sandbox: cat deny file-read-data /dev/dtracehelper",
	}}
	filtered := applySandboxDenialFilters(denials, "/bin/cat", DenialFilter{
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
	filtered := applySandboxDenialFilters(denials, "/bin/gh", DenialFilter{
		Ignore: []DenialIgnoreRule{{
			Scope:   DenialFilterAll,
			Command: "gh",
			Targets: []DenialTargetMatcher{{Exact: "/private/tmp/foo"}},
		}},
	})
	if len(filtered) != 0 {
		t.Fatalf("command-pattern filter = %#v, want empty", filtered)
	}
	kept := applySandboxDenialFilters(denials, "/bin/cat", DenialFilter{
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
	filtered := applySandboxDenialFilters(denials, "/bin/cat", DenialFilter{
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
	filtered := applySandboxDenialFilters(denials, "/bin/cat", DenialFilter{})
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
	filtered := applySandboxDenialFilters(denials, "/bin/cat", DenialFilter{
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
	filtered := applySandboxDenialFilters(denials, "/bin/cat", DenialFilter{
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
	if got := applySandboxDenialFilters(nil, "/bin/cat", DenialFilter{}); got != nil {
		t.Fatalf("nil input = %#v, want nil", got)
	}
}

func TestSandboxDenialOperationFilter(t *testing.T) {
	denials := []Denial{{
		Operation: "file-read-data",
		Target:    "/private/tmp/foo",
	}}
	filtered := applySandboxDenialFilters(denials, "/bin/cat", DenialFilter{
		Ignore: []DenialIgnoreRule{{
			Operations: []string{"file-read-data"},
		}},
	})
	if filtered != nil {
		t.Fatalf("operation filter = %#v, want nil", filtered)
	}
	kept := applySandboxDenialFilters(denials, "/bin/cat", DenialFilter{
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
	filtered := applySandboxDenialFilters(denials, "/bin/cat", DenialFilter{
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
	if !sandboxDenialFilterScopeMatches(DenialFilterDenials, DenialFilterDenials) {
		t.Fatal("denials scope should match denials output")
	}
	if !sandboxDenialFilterScopeMatches("", DenialFilterDenials) {
		t.Fatal("empty scope should match denials output")
	}
	if sandboxDenialFilterScopeMatches(DenialFilterScope("other"), DenialFilterDenials) {
		t.Fatal("unknown scope should not match denials output")
	}
	filtered := applySandboxDenialFilters(denials, "/bin/cat", DenialFilter{
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
	if sandboxDenialTargetMatches("/private/tmp/foo", []DenialTargetMatcher{{Glob: "[invalid"}}) {
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
