//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

func TestAuthenticationRequiredErrorClassification(t *testing.T) {
	t.Parallel()

	requestErr := acpsdk.NewAuthRequired(helperAuthData())
	wrapped := fmt.Errorf("session/new: %w", requestErr)
	required, ok := authenticationRequiredError(
		string(sessionAcquisitionNew),
		helperInitializeResponse().AuthMethods,
		wrapped,
	)
	require.True(t, ok)
	require.Equal(t, "session/new", required.Operation)
	require.Equal(t, helperAuthData(), required.Data)
	require.Len(t, required.Methods, 2)
	require.Equal(t, AuthMethodTypeAgent, required.Methods[0].Type)
	require.Equal(t, AuthMethodTypeTerminal, required.Methods[1].Type)

	var typed *AuthRequiredError
	require.ErrorAs(t, required, &typed)
	require.ErrorContains(t, typed, "session/new")
	require.ErrorIs(t, typed, requestErr)

	wrongCode := &acpsdk.RequestError{
		Code:    -32001,
		Message: "Authentication required",
	}
	_, ok = authenticationRequiredError(
		string(sessionAcquisitionNew),
		helperInitializeResponse().AuthMethods,
		wrongCode,
	)
	require.False(t, ok)
}

func TestWithSessionAuthenticationOnlyAcceptsSessionAcquisition(t *testing.T) {
	t.Parallel()

	manager := &connectionManager{}
	process := &processConnection{}
	called := false
	err := manager.withSessionAuthentication(
		context.Background(),
		process,
		sessionAcquisitionOperation("session/prompt"),
		func(context.Context) error {
			called = true
			return nil
		},
	)
	require.ErrorIs(t, err, errSessionAuthOperation)
	require.False(t, called)
}

func TestWithSessionAuthenticationPassesThroughWhenNotRequired(t *testing.T) {
	t.Parallel()

	manager := &connectionManager{}
	process := &processConnection{}
	calls := 0
	err := manager.withSessionAuthentication(
		context.Background(),
		process,
		sessionAcquisitionNew,
		func(context.Context) error {
			calls++
			return nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, 1, calls)

	operationErr := errors.New("operation failed")
	err = manager.withSessionAuthentication(
		context.Background(),
		process,
		sessionAcquisitionLoad,
		func(context.Context) error {
			return operationErr
		},
	)
	require.ErrorIs(t, err, operationErr)
}

func TestWithSessionAuthenticationReturnsRequiredWithoutHandler(t *testing.T) {
	t.Parallel()

	manager := &connectionManager{}
	process := &processConnection{
		agentDescriptor: acpAgentDescriptor{
			authMethods: helperInitializeResponse().AuthMethods,
		},
	}
	err := manager.withSessionAuthentication(
		context.Background(),
		process,
		sessionAcquisitionNew,
		func(context.Context) error {
			return acpsdk.NewAuthRequired(helperAuthData())
		},
	)
	var required *AuthRequiredError
	require.ErrorAs(t, err, &required)
	require.Equal(t, helperAuthData(), required.Data)
}

func TestWithSessionAuthenticationRequiresAdvertisedMethods(t *testing.T) {
	t.Parallel()

	handlerCalled := false
	manager := &connectionManager{
		authSelector: AuthMethodSelectorFunc(func(
			context.Context,
			AuthChallenge,
		) (AuthSelection, error) {
			handlerCalled = true
			return AuthSelection{MethodID: "agent-login"}, nil
		}),
	}
	process := &processConnection{}
	err := manager.withSessionAuthentication(
		context.Background(),
		process,
		sessionAcquisitionResume,
		func(context.Context) error {
			return acpsdk.NewAuthRequired(nil)
		},
	)
	require.ErrorIs(t, err, errNoAdvertisedAuthMethods)
	var required *AuthRequiredError
	require.ErrorAs(t, err, &required)
	require.False(t, handlerCalled)
}

func TestPerformAgentAuthenticationValidatesSelection(t *testing.T) {
	t.Parallel()

	methods := advertisedAuthMethods(helperInitializeResponse().AuthMethods)
	tests := []struct {
		name      string
		selection AuthSelection
		wantErr   error
	}{
		{
			name:    "empty method ID",
			wantErr: errAuthMethodRequired,
		},
		{
			name:      "unknown method",
			selection: AuthSelection{MethodID: "missing"},
			wantErr:   errAuthMethodNotFound,
		},
		{
			name:      "terminal method",
			selection: AuthSelection{MethodID: "terminal-login"},
			wantErr:   errTerminalAuthPending,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manager := &connectionManager{
				authSelector: AuthMethodSelectorFunc(func(
					context.Context,
					AuthChallenge,
				) (AuthSelection, error) {
					return test.selection, nil
				}),
			}
			err := manager.performAgentAuthentication(
				context.Background(),
				&processConnection{},
				AuthChallenge{
					Operation: string(sessionAcquisitionNew),
					Methods:   methods,
				},
			)
			require.ErrorIs(t, err, test.wantErr)
		})
	}
}

