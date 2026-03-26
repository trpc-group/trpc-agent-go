//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runcontrol

import (
	"context"
	"errors"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

var (
	ErrRunBusy      = errors.New("session run is busy")
	ErrRunLeaseLost = errors.New("session run lease lost")
)

type Policy string

const (
	PolicyEnqueue        Policy = "enqueue"
	PolicyCancelPrevious Policy = "cancel_previous"
	PolicyRejectIfBusy   Policy = "reject_if_busy"
)

type State string

const (
	StateRunning   State = "running"
	StateCompleted State = "completed"
	StateFailed    State = "failed"
	StateCanceled  State = "canceled"
)

type BeginRequest struct {
	SessionKey   session.Key
	RequestID    string
	InvocationID string
	AgentName    string
	NodeID       string

	Policy      Policy
	WaitTimeout time.Duration
	LeaseTTL    time.Duration
	CancelGrace time.Duration
}

type Lease struct {
	SessionKey session.Key
	RequestID  string
	LeaseToken string
	NodeID     string
}

type Permit struct {
	Lease Lease
	State State
}

type RenewResult struct {
	CancelRequested bool
	CancelReason    string
	CancelSeq       int64
	CancelGrace     time.Duration
}

type FinishRequest struct {
	Status       State
	ErrorMessage string
	LineageID    string
	CheckpointID string
	CheckpointNS string
}

type CancelRequest struct {
	SessionKey  session.Key
	RequestID   string
	Reason      string
	CancelGrace time.Duration
}

type Service interface {
	BeginRun(ctx context.Context, req BeginRequest) (*Permit, error)
	RenewRun(ctx context.Context, lease Lease, ttl time.Duration) (*RenewResult, error)
	FinishRun(ctx context.Context, lease Lease, req FinishRequest) error
	CancelRun(ctx context.Context, req CancelRequest) error
}
