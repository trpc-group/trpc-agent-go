//go:build darwin

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
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const macosBaseSeatbeltPolicy = `(version 1)

; Start closed-by-default. Runtime and platform allowances are appended below.
(deny default)

; Child processes inherit this sandbox.
(allow process-exec)
(allow process-fork)
(allow signal (target same-sandbox))
(allow process-info* (target same-sandbox))

; Common runtime probes used by shells, Go, Python, Java, and system libraries.
(allow sysctl-read)
(allow sysctl-write (sysctl-name "kern.grade_cputype"))
(allow iokit-open (iokit-registry-entry-class "RootDomainUserClient"))

; Basic user, preferences, power, and logging services commonly touched at startup.
(allow mach-lookup
  (global-name "com.apple.PowerManagement.control")
  (global-name "com.apple.bsd.dirhelper")
  (global-name "com.apple.cfprefsd.agent")
  (global-name "com.apple.cfprefsd.daemon")
  (global-name "com.apple.system.DirectoryService.libinfo_v1")
  (global-name "com.apple.system.opendirectoryd.libinfo")
  (global-name "com.apple.system.opendirectoryd.membership")
  (local-name "com.apple.cfprefsd.agent"))
(allow user-preference-read)
(allow ipc-posix-sem)
(allow ipc-posix-shm-read* (ipc-posix-name-prefix "apple.cfprefs."))

; Terminal and stdio basics.
(allow pseudo-tty)
(allow file-read* file-write* file-ioctl
  (literal "/dev/null")
  (literal "/dev/ptmx")
  (literal "/dev/random")
  (literal "/dev/tty")
  (literal "/dev/urandom")
  (literal "/dev/zero"))
(allow file-read* file-write* (regex #"^/dev/fd/[0-9]+$"))
(allow file-read* file-write* (regex #"^/dev/ttys[0-9]+$"))
(allow file-read-metadata
  (literal "/dev")
  (literal "/dev/fd")
  (literal "/dev/stdin")
  (literal "/dev/stdout")
  (literal "/dev/stderr")
  (subpath "/dev"))`

type macosAccessRoot struct {
	path       string
	exclusions []string
}

func (r *Runtime) macosSeatbeltProfile(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) (string, error) {
	if err := validateFileSystemRules(profile); err != nil {
		return "", err
	}
	noAccessRoots, err := r.macosNoAccessRoots(profile, ws)
	if err != nil {
		return "", err
	}
	protectedRoots, err := macosProtectedRoots(profile, ws)
	if err != nil {
		return "", err
	}
	readRoots, writeRoots, err := r.macosReadWriteRoots(profile, ws)
	if err != nil {
		return "", err
	}
	explicitReadRoots := append([]string{}, readRoots...)
	platformRoots := macosPlatformDefaultReadRoots()
	readRoots = append(readRoots, writeRoots...)
	readRoots = append(readRoots, platformRoots...)
	readPolicy := macosSeatbeltAccessPolicy(
		"file-read* file-map-executable file-test-existence",
		macosAccessRoots(readRoots, noAccessRoots, nil),
	)
	writeExclusions := append([]string{}, noAccessRoots...)
	writeExclusions = append(writeExclusions, macosStrictChildRoots(writeRoots, explicitReadRoots)...)
	writeExclusions = append(writeExclusions, protectedRoots...)
	writePolicy := macosSeatbeltAccessPolicy(
		"file-write*",
		macosAccessRoots(writeRoots, writeExclusions, protectedRoots),
	)
	globPolicy, err := r.macosNoAccessGlobPolicy(profile, ws)
	if err != nil {
		return "", err
	}
	networkPolicy, err := macosSeatbeltNetworkPolicy(profile.network, profile.macOS)
	if err != nil {
		return "", err
	}
	sections := []string{
		macosBaseSeatbeltPolicy,
		macosPlatformRootLiteralPolicy,
		macosPlatformAliasPolicy,
		macosPlatformTempMetadataPolicy,
		"; allow read-only file operations",
		readPolicy,
		"; allow writable file operations",
		writePolicy,
		"; deny glob-matched no-access paths",
		globPolicy,
		networkPolicy,
	}
	return strings.Join(nonEmptySections(sections), "\n\n"), nil
}

