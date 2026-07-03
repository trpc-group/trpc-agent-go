//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"regexp"
	"strings"
)

// combineInput merges Command and CodeBlocks content into a single
// lower-cased string for unified scanning, preventing CodeBlocks-only
// payloads from bypassing the scanner.
func combineInput(input ScanInput) string {
	var parts []string
	if input.Command != "" {
		parts = append(parts, input.Command)
	}
	for _, cb := range input.CodeBlocks {
		if cb.Code != "" {
			parts = append(parts, cb.Code)
		}
	}
	return strings.ToLower(strings.Join(parts, " "))
}

// isRecursiveForceRm reports whether cmd (already lower-cased and
// trimmed) is an invocation of `rm` carrying the recursive and force
// flags in any recognised form. The detector accepts both short
// ("-rf", "-fr", "-Rf", "-rfi") and long ("--recursive --force",
// "--force --recursive", or the single-token "--recursive-force")
// variants, and treats any rm that targets a filesystem root
// ("/", "~", ".", "*") as destructive regardless of those flags.
//
// Returns the matched evidence (a normalised form) and whether the
// invocation is destructive. The helper exists so DangerousCommandRule
// can recognise the rm family uniformly; previously each variant
// needed its own substring entry, which left several common spellings
// (notably "rm -fr /" and "rm --recursive --force /") uncaught.
func isRecursiveForceRm(cmd string) (string, bool) {
	c := strings.ToLower(strings.TrimSpace(cmd))
	// Find the first `rm` token. Anything before it (sudo, /usr/bin, env
	// assignments, ...) is a prefix and ignored.
	rmIdx := indexOfToken(c, "rm")
	if rmIdx < 0 {
		return "", false
	}
	// Walk every subsequent token and collect flags / operands.
	rest := c[rmIdx+2:]
	hasRecursive := false
	hasForce := false
	for _, tok := range tokenizeShim(rest) {
		switch {
		case tok == "-r" || tok == "-R" || tok == "--recursive":
			hasRecursive = true
		case tok == "-f" || tok == "--force":
			hasForce = true
		case strings.HasPrefix(tok, "-") && !strings.HasPrefix(tok, "--"):
			// Short-option cluster like "-rf", "-fr", "-rfv", "-Rfi".
			// Each character in the cluster counts independently so
			// "rm -fr" matches without enumerating every order.
			for _, ch := range tok[1:] {
				if ch == 'r' {
					hasRecursive = true
				}
				if ch == 'f' {
					hasForce = true
				}
			}
		case strings.HasPrefix(tok, "--"):
			// Long-form combined flags like "--recursive-force".
			if strings.Contains(tok, "recursive") {
				hasRecursive = true
			}
			if strings.Contains(tok, "force") {
				hasForce = true
			}
		}
	}
	if !hasRecursive || !hasForce {
		return "", false
	}
	return "rm -rf", true
}

// indexOfToken returns the byte index of the first occurrence of
// needle in s when both are surrounded by non-alphanumeric boundaries,
// or -1 if no such token exists. Matching is case-insensitive.
func indexOfToken(s, needle string) int {
	lowerS := strings.ToLower(s)
	lowerN := strings.ToLower(needle)
	nLen := len(lowerN)
	for i := 0; i+nLen <= len(lowerS); i++ {
		if lowerS[i:i+nLen] != lowerN {
			continue
		}
		// Boundary on the left.
		if i > 0 {
			prev := lowerS[i-1]
			if isAlnumByte(prev) || prev == '_' || prev == '-' {
				continue
			}
		}
		// Boundary on the right.
		end := i + nLen
		if end < len(lowerS) {
			next := lowerS[end]
			if isAlnumByte(next) || next == '_' || next == '-' {
				continue
			}
		}
		return i
	}
	return -1
}

// isAlnumByte reports whether b is an ASCII letter or digit.
func isAlnumByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

