// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"net/url"
	"regexp"
	"strings"
)

var urlLikeRE = regexp.MustCompile(`(?i)\bhttps?://[^\s'"]+`)

func (s *Scanner) scanNetworkArgv(argv []string, loc string) []Finding {
	cmd := normalizeCommandName(argv[0])
	isNet := cmd == "curl" || cmd == "wget" || cmd == "nc" ||
		cmd == "netcat" || cmd == "ssh" || cmd == "scp" ||
		strings.Contains(cmd, "download")
	if !isNet {
		for _, arg := range argv[1:] {
			if urlLikeRE.MatchString(arg) {
				isNet = true
				break
			}
		}
	}
	if !isNet {
		return nil
	}
	domains := domainsFromArgs(argv)
	if len(domains) == 0 {
		return []Finding{finding(
			RuleNetworkDeniedDomain, CategoryNetwork, RiskHigh, DecisionAsk,
			"network-capable command without a policy-whitelisted domain: "+cmd,
			loc,
			"Specify an allowed domain or avoid outbound network access.",
		)}
	}
	var findings []Finding
	for _, domain := range domains {
		switch {
		case domainMatches(domain, s.policy.DeniedNetworkDomains):
			findings = append(findings, finding(
				RuleNetworkDeniedDomain, CategoryNetwork, RiskHigh, DecisionDeny,
				"network domain is explicitly denied: "+domain,
				loc,
				"Use an approved internal mirror or remove network access.",
			))
		case domainMatches(domain, s.policy.AllowedNetworkDomains):
			findings = append(findings, finding(
				RuleNetworkAllowedDomain, CategoryNetwork, RiskLow, DecisionAllow,
				"network domain is allowed by policy: "+domain,
				loc,
				"Proceed under the configured network whitelist.",
			))
		default:
			findings = append(findings, finding(
				RuleNetworkDeniedDomain, CategoryNetwork, RiskHigh, DecisionDeny,
				"network domain is not whitelisted: "+domain,
				loc,
				"Add the domain to allowed_network_domains only after review.",
			))
		}
	}
	return findings
}

func domainsFromArgs(argv []string) []string {
	var domains []string
	for _, arg := range argv[1:] {
		for _, raw := range urlLikeRE.FindAllString(arg, -1) {
			u, err := url.Parse(raw)
			if err == nil && u.Hostname() != "" {
				domains = append(domains, strings.ToLower(u.Hostname()))
			}
		}
		if strings.Contains(arg, "@") && !strings.Contains(arg, "://") {
			parts := strings.Split(arg, "@")
			host := parts[len(parts)-1]
			host = strings.Trim(host, "/:")
			if host != "" && strings.Contains(host, ".") {
				domains = append(domains, strings.ToLower(host))
			}
		}
	}
	return cleanStrings(domains)
}

func domainMatches(domain string, patterns []string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	for _, pattern := range patterns {
		p := strings.ToLower(strings.TrimSpace(pattern))
		if p == "" {
			continue
		}
		if p == domain {
			return true
		}
		if strings.HasPrefix(p, "*.") && strings.HasSuffix(domain, strings.TrimPrefix(p, "*")) {
			return true
		}
	}
	return false
}
