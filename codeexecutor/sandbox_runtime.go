//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexecutor

import (
	"context"
	"errors"
)

var (
	// ErrSandboxApprovalRequired indicates the request matched a prompt rule.
	ErrSandboxApprovalRequired = errors.New("sandbox approval required")
	// ErrSandboxApprovalDenied indicates the request matched a deny rule.
	ErrSandboxApprovalDenied = errors.New("sandbox approval denied")
	// ErrNoSandboxBackend indicates no configured backend accepted the request.
	ErrNoSandboxBackend = errors.New("no compatible sandbox backend")
)

// SandboxCoordinator wires policy resolution, approval, backend selection,
// and backend execution into one reusable orchestration step.
type SandboxCoordinator struct {
	resolver SandboxPolicyResolver
	decider  ApprovalDecider
	selector SandboxBackendSelector
	backends []SandboxBackend
}

// SandboxCoordinatorOption customizes a SandboxCoordinator.
type SandboxCoordinatorOption func(*SandboxCoordinator)

// WithSandboxPolicyResolver sets the policy resolver.
func WithSandboxPolicyResolver(
	resolver SandboxPolicyResolver,
) SandboxCoordinatorOption {
	return func(c *SandboxCoordinator) { c.resolver = resolver }
}

// WithSandboxApprovalDecider sets the approval decider.
func WithSandboxApprovalDecider(
	decider ApprovalDecider,
) SandboxCoordinatorOption {
	return func(c *SandboxCoordinator) { c.decider = decider }
}

// WithSandboxBackendSelector sets the backend selector.
func WithSandboxBackendSelector(
	selector SandboxBackendSelector,
) SandboxCoordinatorOption {
	return func(c *SandboxCoordinator) { c.selector = selector }
}

// WithSandboxBackends sets the available backends.
func WithSandboxBackends(
	backends ...SandboxBackend,
) SandboxCoordinatorOption {
	return func(c *SandboxCoordinator) {
		c.backends = append([]SandboxBackend(nil), backends...)
	}
}

