//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
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
	"regexp"
	"strings"
)

// networkChecker validates that any network requests in the command
// target whitelisted domains and never blacklisted domains.
type networkChecker struct {
	policy *Policy
}

func (c *networkChecker) Name() string { return "network" }

// urlRe matches URLs in command strings — both scheme-prefixed
// (https://host/path) and bare domains (domain.tld/path).
var urlRe = regexp.MustCompile(
	`https?://[a-zA-Z0-9][^\s` + "`\"'" + `]*|` +
		`(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}(?:/[^\s` + "`\"'" + `]*)?`,
)

// networkCommands is the set of executables that commonly make network
// requests. The checker only activates when one of these is detected.
var networkCommands = map[string]bool{
	"curl": true, "wget": true, "nc": true, "ncat": true,
	"netcat": true, "ssh": true, "scp": true, "rsync": true,
	"ftp": true, "sftp": true, "http": true, "https": true,
	"telnet": true, "dig": true, "nslookup": true, "host": true,
}

func (c *networkChecker) Check(ctx context.Context, req *ScanRequest) (*CheckResult, error) {
	if len(c.policy.Network.Whitelist) == 0 && len(c.policy.Network.Blacklist) == 0 {
		return nil, nil
	}

	text := req.Command
	if len(req.Args) > 0 {
		text += " " + strings.Join(req.Args, " ")
	}

	if !containsNetworkCommand(text) {
		return nil, nil
	}

	urls := urlRe.FindAllString(text, -1)
	for _, rawURL := range urls {
		host := extractHost(rawURL)
		if host == "" || !strings.Contains(host, ".") {
			continue
		}
		// Skip hosts that look like filenames (common extensions).
		if looksLikeFilename(host) {
			continue
		}

		if matchDomain(host, c.policy.Network.Blacklist) {
			return &CheckResult{
				Decision:       DecisionDeny,
				RiskLevel:      RiskCritical,
				RuleID:         "NET_DOMAIN_BLACKLISTED",
				Evidence:       rawURL,
				Recommendation: fmt.Sprintf("Domain %s is blacklisted. Use an approved domain instead.", host),
			}, nil
		}

		if len(c.policy.Network.Whitelist) > 0 && !matchDomain(host, c.policy.Network.Whitelist) {
			return &CheckResult{
				Decision:       DecisionDeny,
				RiskLevel:      RiskCritical,
				RuleID:         "NET_DOMAIN_NOT_WHITELISTED",
				Evidence:       rawURL,
				Recommendation: fmt.Sprintf("Domain %s is not in the whitelist. Add it to the network whitelist to allow.", host),
			}, nil
		}
	}

	return nil, nil
}

func containsNetworkCommand(text string) bool {
	lower := strings.ToLower(text)
	for cmd := range networkCommands {
		if strings.Contains(lower, cmd) {
			return true
		}
	}
	return false
}

func extractHost(rawURL string) string {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Hostname()
}

// fileExtensionSuffixes lists common file extensions that should not
// be treated as domains when matched by the bare-domain regex alternative.
var fileExtensionSuffixes = []string{
	".tar.gz", ".tar.bz2", ".tar.xz", ".tar", ".zip", ".gz",
	".yaml", ".yml", ".json", ".xml", ".toml",
	".log", ".txt", ".csv", ".md", ".rst",
	".env", ".git", ".hg",
	".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico",
	".exe", ".dll", ".so", ".dylib",
	".deb", ".rpm", ".apk",
}

func looksLikeFilename(host string) bool {
	host = strings.ToLower(host)
	for _, ext := range fileExtensionSuffixes {
		if strings.HasSuffix(host, ext) {
			return true
		}
	}
	return false
}

func matchDomain(host string, domains []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if host == d {
			return true
		}
		if strings.HasPrefix(d, "*.") {
			if strings.HasSuffix(host, d[1:]) {
				return true
			}
		}
		if strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}
