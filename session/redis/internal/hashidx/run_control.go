//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hashidx

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/internal/runcontrol"
)

func (c *Client) BeginRun(
	ctx context.Context,
	req runcontrol.BeginRequest,
) (*runcontrol.Permit, error) {
	if err := req.SessionKey.CheckSessionKey(); err != nil {
		return nil, err
	}
	if req.RequestID == "" {
		return nil, fmt.Errorf("requestID is required")
	}
	if req.NodeID == "" {
		return nil, fmt.Errorf("nodeID is required")
	}
	ttl := req.LeaseTTL
	if ttl <= 0 {
		ttl = c.cfg.SessionTTL
	}
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	deadline := time.Time{}
	if req.WaitTimeout > 0 {
		deadline = time.Now().Add(req.WaitTimeout)
	}
	leaseToken := req.RequestID + ":" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if req.Policy == "" {
		req.Policy = runcontrol.PolicyRejectIfBusy
	}

	for {
		res, err := luaRunBegin.Run(
			ctx,
			c.client,
			[]string{c.keys.RunActiveKey(req.SessionKey)},
			time.Now().UnixMilli(),
			ttl.Milliseconds(),
			req.RequestID,
			req.InvocationID,
			req.AgentName,
			req.NodeID,
			leaseToken,
			string(req.Policy),
			"superseded by newer request",
			req.CancelGrace.Milliseconds(),
		).StringSlice()
		if err != nil {
			return nil, fmt.Errorf("begin run: %w", err)
		}
		if len(res) > 0 && res[0] == "running" {
			return &runcontrol.Permit{
				Lease: runcontrol.Lease{
					SessionKey: req.SessionKey,
					RequestID:  req.RequestID,
					LeaseToken: leaseToken,
					NodeID:     req.NodeID,
				},
				State: runcontrol.StateRunning,
			}, nil
		}
		if len(res) > 0 && res[0] == "busy" {
			return nil, runcontrol.ErrRunBusy
		}
		if req.Policy == runcontrol.PolicyRejectIfBusy {
			return nil, runcontrol.ErrRunBusy
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return nil, runcontrol.ErrRunBusy
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(minDuration(ttl/5, time.Second)):
		}
	}
}

func (c *Client) RenewRun(
	ctx context.Context,
	lease runcontrol.Lease,
	ttl time.Duration,
) (*runcontrol.RenewResult, error) {
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	res, err := luaRunRenew.Run(
		ctx,
		c.client,
		[]string{c.keys.RunActiveKey(lease.SessionKey)},
		time.Now().UnixMilli(),
		ttl.Milliseconds(),
		lease.RequestID,
		lease.LeaseToken,
	).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("renew run: %w", err)
	}
	if len(res) == 0 {
		return nil, runcontrol.ErrRunLeaseLost
	}
	switch res[0] {
	case "ok":
		out := &runcontrol.RenewResult{}
		if len(res) > 1 {
			out.CancelRequested = res[1] == "1"
		}
		if len(res) > 2 {
			out.CancelReason = res[2]
		}
		if len(res) > 3 {
			out.CancelSeq, _ = strconv.ParseInt(res[3], 10, 64)
		}
		if len(res) > 4 {
			if graceMs, parseErr := strconv.ParseInt(res[4], 10, 64); parseErr == nil {
				out.CancelGrace = time.Duration(graceMs) * time.Millisecond
			}
		}
		return out, nil
	case "missing", "lost":
		return nil, runcontrol.ErrRunLeaseLost
	default:
		return nil, runcontrol.ErrRunLeaseLost
	}
}

func (c *Client) FinishRun(
	ctx context.Context,
	lease runcontrol.Lease,
	_ runcontrol.FinishRequest,
) error {
	val, err := luaRunFinish.Run(
		ctx,
		c.client,
		[]string{c.keys.RunActiveKey(lease.SessionKey)},
		lease.RequestID,
		lease.LeaseToken,
	).Int()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("finish run: %w", err)
	}
	if val < 0 {
		return runcontrol.ErrRunLeaseLost
	}
	return nil
}

func (c *Client) CancelRun(
	ctx context.Context,
	req runcontrol.CancelRequest,
) error {
	if err := req.SessionKey.CheckSessionKey(); err != nil {
		return err
	}
	_, err := luaRunCancel.Run(
		ctx,
		c.client,
		[]string{c.keys.RunActiveKey(req.SessionKey)},
		req.RequestID,
		req.Reason,
		req.CancelGrace.Milliseconds(),
	).Int()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("cancel run: %w", err)
	}
	return nil
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 || a > b {
		return b
	}
	return a
}
