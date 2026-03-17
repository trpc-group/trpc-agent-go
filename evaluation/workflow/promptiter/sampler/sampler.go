//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package sampler registers PromptIter execution hooks and manages sample collection callbacks.
package sampler

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

var _ plugin.Plugin = (*Sampler)(nil)
var _ plugin.Closer = (*Sampler)(nil)

// Sampler is the plugin wrapper for PromptIter server registration.
type Sampler struct {
}

// New constructs a Sampler instance for registration.
func New() *Sampler {
	return &Sampler{}
}

// Name returns the unique plugin key used by the plugin registry.
func (s *Sampler) Name() string {
	return "promptiter.sampler"
}

// Register currently keeps hook registration placeholder for future extension.
func (s *Sampler) Register(reg *plugin.Registry) {
}

// Close releases optional resources allocated by this plugin.
func (s *Sampler) Close(ctx context.Context) error {
	return nil
}
