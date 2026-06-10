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
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/latencydiag"
)

func appendLatencyDiagnosticsGatewayOption(
	opts []gateway.Option,
	stateDir string,
	enabled bool,
	emitEvents bool,
) []gateway.Option {
	if !enabled {
		return opts
	}
	return append(
		opts,
		gateway.WithRunOptionResolver(
			buildLatencyDiagnosticsRunOptionResolver(
				stateDir,
				emitEvents,
			),
		),
	)
}

func buildLatencyDiagnosticsRunOptionResolver(
	stateDir string,
	emitEvents bool,
) gateway.RunOptionResolver {
	return func(
		ctx context.Context,
		_ gateway.RunOptionInput,
	) (context.Context, []agent.RunOption, error) {
		enabled, err := latencydiag.Enabled(stateDir)
		if err != nil {
			log.Warnf(
				"openclaw: read latency diagnostics state failed: %v",
				err,
			)
		}
		if !enabled {
			return ctx, nil, nil
		}
		return ctx, []agent.RunOption{
			agent.WithLatencyDiagnostics(true),
			agent.WithLatencyDiagnosticsEvents(emitEvents),
		}, nil
	}
}
