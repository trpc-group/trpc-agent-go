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
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/internal/skillprofile"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/admin"
)

const (
	adminRuntimeConfigInputText   = "text"
	adminRuntimeConfigInputNumber = "number"
	adminRuntimeConfigInputSelect = "select"

	adminRuntimeConfigApplyRestart = "restart"

	adminRuntimeConfigSourceExplicit  = "explicit"
	adminRuntimeConfigSourceInherited = "inherited"

	adminRuntimeConfigConfiguredExplicit  = "Explicit in config"
	adminRuntimeConfigConfiguredInherited = "Inherited from current runtime"
	adminRuntimeConfigRuntimeSourceLabel  = "Current runtime"

	adminRuntimeConfigValueString = "string"
	adminRuntimeConfigValueInt    = "int"
	adminRuntimeConfigValueBool   = "bool"
)

var runtimeAdminOptionsStore sync.Map

// AdminSourceConfigPathEnvName overrides the writable admin config file path.
const AdminSourceConfigPathEnvName = "TRPC_CLAW_ADMIN_SOURCE_CONFIG_PATH"

type adminRuntimeConfigProvider struct {
	configPath string
	opts       runOptions
}

type adminRuntimeConfigKeyRef struct {
	Preferred string
	Aliases   []string
}

type adminRuntimeConfigSectionSpec struct {
	Key     string
	Title   string
	Summary string
	Fields  []adminRuntimeConfigFieldSpec
}

type adminRuntimeConfigFieldSpec struct {
	Key         string
	Title       string
	Summary     string
	InputType   string
	Placeholder string
	ApplyMode   string
	VisibleWhen admin.RuntimeConfigVisibleWhen
	ValueType   string
	Path        []adminRuntimeConfigKeyRef
	Options     []admin.RuntimeConfigOption
	Runtime     func(runOptions) string
}

type adminRuntimeConfiguredValue struct {
	Value    string
	Explicit bool
}

func buildAdminRuntimeConfigProvider(
	opts runOptions,
) admin.RuntimeConfigProvider {
	path := adminWritableConfigPath(opts.ConfigPath)
	if path == "" {
		return nil
	}
	return &adminRuntimeConfigProvider{
		configPath: path,
		opts:       opts,
	}
}

func adminWritableConfigPath(configPath string) string {
	override := strings.TrimSpace(
		os.Getenv(AdminSourceConfigPathEnvName),
	)
	if override != "" {
		return override
	}
	return strings.TrimSpace(configPath)
}

func buildAdminOptions(opts runOptions) []admin.Option {
	provider := buildAdminRuntimeConfigProvider(opts)
	if provider == nil {
		return nil
	}
	return []admin.Option{
		admin.WithRuntimeConfigProvider(provider),
	}
}

func runtimeAdminOptions(rt *Runtime) []admin.Option {
	if rt == nil {
		return nil
	}
	raw, ok := runtimeAdminOptionsStore.Load(rt)
	if !ok {
		return nil
	}
	options, ok := raw.([]admin.Option)
	if !ok || len(options) == 0 {
		return nil
	}
	return append([]admin.Option(nil), options...)
}

func setRuntimeAdminOptions(rt *Runtime, opts []admin.Option) {
	if rt == nil {
		return
	}
	if len(opts) == 0 {
		runtimeAdminOptionsStore.Delete(rt)
		return
	}
	runtimeAdminOptionsStore.Store(rt, append([]admin.Option(nil), opts...))
}

func clearRuntimeAdminOptions(rt *Runtime) {
	if rt == nil {
		return
	}
	runtimeAdminOptionsStore.Delete(rt)
}

func (p *adminRuntimeConfigProvider) RuntimeConfigStatus() (
	admin.RuntimeConfigStatus,
	error,
) {
	if p == nil || strings.TrimSpace(p.configPath) == "" {
		return admin.RuntimeConfigStatus{}, nil
	}

	root, err := adminRuntimeConfigRootFromPath(p.configPath)
	if err != nil {
		return admin.RuntimeConfigStatus{}, err
	}

	status := admin.RuntimeConfigStatus{
		Enabled:    true,
		ConfigPath: strings.TrimSpace(p.configPath),
		Sections: make(
			[]admin.RuntimeConfigSection,
			0,
			len(adminRuntimeConfigSectionSpecs()),
		),
	}
	visibleValues := map[string]string{}
	for _, section := range adminRuntimeConfigSectionSpecs() {
		view := admin.RuntimeConfigSection{
			Key:     section.Key,
			Title:   section.Title,
			Summary: section.Summary,
			Fields: make(
				[]admin.RuntimeConfigField,
				0,
				len(section.Fields),
			),
		}
		for _, field := range section.Fields {
			configured := adminRuntimeConfiguredFieldValue(
				root,
				field.Path,
			)
			runtimeValue := strings.TrimSpace(field.Runtime(p.opts))
			nextValue := runtimeValue
			editorValue := runtimeValue
			if configured.Explicit {
				editorValue = adminRuntimeConfiguredEditorValue(
					field,
					configured.Value,
				)
				nextValue = adminRuntimeComparableConfiguredValue(
					field,
					configured.Value,
				)
			}
			hidden := adminRuntimeConfigFieldHidden(field, visibleValues)
			view.Fields = append(view.Fields, admin.RuntimeConfigField{
				Key:                   field.Key,
				Title:                 field.Title,
				Summary:               field.Summary,
				InputType:             field.InputType,
				Placeholder:           field.Placeholder,
				ApplyMode:             field.ApplyMode,
				VisibleWhen:           field.VisibleWhen,
				Hidden:                hidden,
				EditorValue:           editorValue,
				ConfiguredValue:       configured.Value,
				ConfiguredSource:      adminRuntimeConfiguredSource(configured.Explicit),
				ConfiguredSourceLabel: adminRuntimeConfiguredLabel(configured.Explicit),
				RuntimeValue:          runtimeValue,
				RuntimeSourceLabel:    adminRuntimeConfigRuntimeSourceLabel,
				PendingRestart:        strings.TrimSpace(nextValue) != runtimeValue,
				Resettable:            configured.Explicit,
				Options: append(
					[]admin.RuntimeConfigOption(nil),
					field.Options...,
				),
			})
			visibleValues[field.Key] = editorValue
		}
		status.Sections = append(status.Sections, view)
	}
	return status, nil
}

