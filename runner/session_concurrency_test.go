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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/runcontrol"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

type runControlRecordingService struct {
	*sessioninmemory.SessionService

	mu    sync.Mutex
	steps []string
	lease *runcontrol.Lease
}

func newRunControlRecordingService() *runControlRecordingService {
	return &runControlRecordingService{
		SessionService: sessioninmemory.NewSessionService(),
	}
}

func (s *runControlRecordingService) BeginRun(
	ctx context.Context,
	req runcontrol.BeginRequest,
) (*runcontrol.Permit, error) {
	s.mu.Lock()
	s.steps = append(s.steps, "begin")
	s.mu.Unlock()
	lease := runcontrol.Lease{
		SessionKey: req.SessionKey,
		RequestID:  req.RequestID,
		LeaseToken: "lease",
		NodeID:     req.NodeID,
	}
	s.lease = &lease
	return &runcontrol.Permit{Lease: lease, State: runcontrol.StateRunning}, nil
}

func (s *runControlRecordingService) RenewRun(
	ctx context.Context,
	lease runcontrol.Lease,
	ttl time.Duration,
) (*runcontrol.RenewResult, error) {
	return &runcontrol.RenewResult{}, nil
}

func (s *runControlRecordingService) FinishRun(
	ctx context.Context,
	lease runcontrol.Lease,
	req runcontrol.FinishRequest,
) error {
	s.mu.Lock()
	s.steps = append(s.steps, "finish")
	s.mu.Unlock()
	return nil
}

func (s *runControlRecordingService) CancelRun(
	ctx context.Context,
	req runcontrol.CancelRequest,
) error {
	return nil
}

func (s *runControlRecordingService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	opts ...session.Option,
) error {
	if evt != nil && evt.Response != nil && len(evt.Response.Choices) > 0 &&
		evt.Response.Choices[0].Message.Role == model.RoleUser {
		s.mu.Lock()
		s.steps = append(s.steps, "append_user")
		s.mu.Unlock()
	}
	return s.SessionService.AppendEvent(ctx, sess, evt, opts...)
}

func TestRunnerSessionConcurrencyAppendsUserAfterBeginRun(t *testing.T) {
	svc := newRunControlRecordingService()
	r := NewRunner(
		"app",
		&mockAgent{name: "agent"},
		WithSessionService(svc),
		WithSessionConcurrencyPolicy(SessionConcurrencyPolicy{
			Enabled:  true,
			NodeID:   "node-a",
			Policy:   runcontrol.PolicyRejectIfBusy,
			LeaseTTL: 5 * time.Second,
		}),
	)

	ch, err := r.Run(
		context.Background(),
		"user",
		"session",
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	for range ch {
	}

	svc.mu.Lock()
	defer svc.mu.Unlock()
	require.True(t, len(svc.steps) >= 2)
	require.Equal(t, "begin", svc.steps[0])
	require.Equal(t, "append_user", svc.steps[1])
}
