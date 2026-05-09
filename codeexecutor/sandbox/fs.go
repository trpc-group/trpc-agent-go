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
	if profile.Enforcement() != EnforcementDisabled && !pathHasRule(profile, src, AccessRead) {
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
	profile := applyAdditionalPermissions(
		normalizeProfile(r.profile),
		additionalPermissionsFromContext(ctx),
	)
	for _, sp := range specs {
		to := strings.TrimSpace(sp.To)
		if to == "" {
			to = filepath.Join(codeexecutor.DirWork, "inputs", inputName(sp.From))
		}
		switch {
		case strings.HasPrefix(sp.From, inputSchemeHost):
			src := strings.TrimPrefix(sp.From, inputSchemeHost)
			if err := r.StageDirectory(ctx, ws, src, to, codeexecutor.StageOptions{}); err != nil {
				return err
			}
		case strings.HasPrefix(sp.From, inputSchemeWorkspace):
			srcRel := strings.TrimPrefix(sp.From, inputSchemeWorkspace)
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
			if err := copyPath(src, dst); err != nil {
				return err
			}
		case strings.HasPrefix(sp.From, inputSchemeSkill):
			rest := strings.TrimPrefix(sp.From, inputSchemeSkill)
			srcRel := filepath.Join(codeexecutor.DirSkills, filepath.Clean(rest))
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
			if err := copyPath(src, dst); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported input: %s", sp.From)
		}
	}
	return nil
}

// CollectOutputs applies the declarative output spec under the same read
// policy as Collect.
func (r *Runtime) CollectOutputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
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
	files, err := r.Collect(ctx, ws, spec.Globs)
	if err != nil {
		return codeexecutor.OutputManifest{}, err
	}
	out := codeexecutor.OutputManifest{}
	var total int64
	for _, f := range files {
		if len(out.Files) >= maxFiles {
			out.LimitsHit = true
			break
		}
		if f.SizeBytes > maxFileBytes || int64(len(f.Content)) > maxFileBytes {
			f.Content = f.Content[:minInt(len(f.Content), int(maxFileBytes))]
			f.Truncated = true
			out.LimitsHit = true
		}
		total += int64(len(f.Content))
		if total > maxTotalBytes {
			out.LimitsHit = true
			break
		}
		out.Files = append(out.Files, codeexecutor.FileRef{
			Name:      f.Name,
			MIMEType:  f.MIMEType,
			Content:   f.Content,
			SizeBytes: f.SizeBytes,
			Truncated: f.Truncated,
		})
	}
	md, _ := codeexecutor.LoadMetadata(ws.Path)
	md.Outputs = append(md.Outputs, codeexecutor.OutputRecord{
		Globs:     spec.Globs,
		LimitsHit: out.LimitsHit,
		Timestamp: time.Now(),
	})
	_ = codeexecutor.SaveMetadata(ws.Path, md)
	return out, nil
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
	name := filepath.Base(ref)
	if name == "." || name == string(filepath.Separator) {
		return "input"
	}
	return sanitizeID(name)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
