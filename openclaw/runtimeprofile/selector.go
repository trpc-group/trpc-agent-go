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
	"fmt"
	"strings"
)

const selectorConfigPath = "runtime_profiles.selectors"

// Selector maps request attributes to a runtime profile.
type Selector struct {
	ProfileID string   `yaml:"profile_id,omitempty"`
	Profile   string   `yaml:"profile,omitempty"`
	Channels  []string `yaml:"channels,omitempty"`
	Tenants   []string `yaml:"tenants,omitempty"`
	Users     []string `yaml:"users,omitempty"`
	Sessions  []string `yaml:"sessions,omitempty"`
}

// SelectorResolver chooses a selector profile before delegating to base.
type SelectorResolver struct {
	base      Resolver
	selectors []Selector
	aliases   map[string]string
}

// NewResolver creates a resolver from config, including selectors.
func NewResolver(cfg Config) (Resolver, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	base := NewMapResolver(cfg)
	aliases, err := profileIDAliases(cfg.Profiles)
	if err != nil {
		return nil, err
	}
	return newSelectorResolver(base, cfg.Selectors, aliases)
}

// NewSelectorResolver wraps base with selector-based profile selection.
func NewSelectorResolver(
	base Resolver,
	selectors []Selector,
) (Resolver, error) {
	return newSelectorResolver(base, selectors, nil)
}

func newSelectorResolver(
	base Resolver,
	selectors []Selector,
	aliases map[string]string,
) (Resolver, error) {
	cleaned, err := cleanSelectors(selectors)
	if err != nil {
		return nil, err
	}
	if len(cleaned) == 0 {
		return base, nil
	}
	if base == nil {
		return nil, fmt.Errorf(
			"%w: selectors need a base resolver",
			ErrConfigInvalid,
		)
	}
	if len(aliases) == 0 {
		aliases = resolverProfileAliases(base)
	}
	return &SelectorResolver{
		base:      base,
		selectors: cleaned,
		aliases:   copyStringMap(aliases),
	}, nil
}

// Resolve implements Resolver.
func (r *SelectorResolver) Resolve(
	ctx context.Context,
	req Request,
) (Profile, error) {
	if r == nil || r.base == nil {
		return Profile{}, nil
	}
	profileID := SelectProfileID(r.selectors, req)
	if profileID == "" {
		return Profile{}, fmt.Errorf(
			"%w: no selector matched",
			ErrProfileSelectorDenied,
		)
	}
	selected := r.canonicalProfileID(profileID)
	if requested := strings.TrimSpace(req.ProfileID); requested != "" &&
		r.canonicalProfileID(requested) != selected {
		return Profile{}, fmt.Errorf(
			"%w: requested profile %q does not match selector profile %q",
			ErrProfileSelectorDenied,
			requested,
			profileID,
		)
	}
	req.ProfileID = selected
	profile, err := r.base.Resolve(ctx, req)
	if errors.Is(err, ErrProfileNotFound) {
		return Profile{}, fmt.Errorf(
			"%w: selected profile %q was not found",
			ErrProfileSelectorDenied,
			profileID,
		)
	}
	if err != nil {
		return Profile{}, err
	}
	if !HasProfile(profile) {
		return Profile{}, fmt.Errorf(
			"%w: selected profile %q was empty",
			ErrProfileSelectorDenied,
			profileID,
		)
	}
	if resolvedID := strings.TrimSpace(profile.ID); resolvedID != "" &&
		r.canonicalProfileID(resolvedID) != selected {
		return Profile{}, fmt.Errorf(
			"%w: selected profile %q resolved to profile %q",
			ErrProfileSelectorDenied,
			profileID,
			resolvedID,
		)
	}
	return profile, nil
}

func (r *SelectorResolver) canonicalProfileID(profileID string) string {
	profileID = strings.TrimSpace(profileID)
	if r == nil || len(r.aliases) == 0 {
		return profileID
	}
	if effectiveID, ok := r.aliases[profileID]; ok {
		return effectiveID
	}
	return profileID
}

