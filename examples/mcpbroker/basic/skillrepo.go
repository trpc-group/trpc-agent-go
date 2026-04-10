//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const remoteMCPURLPlaceholder = "__REMOTE_MCP_URL__"

func prepareRenderedSkillsRoot(exampleDir string, remoteMCPURL string) (string, error) {
	templateRoot := filepath.Join(exampleDir, "skills")
	renderedRoot, err := os.MkdirTemp("", "trpc-agent-go-mcpbroker-skills-*")
	if err != nil {
		return "", fmt.Errorf("create rendered skills root: %w", err)
	}

	if err := copySkillTemplateTree(templateRoot, renderedRoot, map[string]string{
		remoteMCPURLPlaceholder: remoteMCPURL,
	}); err != nil {
		_ = os.RemoveAll(renderedRoot)
		return "", err
	}
	return renderedRoot, nil
}

func copySkillTemplateTree(srcRoot string, dstRoot string, replacements map[string]string) error {
	replacerArgs := make([]string, 0, len(replacements)*2)
	for oldValue, newValue := range replacements {
		replacerArgs = append(replacerArgs, oldValue, newValue)
	}
	replacer := strings.NewReplacer(replacerArgs...)

	return filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return fmt.Errorf("resolve skill template path %q: %w", path, err)
		}
		if relPath == "." {
			return nil
		}

		targetPath := filepath.Join(dstRoot, relPath)
		if d.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read skill template %q: %w", path, err)
		}
		rendered := replacer.Replace(string(content))
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("create skill template dir %q: %w", targetPath, err)
		}
		if err := os.WriteFile(targetPath, []byte(rendered), 0o644); err != nil {
			return fmt.Errorf("write rendered skill file %q: %w", targetPath, err)
		}
		return nil
	})
}