func (p *adminRuntimeConfigProvider) SaveRuntimeConfigValue(
	key string,
	value string,
) error {
	if p == nil || strings.TrimSpace(p.configPath) == "" {
		return fmt.Errorf("runtime config is not available")
	}
	spec, ok := adminRuntimeConfigFieldSpecByKey(key)
	if !ok {
		return fmt.Errorf("unknown runtime config field")
	}

	doc, root, err := adminRuntimeConfigDocumentFromPath(p.configPath)
	if err != nil {
		return err
	}
	if spec.Key == "tools.code_executor.type" && strings.TrimSpace(value) == "" {
		adminRuntimeDeleteField(root, spec.Path)
	} else {
		parent, err := adminRuntimeEnsureFieldParent(root, spec.Path)
		if err != nil {
			return err
		}
		if err := adminRuntimeSetFieldValue(parent, spec, value); err != nil {
			return err
		}
	}
	if err := p.normalizeCodeExecutorConfig(root, spec.Key); err != nil {
		return err
	}
	return writeConfigDocument(p.configPath, &doc)
}

func (p *adminRuntimeConfigProvider) ResetRuntimeConfigValue(
	key string,
) error {
	if p == nil || strings.TrimSpace(p.configPath) == "" {
		return fmt.Errorf("runtime config is not available")
	}
	spec, ok := adminRuntimeConfigFieldSpecByKey(key)
	if !ok {
		return fmt.Errorf("unknown runtime config field")
	}

	doc, root, err := adminRuntimeConfigDocumentFromPath(p.configPath)
	if err != nil {
		return err
	}
	adminRuntimeDeleteField(root, spec.Path)
	if err := p.normalizeCodeExecutorConfig(root, spec.Key); err != nil {
		return err
	}
	return writeConfigDocument(p.configPath, &doc)
}