// tokenizeShim splits a shell-like argument list on whitespace. It is
// intentionally a minimal shim - quoted args are kept as a single
// token with the surrounding quotes removed - because the rm detector
// only needs to distinguish flag tokens from operand tokens. A real
// shell tokenizer would be overkill and would import a dependency.
func tokenizeShim(s string) []string {
	var out []string
	var cur strings.Builder
	inSingle := false
	inDouble := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case (c == ' ' || c == '\t' || c == '\n') && !inSingle && !inDouble:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// ---------- Rule 1: Dangerous Command Detection ----------

// DangerousCommandRule checks for explicitly dangerous commands.
//
// It detects:
//   - Destructive filesystem operations (rm -rf, format, dd)
//   - Reading sensitive files (~/.ssh, /etc/passwd, .env)
//   - Commands that leak credentials (cat ~/.aws/credentials)
type DangerousCommandRule struct {
	// dangerousCommands is the deny list of explicitly dangerous command
	// prefixes (case-insensitive substring match).
	dangerousCommands []string
	// sensitivePaths is the deny list of sensitive file paths; a read or
	// modify against one of these paths is treated as a critical risk.
	sensitivePaths []string
}

// NewDangerousCommandRule creates a rule with the default deny list.
func NewDangerousCommandRule() *DangerousCommandRule {
	return defaultDangerousCommandRule()
}

// NewDangerousCommandRuleWithPolicy builds a DangerousCommandRule whose
// dangerousCommands and sensitivePaths are taken from p in addition to the
// built-in defaults.
//
// The built-in deny lists are always kept: a partial YAML that omits
// "rm -rf /" or "/etc/shadow" cannot silently disable those checks, which
// would otherwise be a fail-open posture for a security component. Entries
// from p are appended to (and de-duplicated against) the built-in lists so
// policy authors can extend the deny surface without re-stating defaults.
//
// Network client keywords (curl, wget, ssh, ...) must NOT be placed in
// p.DeniedCommands, because the dangerous-command rule fires before
// NetworkAccessRule has a chance to evaluate AllowedDomains. New policy
// files should use p.DangerousCommandDeny for destructive commands and
// p.NetworkClientDeny for network clients; the constructor reads
// p.DangerousCommandDeny first and falls back to p.DeniedCommands only
// when DangerousCommandDeny is empty (for backward compatibility with
// pre-split policy files).
//
// A nil p is treated as "no policy", so the constructor behaves identically
// to NewDangerousCommandRule in that case.
func NewDangerousCommandRuleWithPolicy(p *PolicyFile) *DangerousCommandRule {
	r := defaultDangerousCommandRule()
	if p == nil {
		return r
	}
	// Prefer the explicit DangerousCommandDeny; fall back to the legacy
	// DeniedCommands only when DangerousCommandDeny is empty. This is the
	// single source of the "split by rule" semantics called out in the
	// policy-aware review on PR #2044.
	cmdSource := p.DangerousCommandDeny
	if len(cmdSource) == 0 {
		cmdSource = p.DeniedCommands
	}
	r.dangerousCommands = mergeUnique(r.dangerousCommands, cmdSource)
	r.sensitivePaths = mergeUnique(r.sensitivePaths, p.DeniedPaths)
	return r
}

// defaultDangerousCommandRule returns the canonical rule with the built-in
// deny list. It is the single source of truth shared by the public
// constructors so a future tightening of the default list lands in both
// NewDangerousCommandRule and NewDangerousCommandRuleWithPolicy.
func defaultDangerousCommandRule() *DangerousCommandRule {
	return &DangerousCommandRule{
		dangerousCommands: []string{
			"rm -rf /", "rm -rf ~", "rm -rf .", "rm -rf /*",
			"dd if=", "mkfs.", "fdisk",
			"shutdown", "reboot", "halt", "poweroff", "init 0", "init 6",
			"chmod 777 /", "chown -R", "setfacl -R",
		},
		sensitivePaths: []string{
			".ssh/id_rsa", ".ssh/authorized", ".aws/credentials", ".gcloud/",
			"credentials.json",
			".env", ".env.", "/etc/shadow", "/etc/passwd",
			".pem", ".key", ".p12", ".pfx", "id_ed25519",
			".sql", ".dump", "backup",
		},
	}
}

// ID returns the unique identifier of this rule.
func (r *DangerousCommandRule) ID() string { return "danger_cmd_001" }

// Check inspects the input for dangerous command keywords and sensitive path access.
func (r *DangerousCommandRule) Check(input ScanInput) *ScanResult {
	// combineInput is lower-cased; the rm detector is case-insensitive so
	// it works on the already-normalised text. We still re-toLower here
	// for safety in case combineInput ever changes its policy.
	if ev, ok := isRecursiveForceRm(strings.ToLower(combineInput(input))); ok {
		return &ScanResult{
			Decision:  DecisionDeny,
			RiskLevel: RiskCritical,
			RuleID:    r.ID(),
			Evidence:  ev,
			Reason:    "dangerous command: " + ev + " (any ordering of -r/-f)",
		}
	}
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	for _, kw := range r.dangerousCommands {
		if strings.Contains(cmd, kw) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskCritical,
				RuleID:    r.ID(),
				Evidence:  kw,
				Reason:    "dangerous command: " + kw,
			}
		}
	}
	for _, p := range r.sensitivePaths {
		if strings.Contains(cmd, p) {
			if strings.Contains(cmd, "cat ") || strings.Contains(cmd, "less ") ||
				strings.Contains(cmd, "head ") || strings.Contains(cmd, "tail ") ||
				strings.Contains(cmd, "grep ") || strings.Contains(cmd, "cp ") {
				return &ScanResult{
					Decision:  DecisionDeny,
					RiskLevel: RiskCritical,
					RuleID:    r.ID(),
					Evidence:  p,
					Reason:    "reading sensitive file: " + p,
				}
			}
			if strings.Contains(cmd, "rm ") || strings.Contains(cmd, "mv ") ||
				strings.Contains(cmd, ">") {
				return &ScanResult{
					Decision:  DecisionDeny,
					RiskLevel: RiskCritical,
					RuleID:    r.ID(),
					Evidence:  p,
					Reason:    "modifying/deleting sensitive file: " + p,
				}
			}
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskHigh,
				RuleID:    r.ID(),
				Evidence:  p,
				Reason:    "accessing sensitive path: " + p,
			}
		}
	}
	return nil
}

