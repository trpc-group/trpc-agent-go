//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package query

import "context"

// PassthroughEnhancer is a simple enhancer that returns the original query unchanged.
type PassthroughEnhancer struct {
	// Configuration options can be added here in the future.
}

// Option represents a functional option for configuring PassthroughEnhancer.
type Option func(*PassthroughEnhancer)

// NewPassthroughEnhancer creates a new passthrough query enhancer with options.
func NewPassthroughEnhancer(opts ...Option) *PassthroughEnhancer {
	pe := &PassthroughEnhancer{}

	// Apply options.
	for _, opt := range opts {
		opt(pe)
	}

	return pe
}

// EnhanceQuery implements the Enhancer interface by returning the original query.
func (p *PassthroughEnhancer) EnhanceQuery(ctx context.Context, req *Request) (*Enhanced, error) {
	return &Enhanced{
		Enhanced: req.Query,
		Keywords: []string{req.Query},
	}, nil
}
