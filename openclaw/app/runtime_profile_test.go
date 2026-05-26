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

type failingRuntimeProfileCatalog struct {
	err error
}

func (c failingRuntimeProfileCatalog) ProfileIDs(
	context.Context,
) ([]string, error) {
	return nil, c.err
}

func (c failingRuntimeProfileCatalog) AppNames(
	context.Context,
) ([]string, error) {
	return nil, c.err
}

type countingRuntimeProfileStore struct {
	loads int
	cfg   runtimeprofile.Config
	err   error
}

func (s *countingRuntimeProfileStore) Load(
	context.Context,
) (runtimeprofile.Config, error) {
	s.loads++
	if s.loads > 1 && s.err != nil {
		return runtimeprofile.Config{}, s.err
	}
	return s.cfg, nil
}

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
		false,
	)

	ctx, runOpts, err := resolver(context.Background(), gateway.RunOptionInput{
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
	require.NoError(t, err)

	profile, ok := runtimeprofile.ProfileFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, testRuntimeProfileID, profile.ID)

	req, ok := runtimeprofile.RequestFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "wecom", req.Channel)
	require.Equal(t, testRuntimeProfileTenant, req.TenantID)
	require.Equal(t, "session-a", req.SessionID)

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

		resolver := buildRuntimeProfileRunOptionResolver(nil, false)
		require.Nil(t, resolver)
	})

	t.Run("lookup error fails closed", func(t *testing.T) {
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
			false,
		)

		ctx, runOpts, err := resolver(
			context.Background(),
			gateway.RunOptionInput{},
		)
		require.Error(t, err)
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
			false,
		)

		ctx, runOpts, err := resolver(
			context.Background(),
			gateway.RunOptionInput{},
		)
		require.NoError(t, err)
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
			false,
		)

		ctx, runOpts, err := resolver(
			context.Background(),
			gateway.RunOptionInput{},
		)
		require.NoError(t, err)
		_, ok := runtimeprofile.ProfileFromContext(ctx)
		require.False(t, ok)
		require.Empty(t, runOpts)
	})
}

func TestBuildRuntimeProfileRunOptionResolverExplicitMissingProfile(
	t *testing.T,
) {
	t.Parallel()

	rawExtension, err := json.Marshal(runtimeprofile.Extension{
		ProfileID: testRuntimeProfileID,
	})
	require.NoError(t, err)

	resolver := buildRuntimeProfileRunOptionResolver(
		runtimeprofile.ResolverFunc(func(
			context.Context,
			runtimeprofile.Request,
		) (runtimeprofile.Profile, error) {
			return runtimeprofile.Profile{},
				runtimeprofile.ErrProfileNotFound
		}),
		false,
	)

	_, _, err = resolver(context.Background(), gateway.RunOptionInput{
		Extensions: map[string]json.RawMessage{
			runtimeprofile.ExtensionKey: rawExtension,
		},
	})
	require.ErrorIs(t, err, runtimeprofile.ErrProfileNotFound)
}

func TestBuildRuntimeProfileRunOptionResolverRequired(t *testing.T) {
	t.Parallel()

	resolver := buildRuntimeProfileRunOptionResolver(
		runtimeprofile.ResolverFunc(func(
			context.Context,
			runtimeprofile.Request,
		) (runtimeprofile.Profile, error) {
			return runtimeprofile.Profile{},
				runtimeprofile.ErrProfileNotFound
		}),
		true,
	)

	_, _, err := resolver(context.Background(), gateway.RunOptionInput{})
	require.Error(t, err)
	require.ErrorIs(t, err, runtimeprofile.ErrProfileNotFound)
}

func TestBuildRuntimeProfileRunOptionResolverContextOnlyProfile(
	t *testing.T,
) {
	t.Parallel()

	resolver := buildRuntimeProfileRunOptionResolver(
		runtimeprofile.ResolverFunc(func(
			context.Context,
			runtimeprofile.Request,
		) (runtimeprofile.Profile, error) {
			return runtimeprofile.Profile{
				ID: testRuntimeProfileID,
				Workspace: runtimeprofile.WorkspacePolicy{
					Workdir: "/tmp/retail",
				},
			}, nil
		}),
		false,
	)

	ctx, runOpts, err := resolver(
		context.Background(),
		gateway.RunOptionInput{},
	)
	require.NoError(t, err)
	require.NotEmpty(t, runOpts)
	profile, ok := runtimeprofile.ProfileFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, testRuntimeProfileID, profile.ID)
}