// ---------- Rule 2: Network Access Detection ----------

// NetworkAccessRule detects commands that connect to external networks.
//
// It supports an optional allow list of domains: when the rule fires and the
// command's URL/host matches an entry in the allow list, the rule returns
// DecisionAsk instead of DecisionDeny so a human can approve the call.
type NetworkAccessRule struct {
	// dangerousCmds is the set of command names that perform outbound
	// network access. Matching is case-insensitive substring.
	dangerousCmds []string
	// allowedDomains downgrades a match to DecisionAsk when the target
	// host is in this list. Supports wildcard "*.example.com" entries.
	allowedDomains []string
}

// NewNetworkAccessRule creates a network access detection rule
// with an empty allow list (deny-by-default for any detected network access).
func NewNetworkAccessRule() *NetworkAccessRule {
	return &NetworkAccessRule{
		dangerousCmds: []string{
			"curl", "wget", "nc ", "ncat", "telnet",
			"ssh ", "scp ", "sftp", "rsync",
			"nslookup", "dig ", "host ", "nmap", "socat",
			"git clone", "pip install", "npm install",
			// Common HTTP client APIs in code blocks. CodeBlocks are
			// evaluated together with Command by combineInput, so these
			// let the rule catch payloads like
			// `urllib.request.urlopen(...)` or `requests.post(...)` that
			// pure substring matching on "curl" / "wget" would miss.
			"urllib.request", "urllib.urlopen",
			"requests.get", "requests.post", "requests.put",
			"requests.delete", "requests.patch",
			"httpx.get", "httpx.post", "httpx.put",
			"http.client", "httplib",
			"socket.connect", "socket.create_connection",
			"socket.socket", "ssl.wrap_socket",
			"aiohttp.client", "http.client.httpconnection",
		},
	}
}

// NewNetworkAccessRuleWithAllowlist creates a NetworkAccessRule that downgrades
// matches to DecisionAsk when the target domain is in the allow list.
//
// Each entry in allowedDomains may be a bare host ("github.com") or a wildcard
// like "*.example.com". Comparison is case-insensitive.
func NewNetworkAccessRuleWithAllowlist(allowedDomains []string) *NetworkAccessRule {
	r := NewNetworkAccessRule()
	r.allowedDomains = allowedDomains
	return r
}

