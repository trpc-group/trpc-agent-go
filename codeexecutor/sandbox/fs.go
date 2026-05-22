//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"context"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	ds "github.com/bmatcuk/doublestar/v4"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const (
	maxCollectFileBytes = 4 << 20

	inputSchemeArtifact  = "artifact://"
	inputSchemeHost      = "host://"
	inputSchemeWorkspace = "workspace://"
	inputSchemeSkill     = "skill://"
)

// PutFiles writes files into the workspace after sandbox policy checks.
func (r *Runtime) PutFiles(
	ctx context.Context,
	ws codeexecutor.Workspace,
	files []codeexecutor.PutFile,
) error {
	_ = ctx
	profile := applyAdditionalPermissions(
		normalizeProfile(r.profile),
		additionalPermissionsFromContext(ctx),
	)
	for _, f := range files {
		if err := r.checkWrite(profile, ws, f.Path); err != nil {
			return err
		}
		abs, _, err := r.resolveWorkspacePath(ws, f.Path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(f.Mode)
		if mode == 0 {
			mode = codeexecutor.DefaultScriptFileMode
		}
		if err := os.WriteFile(abs, f.Content, mode); err != nil {
			return err
		}
		if err := os.Chmod(abs, mode); err != nil {
			return err
		}
	}
	return nil
}

// StageDirectory copies a host directory into the workspace. Host sources need
// an explicit read grant unless sandboxing is disabled.
func (r *Runtime) StageDirectory(
	ctx context.Context,
	ws codeexecutor.Workspace,
	src, to string,
	opt codeexecutor.StageOptions,
) error {
	_ = ctx
	profile := applyAdditionalPermissions(
		normalizeProfile(r.profile),
		additionalPermissionsFromContext(ctx),
	)
	if profile.enforcement() != enforcementDisabled && !pathHasRule(profile, src, AccessRead) {
		return deniedf(ErrPathDenied, "read", src, "host path requires explicit read grant")
	}
	if err := r.checkWrite(profile, ws, to); err != nil {
		return err
	}
	dst, _, err := r.resolveWorkspacePath(ws, to)
	if err != nil {
		return err
	}
	if err := copyPath(src, dst); err != nil {
		return err
	}
	if opt.ReadOnly {
		return filepath.WalkDir(dst, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			mode := os.FileMode(0o555)
			if !d.IsDir() {
				mode = 0o444
			}
			return os.Chmod(path, mode)
		})
	}
	return nil
}

// Collect returns workspace files matching patterns after read checks.
func (r *Runtime) Collect(
	ctx context.Context,
	ws codeexecutor.Workspace,
	patterns []string,
) ([]codeexecutor.File, error) {
	_ = ctx
	if _, err := codeexecutor.EnsureLayout(ws.Path); err != nil {
		return nil, err
	}
	profile := applyAdditionalPermissions(
		normalizeProfile(r.profile),
		additionalPermissionsFromContext(ctx),
	)
	patterns = codeexecutor.NormalizeGlobs(patterns)
	var files []codeexecutor.File
	for _, pattern := range patterns {
		pattern = filepath.ToSlash(filepath.Clean(pattern))
		if pattern == "." {
			pattern = "**"
		}
		glob := strings.TrimPrefix(filepath.ToSlash(filepath.Join(ws.Path, pattern)), "/")
		matches, err := ds.Glob(os.DirFS("/"), glob)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			abs := "/" + strings.TrimPrefix(match, "/")
			info, err := os.Stat(abs)
			if err != nil {
				return nil, err
			}
			if info.IsDir() {
				continue
			}
			rel, err := filepath.Rel(ws.Path, abs)
			if err != nil {
				return nil, err
			}
			if err := r.checkRead(profile, ws, rel); err != nil {
				return nil, err
			}
			content, truncated, err := readFileLimited(abs, maxCollectFileBytes)
			if err != nil {
				return nil, err
			}
			files = append(files, codeexecutor.File{
				Name:      filepath.ToSlash(rel),
				Content:   string(content),
				MIMEType:  mime.TypeByExtension(filepath.Ext(abs)),
				SizeBytes: info.Size(),
				Truncated: truncated,
			})
		}
	}
	return files, nil
}

// StageInputs maps supported external input specs into the workspace.
func (r *Runtime) StageInputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	specs []codeexecutor.InputSpec,
) error {
	return codeexecutor.WithWorkspaceMetadataLock(
		ctx,
		ws.Path,
		func(ctx context.Context) error {
			return r.stageInputsLocked(ctx, ws, specs)
		},
	)
}

