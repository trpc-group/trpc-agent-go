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
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/delivery"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
)

func TestBuildDeliveryRunOptionResolver(t *testing.T) {
	t.Parallel()

	extensions, err := delivery.MergeRequestExtension(
		nil,
		delivery.Target{
			Channel: "wecom",
			Target:  "group:chat1",
		},
	)
	require.NoError(t, err)

	_, runOpts := buildDeliveryRunOptionResolver()(
		context.Background(),
		gateway.RunOptionInput{Extensions: extensions},
	)
	require.Len(t, runOpts, 1)

	cfg := agent.RunOptions{}
	for _, opt := range runOpts {
		opt(&cfg)
	}
	require.Equal(t, "wecom", cfg.RuntimeState["openclaw.delivery.channel"])
	require.Equal(
		t,
		"group:chat1",
		cfg.RuntimeState["openclaw.delivery.target"],
	)
}

func TestBuildDeliveryRunOptionResolverSkipsEmptyInput(t *testing.T) {
	t.Parallel()

	_, runOpts := buildDeliveryRunOptionResolver()(
		context.Background(),
		gateway.RunOptionInput{},
	)
	require.Nil(t, runOpts)
}