// NewNetworkAccessRuleWithPolicy builds a NetworkAccessRule that uses
// p.AllowedDomains as the allow list (downgrading matches to DecisionAsk
// when the target host is in the list). The built-in dangerousCmds set is
// preserved unchanged.
//
// The keyword deny list is taken from p.NetworkClientDeny (new, preferred)
// or p.DeniedCommands (legacy, only when NetworkClientDeny is empty). This
// split is what lets a policy like:
//
//	denied_commands: [rm -rf, sudo, ...]   # destructive only
//	network_client_deny: [curl, wget, ...] # outbound only
//	allowed_domains: [github.com]
//
// produce a DecisionAsk for `curl https://github.com/...` instead of the
// previous silent deny caused by the dangerous-command rule shadowing the
// allow list. See the policy-aware review on PR #2044 for context.
//
// A nil p is treated as "no policy", so the constructor behaves identically
// to NewNetworkAccessRule in that case. The wildcard prefix "*." is honored
// by the underlying matchesAllowlist helper.
func NewNetworkAccessRuleWithPolicy(p *PolicyFile) *NetworkAccessRule {
	r := NewNetworkAccessRule()
	if p == nil {
		return r
	}
	r.allowedDomains = append([]string(nil), p.AllowedDomains...)
	cliSource := p.NetworkClientDeny
	if len(cliSource) == 0 {
		cliSource = p.DeniedCommands
	}
	if len(cliSource) > 0 {
		r.dangerousCmds = mergeUnique(r.dangerousCmds, cliSource)
	}
	return r
}

// WithAllowedDomains sets the allow list on an existing rule.
func (r *NetworkAccessRule) WithAllowedDomains(domains []string) *NetworkAccessRule {
	r.allowedDomains = domains
	return r
}

// mergeUnique returns the union of base and extra, preserving order and
// dropping exact-match duplicates. Comparison is case-insensitive so that
// "Curl" and "curl" do not both appear in the merged deny list. Nil/empty
// inputs are tolerated on both sides.
func mergeUnique(base, extra []string) []string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make([]string, 0, len(base)+len(extra))
	seen := make(map[string]struct{}, len(base)+len(extra))
	push := func(s string) {
		key := strings.ToLower(strings.TrimSpace(s))
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	for _, s := range base {
		push(s)
	}
	for _, s := range extra {
		push(s)
	}
	return out
}

// ID returns the unique identifier of this rule.
func (r *NetworkAccessRule) ID() string { return "network_002" }

// Check inspects the input for network access keywords.
//
// If a keyword matches, the rule inspects the command for any URL/host
// arguments. When the host is in the configured allow list, the rule returns
// DecisionAsk (human review) instead of DecisionDeny.
func (r *NetworkAccessRule) Check(input ScanInput) *ScanResult {
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	for _, kw := range r.dangerousCmds {
		if !strings.Contains(cmd, kw) {
			continue
		}
		// If a host in the command matches the allow list, downgrade to ask.
		if r.matchesAllowlist(cmd) {
			return &ScanResult{
				Decision:  DecisionAsk,
				RiskLevel: RiskMedium,
				RuleID:    r.ID(),
				Evidence:  kw,
				Reason:    "network access to allowlisted host: " + kw,
			}
		}
		return &ScanResult{
			Decision:  DecisionDeny,
			RiskLevel: RiskHigh,
			RuleID:    r.ID(),
			Evidence:  kw,
			Reason:    "network access: " + kw,
		}
	}
	return nil
}