func TestWithSessionAuthenticationSelectorErrorDoesNotRetry(t *testing.T) {
	t.Parallel()

	handlerErr := errors.New("selection failed")
	manager := &connectionManager{
		authSelector: AuthMethodSelectorFunc(func(
			context.Context,
			AuthChallenge,
		) (AuthSelection, error) {
			return AuthSelection{}, handlerErr
		}),
	}
	process := &processConnection{
		agentDescriptor: acpAgentDescriptor{
			authMethods: helperInitializeResponse().AuthMethods,
		},
	}
	calls := 0
	err := manager.withSessionAuthentication(
		context.Background(),
		process,
		sessionAcquisitionLoad,
		func(context.Context) error {
			calls++
			return acpsdk.NewAuthRequired(nil)
		},
	)
	require.ErrorIs(t, err, handlerErr)
	require.Equal(t, 1, calls)
}

func TestWithSessionAuthenticationAgentAuth(t *testing.T) {
	t.Parallel()

	var handlerCalls atomic.Int32
	var handlerOperation string
	var handlerMethodCount int
	agent := newHelperAgentWithOptions(
		t,
		"wait",
		io.Discard,
		false,
		WithAuthMethodSelectorFunc(AuthMethodSelectorFunc(func(
			_ context.Context,
			challenge AuthChallenge,
		) (AuthSelection, error) {
			handlerCalls.Add(1)
			handlerOperation = challenge.Operation
			handlerMethodCount = len(challenge.Methods)
			return AuthSelection{
				MethodID: "agent-login",
				Meta:     map[string]any{"tenant": "acme"},
			}, nil
		})),
	)
	process, err := agent.connections.connect(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, agent.Close())
	})

	calls := 0
	var response acpsdk.NewSessionResponse
	err = agent.connections.withSessionAuthentication(
		context.Background(),
		process,
		sessionAcquisitionNew,
		func(ctx context.Context) error {
			calls++
			var callErr error
			response, callErr = process.connection.NewSession(ctx, acpsdk.NewSessionRequest{
				Cwd:        t.TempDir(),
				McpServers: []acpsdk.McpServer{},
			})
			return callErr
		},
	)
	require.NoError(t, err)
	require.Equal(t, int32(1), handlerCalls.Load())
	require.Equal(t, "session/new", handlerOperation)
	require.Equal(t, 2, handlerMethodCount)
	require.Equal(t, 2, calls)
	require.Equal(t, acpsdk.SessionId("helper-session-1"), response.SessionId)
}

func TestWithSessionAuthenticationAuthenticateError(t *testing.T) {
	t.Parallel()

	agent := newHelperAgentWithOptions(
		t,
		"authenticate_error",
		io.Discard,
		false,
		WithAuthMethodSelector(agentLoginSelector()),
	)
	process, err := agent.connections.connect(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, agent.Close())
	})

	calls := 0
	err = agent.connections.withSessionAuthentication(
		context.Background(),
		process,
		sessionAcquisitionNew,
		func(ctx context.Context) error {
			calls++
			_, callErr := process.connection.NewSession(ctx, acpsdk.NewSessionRequest{
				Cwd:        t.TempDir(),
				McpServers: []acpsdk.McpServer{},
			})
			return callErr
		},
	)
	require.ErrorContains(t, err, "authenticate failed")
	require.Equal(t, 1, calls)
}

func TestWithSessionAuthenticationRetriesOnlyOnce(t *testing.T) {
	t.Parallel()

	agent := newHelperAgentWithOptions(
		t,
		"always_auth_required",
		io.Discard,
		false,
		WithAuthMethodSelectorFunc(agentLoginSelector()),
	)
	process, err := agent.connections.connect(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, agent.Close())
	})

	calls := 0
	err = agent.connections.withSessionAuthentication(
		context.Background(),
		process,
		sessionAcquisitionNew,
		func(ctx context.Context) error {
			calls++
			_, callErr := process.connection.NewSession(ctx, acpsdk.NewSessionRequest{
				Cwd:        t.TempDir(),
				McpServers: []acpsdk.McpServer{},
			})
			return callErr
		},
	)
	var required *AuthRequiredError
	require.ErrorAs(t, err, &required)
	require.Equal(t, 2, calls)
}

func TestWithSessionAuthenticationConcurrentRequestsAuthenticateOnce(t *testing.T) {
	t.Parallel()

	var handlerCalls atomic.Int32
	agent := newHelperAgentWithOptions(
		t,
		"wait",
		io.Discard,
		false,
		WithAuthMethodSelector(AuthMethodSelectorFunc(func(
			context.Context,
			AuthChallenge,
		) (AuthSelection, error) {
			handlerCalls.Add(1)
			time.Sleep(50 * time.Millisecond)
			return AuthSelection{
				MethodID: "agent-login",
				Meta:     map[string]any{"tenant": "acme"},
			}, nil
		})),
	)
	process, err := agent.connections.connect(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, agent.Close())
	})

	const callers = 8
	cwd := t.TempDir()
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			errs[index] = agent.connections.withSessionAuthentication(
				context.Background(),
				process,
				sessionAcquisitionNew,
				func(ctx context.Context) error {
					_, callErr := process.connection.NewSession(
						ctx,
						acpsdk.NewSessionRequest{
							Cwd:        cwd,
							McpServers: []acpsdk.McpServer{},
						},
					)
					return callErr
				},
			)
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), handlerCalls.Load())
}

func agentLoginSelector() AuthMethodSelectorFunc {
	return AuthMethodSelectorFunc(func(
		context.Context,
		AuthChallenge,
	) (AuthSelection, error) {
		return AuthSelection{
			MethodID: "agent-login",
			Meta:     map[string]any{"tenant": "acme"},
		}, nil
	})
}