func (r *Runtime) stageInputsLocked(
	ctx context.Context,
	ws codeexecutor.Workspace,
	specs []codeexecutor.InputSpec,
) error {
	if _, err := codeexecutor.EnsureLayout(ws.Path); err != nil {
		return err
	}
	profile := applyAdditionalPermissions(
		normalizeProfile(r.profile),
		additionalPermissionsFromContext(ctx),
	)
	md, err := codeexecutor.LoadMetadata(ws.Path)
	if err != nil {
		return err
	}
	for _, sp := range specs {
		mode := strings.ToLower(strings.TrimSpace(sp.Mode))
		if mode == "" {
			mode = "copy"
		}
		to := strings.TrimSpace(sp.To)
		if to == "" {
			to = filepath.Join(codeexecutor.DirWork, "inputs", inputName(sp.From))
		}
		resolved, version, err := r.stageInput(ctx, ws, profile, md, sp, to)
		if err != nil {
			return err
		}
		md.Inputs = append(md.Inputs, codeexecutor.InputRecord{
			From:      sp.From,
			To:        to,
			Resolved:  resolved,
			Version:   version,
			Mode:      mode,
			Timestamp: time.Now(),
		})
	}
	return codeexecutor.SaveMetadata(ws.Path, md)
}

func (r *Runtime) stageInput(
	ctx context.Context,
	ws codeexecutor.Workspace,
	profile PermissionProfile,
	md codeexecutor.WorkspaceMetadata,
	sp codeexecutor.InputSpec,
	to string,
) (string, *int, error) {
	switch {
	case strings.HasPrefix(sp.From, inputSchemeArtifact):
		return r.stageArtifactInput(ctx, ws, md, sp, to)
	case strings.HasPrefix(sp.From, inputSchemeHost):
		return r.stageHostInput(ctx, ws, sp, to)
	case strings.HasPrefix(sp.From, inputSchemeWorkspace):
		return r.stageWorkspaceInput(ws, profile, sp, to)
	case strings.HasPrefix(sp.From, inputSchemeSkill):
		return r.stageSkillInput(ws, profile, sp, to)
	default:
		return "", nil, fmt.Errorf("unsupported input: %s", sp.From)
	}
}

func (r *Runtime) stageArtifactInput(
	ctx context.Context,
	ws codeexecutor.Workspace,
	md codeexecutor.WorkspaceMetadata,
	sp codeexecutor.InputSpec,
	to string,
) (string, *int, error) {
	name := strings.TrimPrefix(sp.From, inputSchemeArtifact)
	aname, aver, err := codeexecutor.ParseArtifactRef(name)
	if err != nil {
		return "", nil, err
	}
	useVer := aver
	if useVer == nil && sp.Pin {
		useVer = pinnedArtifactVersion(md, aname, to)
	}
	data, _, actual, err := codeexecutor.LoadArtifactHelper(ctx, aname, useVer)
	if err != nil {
		return "", nil, err
	}
	version := useVer
	if version == nil {
		v := actual
		version = &v
	}
	if err := r.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    to,
		Content: data,
		Mode:    codeexecutor.DefaultScriptFileMode,
	}}); err != nil {
		return "", nil, err
	}
	return aname, version, nil
}

func (r *Runtime) stageHostInput(
	ctx context.Context,
	ws codeexecutor.Workspace,
	sp codeexecutor.InputSpec,
	to string,
) (string, *int, error) {
	src := strings.TrimPrefix(sp.From, inputSchemeHost)
	if err := r.StageDirectory(ctx, ws, src, to, codeexecutor.StageOptions{}); err != nil {
		return "", nil, err
	}
	return src, nil, nil
}

func (r *Runtime) stageWorkspaceInput(
	ws codeexecutor.Workspace,
	profile PermissionProfile,
	sp codeexecutor.InputSpec,
	to string,
) (string, *int, error) {
	srcRel := strings.TrimPrefix(sp.From, inputSchemeWorkspace)
	if err := r.stageWorkspaceRelativePath(ws, profile, srcRel, to); err != nil {
		return "", nil, err
	}
	return srcRel, nil, nil
}

func (r *Runtime) stageSkillInput(
	ws codeexecutor.Workspace,
	profile PermissionProfile,
	sp codeexecutor.InputSpec,
	to string,
) (string, *int, error) {
	rest := strings.TrimPrefix(sp.From, inputSchemeSkill)
	srcRel := filepath.Join(codeexecutor.DirSkills, filepath.Clean(rest))
	if err := r.stageWorkspaceRelativePath(ws, profile, srcRel, to); err != nil {
		return "", nil, err
	}
	return srcRel, nil, nil
}