// parseHosts extracts the host portion of every URL / scp-like target / bare
// hostname it can find in cmd. Matching is intentionally conservative:
// we only return a host if we are confident we found a real network target,
// never a substring of a longer word. This is the only correct way to feed
// the allow list; substring matching (e.g. "github.com" inside "evilgithub.com"
// or "evil.com/?next=github.com") is exactly the failure mode called out in
// the policy-aware review on PR #2044.
//
// Recognized shapes:
//
//	https://user@host:port/path?query
//	http://host/path
//	ssh://user@host:port
//	git@host:user/repo         (scp-style)
//	user@host:path             (scp-style)
//	host:port                  (bare host:port, e.g. "nc evil.com 4444")
//
// Anything that does not match one of these shapes is ignored; the caller
// treats the absence of any parsed host as "no allow-listed target found",
// which falls through to a deny for the network call.
func parseHosts(cmd string) []string {
	lower := strings.ToLower(cmd)
	var hosts []string
	appendHost := func(h string) {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			return
		}
		// Strip an optional :port tail. We only do this for the bare-host
		// case to avoid clobbering scheme://user@host:port URLs (those are
		// already split by the scheme parser above).
		if i := strings.LastIndex(h, ":"); i > 0 && i < len(h)-1 {
			// Only strip if the part after the colon looks numeric (port).
			tail := h[i+1:]
			allDigits := true
			for j := 0; j < len(tail); j++ {
				if tail[j] < '0' || tail[j] > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				h = h[:i]
			}
		}
		// Strip a trailing dot (FQDN canonical form).
		h = strings.TrimSuffix(h, ".")
		hosts = append(hosts, h)
	}
	// (1) http://, https://, ssh://, ftp://, file://, ws://, wss://.
	schemeRe := regexp.MustCompile(`[a-z][a-z0-9+.\-]*://(?:[^/@\s]+@)?([a-z0-9._\-]+)`)
	for _, m := range schemeRe.FindAllStringSubmatch(lower, -1) {
		if len(m) > 1 {
			appendHost(m[1])
		}
	}
	// (2) scp-style "user@host:path" (no scheme). Bound the user part to
	// non-space, non-@ characters and the host to non-colon, non-space,
	// non-/ characters. We require the trailing ":path" to disambiguate
	// from a bare "user@host" without a path.
	scpRe := regexp.MustCompile(`(?:^|[\s])([a-z0-9._\-]+)@([a-z0-9.\-]+):[^\s]`)
	for _, m := range scpRe.FindAllStringSubmatch(lower, -1) {
		if len(m) > 2 {
			appendHost(m[2])
		}
	}
	// (3) bare "host:port" (e.g. "nc evil.com 4444", "telnet host 23").
	// We require the host to be a valid DNS label and the tail to be a
	// numeric port. The surrounding context is bounded by whitespace or
	// start/end-of-string so that "host:path" scp-style above is not
	// double-counted.
	// "host:port" (e.g. "nc evil.com:4444") AND "host port" with whitespace
	// (e.g. "nc github.com 443"). Both are valid invocations of netcat /
	// telnet / curl-against-bare-host. We require the host to look like a
	// DNS label and the port to be numeric so we do not mistake path
	// segments for ports.
	bareRe := regexp.MustCompile(`(?:^|[\s])([a-z0-9][a-z0-9.\-]*)[ :](\d+)(?:[\s]|$)`)
	for _, m := range bareRe.FindAllStringSubmatch(lower, -1) {
		if len(m) > 2 {
			appendHost(m[1])
		}
	}
	return hosts
}

// hostMatchesPattern reports whether host is an exact match for pattern,
// or a valid subdomain of pattern when pattern starts with "*.". The
// match is performed on the lower-cased forms. Substring matching is
// intentionally rejected: "evilgithub.com" must not match "github.com",
// and "github.com.evil.com" must not match "github.com" either. Hosts
// are extracted by parseHosts, which guarantees they are full DNS labels,
// not free-form URL fragments.
func hostMatchesPattern(host, pattern string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if host == "" || pattern == "" {
		return false
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		return strings.HasSuffix(host, suffix) && len(host) > len(suffix)
	}
	// Exact match OR valid subdomain match (host ends with
	// "."+pattern). The dot prefix is mandatory so "evilgithub.com" does
	// not match "github.com" while "api.github.com" does.
	if host == pattern {
		return true
	}
	return strings.HasSuffix(host, "."+pattern)
}

// matchesAllowlist reports whether any host in cmd is in the configured
// allow list. Hosts are extracted by parseHosts, and each host is matched
// against every pattern with hostMatchesPattern (exact match or valid
// "*.example.com" subdomain). Substring matching has been removed because
// it accepted "evilgithub.com" against "github.com" and similar evasion
// patterns called out in the policy-aware review on PR #2044.
func (r *NetworkAccessRule) matchesAllowlist(cmd string) bool {
	if len(r.allowedDomains) == 0 {
		return false
	}
	hosts := parseHosts(cmd)
	if len(hosts) == 0 {
		return false
	}
	for _, pattern := range r.allowedDomains {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		for _, h := range hosts {
			if hostMatchesPattern(h, pattern) {
				return true
			}
		}
	}
	return false
}

