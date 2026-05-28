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
// permission policies, or custom runners.
type ToolMetadata struct {
	// ReadOnly reports that the tool does not intentionally mutate external
	// state. Read-only tools can still be expensive or read sensitive data.
	ReadOnly bool
	// Destructive reports that the tool may delete, overwrite, or otherwise
	// irreversibly change external state.
	Destructive bool
	// ConcurrencySafe reports that independent calls to the same tool can run at
	// the same time without corrupting shared state.
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

// ShouldDefer reports whether a tool asks host-side loading logic to defer
// loading its full declaration.
func ShouldDefer(ctx context.Context, t Tool) bool {
	if t == nil {
		return false
	}
	deferred, ok := t.(DeferredTool)
	return ok && deferred.ShouldDefer(ctx)
}