func (r *Runtime) stageWorkspaceRelativePath(
	ws codeexecutor.Workspace,
	profile PermissionProfile,
	srcRel string,
	to string,
) error {
	if err := r.checkRead(profile, ws, srcRel); err != nil {
		return err
	}
	if err := r.checkWrite(profile, ws, to); err != nil {
		return err
	}
	src, _, err := r.resolveWorkspacePath(ws, srcRel)
	if err != nil {
		return err
	}
	dst, _, err := r.resolveWorkspacePath(ws, to)
	if err != nil {
		return err
	}
	return copyPath(src, dst)
}

// CollectOutputs applies the declarative output spec under the same read
// policy as Collect.
func (r *Runtime) CollectOutputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	if _, err := codeexecutor.EnsureLayout(ws.Path); err != nil {
		return codeexecutor.OutputManifest{}, err
	}
	if len(spec.Globs) == 0 {
		spec.Globs = []string{filepath.Join(codeexecutor.DirOut, "**")}
	}
	maxFiles := spec.MaxFiles
	if maxFiles <= 0 {
		maxFiles = 100
	}
	maxFileBytes := spec.MaxFileBytes
	if maxFileBytes <= 0 {
		maxFileBytes = maxCollectFileBytes
	}
	maxTotalBytes := spec.MaxTotalBytes
	if maxTotalBytes <= 0 {
		maxTotalBytes = 64 << 20
	}
	profile := applyAdditionalPermissions(
		normalizeProfile(r.profile),
		additionalPermissionsFromContext(ctx),
	)
	globs := codeexecutor.NormalizeGlobs(spec.Globs)
	out := codeexecutor.OutputManifest{}
	var savedNames []string
	var savedVersions []int
	leftTotal := maxTotalBytes
	count := 0
	for _, pattern := range globs {
		pattern = filepath.ToSlash(filepath.Clean(pattern))
		if pattern == "." {
			pattern = "**"
		}
		absPattern := filepath.Join(ws.Path, pattern)
		glob := strings.TrimPrefix(filepath.ToSlash(absPattern), "/")
		matches, err := ds.Glob(os.DirFS("/"), glob)
		if err != nil {
			return codeexecutor.OutputManifest{}, err
		}
		for _, match := range matches {
			if count >= maxFiles || leftTotal <= 0 {
				out.LimitsHit = true
				break
			}
			ref, consumed, skip, err := r.collectOutputMatch(
				ctx,
				profile,
				ws,
				"/"+strings.TrimPrefix(match, "/"),
				spec,
				maxFileBytes,
				leftTotal,
			)
			if err != nil {
				return codeexecutor.OutputManifest{}, err
			}
			if skip {
				continue
			}
			if ref.Truncated {
				out.LimitsHit = true
			}
			leftTotal -= consumed
			count++
			if ref.SavedAs != "" {
				savedNames = append(savedNames, ref.SavedAs)
				savedVersions = append(savedVersions, ref.Version)
			}
			out.Files = append(out.Files, ref)
		}
	}
	if err := codeexecutor.WithWorkspaceMetadataLock(
		ctx,
		ws.Path,
		func(context.Context) error {
			md, err := codeexecutor.LoadMetadata(ws.Path)
			if err != nil {
				return fmt.Errorf("load workspace metadata: %w", err)
			}
			md.Outputs = append(md.Outputs, codeexecutor.OutputRecord{
				Globs:     spec.Globs,
				SavedAs:   savedNames,
				Versions:  savedVersions,
				LimitsHit: out.LimitsHit,
				Timestamp: time.Now(),
			})
			if err := codeexecutor.SaveMetadata(ws.Path, md); err != nil {
				return fmt.Errorf("save workspace metadata: %w", err)
			}
			return nil
		},
	); err != nil {
		return out, fmt.Errorf(
			"%w: %w",
			codeexecutor.ErrPartialOutputCommit,
			err,
		)
	}
	return out, nil
}

