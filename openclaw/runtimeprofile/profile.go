//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package runtimeprofile defines per-request OpenClaw runtime profiles.
package runtimeprofile

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// ExtensionKey is the request extension key used to select a profile.
	ExtensionKey = "openclaw.runtime_profile"

	// RuntimeStateProfileID is the runtime-state key for the profile id.
	RuntimeStateProfileID = "openclaw.profile.id"
	// RuntimeStateProfileVersion is the runtime-state key for the profile
	// version.
	RuntimeStateProfileVersion = "openclaw.profile.version"
)

// ErrProfileNotFound means a resolver could not find the selected profile.
var ErrProfileNotFound = errors.New("runtime profile not found")

type contextKey struct{}

// Config describes static runtime profiles.
type Config struct {
	Default  string             `yaml:"default,omitempty"`
	Profiles map[string]Profile `yaml:"profiles,omitempty"`
}

// Request describes one profile resolution request.
type Request struct {
	Channel    string
	ProfileID  string
	TenantID   string
	UserID     string
	SessionID  string
	RequestID  string
	Extensions map[string]json.RawMessage
}

// Extension is the normalized runtime-profile request extension.
type Extension struct {
	ProfileID string `json:"profile_id,omitempty"`
	TenantID  string `json:"tenant_id,omitempty"`
}

// Prompt defines prompt overrides for one run.
type Prompt struct {
	Instruction  string `yaml:"instruction,omitempty"`
	SystemPrompt string `yaml:"system_prompt,omitempty"`
}

// ToolPolicy defines name-based tool visibility and execution policy.
type ToolPolicy struct {
	Include          []string `yaml:"include,omitempty"`
	Exclude          []string `yaml:"exclude,omitempty"`
	ExecutionInclude []string `yaml:"execution_include,omitempty"`
	ExecutionExclude []string `yaml:"execution_exclude,omitempty"`
}

// KnowledgePolicy defines per-run knowledge query policy.
type KnowledgePolicy struct {
	Filter map[string]any `yaml:"filter,omitempty"`
}

// Profile is a resolved per-request runtime profile.
type Profile struct {
	ID         string          `yaml:"id,omitempty"`
	Version    string          `yaml:"version,omitempty"`
	AppName    string          `yaml:"app_name,omitempty"`
	AgentName  string          `yaml:"agent_name,omitempty"`
	ModelName  string          `yaml:"model_name,omitempty"`
	Prompt     Prompt          `yaml:"prompt,omitempty"`
	Tools      ToolPolicy      `yaml:"tools,omitempty"`
	Knowledge  KnowledgePolicy `yaml:"knowledge,omitempty"`
	State      map[string]any  `yaml:"runtime_state,omitempty"`
	ExtraModel map[string]any  `yaml:"model_request_extra,omitempty"`
}

// Resolver resolves one request to one runtime profile.
type Resolver interface {
	Resolve(ctx context.Context, req Request) (Profile, error)
}

// ResolverFunc adapts a function to Resolver.
type ResolverFunc func(ctx context.Context, req Request) (Profile, error)

// Resolve implements Resolver.
func (f ResolverFunc) Resolve(
	ctx context.Context,
	req Request,
) (Profile, error) {
	if f == nil {
		return Profile{}, nil
	}
	return f(ctx, req)
}

// WithProfile stores the resolved profile on the context.
func WithProfile(ctx context.Context, profile Profile) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, contextKey{}, cloneProfile(profile))
}

// ProfileFromContext returns the resolved profile stored on ctx.
func ProfileFromContext(ctx context.Context) (Profile, bool) {
	if ctx == nil {
		return Profile{}, false
	}
	profile, ok := ctx.Value(contextKey{}).(Profile)
	if !ok {
		return Profile{}, false
	}
	return cloneProfile(profile), true
}

// AppNameFromContext returns the profile app name or the provided fallback.
func AppNameFromContext(ctx context.Context, fallback string) string {
	if profile, ok := ProfileFromContext(ctx); ok {
		if appName := strings.TrimSpace(profile.AppName); appName != "" {
			return appName
		}
	}
	return strings.TrimSpace(fallback)
}

// MapResolver resolves profiles from an in-memory map.
type MapResolver struct {
	defaultID string
	profiles  map[string]Profile
}

// NewMapResolver creates a resolver backed by static profiles.
func NewMapResolver(cfg Config) *MapResolver {
	if len(cfg.Profiles) == 0 {
		return nil
	}
	profiles := make(map[string]Profile, len(cfg.Profiles))
	for key, profile := range cfg.Profiles {
		lookupID := strings.TrimSpace(key)
		if lookupID == "" {
			continue
		}
		id := strings.TrimSpace(profile.ID)
		if id == "" {
			id = lookupID
		}
		profile.ID = id
		profiles[lookupID] = cloneProfile(profile)
		if id != lookupID {
			profiles[id] = cloneProfile(profile)
		}
	}
	if len(profiles) == 0 {
		return nil
	}
	return &MapResolver{
		defaultID: strings.TrimSpace(cfg.Default),
		profiles:  profiles,
	}
}

