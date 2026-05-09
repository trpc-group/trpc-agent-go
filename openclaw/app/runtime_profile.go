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
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/cron"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/runtimeprofile"
)

const unsupportedRuntimeProfileAgentFmt = "runtime_profiles.profiles.%s." +
	"agent_name: unsupported agent name %q; OpenClaw currently supports %q"

const runtimeProfileRequiredFmt = "runtime profile resolution failed: %w"

const unknownRuntimeProfileID = "unknown"

func buildRuntimeProfileRunOptionResolver(
	resolver runtimeprofile.Resolver,
	required bool,
) gateway.RunOptionResolver {
	if resolver == nil {
		return nil
	}
	return func(
		ctx context.Context,
		input gateway.RunOptionInput,
	) (context.Context, []agent.RunOption, error) {
		req, explicit, err := runtimeProfileRequest(input)
		if err != nil {
			return ctx, nil, fmt.Errorf(runtimeProfileRequiredFmt, err)
		}
		profile, err := resolver.Resolve(ctx, req)
		if err != nil {
			if errors.Is(err, runtimeprofile.ErrProfileNotFound) &&
				!required && !explicit {
				return ctx, nil, nil
			}
			return ctx, nil, fmt.Errorf(runtimeProfileRequiredFmt, err)
		}
		if !runtimeprofile.HasProfile(profile) {
			if required {
				return ctx, nil, fmt.Errorf(
					runtimeProfileRequiredFmt,
					runtimeprofile.ErrProfileNotFound,
				)
			}
			return ctx, nil, nil
		}
		ctx = runtimeprofile.WithProfile(ctx, profile)
		runOpts := runtimeprofile.RunOptions(profile)
		if len(runOpts) == 0 {
			return ctx, nil, nil
		}
		return ctx, runOpts, nil
	}
}

func appendRuntimeProfileGatewayOption(
	opts []gateway.Option,
	resolver runtimeprofile.Resolver,
	required bool,
) []gateway.Option {
	if resolver == nil {
		return opts
	}
	runOptionResolver := buildRuntimeProfileRunOptionResolver(
		resolver,
		required,
	)
	return append(opts, gateway.WithRunOptionResolver(runOptionResolver))
}

func newRuntimeProfileResolver(
	cfg *runtimeprofile.Config,
) runtimeprofile.Resolver {
	if cfg == nil {
		return nil
	}
	if err := runtimeprofile.ValidateConfig(*cfg); err != nil {
		return nil
	}
	if runtimeprofile.NewMapResolver(*cfg) == nil {
		return nil
	}
	return runtimeprofile.NewCachedResolver(
		runtimeprofile.StaticStore{Config: *cfg},
	)
}

func runtimeProfileCronOptions(
	resolver runtimeprofile.Resolver,
) []cron.Option {
	if resolver == nil {
		return nil
	}
	return []cron.Option{cron.WithRuntimeProfileResolver(resolver)}
}

func runtimeProfileResolverFromOptions(
	cfg *runtimeprofile.Config,
	runtimeOpts runtimeOptions,
) (runtimeprofile.Resolver, runtimeprofile.Catalog, bool) {
	var (
		resolver runtimeprofile.Resolver
		required bool
	)
	if runtimeOpts.runtimeProfileResolver != nil {
		resolver = runtimeOpts.runtimeProfileResolver
		required = runtimeOpts.runtimeProfileRequired
	} else {
		resolver = newRuntimeProfileResolver(cfg)
		required = runtimeProfileRequired(cfg)
	}
	return resolver,
		runtimeProfileCatalogFromOptions(
			resolver,
			runtimeOpts.runtimeProfileCatalog,
		),
		required
}

type runtimeProfileCatalogs []runtimeprofile.Catalog

func (c runtimeProfileCatalogs) ProfileIDs(
	ctx context.Context,
) ([]string, error) {
	var out []string
	for _, catalog := range c {
		values, err := catalog.ProfileIDs(ctx)
		if err != nil {
			return nil, err
		}
		out = appendUniqueRuntimeProfileCatalogValues(out, values...)
	}
	return out, nil
}

