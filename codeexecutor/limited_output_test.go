//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexecutor

import "testing"

func TestOutputLimiterRetainsSharedBudgetAndDrains(t *testing.T) {
	limiter := NewOutputLimiter(5)
	stdout := limiter.NewWriter()
	stderr := limiter.NewWriter()

	n, err := stdout.Write([]byte("abcd"))
	if err != nil || n != 4 {
		t.Fatalf("stdout write = (%d, %v), want (4, nil)", n, err)
	}
	n, err = stderr.Write([]byte("efghij"))
	if err != nil || n != 6 {
		t.Fatalf("stderr write = (%d, %v), want (6, nil)", n, err)
	}

	if got := stdout.String() + stderr.String(); got != "abcde" {
		t.Fatalf("retained output = %q, want %q", got, "abcde")
	}
	if !limiter.Truncated() {
		t.Fatal("expected limiter to report truncation")
	}
}

func TestOutputLimiterNonPositiveLimitIsUnbounded(t *testing.T) {
	limiter := NewOutputLimiter(0)
	out := limiter.NewWriter()

	if _, err := out.Write([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "abcdef" {
		t.Fatalf("output = %q, want abcdef", got)
	}
	if limiter.Truncated() {
		t.Fatal("zero limit should not report truncation")
	}
}
