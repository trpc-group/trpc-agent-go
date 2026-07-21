//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// rulePath evaluates path denylist rules. It normalizes / separators and
// lexical ~ forms, applies denied_paths and denied_path_globs, and
// inspects only path-like arguments in known file operations. Built-in
// matches (ssh, aws, .env, ...) fire regardless of configuration.
//
// Rule ids:
//
//   - path.system_write         destructive write/delete to system paths.
//   - path.ssh_private_key      ~/.ssh, private-key basenames, authorized_keys.
//   - path.credential_file      ~/.aws/credentials, ~/.kube/config, ~/.netrc, ...
//   - path.dotenv               .env or .env.* (excluding an explicitly
//     configured safe fixture path).
//   - path.denied               user-configured denied_paths or globs.
func rulePath(a *analysis, p Policy, cwd string) []Finding {
	// Path rules are split across two rule families:
	//   - DangerousCommands controls system_write and denied_path.
	//   - SecretLeak controls ssh_private_key, credential_file, and
	//     dotenv (these are secret-extraction paths, not destructive
	//     command paths). The previous implementation gated all path
	//     rules on DangerousCommands.Enabled, which meant disabling
	//     destructive-command checks also silently disabled SSH/credential
	//     protection even when SecretLeak was still enabled.
	dangerousEnabled := p.Rules.DangerousCommands.Enabled
	secretEnabled := p.Rules.SecretLeak.Enabled
	if !dangerousEnabled && !secretEnabled {
		return nil
	}
	var out []Finding
	seen := map[string]bool{}
	add := func(f Finding) {
		key := f.RuleID + "|" + f.Evidence
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, f)
	}

	for _, op := range a.PathOps {
		evaluatePathOpWithCwd(op, p, cwd, dangerousEnabled, secretEnabled, add)
	}

	// Best-effort: scan the raw source for ssh/credential patterns when
	// parsing failed and secret leak is enabled.
	if secretEnabled && a.Pipeline == nil && a.Source != "" {
		evaluateRawSourcePaths(a.Source, p, add)
	}

	return out
}

// isRelativePath returns true when p is a relative path (does not start
// with /, ~, or a drive letter).
func isRelativePath(p string) bool {
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") {
		return false
	}
	// Windows drive letter.
	if len(p) >= 2 && p[1] == ':' {
		return false
	}
	return true
}

// evaluatePathOpWithCwd checks one path op against all path rules,
// combining cwd with relative tokens before checking.
func evaluatePathOpWithCwd(
	op pathOp, p Policy, cwd string,
	dangerousEnabled, secretEnabled bool,
	add func(Finding),
) {
	normalized := normalizePath(op.Token)
	joined := normalized
	if cwd != "" && isRelativePath(normalized) {
		joined = filepath.Clean(filepath.Join(cwd, normalized))
	}

	if dangerousEnabled {
		addDangerousPathFindings(op, normalized, joined, p, add)
	}
	if secretEnabled {
		addSecretPathFindings(op, normalized, joined, p, add)
	}
}

// addDangerousPathFindings checks system_write and denied paths.
func addDangerousPathFindings(
	op pathOp, normalized, joined string, p Policy,
	add func(Finding),
) {
	if (op.Op == "delete" || op.Op == "write") &&
		(isRootOrSystemPath(normalized) || isRootOrSystemPath(joined)) {
		add(Finding{
			RuleID:         "path.system_write",
			RiskLevel:      RiskCritical,
			Decision:       ruleDecision(p.Rules.DangerousCommands.Action, RiskCritical, p),
			Evidence:       "system path target: " + redactedPath(normalized),
			Recommendation: "Refuse writes or deletes to system paths; scope operations to the workspace",
		})
	}
	if matchesDeniedPath(normalized, p) || matchesDeniedPath(joined, p) {
		add(Finding{
			RuleID:         "path.denied",
			RiskLevel:      RiskHigh,
			Decision:       ruleDecision(p.Rules.DangerousCommands.Action, RiskHigh, p),
			Evidence:       "denied path pattern: " + redactedPath(normalized),
			Recommendation: "Remove the denied path from the command or update the policy explicitly",
		})
	}
}

