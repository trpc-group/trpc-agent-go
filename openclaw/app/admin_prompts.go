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
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/admin"
)

const (
	adminPromptInstructionBundle = "agent_instruction"
	adminPromptSystemBundle      = "agent_system"

	adminPromptFilePerm = 0o600
	adminPromptDirPerm  = 0o700
	adminPromptFileExt  = ".md"
)

type adminPromptProvider struct {
	mu sync.RWMutex

	cwd string

	opts       runOptions
	controller *RuntimePromptController

	instructionOverride *string
	systemOverride      *string
}

func buildAdminPromptProvider(
	opts runOptions,
	controller *RuntimePromptController,
) admin.PromptsProvider {
	if controller == nil {
		return nil
	}
	cwd, _ := os.Getwd()
	return &adminPromptProvider{
		cwd:        strings.TrimSpace(cwd),
		opts:       opts,
		controller: controller,
	}
}

func (p *adminPromptProvider) PromptsStatus() (
	admin.PromptsStatus,
	error,
) {
	if p == nil {
		return admin.PromptsStatus{}, nil
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	snapshot := p.controller.Snapshot()
	bundles := []admin.PromptBundleState{
		p.bundleStateLocked(
			adminPromptInstructionBundle,
			"Instruction",
			snapshot.Instruction,
			p.instructionOverride,
		),
		p.bundleStateLocked(
			adminPromptSystemBundle,
			"System Prompt",
			snapshot.SystemPrompt,
			p.systemOverride,
		),
	}

	return admin.PromptsStatus{
		Enabled: true,
		Sections: []admin.PromptSectionState{{
			Key:     "core",
			Title:   "Core Prompt",
			Summary: "These blocks shape the assistant across every turn.",
			Bundles: bundles,
		}},
		Previews: []admin.PromptPreviewState{{
			Key:   "agent",
			Title: "Agent Prompt",
			Summary: "The resolved instruction and system prompt text" +
				" currently applied to the runtime.",
			Content: buildAdminPromptPreview(
				snapshot.Instruction,
				snapshot.SystemPrompt,
			),
		}},
		Bundles: bundles,
	}, nil
}

func (p *adminPromptProvider) SavePromptRuntime(
	bundleKey string,
	content string,
) error {
	if p == nil {
		return fmt.Errorf("prompt provider is unavailable")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	switch strings.TrimSpace(bundleKey) {
	case adminPromptInstructionBundle:
		p.instructionOverride = promptOverridePtr(content)
	case adminPromptSystemBundle:
		p.systemOverride = promptOverridePtr(content)
	default:
		return fmt.Errorf("unknown prompt bundle: %s", bundleKey)
	}

	return p.applyLocked()
}

func (p *adminPromptProvider) SavePromptInline(
	bundleKey string,
	content string,
) error {
	if p == nil {
		return fmt.Errorf("prompt provider is unavailable")
	}
	return fmt.Errorf("inline prompt edits are not supported: %s", bundleKey)
}

func (p *adminPromptProvider) SavePromptFile(
	bundleKey string,
	path string,
	content string,
) error {
	if p == nil {
		return fmt.Errorf("prompt provider is unavailable")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	path = strings.TrimSpace(path)
	file, ok, err := p.lookupPromptFileLocked(bundleKey, path)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("prompt file is not editable: %s", path)
	}

	if err := writeAdminPromptFile(file.Path, content); err != nil {
		return err
	}
	return p.applyLocked()
}

func (p *adminPromptProvider) CreatePromptFile(
	bundleKey string,
	fileName string,
	content string,
) error {
	if p == nil {
		return fmt.Errorf("prompt provider is unavailable")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	dir, err := p.bundleCreateDirLocked(bundleKey)
	if err != nil {
		return err
	}

	name, err := normalizeAdminPromptFileName(fileName)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("prompt file already exists: %s", name)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := writeAdminPromptFile(path, content); err != nil {
		return err
	}
	return p.applyLocked()
}

func (p *adminPromptProvider) DeletePromptFile(
	bundleKey string,
	path string,
) error {
	if p == nil {
		return fmt.Errorf("prompt provider is unavailable")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	path = strings.TrimSpace(path)
	file, ok, err := p.lookupPromptFileLocked(bundleKey, path)
	if err != nil {
		return err
	}
	if !ok || !file.Deletable {
		return fmt.Errorf("prompt file is not deletable: %s", path)
	}
	if err := os.Remove(file.Path); err != nil {
		return err
	}
	return p.applyLocked()
}

func (p *adminPromptProvider) bundleStateLocked(
	key string,
	title string,
	effective string,
	override *string,
) admin.PromptBundleState {
	configured, loadErr := p.bundleConfiguredPromptLocked(key)
	files, fileErr := p.bundleFilesLocked(key)
	createDir, createErr := p.bundleCreateDirValueLocked(key)

	state := admin.PromptBundleState{
		Key:                key,
		Title:              title,
		Summary:            adminPromptBundleSummary(key),
		SourceSummary:      adminPromptSourceSummary(key, len(files)),
		ConfiguredLabel:    adminPromptConfiguredLabel(key),
		ConfiguredValue:    configured,
		EffectiveLabel:     adminPromptEffectiveLabel(key),
		EffectiveValue:     strings.TrimSpace(effective),
		InlineEditable:     false,
		RuntimeEditable:    true,
		RuntimeOverride:    override != nil,
		SupportsFileEdits:  len(files) > 0,
		SupportsFileCreate: strings.TrimSpace(createDir) != "",
		SupportsFileDelete: true,
		CreateEnabled:      strings.TrimSpace(createDir) != "",
		CreateDir:          strings.TrimSpace(createDir),
		Files:              files,
	}
	if override != nil {
		state.RuntimeValue = *override
	}
	switch {
	case loadErr != nil:
		state.LoadError = loadErr.Error()
	case fileErr != nil:
		state.LoadError = fileErr.Error()
	case createErr != nil:
		state.LoadError = createErr.Error()
	}
	for i := range files {
		if files[i].Deletable {
			return state
		}
	}
	state.SupportsFileDelete = false
	return state
}

func (p *adminPromptProvider) bundleConfiguredPromptLocked(
	key string,
) (string, error) {
	switch strings.TrimSpace(key) {
	case adminPromptInstructionBundle:
		prompt, err := buildAgentPrompt(
			p.opts.AgentInstruction,
			splitCSV(p.opts.AgentInstructionFiles),
			p.opts.AgentInstructionDir,
		)
		if err != nil {
			return "", err
		}
		projectDocs, err := resolveProjectDocs(p.cwd)
		if err != nil {
			return "", err
		}
		prompt = joinPromptParts(projectDocs, prompt)
		if strings.TrimSpace(prompt) == "" {
			prompt = defaultAgentInstruction
		}
		return prompt, nil
	case adminPromptSystemBundle:
		return buildAgentPrompt(
			p.opts.AgentSystemPrompt,
			splitCSV(p.opts.AgentSystemPromptFiles),
			p.opts.AgentSystemPromptDir,
		)
	default:
		return "", fmt.Errorf("unknown prompt bundle: %s", key)
	}
}

func (p *adminPromptProvider) bundleFilesLocked(
	key string,
) ([]admin.PromptFileState, error) {
	files, dir, err := p.bundleResolvedPathsLocked(key)
	if err != nil {
		return nil, err
	}

	out := make([]admin.PromptFileState, 0, len(files)+8)
	for _, path := range files {
		state := admin.PromptFileState{
			Path:  path,
			Label: displayAdminPromptFileLabel(path),
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			state.Error = readErr.Error()
		} else {
			state.Content = string(data)
		}
		out = append(out, state)
	}

	if strings.TrimSpace(dir) == "" {
		return out, nil
	}

	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(dirEntries))
	for _, entry := range dirEntries {
		if entry.IsDir() {
			continue
		}
		if strings.ToLower(filepath.Ext(entry.Name())) !=
			adminPromptFileExt {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(dir, name)
		state := admin.PromptFileState{
			Path:      path,
			Label:     displayAdminPromptFileLabel(path),
			Deletable: true,
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			state.Error = readErr.Error()
		} else {
			state.Content = string(data)
		}
		out = append(out, state)
	}
	return out, nil
}

func (p *adminPromptProvider) lookupPromptFileLocked(
	bundleKey string,
	path string,
) (admin.PromptFileState, bool, error) {
	files, err := p.bundleFilesLocked(bundleKey)
	if err != nil {
		return admin.PromptFileState{}, false, err
	}
	path = filepath.Clean(strings.TrimSpace(path))
	for i := range files {
		if filepath.Clean(files[i].Path) == path {
			return files[i], true, nil
		}
	}
	return admin.PromptFileState{}, false, nil
}

func (p *adminPromptProvider) bundleCreateDirLocked(
	key string,
) (string, error) {
	dir, err := p.bundleCreateDirValueLocked(key)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf(
			"prompt bundle does not support file creation",
		)
	}
	return dir, nil
}

func (p *adminPromptProvider) bundleCreateDirValueLocked(
	key string,
) (string, error) {
	_, dir, err := p.bundleResolvedPathsLocked(key)
	if err != nil {
		return "", err
	}
	return dir, nil
}

func (p *adminPromptProvider) bundleResolvedPathsLocked(
	key string,
) ([]string, string, error) {
	switch strings.TrimSpace(key) {
	case adminPromptInstructionBundle:
		return p.resolvePromptPaths(
			splitCSV(p.opts.AgentInstructionFiles),
			p.opts.AgentInstructionDir,
		)
	case adminPromptSystemBundle:
		return p.resolvePromptPaths(
			splitCSV(p.opts.AgentSystemPromptFiles),
			p.opts.AgentSystemPromptDir,
		)
	default:
		return nil, "", fmt.Errorf(
			"unknown prompt bundle: %s",
			key,
		)
	}
}

func (p *adminPromptProvider) resolvePromptPaths(
	rawFiles []string,
	rawDir string,
) ([]string, string, error) {
	files := make([]string, 0, len(rawFiles))
	for _, raw := range rawFiles {
		path, err := p.resolvePromptPath(raw)
		if err != nil {
			return nil, "", err
		}
		if path == "" {
			continue
		}
		files = append(files, path)
	}
	dir, err := p.resolvePromptPath(rawDir)
	if err != nil {
		return nil, "", err
	}
	return files, dir, nil
}

func (p *adminPromptProvider) resolvePromptPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), nil
	}
	base := strings.TrimSpace(p.cwd)
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		base = cwd
	}
	return filepath.Clean(filepath.Join(base, raw)), nil
}

