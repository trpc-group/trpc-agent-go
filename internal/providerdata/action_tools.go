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
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type actionToolsKey struct{}

// ActionToolIdentity identifies a tool as represented by its provider.
// Namespace is empty for a top-level tool.
type ActionToolIdentity struct {
	Namespace string
	Name      string
}

// ActionBinding identifies the latest provider action that selected one native
// tool call. Identity remains stable when the local execution name changes.
type ActionBinding struct {
	CallID   string
	Name     string
	ActionID string
	Identity ActionToolIdentity
}

// ActionToolResolver decodes provider-native action identities without model,
// invocation, tool, or handler state.
type ActionToolResolver func(*model.Request, model.Message) ([]ActionBinding, error)

type actionDecoder struct {
	bindings  ActionToolResolver
	callIDs   func(*model.ProviderData) ([]string, error)
	reconcile ProjectionToolReconciler
}

var actionDecoders sync.Map // provider protocol -> actionDecoder

// RegisterActionDecoder associates native protocol decoding with its
// adapter package. The decoder also applies when a wrapper delegates to it.
func RegisterActionDecoder(protocol string, resolver ActionToolResolver, callIDs func(*model.ProviderData) ([]string, error), reconcile ProjectionToolReconciler) {
	actionDecoders.Store(protocol, actionDecoder{bindings: resolver, callIDs: callIDs, reconcile: reconcile})
}

// ResolveActionBindings resolves bindings from the native record's protocol.
// Protocols without a registered decoder have no provider action bindings.
func ResolveActionBindings(request *model.Request, message model.Message) ([]ActionBinding, error) {
	if message.ProviderData == nil {
		return nil, nil
	}
	decode := decoderFor(message.ProviderData.Protocol).bindings
	if decode == nil {
		return nil, nil
	}
	return decode(request, message)
}

// RequestedActionIDs returns the client action IDs encoded by a known adapter.
func RequestedActionIDs(data *model.ProviderData) ([]string, error) {
	if data == nil {
		return nil, nil
	}
	decode := decoderFor(data.Protocol).callIDs
	if decode == nil {
		return nil, nil
	}
	return decode(data)
}

func decoderFor(protocol string) actionDecoder {
	value, _ := actionDecoders.Load(protocol)
	decoder, _ := value.(actionDecoder)
	return decoder
}

// ActionTools connects one provider action to the caller's tool executor.
// Expired reports whether an exact prior action binding needs rediscovery.
type ActionTools struct {
	Expired func(callID string, identity ActionToolIdentity) bool
	Bind    func(callID, name string, identity ActionToolIdentity, t tool.Tool) bool
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
