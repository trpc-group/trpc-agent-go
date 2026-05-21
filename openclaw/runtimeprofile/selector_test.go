//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runtimeprofile

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectorResolverSelectsProfile(t *testing.T) {
	t.Parallel()

	resolver, err := NewResolver(Config{
		Default: testProfileDefault,
		Profiles: map[string]Profile{
			testProfileDefault: {
				AppName: "default-app",
			},
			testProfileRetail: {
				AppName: "retail-app",
				Prompt: Prompt{
					Instruction: "help retail customers",
				},
			},
		},
		Selectors: []Selector{
			{
				ProfileID: testProfileRetail,
				Channels:  []string{"wecom"},
				Tenants:   []string{"tenant-a"},
				Users:     []string{"user-a"},
			},
		},
	})
	require.NoError(t, err)

	profile, err := resolver.Resolve(context.Background(), Request{
		Channel:  "wecom",
		TenantID: "tenant-a",
		UserID:   "user-a",
	})
	require.NoError(t, err)
	require.Equal(t, testProfileRetail, profile.ID)
	require.Equal(t, "help retail customers", profile.Prompt.Instruction)
}

func TestSelectorResolverRejectsMissAndMismatch(t *testing.T) {
	t.Parallel()

	resolver, err := NewResolver(Config{
		Profiles: map[string]Profile{
			testProfileDefault: {},
			testProfileRetail:  {},
		},
		Selectors: []Selector{
			{
				ProfileID: testProfileRetail,
				Channels:  []string{"wecom"},
				Users:     []string{"user-a"},
			},
		},
	})
	require.NoError(t, err)

	_, err = resolver.Resolve(context.Background(), Request{
		Channel: "wecom",
		UserID:  "user-b",
	})
	require.ErrorIs(t, err, ErrProfileSelectorDenied)
	require.False(t, errors.Is(err, ErrProfileNotFound))

	_, err = resolver.Resolve(context.Background(), Request{
		Channel:   "wecom",
		ProfileID: testProfileDefault,
		UserID:    "user-a",
	})
	require.ErrorIs(t, err, ErrProfileSelectorDenied)
}

func TestSelectorResolverRejectsStaleSelectorProfile(t *testing.T) {
	t.Parallel()

	base := ResolverFunc(func(
		context.Context,
		Request,
	) (Profile, error) {
		return Profile{}, ErrProfileNotFound
	})
	resolver, err := NewSelectorResolver(base, []Selector{
		{
			ProfileID: testProfileRetail,
			Users:     []string{"user-a"},
		},
	})
	require.NoError(t, err)

	_, err = resolver.Resolve(context.Background(), Request{UserID: "user-a"})
	require.ErrorIs(t, err, ErrProfileSelectorDenied)
	require.False(t, errors.Is(err, ErrProfileNotFound))
}

func TestSelectorResolverRejectsMissingBase(t *testing.T) {
	t.Parallel()

	_, err := NewSelectorResolver(nil, []Selector{
		{
			ProfileID: testProfileRetail,
			Users:     []string{"user-a"},
		},
	})
	require.ErrorIs(t, err, ErrConfigInvalid)
	require.Contains(t, err.Error(), "selectors need a base resolver")
}

func TestValidateConfigRejectsInvalidSelectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name: "unknown profile",
			cfg: Config{
				Profiles: map[string]Profile{
					testProfileDefault: {},
				},
				Selectors: []Selector{
					{
						ProfileID: testProfileRetail,
						Users:     []string{"user-a"},
					},
				},
			},
			wantErr: `unknown profile_id "retail"`,
		},
		{
			name: "missing criteria",
			cfg: Config{
				Profiles: map[string]Profile{
					testProfileDefault: {},
				},
				Selectors: []Selector{
					{ProfileID: testProfileDefault},
				},
			},
			wantErr: "at least one match field is required",
		},
		{
			name: "conflicting aliases",
			cfg: Config{
				Profiles: map[string]Profile{
					testProfileDefault: {},
					testProfileRetail:  {},
				},
				Selectors: []Selector{
					{
						ProfileID: testProfileDefault,
						Profile:   testProfileRetail,
						Users:     []string{"user-a"},
					},
				},
			},
			wantErr: `profile_id "default" conflicts with profile "retail"`,
		},
		{
			name: "selectors without profiles",
			cfg: Config{
				Selectors: []Selector{
					{
						ProfileID: testProfileDefault,
						Users:     []string{"user-a"},
					},
				},
			},
			wantErr: "selectors need configured profiles",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateConfig(tc.cfg)
			require.Error(t, err)
			require.ErrorIs(t, err, ErrConfigInvalid)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestSelectorResolverDelegatesCatalog(t *testing.T) {
	t.Parallel()

	resolver, err := NewResolver(Config{
		Profiles: map[string]Profile{
			testProfileRetail: {
				AppName: "retail-app",
			},
		},
		Selectors: []Selector{
			{
				ProfileID: testProfileRetail,
				Users:     []string{"user-a"},
			},
		},
	})
	require.NoError(t, err)

	catalog, ok := resolver.(Catalog)
	require.True(t, ok)
	ids, err := catalog.ProfileIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{testProfileRetail}, ids)
	appNames, err := catalog.AppNames(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"retail-app"}, appNames)
}