// ---------- Rule 3: Shell Bypass Detection ----------

// ShellBypassRule detects attempts to bypass shell safety restrictions
// by using -c flags, eval, or other indirect execution methods.
type ShellBypassRule struct {
	// bypassPatterns is the deny list of shell-bypass tokens and flags
	// ("sh -c", "eval ", "base64 -d", ...). A match is critical risk
	// because it indicates an attempt to escape the safe-execution
	// surface.
	bypassPatterns []string
}

// NewShellBypassRule creates a shell bypass detection rule.
func NewShellBypassRule() *ShellBypassRule {
	return &ShellBypassRule{
		bypassPatterns: []string{
			"sh -c", "bash -c", "zsh -c",
			"python -c", "python3 -c",
			"perl -e", "ruby -e", "node -e",
			"eval ", "exec ", "source ", "xargs ",
			"env ", "sudo ", "su ",
			"base64 -d", "xxd -r",
			"/dev/tcp/", "/dev/udp/",
		},
	}
}

// ID returns the unique identifier of this rule.
func (r *ShellBypassRule) ID() string { return "shell_bypass_003" }

// Check inspects the input for shell bypass patterns.
func (r *ShellBypassRule) Check(input ScanInput) *ScanResult {
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	for _, p := range r.bypassPatterns {
		if strings.Contains(cmd, p) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskHigh,
				RuleID:    r.ID(),
				Evidence:  p,
				Reason:    "shell bypass attempt: " + p,
			}
		}
	}
	return nil
}

// ---------- Rule 4: Install and System Mutation Detection ----------

// InstallAndMutateRule detects package manager installs and system config changes.
type InstallAndMutateRule struct {
	// patterns is the deny list of install / system-mutation tokens
	// ("apt install", "systemctl enable", "iptables ", ...).
	patterns []string
}

// NewInstallAndMutateRule creates an install/mutation detection rule.
func NewInstallAndMutateRule() *InstallAndMutateRule {
	return &InstallAndMutateRule{patterns: []string{
		"apt install", "apt-get install", "apt-get update",
		"yum install", "dnf install",
		"pacman -S", "brew install",
		"npm install", "npm i ",
		"pip install", "pip3 install",
		"go install", "go get ",
		"gem install", "cargo install",
		"snap install", "flatpak install",
		"systemctl enable", "systemctl start",
		"service start", "service enable",
		"update-rc.d", "crontab ",
		"iptables ", "nft ",
	}}
}

// ID returns the unique identifier of this rule.
func (r *InstallAndMutateRule) ID() string { return "install_004" }

// Check inspects the input for install or system mutation patterns.
func (r *InstallAndMutateRule) Check(input ScanInput) *ScanResult {
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	for _, p := range r.patterns {
		if strings.Contains(cmd, p) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskMedium,
				RuleID:    r.ID(),
				Evidence:  p,
				Reason:    "install or system mutation: " + p,
			}
		}
	}
	return nil
}

// ---------- Rule 5: Host Execution Risk Detection ----------

// HostExecRiskRule detects host-level operations that only apply to the local executor.
type HostExecRiskRule struct {
	// risks is the deny list of host-execution risk tokens ("mount ",
	// "chmod 777", "setuid", ...). The rule is a no-op for non-local
	// executors because these tokens do not apply to containerized
	// runtimes.
	risks []string
}

// NewHostExecRiskRule creates a host execution risk detection rule.
func NewHostExecRiskRule() *HostExecRiskRule {
	return &HostExecRiskRule{risks: []string{
		"tty", "pty",
		"nohup ", "disown", "bg ", "fg ",
		"daemon", "fork",
		"sudo ", "su -", "su root",
		"chmod 777", "chmod -R 777",
		"chown root", "chown :root",
		"setuid", "setgid",
		"insmod ", "modprobe ", "rmmod ",
		"mount ", "umount ",
	}}
}

// ID returns the unique identifier of this rule.
func (r *HostExecRiskRule) ID() string { return "hostexec_005" }