func adminRuntimeConfigSectionSpecs() []adminRuntimeConfigSectionSpec {
	return []adminRuntimeConfigSectionSpec{
		{
			Key:     "admin",
			Title:   "Admin",
			Summary: "Admin server listen settings and safety toggles.",
			Fields: []adminRuntimeConfigFieldSpec{
				adminRuntimeBoolField(
					"admin.enabled",
					"Enabled",
					"Turn the admin surface on or off.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("admin"),
						adminRuntimeKey("enabled"),
					},
					func(opts runOptions) string {
						return strconv.FormatBool(opts.AdminEnabled)
					},
				),
				adminRuntimeTextField(
					"admin.addr",
					"Address",
					"Primary listen address for the admin server.",
					defaultAdminAddr,
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("admin"),
						adminRuntimeKey("addr"),
					},
					func(opts runOptions) string {
						return strings.TrimSpace(opts.AdminAddr)
					},
				),
				adminRuntimeBoolField(
					"admin.auto_port",
					"Auto Port",
					"Search nearby ports when the preferred one is busy.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("admin"),
						adminRuntimeKey("auto_port"),
					},
					func(opts runOptions) string {
						return strconv.FormatBool(opts.AdminAutoPort)
					},
				),
			},
		},
		{
			Key:     "model",
			Title:   "Model",
			Summary: "Core model routing and OpenAI compatibility mode.",
			Fields: []adminRuntimeConfigFieldSpec{
				adminRuntimeSelectField(
					"model.mode",
					"Mode",
					"Backend provider family for the runtime.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("model"),
						adminRuntimeKey("mode"),
					},
					func(opts runOptions) string {
						return strings.TrimSpace(opts.ModelMode)
					},
					"mock",
					"openai",
				),
				adminRuntimeTextField(
					"model.base_url",
					"Base URL",
					"Custom OpenAI-compatible endpoint override.",
					"",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("model"),
						adminRuntimeKey("base_url"),
					},
					func(opts runOptions) string {
						return strings.TrimSpace(opts.OpenAIBaseURL)
					},
				),
				adminRuntimeSelectField(
					"model.openai_variant",
					"OpenAI Variant",
					"Dialect hint for compatible providers.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("model"),
						adminRuntimeKey("openai_variant"),
					},
					func(opts runOptions) string {
						return strings.TrimSpace(opts.OpenAIVariant)
					},
					"auto",
					"openai",
					"deepseek",
					"qwen",
					"hunyuan",
				),
			},
		},
		{
			Key:     "agent",
			Title:   "Agent",
			Summary: "Agent runtime limits and safety budgets.",
			Fields: []adminRuntimeConfigFieldSpec{
				adminRuntimeNumberField(
					"agent.max_llm_calls",
					"Max LLM Calls",
					"Limit LLM calls per invocation; 0 is unlimited.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("agent"),
						adminRuntimeKey("max_llm_calls"),
					},
					func(opts runOptions) string {
						return strconv.Itoa(opts.MaxLLMCalls)
					},
				),
				adminRuntimeNumberField(
					"agent.max_tool_iterations",
					"Max Tool Iterations",
					"Limit tool-call iterations per invocation; 0 is unlimited.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("agent"),
						adminRuntimeKey("max_tool_iterations"),
					},
					func(opts runOptions) string {
						return strconv.Itoa(opts.MaxToolIterations)
					},
				),
			},
		},
		{
			Key:     "skills",
			Title:   "Skills",
			Summary: "Skill watch mode, load policy, and retention settings.",
			Fields: []adminRuntimeConfigFieldSpec{
				adminRuntimeBoolField(
					"skills.watch",
					"Watch",
					"Reload local skill folders when files change.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("skills"),
						adminRuntimeKey("watch"),
					},
					func(opts runOptions) string {
						return strconv.FormatBool(opts.SkillsWatch)
					},
				),
				adminRuntimeBoolField(
					"skills.watch_bundled",
					"Watch Bundled",
					"Watch bundled skill directories as well.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("skills"),
						adminRuntimeKey("watch_bundled"),
					},
					func(opts runOptions) string {
						return strconv.FormatBool(opts.SkillsWatchBundled)
					},
				),
				adminRuntimeNumberField(
					"skills.watch_debounce_ms",
					"Watch Debounce (ms)",
					"Delay before a burst of file changes reloads skills.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("skills"),
						adminRuntimeKey("watch_debounce_ms"),
					},
					func(opts runOptions) string {
						return strconv.Itoa(
							int(opts.SkillsWatchDebounce / time.Millisecond),
						)
					},
				),
				adminRuntimeSelectField(
					"skills.tool_profile",
					"Tool Profile",
					"Which built-in skill tools are exposed.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("skills"),
						adminRuntimeKey("tool_profile", "toolProfile"),
					},
					func(opts runOptions) string {
						return strings.TrimSpace(
							opts.SkillsToolProfile,
						)
					},
					skillprofile.KnowledgeOnly,
					skillprofile.Full,
				),
				adminRuntimeSelectField(
					"skills.load_mode",
					"Load Mode",
					"How long a loaded skill stays active.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("skills"),
						adminRuntimeKey("load_mode"),
					},
					func(opts runOptions) string {
						return strings.TrimSpace(opts.SkillsLoadMode)
					},
					"once",
					"turn",
					"session",
				),
				adminRuntimeNumberField(
					"skills.max_loaded_skills",
					"Max Loaded Skills",
					"Keep only the most recent loaded skills active.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("skills"),
						adminRuntimeKey("max_loaded_skills"),
					},
					func(opts runOptions) string {
						return strconv.Itoa(opts.SkillsMaxLoaded)
					},
				),
				adminRuntimeBoolField(
					"skills.loaded_content_in_tool_results",
					"Loaded Content In Tool Results",
					"Store loaded skill text in tool results instead of only in system context.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("skills"),
						adminRuntimeKey("loaded_content_in_tool_results"),
					},
					func(opts runOptions) string {
						return strconv.FormatBool(opts.SkillsToolResults)
					},
				),
				adminRuntimeBoolField(
					"skills.skip_fallback_on_session_summary",
					"Skip Fallback On Summary",
					"Do not re-inject loaded skill context when session summary is present.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("skills"),
						adminRuntimeKey("skip_fallback_on_session_summary"),
					},
					func(opts runOptions) string {
						return strconv.FormatBool(opts.SkillsSkipFallback)
					},
				),
			},
		},
		{
			Key:     "tools",
			Title:   "Tools",
			Summary: "High-level runtime tool toggles.",
			Fields: []adminRuntimeConfigFieldSpec{
				adminRuntimeBoolField(
					"tools.enable_local_exec",
					"Enable Local Exec",
					"Legacy compatibility toggle. When Code Executor Type is inherited/unset, this enables local execution for assistant code blocks. An explicit Code Executor Type takes precedence.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey("enable_local_exec"),
					},
					func(opts runOptions) string {
						return strconv.FormatBool(opts.EnableLocalExec)
					},
				),
				adminRuntimeBoolField(
					"tools.enable_openclaw_tools",
					"Enable OpenClaw Tools",
					"Expose OpenClaw runtime tools such as exec_command. When Executor Type is sandbox, exec_command uses the sandbox; other OpenClaw tools keep their normal runtime behavior.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey("enable_openclaw_tools"),
					},
					func(opts runOptions) string {
						return strconv.FormatBool(opts.EnableOpenClawTools)
					},
				),
				adminRuntimeTextField(
					"tools.openclaw_tooling_guidance",
					"OpenClaw Tooling Guidance",
					"Override or disable the built-in OpenClaw tooling guidance. Leave unset to use the built-in default, or set an empty string to disable injection.",
					"",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey(
							"openclaw_tooling_guidance",
							"openClawToolingGuidance",
						),
					},
					func(opts runOptions) string {
						if opts.OpenClawToolingGuide != nil {
							return *opts.OpenClawToolingGuide
						}
						return strings.TrimSpace(
							openClawToolingGuidance,
						)
					},
				),
				adminRuntimeBoolField(
					"tools.enable_parallel_tools",
					"Enable Parallel Tools",
					"Allow the runtime to issue compatible tool calls in parallel.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey("enable_parallel_tools"),
					},
					func(opts runOptions) string {
						return strconv.FormatBool(opts.EnableParallelTools)
					},
				),
				adminRuntimeBoolField(
					"tools.refresh_toolsets_on_run",
					"Refresh Toolsets On Run",
					"Refresh the toolset registry before each run.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey("refresh_toolsets_on_run"),
					},
					func(opts runOptions) string {
						return strconv.FormatBool(opts.RefreshToolSetsOnRun)
					},
				),
				adminRuntimeSelectField(
					"tools.defer_to_dynamic_agent_mode",
					"Deferred Tool Surface Mode",
					"Control whether broad tool surfaces are loaded "+
						"directly or through tool_search and "+
						"dynamic_agent.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey(
							"defer_to_dynamic_agent_mode",
							"deferToDynamicAgentMode",
						),
					},
					func(opts runOptions) string {
						mode, _ := normalizeDeferToolSurfaceMode(
							opts.DeferToolSurfaceMode,
						)
						if opts.DeferToolSurface {
							return deferToolSurfaceModeOn
						}
						return mode
					},
					deferToolSurfaceModeOff,
					deferToolSurfaceModeOn,
					deferToolSurfaceModeAuto,
				),
				adminRuntimeNumberField(
					"tools.defer_to_dynamic_agent_threshold_chars",
					"Deferred Tool Surface Threshold Chars",
					"Auto mode defers when direct tool declarations "+
						"exceed this approximate character count.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey(
							"defer_to_dynamic_agent_threshold_chars",
							"deferToDynamicAgentThresholdChars",
						),
					},
					func(opts runOptions) string {
						return strconv.Itoa(
							deferToolSurfaceThresholdChars(
								opts.DeferToolSurfaceChars,
							),
						)
					},
				),
				adminRuntimeTextField(
					"tools.dynamic_agent_timeout",
					"Dynamic Agent Timeout",
					"Maximum duration for one dynamic_agent child "+
						"call, for example 180s. Empty or 0 disables it.",
					"",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey(
							"dynamic_agent_timeout",
							"dynamicAgentTimeout",
						),
					},
					func(opts runOptions) string {
						if opts.DynamicAgentTimeout <= 0 {
							return ""
						}
						return opts.DynamicAgentTimeout.String()
					},
				),
				adminRuntimeTextField(
					"tools.defer_direct_tools",
					"Deferred Direct Tools",
					"Comma-separated additional tool names to keep "+
						"directly on the parent agent when deferred "+
						"mode is active.",
					"",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey(
							"defer_direct_tools",
							"deferDirectTools",
						),
					},
					func(opts runOptions) string {
						return strings.TrimSpace(
							opts.DeferToolSurfaceDirect,
						)
					},
				),
				adminRuntimeBoolField(
					"tools.defer_default_direct_tools",
					"Deferred Default Direct Tools",
					"Keep default direct tools on the parent agent "+
						"when deferred mode is active.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey(
							"defer_default_direct_tools",
							"deferDefaultDirectTools",
						),
					},
					func(opts runOptions) string {
						return strconv.FormatBool(
							opts.
								DeferToolSurfaceDefaultDirectTools,
						)
					},
				),
			},
		},
		{
			Key:     "code_executor",
			Title:   "Code Executor",
			Summary: "Code block execution mode and sandbox runtime settings. These settings take precedence over the legacy Enable Local Exec fallback.",
			Fields: []adminRuntimeConfigFieldSpec{
				adminRuntimeCodeExecutorTypeField(
					"tools.code_executor.type",
					"Executor Type",
					"Leave empty to inherit the legacy local-exec behavior, or select sandbox to run assistant code blocks and OpenClaw exec_command in the sandbox.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey("code_executor"),
						adminRuntimeKey("type"),
					},
					func(opts runOptions) string {
						return adminRuntimeCodeExecutorType(opts)
					},
				),
				adminRuntimeBoolField(
					"tools.code_executor.auto_execute_code_blocks",
					"Auto Execute Code Blocks",
					"Automatically execute assistant code blocks when the runtime supports code execution.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey("code_executor"),
						adminRuntimeKey("auto_execute_code_blocks"),
					},
					func(opts runOptions) string {
						return adminRuntimeOptionalBoolValueOrDefault(
							opts.CodeExecutor.AutoExecuteCodeBlocks,
							true,
						)
					},
				),
				adminRuntimeSandboxField(adminRuntimeTextField(
					"tools.code_executor.sandbox.workspace_root",
					"Sandbox Workspace Root",
					"Workspace root for sandbox sessions. Only used when Executor Type is sandbox; reset to use state_dir/sandbox.",
					"",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey("code_executor"),
						adminRuntimeKey("sandbox"),
						adminRuntimeKey("workspace_root"),
					},
					func(opts runOptions) string {
						if !adminRuntimeSandboxCodeExecutorEnabled(opts) {
							return ""
						}
						return strings.TrimSpace(
							opts.CodeExecutor.Sandbox.WorkspaceRoot,
						)
					},
				)),
				adminRuntimeSandboxField(adminRuntimeSelectField(
					"tools.code_executor.sandbox.profile",
					"Sandbox Profile",
					"Filesystem permission profile. Only used when Executor Type is sandbox.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey("code_executor"),
						adminRuntimeKey("sandbox"),
						adminRuntimeKey("profile"),
					},
					func(opts runOptions) string {
						if !adminRuntimeSandboxCodeExecutorEnabled(opts) {
							return ""
						}
						return strings.TrimSpace(
							opts.CodeExecutor.Sandbox.Profile,
						)
					},
					sandboxProfileWorkspaceWrite,
					sandboxProfileReadOnly,
					sandboxProfileDisabled,
				)),
				adminRuntimeSandboxField(adminRuntimeSelectField(
					"tools.code_executor.sandbox.network",
					"Sandbox Network",
					"Network access policy. Only used when Executor Type is sandbox.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey("code_executor"),
						adminRuntimeKey("sandbox"),
						adminRuntimeKey("network"),
					},
					func(opts runOptions) string {
						if !adminRuntimeSandboxCodeExecutorEnabled(opts) {
							return ""
						}
						return strings.TrimSpace(
							opts.CodeExecutor.Sandbox.Network,
						)
					},
					sandboxNetworkRestricted,
					sandboxNetworkEnabled,
				)),
				adminRuntimeSandboxField(adminRuntimeTextField(
					"tools.code_executor.sandbox.default_timeout",
					"Default Timeout",
					"Default timeout for sandbox program runs, for example 30s. Only used when Executor Type is sandbox.",
					defaultSandboxCodeExecutorTimeout.String(),
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey("code_executor"),
						adminRuntimeKey("sandbox"),
						adminRuntimeKey("default_timeout"),
					},
					func(opts runOptions) string {
						if !adminRuntimeSandboxCodeExecutorEnabled(opts) ||
							opts.CodeExecutor.Sandbox.DefaultTimeout <= 0 {
							return ""
						}
						return opts.CodeExecutor.Sandbox.DefaultTimeout.String()
					},
				)),
				adminRuntimeSandboxField(adminRuntimeNumberField(
					"tools.code_executor.sandbox.output_max_bytes",
					"Output Max Bytes",
					"Maximum stdout/stderr bytes captured per stream. Only used when Executor Type is sandbox.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey("code_executor"),
						adminRuntimeKey("sandbox"),
						adminRuntimeKey("output_max_bytes"),
					},
					func(opts runOptions) string {
						if !adminRuntimeSandboxCodeExecutorEnabled(opts) ||
							opts.CodeExecutor.Sandbox.OutputMaxBytes <= 0 {
							return ""
						}
						return strconv.Itoa(
							opts.CodeExecutor.Sandbox.OutputMaxBytes,
						)
					},
				)),
				adminRuntimeSandboxField(adminRuntimeSelectField(
					"tools.code_executor.sandbox.shell_env.inherit",
					"Shell Env Inherit",
					"Host environment inheritance policy for sandbox commands. Only used when Executor Type is sandbox.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey("code_executor"),
						adminRuntimeKey("sandbox"),
						adminRuntimeKey("shell_env"),
						adminRuntimeKey("inherit"),
					},
					func(opts runOptions) string {
						if !adminRuntimeSandboxCodeExecutorEnabled(opts) {
							return ""
						}
						return strings.TrimSpace(
							opts.CodeExecutor.Sandbox.ShellEnv.Inherit,
						)
					},
					sandboxShellEnvInheritAll,
					sandboxShellEnvInheritCore,
					sandboxShellEnvInheritNone,
				)),
				adminRuntimeSandboxField(adminRuntimeBoolField(
					"tools.code_executor.sandbox.shell_env.apply_default_excludes",
					"Apply Default Env Excludes",
					"Drop inherited environment variables whose names look like secrets, such as KEY, TOKEN, SECRET, PASSWORD, or CREDENTIAL. Only used when Executor Type is sandbox.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey("code_executor"),
						adminRuntimeKey("sandbox"),
						adminRuntimeKey("shell_env"),
						adminRuntimeKey("apply_default_excludes"),
					},
					func(opts runOptions) string {
						if !adminRuntimeSandboxCodeExecutorEnabled(opts) {
							return ""
						}
						return strconv.FormatBool(
							opts.CodeExecutor.Sandbox.ShellEnv.ApplyDefaultExcludes,
						)
					},
				)),
			},
		},
		{
			Key:     "storage",
			Title:   "Storage",
			Summary: "Primary session and memory backend selection.",
			Fields: []adminRuntimeConfigFieldSpec{
				adminRuntimeSelectField(
					"session.backend",
					"Session Backend",
					"Conversation session storage backend.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("session"),
						adminRuntimeKey("backend"),
					},
					func(opts runOptions) string {
						return strings.TrimSpace(opts.SessionBackend)
					},
					sessionBackendInMemory,
					sessionBackendRedis,
					sessionBackendSQLite,
					sessionBackendMySQL,
					sessionBackendPostgres,
					sessionBackendClickHouse,
				),
				adminRuntimeSelectField(
					"memory.backend",
					"Memory Backend",
					"Primary memory backend used by the runtime.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("memory"),
						adminRuntimeKey("backend"),
					},
					func(opts runOptions) string {
						return strings.TrimSpace(opts.MemoryBackend)
					},
					memoryBackendFile,
					memoryBackendInMemory,
					memoryBackendRedis,
					memoryBackendSQLite,
					memoryBackendSQLiteVec,
					memoryBackendMySQL,
					memoryBackendPostgres,
					memoryBackendPGVector,
				),
			},
		},
	}
}

