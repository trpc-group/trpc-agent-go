//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	configKeyChannelsPrefix = "channels."

	configKeyToolsPrefix         = "tools."
	configKeyToolProvidersPrefix = "tools.providers."
	configKeyToolSetsPrefix      = "tools.toolsets."

	configKeyPluginsEntriesPrefix = "plugins.entries."
	configKeyPluginsEnabledSuffix = ".enabled"
	configKeyPluginsConfigPrefix  = ".config"

	configKeyTelegram = "telegram"
	configKeyToken    = "token"

	configKeyExec      = "exec"
	configKeyBash      = "bash"
	configKeyProcess   = "process"
	configKeyLocalExec = "local_exec"
)

func resolveSkillConfigKeys(opts runOptions) []string {
	set := map[string]struct{}{}

	addTelegramConfigKeys(set, opts)
	addPluginSpecsConfigKeys(set, configKeyChannelsPrefix, opts.Channels)
	addPluginSpecsConfigKeys(
		set,
		configKeyToolProvidersPrefix,
		opts.ToolProviders,
	)
	addPluginSpecsConfigKeys(set, configKeyToolSetsPrefix, opts.ToolSets)
	addToolSurfaceKeys(set, opts)

	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func addTelegramConfigKeys(set map[string]struct{}, opts runOptions) {
	if strings.TrimSpace(opts.TelegramToken) == "" {
		return
	}

	chBase := configKeyChannelsPrefix + configKeyTelegram
	addConfigKey(set, chBase)
	addConfigKey(set, chBase+"."+configKeyToken)

	entryBase := configKeyPluginsEntriesPrefix + configKeyTelegram
	addConfigKey(set, entryBase+configKeyPluginsEnabledSuffix)

	cfgBase := entryBase + configKeyPluginsConfigPrefix
	addConfigKey(set, cfgBase)
	addConfigKey(set, cfgBase+"."+configKeyToken)
}

func addPluginSpecsConfigKeys(
	set map[string]struct{},
	prefix string,
	specs []pluginSpec,
) {
	for i := range specs {
		spec := specs[i]
		typeName := normalizeConfigSegment(spec.Type)
		if typeName == "" {
			continue
		}

		base := prefix + typeName
		addConfigKey(set, base)

		entry := configKeyPluginsEntriesPrefix + typeName
		addConfigKey(set, entry+configKeyPluginsEnabledSuffix)

		addPluginConfigNodeKeys(set, base, spec.Config)
		addPluginConfigNodeKeys(
			set,
			entry+configKeyPluginsConfigPrefix,
			spec.Config,
		)
	}
}

func addToolSurfaceKeys(set map[string]struct{}, opts runOptions) {
	if opts.EnableOpenClawTools {
		addConfigKey(set, configKeyToolsPrefix+configKeyExec)
		addConfigKey(set, configKeyToolsPrefix+configKeyBash)
		addConfigKey(set, configKeyToolsPrefix+configKeyProcess)
	}
	if opts.EnableLocalExec {
		addConfigKey(set, configKeyToolsPrefix+configKeyLocalExec)
	}
}

func addPluginConfigNodeKeys(
	set map[string]struct{},
	prefix string,
	node *yaml.Node,
) {
	_ = addYAMLConfigKeys(set, prefix, node)
}

func addYAMLConfigKeys(
	set map[string]struct{},
	prefix string,
	node *yaml.Node,
) bool {
	if node == nil || strings.TrimSpace(prefix) == "" {
		return false
	}

	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) == 0 {
			return false
		}
		return addYAMLConfigKeys(set, prefix, node.Content[0])
	case yaml.MappingNode:
		if len(node.Content) == 0 {
			return false
		}
		any := false
		for i := 0; i+1 < len(node.Content); i += 2 {
			k := normalizeConfigSegment(node.Content[i].Value)
			if k == "" {
				continue
			}
			key := prefix + "." + k
			if addYAMLConfigKeys(set, key, node.Content[i+1]) {
				any = true
			}
		}
		if any {
			addConfigKey(set, prefix)
		}
		return any
	case yaml.SequenceNode:
		if len(node.Content) == 0 {
			return false
		}
		any := false
		for _, child := range node.Content {
			if addYAMLConfigKeys(set, prefix, child) {
				any = true
			}
		}
		if any {
			addConfigKey(set, prefix)
		}
		return any
	case yaml.ScalarNode:
		if !isTruthyScalar(node) {
			return false
		}
		addConfigKey(set, prefix)
		return true
	case yaml.AliasNode:
		return addYAMLConfigKeys(set, prefix, node.Alias)
	default:
		return false
	}
}

func isTruthyScalar(node *yaml.Node) bool {
	if node == nil {
		return false
	}

	val := strings.TrimSpace(node.Value)
	if val == "" {
		return false
	}

	switch node.Tag {
	case "!!bool":
		return strings.EqualFold(val, "true")
	case "!!int":
		n, err := strconv.ParseInt(val, 10, 64)
		return err == nil && n != 0
	case "!!float":
		n, err := strconv.ParseFloat(val, 64)
		return err == nil && n != 0
	default:
		return true
	}
}

func normalizeConfigSegment(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func addConfigKey(set map[string]struct{}, key string) {
	if set == nil {
		return
	}
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return
	}
	set[trimmed] = struct{}{}
}
