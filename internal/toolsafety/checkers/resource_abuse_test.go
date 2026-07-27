// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package checkers

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
)

func TestResourceAbuseChecker_SleepExceedsLimit(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	c := NewResourceAbuseChecker(policy)

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "sleep 3600",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasSleep := false
	for _, f := range findings {
		if f.RuleID == toolsafety.RuleResourceSleepLoop {
			hasSleep = true
			break
		}
	}
	if !hasSleep {
		t.Errorf("expected RESOURCE_SLEEP_LOOP finding for long sleep, got %+v", findings)
	}
}

func TestResourceAbuseChecker_ShortSleepOK(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	c := NewResourceAbuseChecker(policy)

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "sleep 1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasSleep := false
	for _, f := range findings {
		if f.RuleID == toolsafety.RuleResourceSleepLoop {
			hasSleep = true
			break
		}
	}
	if hasSleep {
		t.Errorf("short sleep should not trigger RESOURCE_SLEEP_LOOP, got %+v", findings)
	}
}

func TestResourceAbuseChecker_HugeOutput(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	c := NewResourceAbuseChecker(policy)

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "cat /dev/urandom",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasOutput := false
	for _, f := range findings {
		if f.RuleID == toolsafety.RuleResourceOutputSize {
			hasOutput = true
			break
		}
	}
	if !hasOutput {
		t.Errorf("expected RESOURCE_OUTPUT_SIZE finding for /dev/urandom, got %+v", findings)
	}
}

func TestResourceAbuseChecker_WhileTrue(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	c := NewResourceAbuseChecker(policy)

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "while true; do echo loop; done",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasLoop := false
	for _, f := range findings {
		if f.RuleID == toolsafety.RuleResourceSleepLoop {
			hasLoop = true
			break
		}
	}
	if !hasLoop {
		t.Errorf("expected RESOURCE_SLEEP_LOOP finding for while true, got %+v", findings)
	}
}