func macosPreflightPolicy() string {
	readPolicy := macosSeatbeltAccessPolicy(
		"file-read* file-map-executable file-test-existence",
		macosAccessRoots(macosPlatformDefaultReadRoots(), nil, nil),
	)
	return strings.Join(nonEmptySections([]string{
		macosBaseSeatbeltPolicy,
		macosPlatformRootLiteralPolicy,
		macosPlatformAliasPolicy,
		macosPlatformTempMetadataPolicy,
		readPolicy,
	}), "\n\n")
}

const macosPlatformRootLiteralPolicy = `; Allow processes to read the filesystem root itself for getcwd/path resolution.
(allow file-read* file-test-existence (literal "/"))`

const macosPlatformAliasPolicy = `; Preserve common macOS symlink spellings used by system shims such as xcode-select.
(allow file-read* file-map-executable file-test-existence
  (literal "/var")
  (literal "/var/select")
  (subpath "/var/select"))
(allow file-read-metadata file-test-existence
  (path-ancestors "/Library/Developer/CommandLineTools/Library/Frameworks/Python3.framework/Versions/3.9/bin"))`

const macosPlatformTempMetadataPolicy = `; Allow ancestor metadata for default temp path probes without granting host temp file reads.
; Runtime injects TMPDIR/TMP/TEMP into the workspace tmp directory.
(allow file-read-metadata file-test-existence
  (path-ancestors "/tmp")
  (path-ancestors "/private/tmp")
  (path-ancestors "/var/tmp")
  (path-ancestors "/private/var/tmp")
  (path-ancestors "/var/folders")
  (path-ancestors "/private/var/folders"))`

func (r *Runtime) macosReadWriteRoots(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) ([]string, []string, error) {
	var readRoots []string
	var writeRoots []string
	for _, rule := range profile.fileSystem.Rules {
		if rule.Access != accessRead && rule.Access != accessWrite {
			continue
		}
		target, ok, err := r.macosRuleTarget(profile, ws, rule)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}
		switch rule.Access {
		case accessRead:
			readRoots = append(readRoots, target)
		case accessWrite:
			writeRoots = append(writeRoots, target)
		}
	}
	return dedupeCleanAbs(readRoots), dedupeCleanAbs(writeRoots), nil
}

func (r *Runtime) macosNoAccessRoots(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) ([]string, error) {
	var roots []string
	for _, rule := range profile.fileSystem.Rules {
		if rule.Access != accessNone || rule.Kind == ruleGlob {
			continue
		}
		target, ok, err := r.macosRuleTarget(profile, ws, rule)
		if err != nil {
			return nil, err
		}
		if ok {
			roots = append(roots, target)
		}
	}
	return dedupeCleanAbs(roots), nil
}

func (r *Runtime) macosRuleTarget(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
	rule fileSystemRule,
) (string, bool, error) {
	_ = profile
	switch rule.Kind {
	case rulePath:
		if rule.Path == "" {
			return "", false, nil
		}
		if filepath.IsAbs(rule.Path) {
			target, err := filepath.Abs(rule.Path)
			if err != nil {
				return "", false, err
			}
			wsAbs, err := filepath.Abs(ws.Path)
			if err != nil {
				return "", false, err
			}
			if sameOrChild(wsAbs, target) {
				rel, err := filepath.Rel(wsAbs, target)
				if err != nil {
					return "", false, err
				}
				resolved, _, err := r.resolveWorkspacePath(ws, rel)
				return resolved, err == nil, err
			}
			if rule.Access != accessNone {
				if _, err := filepath.EvalSymlinks(target); err != nil {
					return "", false, deniedf(
						ErrPathDenied,
						"grant",
						target,
						"grant target unavailable",
					)
				}
			}
			return target, true, nil
		}
		target, _, err := r.resolveWorkspacePath(ws, rule.Path)
		return target, err == nil, err
	case ruleSpecial:
		target, ok, err := specialPathAbs(ws, rule.Special)
		if err != nil || !ok {
			return "", ok, err
		}
		wsAbs, err := filepath.Abs(ws.Path)
		if err != nil {
			return "", false, err
		}
		if err := ensureNoSymlinkEscape(wsAbs, target); err != nil {
			return "", false, err
		}
		return target, true, nil
	default:
		return "", false, nil
	}
}