func adminRuntimeBoolField(
	key string,
	title string,
	summary string,
	path []adminRuntimeConfigKeyRef,
	runtime func(runOptions) string,
) adminRuntimeConfigFieldSpec {
	return adminRuntimeConfigFieldSpec{
		Key:       key,
		Title:     title,
		Summary:   summary,
		InputType: adminRuntimeConfigInputSelect,
		ApplyMode: adminRuntimeConfigApplyRestart,
		ValueType: adminRuntimeConfigValueBool,
		Path:      path,
		Options: []admin.RuntimeConfigOption{
			{Value: "true", Label: "true"},
			{Value: "false", Label: "false"},
		},
		Runtime: runtime,
	}
}

func adminRuntimeNumberField(
	key string,
	title string,
	summary string,
	path []adminRuntimeConfigKeyRef,
	runtime func(runOptions) string,
) adminRuntimeConfigFieldSpec {
	return adminRuntimeConfigFieldSpec{
		Key:       key,
		Title:     title,
		Summary:   summary,
		InputType: adminRuntimeConfigInputNumber,
		ApplyMode: adminRuntimeConfigApplyRestart,
		ValueType: adminRuntimeConfigValueInt,
		Path:      path,
		Runtime:   runtime,
	}
}

func adminRuntimeTextField(
	key string,
	title string,
	summary string,
	placeholder string,
	path []adminRuntimeConfigKeyRef,
	runtime func(runOptions) string,
) adminRuntimeConfigFieldSpec {
	return adminRuntimeConfigFieldSpec{
		Key:         key,
		Title:       title,
		Summary:     summary,
		InputType:   adminRuntimeConfigInputText,
		Placeholder: placeholder,
		ApplyMode:   adminRuntimeConfigApplyRestart,
		ValueType:   adminRuntimeConfigValueString,
		Path:        path,
		Runtime:     runtime,
	}
}

