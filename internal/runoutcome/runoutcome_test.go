//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runoutcome

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIsTimedOutReportsTimeoutWithCustomCause(t *testing.T) {
	cause := errors.New("custom timeout cause")
	ctx, cancel := context.WithTimeoutCause(context.Background(), time.Nanosecond, cause)
	defer cancel()
	<-ctx.Done()

	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("ctx.Err() = %v, want %v", ctx.Err(), context.DeadlineExceeded)
	}
	if !errors.Is(context.Cause(ctx), cause) {
		t.Fatalf("context.Cause(ctx) = %v, want %v", context.Cause(ctx), cause)
	}
	if !IsTimedOut(ctx) {
		t.Fatal("IsTimedOut(ctx) = false, want true")
	}
}

func TestIsTimedOutIgnoresManualDeadlineCause(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(context.DeadlineExceeded)

	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("ctx.Err() = %v, want %v", ctx.Err(), context.Canceled)
	}
	if !errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
		t.Fatalf("context.Cause(ctx) = %v, want %v", context.Cause(ctx), context.DeadlineExceeded)
	}
	if IsTimedOut(ctx) {
		t.Fatal("IsTimedOut(ctx) = true, want false")
	}
}
