//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package model

import (
	"context"
	"runtime/debug"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// RecoverCallbackPanic converts provider callback panics into logged errors so
// user-defined hooks cannot crash the framework's streaming goroutines.
func RecoverCallbackPanic(ctx context.Context, stage string) {
	if recovered := recover(); recovered != nil {
		log.ErrorfContext(
			ctx,
			"%s panic: %v\n%s",
			stage,
			recovered,
			string(debug.Stack()),
		)
	}
}
