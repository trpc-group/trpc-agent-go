//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolcall

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/semaphore"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Limiter enforces overall and disjoint shared-group concurrency limits.
type Limiter struct {
	global *semaphore.Weighted
	groups map[string]*semaphore.Weighted
}

// ValidateConcurrencyConfig validates concurrency group membership.
func ValidateConcurrencyConfig(config tool.ConcurrencyConfig) error {
	seen := make(map[string]struct{})
	for _, group := range config.Groups {
		if group.Limit <= 0 {
			continue
		}
		groupNames := make(map[string]struct{})
		for _, name := range group.ToolNames {
			if name == "" {
				continue
			}
			if _, exists := groupNames[name]; exists {
				continue
			}
			groupNames[name] = struct{}{}
			if _, exists := seen[name]; exists {
				return fmt.Errorf(
					"tool %q appears in multiple concurrency groups",
					name,
				)
			}
			seen[name] = struct{}{}
		}
	}
	return nil
}

// NewLimiter builds a limiter from config. It returns nil when config contains
// no positive limits. It panics if a tool belongs to more than one
// positive-limit group.
func NewLimiter(config tool.ConcurrencyConfig) *Limiter {
	if err := ValidateConcurrencyConfig(config); err != nil {
		panic(err)
	}
	var global *semaphore.Weighted
	if config.MaxConcurrency > 0 {
		global = semaphore.NewWeighted(int64(config.MaxConcurrency))
	}
	groups := make(map[string]*semaphore.Weighted)
	for _, group := range config.Groups {
		if group.Limit <= 0 {
			continue
		}
		groupLimiter := semaphore.NewWeighted(int64(group.Limit))
		for _, name := range group.ToolNames {
			if name == "" {
				continue
			}
			groups[name] = groupLimiter
		}
	}
	if global == nil && len(groups) == 0 {
		return nil
	}
	return &Limiter{
		global: global,
		groups: groups,
	}
}

// Acquire waits until the named tool has both group and overall capacity.
// The returned release function is safe to call more than once.
func (l *Limiter) Acquire(
	ctx context.Context,
	toolName string,
) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	group := l.groups[toolName]
	if group != nil {
		if err := group.Acquire(ctx, 1); err != nil {
			return nil, err
		}
	}
	if l.global != nil {
		if err := l.global.Acquire(ctx, 1); err != nil {
			if group != nil {
				group.Release(1)
			}
			return nil, err
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			if l.global != nil {
				l.global.Release(1)
			}
			if group != nil {
				group.Release(1)
			}
		})
	}, nil
}
