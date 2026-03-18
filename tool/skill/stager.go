//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skill

import (
	"context"
	"fmt"
	"path"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	rootskill "trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	errSkillStagerNotConfigured = "skill stager is not configured"
	errSkillRepoNotConfigured   = "skill repository is not configured"
)

// SkillStager materializes a skill into the current workspace.
//
// Implementations may copy, mount, preload, or no-op. The returned
// SkillRoot must be a workspace-relative path that remains within the
// known workspace roots.
type SkillStager interface {
	StageSkill(
		ctx context.Context,
		req SkillStageRequest,
	) (SkillStageResult, error)
}

// SkillStageRequest describes the skill staging context for one run.
type SkillStageRequest struct {
	SkillName  string
	Repository rootskill.Repository
	Engine     codeexecutor.Engine
	Workspace  codeexecutor.Workspace
}

// SkillStageResult reports where the staged skill lives inside the
// workspace.
type SkillStageResult struct {
	SkillRoot string
}

// WithSkillStager overrides the strategy used to materialize skills
// into the workspace.
//
// When unset, RunTool uses the default copy-based stager.
func WithSkillStager(stager SkillStager) func(*RunTool) {
	return func(t *RunTool) {
		t.skillStager = stager
	}
}

type copySkillStager struct {
	tool *RunTool
}

func newCopySkillStager(tool *RunTool) SkillStager {
	return &copySkillStager{tool: tool}
}

func (s *copySkillStager) StageSkill(
	ctx context.Context,
	req SkillStageRequest,
) (SkillStageResult, error) {
	if s == nil || s.tool == nil {
		return SkillStageResult{}, fmt.Errorf(
			errSkillStagerNotConfigured,
		)
	}
	if req.Repository == nil {
		return SkillStageResult{}, fmt.Errorf(
			errSkillRepoNotConfigured,
		)
	}
	root, err := req.Repository.Path(req.SkillName)
	if err != nil {
		return SkillStageResult{}, err
	}
	if err := s.tool.stageSkill(
		ctx,
		req.Engine,
		req.Workspace,
		root,
		req.SkillName,
	); err != nil {
		return SkillStageResult{}, err
	}
	return SkillStageResult{
		SkillRoot: defaultSkillRoot(req.SkillName),
	}, nil
}

func defaultSkillRoot(name string) string {
	return path.Join(codeexecutor.DirSkills, name)
}

func normalizeSkillStageResult(
	res SkillStageResult,
) (SkillStageResult, error) {
	root, err := normalizeStagedSkillRoot(res.SkillRoot)
	if err != nil {
		return SkillStageResult{}, err
	}
	res.SkillRoot = root
	return res, nil
}

func normalizeStagedSkillRoot(skillRoot string) (string, error) {
	root := strings.TrimSpace(skillRoot)
	root = strings.ReplaceAll(root, "\\", "/")
	if root == "" {
		return "", fmt.Errorf("skill root must not be empty")
	}
	if strings.HasPrefix(root, "/") {
		cleaned := path.Clean(root)
		root = strings.TrimPrefix(cleaned, "/")
		if cleaned == "/" {
			root = "."
		}
	} else {
		root = path.Clean(root)
	}
	if root == "" {
		root = "."
	}
	if root == "." {
		return root, nil
	}
	if !isAllowedWorkspacePath(root) {
		return "", fmt.Errorf(
			"skill root %q must stay within the workspace",
			skillRoot,
		)
	}
	return root, nil
}
