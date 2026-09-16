//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"fmt"
	"reflect"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const defaultModelDescription = "Optional host-authorized model profile for " +
	"this sub-agent invocation. Omit to keep the base/template agent's " +
	"existing model selection."

type agentModelProfile struct {
	name        string
	description string
	model       model.Model
}

// WithAgentModelProfile registers one host-authorized model profile that the
// parent model may select for a dynamic sub-agent invocation with the "model"
// field. The name is a stable model-facing alias; the model instance and
// description remain owned by application code. This option may be repeated.
//
// The name and description are included in the tool declaration visible to
// the parent model. Do not put credentials or private provider configuration
// in either value. Profile aliases are an allowlist and are never interpreted
// as provider model identifiers. Omitting "model" preserves the existing
// behavior: a configured template keeps its default model, while a
// parent-derived child keeps the parent's effective model selection.
// Selecting a profile clears inherited ModelContextWindow,
// ModelRequestExtraFields, and ModelRequestHeaders so provider-specific parent
// settings cannot rewrite or leak into the selected model request. Configure
// such settings on the registered model instead.
//
// Selected profiles are consumed by LLMAgent. Other Agent implementations keep
// their own model semantics; use an LLMAgent base or template when model routing
// is required. This option is only meaningful for NewDynamicTool; NewTool
// ignores it.
//
// NewDynamicTool panics if a registered profile has an empty name or
// description, a nil model, or a duplicate name after trimming whitespace.
// Profile names are matched exactly and case-sensitively after surrounding
// whitespace is removed.
func WithAgentModelProfile(
	name string,
	description string,
	m model.Model,
) Option {
	return func(opts *agentToolOptions) {
		cfg := opts.ensureDynamicOptions()
		cfg.modelProfiles = append(cfg.modelProfiles, agentModelProfile{
			name:        name,
			description: description,
			model:       m,
		})
	}
}

func mustNormalizeAgentModelProfiles(
	profiles []agentModelProfile,
) []agentModelProfile {
	if len(profiles) == 0 {
		return nil
	}
	normalized := make([]agentModelProfile, 0, len(profiles))
	seen := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		name := strings.TrimSpace(profile.name)
		description := strings.TrimSpace(profile.description)
		switch {
		case name == "":
			panic("Invalid Dynamic AgentTool configuration: agent model profile name is required")
		case description == "":
			panic(fmt.Sprintf(
				"Invalid Dynamic AgentTool configuration: agent model profile %q description is required",
				name,
			))
		case isNilAgentModel(profile.model):
			panic(fmt.Sprintf(
				"Invalid Dynamic AgentTool configuration: agent model profile %q model is required",
				name,
			))
		case seen[name]:
			panic(fmt.Sprintf(
				"Invalid Dynamic AgentTool configuration: duplicate agent model profile %q",
				name,
			))
		}
		seen[name] = true
		normalized = append(normalized, agentModelProfile{
			name:        name,
			description: description,
			model:       profile.model,
		})
	}
	return normalized
}

func isNilAgentModel(m model.Model) bool {
	if m == nil {
		return true
	}
	value := reflect.ValueOf(m)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func agentModelProfileSchema(profiles []agentModelProfile) *tool.Schema {
	values := make([]any, 0, len(profiles))
	lines := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		values = append(values, profile.name)
		lines = append(lines, profile.name+": "+profile.description)
	}
	description := defaultModelDescription
	if len(lines) > 0 {
		description += " Available profiles: " + strings.Join(lines, "; ") + "."
	}
	return &tool.Schema{
		Type:        "string",
		Description: description,
		Enum:        values,
	}
}

func (at *Tool) resolveAgentModelProfile(alias string) (model.Model, error) {
	if alias == "" {
		return nil, nil
	}
	available := make([]string, 0, len(at.dynamicCfg.modelProfiles))
	for _, profile := range at.dynamicCfg.modelProfiles {
		available = append(available, profile.name)
		if profile.name == alias {
			return profile.model, nil
		}
	}
	if len(available) == 0 {
		return nil, fmt.Errorf(
			"agenttool: unknown agent model profile %q; available: (none)",
			alias,
		)
	}
	return nil, fmt.Errorf(
		"agenttool: unknown agent model profile %q; available: %s",
		alias,
		strings.Join(available, ", "),
	)
}
