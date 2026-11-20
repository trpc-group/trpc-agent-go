//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package providerdata

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type actionToolsKey struct{}

const (
	actionBindingCallID = "trpc.provider_action.binding_call_id"
	actionBindingKey    = "trpc.provider_action.binding_key"
)

// SetActionBindingHint records a possible prior execution key for a single
// provider call. The executor must verify it against its own action bindings.
func SetActionBindingHint(call *model.ToolCall, callID, key string) {
	if call.ExtraFields == nil {
		call.ExtraFields = make(map[string]any)
	}
	call.ExtraFields[actionBindingCallID] = callID
	call.ExtraFields[actionBindingKey] = key
}

// ActionBindingHint returns a provider's hint without authorizing its use.
func ActionBindingHint(call model.ToolCall) (string, string) {
	callID, _ := call.ExtraFields[actionBindingCallID].(string)
	key, _ := call.ExtraFields[actionBindingKey].(string)
	return callID, key
}

// ActionTools connects one provider action to the caller's tool executor.
// Unavailable contains prior search bindings that this run must rediscover.
type ActionTools struct {
	Unavailable map[string]bool
	Bind        func(callID, name string, t tool.Tool) bool
}

// WithActionTools lets a completer return bindings without changing the model
// request. Direct model callers manage their own executors.
func WithActionTools(ctx context.Context, actions *ActionTools) context.Context {
	return context.WithValue(ctx, actionToolsKey{}, actions)
}

// ActionToolsFromContext returns the tool collector for this completion, if any.
func ActionToolsFromContext(ctx context.Context) *ActionTools {
	actions, _ := ctx.Value(actionToolsKey{}).(*ActionTools)
	return actions
}
