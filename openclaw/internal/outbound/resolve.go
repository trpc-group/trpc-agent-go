//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package outbound

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/channel/telegram"
)

const (
	runtimeStateDeliveryChannel = "openclaw.delivery.channel"
	runtimeStateDeliveryTarget  = "openclaw.delivery.target"
)

// ResolveTarget chooses an outbound target from explicit args, runtime
// state, or the current session id.
func ResolveTarget(
	ctx context.Context,
	explicit DeliveryTarget,
) (DeliveryTarget, error) {
	target := sanitizeTarget(explicit)
	target = fillTargetFromOpaqueValue(target)
	target = fillTargetFromRuntime(ctx, target)
	target = fillTargetFromSession(ctx, target)

	if strings.TrimSpace(target.Channel) == "" ||
		strings.TrimSpace(target.Target) == "" {
		return DeliveryTarget{}, fmt.Errorf(
			"outbound: unable to resolve target",
		)
	}
	return target, nil
}

func RuntimeStateForTarget(target DeliveryTarget) map[string]any {
	clean := sanitizeTarget(target)
	if clean.Channel == "" || clean.Target == "" {
		return nil
	}
	return map[string]any{
		runtimeStateDeliveryChannel: clean.Channel,
		runtimeStateDeliveryTarget:  clean.Target,
	}
}

func sanitizeTarget(target DeliveryTarget) DeliveryTarget {
	return DeliveryTarget{
		Channel: strings.TrimSpace(target.Channel),
		Target:  strings.TrimSpace(target.Target),
	}
}

func fillTargetFromOpaqueValue(target DeliveryTarget) DeliveryTarget {
	if target.Channel != "" || target.Target == "" {
		return target
	}
	if resolved, ok := resolveFromOpaqueTarget(target.Target); ok {
		return resolved
	}
	return target
}

func fillTargetFromRuntime(
	ctx context.Context,
	target DeliveryTarget,
) DeliveryTarget {
	if ctx == nil {
		return target
	}
	if target.Channel == "" {
		if channelID, ok := agent.GetRuntimeStateValueFromContext[string](
			ctx,
			runtimeStateDeliveryChannel,
		); ok {
			target.Channel = strings.TrimSpace(channelID)
		}
	}
	if target.Target == "" {
		if value, ok := agent.GetRuntimeStateValueFromContext[string](
			ctx,
			runtimeStateDeliveryTarget,
		); ok {
			target.Target = strings.TrimSpace(value)
		}
	}
	return fillTargetFromOpaqueValue(target)
}

func fillTargetFromSession(
	ctx context.Context,
	target DeliveryTarget,
) DeliveryTarget {
	if ctx == nil {
		return target
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return target
	}
	resolved, ok := ResolveTargetFromSessionID(inv.Session.ID)
	if !ok {
		return target
	}
	if target.Channel == "" {
		target.Channel = resolved.Channel
	}
	if target.Target == "" {
		target.Target = resolved.Target
	}
	return target
}

// ResolveTargetFromSessionID infers an outbound target from a chat session id.
func ResolveTargetFromSessionID(sessionID string) (DeliveryTarget, bool) {
	if target, ok := telegram.ResolveTextTargetFromSessionID(sessionID); ok {
		return DeliveryTarget{
			Channel: telegram.ChannelName,
			Target:  target,
		}, true
	}
	return DeliveryTarget{}, false
}

func resolveFromOpaqueTarget(value string) (DeliveryTarget, bool) {
	if target, ok := telegram.ResolveTextTargetFromSessionID(value); ok {
		return DeliveryTarget{
			Channel: telegram.ChannelName,
			Target:  target,
		}, true
	}
	return DeliveryTarget{}, false
}
