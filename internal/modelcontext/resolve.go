//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package modelcontext resolves model context-window configuration for
// framework-internal threshold decisions.
package modelcontext

import "trpc.group/trpc-go/trpc-agent-go/model"

// ResolveContextWindow resolves a model's context window from instance
// configuration first, then from the process-wide model-name registry.
func ResolveContextWindow(m model.Model) (int, bool) {
	if m == nil {
		return 0, false
	}
	if provider, ok := m.(model.ContextWindowProvider); ok {
		if window, ok := provider.ContextWindow(); ok && window > 0 {
			return window, true
		}
	}
	return model.LookupModelContextWindow(m.Info().Name)
}