func resolverProfileAliases(base Resolver) map[string]string {
	resolver, ok := base.(*MapResolver)
	if !ok || resolver == nil || len(resolver.profiles) == 0 {
		return nil
	}
	aliases := make(map[string]string, len(resolver.profiles))
	for alias, key := range resolver.profiles {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		profile, ok := resolver.cache[key]
		if !ok {
			continue
		}
		effectiveID := strings.TrimSpace(profile.ID)
		if effectiveID == "" {
			effectiveID = strings.TrimSpace(key.id)
		}
		if effectiveID != "" {
			aliases[alias] = effectiveID
		}
	}
	return aliases
}

// ProfileIDs implements Catalog when the base resolver also implements it.
func (r *SelectorResolver) ProfileIDs(
	ctx context.Context,
) ([]string, error) {
	catalog, ok := r.base.(Catalog)
	if !ok || catalog == nil {
		return nil, nil
	}
	return catalog.ProfileIDs(ctx)
}

// AppNames implements Catalog when the base resolver also implements it.
func (r *SelectorResolver) AppNames(
	ctx context.Context,
) ([]string, error) {
	catalog, ok := r.base.(Catalog)
	if !ok || catalog == nil {
		return nil, nil
	}
	return catalog.AppNames(ctx)
}

// SelectProfileID returns the first selector profile that matches req.
func SelectProfileID(
	selectors []Selector,
	req Request,
) string {
	for _, selector := range selectors {
		if selector.matches(req) {
			profileID, err := selector.runtimeProfileID()
			if err != nil {
				return ""
			}
			return profileID
		}
	}
	return ""
}

func validateSelectors(
	selectors []Selector,
	known map[string]string,
) error {
	for i, selector := range selectors {
		profileID, err := selector.runtimeProfileID()
		if err != nil {
			return selectorError(i, err)
		}
		if profileID == "" {
			return selectorError(i, errors.New("profile_id is required"))
		}
		if !selector.hasCriteria() {
			return selectorError(
				i,
				errors.New("at least one match field is required"),
			)
		}
		if len(known) == 0 {
			continue
		}
		if _, ok := known[profileID]; !ok {
			return selectorError(
				i,
				fmt.Errorf("unknown profile_id %q", profileID),
			)
		}
	}
	return nil
}

func cleanSelectors(selectors []Selector) ([]Selector, error) {
	if err := validateSelectors(selectors, nil); err != nil {
		return nil, err
	}
	if len(selectors) == 0 {
		return nil, nil
	}
	out := make([]Selector, 0, len(selectors))
	for _, selector := range selectors {
		profileID, _ := selector.runtimeProfileID()
		out = append(out, Selector{
			ProfileID: profileID,
			Channels:  cleanStrings(selector.Channels),
			Tenants:   cleanStrings(selector.Tenants),
			Users:     cleanStrings(selector.Users),
			Sessions:  cleanStrings(selector.Sessions),
		})
	}
	return out, nil
}

func selectorError(index int, err error) error {
	return fmt.Errorf(
		"%w: %s[%d]: %w",
		ErrConfigInvalid,
		selectorConfigPath,
		index,
		err,
	)
}

func (s Selector) runtimeProfileID() (string, error) {
	profileID := strings.TrimSpace(s.ProfileID)
	alias := strings.TrimSpace(s.Profile)
	if profileID != "" && alias != "" && profileID != alias {
		return "", fmt.Errorf(
			"profile_id %q conflicts with profile %q",
			profileID,
			alias,
		)
	}
	if profileID != "" {
		return profileID, nil
	}
	return alias, nil
}

func (s Selector) hasCriteria() bool {
	return len(cleanStrings(s.Channels)) > 0 ||
		len(cleanStrings(s.Tenants)) > 0 ||
		len(cleanStrings(s.Users)) > 0 ||
		len(cleanStrings(s.Sessions)) > 0
}

func (s Selector) matches(req Request) bool {
	return matchesSelectorValue(s.Channels, req.Channel) &&
		matchesSelectorValue(s.Tenants, req.TenantID) &&
		matchesSelectorValue(s.Users, req.UserID) &&
		matchesSelectorValue(s.Sessions, req.SessionID)
}

func matchesSelectorValue(values []string, got string) bool {
	values = cleanStrings(values)
	if len(values) == 0 {
		return true
	}
	got = strings.TrimSpace(got)
	for _, value := range values {
		if value == got {
			return true
		}
	}
	return false
}