func adminRuntimeSelectField(
	key string,
	title string,
	summary string,
	path []adminRuntimeConfigKeyRef,
	runtime func(runOptions) string,
	options ...string,
) adminRuntimeConfigFieldSpec {
	return adminRuntimeConfigFieldSpec{
		Key:       key,
		Title:     title,
		Summary:   summary,
		InputType: adminRuntimeConfigInputSelect,
		ApplyMode: adminRuntimeConfigApplyRestart,
		ValueType: adminRuntimeConfigValueString,
		Path:      path,
		Options:   adminRuntimeStringOptions(options...),
		Runtime:   runtime,
	}
}

func adminRuntimeCodeExecutorTypeField(
	key string,
	title string,
	summary string,
	path []adminRuntimeConfigKeyRef,
	runtime func(runOptions) string,
) adminRuntimeConfigFieldSpec {
	return adminRuntimeConfigFieldSpec{
		Key:       key,
		Title:     title,
		Summary:   summary,
		InputType: adminRuntimeConfigInputSelect,
		ApplyMode: adminRuntimeConfigApplyRestart,
		ValueType: adminRuntimeConfigValueString,
		Path:      path,
		Options: []admin.RuntimeConfigOption{
			{Value: "", Label: "inherit"},
			{Value: codeExecutorTypeSandbox, Label: codeExecutorTypeSandbox},
		},
		Runtime: runtime,
	}
}

