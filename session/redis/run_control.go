//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redis

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/runcontrol"
)

func (s *Service) BeginRun(
	ctx context.Context,
	req runcontrol.BeginRequest,
) (*runcontrol.Permit, error) {
	if !s.opts.enableRunControl {
		return nil, fmt.Errorf("run control is disabled")
	}
	if req.LeaseTTL <= 0 {
		req.LeaseTTL = s.opts.runLeaseTTL
	}
	return s.hashidxClient.BeginRun(ctx, req)
}

func (s *Service) RenewRun(
	ctx context.Context,
	lease runcontrol.Lease,
	ttl time.Duration,
) (*runcontrol.RenewResult, error) {
	if !s.opts.enableRunControl {
		return nil, fmt.Errorf("run control is disabled")
	}
	if ttl <= 0 {
		ttl = s.opts.runLeaseTTL
	}
	return s.hashidxClient.RenewRun(ctx, lease, ttl)
}

func (s *Service) FinishRun(
	ctx context.Context,
	lease runcontrol.Lease,
	req runcontrol.FinishRequest,
) error {
	if !s.opts.enableRunControl {
		return fmt.Errorf("run control is disabled")
	}
	return s.hashidxClient.FinishRun(ctx, lease, req)
}

func (s *Service) CancelRun(
	ctx context.Context,
	req runcontrol.CancelRequest,
) error {
	if !s.opts.enableRunControl {
		return fmt.Errorf("run control is disabled")
	}
	return s.hashidxClient.CancelRun(ctx, req)
}
