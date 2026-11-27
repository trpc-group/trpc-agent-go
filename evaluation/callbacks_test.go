//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package evaluation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
)

// TestAdditionalCallbacksOwnership protects ordering and reusable registrations.
func TestAdditionalCallbacksOwnership(t *testing.T) {
	callback := func(context.Context, *service.BeforeInferenceCaseArgs) (*service.BeforeInferenceCaseResult, error) {
		return nil, nil
	}
	base := service.NewCallbacks().RegisterBeforeInferenceCase("base", callback)
	extra := service.NewCallbacks().RegisterBeforeInferenceCase("extra", callback)
	opts := &service.Options{Callbacks: base}
	withAdditionalCallbacks(nil)(opts)
	require.Same(t, base, opts.Callbacks)
	withAdditionalCallbacks(extra)(opts)
	require.Equal(t, "base", opts.Callbacks.BeforeInferenceCase[0].Name)
	require.Equal(t, "extra", opts.Callbacks.BeforeInferenceCase[1].Name)
	opts.Callbacks.BeforeInferenceCase[0].Name = "changed"
	opts.Callbacks.BeforeInferenceCase[1].Name = "changed"
	require.Equal(t, "base", base.BeforeInferenceCase[0].Name)
	require.Equal(t, "extra", extra.BeforeInferenceCase[0].Name)
	second := &service.Options{}
	withAdditionalCallbacks(extra)(second)
	require.Len(t, second.Callbacks.BeforeInferenceCase, 1)
	require.Equal(t, "extra", second.Callbacks.BeforeInferenceCase[0].Name)
}