func (c runtimeProfileCatalogs) AppNames(
	ctx context.Context,
) ([]string, error) {
	var out []string
	for _, catalog := range c {
		values, err := catalog.AppNames(ctx)
		if err != nil {
			return nil, err
		}
		out = appendUniqueRuntimeProfileCatalogValues(out, values...)
	}
	return out, nil
}

func runtimeProfileCatalogFromOptions(
	resolver runtimeprofile.Resolver,
	injected runtimeprofile.Catalog,
) runtimeprofile.Catalog {
	catalogs := make([]runtimeprofile.Catalog, 0, 2)
	if catalog, ok := resolver.(runtimeprofile.Catalog); ok && catalog != nil {
		catalogs = append(catalogs, catalog)
	}
	if injected != nil {
		catalogs = append(catalogs, injected)
	}
	switch len(catalogs) {
	case 0:
		return nil
	case 1:
		return catalogs[0]
	default:
		return runtimeProfileCatalogs(catalogs)
	}
}

func appendUniqueRuntimeProfileCatalogValues(
	base []string,
	extra ...string,
) []string {
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, value := range append(base, extra...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func runtimeProfileRequired(cfg *runtimeprofile.Config) bool {
	if cfg == nil {
		return false
	}
	return cfg.Required
}

func validateRuntimeProfiles(cfg *runtimeprofile.Config) error {
	if cfg == nil {
		return nil
	}
	if err := runtimeprofile.ValidateConfig(*cfg); err != nil {
		return err
	}
	for key, profile := range cfg.Profiles {
		agentName := strings.TrimSpace(profile.AgentName)
		if agentName == "" || agentName == defaultAgentName {
			continue
		}
		return fmt.Errorf(
			unsupportedRuntimeProfileAgentFmt,
			runtimeProfileIDForError(key, profile),
			agentName,
			defaultAgentName,
		)
	}
	return nil
}

func runtimeProfileAppNames(cfg *runtimeprofile.Config) []string {
	if cfg == nil || len(cfg.Profiles) == 0 {
		return nil
	}
	appNames := make([]string, 0, len(cfg.Profiles))
	for _, profile := range cfg.Profiles {
		appNames = appendUniqueAppNames(appNames, profile.AppName)
	}
	return appNames
}

func runtimeProfileIDForError(
	key string,
	profile runtimeprofile.Profile,
) string {
	if key = strings.TrimSpace(key); key != "" {
		return key
	}
	if id := strings.TrimSpace(profile.ID); id != "" {
		return id
	}
	return unknownRuntimeProfileID
}

func appendUniqueAppNames(base []string, extra ...string) []string {
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, appName := range append(base, extra...) {
		appName = strings.TrimSpace(appName)
		if appName == "" {
			continue
		}
		if _, ok := seen[appName]; ok {
			continue
		}
		seen[appName] = struct{}{}
		out = append(out, appName)
	}
	return out
}

func runtimeProfileRequest(
	input gateway.RunOptionInput,
) (runtimeprofile.Request, bool, error) {
	ext, ok, err := runtimeprofile.ExtensionFromRequestExtensions(
		input.Extensions,
	)
	if err != nil {
		return runtimeProfileBaseRequest(input), false, err
	}
	req := runtimeProfileBaseRequest(input)
	if !ok {
		return req, false, nil
	}
	req.ProfileID = ext.ProfileID
	req.TenantID = ext.TenantID
	return req, true, nil
}

func runtimeProfileBaseRequest(
	input gateway.RunOptionInput,
) runtimeprofile.Request {
	return runtimeprofile.Request{
		Channel:    input.Inbound.Channel,
		UserID:     input.UserID,
		SessionID:  input.SessionID,
		RequestID:  input.RequestID,
		Extensions: input.Extensions,
	}
}
