//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"trpc.group/trpc-go/trpc-agent-go/openclaw/runtimeprofile"
)

// RuntimeOption customizes an embedded OpenClaw runtime.
type RuntimeOption func(*runtimeOptions)

type runtimeOptions struct {
	runtimeProfileResolver runtimeprofile.Resolver
	runtimeProfileCatalog  runtimeprofile.Catalog
	runtimeProfileRequired bool
}

// WithRuntimeProfileResolver injects per-request runtime profile resolution.
func WithRuntimeProfileResolver(
	resolver runtimeprofile.Resolver,
	required bool,
) RuntimeOption {
	return func(opts *runtimeOptions) {
		if resolver == nil {
			return
		}
		opts.runtimeProfileResolver = resolver
		opts.runtimeProfileRequired = required
	}
}

// WithRuntimeProfileCatalog injects profile metadata for cleanup/catalog use.
func WithRuntimeProfileCatalog(
	catalog runtimeprofile.Catalog,
) RuntimeOption {
	return func(opts *runtimeOptions) {
		if catalog == nil {
			return
		}
		opts.runtimeProfileCatalog = catalog
	}
}

// WithRuntimeProfileStore injects a reloadable runtime profile store.
//
// Callers that need Reload or Invalidate control can create a
// runtimeprofile.CachedResolver and pass WithRuntimeProfileResolver.
func WithRuntimeProfileStore(
	store runtimeprofile.Store,
	required bool,
) RuntimeOption {
	return func(opts *runtimeOptions) {
		resolver := runtimeprofile.NewCachedResolver(store)
		if resolver == nil {
			return
		}
		opts.runtimeProfileResolver = resolver
		opts.runtimeProfileCatalog = resolver
		opts.runtimeProfileRequired = required
	}
}

func buildRuntimeOptions(options []RuntimeOption) runtimeOptions {
	var opts runtimeOptions
	for _, option := range options {
		if option == nil {
			continue
		}
		option(&opts)
	}
	return opts
}
