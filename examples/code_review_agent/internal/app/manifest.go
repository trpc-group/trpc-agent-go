//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentskill "trpc.group/trpc-go/trpc-agent-go/skill"
)

var requiredRules = map[string]struct{}{
	"GO-SECRET-001": {}, "GO-SEC-001": {}, "GO-CTX-001": {}, "GO-GOR-001": {},
	"GO-RES-001": {}, "GO-ERR-001": {}, "GO-DB-001": {}, "GO-TEST-001": {},
}

var requiredImplementations = map[string]string{
	"GO-SECRET-001": "secret", "GO-SEC-001": "security", "GO-CTX-001": "context",
	"GO-GOR-001": "goroutine", "GO-RES-001": "resource", "GO-ERR-001": "error",
	"GO-DB-001": "database", "GO-TEST-001": "tests",
}

var validModes = map[string]struct{}{"ast": {}, "patch": {}}
var validSeverities = map[string]struct{}{"critical": {}, "high": {}, "medium": {}, "low": {}}

// Rule is one deterministic analyzer declaration.
type Rule struct {
	ID             string   `json:"id"`
	Category       string   `json:"category"`
	Severity       string   `json:"severity"`
	Confidence     float64  `json:"confidence"`
	Modes          []string `json:"modes"`
	Implementation string   `json:"implementation"`
	Enabled        bool     `json:"enabled"`
}

// Manifest is the validated Skill and rules declaration.
type Manifest struct {
	Skill *agentskill.Skill
	Path  string
	Rules []Rule
}

// Load requires all security-relevant Skill files and validates rules.
func Load(root string) (*Manifest, error) {
	repo, err := agentskill.NewFSRepository(root)
	if err != nil {
		return nil, err
	}
	skill, err := repo.Get("code-review")
	if err != nil {
		return nil, err
	}
	dir, err := repo.Path("code-review")
	if err != nil {
		return nil, err
	}
	for _, relative := range []string{"SKILL.md", "README.md", "docs/rules.md", "rules/rules.json", "scripts/checkrunner/main.go"} {
		info, statErr := os.Stat(filepath.Join(dir, filepath.FromSlash(relative)))
		if statErr != nil || info.IsDir() {
			return nil, fmt.Errorf("required skill file missing: %s", relative)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "rules", "rules.json"))
	if err != nil {
		return nil, err
	}
	var rules []Rule
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, fmt.Errorf("decode rules manifest: %w", err)
	}
	if err := validateRules(rules); err != nil {
		return nil, err
	}
	return &Manifest{skill, dir, rules}, nil
}

func validateRules(rules []Rule) error {
	const minimumEnabledCategories = 4
	seen := make(map[string]struct{}, len(rules))
	enabledCategories := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		if _, duplicate := seen[rule.ID]; duplicate {
			return fmt.Errorf("duplicate rule ID %q", rule.ID)
		}
		if err := validateRule(rule); err != nil {
			return err
		}
		seen[rule.ID] = struct{}{}
		if rule.Enabled {
			enabledCategories[rule.Category] = struct{}{}
		}
	}
	for id := range requiredRules {
		if _, ok := seen[id]; !ok {
			return fmt.Errorf("required rule %q missing", id)
		}
	}
	if len(enabledCategories) < minimumEnabledCategories {
		return fmt.Errorf("enabled rules cover %d categories; need at least %d",
			len(enabledCategories), minimumEnabledCategories)
	}
	return nil
}

func validateRule(rule Rule) error {
	if strings.TrimSpace(rule.ID) == "" || strings.TrimSpace(rule.Category) == "" {
		return errors.New("rule ID and category are required")
	}
	expected, ok := requiredImplementations[rule.ID]
	if !ok || rule.Implementation != expected {
		return fmt.Errorf("rule %q requires implementation %q", rule.ID, expected)
	}
	if rule.Confidence < 0 || rule.Confidence > 1 {
		return fmt.Errorf("rule %q confidence is outside [0,1]", rule.ID)
	}
	if _, ok := validSeverities[rule.Severity]; !ok {
		return fmt.Errorf("rule %q has invalid severity %q", rule.ID, rule.Severity)
	}
	if len(rule.Modes) == 0 {
		return fmt.Errorf("rule %q has no analysis mode", rule.ID)
	}
	for _, mode := range rule.Modes {
		if _, ok := validModes[mode]; !ok {
			return fmt.Errorf("rule %q has invalid mode %q", rule.ID, mode)
		}
	}
	return nil
}