func adminRuntimeSandboxField(
	field adminRuntimeConfigFieldSpec,
) adminRuntimeConfigFieldSpec {
	field.VisibleWhen = admin.RuntimeConfigVisibleWhen{
		Key:   "tools.code_executor.type",
		Value: codeExecutorTypeSandbox,
	}
	return field
}

func adminRuntimeConfigFieldHidden(
	field adminRuntimeConfigFieldSpec,
	values map[string]string,
) bool {
	if strings.TrimSpace(field.VisibleWhen.Key) == "" {
		return false
	}
	want := strings.TrimSpace(field.VisibleWhen.Value)
	got := strings.TrimSpace(values[field.VisibleWhen.Key])
	return !strings.EqualFold(got, want)
}

func adminRuntimeConfiguredEditorValue(
	spec adminRuntimeConfigFieldSpec,
	value string,
) string {
	value = strings.TrimSpace(value)
	if spec.InputType != adminRuntimeConfigInputSelect {
		return value
	}
	return adminRuntimeCanonicalOptionValue(spec.Options, value)
}

func adminRuntimeStringOptions(
	values ...string,
) []admin.RuntimeConfigOption {
	out := make([]admin.RuntimeConfigOption, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, admin.RuntimeConfigOption{
			Value: value,
			Label: value,
		})
	}
	return out
}

func adminRuntimeCodeExecutorType(opts runOptions) string {
	return strings.TrimSpace(opts.CodeExecutor.Type)
}

func adminRuntimeOptionalBoolValueOrDefault(value *bool, fallback bool) string {
	if value == nil {
		return strconv.FormatBool(fallback)
	}
	return strconv.FormatBool(*value)
}

func adminRuntimeSandboxCodeExecutorEnabled(opts runOptions) bool {
	return strings.EqualFold(
		strings.TrimSpace(opts.CodeExecutor.Type),
		codeExecutorTypeSandbox,
	)
}

func (p *adminRuntimeConfigProvider) normalizeCodeExecutorConfig(
	root *yaml.Node,
	key string,
) error {
	if !strings.HasPrefix(key, "tools.code_executor.") {
		return nil
	}
	toolsNode := adminRuntimeLookupMappingValue(
		root,
		adminRuntimeKey("tools"),
	)
	codeExecutorNode := adminRuntimeLookupMappingValue(
		toolsNode,
		adminRuntimeKey("code_executor"),
	)
	if codeExecutorNode == nil {
		return nil
	}
	if codeExecutorNode.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node")
	}
	switch {
	case key == "tools.code_executor.type":
		typeName := strings.ToLower(strings.TrimSpace(
			adminRuntimeScalarMappingValue(
				codeExecutorNode,
				adminRuntimeKey("type"),
			),
		))
		if typeName != codeExecutorTypeSandbox {
			adminRuntimeDeleteMappingValue(
				codeExecutorNode,
				adminRuntimeKey("sandbox"),
			)
		}
		if typeName == codeExecutorTypeSandbox {
			return adminRuntimeDisableLocalExec(toolsNode)
		}
	case strings.HasPrefix(key, "tools.code_executor.sandbox."):
		if err := adminRuntimeSetMappingString(
			codeExecutorNode,
			adminRuntimeKey("type"),
			codeExecutorTypeSandbox,
		); err != nil {
			return err
		}
		return adminRuntimeDisableLocalExec(toolsNode)
	}
	return nil
}

