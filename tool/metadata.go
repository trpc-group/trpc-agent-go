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
// scheduling implements ConcurrencyAware instead — see IsConcurrencySafe for
// why the struct field cannot serve that purpose.
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
	// This field does not affect scheduling, and setting it to false does not
	// keep a tool off the parallel path: a struct field cannot distinguish "set
	// to false" from "never set", so acting on it would take every tool that
	// publishes unrelated metadata off that path too. Implement
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
// Returning false is an objection: the framework's parallel tool paths keep the
// tool's whole turn sequential rather than run it beside its siblings. Returning
// true, or not implementing this interface at all, raises no objection and
// promises nothing about specific siblings.
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
// fills ConcurrencySafe.
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
// same time as the other tool calls in its turn. It is the question the framework's
// parallel tool paths ask — the LLMAgent function-call processor and graph Tools
// nodes — before admitting a batch: a single objection keeps the whole batch
// sequential.
//
// Only ConcurrencyAware is consulted, deliberately. ToolMetadata.ConcurrencySafe
// is a struct field whose zero value is indistinguishable from an explicit false,
// so reading it here would turn every tool that publishes unrelated metadata — a
// single ReadOnly hint, or a wrapper republishing another tool's metadata — into
// one that appears to declare itself unsafe, and would silently take turns off
// the parallel path that run concurrently today. Reading only the narrow
// interface also leaves ToolMetadata.ConcurrencySafe with the same-tool meaning
// it has always documented, rather than reinterpreting values external tools
// already publish.
//
// A tool that implements neither interface is admitted. That is the pre-existing
// behavior of every multi-call turn with parallel tools enabled, and it is what
// makes this an opt-out rather than an opt-in.
//
// MetadataOf and this function can therefore disagree: a tool publishing
// ToolMetadata{ConcurrencySafe: false} without implementing ConcurrencyAware is
// still admitted here. That is intended — MetadataOf describes, this schedules.
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
