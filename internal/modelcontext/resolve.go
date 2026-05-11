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

// ResolveContextWindow resolves a model's context window from Info first,
// then from the process-wide model-name registry.
func ResolveContextWindow(m model.Model) (int, bool) {
	if m == nil {
		return 0, false
	}
	info := m.Info()
	if info.ContextWindow > 0 {
		return info.ContextWindow, true
	}
	return model.LookupModelContextWindow(info.Name)
}
