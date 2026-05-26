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
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	runtimeStateKeyPrefix = "openclaw.profile."

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

// ErrProfileSelectorDenied means selector policy rejected profile resolution.
var ErrProfileSelectorDenied = errors.New("runtime profile selector denied")

// ErrConfigInvalid means a runtime profile config is internally inconsistent.
var ErrConfigInvalid = errors.New("runtime profile config invalid")

// ErrWorkspaceDenied means a path is outside the profile workspace policy.
var ErrWorkspaceDenied = errors.New("runtime profile workspace denied")

// ErrCredentialDenied means a credential ref is outside the profile policy.
var ErrCredentialDenied = errors.New("runtime profile credential denied")

type contextKey struct{}

type requestContextKey struct{}

// Config describes static runtime profiles.
type Config struct {
	Default           string             `yaml:"default,omitempty"`
	Required          bool               `yaml:"required,omitempty"`
	FallbackToDefault bool               `yaml:"fallback_to_default,omitempty"`
	Profiles          map[string]Profile `yaml:"profiles,omitempty"`
	Selectors         []Selector         `yaml:"selectors,omitempty"`
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
		if len(cfg.Selectors) > 0 {
			return fmt.Errorf(
				"%w: selectors need configured profiles",
				ErrConfigInvalid,
			)
		}
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

	knownIDs, err := profileIDAliases(cfg.Profiles)
	if err != nil {
		return err
	}
	for key, profile := range cfg.Profiles {
		lookupID := strings.TrimSpace(key)
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
		return validateSelectors(cfg.Selectors, knownIDs)
	}
	if _, ok := knownIDs[defaultID]; ok {
		return validateSelectors(cfg.Selectors, knownIDs)
	}
	return fmt.Errorf(
		"%w: default profile %q is not configured",
		ErrConfigInvalid,
		defaultID,
	)
}

func profileIDAliases(
	profiles map[string]Profile,
) (map[string]string, error) {
	aliases := make(map[string]string, len(profiles)*2)
	effectiveIDs := make(map[string]string, len(profiles))
	for key, profile := range profiles {
		lookupID := strings.TrimSpace(key)
		if lookupID == "" {
			return nil, fmt.Errorf("%w: empty profile key", ErrConfigInvalid)
		}
		effectiveID := strings.TrimSpace(profile.ID)
		if effectiveID == "" {
			effectiveID = lookupID
		}
		if previous, ok := effectiveIDs[effectiveID]; ok {
			return nil, fmt.Errorf(
				"%w: duplicate profile id %q for %q and %q",
				ErrConfigInvalid,
				effectiveID,
				previous,
				lookupID,
			)
		}
		effectiveIDs[effectiveID] = lookupID
		if err := addProfileIDAlias(
			aliases,
			lookupID,
			effectiveID,
		); err != nil {
			return nil, err
		}
		if err := addProfileIDAlias(
			aliases,
			effectiveID,
			effectiveID,
		); err != nil {
			return nil, err
		}
	}
	return aliases, nil
}

func addProfileIDAlias(
	aliases map[string]string,
	alias string,
	effectiveID string,
) error {
	if previous, ok := aliases[alias]; ok && previous != effectiveID {
		return fmt.Errorf(
			"%w: profile alias %q points to both %q and %q",
			ErrConfigInvalid,
			alias,
			previous,
			effectiveID,
		)
	}
	aliases[alias] = effectiveID
	return nil
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

// WithRequest stores the profile selection request on the context.
func WithRequest(ctx context.Context, req Request) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, requestContextKey{}, cloneRequest(req))
}

