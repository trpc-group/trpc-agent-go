//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type reviewSkill struct {
	Name    string
	Content string
}

func selectSkill(lines []changedLine, skillsRoot string) (*reviewSkill, error) {
	hasGo := false
	for _, line := range lines {
		if strings.HasSuffix(strings.ToLower(line.File), ".go") {
			hasGo = true
			break
		}
	}
	if !hasGo {
		return nil, errors.New("no review skill matches the changed file types")
	}
	path := filepath.Join(skillsRoot, "code-review", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read code-review skill: %w", err)
	}
	content := string(data)
	if !strings.Contains(content, "name: code-review") || !strings.Contains(content, "rule_id:") {
		return nil, errors.New("code-review skill is missing required metadata or rules")
	}
	return &reviewSkill{Name: "code-review", Content: content}, nil
}
