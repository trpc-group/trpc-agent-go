//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// ---------------------------------------------------------------------------
// R-NET-001  NetworkEgressRule
// ---------------------------------------------------------------------------

// NetworkEgressRule detects network access to non-whitelisted domains.
// Rule ID: R-NET-001.
type NetworkEgressRule struct{}

// ID returns the rule identifier "R-NET-001".
func (r *NetworkEgressRule) ID() string { return "R-NET-001" }

// Name returns the human-readable rule name.
func (r *NetworkEgressRule) Name() string { return "Network Egress" }

// networkToolPatterns maps tool names to a flag indicating they always
// imply network access (even without an explicit URL argument).
var networkToolsDirect = map[string]bool{
	"nc": true, "ncat": true, "netcat": true,
	"ssh": true, "scp": true, "rsync": true,
	"aria2c": true, "axel": true, "lftp": true,
}

// urlBearingTools maps tool names to a set of flags that carry URLs or
// hostnames as the next argument rather than as positional arguments.
var urlBearingToolFlags = map[string]map[string]bool{
	"curl": {
		"--url": true, "-K": true, "--config": true,
		"--resolve": true, "--connect-to": true, "--proxy": true, "-x": true,
	},
	"wget": {
		"-O": true, "--output-document": true,
		"-P": true, "--directory-prefix": true,
	},
}

// pythonNetPatterns detects common Python HTTP-client calls in code blocks.
var pythonNetRe = regexp.MustCompile(
	`(?:urllib\.request\.urlopen|requests\.(?:get|post|put|delete|patch|head)|http\.client\.)`,
)

// urlRe extracts http/https URLs from arbitrary text.
var urlRe = regexp.MustCompile(`https?://[^\s'"<>]+`)

// hostFromArg interprets an argument as a host or URL and returns the
// hostname. If the argument looks like host:port it returns the host
// portion; if it is a full URL it returns the URL hostname.
func hostFromArg(arg string) string {
	// Try URL parse first.
	if strings.Contains(arg, "://") {
		u, err := url.Parse(arg)
		if err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
	}
	// host:port form.
	if h, _, ok := strings.Cut(arg, ":"); ok && h != "" {
		return h
	}
	return arg
}

// extractCurlHosts extracts target hostnames from curl arguments, taking
// into account flags that redirect output or rewrite DNS resolution.
func extractCurlHosts(args []string) ([]string, []Finding) {
	var hosts []string
	var findings []Finding
	skipNext := false
	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if finding, consumesNext, matched := curlConfigFinding(arg); matched {
			findings = append(findings, finding)
			skipNext = consumesNext
			continue
		}
		if finding, consumesNext, matched := curlResolveFinding(arg, i, len(args)); matched {
			findings = append(findings, finding)
			skipNext = consumesNext
			continue
		}
		if host, consumesNext, finding, matched := curlConnectTarget(arg, i, args); matched {
			if host != "" {
				hosts = append(hosts, host)
			}
			if finding != nil {
				findings = append(findings, *finding)
			}
			skipNext = consumesNext
			continue
		}
		if host, consumesNext, finding, matched := curlProxyHost(arg, i, args); matched {
			if host != "" {
				hosts = append(hosts, host)
			}
			if finding != nil {
				findings = append(findings, *finding)
			}
			skipNext = consumesNext
			continue
		}
		if host, consumesNext, matched := curlURLHost(arg, i, args); matched {
			if host != "" {
				hosts = append(hosts, host)
			}
			skipNext = consumesNext
			continue
		}
		if curlSkipsNextArg(arg) {
			skipNext = true
			continue
		}
		if host, ok := curlPositionalHost(arg); ok {
			hosts = append(hosts, host)
		}
	}
	return hosts, findings
}

func curlConfigFinding(arg string) (Finding, bool, bool) {
	if arg == "-K" || arg == "--config" {
		return unscannedCurlConfigFinding(arg), true, true
	}
	if strings.HasPrefix(arg, "--config=") || (strings.HasPrefix(arg, "-K") && len(arg) > 2) {
		return unscannedCurlConfigFinding(arg), false, true
	}
	return Finding{}, false, false
}

