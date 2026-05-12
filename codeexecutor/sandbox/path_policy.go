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
	"fmt"
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
	rel       string
	abs       string
	access    FileSystemAccess
	matched   bool
	protected bool
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
	if err := validateFileSystemRules(profile); err != nil {
		return pathDecision{}, err
	}
	abs, rel, err := r.resolveWorkspacePath(ws, rel)
	if err != nil {
		return pathDecision{}, err
	}
	d := pathDecision{rel: rel, abs: abs}
	d.protected = isProtectedRel(rel, profile.FileSystem.ProtectedMetadata)
	access, matched, err := r.resolveAccess(profile, ws, rel, abs)
	if err != nil {
		return pathDecision{}, err
	}
	d.access = access
	d.matched = matched
	return d, nil
}

func (r *Runtime) resolveAccess(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
	rel string,
	abs string,
) (FileSystemAccess, bool, error) {
	var best FileSystemAccess
	bestSpecificity := -1
	bestRank := -1
	for _, rule := range profile.FileSystem.Rules {
		matched, err := r.matchRule(ws, rel, abs, rule)
		if err != nil {
			return "", false, err
		}
		if !matched {
			continue
		}
		specificity, err := ruleSpecificity(ws, rule)
		if err != nil {
			return "", false, err
		}
		rank := accessPrecedence(rule.Access)
		if specificity > bestSpecificity ||
			(specificity == bestSpecificity && rank > bestRank) {
			best = rule.Access
			bestSpecificity = specificity
			bestRank = rank
		}
	}
	return best, bestSpecificity >= 0, nil
}

func accessPrecedence(access FileSystemAccess) int {
	switch access {
	case AccessNone:
		return 3
	case AccessWrite:
		return 2
	case AccessRead:
		return 1
	default:
		return 0
	}
}

func accessCanRead(access FileSystemAccess) bool {
	return access == AccessRead || access == AccessWrite
}

func accessCanWrite(access FileSystemAccess) bool {
	return access == AccessWrite
}

func validateFileSystemRules(profile PermissionProfile) error {
	for _, rule := range profile.FileSystem.Rules {
		if !validFileSystemRuleShape(rule) {
			return deniedf(
				ErrPolicyViolation,
				"filesystem-rule",
				ruleTarget(rule),
				"unsupported access/kind combination: access=%s kind=%s",
				rule.Access,
				rule.Kind,
			)
		}
	}
	return nil
}

func validFileSystemRuleShape(rule FileSystemRule) bool {
	switch rule.Access {
	case AccessRead, AccessWrite:
		return rule.Kind == RulePath || rule.Kind == RuleSpecial
	case AccessNone:
		return rule.Kind == RulePath || rule.Kind == RuleSpecial || rule.Kind == RuleGlob
	default:
		return false
	}
}

func ruleTarget(rule FileSystemRule) string {
	switch rule.Kind {
	case RulePath:
		return rule.Path
	case RuleGlob:
		return rule.Glob
	case RuleSpecial:
		return string(rule.Special)
	default:
		return fmt.Sprintf("kind=%s", rule.Kind)
	}
}

func ruleSpecificity(ws codeexecutor.Workspace, rule FileSystemRule) (int, error) {
	switch rule.Kind {
	case RuleSpecial:
		rel, ok := specialRel(rule.Special)
		if !ok {
			return 0, nil
		}
		return pathSpecificity(rel), nil
	case RuleGlob:
		return pathSpecificity(rule.Glob), nil
	default:
		target := strings.TrimSpace(rule.Path)
		if target == "" {
			return 0, nil
		}
		if filepath.IsAbs(target) {
			rootAbs, err := filepath.Abs(ws.Path)
			if err != nil {
				return 0, err
			}
			targetAbs, err := filepath.Abs(target)
			if err != nil {
				return 0, err
			}
			if rel, err := filepath.Rel(rootAbs, targetAbs); err == nil &&
				!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) &&
				rel != ".." {
				target = rel
			}
		}
		return pathSpecificity(target), nil
	}
}

func pathSpecificity(path string) int {
	path = strings.Trim(filepath.ToSlash(filepath.Clean(path)), "/")
	if path == "" || path == "." {
		return 0
	}
	return len(strings.Split(path, "/"))
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
	target, ok, err := specialPathAbs(ws, special)
	if err != nil || !ok {
		return false, err
	}
	return sameOrChild(target, abs), nil
}

func specialPathAbs(ws codeexecutor.Workspace, special SpecialPath) (string, bool, error) {
	var target string
	switch special {
	case SpecialRoot:
		target = ws.Path
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
		return "", false, nil
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return "", false, err
	}
	return targetAbs, true, nil
}

func specialRel(special SpecialPath) (string, bool) {
	switch special {
	case SpecialRoot, SpecialWorkspace:
		return ".", true
	case SpecialWork:
		return codeexecutor.DirWork, true
	case SpecialHome:
		return "home", true
	case SpecialTmp:
		return "tmp", true
	case SpecialRuns:
		return codeexecutor.DirRuns, true
	case SpecialOut:
		return codeexecutor.DirOut, true
	case SpecialSkills:
		return codeexecutor.DirSkills, true
	default:
		return "", false
	}
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
	if !accessCanRead(d.access) {
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
	if !accessCanWrite(d.access) {
		return deniedf(ErrPathDenied, "write", rel, "write denied")
	}
	return nil
}

func (r *Runtime) deniedReadMatches(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) ([]string, error) {
	if err := validateFileSystemRules(profile); err != nil {
		return nil, err
	}
	var matches []string
	for _, rule := range profile.FileSystem.Rules {
		if rule.Access != AccessNone {
			continue
		}
		switch rule.Kind {
		case RulePath:
			if rule.Path == "" {
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
		case RuleSpecial:
			abs, ok, err := specialPathAbs(ws, rule.Special)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if _, err := os.Stat(abs); err == nil {
				matches = append(matches, abs)
			}
		case RuleGlob:
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
