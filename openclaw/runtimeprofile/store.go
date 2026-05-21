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
	"sort"
	"strings"
	"sync"
)

// Store loads runtime profile configuration from any backing system.
//
// Implementations can read from files, databases, config centers, or remote
// control planes. CachedResolver stays small so those stores do not need to
// understand runner internals.
type Store interface {
	Load(ctx context.Context) (Config, error)
}

// Catalog lists profile metadata without exposing full profile definitions.
type Catalog interface {
	ProfileIDs(ctx context.Context) ([]string, error)
	AppNames(ctx context.Context) ([]string, error)
}

// StoreFunc adapts a function to Store.
type StoreFunc func(ctx context.Context) (Config, error)

// Load implements Store.
func (f StoreFunc) Load(ctx context.Context) (Config, error) {
	if f == nil {
		return Config{}, nil
	}
	return f(ctx)
}

// StaticStore stores one immutable profile config.
type StaticStore struct {
	Config Config
}

// Load implements Store.
func (s StaticStore) Load(context.Context) (Config, error) {
	return cloneConfig(s.Config), nil
}

// ProfileIDs implements Catalog.
func (s StaticStore) ProfileIDs(context.Context) ([]string, error) {
	return profileIDs(cloneConfig(s.Config)), nil
}

// AppNames implements Catalog.
func (s StaticStore) AppNames(context.Context) ([]string, error) {
	return appNames(cloneConfig(s.Config)), nil
}

// CachedResolver lazily loads profiles and keeps a version-keyed resolver
// snapshot until Reload or Invalidate is called.
type CachedResolver struct {
	mu       sync.RWMutex
	store    Store
	resolver Resolver
	loaded   bool
}

// NewCachedResolver creates a resolver backed by a reloadable store.
func NewCachedResolver(store Store) *CachedResolver {
	if store == nil {
		return nil
	}
	return &CachedResolver{store: store}
}

// Resolve implements Resolver.
func (r *CachedResolver) Resolve(
	ctx context.Context,
	req Request,
) (Profile, error) {
	resolver, err := r.resolverForRead(ctx)
	if err != nil || resolver == nil {
		return Profile{}, err
	}
	return resolver.Resolve(ctx, req)
}

// Reload refreshes the resolver snapshot from the store.
func (r *CachedResolver) Reload(ctx context.Context) error {
	if r == nil || r.store == nil {
		return nil
	}
	cfg, err := r.store.Load(ctx)
	if err != nil {
		return err
	}
	resolver, err := NewResolver(cfg)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolver = resolver
	r.loaded = true
	return nil
}

// Invalidate clears the current snapshot. The next Resolve will reload it.
func (r *CachedResolver) Invalidate() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolver = nil
	r.loaded = false
}

// ProfileIDs implements Catalog.
func (r *CachedResolver) ProfileIDs(ctx context.Context) ([]string, error) {
	cfg, err := r.configForCatalog(ctx)
	if err != nil {
		return nil, err
	}
	return profileIDs(cfg), nil
}

// AppNames implements Catalog.
func (r *CachedResolver) AppNames(ctx context.Context) ([]string, error) {
	cfg, err := r.configForCatalog(ctx)
	if err != nil {
		return nil, err
	}
	return appNames(cfg), nil
}

func (r *CachedResolver) resolverForRead(
	ctx context.Context,
) (Resolver, error) {
	if r == nil {
		return nil, nil
	}
	r.mu.RLock()
	resolver := r.resolver
	loaded := r.loaded
	r.mu.RUnlock()
	if loaded {
		return resolver, nil
	}
	if err := r.Reload(ctx); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.resolver, nil
}

func (r *CachedResolver) configForCatalog(
	ctx context.Context,
) (Config, error) {
	if r == nil || r.store == nil {
		return Config{}, nil
	}
	cfg, err := r.store.Load(ctx)
	if err != nil {
		return Config{}, err
	}
	return cloneConfig(cfg), nil
}

func cloneConfig(cfg Config) Config {
	cfg.Default = strings.TrimSpace(cfg.Default)
	cfg.Selectors = cloneSelectors(cfg.Selectors)
	if len(cfg.Profiles) == 0 {
		cfg.Profiles = nil
		return cfg
	}
	profiles := make(map[string]Profile, len(cfg.Profiles))
	for key, profile := range cfg.Profiles {
		profiles[key] = cloneProfile(profile)
	}
	cfg.Profiles = profiles
	return cfg
}

func profileIDs(cfg Config) []string {
	if len(cfg.Profiles) == 0 {
		return nil
	}
	out := make([]string, 0, len(cfg.Profiles))
	seen := make(map[string]struct{}, len(cfg.Profiles))
	for key, profile := range cfg.Profiles {
		id := strings.TrimSpace(profile.ID)
		if id == "" {
			id = strings.TrimSpace(key)
		}
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func appNames(cfg Config) []string {
	if len(cfg.Profiles) == 0 {
		return nil
	}
	out := make([]string, 0, len(cfg.Profiles))
	seen := make(map[string]struct{}, len(cfg.Profiles))
	for key, profile := range cfg.Profiles {
		id := strings.TrimSpace(profile.ID)
		if id == "" {
			profile.ID = strings.TrimSpace(key)
		}
		appName := RuntimeAppName(profile)
		if appName == "" {
			continue
		}
		if _, ok := seen[appName]; ok {
			continue
		}
		seen[appName] = struct{}{}
		out = append(out, appName)
	}
	sort.Strings(out)
	return out
}
