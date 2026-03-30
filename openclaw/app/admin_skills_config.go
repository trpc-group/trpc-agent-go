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
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	ocskills "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/skills"
)

func (p *adminSkillsProvider) SkillsStatus() (ocskills.StatusReport, error) {
	if p == nil {
		return ocskills.StatusReport{}, nil
	}

	p.mu.RLock()
	repo := p.repo
	skillConfigs := cloneAdminSkillConfigs(p.skillConfigs)
	roots := append([]string(nil), p.roots...)
	bundledRoot := strings.TrimSpace(p.bundledRoot)
	configKeys := append([]string(nil), p.configKeys...)
	allowBundled := append([]string(nil), p.allowBundled...)
	p.mu.RUnlock()

	if repo != nil {
		return repo.Status(), nil
	}

	return ocskills.BuildStatus(
		roots,
		ocskills.WithBundledSkillsRoot(bundledRoot),
		ocskills.WithConfigKeys(configKeys),
		ocskills.WithAllowBundled(allowBundled),
		ocskills.WithSkillConfigs(skillConfigs),
	)
}

func (p *adminSkillsProvider) SkillsConfigPath() string {
	if p == nil {
		return ""
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	return strings.TrimSpace(p.configPath)
}

func (p *adminSkillsProvider) SkillsRefreshable() bool {
	if p == nil {
		return false
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.repo != nil
}

func (p *adminSkillsProvider) RefreshSkills() error {
	if p == nil {
		return fmt.Errorf("skills config is not available")
	}

	p.mu.RLock()
	repo := p.repo
	p.mu.RUnlock()
	if repo == nil {
		return fmt.Errorf("live skills repository is not available")
	}
	return repo.Refresh()
}

func (p *adminSkillsProvider) SetSkillEnabled(
	configKey string,
	enabled bool,
) error {
	if p == nil {
		return fmt.Errorf("skills config is not available")
	}

	key := strings.TrimSpace(configKey)
	if key == "" {
		return fmt.Errorf("skill config key is required")
	}

	path := ""
	value := enabled
	p.mu.Lock()
	defer p.mu.Unlock()
	path = strings.TrimSpace(p.configPath)
	if path == "" {
		return fmt.Errorf("skill toggles require a config-backed runtime")
	}
	if err := setSkillEnabledInConfig(path, key, enabled); err != nil {
		return err
	}
	repo := p.repo
	if repo != nil {
		if err := repo.SetSkillEnabled(key, enabled); err != nil {
			return err
		}
	}
	if p.skillConfigs == nil {
		p.skillConfigs = map[string]ocskills.SkillConfig{}
	}
	cfg := p.skillConfigs[key]
	cfg.Enabled = &value
	p.skillConfigs[key] = cfg
	return nil
}

func cloneAdminSkillConfigs(
	src map[string]ocskills.SkillConfig,
) map[string]ocskills.SkillConfig {
	if len(src) == 0 {
		return nil
	}

	out := make(map[string]ocskills.SkillConfig, len(src))
	for key, cfg := range src {
		var enabled *bool
		if cfg.Enabled != nil {
			value := *cfg.Enabled
			enabled = &value
		}
		env := make(map[string]string, len(cfg.Env))
		for envKey, envValue := range cfg.Env {
			envKey = strings.TrimSpace(envKey)
			envValue = strings.TrimSpace(envValue)
			if envKey == "" || envValue == "" {
				continue
			}
			env[envKey] = envValue
		}
		if len(env) == 0 {
			env = nil
		}
		out[key] = ocskills.SkillConfig{
			Enabled: enabled,
			APIKey:  strings.TrimSpace(cfg.APIKey),
			Env:     env,
		}
	}
	return out
}

func setSkillEnabledInConfig(
	path string,
	configKey string,
	enabled bool,
) error {
	path = strings.TrimSpace(path)
	configKey = strings.TrimSpace(configKey)
	if path == "" {
		return fmt.Errorf("skills config path is empty")
	}
	if configKey == "" {
		return fmt.Errorf("skill config key is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read config: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("mkdir config dir: %w", err)
		}
		data = nil
	}

	doc, err := decodeConfigDocument(data)
	if err != nil {
		return fmt.Errorf("decode config: %w", err)
	}

	root, err := ensureDocumentMapping(&doc)
	if err != nil {
		return fmt.Errorf("config root: %w", err)
	}
	skillsNode, err := ensureMappingChild(root, "skills")
	if err != nil {
		return fmt.Errorf("config skills: %w", err)
	}
	entriesNode, err := ensureMappingChild(skillsNode, "entries")
	if err != nil {
		return fmt.Errorf("config skills.entries: %w", err)
	}
	entryNode, err := ensureMappingChild(entriesNode, configKey)
	if err != nil {
		return fmt.Errorf("config skills.entries.%s: %w", configKey, err)
	}
	if err := setMappingBool(entryNode, "enabled", enabled); err != nil {
		return fmt.Errorf(
			"config skills.entries.%s.enabled: %w",
			configKey,
			err,
		)
	}

	if err := writeConfigDocument(path, &doc); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func decodeConfigDocument(data []byte) (yaml.Node, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return yaml.Node{
			Kind: yaml.DocumentNode,
			Content: []*yaml.Node{{
				Kind: yaml.MappingNode,
				Tag:  "!!map",
			}},
		}, nil
	}

	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&doc); err != nil {
		return yaml.Node{}, err
	}

	var extra any
	err := dec.Decode(&extra)
	if err == io.EOF {
		return doc, nil
	}
	if err != nil {
		return yaml.Node{}, err
	}
	return yaml.Node{}, fmt.Errorf(
		"multiple YAML documents are not supported",
	)
}

func ensureDocumentMapping(doc *yaml.Node) (*yaml.Node, error) {
	if doc == nil {
		return nil, fmt.Errorf("document is required")
	}
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
	}
	if doc.Kind != yaml.DocumentNode {
		return nil, fmt.Errorf("expected document node")
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{
			Kind: yaml.MappingNode,
			Tag:  "!!map",
		}}
	}
	root := doc.Content[0]
	if root == nil {
		root = &yaml.Node{
			Kind: yaml.MappingNode,
			Tag:  "!!map",
		}
		doc.Content[0] = root
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected mapping node")
	}
	return root, nil
}