// RequestFromContext returns the profile selection request stored on ctx.
func RequestFromContext(ctx context.Context) (Request, bool) {
	if ctx == nil {
		return Request{}, false
	}
	req, ok := ctx.Value(requestContextKey{}).(Request)
	if !ok {
		return Request{}, false
	}
	return cloneRequest(req), true
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

func cloneRequest(req Request) Request {
	req.Channel = strings.TrimSpace(req.Channel)
	req.ProfileID = strings.TrimSpace(req.ProfileID)
	req.TenantID = strings.TrimSpace(req.TenantID)
	req.UserID = strings.TrimSpace(req.UserID)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.RequestID = strings.TrimSpace(req.RequestID)
	if len(req.Extensions) == 0 {
		req.Extensions = nil
		return req
	}
	extensions := make(map[string]json.RawMessage, len(req.Extensions))
	for key, raw := range req.Extensions {
		extensions[strings.TrimSpace(key)] = append(
			json.RawMessage(nil),
			raw...,
		)
	}
	req.Extensions = extensions
	return req
}

// AppNameFromContext returns the profile app name or the provided fallback.
func AppNameFromContext(ctx context.Context, fallback string) string {
	if profile, ok := ProfileFromContext(ctx); ok {
		if appName := RuntimeAppName(profile); appName != "" {
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
	addTraceString(fields, "profile_app_name", RuntimeAppName(profile))
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

// ProfileIDs implements Catalog.
func (r *MapResolver) ProfileIDs(context.Context) ([]string, error) {
	if r == nil || len(r.cache) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(r.cache))
	for key := range r.cache {
		if key.id == "" {
			continue
		}
		out = append(out, key.id)
	}
	sort.Strings(out)
	return out, nil
}

// AppNames implements Catalog.
func (r *MapResolver) AppNames(context.Context) ([]string, error) {
	if r == nil || len(r.cache) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(r.cache))
	seen := make(map[string]struct{}, len(r.cache))
	for _, profile := range r.cache {
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
	return out, nil
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
	if appName := RuntimeAppName(profile); appName != "" {
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
	if filter := toolVisibilityFilter(
		profile.Tools,
		profile.Knowledge,
		profile.Credentials,
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

// RuntimeAppName returns the app name OpenClaw should use for a profile run.
func RuntimeAppName(profile Profile) string {
	appName := strings.TrimSpace(profile.AppName)
	if appName != "" {
		return appName
	}
	if !requiresProfileRuntimeIsolation(profile.Isolation) {
		return ""
	}
	return strings.TrimSpace(profile.ID)
}

func requiresProfileRuntimeIsolation(policy IsolationPolicy) bool {
	mode := strings.TrimSpace(string(policy.Mode))
	return mode != "" && mode != string(IsolationModeShared)
}

func runtimeState(profile Profile) map[string]any {
	state := userRuntimeState(profile.State)
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
	if refs := allowedCredentialRefs(
		profile.Tools.CredentialRefs,
		profile.Credentials.AllowedRefs,
	); len(refs) > 0 {
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

func userRuntimeState(values map[string]any) map[string]any {
	state := copyAnyMap(values)
	for key := range state {
		if strings.HasPrefix(key, runtimeStateKeyPrefix) {
			delete(state, key)
		}
	}
	if len(state) == 0 {
		return nil
	}
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

func toolVisibilityFilter(
	toolPolicy ToolPolicy,
	knowledgePolicy KnowledgePolicy,
	credentialPolicy CredentialPolicy,
) tool.FilterFunc {
	tools := toolPolicyFilter(toolPolicy)
	knowledge := knowledgeIndexFilter(knowledgePolicy.Indexes)
	credentials := toolCredentialFilter(
		toolPolicy.CredentialRefs,
		credentialPolicy.AllowedRefs,
	)
	return allToolFilters(tools, knowledge, credentials)
}

func toolPolicyFilter(policy ToolPolicy) tool.FilterFunc {
	names := toolNamesFilter(policy.Include, policy.Exclude)
	toolSets := toolSetNamesFilter(policy.ToolSets)
	switch {
	case names == nil:
		return toolSets
	case toolSets == nil:
		return names
	default:
		return func(ctx context.Context, tl tool.Tool) bool {
			return toolSets(ctx, tl) && names(ctx, tl)
		}
	}
}

func knowledgeIndexFilter(indexes []string) tool.FilterFunc {
	indexes = cleanStrings(indexes)
	if len(indexes) == 0 {
		return nil
	}
	allowed := nameSet(indexes)
	return func(_ context.Context, tl tool.Tool) bool {
		index := sourceKnowledgeIndex(tl)
		if index == "" {
			return true
		}
		_, ok := allowed[index]
		return ok
	}
}

func toolCredentialFilter(
	refs map[string]string,
	allowedRefs []string,
) tool.FilterFunc {
	refs = cleanStringMap(refs)
	if len(refs) == 0 {
		return nil
	}
	allowed := nameSet(cleanStrings(allowedRefs))
	return func(_ context.Context, tl tool.Tool) bool {
		ref := credentialRefForTool(tl, refs)
		if ref == "" || len(allowed) == 0 {
			return true
		}
		_, ok := allowed[ref]
		return ok
	}
}

func credentialRefForTool(
	tl tool.Tool,
	refs map[string]string,
) string {
	if len(refs) == 0 || tl == nil || tl.Declaration() == nil {
		return ""
	}
	if ref := strings.TrimSpace(refs[tl.Declaration().Name]); ref != "" {
		return ref
	}
	if toolSetName := sourceToolSetName(tl); toolSetName != "" {
		if ref := strings.TrimSpace(refs[toolSetName]); ref != "" {
			return ref
		}
	}
	return ""
}

type knowledgeIndexNamer interface {
	KnowledgeIndexName() string
}

func sourceKnowledgeIndex(tl tool.Tool) string {
	named, ok := tl.(knowledgeIndexNamer)
	if !ok || named == nil {
		return ""
	}
	return strings.TrimSpace(named.KnowledgeIndexName())
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

func toolSetNamesFilter(names []string) tool.FilterFunc {
	names = cleanStrings(names)
	if len(names) == 0 {
		return nil
	}
	allowed := nameSet(names)
	return func(_ context.Context, tl tool.Tool) bool {
		name := sourceToolSetName(tl)
		if name == "" {
			return false
		}
		_, ok := allowed[name]
		return ok
	}
}

type toolSetNamer interface {
	ToolSetName() string
}

func sourceToolSetName(tl tool.Tool) string {
	named, ok := tl.(toolSetNamer)
	if !ok || named == nil {
		return ""
	}
	return strings.TrimSpace(named.ToolSetName())
}

func allToolFilters(filters ...tool.FilterFunc) tool.FilterFunc {
	out := make([]tool.FilterFunc, 0, len(filters))
	for _, filter := range filters {
		if filter != nil {
			out = append(out, filter)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return func(ctx context.Context, tl tool.Tool) bool {
		for _, filter := range out {
			if !filter(ctx, tl) {
				return false
			}
		}
		return true
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

func cleanStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func allowedCredentialRefs(
	refs map[string]string,
	allowedRefs []string,
) map[string]string {
	refs = cleanStringMap(refs)
	if len(refs) == 0 {
		return nil
	}
	allowed := nameSet(cleanStrings(allowedRefs))
	if len(allowed) == 0 {
		return refs
	}
	out := make(map[string]string, len(refs))
	for key, ref := range refs {
		if _, ok := allowed[ref]; ok {
			out[key] = ref
		}
	}
	return out
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