// addSecretPathFindings checks ssh, credential, and dotenv paths.
func addSecretPathFindings(
	op pathOp, normalized, joined string, p Policy,
	add func(Finding),
) {
	if isSSHPath(normalized) || isSSHPath(joined) || isSSHRelativePath(normalized) {
		add(Finding{
			RuleID:         "path.ssh_private_key",
			RiskLevel:      RiskCritical,
			Decision:       ruleDecision(p.Rules.SecretLeak.Action, RiskCritical, p),
			Evidence:       "ssh private key or config path pattern",
			Recommendation: "Never read or transmit SSH private keys; require an explicit operator-approved workflow",
		})
	}
	if isCredentialPath(normalized) || isCredentialPath(joined) || isCredentialRelativePath(normalized) {
		add(Finding{
			RuleID:         "path.credential_file",
			RiskLevel:      RiskCritical,
			Decision:       ruleDecision(p.Rules.SecretLeak.Action, RiskCritical, p),
			Evidence:       "credential file path pattern",
			Recommendation: "Never read or transmit credential files; require an explicit operator-approved workflow",
		})
	}
	if isDotenvPath(normalized) || isDotenvPath(joined) {
		add(Finding{
			RuleID:         "path.dotenv",
			RiskLevel:      RiskHigh,
			Decision:       ruleDecision(p.Rules.SecretLeak.Action, RiskHigh, p),
			Evidence:       "dotenv file path pattern",
			Recommendation: "Do not read .env files in tool calls; inject allowed env vars through the policy whitelist",
		})
	}
}

// isSSHRelativePath returns true for bare relative SSH path forms like
// `.ssh/id_rsa`, `.ssh/config`, etc. These are credential paths
// regardless of whether they are qualified with ~ or an absolute home.
func isSSHRelativePath(p string) bool {
	if p == "" {
		return false
	}
	low := strings.ToLower(p)
	if strings.HasPrefix(low, ".ssh/") {
		return true
	}
	return false
}

// isCredentialRelativePath returns true for bare relative credential
// path forms like `.aws/credentials`, `.kube/config`, `.netrc`, etc.
func isCredentialRelativePath(p string) bool {
	if p == "" {
		return false
	}
	low := strings.ToLower(p)
	switch {
	case strings.HasPrefix(low, ".aws/") && strings.Contains(low, "credentials"):
		return true
	case strings.HasPrefix(low, ".kube/") && strings.Contains(low, "config"):
		return true
	case low == ".netrc":
		return true
	case low == ".git-credentials":
		return true
	case low == ".npmrc":
		return true
	case low == ".pypirc":
		return true
	}
	return false
}

// evaluatePathOp inspects one path-like token and emits findings for
// system writes, ssh keys, credential files, dotenv, and user-configured
// denied paths.
func evaluatePathOp(op pathOp, p Policy, add func(Finding)) {
	token := op.Token
	normalized := normalizePath(token)

	if (op.Op == "delete" || op.Op == "write") && isRootOrSystemPath(normalized) {
		add(Finding{
			RuleID:         "path.system_write",
			RiskLevel:      RiskCritical,
			Decision:       ruleDecision(p.Rules.DangerousCommands.Action, RiskCritical, p),
			Evidence:       "system path target: " + redactedPath(normalized),
			Recommendation: "Refuse writes or deletes to system paths; scope operations to the workspace",
		})
	}
	if isSSHPath(normalized) {
		add(Finding{
			RuleID:         "path.ssh_private_key",
			RiskLevel:      RiskCritical,
			Decision:       ruleDecision(p.Rules.SecretLeak.Action, RiskCritical, p),
			Evidence:       "ssh private key or config path pattern",
			Recommendation: "Never read or transmit SSH private keys; require an explicit operator-approved workflow",
		})
	}
	if isCredentialPath(normalized) {
		add(Finding{
			RuleID:         "path.credential_file",
			RiskLevel:      RiskCritical,
			Decision:       ruleDecision(p.Rules.SecretLeak.Action, RiskCritical, p),
			Evidence:       "credential file path pattern",
			Recommendation: "Never read or transmit credential files; require an explicit operator-approved workflow",
		})
	}
	if isDotenvPath(normalized) {
		add(Finding{
			RuleID:         "path.dotenv",
			RiskLevel:      RiskHigh,
			Decision:       ruleDecision(p.Rules.SecretLeak.Action, RiskHigh, p),
			Evidence:       "dotenv file path pattern",
			Recommendation: "Do not read .env files in tool calls; inject allowed env vars through the policy whitelist",
		})
	}
	if matchesDeniedPath(normalized, p) {
		add(Finding{
			RuleID:         "path.denied",
			RiskLevel:      RiskHigh,
			Decision:       ruleDecision(p.Rules.DangerousCommands.Action, RiskHigh, p),
			Evidence:       "denied path pattern: " + redactedPath(normalized),
			Recommendation: "Remove the denied path from the command or update the policy explicitly",
		})
	}
}

