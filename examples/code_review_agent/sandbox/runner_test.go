//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sandbox_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/sandbox"
)

// TestLocalRunner_Timeout verifies related behavior.
func TestLocalRunner_Timeout(t *testing.T) {
	r := sandbox.LocalRunner{}
	limits := safety.DefaultLimits()
	limits.Timeout = 200 * time.Millisecond
	res := r.Run(context.Background(), sandbox.Spec{
		Command: "sleep 2",
	}, limits)
	if res.Summary.Status != "timeout" {
		t.Fatalf("status=%s err=%s", res.Summary.Status, res.Summary.Error)
	}
}

// TestLocalRunner_OutputLimit verifies related behavior.
func TestLocalRunner_OutputLimit(t *testing.T) {
	r := sandbox.LocalRunner{}
	limits := safety.DefaultLimits()
	limits.MaxStdoutBytes = 32
	res := r.Run(context.Background(), sandbox.Spec{
		Command: "python3 -c 'print(\"x\"*1000)'",
	}, limits)
	if !res.Summary.Truncated && res.Summary.Status != "truncated" {
		// truncated flag should be set when writer hits limit
		if res.Summary.StdoutBytes > 32 {
			t.Fatalf("stdout bytes=%d", res.Summary.StdoutBytes)
		}
	}
	if res.Summary.StdoutBytes > 32 {
		t.Fatalf("stdout exceeded limit: %d", res.Summary.StdoutBytes)
	}
}

// TestFailingRunner_DoesNotPanic verifies related behavior.
func TestFailingRunner_DoesNotPanic(t *testing.T) {
	r := sandbox.FailingRunner{Inner: sandbox.LocalRunner{}}
	res := r.Run(context.Background(), sandbox.Spec{
		Command: "bash -lc 'echo FORCE_SANDBOX_FAIL; exit 1'",
	}, safety.DefaultLimits())
	if res.Summary.Status != "failed" {
		t.Fatalf("status=%s", res.Summary.Status)
	}
	if !strings.Contains(res.Stderr, "forced sandbox failure") {
		t.Fatalf("stderr=%q", res.Stderr)
	}
}