func macosProtectedRoots(profile PermissionProfile, ws codeexecutor.Workspace) ([]string, error) {
	var roots []string
	wsAbs, err := filepath.Abs(ws.Path)
	if err != nil {
		return nil, err
	}
	for _, rel := range profile.fileSystem.ProtectedMetadata {
		rel = strings.Trim(filepath.ToSlash(filepath.Clean(rel)), "/")
		if rel == "" || rel == "." {
			continue
		}
		if strings.HasPrefix(rel, "../") {
			return nil, deniedf(ErrPathDenied, "protect", rel, "protected path escapes workspace")
		}
		roots = append(roots, filepath.Join(wsAbs, filepath.FromSlash(rel)))
	}
	return dedupeCleanAbs(roots), nil
}

func macosAccessRoots(roots []string, exclusions []string, hardDeniedRoots []string) []macosAccessRoot {
	var accessRoots []macosAccessRoot
	for _, root := range dedupeCleanAbs(roots) {
		if macosRootUnderAny(root, hardDeniedRoots) {
			continue
		}
		var rootExclusions []string
		for _, exclusion := range exclusions {
			if sameOrChild(root, exclusion) {
				rootExclusions = append(rootExclusions, exclusion)
			}
		}
		accessRoots = append(accessRoots, macosAccessRoot{
			path:       root,
			exclusions: dedupeCleanAbs(rootExclusions),
		})
	}
	return accessRoots
}

func macosSeatbeltAccessPolicy(operations string, roots []macosAccessRoot) string {
	if len(roots) == 0 {
		return ""
	}
	var filters []string
	for _, accessRoot := range roots {
		root := macosSandboxPath(accessRoot.path)
		if len(accessRoot.exclusions) == 0 {
			filters = append(filters,
				fmt.Sprintf("(literal %s)", sbplString(root)),
				fmt.Sprintf("(subpath %s)", sbplString(root)),
			)
			continue
		}
		var requireNot []string
		for _, exclusion := range accessRoot.exclusions {
			exclusion = macosSandboxPath(exclusion)
			requireNot = append(requireNot,
				fmt.Sprintf("(require-not (literal %s))", sbplString(exclusion)),
				fmt.Sprintf("(require-not (subpath %s))", sbplString(exclusion)),
			)
		}
		requireNotPart := strings.Join(requireNot, " ")
		filters = append(filters,
			fmt.Sprintf("(require-all (literal %s) %s)", sbplString(root), requireNotPart),
			fmt.Sprintf("(require-all (subpath %s) %s)", sbplString(root), requireNotPart),
		)
	}
	return fmt.Sprintf("(allow %s\n  %s)", operations, strings.Join(filters, "\n  "))
}

func (r *Runtime) macosNoAccessGlobPolicy(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) (string, error) {
	// macOS glob no-access rules are hard Seatbelt denials, matching
	// Codex's macOS behavior. More-specific read/write grants do not reopen
	// glob-matched paths; use exact no-access paths when carveouts are needed.
	var rules []string
	for _, rule := range profile.fileSystem.Rules {
		if rule.Access != accessNone || rule.Kind != ruleGlob {
			continue
		}
		regex, ok, err := macosSeatbeltRegexForWorkspaceGlob(ws, rule.Glob)
		if err != nil {
			return "", err
		}
		if !ok {
			continue
		}
		regex = strings.ReplaceAll(regex, `"`, `\"`)
		rules = append(rules,
			fmt.Sprintf(`(deny file-read* file-map-executable file-test-existence (regex #"%s"))`, regex),
			fmt.Sprintf(`(deny file-write* (regex #"%s"))`, regex),
		)
	}
	return strings.Join(rules, "\n"), nil
}

