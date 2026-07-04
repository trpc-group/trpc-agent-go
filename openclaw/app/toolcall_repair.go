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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
)

func appendToolCallArgumentsJSONRepairGatewayOption(
	opts []gateway.Option,
	enabled bool,
) []gateway.Option {
	return append(
		opts,
		gateway.WithRunOptionResolver(
			buildToolCallArgumentsJSONRepairRunOptionResolver(enabled),
		),
	)
}

func buildToolCallArgumentsJSONRepairRunOptionResolver(
	enabled bool,
) gateway.RunOptionResolver {
	return func(
		ctx context.Context,
		_ gateway.RunOptionInput,
	) (context.Context, []agent.RunOption, error) {
		return ctx, []agent.RunOption{
			agent.WithToolCallArgumentsJSONRepairEnabled(enabled),
		}, nil
	}
}