func (p *adminPromptProvider) applyLocked() error {
	if p.controller == nil {
		return nil
	}

	instruction, err := p.bundleConfiguredPromptLocked(
		adminPromptInstructionBundle,
	)
	if err != nil {
		return err
	}
	systemPrompt, err := p.bundleConfiguredPromptLocked(
		adminPromptSystemBundle,
	)
	if err != nil {
		return err
	}
	if p.instructionOverride != nil {
		instruction = *p.instructionOverride
	}
	if p.systemOverride != nil {
		systemPrompt = *p.systemOverride
	}
	p.controller.SetPrompts(instruction, systemPrompt)
	return nil
}

func buildAdminPromptPreview(
	instruction string,
	systemPrompt string,
) string {
	parts := make([]string, 0, 2)
	if value := strings.TrimSpace(instruction); value != "" {
		parts = append(parts, adminPromptPreviewBlock(
			"Instruction",
			value,
		))
	}
	if value := strings.TrimSpace(systemPrompt); value != "" {
		parts = append(parts, adminPromptPreviewBlock(
			"System Prompt",
			value,
		))
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func adminPromptPreviewBlock(
	title string,
	content string,
) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	return title + "\n" + strings.Repeat("=", len(title)) +
		"\n" + content
}

func adminPromptBundleSummary(key string) string {
	switch strings.TrimSpace(key) {
	case adminPromptInstructionBundle:
		return "Shapes how the assistant reasons and follows project" +
			" guidance across turns."
	case adminPromptSystemBundle:
		return "Defines runtime-level guardrails and identity guidance."
	default:
		return ""
	}
}

func adminPromptConfiguredLabel(key string) string {
	switch strings.TrimSpace(key) {
	case adminPromptInstructionBundle:
		return "Configured Instruction Sources"
	case adminPromptSystemBundle:
		return "Configured System Sources"
	default:
		return "Configured Prompt"
	}
}

func adminPromptEffectiveLabel(key string) string {
	switch strings.TrimSpace(key) {
	case adminPromptInstructionBundle:
		return "Effective Instruction"
	case adminPromptSystemBundle:
		return "Effective System Prompt"
	default:
		return "Effective Prompt"
	}
}

func adminPromptSourceSummary(key string, fileCount int) string {
	switch strings.TrimSpace(key) {
	case adminPromptInstructionBundle:
		if fileCount == 0 {
			return "Built-in defaults"
		}
	case adminPromptSystemBundle:
		if fileCount == 0 {
			return "Inline or runtime-only configuration"
		}
	}
	if fileCount == 1 {
		return "1 editable file"
	}
	if fileCount > 1 {
		return fmt.Sprintf("%d editable files", fileCount)
	}
	return "No editable files"
}

func displayAdminPromptFileLabel(path string) string {
	base := filepath.Base(strings.TrimSpace(path))
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, "-", " ")
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return base
	}
	for i := range parts {
		runes := []rune(parts[i])
		if len(runes) == 0 {
			continue
		}
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}

func promptOverridePtr(content string) *string {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	return &content
}

func writeAdminPromptFile(path string, content string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("prompt path is empty")
	}
	if err := os.MkdirAll(
		filepath.Dir(path),
		adminPromptDirPerm,
	); err != nil {
		return err
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return os.WriteFile(
		path,
		[]byte(content),
		adminPromptFilePerm,
	)
}

func normalizeAdminPromptFileName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("file name is required")
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return "", fmt.Errorf("file name must not include a path")
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("file name is invalid")
	}
	if filepath.Ext(name) == "" {
		name += adminPromptFileExt
	}
	return name, nil
}
