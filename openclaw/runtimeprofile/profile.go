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
	"fmt"
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
	// RuntimeStateWorkspaceWorkdir is the runtime-state key for the
	// profile workspace workdir.
	RuntimeStateWorkspaceWorkdir = "openclaw.profile.workspace.workdir"
	// RuntimeStateWorkspaceAllowedRoots is the runtime-state key for the
	// profile workspace allowed roots.
	RuntimeStateWorkspaceAllowedRoots = "openclaw.profile.workspace.roots"
	// RuntimeStateCredentialAllowedRefs is the runtime-state key for
	// profile credential references.
	RuntimeStateCredentialAllowedRefs = "openclaw.profile.credentials.refs"
	// RuntimeStateSkillInclude is the runtime-state key for profile skill
	// allowlists.
	RuntimeStateSkillInclude = "openclaw.profile.skills.include"
	// RuntimeStateSkillExclude is the runtime-state key for profile skill
	// denylists.
	RuntimeStateSkillExclude = "openclaw.profile.skills.exclude"
	// RuntimeStateSkillRoots is the runtime-state key for profile skill
	// repository roots.
	RuntimeStateSkillRoots = "openclaw.profile.skills.roots"
	// RuntimeStateKnowledgeIndexes is the runtime-state key for profile
	// knowledge indexes.
	RuntimeStateKnowledgeIndexes = "openclaw.profile.knowledge.indexes"
	// RuntimeStateToolSets is the runtime-state key for profile toolsets.
	RuntimeStateToolSets = "openclaw.profile.tools.toolsets"
	// RuntimeStateToolCredentialRefs is the runtime-state key for
	// tool credential references.
	RuntimeStateToolCredentialRefs = "openclaw.profile.tools.credentials"
	// RuntimeStateIsolationMode is the runtime-state key for the profile
	// isolation mode.
	RuntimeStateIsolationMode = "openclaw.profile.isolation.mode"
	// RuntimeStateIsolationAgentCache is the runtime-state key for
	// per-profile agent cache policy.
	RuntimeStateIsolationAgentCache = "openclaw.profile.isolation.agent_cache"
	// RuntimeStateIsolationToolSetCache is the runtime-state key for
	// per-profile toolset cache policy.
	RuntimeStateIsolationToolSetCache = "openclaw.profile.isolation." +
		"toolset_cache"
	// RuntimeStateIsolationServiceMode is the runtime-state key for
	// per-profile service/process policy.
	RuntimeStateIsolationServiceMode = "openclaw.profile.isolation.service"
)

// ErrProfileNotFound means a resolver could not find the selected profile.
var ErrProfileNotFound = errors.New("runtime profile not found")

// ErrConfigInvalid means a runtime profile config is internally inconsistent.
var ErrConfigInvalid = errors.New("runtime profile config invalid")

// ErrWorkspaceDenied means a path is outside the profile workspace policy.
var ErrWorkspaceDenied = errors.New("runtime profile workspace denied")

// ErrCredentialDenied means a credential ref is outside the profile policy.
var ErrCredentialDenied = errors.New("runtime profile credential denied")

type contextKey struct{}

// Config describes static runtime profiles.
type Config struct {
	Default           string             `yaml:"default,omitempty"`
	Required          bool               `yaml:"required,omitempty"`
	FallbackToDefault bool               `yaml:"fallback_to_default,omitempty"`
	Profiles          map[string]Profile `yaml:"profiles,omitempty"`
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
	Include          []string          `yaml:"include,omitempty"`
	Exclude          []string          `yaml:"exclude,omitempty"`
	ExecutionInclude []string          `yaml:"execution_include,omitempty"`
	ExecutionExclude []string          `yaml:"execution_exclude,omitempty"`
	ToolSets         []string          `yaml:"toolsets,omitempty"`
	CredentialRefs   map[string]string `yaml:"credential_refs,omitempty"`
}

// KnowledgePolicy defines per-run knowledge query policy.
type KnowledgePolicy struct {
	Indexes []string       `yaml:"indexes,omitempty"`
	Filter  map[string]any `yaml:"filter,omitempty"`
}

// WorkspacePolicy defines profile-scoped filesystem boundaries.
type WorkspacePolicy struct {
	Workdir      string   `yaml:"workdir,omitempty"`
	AllowedRoots []string `yaml:"allowed_roots,omitempty"`
}

// CredentialPolicy defines profile-scoped credential references.
type CredentialPolicy struct {
	AllowedRefs []string `yaml:"allowed_refs,omitempty"`
}

