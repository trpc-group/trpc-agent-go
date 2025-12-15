//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// PreSummaryHookContext carries all inputs for pre-summary hooks.
type PreSummaryHookContext struct {
	Ctx     context.Context
	Session *session.Session
	Events  []event.Event
	Text    string
}

// PreSummaryHook adjusts or enriches input text before summarization, e.g. add tool-call info, redact, or reorder events.
type PreSummaryHook func(in *PreSummaryHookContext) error

// PostSummaryHookContext post-processes model output, e.g. append tags, trim, or add checklists.
type PostSummaryHookContext struct {
	Ctx     context.Context
	Session *session.Session
	Summary string
}

// PostSummaryHook post-processes model output, e.g. append tags, trim, or add checklists.
type PostSummaryHook func(in *PostSummaryHookContext) error
