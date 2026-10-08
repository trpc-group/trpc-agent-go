//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAdditionalCallbacksOwnership protects ordering and reusable registrations.
func TestAdditionalCallbacksOwnership(t *testing.T) {
	callback := func(context.Context, *BeforeInferenceCaseArgs) (*BeforeInferenceCaseResult, error) { return nil, nil }
	base := NewCallbacks().RegisterBeforeInferenceCase("base", callback)
	extra := NewCallbacks().RegisterBeforeInferenceCase("extra", callback)
	opts := &Options{Callbacks: base}
	WithAdditionalCallbacks(nil)(opts)
	require.Same(t, base, opts.Callbacks)
	WithAdditionalCallbacks(extra)(opts)
	require.Equal(t, "base", opts.Callbacks.BeforeInferenceCase[0].Name)
	require.Equal(t, "extra", opts.Callbacks.BeforeInferenceCase[1].Name)
	opts.Callbacks.BeforeInferenceCase[0].Name = "changed"
	opts.Callbacks.BeforeInferenceCase[1].Name = "changed"
	require.Equal(t, "base", base.BeforeInferenceCase[0].Name)
	require.Equal(t, "extra", extra.BeforeInferenceCase[0].Name)
	second := &Options{}
	WithAdditionalCallbacks(extra)(second)
	require.Len(t, second.Callbacks.BeforeInferenceCase, 1)
	require.Equal(t, "extra", second.Callbacks.BeforeInferenceCase[0].Name)
}