func macosSeatbeltRegexForWorkspaceGlob(
	ws codeexecutor.Workspace,
	pattern string,
) (string, bool, error) {
	glob := filepath.ToSlash(filepath.Clean(strings.TrimSpace(pattern)))
	if glob == "" || glob == "." {
		return "", false, nil
	}
	if filepath.IsAbs(glob) || strings.HasPrefix(glob, "../") {
		return "", false, deniedf(
			ErrPolicyViolation,
			"no-access-glob",
			pattern,
			"macOS backend requires workspace-relative glob denials",
		)
	}
	wsAbs, err := filepath.Abs(ws.Path)
	if err != nil {
		return "", false, err
	}
	wsAbs, err = canonicalizeExistingPath(wsAbs)
	if err != nil {
		return "", false, err
	}
	absolutePattern := filepath.ToSlash(filepath.Join(wsAbs, filepath.FromSlash(glob)))
	regex, err := macosGlobPatternToRegex(absolutePattern)
	if err != nil {
		return "", false, deniedf(
			ErrPolicyViolation,
			"no-access-glob",
			pattern,
			"invalid glob pattern: %v",
			err,
		)
	}
	return regex, true, nil
}

func macosGlobPatternToRegex(pattern string) (string, error) {
	var b strings.Builder
	b.WriteString("^")
	sawGlob := false
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			sawGlob = true
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(.*/)?")
				} else {
					b.WriteString(".*")
				}
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			sawGlob = true
			b.WriteString("[^/]")
		case '[':
			sawGlob = true
			end := i + 1
			for end < len(pattern) && pattern[end] != ']' {
				end++
			}
			if end >= len(pattern) {
				return "", fmt.Errorf("unclosed character class")
			}
			class := pattern[i+1 : end]
			if class == "" {
				return "", fmt.Errorf("empty character class")
			}
			b.WriteByte('[')
			if class[0] == '!' {
				b.WriteByte('^')
				class = class[1:]
			} else if class[0] == '^' {
				b.WriteString(`\^`)
				class = class[1:]
			}
			for j := 0; j < len(class); j++ {
				if class[j] == '\\' {
					b.WriteString(`\\`)
					continue
				}
				b.WriteByte(class[j])
			}
			b.WriteByte(']')
			i = end
		case ']':
			sawGlob = true
			b.WriteString(`\]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	if !sawGlob {
		b.WriteString("(/.*)?")
	}
	b.WriteString("$")
	return b.String(), nil
}

func macosSeatbeltNetworkPolicy(policy NetworkPolicy, macOS macOSProfilePolicy) (string, error) {
	var sections []string
	if policy.Mode == NetworkEnabled {
		sections = append(sections, `(allow network-outbound)
(allow network-inbound)
(allow system-socket)`, macosNetworkMachLookupPolicy())
	} else if macOS.allowSystemTrustServices {
		sections = append(sections, macosSystemTrustMachLookupPolicy())
	}
	unixSocketPolicy, err := macosUnixSocketPolicy(macOS.unixSocketPaths)
	if err != nil {
		return "", err
	}
	if unixSocketPolicy != "" {
		sections = append(sections, "; allow macOS Unix domain sockets for local IPC", unixSocketPolicy)
	}
	return strings.Join(nonEmptySections(sections), "\n"), nil
}

func macosNetworkMachLookupPolicy() string {
	return `(allow mach-lookup
  (global-name "com.apple.SecurityServer")
  (global-name "com.apple.SystemConfiguration.DNSConfiguration")
  (global-name "com.apple.SystemConfiguration.configd")
  (global-name "com.apple.networkd")
  (global-name "com.apple.ocspd")
  (global-name "com.apple.trustd")
  (global-name "com.apple.trustd.agent"))`
}

func macosSystemTrustMachLookupPolicy() string {
	return `(allow mach-lookup
  (global-name "com.apple.SecurityServer")
  (global-name "com.apple.ocspd")
  (global-name "com.apple.trustd")
  (global-name "com.apple.trustd.agent"))`
}

func macosUnixSocketPolicy(paths []string) (string, error) {
	roots, err := macosUnixSocketRoots(paths)
	if err != nil {
		return "", err
	}
	if len(roots) == 0 {
		return "", nil
	}
	var rules []string
	rules = append(rules, "(allow system-socket (socket-domain AF_UNIX))")
	for _, root := range roots {
		path := sbplString(root)
		rules = append(rules,
			fmt.Sprintf("(allow file-read* file-test-existence (literal %s))", path),
			fmt.Sprintf("(allow network-bind (literal %s))", path),
			fmt.Sprintf("(allow network-bind (path %s))", path),
			fmt.Sprintf("(allow network-bind (local unix-socket (path-literal %s)))", path),
			fmt.Sprintf("(allow network-outbound (literal %s))", path),
			fmt.Sprintf("(allow network-outbound (path %s))", path),
			fmt.Sprintf("(allow network-outbound (remote unix-socket (path-literal %s)))", path),
		)
	}
	return strings.Join(rules, "\n"), nil
}

func macosUnixSocketRoots(paths []string) ([]string, error) {
	seen := map[string]bool{}
	var roots []string
	addRoot := func(path string) {
		path = filepath.Clean(path)
		if seen[path] {
			return
		}
		seen[path] = true
		roots = append(roots, path)
	}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			return nil, deniedf(
				ErrPolicyViolation,
				"unix-socket",
				path,
				"macOS Unix socket paths must be absolute",
			)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		addRoot(abs)
		if canonical, err := canonicalizeExistingPath(abs); err == nil {
			addRoot(canonical)
		}
	}
	sort.Strings(roots)
	return roots, nil
}

func macosPlatformDefaultReadRoots() []string {
	return []string{
		"/Applications",
		"/Library/Apple",
		"/Library/Developer",
		"/Library/Developer/CommandLineTools",
		"/Library/Filesystems/NetFSPlugins",
		"/Library/Preferences",
		"/Library/Preferences/Logging",
		"/System/Library/CoreServices",
		"/System/Library/Frameworks",
		"/System/Library/PrivateFrameworks",
		"/System/Library/SubFrameworks",
		"/bin",
		"/etc",
		"/opt/homebrew/lib",
		"/private/etc",
		"/private/var/db",
		"/private/var/select",
		"/sbin",
		"/usr/bin",
		"/usr/lib",
		"/usr/libexec",
		"/usr/local/lib",
		"/usr/sbin",
		"/usr/share",
		"/var/db",
		"/var/select",
	}
}

func macosStrictChildRoots(roots []string, maybeChildren []string) []string {
	var children []string
	for _, root := range roots {
		for _, child := range maybeChildren {
			if root != child && sameOrChild(root, child) {
				children = append(children, child)
			}
		}
	}
	return dedupeCleanAbs(children)
}

func macosRootUnderAny(root string, parents []string) bool {
	for _, parent := range parents {
		if root != parent && sameOrChild(parent, root) {
			return true
		}
	}
	return false
}

func dedupeCleanAbs(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		if normalized, err := canonicalizeExistingPath(abs); err == nil {
			abs = normalized
		}
		abs = filepath.Clean(abs)
		if seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	sort.Strings(out)
	return out
}

func nonEmptySections(sections []string) []string {
	var out []string
	for _, section := range sections {
		if strings.TrimSpace(section) != "" {
			out = append(out, section)
		}
	}
	return out
}

func macosSandboxPath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

func sbplString(s string) string {
	return strconv.Quote(macosSandboxPath(s))
}
