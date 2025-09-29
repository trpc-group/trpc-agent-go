//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package service defines Service interface for AG-UI services.
package service

import "net/http"

// Service represents the AG-UI service implementation.
// Different transports (SSE, WebSocket, etc.) can return their own http.Handler,
// which can be mounted to an existing HTTP router.
type Service interface {
	Handler() http.Handler
}
