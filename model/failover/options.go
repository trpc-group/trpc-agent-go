//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package failover

import "trpc.group/trpc-go/trpc-agent-go/model"

type options struct {
	candidates []model.Model
}

// Option configures a failover model.
type Option func(*options)

// WithCandidates appends failover candidates in priority order.
func WithCandidates(candidates ...model.Model) Option {
	return func(o *options) {
		o.candidates = append(o.candidates, candidates...)
	}
}
