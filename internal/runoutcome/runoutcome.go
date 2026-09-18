//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package runoutcome contains cancellation markers shared by runners.
package runoutcome

import (
	"context"
	"errors"
)

// ErrExplicitCancel marks a run cancelled through an explicit cancel API.
//
// The marker is intentionally shared internally so protocol adapters can
// identify explicit cancellation when the run completion event is persisted.
var ErrExplicitCancel = errors.New("agui: explicit cancel")

// IsExplicitCancel reports whether ctx was cancelled with ErrExplicitCancel.
func IsExplicitCancel(ctx context.Context) bool {
	return ctx != nil && errors.Is(context.Cause(ctx), ErrExplicitCancel)
}

// IsTimedOut reports whether ctx ended because its deadline was exceeded.
func IsTimedOut(ctx context.Context) bool {
	return ctx != nil && errors.Is(context.Cause(ctx), context.DeadlineExceeded)
}