// Check inspects the input for host execution risk keywords. Skipped for container executors.
func (r *HostExecRiskRule) Check(input ScanInput) *ScanResult {
	if input.ExecutorType != "local" && input.ExecutorType != "" {
		return nil
	}
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	for _, risk := range r.risks {
		if strings.Contains(cmd, risk) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskCritical,
				RuleID:    r.ID(),
				Evidence:  risk,
				Reason:    "host execution risk: " + risk,
			}
		}
	}
	return nil
}

// ---------- Rule 6: Resource Abuse Detection ----------

// ResourceAbuseRule detects resource exhaustion patterns such as infinite loops
// and fork bombs.
type ResourceAbuseRule struct{}

// NewResourceAbuseRule creates a resource abuse detection rule.
func NewResourceAbuseRule() *ResourceAbuseRule { return &ResourceAbuseRule{} }

// ID returns the unique identifier of this rule.
func (r *ResourceAbuseRule) ID() string { return "resource_006" }

// Check inspects the input for resource abuse patterns.
func (r *ResourceAbuseRule) Check(input ScanInput) *ScanResult {
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	for _, lp := range []string{"while true", "while :", "for (( ; ; ))", "while [ 1 ]", "while [[ 1 ]]"} {
		if strings.Contains(cmd, lp) {
			return &ScanResult{Decision: DecisionDeny, RiskLevel: RiskHigh, RuleID: r.ID(), Evidence: lp, Reason: "infinite loop: " + lp}
		}
	}
	for _, fp := range []string{":(){ :|:& };:", "() {"} {
		if strings.Contains(cmd, fp) {
			return &ScanResult{Decision: DecisionDeny, RiskLevel: RiskCritical, RuleID: r.ID(), Evidence: fp, Reason: "fork bomb pattern"}
		}
	}
	for _, rc := range []string{"stress ", "stress-ng", "yes ", "dd if=/dev/zero of=", ">/dev/null", ": >", "sha256sum /dev/zero", "md5sum /dev/zero"} {
		if strings.Contains(cmd, rc) {
			return &ScanResult{Decision: DecisionDeny, RiskLevel: RiskHigh, RuleID: r.ID(), Evidence: rc, Reason: "resource exhaustion: " + rc}
		}
	}
	return nil
}

// ---------- Rule 7: Sensitive Information Leak Detection ----------

// SensitiveInfoLeakRule detects patterns that may leak credentials or sensitive data to files.
type SensitiveInfoLeakRule struct {
	// patterns is the list of credential / secret key names ("api_key",
	// "password", "bearer", "jwt", ...). The rule only fires when one of
	// these names appears together with a write intent ("echo ... >"),
	// preventing false positives on plain reads.
	patterns []string
}

// NewSensitiveInfoLeakRule creates a sensitive info leak detection rule.
func NewSensitiveInfoLeakRule() *SensitiveInfoLeakRule {
	return &SensitiveInfoLeakRule{patterns: []string{
		"api_key", "apikey", "api_secret", "apisecret",
		"access_key", "secret_key", "private_key", "privatekey",
		"password", "passwd", "passphrase",
		"db_password", "db_pass",
		"token", "bearer", "jwt",
		"auth_token", "refresh_token",
	}}
}

// ID returns the unique identifier of this rule.
func (r *SensitiveInfoLeakRule) ID() string { return "leak_007" }

// Check inspects the input for sensitive information leakage patterns.
func (r *SensitiveInfoLeakRule) Check(input ScanInput) *ScanResult {
	all := combineInput(input)
	if all == "" {
		return nil
	}
	for _, p := range r.patterns {
		if !strings.Contains(all, p) {
			continue
		}
		if (strings.Contains(all, "echo ") || strings.Contains(all, "cat ") ||
			strings.Contains(all, "printf ")) && strings.Contains(all, ">") {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskCritical,
				RuleID:    r.ID(),
				Evidence:  p,
				Reason:    "sensitive info leak: " + p,
			}
		}
	}
	return nil
}

// ---------- Rule 8: Human Review (ask decision) ----------

// AskForReviewRule returns DecisionAsk for commands that are
// potentially risky but may have legitimate use cases.
type AskForReviewRule struct {
	// patterns is the list of tokens that should escalate to human review
	// ("rm -r", "git push", "kubectl delete", ...).
	patterns []string
}

