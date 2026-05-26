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

func TestCachedResolverLoadsReloadsAndInvalidates(t *testing.T) {
	t.Parallel()

	loads := 0
	cfg := Config{
		Default: testProfileRetail,
		Profiles: map[string]Profile{
			testProfileRetail: {
				AppName: "retail-v1",
			},
		},
	}
	resolver := NewCachedResolver(StoreFunc(func(context.Context) (
		Config,
		error,
	) {
		loads++
		return cfg, nil
	}))

	profile, err := resolver.Resolve(context.Background(), Request{})
	require.NoError(t, err)
	require.Equal(t, "retail-v1", profile.AppName)
	require.Equal(t, 1, loads)

	cfg.Profiles[testProfileRetail] = Profile{AppName: "retail-v2"}
	profile, err = resolver.Resolve(context.Background(), Request{})
	require.NoError(t, err)
	require.Equal(t, "retail-v1", profile.AppName)
	require.Equal(t, 1, loads)

	require.NoError(t, resolver.Reload(context.Background()))
	profile, err = resolver.Resolve(context.Background(), Request{})
	require.NoError(t, err)
	require.Equal(t, "retail-v2", profile.AppName)
	require.Equal(t, 2, loads)

	resolver.Invalidate()
	profile, err = resolver.Resolve(context.Background(), Request{})
	require.NoError(t, err)
	require.Equal(t, "retail-v2", profile.AppName)
	require.Equal(t, 3, loads)
}

func TestCachedResolverStoreError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("store down")
	resolver := NewCachedResolver(StoreFunc(func(context.Context) (
		Config,
		error,
	) {
		return Config{}, wantErr
	}))

	_, err := resolver.Resolve(context.Background(), Request{})
	require.ErrorIs(t, err, wantErr)
}

func TestCachedResolverRejectsInvalidReload(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Default: testProfileRetail,
		Profiles: map[string]Profile{
			testProfileRetail: {
				AppName: "retail-v1",
			},
		},
	}
	resolver := NewCachedResolver(StoreFunc(func(context.Context) (
		Config,
		error,
	) {
		return cfg, nil
	}))
	profile, err := resolver.Resolve(context.Background(), Request{})
	require.NoError(t, err)
	require.Equal(t, "retail-v1", profile.AppName)

	cfg.Default = "missing"
	err = resolver.Reload(context.Background())
	require.ErrorIs(t, err, ErrConfigInvalid)

	profile, err = resolver.Resolve(context.Background(), Request{})
	require.NoError(t, err)
	require.Equal(t, "retail-v1", profile.AppName)
}

func TestCachedResolverInitialInvalidConfig(t *testing.T) {
	t.Parallel()

	resolver := NewCachedResolver(StaticStore{Config: Config{
		Default: "missing",
		Profiles: map[string]Profile{
			testProfileRetail: {},
		},
	}})

	_, err := resolver.Resolve(context.Background(), Request{})
	require.ErrorIs(t, err, ErrConfigInvalid)
}

func TestProfileCatalog(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Profiles: map[string]Profile{
			"retail-key": {
				ID:      testProfileRetail,
				AppName: "retail-app",
			},
			"support": {
				AppName: "support-app",
			},
			"support-alias": {
				AppName: "support-app",
			},
			"isolated": {
				Isolation: IsolationPolicy{
					Mode: IsolationModeProfileCache,
				},
			},
		},
	}

	store := StaticStore{Config: cfg}
	ids, err := store.ProfileIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{
		"isolated",
		testProfileRetail,
		"support",
		"support-alias",
	}, ids)

	appNames, err := store.AppNames(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{
		"isolated",
		"retail-app",
		"support-app",
	}, appNames)

	resolver := NewCachedResolver(store)
	appNames, err = resolver.AppNames(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{
		"isolated",
		"retail-app",
		"support-app",
	}, appNames)

	ids, err = resolver.ProfileIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{
		"isolated",
		testProfileRetail,
		"support",
		"support-alias",
	}, ids)
}

func TestStaticStoreCopiesSelectors(t *testing.T) {
	t.Parallel()

	store := StaticStore{Config: Config{
		Profiles: map[string]Profile{
			testProfileRetail: {},
		},
		Selectors: []Selector{
			{
				ProfileID: testProfileRetail,
				Channels:  []string{"wecom"},
				Tenants:   []string{"tenant-a"},
				Users:     []string{"user-a"},
				Sessions:  []string{"session-a"},
			},
		},
	}}

	cfg, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"wecom"}, cfg.Selectors[0].Channels)
	require.Equal(t, []string{"tenant-a"}, cfg.Selectors[0].Tenants)
	require.Equal(t, []string{"user-a"}, cfg.Selectors[0].Users)
	require.Equal(t, []string{"session-a"}, cfg.Selectors[0].Sessions)

	cfg.Selectors[0].Channels[0] = "changed"
	cfg.Selectors[0].Tenants[0] = "changed"
	cfg.Selectors[0].Users[0] = "changed"
	cfg.Selectors[0].Sessions[0] = "changed"

	cfg, err = store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"wecom"}, cfg.Selectors[0].Channels)
	require.Equal(t, []string{"tenant-a"}, cfg.Selectors[0].Tenants)
	require.Equal(t, []string{"user-a"}, cfg.Selectors[0].Users)
	require.Equal(t, []string{"session-a"}, cfg.Selectors[0].Sessions)
}

func TestCachedResolverCatalogErrorsAndNilBranches(t *testing.T) {
	t.Parallel()

	var nilStoreFunc StoreFunc
	cfg, err := nilStoreFunc.Load(context.Background())
	require.NoError(t, err)
	require.Empty(t, cfg)

	require.Nil(t, NewCachedResolver(nil))

	var nilResolver *CachedResolver
	require.NoError(t, nilResolver.Reload(context.Background()))
	nilResolver.Invalidate()

	ids, err := nilResolver.ProfileIDs(context.Background())
	require.NoError(t, err)
	require.Empty(t, ids)

	appNames, err := nilResolver.AppNames(context.Background())
	require.NoError(t, err)
	require.Empty(t, appNames)

	wantErr := errors.New("catalog down")
	resolver := NewCachedResolver(StoreFunc(func(context.Context) (
		Config,
		error,
	) {
		return Config{}, wantErr
	}))
	ids, err = resolver.ProfileIDs(context.Background())
	require.ErrorIs(t, err, wantErr)
	require.Nil(t, ids)
}
