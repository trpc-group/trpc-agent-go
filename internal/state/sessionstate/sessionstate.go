//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package sessionstate carries the runner's session service to internal
// background work that must coordinate state with the persisted session.
package sessionstate

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

type serviceContextKey struct{}

// ContextWithService returns a context that carries service for internal
// background work. A nil service leaves ctx unchanged.
func ContextWithService(ctx context.Context, service session.Service) context.Context {
	if service == nil {
		return ctx
	}
	return context.WithValue(ctx, serviceContextKey{}, service)
}

// ServiceFromContext returns the session service attached to ctx.
func ServiceFromContext(ctx context.Context) (session.Service, bool) {
	if ctx == nil {
		return nil, false
	}
	service, ok := ctx.Value(serviceContextKey{}).(session.Service)
	return service, ok && service != nil
}