// evaluateRawSourcePaths scans the raw source for ssh/credential/dotenv
// patterns when shellsafe parsing failed. We accept some false-positive
// risk on unparsable commands because they are already high-risk.
func evaluateRawSourcePaths(src string, p Policy, add func(Finding)) {
	low := strings.ToLower(src)
	if strings.Contains(low, "~/.ssh") || strings.Contains(low, "id_rsa") ||
		strings.Contains(low, "authorized_keys") {
		add(Finding{
			RuleID:         "path.ssh_private_key",
			RiskLevel:      RiskCritical,
			Decision:       ruleDecision(p.Rules.SecretLeak.Action, RiskCritical, p),
			Evidence:       "ssh private key reference in unparsed command",
			Recommendation: "Never read or transmit SSH private keys",
		})
	}
	if strings.Contains(low, ".aws/credentials") ||
		strings.Contains(low, ".kube/config") ||
		strings.Contains(low, ".netrc") ||
		strings.Contains(low, ".git-credentials") {
		add(Finding{
			RuleID:         "path.credential_file",
			RiskLevel:      RiskCritical,
			Decision:       ruleDecision(p.Rules.SecretLeak.Action, RiskCritical, p),
			Evidence:       "credential file reference in unparsed command",
			Recommendation: "Never read or transmit credential files",
		})
	}
	if strings.Contains(low, ".env") {
		add(Finding{
			RuleID:         "path.dotenv",
			RiskLevel:      RiskHigh,
			Decision:       ruleDecision(p.Rules.SecretLeak.Action, RiskHigh, p),
			Evidence:       "dotenv reference in unparsed command",
			Recommendation: "Do not read .env files in tool calls",
		})
	}
}

// normalizePath converts ~ and \ separators to a canonical forward-slash
// form. It does NOT call os.Stat or follow symlinks; it is purely lexical.
func normalizePath(p string) string {
	if p == "" {
		return p
	}
	if p == "~" {
		return "~"
	}
	if strings.HasPrefix(p, "~/") {
		return "~" + p[1:]
	}
	return filepath.ToSlash(p)
}

// redactedPath returns a redacted representation of p for use in
// evidence. It preserves the path category and basename but redacts any
// secret-like content. The previous implementation returned the path
// as-is, which violated the "No raw secret is persisted" contract when
// a path token contained an embedded secret (e.g.
// `/etc/API_KEY=sk_live_1234567890abcdef1234`).
func redactedPath(p string) string {
	redacted, _ := redactString(p)
	return redacted
}

// isSSHPath returns true for ~/.ssh, ~/.ssh/*, private-key basenames, and
// authorized_keys.
func isSSHPath(p string) bool {
	if p == "" {
		return false
	}
	low := strings.ToLower(p)
	if low == "~/.ssh" || strings.HasPrefix(low, "~/.ssh/") {
		return true
	}
	base := path.Base(low)
	switch base {
	case "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519",
		"authorized_keys", "known_hosts":
		return true
	}
	if strings.HasSuffix(low, ".pem") || strings.HasSuffix(low, ".key") {
		return true
	}
	return false
}

