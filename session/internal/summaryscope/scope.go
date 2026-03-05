//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package summaryscope defines shared scope markers used by session summary
// internals and summary checkers.
package summaryscope

const (
	// StateKey stores the summary-check scope on temporary sessions.
	StateKey = "summary:scope"
	// ScopeFullSession means full-session summary threshold evaluation.
	ScopeFullSession = "full"
	// ScopeFilterKey means filterKey-scoped summary threshold evaluation.
	ScopeFilterKey = "filter_key"
)
