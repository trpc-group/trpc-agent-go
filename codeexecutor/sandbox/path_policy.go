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
	"os"
	"path/filepath"
	"strings"

	ds "github.com/bmatcuk/doublestar/v4"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func defaultProtectedMetadata() []string {
	return []string{".git", ".agents", ".codex", ".trpc-agent-sandbox"}
}

type pathDecision struct {
	rel        string
	abs        string
	canRead    bool
	canWrite   bool
	readDenied bool
	protected  bool
}

func (r *Runtime) resolveWorkspacePath(
	ws codeexecutor.Workspace,
	path string,
) (string, string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	clean := filepath.Clean(path)
	rootAbs, err := filepath.Abs(ws.Path)
	if err != nil {
		return "", "", err
	}
	if filepath.IsAbs(clean) {
		abs, err := filepath.Abs(clean)
		if err != nil {
			return "", "", err
		}
		if !sameOrChild(rootAbs, abs) {
			return "", "", deniedf(
				ErrPathDenied, "path", path,
				"absolute path escapes workspace",
			)
		}
		rel, err := filepath.Rel(rootAbs, abs)
		if err != nil {
			return "", "", err
		}
		clean = rel
	}
	clean = filepath.Clean(clean)
	if clean == "." {
		return rootAbs, ".", nil
	}
	if strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || clean == ".." {
		return "", "", deniedf(
			ErrPathDenied, "path", path,
			"relative path escapes workspace",
		)
	}
	abs := filepath.Join(rootAbs, clean)
	if err := ensureNoSymlinkEscape(rootAbs, abs); err != nil {
		return "", "", err
	}
	return abs, filepath.ToSlash(clean), nil
}

func ensureNoSymlinkEscape(rootAbs, abs string) error {
	rootEval, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		rootEval = rootAbs
	}
	rootEval, _ = filepath.Abs(rootEval)
	cur := rootAbs
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil {
		return err
	}
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				break
			}
			return err
		}
		if st.Mode()&os.ModeSymlink == 0 {
			continue
		}
		eval, err := filepath.EvalSymlinks(cur)
		if err != nil {
			return err
		}
		eval, _ = filepath.Abs(eval)
		if !sameOrChild(rootEval, eval) {
			return deniedf(
				ErrPathDenied, "path", cur,
				"symlink escapes workspace",
			)
		}
	}
	return nil
}

func sameOrChild(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return true
	}
	if parent == string(os.PathSeparator) {
		return strings.HasPrefix(child, parent)
	}
	return strings.HasPrefix(child, parent+string(os.PathSeparator))
}

func (r *Runtime) decidePath(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
	rel string,
) (pathDecision, error) {
	abs, rel, err := r.resolveWorkspacePath(ws, rel)
	if err != nil {
		return pathDecision{}, err
	}
	d := pathDecision{rel: rel, abs: abs}
	d.protected = isProtectedRel(rel, profile.FileSystem.ProtectedMetadata)
	for _, rule := range profile.FileSystem.Rules {
		matched, err := r.matchRule(ws, rel, abs, rule)
		if err != nil {
			return pathDecision{}, err
		}
		if !matched {
			continue
		}
		switch rule.Access {
		case AccessRead:
			d.canRead = true
		case AccessWrite:
			d.canRead = true
			d.canWrite = true
		case AccessDenyRead, AccessDenyReadGlob:
			d.readDenied = true
		}
	}
	if d.readDenied {
		d.canRead = false
	}
	if d.protected {
		d.canWrite = false
	}
	return d, nil
}

