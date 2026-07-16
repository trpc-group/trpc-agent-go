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
	"net/url"
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

func (r *NetworkEgressRule) ID() string   { return "R-NET-001" }
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
		"--resolve": true,
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
	if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
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
func extractCurlHosts(args []string) []string {
	var hosts []string
	skipNext := false
	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		// --resolve flag rewrites DNS but the original host is still accessed.
		if arg == "--resolve" && i+1 < len(args) {
			// --resolve takes "host:port:addr" — the original host is the
			// first component before the first colon.
			parts := strings.SplitN(args[i+1], ":", 2)
			if parts[0] != "" {
				hosts = append(hosts, parts[0])
			}
			skipNext = true
			continue
		}
		// Handle --url flag: its value may be a bare host or a full URL.
		if arg == "--url" && i+1 < len(args) {
			h := hostFromArg(args[i+1])
			if h != "" {
				hosts = append(hosts, h)
			}
			skipNext = true
			continue
		}
		// Handle --url=value form.
		if strings.HasPrefix(arg, "--url=") {
			val := strings.TrimPrefix(arg, "--url=")
			h := hostFromArg(val)
			if h != "" {
				hosts = append(hosts, h)
			}
			continue
		}
		// Flags that take a value but are not URLs.
		if flags, ok := urlBearingToolFlags["curl"]; ok {
			if flags[arg] {
				skipNext = true
				continue
			}
			// Handle --flag=value form for non-URL flags.
			for f := range flags {
				if strings.HasPrefix(arg, f+"=") {
					break
				}
			}
		}
		// Positional argument that looks like a URL or host.
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

	// Parse the command via shellsafe if possible.
	if input.Command != "" {
		pipe, err := parseCommandLoose(input.Command)
		if err == nil {
			for _, argv := range pipe.Commands {
				if len(argv) == 0 {
					continue
				}
				tool := argv[0]
				hosts := extractHostsFromCommand(tool, argv[1:])
				allHosts = append(allHosts, hosts...)
			}
		} else {
			// Fallback: extract URLs from the raw command text.
			allHosts = append(allHosts, extractHostsFromText(input.Command)...)
		}
	}

	// Scan code blocks for URLs and Python HTTP patterns.
	text := normalizedScanText(input)
	allHosts = append(allHosts, extractHostsFromText(text)...)

	// Detect Python HTTP calls in code blocks — produce a finding directly
	// since we cannot extract the specific URL from Python code.
	var pythonFindings []Finding
	for _, block := range input.CodeBlocks {
		if pythonNetRe.MatchString(block) {
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

	// Evaluate each host against the allowlist.
	var findings []Finding
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

// extractHostsFromCommand dispatches host extraction based on the tool name.
func extractHostsFromCommand(tool string, args []string) []string {
	switch tool {
	case "curl":
		return extractCurlHosts(args)
	case "wget":
		return extractWgetHosts(args)
	case "ssh", "scp", "rsync":
		return extractSSHHosts(args)
	default:
		if networkToolsDirect[tool] {
			// For nc/ncat/netcat, the first non-flag arg is typically host.
			for _, a := range args {
				if strings.HasPrefix(a, "-") {
					continue
				}
				h, _, ok := strings.Cut(a, ":")
				if ok && h != "" {
					return []string{h}
				}
				if a != "" {
					return []string{a}
				}
			}
			return []string{"unknown"}
		}
		return nil
	}
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