func adminRuntimeDisableLocalExec(toolsNode *yaml.Node) error {
	return adminRuntimeSetMappingBool(
		toolsNode,
		adminRuntimeKey("enable_local_exec"),
		false,
	)
}

func adminRuntimeConfiguredSource(explicit bool) string {
	if explicit {
		return adminRuntimeConfigSourceExplicit
	}
	return adminRuntimeConfigSourceInherited
}

func adminRuntimeConfiguredLabel(explicit bool) string {
	if explicit {
		return adminRuntimeConfigConfiguredExplicit
	}
	return adminRuntimeConfigConfiguredInherited
}

func adminRuntimeComparableConfiguredValue(
	spec adminRuntimeConfigFieldSpec,
	value string,
) string {
	value = strings.TrimSpace(os.ExpandEnv(value))
	if spec.InputType == adminRuntimeConfigInputSelect {
		value = adminRuntimeCanonicalOptionValue(spec.Options, value)
	}
	switch spec.ValueType {
	case adminRuntimeConfigValueBool:
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return value
		}
		return strconv.FormatBool(parsed)
	case adminRuntimeConfigValueInt:
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return value
		}
		return strconv.Itoa(parsed)
	default:
		return value
	}
}

func adminRuntimeCanonicalOptionValue(
	options []admin.RuntimeConfigOption,
	value string,
) string {
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option.Value), value) {
			return strings.TrimSpace(option.Value)
		}
	}
	return value
}

func adminRuntimeConfigFieldSpecByKey(
	key string,
) (adminRuntimeConfigFieldSpec, bool) {
	key = strings.TrimSpace(key)
	for _, section := range adminRuntimeConfigSectionSpecs() {
		for _, field := range section.Fields {
			if field.Key == key {
				return field, true
			}
		}
	}
	return adminRuntimeConfigFieldSpec{}, false
}

func adminRuntimeConfigRootFromPath(
	path string,
) (*yaml.Node, error) {
	_, root, err := adminRuntimeConfigDocumentFromPath(path)
	return root, err
}

func adminRuntimeConfigDocumentFromPath(
	path string,
) (yaml.Node, *yaml.Node, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return yaml.Node{}, nil, fmt.Errorf("runtime config path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return yaml.Node{}, nil, fmt.Errorf("read config: %w", err)
	}
	doc, err := decodeConfigDocument(data)
	if err != nil {
		return yaml.Node{}, nil, fmt.Errorf("decode config: %w", err)
	}
	root, err := ensureDocumentMapping(&doc)
	if err != nil {
		return yaml.Node{}, nil, fmt.Errorf("config root: %w", err)
	}
	return doc, root, nil
}

func adminRuntimeConfiguredFieldValue(
	root *yaml.Node,
	path []adminRuntimeConfigKeyRef,
) adminRuntimeConfiguredValue {
	node := adminRuntimeFieldNode(root, path)
	if node == nil {
		return adminRuntimeConfiguredValue{}
	}
	return adminRuntimeConfiguredValue{
		Value:    strings.TrimSpace(node.Value),
		Explicit: true,
	}
}

func adminRuntimeFieldNode(
	root *yaml.Node,
	path []adminRuntimeConfigKeyRef,
) *yaml.Node {
	parent := adminRuntimeFieldParent(root, path)
	if parent == nil {
		return nil
	}
	return adminRuntimeLookupMappingValue(parent, path[len(path)-1])
}

func adminRuntimeFieldParent(
	root *yaml.Node,
	path []adminRuntimeConfigKeyRef,
) *yaml.Node {
	current := root
	if current == nil || current.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(path); i++ {
		current = adminRuntimeLookupMappingValue(current, path[i])
		if current == nil || current.Kind != yaml.MappingNode {
			return nil
		}
	}
	return current
}

func adminRuntimeEnsureFieldParent(
	root *yaml.Node,
	path []adminRuntimeConfigKeyRef,
) (*yaml.Node, error) {
	current := root
	if current == nil || current.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config root is not writable")
	}
	for i := 0; i+1 < len(path); i++ {
		next, err := adminRuntimeEnsureMappingChild(current, path[i])
		if err != nil {
			return nil, err
		}
		current = next
	}
	return current, nil
}

func adminRuntimeDeleteField(
	root *yaml.Node,
	path []adminRuntimeConfigKeyRef,
) {
	parent := adminRuntimeFieldParent(root, path)
	if parent == nil {
		return
	}
	adminRuntimeDeleteMappingValue(parent, path[len(path)-1])
}

func adminRuntimeSetFieldValue(
	parent *yaml.Node,
	spec adminRuntimeConfigFieldSpec,
	raw string,
) error {
	raw = strings.TrimSpace(raw)
	if spec.InputType == adminRuntimeConfigInputSelect &&
		!adminRuntimeOptionValueAllowed(raw, spec.Options) {
		return fmt.Errorf("invalid config value")
	}
	if spec.InputType == adminRuntimeConfigInputSelect {
		raw = adminRuntimeCanonicalOptionValue(spec.Options, raw)
	}
	switch spec.ValueType {
	case adminRuntimeConfigValueBool:
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("config: value must be true or false")
		}
		return adminRuntimeSetMappingBool(parent, spec.Path[len(spec.Path)-1], value)
	case adminRuntimeConfigValueInt:
		value, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("config: value must be an integer")
		}
		return adminRuntimeSetMappingInt(parent, spec.Path[len(spec.Path)-1], value)
	default:
		if raw == "" {
			return fmt.Errorf("config: value is required; use Reset to inherit")
		}
		return adminRuntimeSetMappingString(parent, spec.Path[len(spec.Path)-1], raw)
	}
}