func (r *Runtime) matchRule(
	ws codeexecutor.Workspace,
	rel string,
	abs string,
	rule FileSystemRule,
) (bool, error) {
	switch rule.Kind {
	case RuleSpecial:
		return matchSpecial(ws, abs, rule.Special)
	case RuleGlob:
		ok, err := ds.Match(filepath.ToSlash(rule.Glob), filepath.ToSlash(rel))
		return ok, err
	default:
		target := strings.TrimSpace(rule.Path)
		if target == "" {
			return false, nil
		}
		if filepath.IsAbs(target) {
			targetAbs, err := filepath.Abs(target)
			if err != nil {
				return false, err
			}
			return sameOrChild(targetAbs, abs), nil
		}
		target = filepath.ToSlash(filepath.Clean(target))
		return rel == target || strings.HasPrefix(rel, target+"/"), nil
	}
}

func matchSpecial(ws codeexecutor.Workspace, abs string, special SpecialPath) (bool, error) {
	var target string
	switch special {
	case SpecialRoot:
		return true, nil
	case SpecialWorkspace:
		target = ws.Path
	case SpecialWork:
		target = filepath.Join(ws.Path, codeexecutor.DirWork)
	case SpecialHome:
		target = filepath.Join(ws.Path, "home")
	case SpecialTmp:
		target = filepath.Join(ws.Path, "tmp")
	case SpecialRuns:
		target = filepath.Join(ws.Path, codeexecutor.DirRuns)
	case SpecialOut:
		target = filepath.Join(ws.Path, codeexecutor.DirOut)
	case SpecialSkills:
		target = filepath.Join(ws.Path, codeexecutor.DirSkills)
	default:
		return false, nil
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false, err
	}
	return sameOrChild(targetAbs, abs), nil
}

func isProtectedRel(rel string, protected []string) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." {
		return false
	}
	for _, p := range protected {
		p = strings.Trim(filepath.ToSlash(filepath.Clean(p)), "/")
		if p == "" || p == "." {
			continue
		}
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
		if !strings.Contains(p, "/") {
			first := rel
			if i := strings.IndexByte(first, '/'); i >= 0 {
				first = first[:i]
			}
			if first == p {
				return true
			}
		}
	}
	return false
}

func (r *Runtime) checkRead(profile PermissionProfile, ws codeexecutor.Workspace, rel string) error {
	if profile.Enforcement() == EnforcementDisabled {
		return nil
	}
	d, err := r.decidePath(profile, ws, rel)
	if err != nil {
		return err
	}
	if !d.canRead {
		return deniedf(ErrPathDenied, "read", rel, "read denied")
	}
	return nil
}

func (r *Runtime) checkWrite(profile PermissionProfile, ws codeexecutor.Workspace, rel string) error {
	if profile.Enforcement() == EnforcementDisabled {
		return nil
	}
	d, err := r.decidePath(profile, ws, rel)
	if err != nil {
		return err
	}
	if d.protected {
		return deniedf(ErrPathDenied, "write", rel, "protected metadata path")
	}
	if !d.canWrite {
		return deniedf(ErrPathDenied, "write", rel, "write denied")
	}
	return nil
}

func (r *Runtime) deniedReadMatches(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) ([]string, error) {
	var matches []string
	for _, rule := range profile.FileSystem.Rules {
		switch rule.Access {
		case AccessDenyRead:
			if rule.Kind != RulePath || rule.Path == "" {
				continue
			}
			abs := rule.Path
			if !filepath.IsAbs(abs) {
				resolved, _, err := r.resolveWorkspacePath(ws, rule.Path)
				if err != nil {
					return nil, err
				}
				abs = resolved
			}
			if _, err := os.Stat(abs); err == nil {
				matches = append(matches, abs)
			}
		case AccessDenyReadGlob:
			if rule.Glob == "" {
				continue
			}
			pattern := strings.TrimPrefix(
				filepath.ToSlash(filepath.Join(ws.Path, rule.Glob)), "/",
			)
			globMatches, err := ds.Glob(os.DirFS("/"), pattern)
			if err != nil {
				return nil, err
			}
			for _, m := range globMatches {
				matches = append(matches, "/"+strings.TrimPrefix(m, "/"))
			}
		}
	}
	return matches, nil
}
