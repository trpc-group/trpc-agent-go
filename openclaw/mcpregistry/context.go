//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mcpregistry

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/conversation"
)

// RuntimeContextFromContext extracts MCP scope context from the current
// agent invocation.
func RuntimeContextFromContext(
	ctx context.Context,
	fallbackAppName string,
) (RuntimeContext, error) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return RuntimeContext{}, errRegistryContextUnavailable
	}

	runtime := RuntimeContext{
		AppName:   strings.TrimSpace(inv.Session.AppName),
		SessionID: strings.TrimSpace(inv.Session.ID),
		UserID:    strings.TrimSpace(inv.Session.UserID),
	}
	if runtime.AppName == "" {
		runtime.AppName = strings.TrimSpace(fallbackAppName)
	}

	annotation, ok := conversation.AnnotationFromRuntimeState(
		inv.RunOptions.RuntimeState,
	)
	if ok {
		runtime.StorageUserID = strings.TrimSpace(annotation.StorageUserID)
		runtime.ChatID = strings.TrimSpace(annotation.ChatID)
	}
	return runtime, nil
}