func ensureMappingChild(
	parent *yaml.Node,
	key string,
) (*yaml.Node, error) {
	if parent == nil {
		return nil, fmt.Errorf("mapping node is required")
	}
	if parent.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected mapping node")
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("mapping key is required")
	}

	for i := 0; i+1 < len(parent.Content); i += 2 {
		keyNode := parent.Content[i]
		if keyNode == nil || strings.TrimSpace(keyNode.Value) != key {
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
			return nil, fmt.Errorf(
				"expected mapping node for %q",
				key,
			)
		}
		return valueNode, nil
	}

	keyNode := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Value: key,
	}
	valueNode := &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
	}
	parent.Content = append(parent.Content, keyNode, valueNode)
	return valueNode, nil
}

func setMappingBool(
	parent *yaml.Node,
	key string,
	value bool,
) error {
	if parent == nil {
		return fmt.Errorf("mapping node is required")
	}
	if parent.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node")
	}

	boolValue := "false"
	if value {
		boolValue = "true"
	}

	for i := 0; i+1 < len(parent.Content); i += 2 {
		keyNode := parent.Content[i]
		if keyNode == nil || strings.TrimSpace(keyNode.Value) != key {
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
		valueNode.Tag = "!!bool"
		valueNode.Value = boolValue
		return nil
	}

	parent.Content = append(
		parent.Content,
		&yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: key,
		},
		&yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!bool",
			Value: boolValue,
		},
	)
	return nil
}

func writeConfigDocument(path string, doc *yaml.Node) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		_ = enc.Close()
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}

	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(
		filepath.Dir(path),
		filepath.Base(path)+".tmp-*",
	)
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
