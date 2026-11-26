//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package providerdata

import "trpc.group/trpc-go/trpc-agent-go/model"

// ProjectionToolReconciler retains execution names for unchanged native call
// identities when a callback edits the native payload. The adapter defines the
// identity; unrelated calls are left as projected by the selected model.
type ProjectionToolReconciler func(request *model.Request, before, after *model.ProviderData, beforeCalls, afterCalls []model.ToolCall) error

func reconcileProjectedTools(request *model.Request, before, after *model.ProviderData, beforeCalls, afterCalls []model.ToolCall) error {
	if after == nil {
		return nil
	}
	if before != nil && (before.Protocol != after.Protocol || before.Model != after.Model) {
		before, beforeCalls = nil, nil
	}
	if reconcile := decoderFor(after.Protocol).reconcile; reconcile != nil {
		return reconcile(request, before, after, beforeCalls, afterCalls)
	}
	return nil
}