func TestAppendRuntimeProfileGatewayOption(t *testing.T) {
	t.Parallel()

	require.Empty(t, appendRuntimeProfileGatewayOption(nil, nil, false))
	require.Empty(t, runtimeProfileCronOptions(nil))

	emptyCfg := runtimeprofile.Config{}
	resolver, catalog, required := runtimeProfileResolverFromOptions(
		&emptyCfg,
		runtimeOptions{},
	)
	require.Nil(t, resolver)
	require.Nil(t, catalog)
	require.False(t, required)

	cfg := runtimeprofile.Config{
		Required: true,
		Default:  testRuntimeProfileID,
		Profiles: map[string]runtimeprofile.Profile{
			testRuntimeProfileID: {
				AppName: "retail-app",
			},
		},
	}
	resolver, catalog, required = runtimeProfileResolverFromOptions(
		&cfg,
		runtimeOptions{},
	)
	require.NotNil(t, resolver)
	require.NotNil(t, catalog)
	require.True(t, required)
	require.Len(
		t,
		appendRuntimeProfileGatewayOption(nil, resolver, required),
		1,
	)
	require.Len(t, runtimeProfileCronOptions(resolver), 1)
}

func TestRuntimeProfileResolverFromInjectedResolver(t *testing.T) {
	t.Parallel()

	const injectedProfileID = "injected"
	injected := runtimeprofile.ResolverFunc(func(
		context.Context,
		runtimeprofile.Request,
	) (runtimeprofile.Profile, error) {
		return runtimeprofile.Profile{ID: injectedProfileID}, nil
	})
	cfg := runtimeprofile.Config{
		Default: testRuntimeProfileID,
		Profiles: map[string]runtimeprofile.Profile{
			testRuntimeProfileID: {},
		},
	}

	runtimeOpts := buildRuntimeOptions([]RuntimeOption{
		nil,
		WithRuntimeProfileResolver(injected, true),
	})
	resolver, catalog, required := runtimeProfileResolverFromOptions(
		&cfg,
		runtimeOpts,
	)

	require.True(t, required)
	require.Nil(t, catalog)
	profile, err := resolver.Resolve(
		context.Background(),
		runtimeprofile.Request{},
	)
	require.NoError(t, err)
	require.Equal(t, injectedProfileID, profile.ID)
}

func TestRuntimeProfileResolverFromConfigSelectorsRequired(t *testing.T) {
	t.Parallel()

	cfg := runtimeprofile.Config{
		Profiles: map[string]runtimeprofile.Profile{
			testRuntimeProfileID: {},
		},
		Selectors: []runtimeprofile.Selector{
			{
				ProfileID: testRuntimeProfileID,
				Users:     []string{"user-a"},
			},
		},
	}

	resolver, _, required := runtimeProfileResolverFromOptions(
		&cfg,
		runtimeOptions{},
	)

	require.True(t, required)
	_, err := resolver.Resolve(
		context.Background(),
		runtimeprofile.Request{UserID: "user-b"},
	)
	require.ErrorIs(t, err, runtimeprofile.ErrProfileSelectorDenied)
}

func TestBuildRuntimeOptionsIgnoresNilRuntimeProfileInputs(t *testing.T) {
	t.Parallel()

	runtimeOpts := buildRuntimeOptions([]RuntimeOption{
		nil,
		WithRuntimeProfileResolver(nil, true),
		WithRuntimeProfileCatalog(nil),
	})

	require.Nil(t, runtimeOpts.runtimeProfileResolver)
	require.Nil(t, runtimeOpts.runtimeProfileCatalog)
	require.False(t, runtimeOpts.runtimeProfileRequired)
}

func TestRuntimeProfileResolverFromInjectedStore(t *testing.T) {
	t.Parallel()

	cfg := runtimeprofile.Config{
		Default: testRuntimeProfileID,
		Profiles: map[string]runtimeprofile.Profile{
			testRuntimeProfileID: {
				AppName: "retail-app",
			},
		},
	}
	runtimeOpts := buildRuntimeOptions([]RuntimeOption{
		WithRuntimeProfileStore(
			runtimeprofile.StaticStore{Config: cfg},
			false,
		),
	})
	resolver, catalog, required := runtimeProfileResolverFromOptions(
		nil,
		runtimeOpts,
	)

	require.False(t, required)
	require.NotNil(t, runtimeOpts.runtimeProfileResolver)
	require.NotNil(t, runtimeOpts.runtimeProfileCatalog)
	require.NotNil(t, catalog)
	profile, err := resolver.Resolve(
		context.Background(),
		runtimeprofile.Request{},
	)
	require.NoError(t, err)
	require.Equal(t, testRuntimeProfileID, profile.ID)
	require.Equal(t, "retail-app", profile.AppName)
}

func TestRuntimeProfileCatalogFromStoreIsDeduplicated(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("duplicate catalog load")
	store := &countingRuntimeProfileStore{
		cfg: runtimeprofile.Config{
			Profiles: map[string]runtimeprofile.Profile{
				testRuntimeProfileID: {
					AppName: "retail-app",
				},
			},
		},
		err: wantErr,
	}
	runtimeOpts := buildRuntimeOptions([]RuntimeOption{
		WithRuntimeProfileStore(store, false),
	})
	_, catalog, _ := runtimeProfileResolverFromOptions(nil, runtimeOpts)

	appNames, err := catalog.AppNames(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"retail-app"}, appNames)
	require.Equal(t, 1, store.loads)
}

