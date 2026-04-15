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
				editorValue = configured.Value
				nextValue = adminRuntimeComparableConfiguredValue(
					field,
					configured.Value,
				)
			}
			view.Fields = append(view.Fields, admin.RuntimeConfigField{
				Key:                   field.Key,
				Title:                 field.Title,
				Summary:               field.Summary,
				InputType:             field.InputType,
				Placeholder:           field.Placeholder,
				ApplyMode:             field.ApplyMode,
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
	parent, err := adminRuntimeEnsureFieldParent(root, spec.Path)
	if err != nil {
		return err
	}
	if err := adminRuntimeSetFieldValue(parent, spec, value); err != nil {
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
					"Expose the local execution tool.",
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
					"Expose host-side OpenClaw runtime tools.",
					[]adminRuntimeConfigKeyRef{
						adminRuntimeKey("tools"),
						adminRuntimeKey("enable_openclaw_tools"),
					},
					func(opts runOptions) string {
						return strconv.FormatBool(opts.EnableOpenClawTools)
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
		if strings.TrimSpace(option.Value) == value {
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
