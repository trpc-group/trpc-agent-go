// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package checkers

import (
	"context"
	"net"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
)

// networkCommands lists programs that make network requests.
var networkCommands = []string{
	"curl", "wget", "nc", "ncat", "netcat",
	"ssh", "scp", "sftp",
	"ftp", "socat", "telnet",
}

// NetworkEgressChecker checks for network access to non-whitelisted domains.
type NetworkEgressChecker struct {
	allowedDomains []string
	defaultAction  string
}

// NewNetworkEgressChecker creates a checker from the given policy.
func NewNetworkEgressChecker(policy *toolsafety.SafetyPolicy) *NetworkEgressChecker {
	c := &NetworkEgressChecker{defaultAction: "deny"}
	if policy != nil && policy.NetworkPolicy != nil {
		c.allowedDomains = policy.NetworkPolicy.AllowedDomains
		if policy.NetworkPolicy.DefaultAction != "" {
			c.defaultAction = policy.NetworkPolicy.DefaultAction
		}
	}
	return c
}

// ID returns the checker identifier.
func (c *NetworkEgressChecker) ID() string { return "network_egress" }

// IsEnabled reports whether this checker is active.
func (c *NetworkEgressChecker) IsEnabled(policy *toolsafety.SafetyPolicy) bool {
	return policy != nil && policy.NetworkPolicy != nil
}

// Check runs the network egress check.
func (c *NetworkEgressChecker) Check(ctx context.Context, req *toolsafety.ScanRequest) ([]toolsafety.RiskFinding, error) {
	var findings []toolsafety.RiskFinding

	if req == nil || req.Command == "" {
		return findings, nil
	}

	cmd := strings.TrimSpace(req.Command)
	if !startsWithNetworkCommand(cmd) {
		return findings, nil
	}

	domain := extractDomain(cmd)
	if domain == "" {
		// Has a networking command but no domain — still flag it.
		findings = append(findings, toolsafety.RiskFinding{
			RuleID:         toolsafety.RuleNetworkUnauthorized,
			RiskLevel:      toolsafety.RiskLevelMedium,
			Evidence:       cmd,
			Recommendation: "Network command detected; ensure the target is trusted",
			SeverityScore:  5,
			MatchedPattern: "network_command",
		})
		return findings, nil
	}

	if c.isDomainAllowed(domain) {
		return findings, nil
	}

	rl := toolsafety.RiskLevelHigh
	rid := toolsafety.RuleNetworkUnauthorized

	findings = append(findings, toolsafety.RiskFinding{
		RuleID:         rid,
		RiskLevel:      rl,
		Evidence:       cmd,
		Recommendation: "Network access to " + domain + " is not in the allowed domains list",
		SeverityScore:  8,
		MatchedPattern: "domain:" + domain,
	})

	return findings, nil
}

func startsWithNetworkCommand(cmd string) bool {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return false
	}
	base := fields[0]
	for _, nc := range networkCommands {
		if strings.EqualFold(base, nc) || strings.EqualFold(base, "/usr/bin/"+nc) || strings.EqualFold(base, "/bin/"+nc) {
			return true
		}
	}
	return false
}

func extractDomain(cmd string) string {
	fields := strings.Fields(cmd)
	for _, f := range fields {
		f = strings.Trim(f, "'\"")
		if strings.HasPrefix(f, "http://") || strings.HasPrefix(f, "https://") {
			f = strings.TrimPrefix(f, "https://")
			f = strings.TrimPrefix(f, "http://")
			if idx := strings.IndexAny(f, "/:"); idx >= 0 {
				f = f[:idx]
			}
			return f
		}
	}
	return ""
}

func (c *NetworkEgressChecker) isDomainAllowed(domain string) bool {
	for _, allowed := range c.allowedDomains {
		if strings.EqualFold(domain, allowed) {
			return true
		}
		if strings.HasPrefix(allowed, "*.") {
			suffix := strings.TrimPrefix(allowed, "*")
			if strings.HasSuffix(domain, suffix) {
				return true
			}
		}
	}
	// Also check if it's a private/internal IP.
	if ip := net.ParseIP(domain); ip != nil {
		if ip.IsPrivate() || ip.IsLoopback() {
			return true
		}
	}
	return false
}
