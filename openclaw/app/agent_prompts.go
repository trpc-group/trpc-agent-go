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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type resolvedAgentPrompts struct {
	Instruction  string
	SystemPrompt string
}

func resolveAgentPrompts(opts runOptions) (resolvedAgentPrompts, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return resolvedAgentPrompts{}, err
	}
	return resolveAgentPromptsForDir(opts, cwd)
}

func resolveAgentPromptsForDir(
	opts runOptions,
	cwd string,
) (resolvedAgentPrompts, error) {
	instruction, err := buildAgentPrompt(
		opts.AgentInstruction,
		splitCSV(opts.AgentInstructionFiles),
		opts.AgentInstructionDir,
	)
	if err != nil {
		return resolvedAgentPrompts{}, err
	}
	projectDocs, err := resolveProjectDocs(cwd)
	if err != nil {
		return resolvedAgentPrompts{}, err
	}
	if strings.TrimSpace(projectDocs) != "" {
		instruction = joinPromptParts(projectDocs, instruction)
	}
	if strings.TrimSpace(instruction) == "" {
		instruction = defaultAgentInstruction
	}

	systemPrompt, err := buildAgentPrompt(
		opts.AgentSystemPrompt,
		splitCSV(opts.AgentSystemPromptFiles),
		opts.AgentSystemPromptDir,
	)
	if err != nil {
		return resolvedAgentPrompts{}, err
	}

	return resolvedAgentPrompts{
		Instruction:  instruction,
		SystemPrompt: systemPrompt,
	}, nil
}

func joinPromptParts(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return strings.Join(out, "\n\n")
}

func buildAgentPrompt(inline string, files []string, dir string) (string, error) {
	parts := make([]string, 0, 1+len(files))
	if v := strings.TrimSpace(inline); v != "" {
		parts = append(parts, v)
	}

	for i := range files {
		path := strings.TrimSpace(files[i])
		if path == "" {
			continue
		}
		content, err := readAgentPromptFile(path)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		parts = append(parts, content)
	}

	dir = strings.TrimSpace(dir)
	if dir != "" {
		dirParts, err := readAgentPromptDir(dir)
		if err != nil {
			return "", err
		}
		parts = append(parts, dirParts...)
	}

	return joinPromptParts(parts...), nil
}

func readAgentPromptFile(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("prompt file path is empty")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt file %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func readAgentPromptDir(dir string) ([]string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("prompt dir is empty")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read prompt dir %s: %w", dir, err)
	}

	files := make([]string, 0, len(entries))
	for i := range entries {
		entry := entries[i]
		if entry.IsDir() {
			continue
		}
		if strings.ToLower(filepath.Ext(entry.Name())) != ".md" {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no .md prompt files in dir %s", dir)
	}

	sort.Strings(files)

	parts := make([]string, 0, len(files))
	for i := range files {
		content, err := readAgentPromptFile(files[i])
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		parts = append(parts, content)
	}
	return parts, nil
}