func curlResolveFinding(arg string, index, argc int) (Finding, bool, bool) {
	if arg == "--resolve" && index+1 < argc {
		return curlResolveOverrideFinding(), true, true
	}
	if strings.HasPrefix(arg, "--resolve=") {
		return curlResolveOverrideFinding(), false, true
	}
	return Finding{}, false, false
}

func curlURLHost(arg string, index int, args []string) (string, bool, bool) {
	if arg == "--url" && index+1 < len(args) {
		return hostFromArg(args[index+1]), true, true
	}
	if strings.HasPrefix(arg, "--url=") {
		return hostFromArg(strings.TrimPrefix(arg, "--url=")), false, true
	}
	return "", false, false
}

func curlConnectTarget(arg string, index int, args []string) (string, bool, *Finding, bool) {
	if arg == "--connect-to" {
		if index+1 >= len(args) {
			finding := curlMalformedOverrideFinding("--connect-to")
			return "", false, &finding, true
		}
		host, err := connectToHost(args[index+1])
		if err != nil {
			finding := curlMalformedOverrideFinding("--connect-to")
			return "", true, &finding, true
		}
		return host, true, nil, true
	}
	if strings.HasPrefix(arg, "--connect-to=") {
		host, err := connectToHost(strings.TrimPrefix(arg, "--connect-to="))
		if err != nil {
			finding := curlMalformedOverrideFinding("--connect-to")
			return "", false, &finding, true
		}
		return host, false, nil, true
	}
	return "", false, nil, false
}

func curlProxyHost(arg string, index int, args []string) (string, bool, *Finding, bool) {
	switch {
	case arg == "--proxy" || arg == "-x":
		if index+1 >= len(args) {
			finding := curlMalformedOverrideFinding(arg)
			return "", false, &finding, true
		}
		return hostFromArg(args[index+1]), true, nil, true
	case strings.HasPrefix(arg, "--proxy="):
		return hostFromArg(strings.TrimPrefix(arg, "--proxy=")), false, nil, true
	case strings.HasPrefix(arg, "-x") && len(arg) > 2:
		return hostFromArg(arg[2:]), false, nil, true
	default:
		return "", false, nil, false
	}
}

func curlSkipsNextArg(arg string) bool {
	flags, ok := urlBearingToolFlags["curl"]
	return ok && flags[arg]
}

func curlPositionalHost(arg string) (string, bool) {
	if strings.HasPrefix(arg, "-") {
		return "", false
	}
	host := hostFromArg(arg)
	if host == "" {
		return "", false
	}
	return host, true
}

func unscannedCurlConfigFinding(arg string) Finding {
	return Finding{
		RuleID:         "R-NET-001",
		RuleName:       "Network Egress",
		RiskLevel:      RiskLevelHigh,
		Decision:       DecisionDeny,
		Evidence:       "curl " + arg + " uses an unscanned config file",
		Recommendation: "Inline the curl URL arguments so the guard can scan the actual endpoint.",
	}
}

func curlResolveOverrideFinding() Finding {
	return Finding{
		RuleID:         "R-NET-001",
		RuleName:       "Network Egress",
		RiskLevel:      RiskLevelHigh,
		Decision:       DecisionDeny,
		Evidence:       "curl --resolve overrides the destination endpoint",
		Recommendation: "Remove --resolve or require explicit human review for endpoint overrides.",
	}
}

func curlMalformedOverrideFinding(flag string) Finding {
	return Finding{
		RuleID:         "R-NET-001",
		RuleName:       "Network Egress",
		RiskLevel:      RiskLevelHigh,
		Decision:       DecisionDeny,
		Evidence:       "curl " + flag + " is malformed and cannot be safely scanned",
		Recommendation: "Provide a complete endpoint override argument or remove the override.",
	}
}

