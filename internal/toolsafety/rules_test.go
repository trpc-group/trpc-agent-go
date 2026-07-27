// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety

import "testing"

func TestRuleIDConstantsAreUnique(t *testing.T) {
	seen := make(map[RuleID]struct{})
	rules := []RuleID{
		RuleDangerousCommand,
		RuleDestructivePath,
		RuleSensitivePath,
		RuleNetworkUnauthorized,
		RuleNetworkAuthorized,
		RuleShellBypass,
		RuleShellWrapper,
		RuleCommandInjection,
		RuleHostExecPTY,
		RuleBackgroundProcess,
		RulePrivilegeEscalation,
		RuleDependencyInstall,
		RuleResourceTimeout,
		RuleResourceOutputSize,
		RuleResourceSleepLoop,
		RuleSensitiveLeak,
	}
	for _, r := range rules {
		if string(r) == "" {
			t.Errorf("RuleID %v has empty string value", r)
		}
		if _, dup := seen[r]; dup {
			t.Errorf("duplicate RuleID: %s", r)
		}
		seen[r] = struct{}{}
	}
	if len(seen) != len(rules) {
		t.Errorf("expected %d unique rules, got %d", len(rules), len(seen))
	}
}
