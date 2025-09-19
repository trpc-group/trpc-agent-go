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

import "context"

// Service is the interface for AG-UI services.
type Service interface {
	// Serve starts the service.
	Serve(ctx context.Context) error
	// Close stops the service.
	Close(ctx context.Context) error
}