// SkillPolicy defines profile-scoped skill visibility and repositories.
type SkillPolicy struct {
	Include []string `yaml:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
	Roots   []string `yaml:"roots,omitempty"`
}

// IsolationMode describes how strongly a profile should be isolated.
type IsolationMode string

const (
	// IsolationModeShared uses the process-level agent and toolsets.
	IsolationModeShared IsolationMode = "shared"
	// IsolationModeProfileCache allows profile-keyed agent/toolset caches.
	IsolationModeProfileCache IsolationMode = "profile_cache"
	// IsolationModeService reserves a separate service/process boundary.
	IsolationModeService IsolationMode = "service"
)

// IsolationPolicy defines optional hard-isolation contracts.
type IsolationPolicy struct {
	Mode         IsolationMode `yaml:"mode,omitempty"`
	AgentCache   bool          `yaml:"agent_cache,omitempty"`
	ToolSetCache bool          `yaml:"toolset_cache,omitempty"`
	ServiceMode  string        `yaml:"service_mode,omitempty"`
}

// Profile is a resolved per-request runtime profile.
type Profile struct {
	ID          string           `yaml:"id,omitempty"`
	Version     string           `yaml:"version,omitempty"`
	AppName     string           `yaml:"app_name,omitempty"`
	AgentName   string           `yaml:"agent_name,omitempty"`
	ModelName   string           `yaml:"model_name,omitempty"`
	Prompt      Prompt           `yaml:"prompt,omitempty"`
	Tools       ToolPolicy       `yaml:"tools,omitempty"`
	Knowledge   KnowledgePolicy  `yaml:"knowledge,omitempty"`
	Workspace   WorkspacePolicy  `yaml:"workspace,omitempty"`
	Credentials CredentialPolicy `yaml:"credentials,omitempty"`
	Skills      SkillPolicy      `yaml:"skills,omitempty"`
	Isolation   IsolationPolicy  `yaml:"isolation,omitempty"`
	State       map[string]any   `yaml:"runtime_state,omitempty"`
	ExtraModel  map[string]any   `yaml:"model_request_extra,omitempty"`
}

// ValidateConfig validates profile keys, aliases, defaults, and isolation
// modes without assuming any OpenClaw application-specific agent names.
func ValidateConfig(cfg Config) error {
	if len(cfg.Profiles) == 0 {
		if cfg.Required {
			return fmt.Errorf("%w: required profiles are empty", ErrConfigInvalid)
		}
		if strings.TrimSpace(cfg.Default) != "" {
			return fmt.Errorf(
				"%w: default profile is not configured",
				ErrConfigInvalid,
			)
		}
		return nil
	}

	effectiveIDs := make(map[string]string, len(cfg.Profiles))
	for key, profile := range cfg.Profiles {
		lookupID := strings.TrimSpace(key)
		if lookupID == "" {
			return fmt.Errorf("%w: empty profile key", ErrConfigInvalid)
		}
		effectiveID := strings.TrimSpace(profile.ID)
		if effectiveID == "" {
			effectiveID = lookupID
		}
		if previous, ok := effectiveIDs[effectiveID]; ok {
			return fmt.Errorf(
				"%w: duplicate profile id %q for %q and %q",
				ErrConfigInvalid,
				effectiveID,
				previous,
				lookupID,
			)
		}
		effectiveIDs[effectiveID] = lookupID
		if err := validateIsolationPolicy(profile.Isolation); err != nil {
			return fmt.Errorf(
				"%w: profiles.%s.%w",
				ErrConfigInvalid,
				lookupID,
				err,
			)
		}
	}

	defaultID := strings.TrimSpace(cfg.Default)
	if defaultID == "" {
		if cfg.FallbackToDefault {
			return fmt.Errorf("%w: fallback needs default", ErrConfigInvalid)
		}
		return nil
	}
	if _, ok := cfg.Profiles[defaultID]; ok {
		return nil
	}
	if _, ok := effectiveIDs[defaultID]; ok {
		return nil
	}
	return fmt.Errorf(
		"%w: default profile %q is not configured",
		ErrConfigInvalid,
		defaultID,
	)
}

func validateIsolationPolicy(policy IsolationPolicy) error {
	switch policy.Mode {
	case "", IsolationModeShared, IsolationModeProfileCache,
		IsolationModeService:
	default:
		return fmt.Errorf("unsupported isolation mode %q", policy.Mode)
	}
	if policy.Mode == "" &&
		(policy.AgentCache ||
			policy.ToolSetCache ||
			strings.TrimSpace(policy.ServiceMode) != "") {
		return fmt.Errorf("isolation settings need mode")
	}
	if policy.Mode == IsolationModeShared &&
		(policy.AgentCache ||
			policy.ToolSetCache ||
			strings.TrimSpace(policy.ServiceMode) != "") {
		return fmt.Errorf("shared isolation cannot set cache or service")
	}
	if policy.Mode == IsolationModeProfileCache &&
		strings.TrimSpace(policy.ServiceMode) != "" {
		return fmt.Errorf("profile_cache isolation cannot set service mode")
	}
	return nil
}

// HasProfile returns true when profile carries any runtime contract.
func HasProfile(profile Profile) bool {
	return strings.TrimSpace(profile.ID) != "" ||
		strings.TrimSpace(profile.Version) != "" ||
		strings.TrimSpace(profile.AppName) != "" ||
		strings.TrimSpace(profile.AgentName) != "" ||
		strings.TrimSpace(profile.ModelName) != "" ||
		hasPrompt(profile.Prompt) ||
		hasToolPolicy(profile.Tools) ||
		hasKnowledgePolicy(profile.Knowledge) ||
		hasWorkspacePolicy(profile.Workspace) ||
		hasCredentialPolicy(profile.Credentials) ||
		hasSkillPolicy(profile.Skills) ||
		hasIsolationPolicy(profile.Isolation) ||
		len(profile.State) > 0 ||
		len(profile.ExtraModel) > 0
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

// TraceFields returns non-secret profile metadata suitable for logs/traces.
func TraceFields(profile Profile) map[string]any {
	fields := make(map[string]any)
	addTraceString(fields, "profile_id", profile.ID)
	addTraceString(fields, "profile_version", profile.Version)
	addTraceString(fields, "profile_app_name", profile.AppName)
	addTraceStrings(fields, "skill_include", profile.Skills.Include)
	addTraceStrings(fields, "skill_exclude", profile.Skills.Exclude)
	addTraceStrings(fields, "knowledge_indexes", profile.Knowledge.Indexes)
	addTraceStrings(fields, "toolsets", profile.Tools.ToolSets)
	addTraceString(fields, "isolation_mode", string(profile.Isolation.Mode))
	addTraceString(fields, "service_mode", profile.Isolation.ServiceMode)
	if profile.Isolation.AgentCache {
		fields["agent_cache"] = true
	}
	if profile.Isolation.ToolSetCache {
		fields["toolset_cache"] = true
	}
	if count := len(cleanStrings(profile.Credentials.AllowedRefs)); count > 0 {
		fields["credential_ref_count"] = count
	}
	if count := len(profile.Tools.CredentialRefs); count > 0 {
		fields["tool_credential_ref_count"] = count
	}
	if count := len(cleanStrings(profile.Workspace.AllowedRoots)); count > 0 {
		fields["workspace_allowed_root_count"] = count
	}
	if strings.TrimSpace(profile.Workspace.Workdir) != "" {
		fields["has_workspace_workdir"] = true
	}
	if count := len(cleanStrings(profile.Skills.Roots)); count > 0 {
		fields["skill_root_count"] = count
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// MapResolver resolves profiles from an in-memory map.
type MapResolver struct {
	defaultID         string
	fallbackToDefault bool
	profiles          map[string]profileCacheKey
	cache             map[profileCacheKey]Profile
}

type profileCacheKey struct {
	id      string
	version string
}

// NewMapResolver creates a resolver backed by static profiles.
func NewMapResolver(cfg Config) *MapResolver {
	if len(cfg.Profiles) == 0 {
		return nil
	}
	profiles := make(map[string]profileCacheKey, len(cfg.Profiles))
	cache := make(map[profileCacheKey]Profile, len(cfg.Profiles))
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
		cacheKey := profileCacheKey{
			id:      id,
			version: strings.TrimSpace(profile.Version),
		}
		profiles[lookupID] = cacheKey
		cache[cacheKey] = cloneProfile(profile)
		if id != lookupID {
			profiles[id] = cacheKey
		}
	}
	if len(profiles) == 0 {
		return nil
	}
	return &MapResolver{
		defaultID:         strings.TrimSpace(cfg.Default),
		fallbackToDefault: cfg.FallbackToDefault,
		profiles:          profiles,
		cache:             cache,
	}
}

// Resolve implements Resolver.
func (r *MapResolver) Resolve(
	ctx context.Context,
	req Request,
) (Profile, error) {
	_ = ctx
	if r == nil || len(r.cache) == 0 {
		return Profile{}, nil
	}
	id := strings.TrimSpace(req.ProfileID)
	if id == "" {
		id = r.defaultID
	}
	if id == "" {
		return Profile{}, nil
	}
	cacheKey, ok := r.profiles[id]
	if !ok && r.fallbackToDefault && id != r.defaultID {
		cacheKey, ok = r.profiles[r.defaultID]
	}
	if !ok {
		return Profile{}, ErrProfileNotFound
	}
	profile, ok := r.cache[cacheKey]
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
	state = addRuntimeStateString(
		state,
		RuntimeStateWorkspaceWorkdir,
		profile.Workspace.Workdir,
	)
	state = addRuntimeStateStrings(
		state,
		RuntimeStateWorkspaceAllowedRoots,
		profile.Workspace.AllowedRoots,
	)
	state = addRuntimeStateStrings(
		state,
		RuntimeStateCredentialAllowedRefs,
		profile.Credentials.AllowedRefs,
	)
	state = addRuntimeStateStrings(
		state,
		RuntimeStateSkillInclude,
		profile.Skills.Include,
	)
	state = addRuntimeStateStrings(
		state,
		RuntimeStateSkillExclude,
		profile.Skills.Exclude,
	)
	state = addRuntimeStateStrings(
		state,
		RuntimeStateSkillRoots,
		profile.Skills.Roots,
	)
	state = addRuntimeStateStrings(
		state,
		RuntimeStateKnowledgeIndexes,
		profile.Knowledge.Indexes,
	)
	state = addRuntimeStateStrings(
		state,
		RuntimeStateToolSets,
		profile.Tools.ToolSets,
	)
	if refs := copyStringMap(profile.Tools.CredentialRefs); len(refs) > 0 {
		if state == nil {
			state = make(map[string]any)
		}
		state[RuntimeStateToolCredentialRefs] = refs
	}
	if mode := strings.TrimSpace(string(profile.Isolation.Mode)); mode != "" {
		if state == nil {
			state = make(map[string]any)
		}
		state[RuntimeStateIsolationMode] = mode
	}
	if profile.Isolation.AgentCache {
		if state == nil {
			state = make(map[string]any)
		}
		state[RuntimeStateIsolationAgentCache] = true
	}
	if profile.Isolation.ToolSetCache {
		if state == nil {
			state = make(map[string]any)
		}
		state[RuntimeStateIsolationToolSetCache] = true
	}
	state = addRuntimeStateString(
		state,
		RuntimeStateIsolationServiceMode,
		profile.Isolation.ServiceMode,
	)
	return state
}

func addRuntimeStateString(
	state map[string]any,
	key string,
	value string,
) map[string]any {
	value = strings.TrimSpace(value)
	if value == "" {
		return state
	}
	if state == nil {
		state = make(map[string]any)
	}
	state[key] = value
	return state
}

func addRuntimeStateStrings(
	state map[string]any,
	key string,
	values []string,
) map[string]any {
	values = cleanStrings(values)
	if len(values) == 0 {
		return state
	}
	if state == nil {
		state = make(map[string]any)
	}
	state[key] = copyStrings(values)
	return state
}

func addTraceString(fields map[string]any, key string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	fields[key] = value
}

func addTraceStrings(fields map[string]any, key string, values []string) {
	values = cleanStrings(values)
	if len(values) == 0 {
		return
	}
	fields[key] = copyStrings(values)
}

func hasPrompt(prompt Prompt) bool {
	return strings.TrimSpace(prompt.Instruction) != "" ||
		strings.TrimSpace(prompt.SystemPrompt) != ""
}

func hasToolPolicy(policy ToolPolicy) bool {
	return len(cleanStrings(policy.Include)) > 0 ||
		len(cleanStrings(policy.Exclude)) > 0 ||
		len(cleanStrings(policy.ExecutionInclude)) > 0 ||
		len(cleanStrings(policy.ExecutionExclude)) > 0 ||
		len(cleanStrings(policy.ToolSets)) > 0 ||
		len(policy.CredentialRefs) > 0
}

func hasKnowledgePolicy(policy KnowledgePolicy) bool {
	return len(cleanStrings(policy.Indexes)) > 0 || len(policy.Filter) > 0
}

func hasWorkspacePolicy(policy WorkspacePolicy) bool {
	return strings.TrimSpace(policy.Workdir) != "" ||
		len(cleanStrings(policy.AllowedRoots)) > 0
}

func hasCredentialPolicy(policy CredentialPolicy) bool {
	return len(cleanStrings(policy.AllowedRefs)) > 0
}

func hasSkillPolicy(policy SkillPolicy) bool {
	return len(cleanStrings(policy.Include)) > 0 ||
		len(cleanStrings(policy.Exclude)) > 0 ||
		len(cleanStrings(policy.Roots)) > 0
}

func hasIsolationPolicy(policy IsolationPolicy) bool {
	return strings.TrimSpace(string(policy.Mode)) != "" ||
		policy.AgentCache ||
		policy.ToolSetCache ||
		strings.TrimSpace(policy.ServiceMode) != ""
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