// NewAskForReviewRule creates a rule that returns ask for risky-but-legitimate commands.
func NewAskForReviewRule() *AskForReviewRule {
	return &AskForReviewRule{patterns: []string{
		"rm -r", "git push", "docker push",
		"kubectl delete", "drop table", "truncate ",
		"force",
	}}
}

// ID returns the unique identifier of this rule.
func (r *AskForReviewRule) ID() string { return "ask_review_008" }

// Check inspects the input for risky-but-legitimate commands requiring human review.
func (r *AskForReviewRule) Check(input ScanInput) *ScanResult {
	cmd := combineInput(input)
	if cmd == "" {
		return nil
	}
	for _, p := range r.patterns {
		if strings.Contains(cmd, p) {
			return &ScanResult{
				Decision:  DecisionAsk,
				RiskLevel: RiskMedium,
				RuleID:    r.ID(),
				Evidence:  p,
				Reason:    "requires human review: " + p,
			}
		}
	}
	return nil
}

// ---------- Rule 9: Parse Failure (fail-closed) ----------

// ParseFailureRule denies any command that the shellsafe parser
// rejects. This is the "fail-closed" half of the safety contract:
// a structurally unsafe command (variable expansion, command
// substitution, subshells, ...) must never reach the executor even
// if no other rule fires.
//
// Empty input is treated as "no rule applies" and returns nil, so
// non-command code blocks (e.g. pure documentation snippets) are
// not penalised.
type ParseFailureRule struct{}

// NewParseFailureRule creates a rule that fails closed on parser errors.
func NewParseFailureRule() *ParseFailureRule { return &ParseFailureRule{} }

// ID returns the unique identifier of this rule.
func (r *ParseFailureRule) ID() string { return "parse_fail_009" }

// Check parses the input.Command via shellsafe. On any parse error
// the rule returns DecisionDeny with RiskCritical.
func (r *ParseFailureRule) Check(input ScanInput) *ScanResult {
	cmd := strings.TrimSpace(input.Command)
	if cmd == "" {
		return nil
	}
	if _, err := ParseCommand(cmd); err != nil {
		return &ScanResult{
			Decision:  DecisionDeny,
			RiskLevel: RiskCritical,
			RuleID:    r.ID(),
			Evidence:  err.Error(),
			Reason:    "unparsable command (shellsafe rejected): " + err.Error(),
		}
	}
	return nil
}

// ---------- Rule 10: Shell Wrapper Detection (structural) ----------

// ShellWrapperRule denies commands whose pipeline starts with a
// shell wrapper / re-executing builtin (sh, bash, sudo, xargs,
// eval, ...). The check is structural - it inspects argv[0] of
// every segment via the shellsafe parser rather than scanning the
// raw command text, so encoded / wrapped forms ("$(echo sh)",
// "/usr/bin/SH", "sh.exe", "${X}sh") cannot smuggle past.
//
// Empty input is treated as "no rule applies" and returns nil.
type ShellWrapperRule struct{}

// NewShellWrapperRule creates a rule that denies shell wrappers.
func NewShellWrapperRule() *ShellWrapperRule { return &ShellWrapperRule{} }

// ID returns the unique identifier of this rule.
func (r *ShellWrapperRule) ID() string { return "shell_wrapper_010" }

// Check parses the input.Command via shellsafe and rejects any
// segment whose argv[0] is a known shell wrapper.
//
// If the command cannot be parsed at all, the rule returns nil so it
// does not double-report the failure (that is ParseFailureRule's job).
// The fail-closed behaviour is still preserved because the parser
// will emit a separate deny.
func (r *ShellWrapperRule) Check(input ScanInput) *ScanResult {
	cmd := strings.TrimSpace(input.Command)
	if cmd == "" {
		return nil
	}
	parsed, err := ParseCommand(cmd)
	if err != nil {
		return nil
	}
	for _, seg := range parsed.Segments {
		if len(seg) == 0 {
			continue
		}
		if IsShellWrapper(seg[0]) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskCritical,
				RuleID:    r.ID(),
				Evidence:  seg[0],
				Reason:    "shell wrapper detected: " + seg[0],
			}
		}
	}
	return nil
}