// isCredentialPath returns true for cloud/VCS credential files, runtime
// secret mounts, and absolute home-directory credential paths.
func isCredentialPath(p string) bool {
	if p == "" {
		return false
	}
	low := strings.ToLower(p)
	if isTildeCredentialPath(low) {
		return true
	}
	if isAbsoluteHomeCredentialPath(low) {
		return true
	}
	if isRuntimeSecretPath(low) {
		return true
	}
	return false
}

// isTildeCredentialPath checks ~/.aws, ~/.kube, ~/.docker, ~/.netrc, etc.
func isTildeCredentialPath(low string) bool {
	switch {
	case low == "~/.aws" || strings.HasPrefix(low, "~/.aws/"):
		return strings.Contains(low, "credentials")
	case low == "~/.kube" || strings.HasPrefix(low, "~/.kube/"):
		return strings.Contains(low, "config")
	case low == "~/.docker/config.json":
		return true
	case low == "~/.netrc":
		return true
	case strings.HasSuffix(low, "/.git-credentials"):
		return true
	case strings.HasSuffix(low, "/.npmrc"):
		return true
	case strings.HasSuffix(low, "/.pypirc"):
		return true
	}
	return false
}

// isAbsoluteHomeCredentialPath checks /home/<user>/.aws/credentials, etc.
func isAbsoluteHomeCredentialPath(low string) bool {
	if strings.HasPrefix(low, "/home/") {
		return strings.Contains(low, "/.aws/credentials") ||
			strings.Contains(low, "/.ssh/") ||
			strings.Contains(low, "/.kube/config")
	}
	if strings.HasPrefix(low, "/users/") {
		return strings.Contains(low, "/.aws/credentials") ||
			strings.Contains(low, "/.ssh/")
	}
	return false
}

// isRuntimeSecretPath checks /run/secrets/*, /var/run/secrets/*, /proc/*/environ.
func isRuntimeSecretPath(low string) bool {
	if strings.HasPrefix(low, "/run/secrets/") || strings.HasPrefix(low, "/var/run/secrets/") {
		return true
	}
	if strings.HasPrefix(low, "/proc/") && strings.HasSuffix(low, "/environ") {
		return true
	}
	return false
}

// isDotenvPath returns true for .env and .env.* files.
func isDotenvPath(p string) bool {
	if p == "" {
		return false
	}
	low := strings.ToLower(p)
	base := path.Base(low)
	if base == ".env" {
		return true
	}
	if strings.HasPrefix(base, ".env.") {
		return true
	}
	return false
}

// matchesDeniedPath returns true when normalized matches any exact
// denied_paths entry or denied_path_globs doublestar pattern, or when
// normalized is a descendant of a denied path. The descendant check is
// critical: /etc/passwd must be denied by a denied_paths entry for
// /etc, and ~/.ssh/id_rsa must be denied by a denied_paths entry for
// ~/.ssh. The previous exact-match-only implementation allowed both.
func matchesDeniedPath(normalized string, p Policy) bool {
	clean := filepath.Clean(normalized)
	for _, e := range p.DeniedPaths {
		entry := filepath.Clean(normalizePath(e))
		if clean == entry {
			return true
		}
		// Descendant check: clean is inside entry.
		if isDescendant(clean, entry) {
			return true
		}
	}
	for _, pattern := range p.DeniedPathGlobs {
		ok, err := doublestar.Match(pattern, clean)
		if err == nil && ok {
			return true
		}
		// Also try with a leading **/ for ~-rooted patterns.
		if strings.HasPrefix(pattern, "~/") {
			alt := "**" + pattern[1:]
			if ok, err := doublestar.Match(alt, clean); err == nil && ok {
				return true
			}
		}
	}
	return false
}

// isDescendant returns true when target is inside dir. Both must be
// cleaned paths. /etc/passwd is a descendant of /etc; /etc is not a
// descendant of /etc/passwd.
func isDescendant(target, dir string) bool {
	if dir == "" || dir == "." {
		return false
	}
	if dir == "/" {
		return target != "" && target != "/"
	}
	// Ensure dir has a trailing separator so /etcfoo is not treated as
	// a descendant of /etc.
	prefix := dir
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return strings.HasPrefix(target, prefix)
}
