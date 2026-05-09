//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/runtimeprofile"
)

const (
	testRuntimeProfileID     = "retail"
	testRuntimeProfileTenant = "tenant-a"
)

func TestBuildRuntimeProfileRunOptionResolver(t *testing.T) {
	t.Parallel()

	rawExtension, err := json.Marshal(runtimeprofile.Extension{
		ProfileID: testRuntimeProfileID,
		TenantID:  testRuntimeProfileTenant,
	})
	require.NoError(t, err)

	resolver := buildRuntimeProfileRunOptionResolver(
		runtimeprofile.ResolverFunc(func(
			ctx context.Context,
			req runtimeprofile.Request,
		) (runtimeprofile.Profile, error) {
			require.NotNil(t, ctx)
			require.Equal(t, "wecom", req.Channel)
			require.Equal(t, testRuntimeProfileID, req.ProfileID)
			require.Equal(t, testRuntimeProfileTenant, req.TenantID)
			require.Equal(t, "user-a", req.UserID)
			require.Equal(t, "session-a", req.SessionID)
			require.Equal(t, "request-a", req.RequestID)
			return runtimeprofile.Profile{
				ID:      req.ProfileID,
				AppName: "retail-app",
				Prompt: runtimeprofile.Prompt{
					Instruction: "help retail customers",
				},
			}, nil
		}),
	)

	ctx, runOpts := resolver(context.Background(), gateway.RunOptionInput{
		Inbound: gateway.InboundMessage{
			Channel: "wecom",
		},
		UserID:    "user-a",
		SessionID: "session-a",
		RequestID: "request-a",
		Message:   model.NewUserMessage("hello"),
		Extensions: map[string]json.RawMessage{
			runtimeprofile.ExtensionKey: rawExtension,
		},
	})

	profile, ok := runtimeprofile.ProfileFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, testRuntimeProfileID, profile.ID)

	opts := agent.NewRunOptions(runOpts...)
	require.Equal(t, "retail-app", opts.AppName)
	require.Equal(t, "help retail customers", opts.Instruction)
	require.Equal(
		t,
		testRuntimeProfileID,
		opts.RuntimeState[runtimeprofile.RuntimeStateProfileID],
	)
}

func TestBuildRuntimeProfileRunOptionResolverSkipsErrors(t *testing.T) {
	t.Parallel()

	t.Run("nil resolver", func(t *testing.T) {
		t.Parallel()

		resolver := buildRuntimeProfileRunOptionResolver(nil)
		require.Nil(t, resolver)
	})

	t.Run("lookup error", func(t *testing.T) {
		t.Parallel()

		resolver := buildRuntimeProfileRunOptionResolver(
			runtimeprofile.ResolverFunc(func(
				context.Context,
				runtimeprofile.Request,
			) (runtimeprofile.Profile, error) {
				return runtimeprofile.Profile{}, errors.New(
					"lookup failed",
				)
			}),
		)

		ctx, runOpts := resolver(context.Background(), gateway.RunOptionInput{})
		_, ok := runtimeprofile.ProfileFromContext(ctx)
		require.False(t, ok)
		require.Empty(t, runOpts)
	})

	t.Run("missing profile", func(t *testing.T) {
		t.Parallel()

		resolver := buildRuntimeProfileRunOptionResolver(
			runtimeprofile.ResolverFunc(func(
				context.Context,
				runtimeprofile.Request,
			) (runtimeprofile.Profile, error) {
				return runtimeprofile.Profile{},
					runtimeprofile.ErrProfileNotFound
			}),
		)

		ctx, runOpts := resolver(context.Background(), gateway.RunOptionInput{})
		_, ok := runtimeprofile.ProfileFromContext(ctx)
		require.False(t, ok)
		require.Empty(t, runOpts)
	})

	t.Run("empty profile", func(t *testing.T) {
		t.Parallel()

		resolver := buildRuntimeProfileRunOptionResolver(
			runtimeprofile.ResolverFunc(func(
				context.Context,
				runtimeprofile.Request,
			) (runtimeprofile.Profile, error) {
				return runtimeprofile.Profile{}, nil
			}),
		)

		ctx, runOpts := resolver(context.Background(), gateway.RunOptionInput{})
		_, ok := runtimeprofile.ProfileFromContext(ctx)
		require.False(t, ok)
		require.Empty(t, runOpts)
	})
}

func TestAppendRuntimeProfileGatewayOption(t *testing.T) {
	t.Parallel()

	require.Empty(t, appendRuntimeProfileGatewayOption(nil, nil))

	emptyCfg := runtimeprofile.Config{}
	require.Empty(t, appendRuntimeProfileGatewayOption(nil, &emptyCfg))

	cfg := runtimeprofile.Config{
		Default: testRuntimeProfileID,
		Profiles: map[string]runtimeprofile.Profile{
			testRuntimeProfileID: {
				AppName: "retail-app",
			},
		},
	}
	require.Len(t, appendRuntimeProfileGatewayOption(nil, &cfg), 1)
}

func TestValidateRuntimeProfiles(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateRuntimeProfiles(nil))

	cfg := runtimeprofile.Config{
		Profiles: map[string]runtimeprofile.Profile{
			testRuntimeProfileID: {
				AgentName: defaultAgentName,
			},
		},
	}
	require.NoError(t, validateRuntimeProfiles(&cfg))

	cfg.Profiles[testRuntimeProfileID] = runtimeprofile.Profile{
		AgentName: "sales-agent",
	}
	err := validateRuntimeProfiles(&cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "runtime_profiles.profiles.retail")
	require.Contains(t, err.Error(), "sales-agent")
	require.Contains(t, err.Error(), defaultAgentName)

	require.Equal(
		t,
		"profile-id",
		runtimeProfileIDForError(" ", runtimeprofile.Profile{
			ID: " profile-id ",
		}),
	)
	require.Equal(
		t,
		unknownRuntimeProfileID,
		runtimeProfileIDForError(" ", runtimeprofile.Profile{}),
	)
}

func TestRuntimeProfileAppNames(t *testing.T) {
	t.Parallel()

	require.Empty(t, runtimeProfileAppNames(nil))

	cfg := runtimeprofile.Config{
		Profiles: map[string]runtimeprofile.Profile{
			"blank": {
				AppName: " ",
			},
			"retail": {
				AppName: " retail-app ",
			},
			"retail-alias": {
				AppName: "retail-app",
			},
			"support": {
				AppName: "support-app",
			},
		},
	}

	got := runtimeProfileAppNames(&cfg)
	require.ElementsMatch(t, []string{
		"retail-app",
		"support-app",
	}, got)
}

func TestRuntimeProfileRequestIgnoresMalformedExtension(t *testing.T) {
	t.Parallel()

	req := runtimeProfileRequest(gateway.RunOptionInput{
		UserID:    "user-a",
		SessionID: "session-a",
		Extensions: map[string]json.RawMessage{
			runtimeprofile.ExtensionKey: json.RawMessage("{"),
		},
	})
	require.Equal(t, "user-a", req.UserID)
	require.Equal(t, "session-a", req.SessionID)
	require.Empty(t, req.ProfileID)
	require.Empty(t, req.TenantID)
}