func TestRuntimeProfileResolverFromInjectedCatalog(t *testing.T) {
	t.Parallel()

	cfg := runtimeprofile.Config{
		Required: true,
		Default:  testRuntimeProfileID,
		Profiles: map[string]runtimeprofile.Profile{
			testRuntimeProfileID: {
				AppName: "yaml-app",
			},
		},
	}
	catalog := runtimeprofile.StaticStore{Config: runtimeprofile.Config{
		Profiles: map[string]runtimeprofile.Profile{
			"retail": {
				AppName: "retail-app",
			},
		},
	}}
	runtimeOpts := buildRuntimeOptions([]RuntimeOption{
		WithRuntimeProfileCatalog(catalog),
	})
	resolver, gotCatalog, required := runtimeProfileResolverFromOptions(
		&cfg,
		runtimeOpts,
	)

	require.NotNil(t, resolver)
	require.True(t, required)
	require.NotNil(t, gotCatalog)
	profile, err := resolver.Resolve(
		context.Background(),
		runtimeprofile.Request{},
	)
	require.NoError(t, err)
	require.Equal(t, testRuntimeProfileID, profile.ID)

	appNames, err := gotCatalog.AppNames(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"yaml-app", "retail-app"}, appNames)
}

func TestRuntimeProfileCatalogsProfileIDs(t *testing.T) {
	t.Parallel()

	catalogs := runtimeProfileCatalogs{
		runtimeprofile.StaticStore{Config: runtimeprofile.Config{
			Profiles: map[string]runtimeprofile.Profile{
				"retail": {
					AppName: "retail-app",
				},
			},
		}},
		runtimeprofile.StaticStore{Config: runtimeprofile.Config{
			Profiles: map[string]runtimeprofile.Profile{
				"retail-alias": {
					ID:      "retail",
					AppName: "retail-app",
				},
				"support": {
					AppName: "support-app",
				},
			},
		}},
	}

	ids, err := catalogs.ProfileIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"retail", "support"}, ids)

	appNames, err := catalogs.AppNames(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"retail-app", "support-app"}, appNames)

	got := appendUniqueRuntimeProfileCatalogValues(
		[]string{"base"},
		" ",
		"base",
		"next",
	)
	require.Equal(t, []string{"base", "next"}, got)
}

func TestRuntimeProfileCatalogsReturnProfileIDErrors(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("catalog down")
	catalogs := runtimeProfileCatalogs{
		failingRuntimeProfileCatalog{err: wantErr},
	}

	ids, err := catalogs.ProfileIDs(context.Background())
	require.ErrorIs(t, err, wantErr)
	require.Nil(t, ids)
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

	cfg = runtimeprofile.Config{
		Default: "missing",
		Profiles: map[string]runtimeprofile.Profile{
			testRuntimeProfileID: {},
		},
	}
	err = validateRuntimeProfiles(&cfg)
	require.ErrorIs(t, err, runtimeprofile.ErrConfigInvalid)

	cfg = runtimeprofile.Config{
		Profiles: map[string]runtimeprofile.Profile{
			"a": {ID: testRuntimeProfileID},
			"b": {ID: testRuntimeProfileID},
		},
	}
	err = validateRuntimeProfiles(&cfg)
	require.ErrorIs(t, err, runtimeprofile.ErrConfigInvalid)

	cfg = runtimeprofile.Config{
		Profiles: map[string]runtimeprofile.Profile{
			testRuntimeProfileID: {
				Isolation: runtimeprofile.IsolationPolicy{Mode: "bad"},
			},
		},
	}
	err = validateRuntimeProfiles(&cfg)
	require.ErrorIs(t, err, runtimeprofile.ErrConfigInvalid)

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
			"isolated": {
				Isolation: runtimeprofile.IsolationPolicy{
					Mode: runtimeprofile.IsolationModeProfileCache,
				},
			},
			"service-key": {
				ID: "service-profile",
				Isolation: runtimeprofile.IsolationPolicy{
					Mode: runtimeprofile.IsolationModeService,
				},
			},
		},
	}

	got := runtimeProfileAppNames(&cfg)
	require.ElementsMatch(t, []string{
		"isolated",
		"retail-app",
		"service-profile",
		"support-app",
	}, got)
}

func TestRuntimeProfileRequestReturnsMalformedExtension(t *testing.T) {
	t.Parallel()

	req, explicit, err := runtimeProfileRequest(gateway.RunOptionInput{
		UserID:    "user-a",
		SessionID: "session-a",
		Extensions: map[string]json.RawMessage{
			runtimeprofile.ExtensionKey: json.RawMessage("{"),
		},
	})
	require.Error(t, err)
	require.False(t, explicit)
	require.Equal(t, "user-a", req.UserID)
	require.Equal(t, "session-a", req.SessionID)
	require.Empty(t, req.ProfileID)
	require.Empty(t, req.TenantID)
}