func adminRuntimeOptionValueAllowed(
	value string,
	options []admin.RuntimeConfigOption,
) bool {
	if len(options) == 0 {
		return true
	}
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option.Value), value) {
			return true
		}
	}
	return false
}

func adminRuntimeSetMappingBool(
	parent *yaml.Node,
	key adminRuntimeConfigKeyRef,
	value bool,
) error {
	boolValue := "false"
	if value {
		boolValue = "true"
	}
	return adminRuntimeSetScalarValue(parent, key, "!!bool", boolValue)
}

func adminRuntimeSetMappingInt(
	parent *yaml.Node,
	key adminRuntimeConfigKeyRef,
	value int,
) error {
	return adminRuntimeSetScalarValue(
		parent,
		key,
		"!!int",
		strconv.Itoa(value),
	)
}

func adminRuntimeSetMappingString(
	parent *yaml.Node,
	key adminRuntimeConfigKeyRef,
	value string,
) error {
	return adminRuntimeSetScalarValue(parent, key, "!!str", value)
}

func adminRuntimeSetScalarValue(
	parent *yaml.Node,
	key adminRuntimeConfigKeyRef,
	tag string,
	value string,
) error {
	if parent == nil {
		return fmt.Errorf("mapping node is required")
	}
	if parent.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node")
	}

	for i := 0; i+1 < len(parent.Content); i += 2 {
		keyNode := parent.Content[i]
		if !adminRuntimeKeyMatches(keyNode, key) {
			continue
		}
		valueNode := parent.Content[i+1]
		if valueNode == nil {
			valueNode = &yaml.Node{}
			parent.Content[i+1] = valueNode
		}
		if valueNode.Kind != 0 && valueNode.Kind != yaml.ScalarNode {
			return fmt.Errorf("expected scalar node")
		}
		valueNode.Kind = yaml.ScalarNode
		valueNode.Tag = tag
		valueNode.Value = value
		return nil
	}

	parent.Content = append(
		parent.Content,
		&yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: adminRuntimePreferredKey(key),
		},
		&yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   tag,
			Value: value,
		},
	)
	return nil
}

func adminRuntimeEnsureMappingChild(
	parent *yaml.Node,
	key adminRuntimeConfigKeyRef,
) (*yaml.Node, error) {
	if parent == nil {
		return nil, fmt.Errorf("mapping node is required")
	}
	if parent.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected mapping node")
	}

	for i := 0; i+1 < len(parent.Content); i += 2 {
		keyNode := parent.Content[i]
		if !adminRuntimeKeyMatches(keyNode, key) {
			continue
		}
		valueNode := parent.Content[i+1]
		if valueNode == nil {
			valueNode = &yaml.Node{
				Kind: yaml.MappingNode,
				Tag:  "!!map",
			}
			parent.Content[i+1] = valueNode
		}
		if valueNode.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("expected mapping node")
		}
		return valueNode, nil
	}

	keyNode := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Value: adminRuntimePreferredKey(key),
	}
	valueNode := &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
	}
	parent.Content = append(parent.Content, keyNode, valueNode)
	return valueNode, nil
}

func adminRuntimeLookupMappingValue(
	parent *yaml.Node,
	key adminRuntimeConfigKeyRef,
) *yaml.Node {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if adminRuntimeKeyMatches(parent.Content[i], key) {
			return parent.Content[i+1]
		}
	}
	return nil
}

func adminRuntimeScalarMappingValue(
	parent *yaml.Node,
	key adminRuntimeConfigKeyRef,
) string {
	node := adminRuntimeLookupMappingValue(parent, key)
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(node.Value)
}

func adminRuntimeDeleteMappingValue(
	parent *yaml.Node,
	key adminRuntimeConfigKeyRef,
) {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if !adminRuntimeKeyMatches(parent.Content[i], key) {
			continue
		}
		parent.Content = append(parent.Content[:i], parent.Content[i+2:]...)
		return
	}
}

func adminRuntimeKey(
	preferred string,
	aliases ...string,
) adminRuntimeConfigKeyRef {
	return adminRuntimeConfigKeyRef{
		Preferred: strings.TrimSpace(preferred),
		Aliases:   append([]string(nil), aliases...),
	}
}

func adminRuntimePreferredKey(key adminRuntimeConfigKeyRef) string {
	if strings.TrimSpace(key.Preferred) != "" {
		return strings.TrimSpace(key.Preferred)
	}
	for _, alias := range key.Aliases {
		alias = strings.TrimSpace(alias)
		if alias != "" {
			return alias
		}
	}
	return ""
}

func adminRuntimeKeyMatches(
	node *yaml.Node,
	key adminRuntimeConfigKeyRef,
) bool {
	if node == nil {
		return false
	}
	value := strings.TrimSpace(node.Value)
	if value == "" {
		return false
	}
	if value == strings.TrimSpace(key.Preferred) {
		return true
	}
	for _, alias := range key.Aliases {
		if value == strings.TrimSpace(alias) {
			return true
		}
	}
	return false
}
