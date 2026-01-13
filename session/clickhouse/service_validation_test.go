//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestService_UpdateSessionState_InvalidKey(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	// Empty app name
	key := session.Key{AppName: "", UserID: "user", SessionID: "sess"}
	err := s.UpdateSessionState(ctx, key, session.StateMap{"k": []byte("v")})
	assert.Error(t, err)
	assert.Equal(t, session.ErrAppNameRequired, err)
}

func TestService_UpdateAppState_InvalidKey(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	// Empty app name
	err := s.UpdateAppState(ctx, "", session.StateMap{"k": []byte("v")})
	assert.Error(t, err)
}

func TestService_UpdateUserState_InvalidKey(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	// Empty user id
	key := session.UserKey{AppName: "app", UserID: ""}
	err := s.UpdateUserState(ctx, key, session.StateMap{"k": []byte("v")})
	assert.Error(t, err)
}

func TestService_DeleteAppState_InvalidKey(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	err := s.DeleteAppState(ctx, "", "key")
	assert.Error(t, err)
}

func TestService_DeleteUserState_InvalidKey(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	key := session.UserKey{AppName: "app", UserID: ""}
	err := s.DeleteUserState(ctx, key, "key")
	assert.Error(t, err)
}

func TestService_GetState_InvalidKey(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	_, err := s.ListAppStates(ctx, "")
	assert.Error(t, err)

	key := session.UserKey{AppName: "app", UserID: ""}
	_, err = s.ListUserStates(ctx, key)
	assert.Error(t, err)
}

func TestService_EnqueueSummaryJob_Invalid(t *testing.T) {
	s := &Service{
		opts: ServiceOpts{
			summarizer: &mockSummarizer{}, // Needs a non-nil summarizer to proceed to validation
		},
	}
	ctx := context.Background()
	// Nil session
	err := s.EnqueueSummaryJob(ctx, nil, "", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session is nil")
}

func TestService_CreateSessionSummary_Invalid(t *testing.T) {
	s := &Service{
		opts: ServiceOpts{
			summarizer: &mockSummarizer{}, // Needs a non-nil summarizer to proceed to validation
		},
	}
	ctx := context.Background()
	// Nil session
	err := s.CreateSessionSummary(ctx, nil, "", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session is nil")
}