func (r *Runtime) collectOutputMatch(
	ctx context.Context,
	profile PermissionProfile,
	ws codeexecutor.Workspace,
	absPath string,
	spec codeexecutor.OutputSpec,
	maxFileBytes int64,
	leftTotal int64,
) (codeexecutor.FileRef, int64, bool, error) {
	rootAbs, err := filepath.Abs(ws.Path)
	if err != nil {
		return codeexecutor.FileRef{}, 0, false, err
	}
	absPath, err = filepath.Abs(absPath)
	if err != nil {
		return codeexecutor.FileRef{}, 0, false, err
	}
	if !sameOrChild(rootAbs, absPath) {
		return codeexecutor.FileRef{}, 0, true, nil
	}
	rel, err := filepath.Rel(rootAbs, absPath)
	if err != nil {
		return codeexecutor.FileRef{}, 0, false, err
	}
	rel = filepath.ToSlash(rel)
	if err := r.checkRead(profile, ws, rel); err != nil {
		return codeexecutor.FileRef{}, 0, false, err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return codeexecutor.FileRef{}, 0, false, err
	}
	if info.IsDir() {
		return codeexecutor.FileRef{}, 0, true, nil
	}
	limit := maxFileBytes
	if leftTotal < limit {
		limit = leftTotal
	}
	if limit < 0 {
		limit = 0
	}
	data, truncated, err := readFileLimited(absPath, int(limit))
	if err != nil {
		return codeexecutor.FileRef{}, 0, false, err
	}
	if info.Size() > int64(len(data)) {
		truncated = true
	}
	if truncated && spec.Save {
		return codeexecutor.FileRef{}, 0, false, fmt.Errorf(
			"cannot save truncated output file: %s",
			rel,
		)
	}
	ref := codeexecutor.FileRef{
		Name:      rel,
		MIMEType:  fileMIMEType(absPath),
		SizeBytes: info.Size(),
		Truncated: truncated,
	}
	if spec.Inline {
		ref.Content = string(data)
	}
	if spec.Save {
		saveName := rel
		if spec.NameTemplate != "" {
			saveName = spec.NameTemplate + rel
		}
		ver, err := codeexecutor.SaveArtifactHelper(ctx, saveName, data, ref.MIMEType)
		if err != nil {
			return codeexecutor.FileRef{}, 0, false, err
		}
		ref.SavedAs = saveName
		ref.Version = ver
	}
	return ref, int64(len(data)), false, nil
}

func pinnedArtifactVersion(
	md codeexecutor.WorkspaceMetadata,
	name string,
	to string,
) *int {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(to) == "" {
		return nil
	}
	for i := len(md.Inputs) - 1; i >= 0; i-- {
		rec := md.Inputs[i]
		if rec.To != to || rec.Version == nil {
			continue
		}
		if rec.Resolved == name {
			return rec.Version
		}
		if !strings.HasPrefix(rec.From, inputSchemeArtifact) {
			continue
		}
		ref := strings.TrimPrefix(rec.From, inputSchemeArtifact)
		rname, _, err := codeexecutor.ParseArtifactRef(ref)
		if err == nil && rname == name {
			return rec.Version
		}
	}
	return nil
}

func pathHasRule(profile PermissionProfile, target string, access FileSystemAccess) bool {
	for _, rule := range profile.FileSystem.Rules {
		if rule.Kind != RulePath {
			continue
		}
		if rule.Access != access && rule.Access != AccessWrite {
			continue
		}
		if rule.Path == target {
			return true
		}
		if filepath.IsAbs(rule.Path) && filepath.IsAbs(target) {
			ruleAbs, _ := filepath.Abs(rule.Path)
			targetAbs, _ := filepath.Abs(target)
			if sameOrChild(ruleAbs, targetAbs) {
				return true
			}
		}
	}
	return false
}

func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDir(src, dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return copyFile(src, dst, info.Mode())
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func readFileLimited(path string, max int) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	lr := io.LimitReader(f, int64(max)+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, false, err
	}
	if len(data) > max {
		return data[:max], true, nil
	}
	return data, false, nil
}

func inputName(ref string) string {
	ref = strings.TrimSuffix(ref, "/")
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "input"
	}
	if strings.HasPrefix(ref, inputSchemeArtifact) {
		name := strings.TrimPrefix(ref, inputSchemeArtifact)
		if parsed, _, err := codeexecutor.ParseArtifactRef(name); err == nil {
			ref = parsed
		}
	}
	name := filepath.Base(ref)
	if name == "." || name == string(filepath.Separator) {
		return "input"
	}
	return sanitizeID(name)
}

func fileMIMEType(path string) string {
	mt := mime.TypeByExtension(filepath.Ext(path))
	if mt == "" {
		return "application/octet-stream"
	}
	return mt
}