func connectToHost(value string) (string, error) {
	parts := strings.Split(value, ":")
	if len(parts) < 4 {
		return "", fmt.Errorf("invalid --connect-to value")
	}
	host := strings.TrimSpace(parts[2])
	if host == "" {
		return "", fmt.Errorf("invalid --connect-to value")
	}
	return host, nil
}

// extractWgetHosts extracts target hostnames from wget arguments.
func extractWgetHosts(args []string) []string {
	var hosts []string
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if flags, ok := urlBearingToolFlags["wget"]; ok {
			if flags[arg] {
				skipNext = true
				continue
			}
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		h := hostFromArg(arg)
		if h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

// extractSSHHosts extracts target hostnames from ssh/scp/rsync arguments.
// It recognizes both "user@host" and bare host arguments, as well as
// SCP source/destination syntax (user@host:path).
func extractSSHHosts(args []string) []string {
	var hosts []string
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(arg, "-") {
			// Flags like -p (port) take a value; skip the next arg.
			// Handle -p 22 and -p=22 forms.
			if arg == "-p" || arg == "-P" {
				skipNext = true
			}
			continue
		}
		// ssh user@host or scp user@host:path
		if h, ok := parseUserHost(arg); ok {
			hosts = append(hosts, h)
			continue
		}
		// SCP destination (no @-sign): host:path
		if colonIdx := strings.Index(arg, ":"); colonIdx > 0 {
			candidate := arg[:colonIdx]
			// Validate the candidate looks like a hostname (not a Windows path like C:).
			if !strings.Contains(candidate, "/") && candidate != "" {
				hosts = append(hosts, candidate)
				continue
			}
		}
		// Bare host argument (e.g. "ssh myhost" without user@).
		if arg != "" && !strings.Contains(arg, "/") && !strings.Contains(arg, "=") {
			hosts = append(hosts, arg)
		}
	}
	return hosts
}

// parseUserHost parses "user@host" or "user@host:path" and returns the host.
func parseUserHost(s string) (string, bool) {
	at := strings.Index(s, "@")
	if at < 0 {
		return "", false
	}
	rest := s[at+1:]
	colon := strings.Index(rest, ":")
	if colon >= 0 {
		rest = rest[:colon]
	}
	if rest == "" {
		return "", false
	}
	return rest, true
}

// domainMatchesAllowlist reports whether the domain is allowed by the
// NetworkAllowlist. An exact match or a subdomain match (e.g.
// "*.example.com" matches "sub.example.com") are both accepted.
// If the allowlist is empty, no domain is allowed.
func domainMatchesAllowlist(domain string, allowlist []string) bool {
	if len(allowlist) == 0 {
		return false
	}
	domain = strings.ToLower(domain)
	for _, pat := range allowlist {
		pat = strings.ToLower(pat)
		if pat == domain {
			return true
		}
		// Wildcard subdomain: *.example.com matches sub.example.com.
		if strings.HasPrefix(pat, "*.") {
			suffix := pat[1:] // ".example.com"
			if strings.HasSuffix(domain, suffix) && len(domain) > len(suffix) {
				return true
			}
		}
	}
	return false
}

// Scan evaluates the input for network egress policy violations.
func (r *NetworkEgressRule) Scan(_ context.Context, input ScanInput, policy PolicyFile) []Finding {
	var allHosts []string
	var findings []Finding
	networkIntent := false

	// Parse the command via shellsafe if possible.
	if input.Command != "" {
		pipe, err := parseCommandLoose(input.Command)
		if err == nil {
			for _, argv := range pipe.Commands {
				if len(argv) == 0 {
					continue
				}
				hosts, commandFindings := extractHostsFromCommand(argv[0], argv[1:])
				allHosts = append(allHosts, hosts...)
				findings = append(findings, commandFindings...)
				if len(hosts) > 0 || len(commandFindings) > 0 || isNetworkTool(argv[0]) {
					networkIntent = true
				}
			}
		} else {
			// Fallback: extract URLs from the raw command text.
			hosts := extractHostsFromText(input.Command)
			allHosts = append(allHosts, hosts...)
			networkIntent = networkIntent || len(hosts) > 0
		}
	}

	// Scan code blocks for URLs and Python HTTP patterns.
	text := normalizedScanText(input)
	textHosts := extractHostsFromText(text)
	allHosts = append(allHosts, textHosts...)
	networkIntent = networkIntent || len(textHosts) > 0

	// Detect Python HTTP calls in code blocks — produce a finding directly
	// since we cannot extract the specific URL from Python code.
	var pythonFindings []Finding
	for _, block := range input.CodeBlocks {
		if pythonNetRe.MatchString(block) {
			networkIntent = true
			pythonFindings = append(pythonFindings, Finding{
				RuleID:         r.ID(),
				RuleName:       r.Name(),
				RiskLevel:      RiskLevelHigh,
				Decision:       DecisionDeny,
				Evidence:       "Python HTTP client detected (unknown destination)",
				Recommendation: "Add the target domain to network_allowlist in the policy file, or remove the network access.",
			})
		}
	}

	if len(allHosts) == 0 && len(pythonFindings) == 0 {
		return nil
	}

	if networkIntent {
		allHosts = append(allHosts, extractProxyHostsFromEnv(input.Env)...)
	}

	// Evaluate each host against the allowlist.
	seen := make(map[string]struct{})
	for _, h := range allHosts {
		h = strings.ToLower(h)
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}

		if domainMatchesAllowlist(h, policy.NetworkAllowlist) {
			continue
		}
		findings = append(findings, Finding{
			RuleID:         r.ID(),
			RuleName:       r.Name(),
			RiskLevel:      RiskLevelHigh,
			Decision:       DecisionDeny,
			Evidence:       "network access to " + h,
			Recommendation: "Add the domain to network_allowlist in the policy file, or remove the network access.",
		})
	}

	// Append Python HTTP findings (these are not domain-based and always deny).
	findings = append(findings, pythonFindings...)

	return findings
}

func extractProxyHostsFromEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := []string{
		"http_proxy", "https_proxy", "all_proxy",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY",
	}
	hosts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(env[key])
		if value == "" {
			continue
		}
		if host := hostFromArg(value); host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// extractHostsFromCommand dispatches host extraction based on the tool name.
func extractHostsFromCommand(tool string, args []string) ([]string, []Finding) {
	switch normalizeExecutableName(tool) {
	case "curl":
		return extractCurlHosts(args)
	case "wget":
		return extractWgetHosts(args), nil
	case "ssh", "scp", "rsync":
		return extractSSHHosts(args), nil
	default:
		if networkToolsDirect[normalizeExecutableName(tool)] {
			// For nc/ncat/netcat, the first non-flag arg is typically host.
			for _, a := range args {
				if strings.HasPrefix(a, "-") {
					continue
				}
				h, _, ok := strings.Cut(a, ":")
				if ok && h != "" {
					return []string{h}, nil
				}
				if a != "" {
					return []string{a}, nil
				}
			}
			return []string{"unknown"}, nil
		}
		return nil, nil
	}
}

func normalizeExecutableName(tool string) string {
	tool = strings.TrimSpace(tool)
	tool = filepath.Base(strings.ReplaceAll(tool, `\`, `/`))
	return strings.ToLower(tool)
}

func isNetworkTool(tool string) bool {
	name := normalizeExecutableName(tool)
	if networkToolsDirect[name] {
		return true
	}
	_, ok := urlBearingToolFlags[name]
	return ok
}

// extractHostsFromText finds http/https URLs in raw text and returns their
// hostnames.
func extractHostsFromText(text string) []string {
	var hosts []string
	for _, m := range urlRe.FindAllString(text, -1) {
		u, err := url.Parse(m)
		if err != nil || u.Hostname() == "" {
			continue
		}
		hosts = append(hosts, u.Hostname())
	}
	return hosts
}

// parseCommandLoose is a thin wrapper around shellsafe.Parse that returns
// the pipeline on success or nil on any parse error.
func parseCommandLoose(cmd string) (*shellsafe.Pipeline, error) {
	return shellsafe.Parse(cmd)
}
