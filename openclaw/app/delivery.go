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
	"trpc.group/trpc-go/trpc-agent-go/openclaw/delivery"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	internaloutbound "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
)

func buildDeliveryRunOptionResolver() gateway.RunOptionResolver {
	return func(
		ctx context.Context,
		input gateway.RunOptionInput,
	) (context.Context, []agent.RunOption) {
		target, ok, err := delivery.TargetFromRequestExtensions(
			input.Extensions,
		)
		if err != nil || !ok {
			return ctx, nil
		}

		runtimeState := internaloutbound.RuntimeStateForTarget(
			internaloutbound.DeliveryTarget{
				Channel: target.Channel,
				Target:  target.Target,
			},
		)
		if len(runtimeState) == 0 {
			return ctx, nil
		}

		return ctx, []agent.RunOption{
			agent.MergeRuntimeState(runtimeState),
		}
	}
}
