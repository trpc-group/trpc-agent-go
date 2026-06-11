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
	return []string{".git", ".agents", ".trpc-agent-sandbox"}
}

type pathDecision struct {
	rel       string
	abs       string
	access    fileSystemAccess
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
	d.protected = isProtectedRel(rel, profile.fileSystem.ProtectedMetadata)
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
) (fileSystemAccess, bool, error) {
	var best fileSystemAccess
	bestSpecificity := -1
	bestRank := -1
	for _, rule := range profile.fileSystem.Rules {
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

func accessPrecedence(access fileSystemAccess) int {
	switch access {
	case accessNone:
		return 3
	case accessWrite:
		return 2
	case accessRead:
		return 1
	default:
		return 0
	}
}

func accessCanRead(access fileSystemAccess) bool {
	return access == accessRead || access == accessWrite
}

func accessCanWrite(access fileSystemAccess) bool {
	return access == accessWrite
}

func validateFileSystemRules(profile PermissionProfile) error {
	for _, rule := range profile.fileSystem.Rules {
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

func validFileSystemRuleShape(rule fileSystemRule) bool {
	switch rule.Access {
	case accessRead, accessWrite:
		return rule.Kind == rulePath || rule.Kind == ruleSpecial
	case accessNone:
		return rule.Kind == rulePath || rule.Kind == ruleSpecial || rule.Kind == ruleGlob
	default:
		return false
	}
}

func ruleTarget(rule fileSystemRule) string {
	switch rule.Kind {
	case rulePath:
		return rule.Path
	case ruleGlob:
		return rule.Glob
	case ruleSpecial:
		return string(rule.Special)
	default:
		return fmt.Sprintf("kind=%s", rule.Kind)
	}
}

func ruleSpecificity(ws codeexecutor.Workspace, rule fileSystemRule) (int, error) {
	switch rule.Kind {
	case ruleSpecial:
		rel, ok := specialRel(rule.Special)
		if !ok {
			return 0, nil
		}
		return pathSpecificity(rel), nil
	case ruleGlob:
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

func globMayMatchUnder(pattern string, prefix string) bool {
	patternParts := splitCleanRel(pattern)
	prefixParts := splitCleanRel(prefix)
	if len(prefixParts) == 0 {
		return true
	}
	type state struct {
		pattern int
		prefix  int
	}
	seen := map[state]bool{}
	var matchPrefix func(patternIdx, prefixIdx int) bool
	matchPrefix = func(patternIdx, prefixIdx int) bool {
		if prefixIdx == len(prefixParts) {
			return true
		}
		if patternIdx == len(patternParts) {
			return false
		}
		st := state{pattern: patternIdx, prefix: prefixIdx}
		if seen[st] {
			return false
		}
		seen[st] = true
		part := patternParts[patternIdx]
		if part == "**" {
			return matchPrefix(patternIdx+1, prefixIdx) ||
				matchPrefix(patternIdx, prefixIdx+1)
		}
		ok, err := ds.Match(part, prefixParts[prefixIdx])
		if err != nil || !ok {
			return false
		}
		return matchPrefix(patternIdx+1, prefixIdx+1)
	}
	return matchPrefix(0, 0)
}

func splitCleanRel(path string) []string {
	path = strings.Trim(filepath.ToSlash(filepath.Clean(path)), "/")
	if path == "" || path == "." {
		return nil
	}
	return strings.Split(path, "/")
}

func (r *Runtime) matchRule(
	ws codeexecutor.Workspace,
	rel string,
	abs string,
	rule fileSystemRule,
) (bool, error) {
	switch rule.Kind {
	case ruleSpecial:
		return matchSpecial(ws, abs, rule.Special)
	case ruleGlob:
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

func matchSpecial(ws codeexecutor.Workspace, abs string, special specialPath) (bool, error) {
	target, ok, err := specialPathAbs(ws, special)
	if err != nil || !ok {
		return false, err
	}
	return sameOrChild(target, abs), nil
}

func specialPathAbs(ws codeexecutor.Workspace, special specialPath) (string, bool, error) {
	var target string
	switch special {
	case specialRoot:
		target = ws.Path
	case specialWorkspace:
		target = ws.Path
	case specialWork:
		target = filepath.Join(ws.Path, codeexecutor.DirWork)
	case specialHome:
		target = filepath.Join(ws.Path, "home")
	case specialTmp:
		target = filepath.Join(ws.Path, "tmp")
	case specialRuns:
		target = filepath.Join(ws.Path, codeexecutor.DirRuns)
	case specialOut:
		target = filepath.Join(ws.Path, codeexecutor.DirOut)
	case specialSkills:
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

func specialRel(special specialPath) (string, bool) {
	switch special {
	case specialRoot, specialWorkspace:
		return ".", true
	case specialWork:
		return codeexecutor.DirWork, true
	case specialHome:
		return "home", true
	case specialTmp:
		return "tmp", true
	case specialRuns:
		return codeexecutor.DirRuns, true
	case specialOut:
		return codeexecutor.DirOut, true
	case specialSkills:
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
	}
	return false
}

func (r *Runtime) checkRead(profile PermissionProfile, ws codeexecutor.Workspace, rel string) error {
	if profile.enforcement() == enforcementDisabled {
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
	if profile.enforcement() == enforcementDisabled {
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
	for _, rule := range profile.fileSystem.Rules {
		if rule.Access != accessNone {
			continue
		}
		switch rule.Kind {
		case rulePath:
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
		case ruleSpecial:
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
		case ruleGlob:
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