// Resolve implements Resolver.
func (r *MapResolver) Resolve(
	ctx context.Context,
	req Request,
) (Profile, error) {
	_ = ctx
	if r == nil || len(r.profiles) == 0 {
		return Profile{}, nil
	}
	id := strings.TrimSpace(req.ProfileID)
	if id == "" {
		id = r.defaultID
	}
	if id == "" {
		return Profile{}, nil
	}
	profile, ok := r.profiles[id]
	if !ok {
		return Profile{}, ErrProfileNotFound
	}
	return cloneProfile(profile), nil
}

// ExtensionFromRequestExtensions reads the runtime-profile request extension.
func ExtensionFromRequestExtensions(
	extensions map[string]json.RawMessage,
) (Extension, bool, error) {
	if len(extensions) == 0 {
		return Extension{}, false, nil
	}
	raw, ok := extensions[ExtensionKey]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return Extension{}, false, nil
	}
	var ext Extension
	if err := json.Unmarshal(raw, &ext); err != nil {
		return Extension{}, false, err
	}
	ext.ProfileID = strings.TrimSpace(ext.ProfileID)
	ext.TenantID = strings.TrimSpace(ext.TenantID)
	return ext, true, nil
}

// RunOptions converts a profile to agent run options.
func RunOptions(profile Profile) []agent.RunOption {
	var opts []agent.RunOption
	if appName := strings.TrimSpace(profile.AppName); appName != "" {
		opts = append(opts, agent.WithAppName(appName))
	}
	if agentName := strings.TrimSpace(profile.AgentName); agentName != "" {
		opts = append(opts, agent.WithAgentByName(agentName))
	}
	if modelName := strings.TrimSpace(profile.ModelName); modelName != "" {
		opts = append(opts, agent.WithModelName(modelName))
	}
	if profile.Prompt.Instruction != "" {
		opts = append(
			opts,
			agent.WithInstruction(profile.Prompt.Instruction),
		)
	}
	if profile.Prompt.SystemPrompt != "" {
		opts = append(
			opts,
			agent.WithGlobalInstruction(profile.Prompt.SystemPrompt),
		)
	}
	if filter := toolNamesFilter(
		profile.Tools.Include,
		profile.Tools.Exclude,
	); filter != nil {
		opts = append(opts, agent.WithToolFilter(filter))
	}
	if filter := toolNamesFilter(
		profile.Tools.ExecutionInclude,
		profile.Tools.ExecutionExclude,
	); filter != nil {
		opts = append(opts, agent.WithToolExecutionFilter(filter))
	}
	if len(profile.Knowledge.Filter) > 0 {
		opts = append(
			opts,
			agent.WithKnowledgeFilter(copyAnyMap(
				profile.Knowledge.Filter,
			)),
		)
	}
	if len(profile.ExtraModel) > 0 {
		opts = append(
			opts,
			agent.WithModelRequestExtraFields(copyAnyMap(
				profile.ExtraModel,
			)),
		)
	}
	if state := runtimeState(profile); len(state) > 0 {
		opts = append(opts, agent.MergeRuntimeState(state))
	}
	return opts
}

func runtimeState(profile Profile) map[string]any {
	state := copyAnyMap(profile.State)
	if id := strings.TrimSpace(profile.ID); id != "" {
		if state == nil {
			state = make(map[string]any)
		}
		state[RuntimeStateProfileID] = id
	}
	if version := strings.TrimSpace(profile.Version); version != "" {
		if state == nil {
			state = make(map[string]any)
		}
		state[RuntimeStateProfileVersion] = version
	}
	return state
}

func toolNamesFilter(include []string, exclude []string) tool.FilterFunc {
	include = cleanStrings(include)
	exclude = cleanStrings(exclude)
	if len(include) == 0 && len(exclude) == 0 {
		return nil
	}
	included := nameSet(include)
	excluded := nameSet(exclude)
	return func(ctx context.Context, tl tool.Tool) bool {
		_ = ctx
		if tl == nil || tl.Declaration() == nil {
			return false
		}
		name := tl.Declaration().Name
		if _, blocked := excluded[name]; blocked {
			return false
		}
		if len(included) == 0 {
			return true
		}
		_, allowed := included[name]
		return allowed
	}
}

func nameSet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}

func cleanStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func cloneProfile(profile Profile) Profile {
	profile.Tools.Include = copyStrings(profile.Tools.Include)
	profile.Tools.Exclude = copyStrings(profile.Tools.Exclude)
	profile.Tools.ExecutionInclude = copyStrings(
		profile.Tools.ExecutionInclude,
	)
	profile.Tools.ExecutionExclude = copyStrings(
		profile.Tools.ExecutionExclude,
	)
	profile.Knowledge.Filter = copyAnyMap(profile.Knowledge.Filter)
	profile.State = copyAnyMap(profile.State)
	profile.ExtraModel = copyAnyMap(profile.ExtraModel)
	return profile
}

func copyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	copied := make([]string, len(values))
	copy(copied, values)
	return copied
}

func copyAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]any, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}
