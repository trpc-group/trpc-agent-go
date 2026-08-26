//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import "context"

// ToolMetadata describes execution properties that hosts and policies can use
// before deciding whether a tool is safe to expose or execute.
//
// Metadata is descriptive. The framework does not change scheduling or loading
// behavior from these fields alone; callers can opt in by using filters,
// permission policies, or custom runners. A tool that needs to influence
// scheduling implements ConcurrencyAware instead; see IsConcurrencySafe.
type ToolMetadata struct {
	// ReadOnly reports that the tool does not intentionally mutate external
	// state. Read-only tools can still be expensive or read sensitive data.
	ReadOnly bool
	// Destructive reports that the tool may delete, overwrite, or otherwise
	// irreversibly change external state.
	Destructive bool
	// ConcurrencySafe reports that independent calls to the same tool can run at
	// the same time without corrupting shared state.
	//
	// It does not affect scheduling: a struct field cannot distinguish "set to
	// false" from "never set", so acting on it would take every tool that
	// publishes unrelated metadata off the parallel path too. Implement
	// ConcurrencyAware to make that decision explicit.
	ConcurrencySafe bool
	// SearchOrRead reports that the tool primarily searches or reads data.
	SearchOrRead bool
	// OpenWorld reports that the tool can reach outside the current process or
	// workspace, for example through network, shell, or remote service calls.
	OpenWorld bool
	// MaxResultSize is an optional advisory result-size limit in bytes. Zero
	// means the tool does not publish a limit.
	MaxResultSize int
}

// MetadataProvider is implemented by tools that publish ToolMetadata.
type MetadataProvider interface {
	ToolMetadata() ToolMetadata
}

// ConcurrencyAware is a small opt-in interface for tools that only need to
// publish their concurrency property.
//
// Returning false is an objection: the parallel tool paths keep the tool's whole
// turn sequential. Returning true is a guarantee — the tool can run at the same
// time as any other tool call in its turn, other calls to itself included — which
// is why MetadataOf reads it as ConcurrencySafe. Not implementing the interface
// raises no objection and promises nothing: it is the admission default, and
// MetadataOf reports nothing for it.
//
// Framework wrappers forward the answer of the tool they wrap and delegate
// ToolMetadata the same way, so a wrapper neither adds a guarantee nor hides an
// objection.
type ConcurrencyAware interface {
	IsConcurrencySafe() bool
}

// DeferredTool is implemented by tools that want hosts to hide the full tool
// declaration until it is explicitly needed. The core runner does not defer
// tools by itself; this is intended for tool-search or host-side loading logic.
type DeferredTool interface {
	ShouldDefer(ctx context.Context) bool
}

// MetadataOf returns the metadata published by a tool. Tools that do not
// implement MetadataProvider get the zero value, preserving existing behavior.
//
// If a tool implements ConcurrencyAware but not MetadataProvider, that value
// fills ConcurrencySafe. The two agree by contract: a ConcurrencyAware true
// guarantees the tool can run beside any call in its turn, itself included, so
// it covers the same-tool reentrancy ConcurrencySafe describes; a false objects
// to both. A tool implementing neither publishes nothing, and nothing is
// synthesized for it.
func MetadataOf(t Tool) ToolMetadata {
	if t == nil {
		return ToolMetadata{}
	}
	if provider, ok := t.(MetadataProvider); ok {
		return provider.ToolMetadata()
	}
	if aware, ok := t.(ConcurrencyAware); ok {
		return ToolMetadata{ConcurrencySafe: aware.IsConcurrencySafe()}
	}
	return ToolMetadata{}
}

// IsConcurrencySafe reports whether a tool raises no objection to running at the
// same time as the other tool calls in its turn. Both parallel paths — the
// LLMAgent function-call processor and graph Tools nodes — ask before admitting a
// batch, and one objection keeps the whole batch sequential.
//
// Only ConcurrencyAware is consulted. ToolMetadata.ConcurrencySafe is a struct
// field whose zero value cannot be told from an explicit false, so reading it
// here would make every tool publishing unrelated metadata look unsafe and
// silently serialize turns that run concurrently today. The two can therefore
// disagree, which is intended: MetadataOf describes, this schedules.
//
// A tool implementing neither interface is admitted, which is what makes this an
// opt-out rather than an opt-in.
func IsConcurrencySafe(t Tool) bool {
	if t == nil {
		return true
	}
	if aware, ok := t.(ConcurrencyAware); ok {
		return aware.IsConcurrencySafe()
	}
	return true
}

// ShouldDefer reports whether a tool asks host-side loading logic to defer
// loading its full declaration.
func ShouldDefer(ctx context.Context, t Tool) bool {
	if t == nil {
		return false
	}
	deferred, ok := t.(DeferredTool)
	return ok && deferred.ShouldDefer(ctx)
}