// NewSandboxCoordinator creates a coordinator with conservative defaults:
// a static empty policy resolver, allow-all approval, and first-compatible
// backend selection.
func NewSandboxCoordinator(opts ...SandboxCoordinatorOption) *SandboxCoordinator {
	c := &SandboxCoordinator{
		resolver: StaticSandboxPolicyResolver{},
		decider:  AllowAllApprovalDecider{},
		selector: FirstCompatibleSandboxBackendSelector{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

func (c *SandboxCoordinator) withDefaults() *SandboxCoordinator {
	if c == nil {
		return NewSandboxCoordinator()
	}
	out := *c
	if out.resolver == nil {
		out.resolver = StaticSandboxPolicyResolver{}
	}
	if out.decider == nil {
		out.decider = AllowAllApprovalDecider{}
	}
	if out.selector == nil {
		out.selector = FirstCompatibleSandboxBackendSelector{}
	}
	out.backends = append([]SandboxBackend(nil), c.backends...)
	return &out
}

// WithBackends returns a shallow copy of the coordinator with the provided
// backends appended after any existing configured backends.
func (c *SandboxCoordinator) WithBackends(
	backends ...SandboxBackend,
) *SandboxCoordinator {
	out := c.withDefaults()
	out.backends = append(out.backends, backends...)
	return out
}

// ResolvePolicy resolves the execution policy for a request.
func (c *SandboxCoordinator) ResolvePolicy(
	ctx context.Context,
	req SandboxRunRequest,
) (ExecutionPolicy, error) {
	c = c.withDefaults()
	pol, err := c.resolver.ResolveSandboxPolicy(ctx, PolicyResolveRequest{
		Intent:    req.Intent,
		Workspace: req.Workspace,
		Spec:      req.Spec,
		Metadata:  req.Metadata,
	})
	if err != nil {
		return ExecutionPolicy{}, err
	}
	if pol.Intent == "" {
		pol.Intent = req.Intent
	}
	return pol, nil
}

// DecideApproval evaluates whether a resolved request may proceed.
func (c *SandboxCoordinator) DecideApproval(
	ctx context.Context,
	req SandboxRunRequest,
	pol ExecutionPolicy,
) (ApprovalResult, error) {
	c = c.withDefaults()
	return c.decider.DecideSandboxApproval(ctx, ApprovalRequest{
		Workspace: req.Workspace,
		Spec:      req.Spec,
		Policy:    pol,
		Metadata:  req.Metadata,
	})
}

// SelectBackend selects a compatible backend for a resolved request.
func (c *SandboxCoordinator) SelectBackend(
	ctx context.Context,
	req SandboxRequest,
) (SandboxBackend, error) {
	c = c.withDefaults()
	return c.selector.SelectSandboxBackend(ctx, req, c.backends)
}

// SelectInteractiveBackend selects a compatible backend for an interactive
// request.
func (c *SandboxCoordinator) SelectInteractiveBackend(
	ctx context.Context,
	req SandboxInteractiveRequest,
) (SandboxInteractiveBackend, error) {
	c = c.withDefaults()
	if selector, ok := c.selector.(SandboxInteractiveBackendSelector); ok {
		return selector.SelectSandboxInteractiveBackend(
			ctx,
			req,
			c.backends,
		)
	}
	for _, backend := range c.backends {
		interactiveBackend, ok := backend.(SandboxInteractiveBackend)
		if !ok || interactiveBackend == nil {
			continue
		}
		if interactiveBackend.CanStartProgram(req) {
			return interactiveBackend, nil
		}
	}
	return nil, ErrNoSandboxBackend
}

// RunProgram orchestrates policy resolution, approval, backend selection,
// and backend execution for a request.
func (c *SandboxCoordinator) RunProgram(
	ctx context.Context,
	req SandboxRunRequest,
) (RunResult, error) {
	c = c.withDefaults()
	pol, err := c.ResolvePolicy(ctx, req)
	if err != nil {
		return RunResult{}, err
	}
	approval, err := c.DecideApproval(ctx, req, pol)
	if err != nil {
		return RunResult{}, err
	}
	switch approval.Action {
	case ApprovalActionAllow:
	case ApprovalActionPrompt:
		return RunResult{}, ErrSandboxApprovalRequired
	case ApprovalActionDeny:
		return RunResult{}, ErrSandboxApprovalDenied
	default:
		return RunResult{}, ErrSandboxApprovalDenied
	}
	sandboxReq := SandboxRequest{
		Workspace:             req.Workspace,
		Spec:                  req.Spec,
		Policy:                pol,
		AdditionalPermissions: req.AdditionalPermissions,
		Metadata:              req.Metadata,
	}
	backend, err := c.SelectBackend(ctx, sandboxReq)
	if err != nil {
		return RunResult{}, err
	}
	return backend.RunProgram(ctx, sandboxReq)
}

// StartProgram orchestrates policy resolution, approval, backend selection,
// and backend session startup for an interactive request.
func (c *SandboxCoordinator) StartProgram(
	ctx context.Context,
	req SandboxStartProgramRequest,
) (ProgramSession, error) {
	c = c.withDefaults()
	pol, err := c.ResolvePolicy(ctx, SandboxRunRequest{
		Intent:                req.Intent,
		Workspace:             req.Workspace,
		Spec:                  req.Spec.RunProgramSpec,
		AdditionalPermissions: req.AdditionalPermissions,
		Metadata:              req.Metadata,
	})
	if err != nil {
		return nil, err
	}
	approval, err := c.DecideApproval(ctx, SandboxRunRequest{
		Intent:                req.Intent,
		Workspace:             req.Workspace,
		Spec:                  req.Spec.RunProgramSpec,
		AdditionalPermissions: req.AdditionalPermissions,
		Metadata:              req.Metadata,
	}, pol)
	if err != nil {
		return nil, err
	}
	switch approval.Action {
	case ApprovalActionAllow:
	case ApprovalActionPrompt:
		return nil, ErrSandboxApprovalRequired
	case ApprovalActionDeny:
		return nil, ErrSandboxApprovalDenied
	default:
		return nil, ErrSandboxApprovalDenied
	}
	interactiveReq := SandboxInteractiveRequest{
		Workspace:             req.Workspace,
		Spec:                  req.Spec,
		Policy:                pol,
		AdditionalPermissions: req.AdditionalPermissions,
		Metadata:              req.Metadata,
	}
	backend, err := c.SelectInteractiveBackend(ctx, interactiveReq)
	if err != nil {
		return nil, err
	}
	return backend.StartProgram(ctx, interactiveReq)
}

// StaticSandboxPolicyResolver always returns the configured policy.
type StaticSandboxPolicyResolver struct {
	Policy ExecutionPolicy
}

// ResolveSandboxPolicy implements SandboxPolicyResolver.
func (r StaticSandboxPolicyResolver) ResolveSandboxPolicy(
	_ context.Context,
	req PolicyResolveRequest,
) (ExecutionPolicy, error) {
	pol := r.Policy
	if pol.Intent == "" {
		pol.Intent = req.Intent
	}
	return pol, nil
}

// AllowAllApprovalDecider allows every request.
type AllowAllApprovalDecider struct{}

// DecideSandboxApproval implements ApprovalDecider.
func (AllowAllApprovalDecider) DecideSandboxApproval(
	_ context.Context,
	_ ApprovalRequest,
) (ApprovalResult, error) {
	return ApprovalResult{
		Action: ApprovalActionAllow,
		Rule:   "default_allow_all",
	}, nil
}

// FirstCompatibleSandboxBackendSelector picks the first backend that accepts a request.
type FirstCompatibleSandboxBackendSelector struct{}

// SelectSandboxBackend implements SandboxBackendSelector.
func (FirstCompatibleSandboxBackendSelector) SelectSandboxBackend(
	_ context.Context,
	req SandboxRequest,
	backends []SandboxBackend,
) (SandboxBackend, error) {
	for _, backend := range backends {
		if backend == nil {
			continue
		}
		if backend.CanApply(req) {
			return backend, nil
		}
	}
	return nil, ErrNoSandboxBackend
}

// SelectSandboxInteractiveBackend implements
// SandboxInteractiveBackendSelector.
func (FirstCompatibleSandboxBackendSelector) SelectSandboxInteractiveBackend(
	_ context.Context,
	req SandboxInteractiveRequest,
	backends []SandboxBackend,
) (SandboxInteractiveBackend, error) {
	for _, backend := range backends {
		interactiveBackend, ok := backend.(SandboxInteractiveBackend)
		if !ok || interactiveBackend == nil {
			continue
		}
		if interactiveBackend.CanStartProgram(req) {
			return interactiveBackend, nil
		}
	}
	return nil, ErrNoSandboxBackend
}
