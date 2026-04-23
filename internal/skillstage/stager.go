//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skillstage

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const (
	skillDirInputs = "inputs"
	skillDirVenv   = ".venv"
)

const workspaceMetadataFileMode uint32 = 0o600

const workspaceMetadataTmpFile = ".metadata.tmp"

// Stager materializes skill package contents into a workspace and maintains
// the corresponding workspace metadata and links.
type Stager struct{}

// New creates a skill stager.
func New() *Stager {
	return &Stager{}
}

// StageOptions tunes how StageSkillWithOptions materializes a skill
// working copy. The zero value matches the default behavior expected
// by the workspaceprep reconciler: a writable session-level working
// copy that scripts may freely modify.
type StageOptions struct {
	// ReadOnly flips the staged tree to read-only after copy. This
	// is the legacy behavior used by the now-deprecated skill_run
	// tool and should not be used by new callers; treating skills/
	// as a writable working copy is the default contract.
	ReadOnly bool
}

// StageSkill copies a skill into the shared workspace and links the shared
// work/out roots under skills/<name>. The staged tree is writable by
// default; callers that need the legacy read-only semantics can use
// StageSkillWithOptions.
func (s *Stager) StageSkill(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	root string,
	name string,
) error {
	return s.StageSkillWithOptions(ctx, eng, ws, root, name, StageOptions{})
}

// StageSkillWithOptions is StageSkill with explicit knobs. It exists
// so legacy entry points can request the old read-only behavior while
// new workspace-preparation code keeps the writable-by-default
// contract.
func (s *Stager) StageSkillWithOptions(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	root string,
	name string,
	opts StageOptions,
) error {
	dg, err := codeexecutor.DirDigest(root)
	if err != nil {
		return err
	}
	md, err := s.LoadWorkspaceMetadata(ctx, eng, ws)
	if err != nil {
		return err
	}
	dest := path.Join(codeexecutor.DirSkills, name)
	if meta, ok := md.Skills[name]; ok && meta.Digest == dg && meta.Mounted {
		ok, err := s.SkillLinksPresent(ctx, eng, ws, name)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	if err := s.RemoveWorkspacePath(ctx, eng, ws, dest); err != nil {
		return err
	}
	if err := eng.FS().StageDirectory(
		ctx,
		ws,
		root,
		dest,
		codeexecutor.StageOptions{ReadOnly: false, AllowMount: false},
	); err != nil {
		return err
	}
	if err := s.linkWorkspaceDirs(ctx, eng, ws, name); err != nil {
		return err
	}
	if opts.ReadOnly {
		if err := s.readOnlyExceptSymlinks(
			ctx, eng, ws, dest,
		); err != nil {
			return err
		}
	}
	md.Skills[name] = codeexecutor.SkillMeta{
		Name:     name,
		RelPath:  dest,
		Digest:   dg,
		Mounted:  true,
		StagedAt: time.Now(),
	}
	return s.SaveWorkspaceMetadata(ctx, eng, ws, md)
}

// LoadWorkspaceMetadata reads workspace metadata from the shared metadata file
// and returns a normalized in-memory view with defaults applied.
func (s *Stager) LoadWorkspaceMetadata(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
) (codeexecutor.WorkspaceMetadata, error) {
	now := time.Now()
	md := codeexecutor.WorkspaceMetadata{
		Version:    1,
		CreatedAt:  now,
		UpdatedAt:  now,
		LastAccess: now,
		Skills:     map[string]codeexecutor.SkillMeta{},
	}
	if eng == nil || eng.FS() == nil {
		return md, fmt.Errorf("workspace fs is not configured")
	}
	files, err := eng.FS().Collect(
		ctx, ws, []string{codeexecutor.MetaFileName},
	)
	if err != nil {
		return md, err
	}
	if len(files) == 0 || strings.TrimSpace(files[0].Content) == "" {
		return md, nil
	}
	if err := json.Unmarshal([]byte(files[0].Content), &md); err != nil {
		return codeexecutor.WorkspaceMetadata{}, err
	}
	if md.Version == 0 {
		md.Version = 1
	}
	if md.CreatedAt.IsZero() {
		md.CreatedAt = now
	}
	md.LastAccess = now
	if md.Skills == nil {
		md.Skills = map[string]codeexecutor.SkillMeta{}
	}
	return md, nil
}

// SaveWorkspaceMetadata persists workspace metadata into the shared metadata
// file within the current workspace.
func (s *Stager) SaveWorkspaceMetadata(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	md codeexecutor.WorkspaceMetadata,
) error {
	if eng == nil || eng.FS() == nil {
		return fmt.Errorf("workspace fs is not configured")
	}
	if eng.Runner() == nil {
		return fmt.Errorf("workspace runner is not configured")
	}
	if md.Version == 0 {
		md.Version = 1
	}
	now := time.Now()
	if md.CreatedAt.IsZero() {
		md.CreatedAt = now
	}
	md.UpdatedAt = now
	md.LastAccess = now
	if md.Skills == nil {
		md.Skills = map[string]codeexecutor.SkillMeta{}
	}
	buf, err := json.MarshalIndent(md, "", "  ")
	if err != nil {
		return err
	}
	if err := eng.FS().PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    workspaceMetadataTmpFile,
		Content: buf,
		Mode:    workspaceMetadataFileMode,
	}}); err != nil {
		return err
	}
	var sb strings.Builder
	sb.WriteString("set -e; mv -f ")
	sb.WriteString(shellQuote(workspaceMetadataTmpFile))
	sb.WriteString(" ")
	sb.WriteString(shellQuote(codeexecutor.MetaFileName))
	_, err = eng.Runner().RunProgram(
		ctx, ws, codeexecutor.RunProgramSpec{
			Cmd:     "bash",
			Args:    []string{"-lc", sb.String()},
			Env:     map[string]string{},
			Cwd:     ".",
			Timeout: 5 * time.Second,
		},
	)
	return err
}

