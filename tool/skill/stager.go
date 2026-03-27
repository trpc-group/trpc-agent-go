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
// WorkspaceSkillDir must be the workspace-relative directory of the
// specific staged skill and must remain within the known workspace
// roots.
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
	// WorkspaceSkillDir is the workspace-relative directory of the
	// staged skill, such as "skills/weather" or
	// "work/custom/weather". It must point to the specific skill
	// directory, not just the shared "skills" root, and it must not
	// be a sandbox absolute path like "/sandbox/workspace/skills".
	WorkspaceSkillDir string
}

// WithSkillStager overrides the strategy used to materialize skills
// into the workspace.
//
// When unset, RunTool uses the default copy-based stager, which stages
// the skill under "skills/<skill-name>".
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
		WorkspaceSkillDir: defaultWorkspaceSkillDir(req.SkillName),
	}, nil
}

func defaultWorkspaceSkillDir(name string) string {
	return path.Join(codeexecutor.DirSkills, name)
}

func normalizeSkillStageResult(
	res SkillStageResult,
) (SkillStageResult, error) {
	dir, err := normalizeWorkspaceSkillDir(res.WorkspaceSkillDir)
	if err != nil {
		return SkillStageResult{}, err
	}
	res.WorkspaceSkillDir = dir
	return res, nil
}

func normalizeWorkspaceSkillDir(dir string) (string, error) {
	workspaceDir := strings.TrimSpace(dir)
	workspaceDir = strings.ReplaceAll(workspaceDir, "\\", "/")
	if workspaceDir == "" {
		return "", fmt.Errorf(
			"workspace skill dir must not be empty",
		)
	}
	if strings.HasPrefix(workspaceDir, "/") {
		cleaned := path.Clean(workspaceDir)
		workspaceDir = strings.TrimPrefix(cleaned, "/")
		if cleaned == "/" {
			workspaceDir = "."
		}
	} else {
		workspaceDir = path.Clean(workspaceDir)
	}
	if workspaceDir == "" {
		workspaceDir = "."
	}
	if workspaceDir == "." {
		return workspaceDir, nil
	}
	if !isAllowedWorkspacePath(workspaceDir) {
		return "", fmt.Errorf(
			"workspace skill dir %q must stay within the workspace",
			dir,
		)
	}
	return workspaceDir, nil
}
