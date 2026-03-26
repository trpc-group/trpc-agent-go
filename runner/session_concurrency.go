//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/internal/runcontrol"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const defaultSessionRunLeaseTTL = 15 * time.Second

// SessionConcurrencyPolicy configures distributed active-run coordination when
// the session backend supports the internal runcontrol service.
type SessionConcurrencyPolicy struct {
	Enabled     bool
	NodeID      string
	Policy      runcontrol.Policy
	WaitTimeout time.Duration
	LeaseTTL    time.Duration
	RenewEvery  time.Duration
	CancelGrace time.Duration
}

func (p SessionConcurrencyPolicy) normalized() SessionConcurrencyPolicy {
	if p.NodeID == "" {
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			p.NodeID = hostname
		} else {
			p.NodeID = "local"
		}
	}
	if p.Policy == "" {
		p.Policy = runcontrol.PolicyRejectIfBusy
	}
	if p.LeaseTTL <= 0 {
		p.LeaseTTL = defaultSessionRunLeaseTTL
	}
	if p.RenewEvery <= 0 {
		p.RenewEvery = p.LeaseTTL / 3
	}
	if p.RenewEvery <= 0 {
		p.RenewEvery = time.Second
	}
	return p
}

func (r *runner) runControlService() (runcontrol.Service, bool) {
	if r == nil || !r.sessionRunPolicy.Enabled || r.sessionService == nil {
		return nil, false
	}
	rc, ok := r.sessionService.(runcontrol.Service)
	return rc, ok
}

func (r *runner) beginManagedRun(
	ctx context.Context,
	key session.Key,
	requestID string,
	invocationID string,
	agentName string,
) (*runcontrol.Lease, error) {
	rc, ok := r.runControlService()
	if !ok {
		return nil, nil
	}
	policy := r.sessionRunPolicy.normalized()
	permit, err := rc.BeginRun(ctx, runcontrol.BeginRequest{
		SessionKey:   key,
		RequestID:    requestID,
		InvocationID: invocationID,
		AgentName:    agentName,
		NodeID:       policy.NodeID,
		Policy:       policy.Policy,
		WaitTimeout:  policy.WaitTimeout,
		LeaseTTL:     policy.LeaseTTL,
		CancelGrace:  policy.CancelGrace,
	})
	if err != nil {
		return nil, err
	}
	if permit == nil {
		return nil, nil
	}
	return &permit.Lease, nil
}

func (r *runner) startManagedRunLoops(
	ctx context.Context,
	handle *runHandle,
) {
	rc, ok := r.runControlService()
	if !ok || handle == nil || handle.lease == nil {
		return
	}
	policy := r.sessionRunPolicy.normalized()
	go r.renewManagedRun(ctx, rc, handle, policy)
}

func (r *runner) renewManagedRun(
	ctx context.Context,
	rc runcontrol.Service,
	handle *runHandle,
	policy SessionConcurrencyPolicy,
) {
	ticker := time.NewTicker(policy.RenewEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result, err := rc.RenewRun(ctx, *handle.lease, policy.LeaseTTL)
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					log.DebugfContext(ctx, "session renew run failed: %v", err)
				}
				handle.cancelOnce.Do(handle.cancel)
				return
			}
			if result == nil || !result.CancelRequested {
				continue
			}
			handle.mu.Lock()
			if result.CancelSeq <= handle.lastCancelSeq {
				handle.mu.Unlock()
				continue
			}
			handle.lastCancelSeq = result.CancelSeq
			handle.mu.Unlock()
			handle.cancelOnce.Do(handle.cancel)
			return
		}
	}
}

func (r *runner) finishManagedRun(
	ctx context.Context,
	handle *runHandle,
	req runcontrol.FinishRequest,
) {
	rc, ok := r.runControlService()
	if !ok || handle == nil || handle.lease == nil {
		return
	}
	if err := rc.FinishRun(ctx, *handle.lease, req); err != nil &&
		!errors.Is(err, runcontrol.ErrRunLeaseLost) &&
		!errors.Is(err, context.Canceled) {
		log.DebugfContext(ctx, "session finish run failed: %v", err)
	}
}

func managedInvocationID() string {
	return uuid.NewString()
}