// SkillLinksPresent reports whether the staged skill directory still exposes
// the expected shared-directory symlinks back into the workspace roots.
func (s *Stager) SkillLinksPresent(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	name string,
) (bool, error) {
	skillName := strings.TrimSpace(name)
	if skillName == "" {
		return false, nil
	}
	if eng == nil || eng.Runner() == nil {
		return false, fmt.Errorf("workspace runner is not configured")
	}
	base := path.Join(codeexecutor.DirSkills, skillName)
	var sb strings.Builder
	sb.WriteString("test -L ")
	sb.WriteString(shellQuote(path.Join(base, codeexecutor.DirOut)))
	sb.WriteString(" && test -L ")
	sb.WriteString(shellQuote(path.Join(base, codeexecutor.DirWork)))
	sb.WriteString(" && test -L ")
	sb.WriteString(shellQuote(path.Join(base, skillDirInputs)))
	rr, err := eng.Runner().RunProgram(
		ctx, ws, codeexecutor.RunProgramSpec{
			Cmd:     "bash",
			Args:    []string{"-lc", sb.String()},
			Env:     map[string]string{},
			Cwd:     ".",
			Timeout: 5 * time.Second,
		},
	)
	if err != nil {
		return false, err
	}
	return rr.ExitCode == 0, nil
}

func (s *Stager) linkWorkspaceDirs(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	name string,
) error {
	skillRoot := path.Join(codeexecutor.DirSkills, name)
	toOut := path.Join("..", "..", codeexecutor.DirOut)
	toWork := path.Join("..", "..", codeexecutor.DirWork)
	toInputs := path.Join("..", "..", codeexecutor.DirWork, skillDirInputs)
	var sb strings.Builder
	sb.WriteString("set -e; cd ")
	sb.WriteString(shellQuote(skillRoot))
	sb.WriteString("; rm -rf out work ")
	sb.WriteString(skillDirInputs)
	sb.WriteString(" ")
	sb.WriteString(shellQuote(skillDirVenv))
	sb.WriteString("; mkdir -p ")
	sb.WriteString(shellQuote(toInputs))
	sb.WriteString(" ")
	sb.WriteString(shellQuote(skillDirVenv))
	sb.WriteString("; ln -sfn ")
	sb.WriteString(shellQuote(toOut))
	sb.WriteString(" out; ln -sfn ")
	sb.WriteString(shellQuote(toWork))
	sb.WriteString(" work; ln -sfn ")
	sb.WriteString(shellQuote(toInputs))
	sb.WriteString(" inputs")
	_, err := eng.Runner().RunProgram(
		ctx, ws, codeexecutor.RunProgramSpec{
			Cmd:     "bash",
			Args:    []string{"-lc", sb.String()},
			Env:     map[string]string{},
			Cwd:     ".",
			Timeout: 5 * time.Second,
		},
	)
	return err
}

// RemoveWorkspacePath removes a workspace-relative path after first making
// non-symlink files writable so cleanup can succeed on read-only staged trees.
func (s *Stager) RemoveWorkspacePath(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	rel string,
) error {
	target := strings.TrimSpace(rel)
	if target == "" {
		return nil
	}
	if eng == nil || eng.Runner() == nil {
		return fmt.Errorf("workspace runner is not configured")
	}
	var sb strings.Builder
	sb.WriteString("set -e; if [ -e ")
	sb.WriteString(shellQuote(target))
	sb.WriteString(" ]; then find ")
	sb.WriteString(shellQuote(target))
	sb.WriteString(" -type l -prune -o -exec chmod u+w {} +; fi")
	sb.WriteString("; rm -rf ")
	sb.WriteString(shellQuote(target))
	_, err := eng.Runner().RunProgram(
		ctx,
		ws,
		codeexecutor.RunProgramSpec{
			Cmd:     "bash",
			Args:    []string{"-lc", sb.String()},
			Env:     map[string]string{},
			Cwd:     ".",
			Timeout: 5 * time.Second,
		},
	)
	return err
}

func (s *Stager) readOnlyExceptSymlinks(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	dest string,
) error {
	venv := path.Join(dest, skillDirVenv)
	var sb strings.Builder
	sb.WriteString("set -e; find ")
	sb.WriteString(shellQuote(dest))
	sb.WriteString(" -path ")
	sb.WriteString(shellQuote(venv))
	sb.WriteString(" -prune -o -type l -prune -o -exec chmod a-w {} +")
	_, err := eng.Runner().RunProgram(
		ctx, ws, codeexecutor.RunProgramSpec{
			Cmd:     "bash",
			Args:    []string{"-lc", sb.String()},
			Env:     map[string]string{},
			Cwd:     ".",
			Timeout: 5 * time.Second,
		},
	)
	return err
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	q := strings.ReplaceAll(s, "'", "'\\''")
	return "'" + q + "'"
}
