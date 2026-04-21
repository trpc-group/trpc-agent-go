//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import (
	"context"
	"testing"
)

func TestIsInvokeAgentActive_DefaultFalse(t *testing.T) {
	if IsInvokeAgentActive(context.Background()) {
		t.Fatalf("expected IsInvokeAgentActive to return false for a plain context")
	}
}

func TestIsInvokeAgentActive_NilContext(t *testing.T) {
	// SA1012: nil context intentionally exercised to validate robustness.
	var nilCtx context.Context //nolint:staticcheck
	if IsInvokeAgentActive(nilCtx) {
		t.Fatalf("expected IsInvokeAgentActive to return false for a nil context")
	}
}

func TestWithInvokeAgentActive_SetsFlag(t *testing.T) {
	ctx := WithInvokeAgentActive(context.Background())
	if !IsInvokeAgentActive(ctx) {
		t.Fatalf("expected IsInvokeAgentActive to be true after WithInvokeAgentActive")
	}
}

func TestWithInvokeAgentActive_DoesNotLeakToSibling(t *testing.T) {
	base := context.Background()
	scoped := WithInvokeAgentActive(base)
	if !IsInvokeAgentActive(scoped) {
		t.Fatalf("expected scoped context to be active")
	}
	if IsInvokeAgentActive(base) {
		t.Fatalf("expected base context to remain inactive")
	}
}

func TestWithInvokeAgentActive_Idempotent(t *testing.T) {
	ctx := WithInvokeAgentActive(context.Background())
	again := WithInvokeAgentActive(ctx)
	if again != ctx {
		t.Fatalf("expected WithInvokeAgentActive to return the same context when already active")
	}
	if !IsInvokeAgentActive(again) {
		t.Fatalf("expected active flag to remain set")
	}
}

func TestWithInvokeAgentActive_NilContext(t *testing.T) {
	// SA1012: nil context intentionally exercised to validate robustness.
	var nilCtx context.Context //nolint:staticcheck
	if got := WithInvokeAgentActive(nilCtx); got != nil {
		t.Fatalf("expected nil context to be returned unchanged, got %v", got)
	}
}
