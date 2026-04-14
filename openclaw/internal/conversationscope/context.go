//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package conversationscope

import (
	"context"
	"encoding/json"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/conversation"
)

type storageUserIDContextKey struct{}

// WithStorageUserID records the storage user scope for the current request.
func WithStorageUserID(ctx context.Context, userID string) context.Context {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, storageUserIDContextKey{}, userID)
}

// StorageUserIDFromContext resolves the persisted conversation user scope.
func StorageUserIDFromContext(ctx context.Context, fallback string) string {
	if ctx != nil {
		if userID, ok := ctx.Value(storageUserIDContextKey{}).(string); ok {
			userID = strings.TrimSpace(userID)
			if userID != "" {
				return userID
			}
		}
	}
	return strings.TrimSpace(fallback)
}

// ResolveStorageUserID extracts an explicit storage user override from request
// extensions and falls back to the canonical request user when absent.
func ResolveStorageUserID(
	extensions map[string]json.RawMessage,
	fallback string,
) (string, error) {
	annotation, ok, err := conversation.AnnotationFromRequestExtensions(
		extensions,
	)
	if err != nil {
		return strings.TrimSpace(fallback), err
	}
	if ok {
		if userID := strings.TrimSpace(annotation.StorageUserID); userID != "" {
			return userID, nil
		}
	}
	return strings.TrimSpace(fallback), nil
}
