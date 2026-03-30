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

	wecomChannelName = "wecom"

	wecomThreadPrefix = wecomChannelName + ":thread:"
	wecomChatPrefix   = wecomChannelName + ":chat:"
	wecomDMPrefix     = wecomChannelName + ":dm:"

	wecomScopedUserSeparator = ":user:"

	wecomGroupTargetPrefix  = "group:"
	wecomSingleTargetPrefix = "single:"
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
	if err := validateTarget(target); err != nil {
		return DeliveryTarget{}, err
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
	if target.Target == "" {
		return target
	}
	if resolved, ok := resolveTargetValue(
		target.Channel,
		target.Target,
	); ok {
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
	if target, ok := normalizeWeComTarget(sessionID); ok {
		return DeliveryTarget{
			Channel: wecomChannelName,
			Target:  target,
		}, true
	}
	return DeliveryTarget{}, false
}

func resolveFromOpaqueTarget(value string) (DeliveryTarget, bool) {
	return resolveTargetValue("", value)
}

func resolveTargetValue(
	channelID string,
	value string,
) (DeliveryTarget, bool) {
	if target, ok := resolveTelegramTargetValue(
		channelID,
		value,
	); ok {
		return target, true
	}
	if target, ok := resolveWeComTargetValue(
		channelID,
		value,
	); ok {
		return target, true
	}
	return DeliveryTarget{}, false
}

func resolveTelegramTargetValue(
	channelID string,
	value string,
) (DeliveryTarget, bool) {
	if channelID != "" && channelID != telegram.ChannelName {
		return DeliveryTarget{}, false
	}
	target, ok := telegram.ResolveTextTargetFromSessionID(value)
	if !ok {
		return DeliveryTarget{}, false
	}
	return DeliveryTarget{
		Channel: telegram.ChannelName,
		Target:  target,
	}, true
}

func resolveWeComTargetValue(
	channelID string,
	value string,
) (DeliveryTarget, bool) {
	if channelID != "" && channelID != wecomChannelName {
		return DeliveryTarget{}, false
	}
	target, ok := normalizeWeComTarget(value)
	if !ok {
		return DeliveryTarget{}, false
	}
	return DeliveryTarget{
		Channel: wecomChannelName,
		Target:  target,
	}, true
}

func validateTarget(target DeliveryTarget) error {
	if target.Channel != wecomChannelName {
		return nil
	}
	if _, ok := parseWeComPushTarget(target.Target); ok {
		return nil
	}
	return fmt.Errorf(
		"outbound: invalid target for %s: %s",
		target.Channel,
		target.Target,
	)
}

func normalizeWeComTarget(value string) (string, bool) {
	raw := strings.TrimSpace(value)
	if target, ok := parseWeComPushTarget(raw); ok {
		return target, true
	}
	switch {
	case strings.HasPrefix(raw, wecomThreadPrefix):
		return normalizeWeComTarget(
			strings.TrimPrefix(raw, wecomThreadPrefix),
		)
	case strings.HasPrefix(raw, wecomChatPrefix):
		return normalizeWeComChatTarget(
			strings.TrimPrefix(raw, wecomChatPrefix),
		)
	case strings.HasPrefix(raw, wecomDMPrefix):
		return normalizeWeComDMTarget(
			strings.TrimPrefix(raw, wecomDMPrefix),
		)
	default:
		return "", false
	}
}

func normalizeWeComChatTarget(value string) (string, bool) {
	chatID := strings.TrimSpace(value)
	if idx := strings.Index(chatID, wecomScopedUserSeparator); idx >= 0 {
		chatID = chatID[:idx]
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return "", false
	}
	return wecomGroupTargetPrefix + chatID, true
}

func normalizeWeComDMTarget(value string) (string, bool) {
	userID := strings.TrimSpace(value)
	if userID == "" {
		return "", false
	}
	return wecomSingleTargetPrefix + userID, true
}

func parseWeComPushTarget(value string) (string, bool) {
	raw := strings.TrimSpace(value)
	switch {
	case strings.HasPrefix(raw, wecomGroupTargetPrefix):
		return parseWeComPushTargetWithPrefix(
			raw,
			wecomGroupTargetPrefix,
		)
	case strings.HasPrefix(raw, wecomSingleTargetPrefix):
		return parseWeComPushTargetWithPrefix(
			raw,
			wecomSingleTargetPrefix,
		)
	default:
		return "", false
	}
}

func parseWeComPushTargetWithPrefix(
	value string,
	prefix string,
) (string, bool) {
	id := strings.TrimSpace(strings.TrimPrefix(value, prefix))
	if id == "" {
		return "", false
	}
	return prefix + id, true
}
